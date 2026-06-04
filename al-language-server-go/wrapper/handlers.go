package wrapper

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// TextDocumentPositionParams represents LSP text document position parameters
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// TextDocumentIdentifier represents a text document identifier
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// Position represents a position in a text document
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// ALGotoDefinitionParams represents parameters for al/gotodefinition
type ALGotoDefinitionParams struct {
	TextDocumentPositionParams TextDocumentPositionParams `json:"textDocumentPositionParams"`
}

// WorkspaceSymbolParams represents workspace/symbol parameters
type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

// ALSymbolSearchParams represents parameters for al/symbolSearch
// The AL Language Server expects {"query": "..."} NOT {"filter": "..."}
// The old field name was wrong and caused the AL LS to ignore queries
type ALSymbolSearchParams struct {
	Query string `json:"query"`
}

// ALSymbolSearchResponse represents the al/symbolSearch response format
type ALSymbolSearchResponse struct {
	Symbols   []ALSymbol `json:"symbols"`
	Truncated bool       `json:"truncated"`
}

// ALSymbol represents a symbol from al/symbolSearch
type ALSymbol struct {
	Name          string `json:"name"`
	FullName      string `json:"fullName"`
	Kind          string `json:"kind"`
	ContainerName string `json:"containerName"`
	Signature     string `json:"signature"`
	Path          string `json:"path"`
}

// alKindToLSPKind converts AL symbol kind strings to LSP SymbolKind numbers
func alKindToLSPKind(kind string) int {
	switch kind {
	case "Table", "TableExtension":
		return 5 // Class
	case "Page", "PageExtension", "PageCustomization":
		return 5 // Class
	case "Codeunit":
		return 5 // Class
	case "Report", "ReportExtension":
		return 5 // Class
	case "Query":
		return 5 // Class
	case "XmlPort":
		return 5 // Class
	case "Enum", "EnumExtension":
		return 10 // Enum
	case "Interface":
		return 11 // Interface
	case "PermissionSet", "PermissionSetExtension":
		return 5 // Class
	case "Profile":
		return 5 // Class
	case "Entitlement":
		return 5 // Class
	case "Method", "Procedure", "Trigger":
		return 6 // Method
	case "Field":
		return 8 // Field
	case "Variable":
		return 13 // Variable
	case "EnumValue":
		return 22 // EnumMember
	default:
		return 5 // Class as default
	}
}

// Location represents an LSP location
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// Range represents an LSP range
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// HoverResponse represents an LSP hover response
type HoverResponse struct {
	Contents MarkupContent `json:"contents"`
}

// MarkupContent represents LSP markup content
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// DocumentSymbol represents an LSP document symbol
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// SymbolInformation represents an LSP symbol information (flat format)
type SymbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
}

// Handler interface for method handlers
type Handler interface {
	// ShouldHandle returns true if this handler should process the method
	ShouldHandle(method string) bool

	// Handle processes the message and returns a modified message or an error response
	// The wrapper is passed to allow handlers to send requests/notifications
	Handle(msg *Message, wrapper WrapperInterface) (*Message, *Message)
}

// WrapperInterface defines methods handlers can call on the wrapper
type WrapperInterface interface {
	// EnsureFileOpened ensures a file is opened in the AL LSP
	EnsureFileOpened(filePath string) error

	// EnsureProjectInitialized ensures the project for a file is initialized
	EnsureProjectInitialized(filePath string) error

	// SendRequestToLSP sends a request to the AL LSP and waits for response
	SendRequestToLSP(method string, params interface{}) (*Message, error)

	// SendNotificationToLSP sends a notification to the AL LSP
	SendNotificationToLSP(method string, params interface{}) error

	// SendNotificationToClient sends a notification (e.g. window/showMessage)
	// upstream to the editor / Claude Code.
	SendNotificationToClient(method string, params interface{})

	// GetCallHierarchyServer returns the call hierarchy server (may be nil)
	GetCallHierarchyServer() *CallHierarchyServer

	// UpdateWorkspaceFolders adds/removes workspace folders from internal state
	UpdateWorkspaceFolders(added []WorkspaceFolder, removed []WorkspaceFolder)

	// WorkspaceFolders returns a snapshot of the current workspace folders
	// (used to locate the project root for cache materialization).
	WorkspaceFolders() []WorkspaceFolder

	// PreviewCache returns the al-preview→file materializer, or nil when
	// no workspace folder is known yet.
	PreviewCache() *previewCache

	// Log logs a message
	Log(format string, args ...interface{})
}

// DefinitionHandler handles textDocument/definition
type DefinitionHandler struct{}

func (h *DefinitionHandler) ShouldHandle(method string) bool {
	return method == "textDocument/definition"
}

