package wrapper

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// ALLSPWrapper wraps the AL Language Server
type ALLSPWrapper struct {
	// AL LSP process
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr io.ReadCloser

	// Client (Claude Code) communication
	clientReader *bufio.Reader
	clientWriter io.Writer

	// State tracking
	openedFiles         map[string]bool
	initializedProjects map[string]bool
	workspaceRoot       string

	// Request tracking
	requestID      int
	pendingMu      sync.Mutex
	pendingReqs    map[int]chan *Message

	// Response queue for requests we sent to LSP
	responseMu     sync.Mutex
	responseQueue  map[int]*Message

	// Handlers
	handlers []Handler

	// Call hierarchy server
	callHierarchyServer *CallHierarchyServer

	// Logging
	logFile *os.File
	logMu   sync.Mutex

	// Initialization
	initialized bool
	initMu      sync.Mutex
}

// New creates a new ALLSPWrapper
func New() *ALLSPWrapper {
	return &ALLSPWrapper{
		openedFiles:         make(map[string]bool),
		initializedProjects: make(map[string]bool),
		pendingReqs:         make(map[int]chan *Message),
		responseQueue:       make(map[int]*Message),
		handlers:            GetDefaultHandlers(),
	}
}

// Run starts the wrapper
func (w *ALLSPWrapper) Run() error {
	// Setup logging
	if err := w.setupLogging(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to setup logging: %v\n", err)
	}

	w.Log("AL LSP Wrapper (Go) starting...")

	// Find AL extension
	extensionPath, err := FindALExtension()
	if err != nil {
		w.Log("Failed to find AL extension: %v", err)
		return fmt.Errorf("AL extension not found: %w", err)
	}
	w.Log("Found AL extension: %s", extensionPath)

	// Get executable path
	executable := GetALLSPExecutable(extensionPath)
	w.Log("AL LSP executable: %s", executable)

	// Check executable exists
	if _, err := os.Stat(executable); os.IsNotExist(err) {
		w.Log("AL LSP executable not found: %s", executable)
		return fmt.Errorf("AL LSP executable not found: %s", executable)
	}

	// Start AL LSP process
	w.cmd = exec.Command(executable)
	w.cmd.Dir = extensionPath

	w.stdin, err = w.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	stdoutPipe, err := w.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	w.stdout = bufio.NewReader(stdoutPipe)

	w.stderr, err = w.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	if err := w.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start AL LSP: %w", err)
	}
	w.Log("AL LSP process started (PID: %d)", w.cmd.Process.Pid)

	// Add to Windows job object for automatic cleanup on parent exit
	addProcessToJob(w.cmd.Process)

	// Setup client communication
	w.clientReader = bufio.NewReader(os.Stdin)
	w.clientWriter = os.Stdout

	// Start goroutines
	errChan := make(chan error, 2)

	// Read stderr in background
	go w.readStderr()

	// Read from AL LSP and forward notifications/handle responses
	go func() {
		errChan <- w.readFromLSP()
	}()

	// Main loop: read from client and process
	go func() {
		errChan <- w.readFromClient()
	}()

	// Wait for error or completion
	err = <-errChan
	w.Log("Wrapper stopping: %v", err)

	// Cleanup
	if w.cmd.Process != nil {
		w.cmd.Process.Kill()
	}

	return err
}

func (w *ALLSPWrapper) setupLogging() error {
	logPath := GetLogPath()
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	w.logFile = f
	return nil
}

