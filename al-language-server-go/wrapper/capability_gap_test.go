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

func TestCapabilityGapEmitsAtFullLevel(t *testing.T) {
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
	cli.TrackCapabilityGap(s, "textDocument/foldingRange", "unhandled")
	cli.WaitDrain(2 * time.Second)
	raw, _ := os.ReadFile(dump)
	var ev map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &ev); err != nil {
		t.Fatalf("unmarshal: %v; raw=%s", err, raw)
	}
	if ev["name"] != "lsp.capability_gap" {
		t.Errorf("name = %v", ev["name"])
	}
	if ev["reason"] != "unhandled" {
		t.Errorf("reason = %v", ev["reason"])
	}
	if ev["method"] != "textDocument/foldingRange" {
		t.Errorf("method = %v", ev["method"])
	}
}

func TestCapabilityGapSuppressedAtErrorsLevel(t *testing.T) {
	dump := filepath.Join(t.TempDir(), "dump.jsonl")
	cli, _ := telemetry.NewClient(telemetry.ClientConfig{
		ConnString: "InstrumentationKey=fake",
		DumpPath:   dump,
		Level:      telemetry.LevelErrors,
	})
	s := telemetry.NewSession()
	cli.TrackCapabilityGap(s, "textDocument/foldingRange", "unhandled")
	cli.WaitDrain(2 * time.Second)
	info, _ := os.Stat(dump)
	if info != nil && info.Size() != 0 {
		t.Errorf("expected empty dump at LevelErrors, got %d bytes", info.Size())
	}
}
