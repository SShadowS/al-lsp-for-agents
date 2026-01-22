package wrapper

import (
	"encoding/json"
	"regexp"
	"strings"
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
type ALSymbolSearchParams struct {
	Filter string `json:"filter"`
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
	Name     string   `json:"name"`
	Kind     int      `json:"kind"`
	Location Location `json:"location"`
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

	// GetCallHierarchyServer returns the call hierarchy server (may be nil)
	GetCallHierarchyServer() *CallHierarchyServer

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

	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  response.Result,
	}, nil
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

	// Forward to AL LSP
	response, err := w.SendRequestToLSP("textDocument/hover", params)
	if err != nil {
		w.Log("Failed to send hover request: %v", err)
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

	// Forward to AL LSP
	response, err := w.SendRequestToLSP("textDocument/documentSymbol", params)
	if err != nil {
		w.Log("Failed to send documentSymbol request: %v", err)
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

	// Check for empty query
	if strings.TrimSpace(query) == "" {
		w.Log("Empty workspace/symbol query")
		return nil, NewErrorResponse(msg.ID, InvalidParams,
			"AL Language Server requires a non-empty query for workspace/symbol. "+
				"Please provide a symbol name to search for.")
	}

	// Workaround: Claude Code sometimes sends file paths instead of symbol names
	if strings.Contains(query, "/") || strings.Contains(query, "\\") {
		query = ExtractSymbolFromPath(query)
		w.Log("Extracted symbol from path: %s", query)
	}

	// First try standard workspace/symbol
	response, err := w.SendRequestToLSP("workspace/symbol", WorkspaceSymbolParams{Query: query})
	if err != nil {
		w.Log("Failed to send workspace/symbol request: %v", err)
		return nil, NewErrorResponse(msg.ID, InternalError, err.Error())
	}

	// Check if we got results
	if response.Result != nil {
		var results []interface{}
		if err := json.Unmarshal(response.Result, &results); err == nil && len(results) > 0 {
			return &Message{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result:  response.Result,
			}, nil
		}
	}

	// Fallback to al/symbolSearch
	w.Log("Falling back to al/symbolSearch for query: %s", query)
	response, err = w.SendRequestToLSP("al/symbolSearch", ALSymbolSearchParams{Filter: query})
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

	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  response.Result,
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

// GetDefaultHandlers returns the default set of handlers
func GetDefaultHandlers() []Handler {
	return []Handler{
		&DefinitionHandler{},
		&HoverHandler{},
		&DocumentSymbolHandler{},
		&WorkspaceSymbolHandler{},
		&ReferencesHandler{},
		NewCallHierarchyHandler(),
	}
}