// Log logs a message
func (w *ALLSPWrapper) Log(format string, args ...interface{}) {
	w.logMu.Lock()
	defer w.logMu.Unlock()

	if w.logFile == nil {
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(w.logFile, "[%s] %s\n", timestamp, msg)
	w.logFile.Sync()
}

func (w *ALLSPWrapper) readStderr() {
	scanner := bufio.NewScanner(w.stderr)
	for scanner.Scan() {
		w.Log("[AL LSP stderr] %s", scanner.Text())
	}
}

func (w *ALLSPWrapper) readFromLSP() error {
	for {
		msg, err := ReadMessage(w.stdout)
		if err != nil {
			if err == io.EOF {
				return fmt.Errorf("AL LSP connection closed")
			}
			w.Log("Error reading from AL LSP: %v", err)
			return err
		}

		if msg.IsResponse() {
			// This is a response to a request we sent
			id := msg.GetIDInt()
			w.pendingMu.Lock()
			if ch, ok := w.pendingReqs[id]; ok {
				ch <- msg
				delete(w.pendingReqs, id)
			}
			w.pendingMu.Unlock()
		} else if msg.IsNotification() {
			// Forward notifications to client
			w.Log("Forwarding notification to client: %s", msg.Method)
			if err := WriteMessage(w.clientWriter, msg); err != nil {
				w.Log("Error forwarding notification: %v", err)
			}
		}
	}
}

func (w *ALLSPWrapper) readFromClient() error {
	for {
		msg, err := ReadMessage(w.clientReader)
		if err != nil {
			if err == io.EOF {
				return fmt.Errorf("client connection closed")
			}
			w.Log("Error reading from client: %v", err)
			return err
		}

		w.Log("Received from client: method=%s id=%s", msg.Method, msg.GetIDString())

		// Handle the message
		response, err := w.handleMessage(msg)
		if err != nil {
			w.Log("Error handling message: %v", err)
			if msg.IsRequest() {
				errResp := NewErrorResponse(msg.ID, InternalError, err.Error())
				WriteMessage(w.clientWriter, errResp)
			}
			continue
		}

		// Send response if any
		if response != nil {
			w.Log("Sending response to client: id=%s", response.GetIDString())
			if err := WriteMessage(w.clientWriter, response); err != nil {
				w.Log("Error writing response: %v", err)
			}
		}
	}
}

func (w *ALLSPWrapper) handleMessage(msg *Message) (*Message, error) {
	// Handle initialize specially
	if msg.Method == "initialize" {
		return w.handleInitialize(msg)
	}

	// Handle initialized notification
	if msg.Method == "initialized" {
		w.SendNotificationToLSP("initialized", nil)
		// Start call hierarchy server after AL LSP is initialized
		go w.startCallHierarchyServer()
		return nil, nil
	}

	// Handle shutdown
	if msg.Method == "shutdown" {
		resp, err := w.SendRequestToLSP("shutdown", nil)
		if err != nil {
			return nil, err
		}
		return &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  resp.Result,
		}, nil
	}

	// Handle exit
	if msg.Method == "exit" {
		// Shutdown call hierarchy server first
		if w.callHierarchyServer != nil {
			w.callHierarchyServer.Shutdown()
		}
		w.SendNotificationToLSP("exit", nil)
		os.Exit(0)
		return nil, nil
	}

	// Check handlers
	for _, handler := range w.handlers {
		if handler.ShouldHandle(msg.Method) {
			response, errResp := handler.Handle(msg, w)
			if errResp != nil {
				return errResp, nil
			}
			return response, nil
		}
	}

	// Pass through to AL LSP
	if msg.IsRequest() {
		var params interface{}
		if len(msg.Params) > 0 {
			json.Unmarshal(msg.Params, &params)
		}
		resp, err := w.SendRequestToLSP(msg.Method, params)
		if err != nil {
			return nil, err
		}
		return &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  resp.Result,
			Error:   resp.Error,
		}, nil
	}

	// Forward notification
	if msg.IsNotification() {
		var params interface{}
		if len(msg.Params) > 0 {
			json.Unmarshal(msg.Params, &params)
		}
		w.SendNotificationToLSP(msg.Method, params)

		// Also forward document events to call hierarchy server
		if w.callHierarchyServer != nil && w.callHierarchyServer.IsInitialized() {
			switch msg.Method {
			case "textDocument/didOpen", "textDocument/didClose", "textDocument/didChange":
				w.Log("Forwarding %s to al-call-hierarchy", msg.Method)
				w.callHierarchyServer.SendNotification(msg.Method, params)
			}
		}
	}

	return nil, nil
}

