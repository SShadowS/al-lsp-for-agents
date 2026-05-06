package telemetry

import "regexp"

// MSBugPattern is one entry in the upstream-bug fingerprint registry.
// Adding an entry requires: a stable ID, a precompiled regex on the
// stderr/diagnostic text, and a public issue URL.
type MSBugPattern struct {
	ID       string         // stable identifier shipped in events
	Pattern  *regexp.Regexp // matched against scrubbed text
	IssueURL string         // upstream issue link (constant, no user data)
}

// MSBugPatterns is the closed registry. Adding entries is a code change
// reviewed alongside the test suite.
var MSBugPatterns = []MSBugPattern{
	{
		ID:       "ms-already-declared",
		Pattern:  regexp.MustCompile(`(?i)is already declared`),
		IssueURL: "https://github.com/microsoft/AL/issues/15",
	},
	{
		ID:       "ms-dup-didopen",
		Pattern:  regexp.MustCompile(`(?i)didOpen.*duplicate|test runner sent didOpen`),
		IssueURL: "https://github.com/microsoft/AL/issues/17",
	},
	{
		ID:       "ms-uri-casing",
		Pattern:  regexp.MustCompile(`(?i)malformed uri.*casing|uri.*case.*mismatch`),
		IssueURL: "https://github.com/microsoft/AL/issues/8249",
	},
}

// MatchMSBug scans s against the registry and returns the first matching
// entry plus the matched substring (for the patternId field). Returns
// (nil, "") if no match.
func MatchMSBug(s string) (*MSBugPattern, string) {
	for i := range MSBugPatterns {
		if loc := MSBugPatterns[i].Pattern.FindStringIndex(s); loc != nil {
			return &MSBugPatterns[i], MSBugPatterns[i].ID + ":" + MSBugPatterns[i].Pattern.String()
		}
	}
	return nil, ""
}
