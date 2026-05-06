package telemetry

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestEnvelopeCarriesCommonFields(t *testing.T) {
	s := NewSession()
	env := NewEnvelope(s, "wrapper.panic", LevelErrors)
	if env.SchemaVersion != SchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", env.SchemaVersion, SchemaVersion)
	}
	if env.SessionID != s.ID {
		t.Errorf("sessionId mismatch")
	}
	if env.ConsentLevel != "errors" {
		t.Errorf("consentLevel = %q, want errors", env.ConsentLevel)
	}
	if env.Name != "wrapper.panic" {
		t.Errorf("name = %q", env.Name)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	s7 := string(raw)
	for _, f := range []string{"schemaVersion", "wrapperVersion", "alExtensionVersion", "os", "arch", "sessionId", "consentLevel"} {
		if !strings.Contains(s7, f) {
			t.Errorf("missing field %q in JSON: %s", f, s7)
		}
	}
}

func TestPanicEventBuilder(t *testing.T) {
	s := NewSession()
	ev := BuildPanicEvent(s, LevelErrors, "runtime error: invalid memory address", []Frame{
		{Function: "wrapper.handle", Line: 42},
	})
	if ev.MessageSignature != "nil-deref" {
		t.Errorf("messageSignature = %q, want nil-deref", ev.MessageSignature)
	}
	if len(ev.StackFrames) != 1 || ev.StackFrames[0].Function != "wrapper.handle" {
		t.Errorf("frames not preserved: %+v", ev.StackFrames)
	}
}

func TestALLSFailureEventBuilder(t *testing.T) {
	s := NewSession()
	ev := BuildALLSFailureEvent(s, LevelErrors, "crash", 137, "AL_LS panic at line 12")
	if ev.Subtype != "crash" {
		t.Errorf("subtype = %q", ev.Subtype)
	}
	if ev.ExitCode != 137 {
		t.Errorf("exitCode = %d", ev.ExitCode)
	}
	if ev.StderrSignature != "panic" {
		t.Errorf("stderrSignature = %q, want panic", ev.StderrSignature)
	}
}

func TestCaptureFramesReturnsFunctionAndLine(t *testing.T) {
	var frames []Frame
	func() {
		frames = CaptureFrames(0)
	}()
	if len(frames) == 0 {
		t.Fatalf("no frames captured")
	}
	if frames[0].Function == "" || frames[0].Line == 0 {
		t.Errorf("first frame missing fields: %+v", frames[0])
	}
	for _, f := range frames {
		if !strings.Contains(f.Function, ".") {
			t.Errorf("function name lacks package: %q", f.Function)
		}
	}
	_ = runtime.Caller // keep import live
}
