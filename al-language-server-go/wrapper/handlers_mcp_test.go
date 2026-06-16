package wrapper

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSymbolRelationsReshape(t *testing.T) {
	res := &ToolCallResult{
		Content: []ContentBlock{{Type: "text", Text: `{"relations":[{"type":"Extends","name":"Customer"}],"truncated":false}`}},
		IsError: false,
	}
	out, isErr := reshapeToolResult(res)
	if isErr {
		t.Fatalf("unexpected error flag")
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("result not JSON object: %v (%s)", err, string(out))
	}
	if _, ok := parsed["relations"]; !ok {
		t.Fatalf("expected relations key, got %s", string(out))
	}
}

func TestReshapeToolResultPlainText(t *testing.T) {
	res := &ToolCallResult{Content: []ContentBlock{{Type: "text", Text: "not json"}}, IsError: true}
	out, isErr := reshapeToolResult(res)
	if !isErr {
		t.Fatalf("expected error flag true")
	}
	if !strings.Contains(string(out), "not json") {
		t.Fatalf("expected text passthrough, got %s", string(out))
	}
}

func TestInspectPageHandlerName(t *testing.T) {
	h := &InspectPageHandler{}
	if !h.ShouldHandle("al/inspectPage") {
		t.Fatalf("should handle al/inspectPage")
	}
	if h.ShouldHandle("al/symbolRelations") {
		t.Fatalf("should not handle other methods")
	}
}

func TestSymbolRelationsFallbackToEditorServices(t *testing.T) {
	m := &mockWrapper{
		lspResponder: func(method string, params interface{}) (*Message, error) {
			if method != "al/symbolRelations" {
				t.Fatalf("expected al/symbolRelations, got %s", method)
			}
			return &Message{JSONRPC: "2.0", Result: json.RawMessage(`{"relations":[{"type":"Extends"}],"truncated":false}`)}, nil
		},
	}
	h := &SymbolRelationsHandler{}
	id := json.RawMessage(`1`)
	out, errMsg := h.Handle(&Message{ID: &id, Method: "al/symbolRelations", Params: json.RawMessage(`{"symbolName":"Customer","symbolKind":"Table"}`)}, m)
	if errMsg != nil {
		t.Fatalf("unexpected error response: %+v", errMsg)
	}
	if out == nil || !strings.Contains(string(out.Result), `"relations"`) {
		t.Fatalf("expected relations result, got %+v", out)
	}
}