func (w *ALLSPWrapper) handleInitialize(msg *Message) (*Message, error) {
	// Log raw initialize params from Claude Code to see client capabilities
	w.Log("=== CLIENT INITIALIZE PARAMS (raw) ===")
	w.Log("%s", string(msg.Params))
	w.Log("=== END CLIENT INITIALIZE PARAMS ===")

	var params InitializeParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		w.Log("Failed to parse initialize params: %v", err)
	}

	// Log parsed capabilities for easier reading
	if capsJSON, err := json.MarshalIndent(params.Capabilities, "", "  "); err == nil {
		w.Log("=== CLIENT CAPABILITIES (parsed) ===")
		w.Log("%s", string(capsJSON))
		w.Log("=== END CLIENT CAPABILITIES ===")
	}

	// Extract workspace root
	if params.RootURI != "" {
		if path, err := FileURIToPath(params.RootURI); err == nil {
			w.workspaceRoot = path
			w.Log("Workspace root: %s", w.workspaceRoot)
		}
	}

	// Find app.json to determine AL project root
	projectRoot := ""
	if w.workspaceRoot != "" {
		appJson := FindAppJSON(w.workspaceRoot, 5)
		if appJson != "" {
			projectRoot = filepath.Dir(appJson)
			w.Log("Found AL project at: %s", projectRoot)
		}
	}

	// Build initialize params for AL LSP
	var initParams *InitializeParams
	if projectRoot != "" {
		initParams = NewInitializeParams(projectRoot)
	} else if w.workspaceRoot != "" {
		initParams = NewInitializeParams(w.workspaceRoot)
	} else {
		// Use current directory as fallback
		cwd, _ := os.Getwd()
		initParams = NewInitializeParams(cwd)
	}

	// Send initialize to AL LSP
	response, err := w.SendRequestToLSP("initialize", initParams)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize AL LSP: %w", err)
	}

	w.initMu.Lock()
	w.initialized = true
	w.initMu.Unlock()

	// Modify capabilities to advertise codeLensProvider (provided by al-call-hierarchy)
	modifiedResult := w.addCodeLensCapability(response.Result)

	// Return response to client
	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  modifiedResult,
	}, nil
}

// addCodeLensCapability adds codeLensProvider to server capabilities
func (w *ALLSPWrapper) addCodeLensCapability(result json.RawMessage) json.RawMessage {
	if result == nil {
		return result
	}

	var initResult map[string]interface{}
	if err := json.Unmarshal(result, &initResult); err != nil {
		w.Log("Failed to parse initialize result for capability modification: %v", err)
		return result
	}

	// Get or create capabilities
	caps, ok := initResult["capabilities"].(map[string]interface{})
	if !ok {
		w.Log("No capabilities in initialize result")
		return result
	}

	// Add codeLensProvider capability
	caps["codeLensProvider"] = map[string]interface{}{
		"resolveProvider": false,
	}

	w.Log("Added codeLensProvider capability to server capabilities")

	modifiedResult, err := json.Marshal(initResult)
	if err != nil {
		w.Log("Failed to marshal modified capabilities: %v", err)
		return result
	}

	return modifiedResult
}

// SendRequestToLSP sends a request to the AL LSP and waits for response
func (w *ALLSPWrapper) SendRequestToLSP(method string, params interface{}) (*Message, error) {
	w.requestID++
	id := w.requestID

	msg, err := NewRequest(id, method, params)
	if err != nil {
		return nil, err
	}

	// Create response channel
	respChan := make(chan *Message, 1)
	w.pendingMu.Lock()
	w.pendingReqs[id] = respChan
	w.pendingMu.Unlock()

	// Send request
	w.Log("Sending request to AL LSP: method=%s id=%d", method, id)
	if err := WriteMessage(w.stdin, msg); err != nil {
		w.pendingMu.Lock()
		delete(w.pendingReqs, id)
		w.pendingMu.Unlock()
		return nil, err
	}

	// Wait for response with timeout
	select {
	case resp := <-respChan:
		w.Log("Received response from AL LSP: id=%d", id)
		return resp, nil
	case <-time.After(30 * time.Second):
		w.pendingMu.Lock()
		delete(w.pendingReqs, id)
		w.pendingMu.Unlock()
		return nil, fmt.Errorf("timeout waiting for response to %s", method)
	}
}

// SendNotificationToLSP sends a notification to the AL LSP
func (w *ALLSPWrapper) SendNotificationToLSP(method string, params interface{}) error {
	msg, err := NewNotification(method, params)
	if err != nil {
		return err
	}

	w.Log("Sending notification to AL LSP: %s", method)
	return WriteMessage(w.stdin, msg)
}

