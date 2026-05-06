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

func TestLSPRequestErrorEmitsEventAtFullLevel(t *testing.T) {
	dump := filepath.Join(t.TempDir(), "dump.jsonl")
	cli, err := telemetry.NewClient(telemetry.ClientConfig{
		ConnString: "InstrumentationKey=fake",
		DumpPath:   dump,
		Level:      telemetry.LevelFull,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := telemetry.NewSession()
	cli.TrackLSPRequestError(s, "al/gotodefinition", -32603, "deadline exceeded", 7000)
	cli.WaitDrain(2 * time.Second)
	raw, _ := os.ReadFile(dump)
	var ev map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &ev); err != nil {
		t.Fatalf("unmarshal: %v; raw=%s", err, raw)
	}
	if ev["errorClass"] != "timeout" {
		t.Errorf("errorClass = %v, want timeout", ev["errorClass"])
	}
	if ev["method"] != "al/gotodefinition" {
		t.Errorf("method = %v, want al/gotodefinition", ev["method"])
	}
}

func TestLSPRequestErrorSuppressedAtErrorsLevel(t *testing.T) {
	dump := filepath.Join(t.TempDir(), "dump.jsonl")
	cli, _ := telemetry.NewClient(telemetry.ClientConfig{
		ConnString: "InstrumentationKey=fake",
		DumpPath:   dump,
		Level:      telemetry.LevelErrors,
	})
	s := telemetry.NewSession()
	cli.TrackLSPRequestError(s, "al/gotodefinition", -32603, "deadline exceeded", 7000)
	cli.WaitDrain(2 * time.Second)
	info, _ := os.Stat(dump)
	if info != nil && info.Size() != 0 {
		t.Errorf("expected empty dump at LevelErrors, got %d bytes", info.Size())
	}
}
