package wrapper

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
)

// TestDiagnosticMerger_UnionSurvivesCrossBackendPublish reproduces issue #20
// symptom #2: al-call-hierarchy publishes diagnostics for a file, then the AL LS
// publishes its own diagnostics for the same file (as happens after
// prepareCallHierarchy triggers re-analysis). Without merging, the second
// publish clobbers the first. The merger must keep both.
func TestDiagnosticMerger_UnionSurvivesCrossBackendPublish(t *testing.T) {
	const uri = "file:///c:/proj/Foo.al"
	chDiag := json.RawMessage(`{"source":"al-call-hierarchy","message":"unused procedure"}`)
	alDiag := json.RawMessage(`{"source":"AL","message":"already declared"}`)

	m := NewDiagnosticMerger()

	// al-call-hierarchy publishes first (e.g. at startup).
	got := m.Merge(diagBackendCallHierarchy, uri, []json.RawMessage{chDiag})
	if len(got) != 1 {
		t.Fatalf("after call-hierarchy publish: len=%d, want 1", len(got))
	}

	// AL LS then publishes for the SAME uri. Verbatim forwarding would erase the
	// call-hierarchy diagnostic; the merger must return the union.
	got = m.Merge(diagBackendALLS, uri, []json.RawMessage{alDiag})
	if len(got) != 2 {
		t.Fatalf("after AL-LS publish: len=%d, want 2 (union of both backends)", len(got))
	}
	if !containsSource(got, "al-call-hierarchy") || !containsSource(got, "AL") {
		t.Fatalf("union missing a backend: %v", rawSlice(got))
	}
}

// TestDiagnosticMerger_ClearRemovesOnlyOwnBackend verifies a backend clearing
// its diagnostics (empty array) removes only its own contribution, leaving the
// other backend's diagnostics intact.
func TestDiagnosticMerger_ClearRemovesOnlyOwnBackend(t *testing.T) {
	const uri = "file:///c:/proj/Bar.al"
	chDiag := json.RawMessage(`{"source":"al-call-hierarchy","message":"high complexity"}`)
	alDiag := json.RawMessage(`{"source":"AL","message":"syntax error"}`)

	m := NewDiagnosticMerger()
	m.Merge(diagBackendCallHierarchy, uri, []json.RawMessage{chDiag})
	m.Merge(diagBackendALLS, uri, []json.RawMessage{alDiag})

	// AL LS clears its diagnostics (file now compiles). call-hierarchy's stay.
	got := m.Merge(diagBackendALLS, uri, []json.RawMessage{})
	if len(got) != 1 || !containsSource(got, "al-call-hierarchy") {
		t.Fatalf("after AL-LS clear: got %v, want only the al-call-hierarchy diagnostic", rawSlice(got))
	}
}

