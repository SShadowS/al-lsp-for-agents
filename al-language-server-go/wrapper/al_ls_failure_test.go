package wrapper

import (
	"sync"
	"testing"
)

func TestLastStderrLineUpdated(t *testing.T) {
	w := New()
	w.appendStderrLine("first line")
	w.appendStderrLine("second line")
	if got := w.getLastStderrLine(); got != "second line" {
		t.Errorf("got %q, want %q", got, "second line")
	}
}

func TestLastStderrLineConcurrentSafe(t *testing.T) {
	w := New()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w.appendStderrLine("line")
			_ = w.getLastStderrLine()
		}(i)
	}
	wg.Wait()
	// No assertion beyond "did not race or panic"; race detector enforces.
}
