package telemetry

import (
	"regexp"
	"strings"
	"testing"
)

func newCtx() *ScrubContext {
	return &ScrubContext{
		HomeDir:        `C:\Users\testuser`,
		WorkspaceRoots: []string{`C:\Users\testuser\Projects\my-app`},
		TempDir:        `C:\Users\testuser\AppData\Local\Temp`,
		Salt:           []byte("01234567890123456789012345678901"),
	}
}

func TestScrubReplacesHomeDir(t *testing.T) {
	in := `error in C:\Users\testuser\Projects\my-app\Codeunit50104.al`
	got := Scrub(in, SourcePath, newCtx())
	if strings.Contains(got, "testuser") {
		t.Errorf("home leaked: %q", got)
	}
	if !strings.Contains(got, "<WS>") {
		t.Errorf("expected <WS> placeholder: %q", got)
	}
}

func TestScrubHashesGUID(t *testing.T) {
	in := `app id 11111111-2222-3333-4444-555555555555 failed`
	got := Scrub(in, SourceALText, newCtx())
	if strings.Contains(got, "11111111-2222") {
		t.Errorf("GUID leaked: %q", got)
	}
	if !strings.Contains(got, "<guid:") {
		t.Errorf("expected <guid:HASH>: %q", got)
	}
}

func TestScrubAllowlistRejectsLookalikes(t *testing.T) {
	cases := []string{"MicrosoftFoo", "SystemTools", "MyCompany.SystemBridge"}
	for _, in := range cases {
		got := Scrub(in, SourceALText, newCtx())
		if strings.Contains(got, in) {
			t.Errorf("lookalike %q passed through unhashed: %q", in, got)
		}
		if !strings.Contains(got, "<sym:") {
			t.Errorf("expected <sym:HASH> for %q: %q", in, got)
		}
	}
}

func TestScrubAllowsBCSymbolsVerbatim(t *testing.T) {
	in := "Microsoft.Sales.Receivables"
	got := Scrub(in, SourceALText, newCtx())
	if !strings.Contains(got, "Microsoft.Sales.Receivables") {
		t.Errorf("BC symbol got hashed: %q", got)
	}
}

func TestScrubCustomerObjectIDBucketed(t *testing.T) {
	in := "Codeunit 50104"
	got := Scrub(in, SourceALText, newCtx())
	if strings.Contains(got, "50104") {
		t.Errorf("customer id leaked: %q", got)
	}
	if !strings.Contains(got, "<id:50xxx>") {
		t.Errorf("expected bucket <id:50xxx>: %q", got)
	}
}

func TestScrubMSObjectIDVerbatim(t *testing.T) {
	in := "Codeunit 1"
	got := Scrub(in, SourceALText, newCtx())
	if !strings.Contains(got, "Codeunit 1") {
		t.Errorf("MS object id wrongly bucketed: %q", got)
	}
}

func TestScrubSaltIsolatesHashes(t *testing.T) {
	a := newCtx()
	b := newCtx()
	b.Salt = []byte("99999999999999999999999999999999")
	inA := Scrub("MyCompanyCodeunit", SourceALText, a)
	inB := Scrub("MyCompanyCodeunit", SourceALText, b)
	if inA == inB {
		t.Errorf("expected different hashes under different salts; got %q == %q", inA, inB)
	}
}

func TestScrubSafetyNetRejectsLeaks(t *testing.T) {
	leaks := []string{
		`\\Users\\bob\\thing`,
		`/Users/alice/file.al`,
		`/home/dan/code`,
		`C:\Bob`,
	}
	for _, in := range leaks {
		if !ContainsLeak(in) {
			t.Errorf("ContainsLeak(%q) = false; should detect", in)
		}
	}
}

func TestScrubTruncatesLongInput(t *testing.T) {
	in := strings.Repeat("a", 5000)
	got := Scrub(in, SourceJSONRPC, newCtx())
	if len(got) > 1024+3 { // accept "..." suffix
		t.Errorf("expected truncation to <=1027, got %d", len(got))
	}
}

func TestScrubURLKeepsHostFromAllowlist(t *testing.T) {
	in := "https://marketplace.visualstudio.com/_apis/public/gallery/something"
	got := Scrub(in, SourceURL, newCtx())
	if !strings.Contains(got, "marketplace.visualstudio.com") {
		t.Errorf("known host stripped: %q", got)
	}
	if strings.Contains(got, "/something") {
		t.Errorf("path leaked: %q", got)
	}
}

func TestScrubURLDropsUnknownHost(t *testing.T) {
	in := "https://attacker.example.com/leak?secret=abc"
	got := Scrub(in, SourceURL, newCtx())
	if strings.Contains(got, "attacker.example.com") {
		t.Errorf("unknown host preserved: %q", got)
	}
	if strings.Contains(got, "secret=abc") {
		t.Errorf("query leaked: %q", got)
	}
	if !regexp.MustCompile(`<url:other>`).MatchString(got) {
		t.Errorf("expected <url:other>: %q", got)
	}
}
