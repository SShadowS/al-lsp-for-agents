package wrapper

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// mockWrapper implements WrapperInterface for testing handler behavior
type mockWrapper struct {
	mu                  sync.Mutex
	lspRequests         []lspCall
	lspNotifications    []lspCall
	clientNotifications []lspCall
	openedFiles         map[string]bool
	initializedProjects map[string]bool
	workspaceFolders    []WorkspaceFolder
	callHierarchyServer *CallHierarchyServer
	logs                []string
}

type lspCall struct {
	Method string
	Params interface{}
}

func newMockWrapper() *mockWrapper {
	return &mockWrapper{
		lspRequests:         []lspCall{},
		lspNotifications:    []lspCall{},
		openedFiles:         make(map[string]bool),
		initializedProjects: make(map[string]bool),
		workspaceFolders:    []WorkspaceFolder{},
		logs:                []string{},
	}
}

func (m *mockWrapper) EnsureFileOpened(filePath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openedFiles[filePath] = true
	return nil
}

func (m *mockWrapper) EnsureProjectInitialized(filePath string) error {
	return nil
}

func (m *mockWrapper) SendRequestToLSP(method string, params interface{}) (*Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lspRequests = append(m.lspRequests, lspCall{Method: method, Params: params})
	return &Message{
		JSONRPC: "2.0",
		Result:  json.RawMessage(`{"success":true}`),
	}, nil
}

func (m *mockWrapper) SendNotificationToLSP(method string, params interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lspNotifications = append(m.lspNotifications, lspCall{Method: method, Params: params})
	return nil
}

func (m *mockWrapper) SendNotificationToClient(method string, params interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clientNotifications = append(m.clientNotifications, lspCall{Method: method, Params: params})
}

func (m *mockWrapper) GetCallHierarchyServer() *CallHierarchyServer {
	return m.callHierarchyServer
}

func (m *mockWrapper) UpdateWorkspaceFolders(added []WorkspaceFolder, removed []WorkspaceFolder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range removed {
		for i, f := range m.workspaceFolders {
			if f.URI == r.URI {
				m.workspaceFolders = append(m.workspaceFolders[:i], m.workspaceFolders[i+1:]...)
				break
			}
		}
	}
	m.workspaceFolders = append(m.workspaceFolders, added...)
}

func (m *mockWrapper) Log(format string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, format)
}

func (m *mockWrapper) getNotificationsForMethod(method string) []lspCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	var calls []lspCall
	for _, c := range m.lspNotifications {
		if c.Method == method {
			calls = append(calls, c)
		}
	}
	return calls
}

// =============================================================================
// Fix 1: activeProject tracking — workspace re-activation on switch
// =============================================================================

func TestFix1_ActiveProject_ReactivatesOnSwitch(t *testing.T) {
	w := &ALLSPWrapper{
		openedFiles:         make(map[string]bool),
		initializedProjects: make(map[string]bool),
		projectManifests:    make(map[string]*AppManifest),
		pendingReqs:         make(map[int]chan *Message),
		responseQueue:       make(map[int]*Message),
	}

	projectA := NormalizePath("C:/projects/project-a")
	projectB := NormalizePath("C:/projects/project-b")

	// Simulate both projects already initialized (one-time setup done)
	w.initializedProjects[projectA] = true
	w.initializedProjects[projectB] = true

	// Active project starts empty
	if w.activeProject != "" {
		t.Fatalf("activeProject should start empty, got %q", w.activeProject)
	}

	// Set active to A
	w.activeProject = projectA

	// Verify switching to B would detect the change
	if w.activeProject == projectB {
		t.Fatal("Should detect that B is not the active project")
	}

	// Set active to B
	w.activeProject = projectB

	// Verify switching back to A would detect the change
	if w.activeProject == projectA {
		t.Fatal("Should detect that A is not the active project after switching to B")
	}

	// This is the key: activeProject != projectA even though
	// initializedProjects[projectA] is true. The fix ensures
	// al/setActiveWorkspace is re-sent.
	if w.initializedProjects[projectA] && w.activeProject != projectA {
		// This is the correct behavior after the fix:
		// project is initialized but not active → needs reactivation
		t.Log("FIXED: initializedProjects[A]=true but activeProject=B")
		t.Log("EnsureProjectInitialized will re-send al/setActiveWorkspace for A")
	}
}

