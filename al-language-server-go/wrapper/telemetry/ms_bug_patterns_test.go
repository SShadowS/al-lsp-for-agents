package telemetry

import "testing"

func TestMatchKnownMSBug(t *testing.T) {
	cases := []struct {
		in     string
		wantID string
	}{
		{"Object 'X' is already declared in 'Y'", "ms-already-declared"},
		{"AL Test Runner sent didOpen for file://...", "ms-dup-didopen"},
		{"Malformed URI 'file:///c:/Foo.al' due to casing", "ms-uri-casing"},
		{"unrelated", ""},
	}
	for _, c := range cases {
		bug, _ := MatchMSBug(c.in)
		if bug == nil && c.wantID != "" {
			t.Errorf("MatchMSBug(%q) returned nil, want id %q", c.in, c.wantID)
			continue
		}
		if bug != nil && bug.ID != c.wantID {
			t.Errorf("MatchMSBug(%q).ID = %q, want %q", c.in, bug.ID, c.wantID)
		}
	}
}

func TestEveryRegistryEntryHasIssueURL(t *testing.T) {
	for _, p := range MSBugPatterns {
		if p.IssueURL == "" {
			t.Errorf("bug %q has empty IssueURL", p.ID)
		}
		if p.Pattern == nil {
			t.Errorf("bug %q has nil Pattern", p.ID)
		}
	}
}