func (h *DefinitionHandler) Handle(msg *Message, w WrapperInterface) (*Message, *Message) {
	var params TextDocumentPositionParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		w.Log("Failed to parse definition params: %v", err)
		return nil, NewErrorResponse(msg.ID, InvalidParams, "Invalid parameters")
	}

	// If the request targets a materialized preview-cache file, recover the
	// original al-preview:/ URI so AL LSP keeps treating it as a virtual
	// dependency document.
	if virtual, ok := translateCachePathToVirtual(w, params.TextDocument.URI); ok {
		params.TextDocument.URI = virtual
	}

	// Virtual URIs (e.g. al-preview:) are dependency documents managed by the AL LSP
	if !IsVirtualURI(params.TextDocument.URI) {
		filePath, err := FileURIToPath(params.TextDocument.URI)
		if err != nil {
			w.Log("Failed to convert URI: %v", err)
			return nil, NewErrorResponse(msg.ID, InternalError, "Invalid file URI")
		}

		// Ensure the file is opened
		if err := w.EnsureFileOpened(filePath); err != nil {
			w.Log("Failed to open file: %v", err)
			return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
		}

		// Ensure project is initialized
		if err := w.EnsureProjectInitialized(filePath); err != nil {
			w.Log("Failed to initialize project: %v", err)
			return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
		}
	} else {
		w.Log("Virtual URI detected, forwarding directly: %s", params.TextDocument.URI)
	}

	// Transform to AL-specific command
	alParams := ALGotoDefinitionParams{
		TextDocumentPositionParams: params,
	}

	response, err := w.SendRequestToLSP("al/gotodefinition", alParams)
	if err != nil {
		w.Log("Failed to send definition request: %v", err)
		return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
	}

	// Return response with original request ID
	if response.Error != nil {
		return nil, &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error:   response.Error,
		}
	}

	// Check if result is empty - try fallback using documentSymbol
	if isEmptyDefinitionResult(response.Result) {
		w.Log("Definition result empty, trying documentSymbol fallback")

		// Get symbol name via hover
		hoverResp, err := w.SendRequestToLSP("textDocument/hover", params)
		if err == nil && hoverResp.Error == nil && hoverResp.Result != nil {
			symbolName := extractSymbolNameFromHover(hoverResp.Result)
			if symbolName != "" {
				w.Log("Extracted symbol name from hover: %s", symbolName)

				// Get document symbols
				docSymbolParams := struct {
					TextDocument TextDocumentIdentifier `json:"textDocument"`
				}{
					TextDocument: params.TextDocument,
				}
				symbolsResp, err := w.SendRequestToLSP("textDocument/documentSymbol", docSymbolParams)
				if err == nil && symbolsResp.Error == nil && symbolsResp.Result != nil {
					if location := findSymbolLocation(symbolsResp.Result, symbolName, params.TextDocument.URI); location != nil {
						w.Log("Found symbol via documentSymbol fallback: %s", symbolName)
						locationJSON, _ := json.Marshal(location)
						return &Message{
							JSONRPC: "2.0",
							ID:      msg.ID,
							Result:  locationJSON,
						}, nil
					}
				}
			}
		}
	}

	// Rewrite al-preview:/ URIs in the definition response to materialized
	// file:// URIs so Claude Code's filesystem-existence pre-flight check
	// on the LSP tool surface passes (it rejects al-preview:/ paths with
	// "File does not exist" before the wrapper sees subsequent requests).
	finalResult := rewriteDefinitionPreviewURIs(w, response.Result)

	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  finalResult,
	}, nil
}

// rewriteDefinitionPreviewURIs walks a definition response and replaces
// any al-preview:/ URI with the corresponding file:// URI from the
// preview cache. Materializes the .al source on demand.
//
// Definition results can be Location, Location[], LocationLink, or
// LocationLink[]. We parse defensively and only rewrite the `uri` /
// `targetUri` fields when they're al-preview:/.
func rewriteDefinitionPreviewURIs(w WrapperInterface, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return raw
	}
	cache := w.PreviewCache()
	if cache == nil {
		return raw
	}
	// AL LSP's packageCachePaths come from al/setActiveWorkspace — we
	// rebuild them via the same discovery helper used there.
	folders := w.WorkspaceFolders()
	if len(folders) == 0 {
		return raw
	}
	projectRoot, err := FileURIToPath(folders[0].URI)
	if err != nil {
		return raw
	}
	packagePaths := DiscoverPackageCachePaths(projectRoot)

	// Try array first, then single object.
	var arr []map[string]interface{}
	if err := json.Unmarshal(raw, &arr); err == nil {
		changed := false
		for i := range arr {
			if rewritePreviewURIFields(w, cache, packagePaths, arr[i]) {
				changed = true
			}
		}
		if !changed {
			return raw
		}
		out, err := json.Marshal(arr)
		if err != nil {
			return raw
		}
		return out
	}

	var single map[string]interface{}
	if err := json.Unmarshal(raw, &single); err == nil {
		if rewritePreviewURIFields(w, cache, packagePaths, single) {
			out, err := json.Marshal(single)
			if err != nil {
				return raw
			}
			return out
		}
	}
	return raw
}

// translateCachePathToVirtual checks whether `uri` is a file:// URI pointing
// at the al-preview cache (i.e. a path under <workspace>/.al-preview-cache).
// When it is, returns the recovered al-preview:/ URI so downstream code can
// route the request through the Phase A virtual-URI handling.
//
// Returns (uri, false) for everything else (real source files, already-
// virtual URIs, missing cache, etc.).
//
// Rationale: Claude Code's LSP tool surface performs a filesystem-existence
// check on `filePath` before forwarding the call to the wrapper. Real
// al-preview:/ URIs fail that check. The wrapper materializes the .al
// source under .al-preview-cache so the check passes, then translates the
// cache path back to the original virtual URI here so the AL LSP (which
// only knows the virtual URI) gets the right input.
func translateCachePathToVirtual(w WrapperInterface, uri string) (string, bool) {
	if IsVirtualURI(uri) {
		return uri, false
	}
	cache := w.PreviewCache()
	if cache == nil {
		return uri, false
	}
	filePath, err := FileURIToPath(uri)
	if err != nil {
		return uri, false
	}
	original, ok := cache.resolveCachePath(filePath)
	if !ok {
		return uri, false
	}
	w.Log("translateCachePathToVirtual: %s -> %s", uri, original)
	return original, true
}

// rewritePreviewURIFields materializes any al-preview:/ URI inside a
// Location or LocationLink object and replaces the URI field with the
// resulting file:// URI. Returns true when any field was rewritten.
func rewritePreviewURIFields(
	w WrapperInterface,
	cache *previewCache,
	packagePaths []string,
	obj map[string]interface{},
) bool {
	changed := false
	for _, field := range []string{"uri", "targetUri"} {
		v, ok := obj[field].(string)
		if !ok || !strings.HasPrefix(v, "al-preview:") {
			continue
		}
		mapped, err := cache.materialize(v, packagePaths)
		if err != nil {
			w.Log("previewCache: failed to materialize %s: %v", v, err)
			continue
		}
		obj[field] = mapped
		changed = true
		w.Log("Rewrote definition %s: %s -> %s", field, v, mapped)
	}
	return changed
}

// isEmptyDefinitionResult checks if a definition result is empty (null or empty array)
func isEmptyDefinitionResult(result json.RawMessage) bool {
	if result == nil || len(result) == 0 {
		return true
	}
	// Check for JSON null
	if string(result) == "null" {
		return true
	}
	// Check for empty array
	var arr []interface{}
	if err := json.Unmarshal(result, &arr); err == nil && len(arr) == 0 {
		return true
	}
	return false
}

