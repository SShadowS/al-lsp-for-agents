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

func TestObjectIDRange(t *testing.T) {
	cases := []struct {
		id   int
		want ObjectIDRange
	}{
		{1, RangeMSReserved},
		{49999, RangeMSReserved},
		{50000, RangeCustomer},
		{99999, RangeCustomer},
		{130000, RangeMSTest},
		{150000, RangeMSTest},
		{0, RangeUnknown},
		{200000, RangeUnknown},
	}
	for _, c := range cases {
		if got := ClassifyObjectID(c.id); got != c.want {
			t.Errorf("ClassifyObjectID(%d) = %v, want %v", c.id, got, c.want)
		}
	}
}

func TestCustomerBucket(t *testing.T) {
	if got := CustomerBucket(50104); got != "50xxx" {
		t.Errorf("CustomerBucket(50104) = %q, want %q", got, "50xxx")
	}
	if got := CustomerBucket(99999); got != "99xxx" {
		t.Errorf("CustomerBucket(99999) = %q, want %q", got, "99xxx")
	}
}
