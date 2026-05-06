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

// TestMSBugEmitsAtErrorsLevel verifies that a line matching the MS bug
// registry produces an ms_bug.fingerprint event under LevelErrors. We
// invoke the client directly (rather than spinning up a real wrapper)
// because the stderr scanner pulls from the AL LS process; integration
// at that level happens in test-al-project/test_telemetry.py.
func TestMSBugEmitsAtErrorsLevel(t *testing.T) {
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
	bug, patternID := telemetry.MatchMSBug("Object 'Foo' is already declared in 'Bar'")
	if bug == nil {
		t.Fatalf("expected MatchMSBug to return a bug")
	}
	cli.TrackMSBug(s, bug, patternID)
	cli.WaitDrain(2 * time.Second)
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatalf("dump file empty")
	}
	var ev map[string]interface{}
	line := strings.TrimSpace(string(raw))
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("unmarshal: %v; raw=%q", err, raw)
	}
	if ev["name"] != "ms_bug.fingerprint" {
		t.Errorf("name = %v", ev["name"])
	}
	if ev["bugId"] != "ms-already-declared" {
		t.Errorf("bugId = %v", ev["bugId"])
	}
}