// extractSymbolNameFromHover extracts the symbol name from a hover response
func extractSymbolNameFromHover(result json.RawMessage) string {
	var hover HoverResponse
	if err := json.Unmarshal(result, &hover); err != nil {
		return ""
	}

	content := hover.Contents.Value
	if content == "" {
		return ""
	}

	// AL hover typically returns markdown like:
	// "procedure TranslateEmailWithAI(...)" or
	// "local procedure Translate(...)"
	// Extract the procedure/trigger/field name

	// Pattern to match AL declarations
	patterns := []string{
		// procedure Name or local procedure Name
		`(?:local\s+)?procedure\s+("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)`,
		// trigger OnRun or OnInsert etc
		`trigger\s+("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)`,
		// field "Name" or field Name
		`field\s*\([^)]+\)\s+("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)`,
		// var Name: Type - variable declarations
		`var\s+("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)\s*:`,
		// Generic: first identifier in the content (fallback)
		`^[^A-Za-z_"]*("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(content)
		if len(matches) > 1 {
			name := matches[1]
			// Remove quotes if present
			if strings.HasPrefix(name, "\"") && strings.HasSuffix(name, "\"") {
				name = name[1 : len(name)-1]
			}
			return name
		}
	}

	return ""
}

// findSymbolLocation searches document symbols for a matching name and returns its location
func findSymbolLocation(result json.RawMessage, symbolName string, fileURI string) *Location {
	// Try parsing as DocumentSymbol[] (hierarchical)
	var docSymbols []DocumentSymbol
	if err := json.Unmarshal(result, &docSymbols); err == nil {
		if loc := findInDocumentSymbols(docSymbols, symbolName, fileURI); loc != nil {
			return loc
		}
	}

	// Try parsing as SymbolInformation[] (flat)
	var symbolInfos []SymbolInformation
	if err := json.Unmarshal(result, &symbolInfos); err == nil {
		for _, sym := range symbolInfos {
			if strings.EqualFold(sym.Name, symbolName) || strings.EqualFold(cleanSymbolName(sym.Name), symbolName) {
				return &sym.Location
			}
		}
	}

	return nil
}

// findInDocumentSymbols recursively searches DocumentSymbol tree
func findInDocumentSymbols(symbols []DocumentSymbol, symbolName string, fileURI string) *Location {
	for _, sym := range symbols {
		cleanedName := cleanSymbolName(sym.Name)
		if strings.EqualFold(sym.Name, symbolName) || strings.EqualFold(cleanedName, symbolName) {
			return &Location{
				URI:   fileURI,
				Range: sym.SelectionRange,
			}
		}
		// Search children
		if len(sym.Children) > 0 {
			if loc := findInDocumentSymbols(sym.Children, symbolName, fileURI); loc != nil {
				return loc
			}
		}
	}
	return nil
}

// cleanSymbolName removes parameters and return type from symbol name
// e.g., "TranslateEmailWithAI(...)" -> "TranslateEmailWithAI"
func cleanSymbolName(name string) string {
	if idx := strings.Index(name, "("); idx > 0 {
		return strings.TrimSpace(name[:idx])
	}
	return name
}

// HoverHandler handles textDocument/hover
type HoverHandler struct{}

func (h *HoverHandler) ShouldHandle(method string) bool {
	return method == "textDocument/hover"
}

func (h *HoverHandler) Handle(msg *Message, w WrapperInterface) (*Message, *Message) {
	var params TextDocumentPositionParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		w.Log("Failed to parse hover params: %v", err)
		return nil, NewErrorResponse(msg.ID, InvalidParams, "Invalid parameters")
	}

	// If the request targets a materialized preview-cache file, recover the
	// original al-preview:/ URI so AL LSP recognizes it as a virtual document.
	if virtual, ok := translateCachePathToVirtual(w, params.TextDocument.URI); ok {
		params.TextDocument.URI = virtual
	}

	// Virtual URIs (e.g. al-preview:) are dependency documents managed by the AL LSP
	if !IsVirtualURI(params.TextDocument.URI) {
		filePath, err := FileURIToPath(params.TextDocument.URI)
		if err != nil {
			w.Log("Failed to convert URI: %v", err)
			return nil, NewErrorResponse(msg.ID, InternalError, "Invalid file URI")
		}

		// Ensure the file is opened
		if err := w.EnsureFileOpened(filePath); err != nil {
			w.Log("Failed to open file: %v", err)
			return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
		}

		// Ensure project is initialized
		if err := w.EnsureProjectInitialized(filePath); err != nil {
			w.Log("Failed to initialize project: %v", err)
			return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
		}
	} else {
		w.Log("Virtual URI detected, forwarding directly: %s", params.TextDocument.URI)
	}

	// Forward to AL LSP
	response, err := w.SendRequestToLSP("textDocument/hover", params)
	if err != nil {
		w.Log("Hover request failed: %v, returning null", err)
		// Return null result instead of error - hover is optional
		return &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  json.RawMessage("null"),
		}, nil
	}

	if response.Error != nil {
		w.Log("Hover response error: %s, returning null", response.Error.Message)
		// Return null result instead of error - hover is optional
		return &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  json.RawMessage("null"),
		}, nil
	}

	normalized := normalizeHoverResult(response.Result)

	// Enrich with full XML doc comments (params, returns, remarks, etc.)
	enriched := enrichHoverWithXmlDoc(w, normalized, params)

	// Enrich field hovers with properties (Caption, TableRelation, CalcFormula, etc.)
	enriched = enrichFieldHover(w, enriched, params)

	// Enrich action hovers with properties (RunObject, ToolTip, Image, etc.)
	enriched = enrichActionHover(w, enriched, params)

	// Enrich event-name literals inside [EventSubscriber(...)] — agent's most
	// common discovery path. One hover call returns the publisher's full
	// signature + attribute kind + source app.
	enriched = enrichEventReferenceHover(w, enriched, params)

	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  enriched,
	}, nil
}

// enrichEventReferenceHover detects when the cursor is on the event-name
// string literal of an [EventSubscriber(...)] attribute. When it is, ask
// al-call-hierarchy to look up the publisher in its dependency index and
// append (or set) the resulting signature + attribute tag in the hover.
//
// Returns the original hover unchanged when:
//   - the cursor isn't on an event reference,
//   - the call-hierarchy server isn't initialized,
//   - the publisher couldn't be resolved.
func enrichEventReferenceHover(
	w WrapperInterface,
	hoverResult json.RawMessage,
	params TextDocumentPositionParams,
) json.RawMessage {
	ch := w.GetCallHierarchyServer()
	if ch == nil || !ch.IsInitialized() {
		return hoverResult
	}
	resp, err := ch.Request("al-call-hierarchy/eventReferenceAtPosition", map[string]interface{}{
		"uri":      params.TextDocument.URI,
		"position": params.Position,
	})
	if err != nil || resp == nil || resp.Error != nil || resp.Result == nil || string(resp.Result) == "null" {
		return hoverResult
	}
	var match struct {
		PublisherObjectType string  `json:"publisherObjectType"`
		PublisherObject     string  `json:"publisherObject"`
		EventName           string  `json:"eventName"`
		Signature           *string `json:"signature"`
		AttributeKind       *string `json:"attributeKind"`
		AppName             *string `json:"appName"`
		AppVersion          *string `json:"appVersion"`
	}
	if err := json.Unmarshal(resp.Result, &match); err != nil {
		return hoverResult
	}

	// Build a markdown block summarizing the event reference.
	var b strings.Builder
	tag := ""
	if match.AttributeKind != nil {
		tag = *match.AttributeKind + " "
	}
	b.WriteString(fmt.Sprintf("**%s%s**\n\n", tag, match.EventName))
	if match.Signature != nil {
		b.WriteString("```al\n")
		b.WriteString(*match.Signature)
		b.WriteString("\n```\n\n")
	}
	b.WriteString(fmt.Sprintf("Publisher: %s \"%s\"", match.PublisherObjectType, match.PublisherObject))
	if match.AppName != nil {
		ver := ""
		if match.AppVersion != nil {
			ver = " " + *match.AppVersion
		}
		b.WriteString(fmt.Sprintf("  \nSource: %s%s", *match.AppName, ver))
	}
	enrichment := b.String()

	return appendHoverMarkdown(hoverResult, enrichment)
}

