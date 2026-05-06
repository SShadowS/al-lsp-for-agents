package wrapper

import "testing"

func TestRecoverGoroutineCapturesPanic(t *testing.T) {
	w := New()
	// recoverGoroutine re-panics, so we wrap in a defer to catch it.
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected re-panic")
		}
	}()
	func() {
		defer w.recoverGoroutine("test")
		panic("boom")
	}()
}
