package telemetry

import (
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