// appendHoverMarkdown returns the hover result with `extra` appended to its
// MarkupContent value (or replaces a null hover with one containing extra).
func appendHoverMarkdown(hoverResult json.RawMessage, extra string) json.RawMessage {
	if extra == "" {
		return hoverResult
	}
	if len(hoverResult) == 0 || string(hoverResult) == "null" {
		// AL LSP returned nothing — synthesize a hover entirely from the enrichment.
		return makeHoverMarkdown(extra)
	}
	var hover struct {
		Contents struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"contents"`
		Range json.RawMessage `json:"range,omitempty"`
	}
	if err := json.Unmarshal(hoverResult, &hover); err != nil {
		return makeHoverMarkdown(extra)
	}
	if hover.Contents.Value == "" {
		hover.Contents.Kind = "markdown"
		hover.Contents.Value = extra
	} else {
		hover.Contents.Kind = "markdown"
		hover.Contents.Value = hover.Contents.Value + "\n\n---\n\n" + extra
	}
	out, err := json.Marshal(hover)
	if err != nil {
		return hoverResult
	}
	return out
}

// makeHoverMarkdown builds a minimal Hover with markdown contents.
func makeHoverMarkdown(value string) json.RawMessage {
	hover := struct {
		Contents struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"contents"`
	}{}
	hover.Contents.Kind = "markdown"
	hover.Contents.Value = value
	out, _ := json.Marshal(hover)
	return out
}

// normalizeHoverResult converts deprecated hover content formats to MarkupContent.
// Claude Code's LSP client expects { kind: "markdown"|"plaintext", value: "..." }
// but the AL LSP may return MarkedString, plain string, or array formats.
func normalizeHoverResult(result json.RawMessage) json.RawMessage {
	if result == nil || string(result) == "null" {
		return result
	}

	// Parse the hover result to inspect contents
	var hover struct {
		Contents json.RawMessage `json:"contents"`
		Range    json.RawMessage `json:"range,omitempty"`
	}
	if err := json.Unmarshal(result, &hover); err != nil {
		return result // Can't parse, return as-is
	}
	if hover.Contents == nil || string(hover.Contents) == "null" {
		return json.RawMessage("null") // Return null hover, not a hover with null contents
	}

	normalized := normalizeContents(hover.Contents)
	if normalized == nil {
		// normalizeContents returns nil when content is already MarkupContent
		// or when it can't recognize the format. Validate the original is safe.
		var check struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(hover.Contents, &check); err == nil && check.Kind != "" {
			return result // Already valid MarkupContent, safe to return original
		}
		// Unknown format — force to markdown to prevent Claude Code crash
		var raw interface{}
		if err := json.Unmarshal(hover.Contents, &raw); err == nil {
			str := fmt.Sprintf("%v", raw)
			normalized = makeMarkupContent(str)
		} else {
			return json.RawMessage("null")
		}
	}

	// Rebuild the hover object with normalized contents
	out := map[string]json.RawMessage{
		"contents": normalized,
	}
	if hover.Range != nil && string(hover.Range) != "null" {
		out["range"] = hover.Range
	}
	res, err := json.Marshal(out)
	if err != nil {
		return result
	}
	return res
}

// normalizeContents converts hover contents to MarkupContent format.
func normalizeContents(contents json.RawMessage) json.RawMessage {
	// Try MarkupContent first: { kind: "...", value: "..." }
	var markup struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(contents, &markup); err == nil && markup.Kind != "" {
		return nil // Already MarkupContent, no change needed
	}

	// Try MarkedString: { language: "...", value: "..." }
	var marked struct {
		Language string `json:"language"`
		Value    string `json:"value"`
	}
	if err := json.Unmarshal(contents, &marked); err == nil && marked.Language != "" {
		return makeMarkupContent("```" + marked.Language + "\n" + marked.Value + "\n```")
	}

	// Try plain string
	var str string
	if err := json.Unmarshal(contents, &str); err == nil {
		return makeMarkupContent(str)
	}

	// Try array of MarkedString or strings
	var arr []json.RawMessage
	if err := json.Unmarshal(contents, &arr); err == nil && len(arr) > 0 {
		var parts []string
		for _, item := range arr {
			// Try MarkedString
			var ms struct {
				Language string `json:"language"`
				Value    string `json:"value"`
			}
			if err := json.Unmarshal(item, &ms); err == nil && ms.Language != "" {
				parts = append(parts, "```"+ms.Language+"\n"+ms.Value+"\n```")
				continue
			}
			// Try plain string
			var s string
			if err := json.Unmarshal(item, &s); err == nil {
				parts = append(parts, s)
				continue
			}
			// Try MarkupContent in array
			var mc struct {
				Kind  string `json:"kind"`
				Value string `json:"value"`
			}
			if err := json.Unmarshal(item, &mc); err == nil && mc.Kind != "" {
				parts = append(parts, mc.Value)
				continue
			}
		}
		if len(parts) > 0 {
			return makeMarkupContent(strings.Join(parts, "\n\n---\n\n"))
		}
	}

	return nil // Unknown format, don't modify
}

