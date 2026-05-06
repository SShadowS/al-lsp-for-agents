package telemetry

import "testing"

func TestAllowlistAllowsBCSegments(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"Microsoft", true},
		{"Microsoft.Sales", true},
		{"Microsoft.Sales.Receivables", true},
		{"System", true},
		{"System.IO", true},
	}
	for _, c := range cases {
		got := IsAllowedBCSymbol(c.in)
		if got != c.want {
			t.Errorf("IsAllowedBCSymbol(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestAllowlistRejectsLookalikes(t *testing.T) {
	rejects := []string{
		"MicrosoftFoo",
		"SystemTools",
		"MyCompany.SystemBridge",
		"Microsoft.Sales.Custom",
		"",
	}
	for _, in := range rejects {
		if IsAllowedBCSymbol(in) {
			t.Errorf("IsAllowedBCSymbol(%q) returned true; should be rejected", in)
		}
	}
}
