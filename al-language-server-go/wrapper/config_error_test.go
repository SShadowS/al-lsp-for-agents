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

func TestConfigErrorEmitsForEachSubsystem(t *testing.T) {
	cases := []struct {
		subsystem string
		errorCode string
	}{
		{"path-discovery", "no-extension-found"},
		{"lockfile", "stale-lock"},
		{"al-call-hierarchy-config", "invalid-json"},
	}
	for _, c := range cases {
		t.Run(c.subsystem, func(t *testing.T) {
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
			cli.TrackConfigError(s, c.subsystem, c.errorCode)
			cli.WaitDrain(2 * time.Second)
			raw, _ := os.ReadFile(dump)
			var ev map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &ev); err != nil {
				t.Fatalf("unmarshal: %v; raw=%s", err, raw)
			}
			if ev["subsystem"] != c.subsystem {
				t.Errorf("subsystem = %v, want %s", ev["subsystem"], c.subsystem)
			}
			if ev["errorCode"] != c.errorCode {
				t.Errorf("errorCode = %v, want %s", ev["errorCode"], c.errorCode)
			}
		})
	}
}

func TestConfigErrorSuppressedAtErrorsLevel(t *testing.T) {
	dump := filepath.Join(t.TempDir(), "dump.jsonl")
	cli, _ := telemetry.NewClient(telemetry.ClientConfig{
		ConnString: "InstrumentationKey=fake",
		DumpPath:   dump,
		Level:      telemetry.LevelErrors,
	})
	s := telemetry.NewSession()
	cli.TrackConfigError(s, "path-discovery", "no-extension-found")
	cli.WaitDrain(2 * time.Second)
	info, _ := os.Stat(dump)
	if info != nil && info.Size() != 0 {
		t.Errorf("expected empty dump at LevelErrors, got %d bytes", info.Size())
	}
}