// makeMarkupContent creates a MarkupContent JSON with kind "markdown".
func makeMarkupContent(value string) json.RawMessage {
	mc := struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}{
		Kind:  "markdown",
		Value: value,
	}
	data, _ := json.Marshal(mc)
	return data
}

// DocumentSymbolHandler handles textDocument/documentSymbol
type DocumentSymbolHandler struct{}

func (h *DocumentSymbolHandler) ShouldHandle(method string) bool {
	return method == "textDocument/documentSymbol"
}

func (h *DocumentSymbolHandler) Handle(msg *Message, w WrapperInterface) (*Message, *Message) {
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		w.Log("Failed to parse documentSymbol params: %v", err)
		return nil, NewErrorResponse(msg.ID, InvalidParams, "Invalid parameters")
	}

	// If the request targets a materialized preview-cache file, recover the
	// original al-preview:/ URI so the call-hierarchy fallback can synthesize
	// dependency document symbols.
	if virtual, ok := translateCachePathToVirtual(w, params.TextDocument.URI); ok {
		params.TextDocument.URI = virtual
	}

	// Virtual URIs (e.g. al-preview:) are dependency documents managed by the AL LSP
	if !IsVirtualURI(params.TextDocument.URI) {
		filePath, err := FileURIToPath(params.TextDocument.URI)
		if err != nil {
			w.Log("Failed to convert URI: %v", err)
			return nil, NewErrorResponse(msg.ID, InternalError, "Invalid file URI")
		}

		// Ensure the file is opened
		if err := w.EnsureFileOpened(filePath); err != nil {
			w.Log("Failed to open file: %v", err)
			return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
		}

		// Ensure project is initialized
		if err := w.EnsureProjectInitialized(filePath); err != nil {
			w.Log("Failed to initialize project: %v", err)
			return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
		}
	} else {
		w.Log("Virtual URI detected, forwarding directly: %s", params.TextDocument.URI)
	}

	// Forward to AL LSP
	response, err := w.SendRequestToLSP("textDocument/documentSymbol", params)
	if err != nil {
		w.Log("Failed to send documentSymbol request: %v", err)
		// For virtual URIs, AL LSP sometimes silently drops requests.
		// Fall through so we can try the al-call-hierarchy fallback.
		response = &Message{Result: json.RawMessage("[]")}
	}

	if response.Error != nil {
		// Same idea: if AL LSP errored on a virtual URI, the fallback may
		// still produce useful data. Otherwise propagate the error.
		if !IsVirtualURI(params.TextDocument.URI) {
			return nil, &Message{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Error:   response.Error,
			}
		}
		w.Log("AL LSP error on virtual URI %s: %s — trying call-hierarchy fallback",
			params.TextDocument.URI, response.Error.Message)
		response.Result = json.RawMessage("[]")
		response.Error = nil
	}

	// Virtual-URI fallback: dependency objects can be served from the
	// al-call-hierarchy dependency index even when the AL LSP returns nothing.
	if IsVirtualURI(params.TextDocument.URI) {
		if synth := tryDependencyDocumentSymbol(w, params.TextDocument.URI); synth != nil {
			alCount := countDocumentSymbols(response.Result)
			synthCount := countDocumentSymbols(synth)
			if synthCount > alCount {
				w.Log("documentSymbol: using call-hierarchy fallback (synth=%d, AL=%d) for %s",
					synthCount, alCount, params.TextDocument.URI)
				return &Message{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Result:  synth,
				}, nil
			}
		}
	}

	// Local-file overlay: re-tag procedures decorated with [IntegrationEvent]
	// or [BusinessEvent] as SymbolKind.Event (24) and prepend the attribute
	// tag to the detail string. Matches Phase A behavior for dependency
	// objects so the agent sees events consistently across local + external code.
	enriched := response.Result
	if !IsVirtualURI(params.TextDocument.URI) {
		enriched = overlayLocalEventPublishers(w, response.Result, params.TextDocument.URI)
	}

	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  enriched,
	}, nil
}

// overlayLocalEventPublishers rewrites the documentSymbol response so any
// procedure that has [IntegrationEvent]/[BusinessEvent]/[InternalEvent]
// attached is tagged as kind:Event with a "[IntegrationEvent] " prefix on
// detail. Returns the original result unchanged when no publishers are found
// or the call-hierarchy server is unavailable.
func overlayLocalEventPublishers(w WrapperInterface, raw json.RawMessage, uri string) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return raw
	}
	ch := w.GetCallHierarchyServer()
	if ch == nil || !ch.IsInitialized() {
		return raw
	}
	resp, err := ch.Request("al-call-hierarchy/eventPublishersInFile", map[string]interface{}{
		"uri": uri,
	})
	if err != nil || resp == nil || resp.Error != nil || resp.Result == nil {
		return raw
	}
	// Parse al-call-hierarchy's publishers list (camelCase JSON).
	var pubs []struct {
		Name   string `json:"name"`
		Detail string `json:"detail"`
		Kind   uint32 `json:"kind"`
		Range  struct {
			Start struct{ Line, Character uint32 } `json:"start"`
			End   struct{ Line, Character uint32 } `json:"end"`
		} `json:"range"`
		SelectionRange struct {
			Start struct{ Line, Character uint32 } `json:"start"`
			End   struct{ Line, Character uint32 } `json:"end"`
		} `json:"selectionRange"`
	}
	if err := json.Unmarshal(resp.Result, &pubs); err != nil || len(pubs) == 0 {
		return raw
	}
	pubByName := make(map[string]int, len(pubs))
	for i, p := range pubs {
		pubByName[strings.ToLower(p.Name)] = i
	}
	w.Log("overlayLocalEventPublishers: %d publishers in %s", len(pubs), uri)

	// AL LSP's documentSymbol can return either DocumentSymbol[] (hierarchical,
	// with `children`) or SymbolInformation[] (flat). Handle both by parsing
	// into a generic []map and rewriting matching entries in place.
	var entries []map[string]interface{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return raw
	}
	rewritten := 0
	walkAndOverlay(entries, pubs, pubByName, &rewritten)
	if rewritten == 0 {
		return raw
	}
	out, err := json.Marshal(entries)
	if err != nil {
		w.Log("overlayLocalEventPublishers: failed to marshal: %v", err)
		return raw
	}
	w.Log("overlayLocalEventPublishers: tagged %d procedures as Event", rewritten)
	return out
}