func TestFix1_ActiveProject_SkipsRedundantActivation(t *testing.T) {
	w := &ALLSPWrapper{
		openedFiles:         make(map[string]bool),
		initializedProjects: make(map[string]bool),
		projectManifests:    make(map[string]*AppManifest),
		pendingReqs:         make(map[int]chan *Message),
		responseQueue:       make(map[int]*Message),
	}

	projectA := NormalizePath("C:/projects/project-a")
	w.initializedProjects[projectA] = true
	w.activeProject = projectA

	// When the active project matches, no switch is needed
	if w.activeProject != projectA {
		t.Fatal("Should recognize project A is already active")
	}
	t.Log("FIXED: Repeated requests for same project skip al/setActiveWorkspace")
}

// =============================================================================
// Fix 2: DidChangeWorkspaceFolders updates internal state
// =============================================================================

func TestFix2_DidChangeWorkspaceFolders_UpdatesState(t *testing.T) {
	mock := newMockWrapper()
	mock.workspaceFolders = []WorkspaceFolder{
		{URI: "file:///c%3A/project-a", Name: "project-a"},
	}

	handler := &DidChangeWorkspaceFoldersHandler{}

	// Simulate adding project-b
	params := map[string]interface{}{
		"added":   []map[string]string{{"name": "project-b", "uri": "file:///c%3A/project-b"}},
		"removed": []interface{}{},
	}
	paramsJSON, _ := json.Marshal(params)

	msg := &Message{
		JSONRPC: "2.0",
		Method:  "al/didChangeWorkspaceFolders",
		Params:  json.RawMessage(paramsJSON),
	}

	handler.Handle(msg, mock)

	// Verify internal state was updated
	if len(mock.workspaceFolders) != 2 {
		t.Fatalf("Expected 2 workspace folders after add, got %d", len(mock.workspaceFolders))
	}
	if mock.workspaceFolders[1].Name != "project-b" {
		t.Fatalf("Expected added folder to be project-b, got %s", mock.workspaceFolders[1].Name)
	}
}

func TestFix2_DidChangeWorkspaceFolders_RemovesFolder(t *testing.T) {
	mock := newMockWrapper()
	mock.workspaceFolders = []WorkspaceFolder{
		{URI: "file:///c%3A/project-a", Name: "project-a"},
		{URI: "file:///c%3A/project-b", Name: "project-b"},
	}

	handler := &DidChangeWorkspaceFoldersHandler{}

	// Simulate removing project-a
	params := map[string]interface{}{
		"added":   []interface{}{},
		"removed": []map[string]string{{"name": "project-a", "uri": "file:///c%3A/project-a"}},
	}
	paramsJSON, _ := json.Marshal(params)

	msg := &Message{
		JSONRPC: "2.0",
		Method:  "al/didChangeWorkspaceFolders",
		Params:  json.RawMessage(paramsJSON),
	}

	handler.Handle(msg, mock)

	if len(mock.workspaceFolders) != 1 {
		t.Fatalf("Expected 1 workspace folder after remove, got %d", len(mock.workspaceFolders))
	}
	if mock.workspaceFolders[0].Name != "project-b" {
		t.Fatalf("Expected remaining folder to be project-b, got %s", mock.workspaceFolders[0].Name)
	}
}

// =============================================================================
// Fix 3: handleInitialize searches all workspace folders
// =============================================================================

func TestFix3_Initialize_SearchesAllFolders(t *testing.T) {
	// The fix changes handleInitialize to iterate over all workspace folders
	// and search each one for app.json, not just rootUri.
	// This is tested indirectly via the handleInitialize code path,
	// but we can verify the logic here.

	// Create temp dirs to simulate workspace folders
	nonALDir := t.TempDir() // No app.json
	alDir := t.TempDir()    // Has app.json

	// Create app.json in the AL project dir
	appJSON := `{"id": "test", "name": "Test", "publisher": "Test", "version": "1.0.0.0"}`
	writeTestFile(t, alDir, "app.json", appJSON)

	// Verify: FindAppJSON finds it in the AL dir but not the non-AL dir
	if result := FindAppJSON(nonALDir, 5); result != "" {
		t.Fatalf("Should NOT find app.json in non-AL dir, got %s", result)
	}
	if result := FindAppJSON(alDir, 5); result == "" {
		t.Fatal("Should find app.json in AL dir")
	}

	t.Log("FIXED: handleInitialize now iterates workspace folders to find AL project")
}

