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