// walkAndOverlay rewrites kind+detail on entries whose `name` matches a known
// event publisher. Recurses into `children` so hierarchical DocumentSymbol
// trees get fully covered.
func walkAndOverlay(
	entries []map[string]interface{},
	pubs []struct {
		Name   string `json:"name"`
		Detail string `json:"detail"`
		Kind   uint32 `json:"kind"`
		Range  struct {
			Start struct{ Line, Character uint32 } `json:"start"`
			End   struct{ Line, Character uint32 } `json:"end"`
		} `json:"range"`
		SelectionRange struct {
			Start struct{ Line, Character uint32 } `json:"start"`
			End   struct{ Line, Character uint32 } `json:"end"`
		} `json:"selectionRange"`
	},
	pubByName map[string]int,
	rewritten *int,
) {
	for _, entry := range entries {
		name, _ := entry["name"].(string)
		if name != "" {
			cleanName := strings.TrimSpace(strings.Trim(name, "\""))
			// AL LSP names may include parens — drop them for the lookup.
			if idx := strings.Index(cleanName, "("); idx > 0 {
				cleanName = strings.TrimSpace(cleanName[:idx])
			}
			if pi, ok := pubByName[strings.ToLower(cleanName)]; ok {
				p := pubs[pi]
				entry["kind"] = p.Kind
				existingDetail, _ := entry["detail"].(string)
				if existingDetail == "" {
					entry["detail"] = p.Detail
				} else if !strings.Contains(existingDetail, "[IntegrationEvent]") &&
					!strings.Contains(existingDetail, "[BusinessEvent]") &&
					!strings.Contains(existingDetail, "[InternalEvent]") {
					// Prepend the event tag from p.Detail (which starts with it).
					tag := p.Detail
					if i := strings.Index(tag, " "); i > 0 {
						tag = tag[:i]
					}
					entry["detail"] = tag + " " + existingDetail
				}
				*rewritten++
			}
		}
		// Recurse into children if present (DocumentSymbol[] form).
		if children, ok := entry["children"].([]interface{}); ok {
			childMaps := make([]map[string]interface{}, 0, len(children))
			for _, c := range children {
				if cm, ok := c.(map[string]interface{}); ok {
					childMaps = append(childMaps, cm)
				}
			}
			walkAndOverlay(childMaps, pubs, pubByName, rewritten)
		}
	}
}

// tryDependencyDocumentSymbol asks the al-call-hierarchy server to synthesize
// a DocumentSymbol[] for a dependency object addressed by an al-preview:/ URI.
// Returns nil if the call-hierarchy server isn't available, isn't initialized,
// or returns no results.
func tryDependencyDocumentSymbol(w WrapperInterface, uri string) json.RawMessage {
	ch := w.GetCallHierarchyServer()
	if ch == nil || !ch.IsInitialized() {
		return nil
	}
	resp, err := ch.Request("al-call-hierarchy/dependencyDocumentSymbol", map[string]interface{}{
		"uri": uri,
	})
	if err != nil {
		w.Log("dependencyDocumentSymbol RPC failed: %v", err)
		return nil
	}
	if resp == nil || resp.Error != nil || resp.Result == nil {
		return nil
	}
	if countDocumentSymbols(resp.Result) == 0 {
		return nil
	}
	return resp.Result
}

// countDocumentSymbols returns the length of a DocumentSymbol[] or
// SymbolInformation[] JSON array. Returns 0 for null, empty, or unparseable.
func countDocumentSymbols(raw json.RawMessage) int {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return 0
	}
	return len(arr)
}

// emptyWorkspaceSymbolWarned is set the first time we surface the empty-query
// warning so users see the anomaly without being spammed on every call.
var emptyWorkspaceSymbolWarned atomic.Bool

func warnOnceEmptyWorkspaceSymbol(w WrapperInterface) {
	if !emptyWorkspaceSymbolWarned.CompareAndSwap(false, true) {
		return
	}
	w.SendNotificationToClient("window/showMessage", map[string]interface{}{
		"type": 2, // Warning
		"message": "workspaceSymbol called with an empty query — the AL LSP requires " +
			"a non-empty search term, so this call returns no results. Claude Code " +
			"2.1.x+ passes the query through (anthropics/claude-code#17149); if you see " +
			"this on a recent Claude Code, the query was genuinely empty. Pass a symbol " +
			"name, or use documentSymbol(file) for per-file symbols.",
	})
}

// symbolSearchEverReturnedResults flips true the first time al/symbolSearch
// yields >0 results in this process, and never flips back.
//
// Until it is set, a 0-result response is ambiguous: the symbol genuinely may
// not exist, OR the AL LS symbol index is still warming up. The AL LS builds
// its workspace symbol index asynchronously after initialize, so the first
// workspace symbol search of a session can race the index and come back empty
// even though the symbol exists (observed: a search 40ms after initialize
// returns [], the same search seconds later returns matches). Once any search
// returns results we know the index is warm and treat 0 as a genuine miss.
var symbolSearchEverReturnedResults atomic.Bool

// coldSymbolSearchBackoffs are the waits between retries when al/symbolSearch
// returns 0 results and the index has never warmed this session. One-time
// cost at session start; bounded so the (synchronous) client read loop is
// never stalled for more than their sum (~1.6s).
var coldSymbolSearchBackoffs = []time.Duration{
	300 * time.Millisecond,
	500 * time.Millisecond,
	800 * time.Millisecond,
}

// convertALSymbols maps al/symbolSearch results to LSP SymbolInformation.
func convertALSymbols(in []ALSymbol) []SymbolInformation {
	symbols := make([]SymbolInformation, 0, len(in))
	for _, s := range in {
		uri := s.Path
		if uri != "" {
			uri = strings.ReplaceAll(uri, "\\", "/")
			if !strings.HasPrefix(uri, "file://") {
				if !strings.HasPrefix(uri, "/") {
					uri = "/" + uri
				}
				uri = "file://" + uri
			}
		}
		symbols = append(symbols, SymbolInformation{
			Name:          s.Name,
			Kind:          alKindToLSPKind(s.Kind),
			ContainerName: s.ContainerName,
			Location: Location{
				URI:   uri,
				Range: Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 0}},
			},
		})
	}
	return symbols
}

