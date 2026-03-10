package wrapper

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// fieldHoverRegex matches AL field hover output: "(field) Name: Type" or "(field) "Quoted Name": Type"
var fieldHoverRegex = regexp.MustCompile(`(?m)^\(field\)\s+("?[^":]+?"?)\s*:\s*(.+)$`)

// actionHoverRegex matches AL action hover output patterns.
// Actions may show as "(action) Name" or just the action name.
var actionHoverRegex = regexp.MustCompile(`(?m)^\(action\)\s+("?[^"]+?"?)`)

// enrichFieldHover detects field hover patterns and enriches them with properties
// from al-call-hierarchy's tree-sitter parsing.
func enrichFieldHover(w WrapperInterface, hoverResult json.RawMessage, params TextDocumentPositionParams) json.RawMessage {
	hoverText := extractHoverText(hoverResult)
	if hoverText == "" {
		return hoverResult
	}

	// Check if this is a field hover
	match := fieldHoverRegex.FindStringSubmatch(hoverText)
	if match == nil {
		return hoverResult
	}

	fieldName := strings.Trim(match[1], "\"")

	chServer := w.GetCallHierarchyServer()
	if chServer == nil || !chServer.IsInitialized() {
		return hoverResult
	}

	// Resolve definition to find the source file
	uri := resolveDefinitionURI(w, params)
	return enrichFieldFromFile(w, chServer, hoverResult, uri, fieldName)
}

// enrichActionHover detects action hover patterns and enriches them with properties.
// If the hover text matches an action pattern, or if definition resolves to a page file
// with a matching action, properties like RunObject, ToolTip, etc. are appended.
func enrichActionHover(w WrapperInterface, hoverResult json.RawMessage, params TextDocumentPositionParams) json.RawMessage {
	hoverText := extractHoverText(hoverResult)

	// Skip if this was already identified as a field
	if fieldHoverRegex.MatchString(hoverText) {
		return hoverResult
	}

	chServer := w.GetCallHierarchyServer()
	if chServer == nil || !chServer.IsInitialized() {
		return hoverResult
	}

	// Try to extract action name from hover text
	var actionName string
	if match := actionHoverRegex.FindStringSubmatch(hoverText); match != nil {
		actionName = strings.Trim(match[1], "\"")
	}

	// If no action pattern in hover, try to extract symbol name from hover text
	// The AL LSP may return just a name or a short identifier for actions
	if actionName == "" && hoverText != "" {
		// Try extracting a simple identifier from the hover (no type annotation = likely action/other)
		actionName = extractSimpleIdentifier(hoverText)
	}

	if actionName == "" {
		return hoverResult
	}

	// Resolve to source file and try action properties
	uri := resolveDefinitionURI(w, params)
	return enrichActionFromFile(w, chServer, hoverResult, uri, actionName)
}

// resolveDefinitionURI resolves the definition location and returns the file URI.
// Falls back to the current file URI if resolution fails.
func resolveDefinitionURI(w WrapperInterface, params TextDocumentPositionParams) string {
	alParams := ALGotoDefinitionParams{
		TextDocumentPositionParams: params,
	}
	defResp, err := w.SendRequestToLSP("al/gotodefinition", alParams)
	if err != nil || defResp.Error != nil || defResp.Result == nil {
		return params.TextDocument.URI
	}

	_, defPath := parseDefinitionLocation(defResp.Result)
	if defPath == "" {
		return params.TextDocument.URI
	}

	return PathToFileURI(defPath)
}

// symbolPropertiesResponse is the generic response from al-call-hierarchy property endpoints.
// Properties are returned as key-value pairs rather than a typed struct, so we never
// miss properties that tree-sitter can extract.
type symbolPropertiesResponse struct {
	FieldID    *uint32         `json:"field_id,omitempty"`
	Properties []propertyEntry `json:"properties"`
}

type propertyEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// enrichFieldFromFile queries al-call-hierarchy for field properties and appends to hover
func enrichFieldFromFile(w WrapperInterface, chServer *CallHierarchyServer, hoverResult json.RawMessage, uri string, fieldName string) json.RawMessage {
	w.Log("Requesting field properties for %s in %s", fieldName, uri)

	resp, err := chServer.Request("al-call-hierarchy/fieldProperties", map[string]string{
		"uri":       uri,
		"fieldName": fieldName,
	})
	if err != nil {
		w.Log("Field properties request failed: %v", err)
		return hoverResult
	}
	if resp.Error != nil {
		w.Log("Field properties error: %s", resp.Error.Message)
		return hoverResult
	}
	if resp.Result == nil || string(resp.Result) == "null" {
		return hoverResult
	}

	var props symbolPropertiesResponse
	if err := json.Unmarshal(resp.Result, &props); err != nil {
		w.Log("Failed to parse field properties: %v", err)
		return hoverResult
	}

	var parts []string
	if props.FieldID != nil {
		parts = append(parts, formatProp("Field ID", uintToStr(*props.FieldID)))
	}
	for _, p := range props.Properties {
		parts = append(parts, formatProp(p.Name, p.Value))
	}

	if len(parts) == 0 {
		return hoverResult
	}

	extra := "\n\n--- Field Properties ---\n" + strings.Join(parts, "\n")
	return appendToHoverContent(hoverResult, extra)
}

// enrichActionFromFile queries al-call-hierarchy for action properties and appends to hover
func enrichActionFromFile(w WrapperInterface, chServer *CallHierarchyServer, hoverResult json.RawMessage, uri string, actionName string) json.RawMessage {
	w.Log("Requesting action properties for %s in %s", actionName, uri)

	resp, err := chServer.Request("al-call-hierarchy/actionProperties", map[string]string{
		"uri":        uri,
		"actionName": actionName,
	})
	if err != nil {
		w.Log("Action properties request failed: %v", err)
		return hoverResult
	}
	if resp.Error != nil {
		w.Log("Action properties error: %s", resp.Error.Message)
		return hoverResult
	}
	if resp.Result == nil || string(resp.Result) == "null" {
		return hoverResult
	}

	var props symbolPropertiesResponse
	if err := json.Unmarshal(resp.Result, &props); err != nil {
		w.Log("Failed to parse action properties: %v", err)
		return hoverResult
	}

	if len(props.Properties) == 0 {
		return hoverResult
	}

	var parts []string
	for _, p := range props.Properties {
		parts = append(parts, formatProp(p.Name, p.Value))
	}

	extra := "\n\n--- Action Properties ---\n" + strings.Join(parts, "\n")
	return appendToHoverContent(hoverResult, extra)
}

// extractHoverText extracts the text content from a hover result
func extractHoverText(hoverResult json.RawMessage) string {
	var hover struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(hoverResult, &hover); err != nil {
		return ""
	}

	var content struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(hover.Contents, &content); err != nil {
		return ""
	}

	return content.Value
}

// extractSimpleIdentifier extracts a simple identifier from hover text.
// Used as a fallback when no known hover pattern matches.
func extractSimpleIdentifier(text string) string {
	// Skip if text looks like a procedure/trigger/table/field declaration
	skipPrefixes := []string{"(field)", "(local)", "procedure ", "trigger ", "table ", "page ", "codeunit ", "enum "}
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range skipPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}

	// Extract first word/identifier (might be a quoted name)
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "\"") {
		if end := strings.Index(trimmed[1:], "\""); end >= 0 {
			return trimmed[1 : end+1]
		}
	}

	// Simple word
	for i, c := range trimmed {
		if c == ' ' || c == '(' || c == ':' || c == '\n' {
			return trimmed[:i]
		}
	}
	return trimmed
}

func formatProp(name, value string) string {
	return "- **" + name + ":** " + value
}

func uintToStr(n uint32) string {
	return fmt.Sprintf("%d", n)
}
