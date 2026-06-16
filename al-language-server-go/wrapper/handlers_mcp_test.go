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