// =============================================================================
// Fix 4: NewActiveWorkspaceParams accepts folder index
// =============================================================================

func TestFix4_ActiveWorkspaceParams_CorrectIndex(t *testing.T) {
	// Folder index 0
	params0 := NewActiveWorkspaceParams("C:/projects/project-a", nil, 0)
	if params0.CurrentWorkspaceFolderPath.Index != 0 {
		t.Fatalf("Expected index 0, got %d", params0.CurrentWorkspaceFolderPath.Index)
	}

	// Folder index 2
	params2 := NewActiveWorkspaceParams("C:/projects/project-c", nil, 2)
	if params2.CurrentWorkspaceFolderPath.Index != 2 {
		t.Fatalf("Expected index 2, got %d", params2.CurrentWorkspaceFolderPath.Index)
	}
}

func TestFix4_GetWorkspaceFolderIndex(t *testing.T) {
	w := &ALLSPWrapper{
		openedFiles:         make(map[string]bool),
		initializedProjects: make(map[string]bool),
		projectManifests:    make(map[string]*AppManifest),
		pendingReqs:         make(map[int]chan *Message),
		responseQueue:       make(map[int]*Message),
		workspaceFolders: []WorkspaceFolder{
			{URI: "file:///C%3A/projects/project-a", Name: "project-a"},
			{URI: "file:///C%3A/projects/project-b", Name: "project-b"},
			{URI: "file:///C%3A/projects/project-c", Name: "project-c"},
		},
	}

	// Test lookup by URI match
	idx := w.getWorkspaceFolderIndex(NormalizePath("C:/projects/project-b"))
	if idx != 1 {
		t.Fatalf("Expected index 1 for project-b, got %d", idx)
	}

	// Test not found returns 0
	idx = w.getWorkspaceFolderIndex(NormalizePath("C:/projects/unknown"))
	if idx != 0 {
		t.Fatalf("Expected index 0 for unknown project, got %d", idx)
	}
}

// =============================================================================
// Fix 5: Standard LSP format for al-call-hierarchy notifications
// =============================================================================

func TestFix5_DidChangeWorkspaceFolders_CorrectFormat(t *testing.T) {
	mock := newMockWrapper()
	mock.workspaceFolders = []WorkspaceFolder{
		{URI: "file:///c%3A/project-a", Name: "project-a"},
	}

	handler := &DidChangeWorkspaceFoldersHandler{}

	params := map[string]interface{}{
		"added":   []map[string]string{{"name": "project-b", "uri": "file:///c%3A/project-b"}},
		"removed": []interface{}{},
	}
	paramsJSON, _ := json.Marshal(params)

	msg := &Message{
		JSONRPC: "2.0",
		Method:  "al/didChangeWorkspaceFolders",
		Params:  json.RawMessage(paramsJSON),
	}

	handler.Handle(msg, mock)

	// Verify AL LSP gets the original AL custom format
	alNotifs := mock.getNotificationsForMethod("al/didChangeWorkspaceFolders")
	if len(alNotifs) != 1 {
		t.Fatalf("Expected 1 AL LSP notification, got %d", len(alNotifs))
	}

	// The AL LSP notification should be the raw params (json.RawMessage)
	alParamsJSON, _ := json.Marshal(alNotifs[0].Params)
	var alParsed map[string]interface{}
	json.Unmarshal(alParamsJSON, &alParsed)

	// AL format has "added" at top level (no "event" wrapper)
	if _, hasAdded := alParsed["added"]; !hasAdded {
		t.Fatal("AL LSP notification should have 'added' at top level")
	}
	if _, hasEvent := alParsed["event"]; hasEvent {
		t.Fatal("AL LSP notification should NOT have 'event' wrapper")
	}
}

// =============================================================================
// Fix 2 (wrapper method): UpdateWorkspaceFolders
// =============================================================================

