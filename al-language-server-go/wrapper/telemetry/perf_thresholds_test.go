package telemetry

import "testing"

func TestPerfThresholdLookup(t *testing.T) {
	cases := []struct {
		method  string
		wantMs  int
	}{
		{"al/gotodefinition", 5000},
		{"textDocument/hover", 2000},
		{"textDocument/documentSymbol", 5000},
		{"unknown/method", 10000},
	}
	for _, c := range cases {
		if got := PerfThresholdMs(c.method); got != c.wantMs {
			t.Errorf("PerfThresholdMs(%q) = %d, want %d", c.method, got, c.wantMs)
		}
	}
}
