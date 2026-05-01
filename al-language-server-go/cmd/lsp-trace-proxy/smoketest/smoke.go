// Smoke test: spawn lsp-trace-proxy with a fake "real" that echoes
// each incoming LSP frame back unchanged. Send 2 frames in,
// verify they come back, and verify the trace file contains 4 records
// (2 in, 2 out) plus the banner.
//
// Build proxy first; run from this dir:
//   go run smoke.go
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func main() {
	tmp, err := os.MkdirTemp("", "lsp-trace-smoke-")
	must(err)
	defer os.RemoveAll(tmp)

	// Build a fake "real" that echoes each frame back.
	fakeSrc := filepath.Join(tmp, "fake.go")
	must(os.WriteFile(fakeSrc, []byte(fakeRealSource), 0o644))

	fakeExe := filepath.Join(tmp, "fake-real.exe")
	if runtime.GOOS != "windows" {
		fakeExe = filepath.Join(tmp, "fake-real")
	}
	cmd := exec.Command("go", "build", "-o", fakeExe, fakeSrc)
	cmd.Stderr = os.Stderr
	must(cmd.Run())

	// Stage proxy and fake side-by-side: proxy.exe, proxy.exe.real
	proxySrc := "../lsp-trace-proxy.exe"
	if _, err := os.Stat(proxySrc); err != nil {
		fmt.Fprintf(os.Stderr, "proxy not built at %s\n", proxySrc)
		os.Exit(2)
	}
	proxyExe := filepath.Join(tmp, "proxy.exe")
	must(copyFile(proxySrc, proxyExe))
	must(copyFile(fakeExe, proxyExe+".real"))

	// Trace file
	tracePath := filepath.Join(tmp, "trace.ndjson")

	// Spawn proxy
	proxy := exec.Command(proxyExe, "--some-arg", "value")
	proxy.Env = append(os.Environ(), "AL_LSP_TRACE_FILE="+tracePath)
	proxy.Stderr = os.Stderr
	stdin, err := proxy.StdinPipe()
	must(err)
	stdout, err := proxy.StdoutPipe()
	must(err)
	must(proxy.Start())

	// Send 2 LSP frames
	frames := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///x.al","languageId":"al","version":1,"text":"codeunit 1 X{}"}}}`,
	}
	for _, body := range frames {
		writeFrame(stdin, body)
	}
	stdin.Close()

	// Read 2 frames back
	br := bufio.NewReader(stdout)
	for i := 0; i < 2; i++ {
		body, err := readFrame(br)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read frame %d: %v\n", i, err)
			os.Exit(1)
		}
		if string(body) != frames[i] {
			fmt.Fprintf(os.Stderr, "frame %d body mismatch:\nexpected: %s\ngot: %s\n", i, frames[i], string(body))
			os.Exit(1)
		}
	}

	// Wait for exit
	if err := proxy.Wait(); err != nil {
		fmt.Fprintf(os.Stderr, "proxy wait: %v\n", err)
		os.Exit(1)
	}

	// Validate trace
	traceBytes, err := os.ReadFile(tracePath)
	must(err)
	lines := strings.Split(strings.TrimRight(string(traceBytes), "\n"), "\n")

	var inCount, outCount int
	hasBanner := false
	hasExit := false
	for _, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			fmt.Fprintf(os.Stderr, "trace parse: %v\n%s\n", err, line)
			os.Exit(1)
		}
		switch rec["type"] {
		case "banner":
			hasBanner = true
		case "exit":
			hasExit = true
		}
		switch rec["dir"] {
		case "in":
			inCount++
		case "out":
			outCount++
		}
	}

	if !hasBanner || !hasExit || inCount != 2 || outCount != 2 {
		fmt.Fprintf(os.Stderr, "trace shape wrong: banner=%v exit=%v in=%d out=%d\n", hasBanner, hasExit, inCount, outCount)
		fmt.Fprintln(os.Stderr, string(traceBytes))
		os.Exit(1)
	}

	fmt.Printf("smoke OK: %d frames forwarded, %d trace records\n", inCount+outCount, len(lines))
	_ = time.Now() // silence unused if any
}

const fakeRealSource = `package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func main() {
	br := bufio.NewReader(os.Stdin)
	for {
		var cl int = -1
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				if err == io.EOF { return }
				panic(err)
			}
			t := strings.TrimRight(line, "\r\n")
			if t == "" { break }
			if strings.HasPrefix(strings.ToLower(t), "content-length:") {
				v := strings.TrimSpace(t[len("content-length:"):])
				n, _ := strconv.Atoi(v)
				cl = n
			}
		}
		if cl < 0 { return }
		body := make([]byte, cl)
		if _, err := io.ReadFull(br, body); err != nil { return }
		// Echo back
		fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n", cl)
		os.Stdout.Write(body)
	}
}
`

func writeFrame(w io.Writer, body string) {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := w.Write([]byte(header)); err != nil {
		panic(err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		panic(err)
	}
}

func readFrame(br *bufio.Reader) ([]byte, error) {
	var cl = -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		t := strings.TrimRight(line, "\r\n")
		if t == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(t), "content-length:") {
			v := strings.TrimSpace(t[len("content-length:"):])
			n, perr := strconv.Atoi(v)
			if perr != nil {
				return nil, perr
			}
			cl = n
		}
	}
	if cl < 0 {
		return nil, fmt.Errorf("no Content-Length")
	}
	body := make([]byte, cl)
	if _, err := io.ReadFull(br, body); err != nil {
		return nil, err
	}
	return body, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

var _ = bytes.NewReader