// TestDiagnosticMerger_RewritesMessageParams checks the message-level helper
// rewrites publishDiagnostics params to the merged union and preserves the URI.
func TestDiagnosticMerger_RewritesMessageParams(t *testing.T) {
	const uri = "file:///c:/proj/Baz.al"
	m := NewDiagnosticMerger()
	// Seed a call-hierarchy diagnostic for the uri.
	m.Merge(diagBackendCallHierarchy, uri, []json.RawMessage{
		json.RawMessage(`{"source":"al-call-hierarchy","message":"unused"}`),
	})

	// AL LS publishes its own single diagnostic via a real message.
	params, _ := json.Marshal(map[string]interface{}{
		"uri": uri,
		"diagnostics": []interface{}{
			map[string]interface{}{"source": "AL", "message": "warning"},
		},
	})
	msg := &Message{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics", Params: params}

	if !m.MergePublishDiagnostics(diagBackendALLS, msg) {
		t.Fatal("MergePublishDiagnostics returned false on a valid payload")
	}
	var out struct {
		URI         string            `json:"uri"`
		Diagnostics []json.RawMessage `json:"diagnostics"`
	}
	if err := json.Unmarshal(msg.Params, &out); err != nil {
		t.Fatalf("rewritten params not valid JSON: %v", err)
	}
	if out.URI != uri {
		t.Fatalf("uri = %q, want %q", out.URI, uri)
	}
	if len(out.Diagnostics) != 2 {
		t.Fatalf("merged diagnostics len=%d, want 2", len(out.Diagnostics))
	}
}

// TestWriteToClient_MergesDiagnostics is the end-to-end wiring test: a
// call-hierarchy diagnostic is already known for a URI, then the AL LS publishes
// its own for the same URI through writeToClient. The bytes sent to the client
// must carry the union, not just the AL LS set (issue #20 symptom #2).
func TestWriteToClient_MergesDiagnostics(t *testing.T) {
	const uri = "file:///c:/proj/Qux.al"
	w := New()
	var buf bytes.Buffer
	w.clientWriter = &buf
	w.diagMerger = NewDiagnosticMerger()

	// al-call-hierarchy already reported one diagnostic for this URI.
	w.diagMerger.Merge(diagBackendCallHierarchy, uri, []json.RawMessage{
		json.RawMessage(`{"source":"al-call-hierarchy","message":"unused procedure"}`),
	})

	// AL LS now publishes its own diagnostic for the same URI.
	params, _ := json.Marshal(map[string]interface{}{
		"uri": uri,
		"diagnostics": []interface{}{
			map[string]interface{}{"source": "AL", "message": "already declared"},
		},
	})
	msg := &Message{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics", Params: params}
	if err := w.writeToClient(msg); err != nil {
		t.Fatalf("writeToClient: %v", err)
	}

	sent, err := ReadMessage(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("reading framed message: %v", err)
	}
	var pd struct {
		URI         string            `json:"uri"`
		Diagnostics []json.RawMessage `json:"diagnostics"`
	}
	if err := json.Unmarshal(sent.Params, &pd); err != nil {
		t.Fatalf("params: %v", err)
	}
	if len(pd.Diagnostics) != 2 {
		t.Fatalf("client received %d diagnostics, want 2 (union): %v", len(pd.Diagnostics), rawSlice(pd.Diagnostics))
	}
	if !containsSource(pd.Diagnostics, "al-call-hierarchy") || !containsSource(pd.Diagnostics, "AL") {
		t.Fatalf("client union missing a backend: %v", rawSlice(pd.Diagnostics))
	}
}

func containsSource(diags []json.RawMessage, source string) bool {
	for _, d := range diags {
		var x struct {
			Source string `json:"source"`
		}
		if json.Unmarshal(d, &x) == nil && x.Source == source {
			return true
		}
	}
	return false
}

func rawSlice(diags []json.RawMessage) []string {
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = string(d)
	}
	return out
}

func publishMsg(t *testing.T, uri, message string) *Message {
	t.Helper()
	params, _ := json.Marshal(map[string]interface{}{
		"uri":         uri,
		"diagnostics": []interface{}{map[string]interface{}{"message": message}},
	})
	return &Message{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics", Params: params}
}

func decodePublish(t *testing.T, msg *Message) (string, int) {
	t.Helper()
	var out struct {
		URI         string            `json:"uri"`
		Diagnostics []json.RawMessage `json:"diagnostics"`
	}
	if err := json.Unmarshal(msg.Params, &out); err != nil {
		t.Fatal(err)
	}
	return out.URI, len(out.Diagnostics)
}

// TestDiagnosticMerger_CaseFoldedURIsMerge covers Windows: al-call-hierarchy
// publishes under a case-folded URI (file:///u:/git/a/src/X.al) while the AL LS
// uses the real casing. They are the same file and must merge, and the client
// must see one URI: the AL LS form.
func TestDiagnosticMerger_CaseFoldedURIsMerge(t *testing.T) {
	old := caseInsensitivePaths
	caseInsensitivePaths = true
	defer func() { caseInsensitivePaths = old }()

	const alURI = "file:///U:/Git/Repo/A/src/ATop.Codeunit.al"
	const chURI = "file:///u%3A/git/repo/a/src/ATop.Codeunit.al"
	m := NewDiagnosticMerger()

	m.MergePublishDiagnostics(diagBackendALLS, publishMsg(t, alURI, "error"))
	ch := publishMsg(t, chURI, "unused")
	m.MergePublishDiagnostics(diagBackendCallHierarchy, ch)

	if uri, n := decodePublish(t, ch); uri != alURI || n != 2 {
		t.Errorf("call-hierarchy publish = (%q, %d), want (%q, 2)", uri, n, alURI)
	}
}

// TestDiagnosticMerger_CaseSensitiveKeepsDistinct: on case-sensitive
// filesystems two URIs differing only in case are different files.
func TestDiagnosticMerger_CaseSensitiveKeepsDistinct(t *testing.T) {
	old := caseInsensitivePaths
	caseInsensitivePaths = false
	defer func() { caseInsensitivePaths = old }()

	m := NewDiagnosticMerger()
	m.MergePublishDiagnostics(diagBackendALLS, publishMsg(t, "file:///src/A.al", "error"))
	ch := publishMsg(t, "file:///src/a.al", "unused")
	m.MergePublishDiagnostics(diagBackendCallHierarchy, ch)

	if uri, n := decodePublish(t, ch); uri != "file:///src/a.al" || n != 1 {
		t.Errorf("got (%q, %d), want (file:///src/a.al, 1)", uri, n)
	}
}

// TestDiagnosticMerger_ClientURIPreferred: when al-call-hierarchy publishes
// before the AL LS, the client's didOpen URI is used.
func TestDiagnosticMerger_ClientURIPreferred(t *testing.T) {
	old := caseInsensitivePaths
	caseInsensitivePaths = true
	defer func() { caseInsensitivePaths = old }()

	m := NewDiagnosticMerger()
	m.PreferURI("file:///C:/Proj/Src/X.al")
	ch := publishMsg(t, "file:///c:/proj/src/X.al", "unused")
	m.MergePublishDiagnostics(diagBackendCallHierarchy, ch)
	if uri, _ := decodePublish(t, ch); uri != "file:///C:/Proj/Src/X.al" {
		t.Errorf("uri = %q, want the client's", uri)
	}
}
