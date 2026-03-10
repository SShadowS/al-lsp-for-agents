package wrapper

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// fieldHoverRegex matches AL field hover output: "(field) Name: Type" or "(field) "Quoted Name": Type"
var fieldHoverRegex = regexp.MustCompile(`(?m)^\(field\)\s+("?[^":]+?"?)\s*:\s*(.+)$`)

// enrichFieldHover detects field hover patterns and enriches them with properties
// from al-call-hierarchy's tree-sitter parsing.
func enrichFieldHover(w WrapperInterface, hoverResult json.RawMessage, params TextDocumentPositionParams) json.RawMessage {
	// Extract hover text to check if it's a field
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

	// Query al-call-hierarchy for field properties
	chServer := w.GetCallHierarchyServer()
	if chServer == nil || !chServer.IsInitialized() {
		w.Log("al-call-hierarchy not available for field properties")
		return hoverResult
	}

	// First resolve the definition to find the source file
	alParams := ALGotoDefinitionParams{
		TextDocumentPositionParams: params,
	}
	defResp, err := w.SendRequestToLSP("al/gotodefinition", alParams)
	if err != nil || defResp.Error != nil || defResp.Result == nil {
		// Fallback: use the current file URI
		return enrichFieldFromFile(w, chServer, hoverResult, params.TextDocument.URI, fieldName)
	}

	// Parse definition location to get the source file
	_, defPath := parseDefinitionLocation(defResp.Result)
	if defPath == "" {
		// Fallback: use current file
		return enrichFieldFromFile(w, chServer, hoverResult, params.TextDocument.URI, fieldName)
	}

	defURI := PathToFileURI(defPath)
	return enrichFieldFromFile(w, chServer, hoverResult, defURI, fieldName)
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

	// Parse the field properties
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

	// Build the enrichment text
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

func formatProp(name, value string) string {
	return "- **" + name + ":** " + value
}

func uintToStr(n uint32) string {
	return fmt.Sprintf("%d", n)
}