// EnsureFileOpened ensures a file is opened in the AL LSP
func (w *ALLSPWrapper) EnsureFileOpened(filePath string) error {
	normalizedPath := NormalizePath(filePath)

	if w.openedFiles[normalizedPath] {
		return nil
	}

	w.Log("Opening file: %s", normalizedPath)

	// Read file content
	content, err := os.ReadFile(normalizedPath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Send didOpen notification
	params := NewDidOpenParams(normalizedPath, string(content))
	if err := w.SendNotificationToLSP("textDocument/didOpen", params); err != nil {
		return err
	}

	w.openedFiles[normalizedPath] = true
	return nil
}

// EnsureProjectInitialized ensures the project for a file is initialized
func (w *ALLSPWrapper) EnsureProjectInitialized(filePath string) error {
	projectRoot := GetProjectRoot(filePath)
	if projectRoot == "" {
		w.Log("No AL project found for: %s", filePath)
		return nil // Not an error - might not be an AL file
	}

	normalizedRoot := NormalizePath(projectRoot)

	if w.initializedProjects[normalizedRoot] {
		return nil
	}

	w.Log("Initializing project: %s", normalizedRoot)

	// Send workspace configuration
	settings := NewWorkspaceSettings(normalizedRoot)
	configParams := DidChangeConfigurationParams{Settings: settings}
	if err := w.SendNotificationToLSP("workspace/didChangeConfiguration", configParams); err != nil {
		w.Log("Failed to send workspace configuration: %v", err)
	}

	// Open app.json
	appJsonPath := filepath.Join(normalizedRoot, "app.json")
	if err := w.EnsureFileOpened(appJsonPath); err != nil {
		w.Log("Failed to open app.json: %v", err)
		// Continue anyway - app.json might not exist
	}

	// Set active workspace
	activeParams := NewActiveWorkspaceParams(normalizedRoot)
	if _, err := w.SendRequestToLSP("al/setActiveWorkspace", activeParams); err != nil {
		w.Log("Failed to set active workspace: %v", err)
	}

	// Wait for project to load
	w.waitForProjectLoad()

	w.initializedProjects[normalizedRoot] = true
	w.Log("Project initialized: %s", normalizedRoot)

	return nil
}

func (w *ALLSPWrapper) waitForProjectLoad() {
	// Poll for project load status
	for i := 0; i < 10; i++ {
		resp, err := w.SendRequestToLSP("al/hasProjectClosureLoadedRequest", nil)
		if err != nil {
			w.Log("Error checking project load status: %v", err)
			break
		}

		var loaded bool
		if err := json.Unmarshal(resp.Result, &loaded); err == nil && loaded {
			w.Log("Project loaded successfully")
			return
		}

		time.Sleep(500 * time.Millisecond)
	}

	w.Log("Timeout waiting for project load, continuing anyway")
}

// GetCallHierarchyServer returns the call hierarchy server
func (w *ALLSPWrapper) GetCallHierarchyServer() *CallHierarchyServer {
	return w.callHierarchyServer
}

// startCallHierarchyServer starts the al-call-hierarchy server
func (w *ALLSPWrapper) startCallHierarchyServer() {
	w.callHierarchyServer = NewCallHierarchyServer(w.Log)

	executable := w.callHierarchyServer.FindExecutable()
	if executable == "" {
		w.Log("al-call-hierarchy executable not found, call hierarchy disabled")
		w.callHierarchyServer = nil
		return
	}

	if err := w.callHierarchyServer.Start(executable); err != nil {
		w.Log("Failed to start al-call-hierarchy: %v", err)
		w.callHierarchyServer = nil
		return
	}

	// Initialize with workspace root
	workspacePath := w.workspaceRoot
	if workspacePath == "" {
		workspacePath, _ = os.Getwd()
	}

	workspaceURI := PathToFileURI(workspacePath)
	workspaceName := filepath.Base(workspacePath)
	workspaceFolders := []WorkspaceFolder{
		{URI: workspaceURI, Name: workspaceName},
	}

	w.Log("Initializing call hierarchy with workspace: %s", workspacePath)
	if err := w.callHierarchyServer.Initialize(workspaceURI, workspaceFolders); err != nil {
		w.Log("Failed to initialize al-call-hierarchy: %v", err)
		w.callHierarchyServer.Shutdown()
		w.callHierarchyServer = nil
		return
	}

	w.Log("al-call-hierarchy server ready")
}
