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

	var props struct {
		FieldID            *uint32 `json:"fieldId,omitempty"`
		Caption            *string `json:"caption,omitempty"`
		FieldClass         *string `json:"fieldClass,omitempty"`
		CalcFormula        *string `json:"calcFormula,omitempty"`
		TableRelation      *string `json:"tableRelation,omitempty"`
		Editable           *string `json:"editable,omitempty"`
		DataClassification *string `json:"dataClassification,omitempty"`
	}
	if err := json.Unmarshal(resp.Result, &props); err != nil {
		w.Log("Failed to parse field properties: %v", err)
		return hoverResult
	}

	var parts []string
	if props.FieldID != nil {
		parts = append(parts, formatProp("Field ID", uintToStr(*props.FieldID)))
	}
	if props.Caption != nil {
		parts = append(parts, formatProp("Caption", *props.Caption))
	}
	if props.FieldClass != nil {
		parts = append(parts, formatProp("FieldClass", *props.FieldClass))
	}
	if props.CalcFormula != nil {
		parts = append(parts, formatProp("CalcFormula", *props.CalcFormula))
	}
	if props.TableRelation != nil {
		parts = append(parts, formatProp("TableRelation", *props.TableRelation))
	}
	if props.Editable != nil {
		parts = append(parts, formatProp("Editable", *props.Editable))
	}
	if props.DataClassification != nil {
		parts = append(parts, formatProp("DataClassification", *props.DataClassification))
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

	var props struct {
		Caption          *string `json:"caption,omitempty"`
		Image            *string `json:"image,omitempty"`
		RunObject        *string `json:"runObject,omitempty"`
		RunPageLink      *string `json:"runPageLink,omitempty"`
		RunPageMode      *string `json:"runPageMode,omitempty"`
		RunPageView      *string `json:"runPageView,omitempty"`
		Tooltip          *string `json:"tooltip,omitempty"`
		Promoted         *string `json:"promoted,omitempty"`
		PromotedCategory *string `json:"promotedCategory,omitempty"`
		ShortcutKey      *string `json:"shortcutKey,omitempty"`
		Scope            *string `json:"scope,omitempty"`
		Enabled          *string `json:"enabled,omitempty"`
		Visible          *string `json:"visible,omitempty"`
	}
	if err := json.Unmarshal(resp.Result, &props); err != nil {
		w.Log("Failed to parse action properties: %v", err)
		return hoverResult
	}

	// Check if we actually got any properties (empty result = not an action)
	hasProps := props.Caption != nil || props.RunObject != nil || props.Image != nil ||
		props.Tooltip != nil || props.Promoted != nil || props.ShortcutKey != nil

	if !hasProps {
		return hoverResult
	}

	var parts []string
	if props.Caption != nil {
		parts = append(parts, formatProp("Caption", *props.Caption))
	}
	if props.RunObject != nil {
		parts = append(parts, formatProp("RunObject", *props.RunObject))
	}
	if props.RunPageLink != nil {
		parts = append(parts, formatProp("RunPageLink", *props.RunPageLink))
	}
	if props.RunPageMode != nil {
		parts = append(parts, formatProp("RunPageMode", *props.RunPageMode))
	}
	if props.RunPageView != nil {
		parts = append(parts, formatProp("RunPageView", *props.RunPageView))
	}
	if props.Image != nil {
		parts = append(parts, formatProp("Image", *props.Image))
	}
	if props.Tooltip != nil {
		parts = append(parts, formatProp("ToolTip", *props.Tooltip))
	}
	if props.ShortcutKey != nil {
		parts = append(parts, formatProp("ShortcutKey", *props.ShortcutKey))
	}
	if props.Promoted != nil {
		parts = append(parts, formatProp("Promoted", *props.Promoted))
	}
	if props.PromotedCategory != nil {
		parts = append(parts, formatProp("PromotedCategory", *props.PromotedCategory))
	}
	if props.Scope != nil {
		parts = append(parts, formatProp("Scope", *props.Scope))
	}
	if props.Enabled != nil {
		parts = append(parts, formatProp("Enabled", *props.Enabled))
	}
	if props.Visible != nil {
		parts = append(parts, formatProp("Visible", *props.Visible))
	}

	if len(parts) == 0 {
		return hoverResult
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