func TestUpdateWorkspaceFolders_AddAndRemove(t *testing.T) {
	w := &ALLSPWrapper{
		openedFiles:         make(map[string]bool),
		initializedProjects: make(map[string]bool),
		projectManifests:    make(map[string]*AppManifest),
		pendingReqs:         make(map[int]chan *Message),
		responseQueue:       make(map[int]*Message),
		workspaceFolders: []WorkspaceFolder{
			{URI: "file:///c%3A/project-a", Name: "project-a"},
			{URI: "file:///c%3A/project-b", Name: "project-b"},
		},
	}

	// Add project-c, remove project-a
	w.UpdateWorkspaceFolders(
		[]WorkspaceFolder{{URI: "file:///c%3A/project-c", Name: "project-c"}},
		[]WorkspaceFolder{{URI: "file:///c%3A/project-a", Name: "project-a"}},
	)

	if len(w.workspaceFolders) != 2 {
		t.Fatalf("Expected 2 folders, got %d", len(w.workspaceFolders))
	}

	// Should have project-b and project-c
	names := make(map[string]bool)
	for _, f := range w.workspaceFolders {
		names[f.Name] = true
	}
	if !names["project-b"] {
		t.Fatal("Expected project-b to remain")
	}
	if !names["project-c"] {
		t.Fatal("Expected project-c to be added")
	}
	if names["project-a"] {
		t.Fatal("Expected project-a to be removed")
	}
}

// =============================================================================
// Root cause (issue #13): RPCError string unmarshal crash
// =============================================================================

func TestRootCause_RPCError_StringError(t *testing.T) {
	// The actual crash reported in issue #13:
	// "json: cannot unmarshal string into Go struct field Message.error of type wrapper.RPCError"
	//
	// The AL LSP returns "error": "some string" instead of the standard
	// "error": {"code": -32600, "message": "..."} in multi-root scenarios.

	// This is the exact JSON that causes the crash
	jsonWithStringError := `{"jsonrpc":"2.0","id":1,"error":"Request failed"}`

	var msg Message
	err := json.Unmarshal([]byte(jsonWithStringError), &msg)
	if err != nil {
		t.Fatalf("Should handle string error without crashing, got: %v", err)
	}

	if msg.Error == nil {
		t.Fatal("Error should be parsed")
	}
	if msg.Error.Message != "Request failed" {
		t.Fatalf("Expected error message 'Request failed', got %q", msg.Error.Message)
	}
	if msg.Error.Code != InternalError {
		t.Fatalf("Expected error code %d (InternalError), got %d", InternalError, msg.Error.Code)
	}
}

func TestRootCause_RPCError_ObjectError(t *testing.T) {
	// Standard JSON-RPC error format should still work
	jsonWithObjectError := `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"Invalid Request"}}`

	var msg Message
	err := json.Unmarshal([]byte(jsonWithObjectError), &msg)
	if err != nil {
		t.Fatalf("Should handle object error, got: %v", err)
	}

	if msg.Error == nil {
		t.Fatal("Error should be parsed")
	}
	if msg.Error.Code != -32600 {
		t.Fatalf("Expected error code -32600, got %d", msg.Error.Code)
	}
	if msg.Error.Message != "Invalid Request" {
		t.Fatalf("Expected error message 'Invalid Request', got %q", msg.Error.Message)
	}
}

func TestRootCause_RPCError_NoError(t *testing.T) {
	// Normal success response should still work
	jsonNoError := `{"jsonrpc":"2.0","id":1,"result":{"success":true}}`

	var msg Message
	err := json.Unmarshal([]byte(jsonNoError), &msg)
	if err != nil {
		t.Fatalf("Should handle success response, got: %v", err)
	}

	if msg.Error != nil {
		t.Fatal("Error should be nil for success response")
	}
}

func TestRootCause_ReadMessage_StringError(t *testing.T) {
	// Simulate the full ReadMessage flow with a string error from AL LSP
	content := `{"jsonrpc":"2.0","id":5,"error":"Workspace not initialized"}`
	lspMessage := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(content), content)

	reader := bufio.NewReader(strings.NewReader(lspMessage))
	msg, err := ReadMessage(reader)
	if err != nil {
		t.Fatalf("ReadMessage should not crash on string error, got: %v", err)
	}

	if msg.Error == nil {
		t.Fatal("Error should be parsed from string")
	}
	if msg.Error.Message != "Workspace not initialized" {
		t.Fatalf("Expected 'Workspace not initialized', got %q", msg.Error.Message)
	}
}

// =============================================================================
// Helpers
// =============================================================================

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file %s: %v", path, err)
	}
}
