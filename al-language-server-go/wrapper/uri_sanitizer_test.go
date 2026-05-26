package wrapper

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLooksLikeMalformedFileURI(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{`file://%3F\C:\foo.al`, true},
		{`file://%3F/C:/foo.al`, true},
		{`\\?\C:\foo.al`, true},
		{`\\?\UNC\srv\share\f.al`, true},
		{`file:///C:/foo.al`, false},
		{`al-preview:/foo/bar.dal`, false},
		{``, false},
		{`untitled:Untitled-1`, false},
		{`some text that mentions \\?\ but is not a URI`, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := looksLikeMalformedFileURI(tt.in); got != tt.want {
				t.Errorf("looksLikeMalformedFileURI(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestSanitizeOutboundMessage_PublishDiagnostics covers the exact shape the
// crash report uses: a textDocument/publishDiagnostics notification whose
// `uri` field is the malformed AL-LS output.
func TestSanitizeOutboundMessage_PublishDiagnostics(t *testing.T) {
	badURI := `file://%3F\C:\Users\arbo\Documents\source\repos\Document-Output-Extensions\Cloud\Al\Page\Page%206175339%20CDO%20Variant%20Entry%20Wizard.al`
	wantURI := `file:///C:/Users/arbo/Documents/source/repos/Document-Output-Extensions/Cloud/Al/Page/Page%206175339%20CDO%20Variant%20Entry%20Wizard.al`

	params := map[string]interface{}{
		"uri": badURI,
		"diagnostics": []interface{}{
			map[string]interface{}{
				"severity": 1,
				"message":  "some message",
				"range": map[string]interface{}{
					"start": map[string]interface{}{"line": 0, "character": 0},
					"end":   map[string]interface{}{"line": 0, "character": 10},
				},
			},
		},
	}
	rawParams, _ := json.Marshal(params)
	msg := &Message{
		JSONRPC: "2.0",
		Method:  "textDocument/publishDiagnostics",
		Params:  rawParams,
	}

	count, orig, norm := SanitizeOutboundMessage(msg)
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if orig != badURI || norm != wantURI {
		t.Fatalf("sample = (%q, %q), want (%q, %q)", orig, norm, badURI, wantURI)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(msg.Params, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := out["uri"].(string); got != wantURI {
		t.Errorf("out.uri = %q, want %q", got, wantURI)
	}
}

// TestSanitizeOutboundMessage_Location covers a Location-bearing result
// (e.g. textDocument/definition).
func TestSanitizeOutboundMessage_Location(t *testing.T) {
	result := map[string]interface{}{
		"uri": `file://%3F\C:\Users\arbo\foo.al`,
		"range": map[string]interface{}{
			"start": map[string]interface{}{"line": 1, "character": 2},
			"end":   map[string]interface{}{"line": 3, "character": 4},
		},
	}
	rawResult, _ := json.Marshal(result)
	msg := &Message{JSONRPC: "2.0", Result: rawResult}

	count, _, _ := SanitizeOutboundMessage(msg)
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if !strings.Contains(string(msg.Result), "file:///C:/Users/arbo/foo.al") {
		t.Errorf("result not normalized: %s", string(msg.Result))
	}
	if strings.Contains(string(msg.Result), "%3F") {
		t.Errorf("result still contains %%3F: %s", string(msg.Result))
	}
}

// TestSanitizeOutboundMessage_ArrayOfLocations covers references results.
func TestSanitizeOutboundMessage_ArrayOfLocations(t *testing.T) {
	locs := []map[string]interface{}{
		{"uri": `file://%3F\C:\a\b.al`, "range": map[string]interface{}{}},
		{"uri": `file://%3F\C:\c\d.al`, "range": map[string]interface{}{}},
		{"uri": `file:///C:/already/good.al`, "range": map[string]interface{}{}},
	}
	rawResult, _ := json.Marshal(locs)
	msg := &Message{JSONRPC: "2.0", Result: rawResult}

	count, _, _ := SanitizeOutboundMessage(msg)
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

// TestSanitizeOutboundMessage_WorkspaceEditChangesMapKeys covers the case
// where URIs appear as map keys (WorkspaceEdit.changes is keyed by URI).
func TestSanitizeOutboundMessage_WorkspaceEditChangesMapKeys(t *testing.T) {
	badURI := `file://%3F\C:\Users\arbo\foo.al`
	wantURI := `file:///C:/Users/arbo/foo.al`

	params := map[string]interface{}{
		"edit": map[string]interface{}{
			"changes": map[string]interface{}{
				badURI: []interface{}{
					map[string]interface{}{"newText": "x"},
				},
			},
		},
	}
	rawParams, _ := json.Marshal(params)
	msg := &Message{JSONRPC: "2.0", Method: "workspace/applyEdit", Params: rawParams}

	count, _, _ := SanitizeOutboundMessage(msg)
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	var out map[string]interface{}
	json.Unmarshal(msg.Params, &out)
	changes := out["edit"].(map[string]interface{})["changes"].(map[string]interface{})
	if _, ok := changes[badURI]; ok {
		t.Errorf("malformed key still present")
	}
	if _, ok := changes[wantURI]; !ok {
		t.Errorf("normalized key %q not present; got map keys %v", wantURI, mapKeys(changes))
	}
}

// TestSanitizeOutboundMessage_NestedDeep covers a deeply nested URI inside
// the kind of structure DocumentSymbol or CallHierarchy returns.
func TestSanitizeOutboundMessage_NestedDeep(t *testing.T) {
	result := map[string]interface{}{
		"items": []interface{}{
			map[string]interface{}{
				"name": "Foo",
				"location": map[string]interface{}{
					"uri": `file://%3F\C:\deep\path\to\file.al`,
					"range": map[string]interface{}{
						"start": map[string]interface{}{"line": 0},
					},
				},
				"children": []interface{}{
					map[string]interface{}{
						"name": "Bar",
						"location": map[string]interface{}{
							"uri": `file://%3F\C:\deep\path\to\file.al`,
						},
					},
				},
			},
		},
	}
	rawResult, _ := json.Marshal(result)
	msg := &Message{JSONRPC: "2.0", Result: rawResult}

	count, _, _ := SanitizeOutboundMessage(msg)
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

// TestSanitizeOutboundMessage_NoMalformedURI verifies the fast path: a healthy
// message round-trips with zero rewrites and unchanged bytes.
func TestSanitizeOutboundMessage_NoMalformedURI(t *testing.T) {
	rawParams := json.RawMessage(`{"uri":"file:///C:/clean/path.al","line":42}`)
	original := append(json.RawMessage(nil), rawParams...)
	msg := &Message{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: rawParams}

	count, orig, norm := SanitizeOutboundMessage(msg)
	if count != 0 || orig != "" || norm != "" {
		t.Fatalf("expected no rewrites, got count=%d orig=%q norm=%q", count, orig, norm)
	}
	if string(msg.Params) != string(original) {
		t.Errorf("params mutated despite fast path: got %s want %s", string(msg.Params), string(original))
	}
}

// TestSanitizeOutboundMessage_NilMessage is a guardrail.
func TestSanitizeOutboundMessage_NilMessage(t *testing.T) {
	count, _, _ := SanitizeOutboundMessage(nil)
	if count != 0 {
		t.Errorf("nil message: count = %d, want 0", count)
	}
}

// TestSanitizeOutboundMessage_VirtualURIsPassThrough confirms al-preview: and
// other non-file URI schemes are never touched.
func TestSanitizeOutboundMessage_VirtualURIsPassThrough(t *testing.T) {
	params := map[string]interface{}{
		"uri": "al-preview:/allang/App/Codeunit/123/MyCodeunit.dal",
	}
	rawParams, _ := json.Marshal(params)
	original := append(json.RawMessage(nil), rawParams...)
	msg := &Message{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: rawParams}

	count, _, _ := SanitizeOutboundMessage(msg)
	if count != 0 {
		t.Errorf("virtual URI: count = %d, want 0", count)
	}
	if string(msg.Params) != string(original) {
		t.Errorf("virtual URI params mutated")
	}
}

// TestSanitizeOutboundMessage_Idempotent verifies running the sanitizer twice
// on the same message changes nothing the second time.
func TestSanitizeOutboundMessage_Idempotent(t *testing.T) {
	params := map[string]interface{}{
		"uri": `file://%3F\C:\foo.al`,
	}
	rawParams, _ := json.Marshal(params)
	msg := &Message{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: rawParams}

	count1, _, _ := SanitizeOutboundMessage(msg)
	if count1 != 1 {
		t.Fatalf("first pass count = %d, want 1", count1)
	}
	count2, _, _ := SanitizeOutboundMessage(msg)
	if count2 != 0 {
		t.Errorf("second pass count = %d, want 0 (sanitizer not idempotent)", count2)
	}
}

func TestUriSanitizationStats_FirstHitLogsLoudly(t *testing.T) {
	var stats uriSanitizationStats
	var logs []string
	logFn := func(format string, args ...interface{}) {
		logs = append(logs, format)
	}

	stats.record(logFn, "AL-LS->client", "textDocument/publishDiagnostics", 1,
		`file://%3F\C:\foo.al`, `file:///C:/foo.al`)
	if len(logs) != 1 {
		t.Fatalf("first hit should log once, got %d log lines", len(logs))
	}
	if !strings.Contains(logs[0], "Detected malformed") {
		t.Errorf("first log should mention detection, got: %s", logs[0])
	}
	if stats.Total() != 1 {
		t.Errorf("total = %d, want 1", stats.Total())
	}
}

func TestUriSanitizationStats_Sampled(t *testing.T) {
	var stats uriSanitizationStats
	var logs []string
	logFn := func(format string, args ...interface{}) {
		logs = append(logs, format)
	}

	for i := 0; i < 250; i++ {
		stats.record(logFn, "AL-LS->client", "textDocument/foo", 1,
			"orig", "norm")
	}
	// Expect: 1 initial log + 2 sampled logs at the 100 and 200 boundary = 3.
	if len(logs) != 3 {
		t.Errorf("expected 3 log lines (initial + 2 samples), got %d: %v", len(logs), logs)
	}
	if stats.Total() != 250 {
		t.Errorf("total = %d, want 250", stats.Total())
	}
}

func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
