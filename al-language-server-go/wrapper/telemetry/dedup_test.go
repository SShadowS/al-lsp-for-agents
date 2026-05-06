package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFingerprintInputsPerEventType(t *testing.T) {
	if Fingerprint("wrapper.panic", "nil-deref|wrapper.handle") == "" {
		t.Errorf("Fingerprint returned empty")
	}
	a := Fingerprint("wrapper.panic", "nil-deref|x")
	b := Fingerprint("wrapper.panic", "nil-deref|y")
	if a == b {
		t.Errorf("different inputs produced same fingerprint")
	}
	same1 := Fingerprint("wrapper.panic", "nil-deref|x")
	same2 := Fingerprint("wrapper.panic", "nil-deref|x")
	if same1 != same2 {
		t.Errorf("same inputs produced different fingerprints")
	}
}

func TestDedupSendsFirstThenSuppresses(t *testing.T) {
	d := NewDedup()
	d.now = func() time.Time { return time.Unix(1000, 0) }
	if !d.ShouldSend("fp1") {
		t.Fatalf("first occurrence should send")
	}
	if d.ShouldSend("fp1") {
		t.Errorf("immediate second should be suppressed")
	}
	d.now = func() time.Time { return time.Unix(1059, 0) }
	if d.ShouldSend("fp1") {
		t.Errorf("second within 60s should be suppressed")
	}
	d.now = func() time.Time { return time.Unix(1061, 0) }
	if !d.ShouldSend("fp1") {
		t.Errorf("after 60s should send again")
	}
}

func TestDedupTracksSuppressedCount(t *testing.T) {
	d := NewDedup()
	d.now = func() time.Time { return time.Unix(1000, 0) }
	d.ShouldSend("fp")
	for i := 0; i < 5; i++ {
		d.ShouldSend("fp")
	}
	if got := d.SuppressedSince("fp"); got != 5 {
		t.Errorf("suppressedCount = %d, want 5", got)
	}
}

func TestDedupLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry-dedup.json")
	data, _ := json.Marshal(map[string]int64{"fp1": time.Now().Unix()})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	d := NewDedup()
	if err := d.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if d.ShouldSend("fp1") {
		t.Errorf("loaded fp1 should be in dedup window")
	}
}

func TestDedupSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry-dedup.json")
	d := NewDedup()
	d.ShouldSend("fp1")
	d.ShouldSend("fp2")
	if err := d.Save(path); err != nil {
		t.Fatal(err)
	}
	d2 := NewDedup()
	if err := d2.Load(path); err != nil {
		t.Fatal(err)
	}
	if d2.ShouldSend("fp1") {
		t.Errorf("fp1 lost after save+load")
	}
}

func TestDedupLoadFailOpenOnCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry-dedup.json")
	if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	d := NewDedup()
	if err := d.Load(path); err != nil {
		t.Errorf("Load should fail open on corrupt file, got error %v", err)
	}
	if !d.ShouldSend("anything") {
		t.Errorf("should send when load failed open")
	}
}

func TestDedupSaveCapsEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry-dedup.json")
	d := NewDedup()
	for i := 0; i < 300; i++ {
		d.ShouldSend("fp" + string(rune(i)))
	}
	if err := d.Save(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var m map[string]int64
	json.Unmarshal(raw, &m)
	if len(m) > 256 {
		t.Errorf("saved file has %d entries, cap is 256", len(m))
	}
}