// WorkspaceSymbolHandler handles workspace/symbol
type WorkspaceSymbolHandler struct{}

func (h *WorkspaceSymbolHandler) ShouldHandle(method string) bool {
	return method == "workspace/symbol"
}

func (h *WorkspaceSymbolHandler) Handle(msg *Message, w WrapperInterface) (*Message, *Message) {
	var params WorkspaceSymbolParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		w.Log("Failed to parse workspaceSymbol params: %v", err)
		return nil, NewErrorResponse(msg.ID, InvalidParams, "Invalid parameters")
	}

	query := params.Query

	// Empty/whitespace queries: return [] AND surface the anomaly.
	//
	// History: Claude Code <2.1.x hardcoded an empty query for workspaceSymbol
	// (anthropics/claude-code#17149) — its LSP tool surface had no `query`
	// field, so the agent literally could not pass a search term. That is
	// fixed: 2.1.x+ passes the query through. We keep this guard because the
	// AL LSP still rejects empty queries, and an empty query can still arrive
	// from an old Claude Code, a non-Claude LSP client, or genuinely empty
	// input.
	//
	// Returning -32602 caused agents to retry-loop. Returning [] silently
	// would hide the anomaly. Compromise: return [] (agent moves on) AND raise
	// a window/showMessage warning the first time per session. The log line
	// stays loud either way.
	if strings.TrimSpace(query) == "" {
		w.Log("Empty workspace/symbol query — the AL LSP requires a non-empty search term. " +
			"Returning [] so agents don't retry-loop; user is warned via window/showMessage. " +
			"Claude Code 2.1.x+ passes the query through (anthropics/claude-code#17149).")
		warnOnceEmptyWorkspaceSymbol(w)
		return &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  json.RawMessage("[]"),
		}, nil
	}

	// Workaround: Claude Code sometimes sends file paths instead of symbol names
	if strings.Contains(query, "/") || strings.Contains(query, "\\") {
		query = ExtractSymbolFromPath(query)
		w.Log("Extracted symbol from path: %s", query)
	}

	// Use al/symbolSearch exclusively - workspace/symbol deadlocks the AL LSP
	// Confirmed: deadlock persists even with proper server request handling
	//
	// Retry on an empty result while the symbol index may still be cold (see
	// symbolSearchEverReturnedResults). Once any search has returned results
	// this session, 0 is a genuine miss and we return immediately.
	var symbols []SymbolInformation
	for attempt := 0; ; attempt++ {
		w.Log("Sending al/symbolSearch for query: %s (attempt %d)", query, attempt+1)
		response, err := w.SendRequestToLSP("al/symbolSearch", ALSymbolSearchParams{Query: query})
		if err != nil {
			w.Log("Failed to send al/symbolSearch request: %v", err)
			return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
		}

		if response.Error != nil {
			return nil, &Message{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Error:   response.Error,
			}
		}

		// Convert al/symbolSearch response to standard LSP SymbolInformation[]
		var searchResult ALSymbolSearchResponse
		if err := json.Unmarshal(response.Result, &searchResult); err != nil {
			w.Log("Failed to parse al/symbolSearch response: %v", err)
			// Return raw result as fallback
			return &Message{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result:  response.Result,
			}, nil
		}

		symbols = convertALSymbols(searchResult.Symbols)
		if len(symbols) > 0 {
			symbolSearchEverReturnedResults.Store(true)
			break
		}

		// 0 results. Retry only while the index has never warmed this session
		// and we have backoffs left — otherwise this is a genuine miss.
		if symbolSearchEverReturnedResults.Load() || attempt >= len(coldSymbolSearchBackoffs) {
			break
		}
		backoff := coldSymbolSearchBackoffs[attempt]
		w.Log("al/symbolSearch returned 0 results and the symbol index has never warmed "+
			"this session; retrying in %s to ride out AL LS index warmup (attempt %d/%d)",
			backoff, attempt+1, len(coldSymbolSearchBackoffs))
		time.Sleep(backoff)
	}

	w.Log("Converted %d al/symbolSearch results to SymbolInformation", len(symbols))

	resultJSON, err := json.Marshal(symbols)
	if err != nil {
		w.Log("Failed to marshal symbols: %v", err)
		return nil, NewErrorResponse(msg.ID, InternalError, "Failed to marshal results")
	}

	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  json.RawMessage(resultJSON),
	}, nil
}

// ReferencesHandler handles textDocument/references
type ReferencesHandler struct{}

func (h *ReferencesHandler) ShouldHandle(method string) bool {
	return method == "textDocument/references"
}

