package wrapper

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// enrichHoverWithXmlDoc resolves the definition of the symbol at the given position,
// reads the source file, extracts the full XML doc comment block, and appends it
// to the hover result. This gives AI agents the full documentation including
// <param>, <returns>, <remarks>, and <example> tags that the AL LSP's hover
// response normally omits (only showing <summary>).
func enrichHoverWithXmlDoc(w WrapperInterface, hoverResult json.RawMessage, params TextDocumentPositionParams) json.RawMessage {
	// Resolve definition to find the source location
	alParams := ALGotoDefinitionParams{
		TextDocumentPositionParams: params,
	}
	defResp, err := w.SendRequestToLSP("al/gotodefinition", alParams)
	if err != nil || defResp.Error != nil || defResp.Result == nil {
		return hoverResult
	}

	// Parse the definition location
	defLine, defPath := parseDefinitionLocation(defResp.Result)
	if defPath == "" || defLine < 0 {
		return hoverResult
	}

	// Read the source file and extract XML doc comments above the definition
	xmlDoc := extractXmlDocFromFile(defPath, defLine)
	if xmlDoc == "" {
		return hoverResult
	}

	// Parse the XML doc into a readable format
	parsed := parseXmlDoc(xmlDoc)
	if parsed == "" {
		return hoverResult
	}

	// Append the parsed XML doc to the hover content
	return appendToHoverContent(hoverResult, "\n\n--- Documentation ---\n"+parsed)
}

// parseDefinitionLocation extracts the file path and line number from a definition response.
// The response can be a single Location or an array of Locations.
func parseDefinitionLocation(result json.RawMessage) (int, string) {
	// Try single location
	var loc struct {
		URI   string `json:"uri"`
		Range Range  `json:"range"`
	}
	if err := json.Unmarshal(result, &loc); err == nil && loc.URI != "" {
		path, err := FileURIToPath(loc.URI)
		if err != nil {
			return -1, ""
		}
		return loc.Range.Start.Line, path
	}

	// Try array of locations
	var locs []struct {
		URI   string `json:"uri"`
		Range Range  `json:"range"`
	}
	if err := json.Unmarshal(result, &locs); err == nil && len(locs) > 0 {
		path, err := FileURIToPath(locs[0].URI)
		if err != nil {
			return -1, ""
		}
		return locs[0].Range.Start.Line, path
	}

	return -1, ""
}

// extractXmlDocFromFile reads the file and collects consecutive /// comment lines
// immediately above the given line number (0-based).
func extractXmlDocFromFile(filePath string, defLine int) string {
	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	// Read all lines up to and including the definition line
	var lines []string
	scanner := bufio.NewScanner(f)
	for i := 0; scanner.Scan() && i <= defLine; i++ {
		lines = append(lines, scanner.Text())
	}

	if defLine >= len(lines) {
		return ""
	}

	// Walk backwards from the line before the definition collecting /// lines
	var commentLines []string
	for i := defLine - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "///") {
			// Strip the /// prefix and optional leading space
			content := strings.TrimPrefix(trimmed, "///")
			if strings.HasPrefix(content, " ") {
				content = content[1:]
			}
			commentLines = append([]string{content}, commentLines...)
		} else {
			break
		}
	}

	if len(commentLines) == 0 {
		return ""
	}

	return strings.Join(commentLines, "\n")
}

var paramRegex = regexp.MustCompile(`<param\s+name=["']([^"']+)["']>([\s\S]*?)</param>`)

// parseXmlDoc parses XML doc comment text into a readable format for AI agents.
func parseXmlDoc(xml string) string {
	var parts []string

	// Extract <summary>
	if summary := extractXmlTag(xml, "summary"); summary != "" {
		parts = append(parts, strings.TrimSpace(summary))
	}

	// Extract <param> tags
	matches := paramRegex.FindAllStringSubmatch(xml, -1)
	if len(matches) > 0 {
		var params []string
		for _, m := range matches {
			params = append(params, fmt.Sprintf("- `%s`: %s", m[1], strings.TrimSpace(m[2])))
		}
		parts = append(parts, "**Parameters:**\n"+strings.Join(params, "\n"))
	}

	// Extract <returns>
	if returns := extractXmlTag(xml, "returns"); returns != "" {
		parts = append(parts, "**Returns:** "+strings.TrimSpace(returns))
	}

	// Extract <remarks>
	if remarks := extractXmlTag(xml, "remarks"); remarks != "" {
		parts = append(parts, "**Remarks:** "+strings.TrimSpace(remarks))
	}

	// Extract <example>
	if example := extractXmlTag(xml, "example"); example != "" {
		parts = append(parts, "**Example:** "+strings.TrimSpace(example))
	}

	return strings.Join(parts, "\n\n")
}

// extractXmlTag extracts the content of a simple XML tag.
func extractXmlTag(xml string, tag string) string {
	re := regexp.MustCompile(`(?s)<` + tag + `>(.*?)</` + tag + `>`)
	match := re.FindStringSubmatch(xml)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

// appendToHoverContent appends text to the hover result's markdown content.
func appendToHoverContent(hoverResult json.RawMessage, extra string) json.RawMessage {
	var hover struct {
		Contents json.RawMessage `json:"contents"`
		Range    json.RawMessage `json:"range,omitempty"`
	}
	if err := json.Unmarshal(hoverResult, &hover); err != nil {
		return hoverResult
	}

	var content struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(hover.Contents, &content); err != nil || content.Kind == "" {
		return hoverResult
	}

	content.Value += extra
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return hoverResult
	}

	out := map[string]json.RawMessage{
		"contents": contentJSON,
	}
	if hover.Range != nil && string(hover.Range) != "null" {
		out["range"] = hover.Range
	}

	result, err := json.Marshal(out)
	if err != nil {
		return hoverResult
	}
	return result
}
