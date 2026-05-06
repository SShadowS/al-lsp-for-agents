package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGlobalSingleton(t *testing.T) {
	// Reset global state after test.
	t.Cleanup(func() {
		globalClient.Store(nil)
		globalSession.Store(nil)
	})

	dump := filepath.Join(t.TempDir(), "singleton.jsonl")
	cli, err := NewClient(ClientConfig{
		ConnString: "InstrumentationKey=fake",
		DumpPath:   dump,
		Level:      LevelFull,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSession()

	// Before SetGlobal, TrackGlobalConfigError must be a no-op.
	TrackGlobalConfigError("path-discovery", "no-extension-found")

	SetGlobal(cli, s)

	// After SetGlobal, event must be emitted.
	TrackGlobalConfigError("path-discovery", "no-extension-found")
	cli.WaitDrain(2 * time.Second)

	raw, _ := os.ReadFile(dump)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 event in dump, got %d; raw=%s", len(lines), raw)
	}
	var ev map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("unmarshal: %v; raw=%s", err, raw)
	}
	if ev["subsystem"] != "path-discovery" {
		t.Errorf("subsystem = %v, want path-discovery", ev["subsystem"])
	}
	if ev["errorCode"] != "no-extension-found" {
		t.Errorf("errorCode = %v, want no-extension-found", ev["errorCode"])
	}
}