func (h *ReferencesHandler) Handle(msg *Message, w WrapperInterface) (*Message, *Message) {
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Position     Position               `json:"position"`
		Context      struct {
			IncludeDeclaration bool `json:"includeDeclaration"`
		} `json:"context"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		w.Log("Failed to parse references params: %v", err)
		return nil, NewErrorResponse(msg.ID, InvalidParams, "Invalid parameters")
	}

	// Virtual URIs (e.g. al-preview:) are dependency documents managed by the AL LSP
	if !IsVirtualURI(params.TextDocument.URI) {
		filePath, err := FileURIToPath(params.TextDocument.URI)
		if err != nil {
			w.Log("Failed to convert URI: %v", err)
			return nil, NewErrorResponse(msg.ID, InternalError, "Invalid file URI")
		}

		// Ensure the file is opened
		if err := w.EnsureFileOpened(filePath); err != nil {
			w.Log("Failed to open file: %v", err)
			return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
		}

		// Ensure project is initialized
		if err := w.EnsureProjectInitialized(filePath); err != nil {
			w.Log("Failed to initialize project: %v", err)
			return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
		}
	} else {
		w.Log("Virtual URI detected, forwarding directly: %s", params.TextDocument.URI)
	}

	// Forward to AL LSP
	response, err := w.SendRequestToLSP("textDocument/references", params)
	if err != nil {
		w.Log("Failed to send references request: %v", err)
		return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
	}

	if response.Error != nil {
		return nil, &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error:   response.Error,
		}
	}

	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  response.Result,
	}, nil
}

// CallHierarchyHandler handles call hierarchy methods via al-call-hierarchy server
type CallHierarchyHandler struct {
	methods map[string]bool
}

func NewCallHierarchyHandler() *CallHierarchyHandler {
	return &CallHierarchyHandler{
		methods: map[string]bool{
			"textDocument/prepareCallHierarchy": true,
			"callHierarchy/incomingCalls":       true,
			"callHierarchy/outgoingCalls":       true,
		},
	}
}

func (h *CallHierarchyHandler) ShouldHandle(method string) bool {
	return h.methods[method]
}

func (h *CallHierarchyHandler) Handle(msg *Message, w WrapperInterface) (*Message, *Message) {
	// Get call hierarchy server from wrapper
	chServer := w.GetCallHierarchyServer()
	if chServer == nil || !chServer.IsInitialized() {
		w.Log("Call hierarchy server not available for: %s", msg.Method)
		return nil, NewErrorResponse(msg.ID, MethodNotFound,
			"Call hierarchy server not available")
	}

	w.Log("Routing %s to al-call-hierarchy", msg.Method)

	// Parse params
	var params interface{}
	if len(msg.Params) > 0 {
		json.Unmarshal(msg.Params, &params)
	}

	// Forward to al-call-hierarchy
	response, err := chServer.Request(msg.Method, params)
	if err != nil {
		w.Log("Call hierarchy request failed: %v", err)
		return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
	}

	if response.Error != nil {
		return nil, &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error:   response.Error,
		}
	}

	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  response.Result,
	}, nil
}

// CodeLensHandler handles textDocument/codeLens via al-call-hierarchy server
type CodeLensHandler struct{}

func (h *CodeLensHandler) ShouldHandle(method string) bool {
	return method == "textDocument/codeLens"
}

func (h *CodeLensHandler) Handle(msg *Message, w WrapperInterface) (*Message, *Message) {
	// Get call hierarchy server from wrapper
	chServer := w.GetCallHierarchyServer()
	if chServer == nil || !chServer.IsInitialized() {
		w.Log("Call hierarchy server not available for codeLens, returning empty result")
		// Return empty array instead of error - codeLens is optional
		return &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  []byte("[]"),
		}, nil
	}

	w.Log("Routing textDocument/codeLens to al-call-hierarchy")

	// Parse params
	var params interface{}
	if len(msg.Params) > 0 {
		json.Unmarshal(msg.Params, &params)
	}

	// Forward to al-call-hierarchy
	response, err := chServer.Request(msg.Method, params)
	if err != nil {
		w.Log("CodeLens request failed: %v", err)
		// Return empty array on error - codeLens is optional
		return &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  []byte("[]"),
		}, nil
	}

	if response.Error != nil {
		w.Log("CodeLens response error: %v", response.Error)
		// Return empty array on error - codeLens is optional
		return &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  []byte("[]"),
		}, nil
	}

	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  response.Result,
	}, nil
}

// GetDefaultHandlers returns the default set of handlers
// SetActiveWorkspaceHandler handles al/setActiveWorkspace
type SetActiveWorkspaceHandler struct{}

func (h *SetActiveWorkspaceHandler) ShouldHandle(method string) bool {
	return method == "al/setActiveWorkspace"
}

func (h *SetActiveWorkspaceHandler) Handle(msg *Message, w WrapperInterface) (*Message, *Message) {
	// Parse to extract workspace path for logging
	var params struct {
		CurrentWorkspaceFolderPath struct {
			URI  string `json:"uri"`
			Name string `json:"name"`
		} `json:"currentWorkspaceFolderPath"`
	}
	if err := json.Unmarshal(msg.Params, &params); err == nil && params.CurrentWorkspaceFolderPath.URI != "" {
		w.Log("Switching active workspace to: %s (%s)",
			params.CurrentWorkspaceFolderPath.Name,
			params.CurrentWorkspaceFolderPath.URI)
	}

	// Forward to AL LSP
	var rawParams interface{}
	json.Unmarshal(msg.Params, &rawParams)

	response, err := w.SendRequestToLSP("al/setActiveWorkspace", rawParams)
	if err != nil {
		w.Log("al/setActiveWorkspace failed: %v", err)
		return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
	}

	if response.Error != nil {
		return nil, &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error:   response.Error,
		}
	}

	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  response.Result,
	}, nil
}

// DidChangeWorkspaceFoldersHandler handles al/didChangeWorkspaceFolders (notification)
type DidChangeWorkspaceFoldersHandler struct{}

func (h *DidChangeWorkspaceFoldersHandler) ShouldHandle(method string) bool {
	return method == "al/didChangeWorkspaceFolders"
}

func (h *DidChangeWorkspaceFoldersHandler) Handle(msg *Message, w WrapperInterface) (*Message, *Message) {
	w.Log("Forwarding al/didChangeWorkspaceFolders to AL LSP and al-call-hierarchy")

	// Parse added/removed folders to update internal state
	var changeParams struct {
		Added   []WorkspaceFolder `json:"added"`
		Removed []WorkspaceFolder `json:"removed"`
	}
	if err := json.Unmarshal(msg.Params, &changeParams); err == nil {
		w.UpdateWorkspaceFolders(changeParams.Added, changeParams.Removed)
	} else {
		w.Log("Failed to parse workspace folder changes: %v", err)
	}

	// Forward to AL LSP as notification (AL custom format: {added, removed})
	w.SendNotificationToLSP("al/didChangeWorkspaceFolders", msg.Params)

	// Forward to al-call-hierarchy using standard LSP format: {event: {added, removed}}
	chServer := w.GetCallHierarchyServer()
	if chServer != nil && chServer.IsInitialized() {
		// Standard LSP wraps the change in an "event" field
		standardParams := map[string]json.RawMessage{
			"event": msg.Params,
		}
		chServer.SendNotification("workspace/didChangeWorkspaceFolders", standardParams)
	}

	// Notification — no response to client
	return nil, nil
}

func GetDefaultHandlers() []Handler {
	return []Handler{
		&DefinitionHandler{},
		&HoverHandler{},
		&DocumentSymbolHandler{},
		&WorkspaceSymbolHandler{},
		&ReferencesHandler{},
		NewCallHierarchyHandler(),
		&CodeLensHandler{},
		&SetActiveWorkspaceHandler{},
		&DidChangeWorkspaceFoldersHandler{},
	}
}
