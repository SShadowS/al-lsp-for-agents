package wrapper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SShadowS/al-lsp-for-agents/al-language-server-go/wrapper/telemetry"
)

func TestDownloadFailureEmitsEvent(t *testing.T) {
	dump := filepath.Join(t.TempDir(), "dump.jsonl")
	cli, err := telemetry.NewClient(telemetry.ClientConfig{
		ConnString: "InstrumentationKey=fake",
		DumpPath:   dump,
		Level:      telemetry.LevelErrors,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := telemetry.NewSession()
	cli.TrackDownloadFailure(s, "lookup", "dial tcp: lookup marketplace.visualstudio.com: no such host", 0, "marketplace.visualstudio.com")
	cli.WaitDrain(2 * time.Second)
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(raw))
	if line == "" {
		t.Fatalf("expected event emitted, dump file is empty")
	}
	var ev map[string]interface{}
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev["name"] != "download.failure" {
		t.Errorf("name = %v, want download.failure", ev["name"])
	}
	if ev["errorClass"] != "dns" {
		t.Errorf("errorClass = %v, want dns", ev["errorClass"])
	}
	if ev["stage"] != "lookup" {
		t.Errorf("stage = %v, want lookup", ev["stage"])
	}
}

func TestDownloadFailureHTTPStatus(t *testing.T) {
	dump := filepath.Join(t.TempDir(), "dump.jsonl")
	cli, err := telemetry.NewClient(telemetry.ClientConfig{
		ConnString: "InstrumentationKey=fake",
		DumpPath:   dump,
		Level:      telemetry.LevelErrors,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := telemetry.NewSession()
	cli.TrackDownloadFailure(s, "download", "download returned status 503", 503, "marketplace.visualstudio.com")
	cli.WaitDrain(2 * time.Second)
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(raw))
	if line == "" {
		t.Fatalf("expected event emitted, dump file is empty")
	}
	var ev map[string]interface{}
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev["name"] != "download.failure" {
		t.Errorf("name = %v, want download.failure", ev["name"])
	}
	if ev["errorClass"] != "http-status" {
		t.Errorf("errorClass = %v, want http-status", ev["errorClass"])
	}
	if ev["stage"] != "download" {
		t.Errorf("stage = %v, want download", ev["stage"])
	}
	// httpStatus should be present and equal 503
	httpStatus, ok := ev["httpStatus"].(float64)
	if !ok {
		t.Errorf("httpStatus missing or wrong type: %v", ev["httpStatus"])
	} else if int(httpStatus) != 503 {
		t.Errorf("httpStatus = %v, want 503", httpStatus)
	}
}

func TestHostOf(t *testing.T) {
	cases := []struct {
		rawURL string
		want   string
	}{
		{"https://marketplace.visualstudio.com/path", "marketplace.visualstudio.com"},
		{"https://marketplace.visualstudio.com/_apis/public/gallery/extensionquery", "marketplace.visualstudio.com"},
		{"not a url ://", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := hostOf(c.rawURL)
		if got != c.want {
			t.Errorf("hostOf(%q) = %q, want %q", c.rawURL, got, c.want)
		}
	}
}

func TestNoopTelemFn(t *testing.T) {
	// Ensure a nil-telem wrapper produces a callable telemFn (no panic).
	w := &ALLSPWrapper{}
	fn := w.downloadTelemFn()
	fn("lookup", "some error", 0, "example.com") // must not panic
}
