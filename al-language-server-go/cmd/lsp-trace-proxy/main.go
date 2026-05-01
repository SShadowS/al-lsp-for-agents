// lsp-trace-proxy — single-purpose stdio interceptor for the Microsoft AL
// Language Server. Replaces Microsoft.Dynamics.Nav.EditorServices.Host.exe
// in a per-cell install of the AL extension. Spawns the renamed real
// binary (.exe.real) with our argv, pipes stdin/stdout verbatim, and
// appends one NDJSON record per LSP frame to AL_LSP_TRACE_FILE.
//
// Intended only for issue #17 wire-tracing — see docs/issue-17/.
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
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	args := os.Args[1:]

	selfPath, err := os.Executable()
	if err != nil {
		die("get executable: %v", err)
	}
	realPath := selfPath + ".real"
	if _, err := os.Stat(realPath); err != nil {
		die("real binary not found at %s: %v", realPath, err)
	}

	tracePath := os.Getenv("AL_LSP_TRACE_FILE")
	if tracePath == "" {
		tracePath = filepath.Join(os.TempDir(), "al-lsp-trace.ndjson")
	}
	if err := os.MkdirAll(filepath.Dir(tracePath), 0o755); err != nil {
		die("mkdir trace dir: %v", err)
	}
	traceFile, err := os.OpenFile(tracePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		die("open trace file %s: %v", tracePath, err)
	}
	defer traceFile.Close()

	var traceMu sync.Mutex

	writeTrace(traceFile, &traceMu, map[string]any{
		"ts":   nowRFC3339Nano(),
		"type": "banner",
		"real": realPath,
		"args": args,
		"pid":  os.Getpid(),
		"self": selfPath,
	})

	cmd := exec.Command(realPath, args...)
	cmd.Stderr = os.Stderr

	inPipe, err := cmd.StdinPipe()
	if err != nil {
		die("stdin pipe: %v", err)
	}
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		die("stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		die("start real: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// VS Code -> AL LS
	go func() {
		defer wg.Done()
		defer inPipe.Close()
		copyAndTrace(os.Stdin, inPipe, "in", traceFile, &traceMu)
	}()

	// AL LS -> VS Code
	go func() {
		defer wg.Done()
		copyAndTrace(outPipe, os.Stdout, "out", traceFile, &traceMu)
	}()

	wg.Wait()

	werr := cmd.Wait()
	exitCode := 0
	if werr != nil {
		if ee, ok := werr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			fmt.Fprintf(os.Stderr, "lsp-trace-proxy: wait: %v\n", werr)
			exitCode = 1
		}
	}

	writeTrace(traceFile, &traceMu, map[string]any{
		"ts":   nowRFC3339Nano(),
		"type": "exit",
		"code": exitCode,
	})

	os.Exit(exitCode)
}

// copyAndTrace reads framed LSP messages from src, writes each frame
// header+body verbatim to dst, and appends one NDJSON record per frame
// to trace. Stops on EOF or first I/O error.
func copyAndTrace(src io.Reader, dst io.Writer, dir string, trace *os.File, mu *sync.Mutex) {
	br := bufio.NewReaderSize(src, 1<<20)
	for {
		var headerBuf bytes.Buffer
		contentLength := -1

		for {
			line, err := br.ReadString('\n')
			if len(line) > 0 {
				headerBuf.WriteString(line)
			}
			if err != nil {
				if err == io.EOF && headerBuf.Len() == 0 {
					return
				}
				fmt.Fprintf(os.Stderr, "lsp-trace-proxy: read header (%s): %v\n", dir, err)
				if headerBuf.Len() > 0 {
					_, _ = dst.Write(headerBuf.Bytes())
				}
				return
			}
			trimmed := strings.TrimRight(line, "\r\n")
			if trimmed == "" {
				break
			}
			lower := strings.ToLower(trimmed)
			if strings.HasPrefix(lower, "content-length:") {
				v := strings.TrimSpace(trimmed[len("content-length:"):])
				if n, perr := strconv.Atoi(v); perr == nil {
					contentLength = n
				}
			}
		}

		if contentLength < 0 {
			fmt.Fprintf(os.Stderr, "lsp-trace-proxy: missing Content-Length (%s)\n", dir)
			if _, err := dst.Write(headerBuf.Bytes()); err != nil {
				return
			}
			continue
		}

		body := make([]byte, contentLength)
		if _, err := io.ReadFull(br, body); err != nil {
			fmt.Fprintf(os.Stderr, "lsp-trace-proxy: read body (%s): %v\n", dir, err)
			return
		}

		if _, err := dst.Write(headerBuf.Bytes()); err != nil {
			return
		}
		if _, err := dst.Write(body); err != nil {
			return
		}

		record := buildTraceRecord(dir, body, contentLength)
		writeTrace(trace, mu, record)
	}
}

func buildTraceRecord(dir string, body []byte, contentLength int) map[string]any {
	record := map[string]any{
		"ts":  nowRFC3339Nano(),
		"dir": dir,
		"len": contentLength,
	}

	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		record["raw"] = string(body)
		record["parse_error"] = err.Error()
		return record
	}

	if len(msg.ID) > 0 {
		record["id"] = msg.ID
	} else {
		record["id"] = nil
	}
	if msg.Method != "" {
		record["method"] = msg.Method
	} else {
		record["method"] = nil
	}
	if len(msg.Params) > 0 {
		record["params"] = msg.Params
	}
	if len(msg.Result) > 0 {
		record["result"] = msg.Result
	}
	if len(msg.Error) > 0 {
		record["error"] = msg.Error
	}
	return record
}

func writeTrace(f *os.File, mu *sync.Mutex, record map[string]any) {
	b, err := json.Marshal(record)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lsp-trace-proxy: marshal trace: %v\n", err)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	_, _ = f.Write(b)
	_, _ = f.Write([]byte("\n"))
}

func nowRFC3339Nano() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "lsp-trace-proxy: "+format+"\n", args...)
	os.Exit(2)
}
