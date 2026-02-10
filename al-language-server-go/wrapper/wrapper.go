package wrapper

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// maxLogFileSize is the maximum size of the log file before rotation (20MB)
	maxLogFileSize = 20 * 1024 * 1024
	// logFileTruncateSize is the size to keep after rotation (10MB)
	logFileTruncateSize = 10 * 1024 * 1024
	// logFileMaxAge is the maximum age of stale log files before cleanup (24 hours)
	logFileMaxAge = 24 * time.Hour
	// logSizeCheckInterval is how often to check log file size (every N writes)
	logSizeCheckInterval = 100
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
	projectManifests    map[string]*AppManifest
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
	logFile       *os.File
	logMu         sync.Mutex
	logWriteCount int

	// Initialization
	initialized bool
	initMu      sync.Mutex
}

// New creates a new ALLSPWrapper
func New() *ALLSPWrapper {
	return &ALLSPWrapper{
		openedFiles:         make(map[string]bool),
		initializedProjects: make(map[string]bool),
		projectManifests:    make(map[string]*AppManifest),
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
	// Clean up old log files from dead processes first
	w.cleanupOldLogs()

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

	// Check log file size periodically (avoid stat() on every write)
	w.logWriteCount++
	if w.logWriteCount >= logSizeCheckInterval {
		w.logWriteCount = 0
		w.checkAndRotateLog()
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(w.logFile, "[%s] %s\n", timestamp, msg)
	w.logFile.Sync()
}

// checkAndRotateLog checks if the log file exceeds the maximum size and rotates if needed
// Must be called with logMu held
func (w *ALLSPWrapper) checkAndRotateLog() {
	if w.logFile == nil {
		return
	}

	info, err := w.logFile.Stat()
	if err != nil || info.Size() < maxLogFileSize {
		return
	}

	// Close current file
	w.logFile.Close()

	// Truncate by keeping last portion
	w.truncateLogFile()

	// Reopen the log file
	logPath := GetLogPath()
	w.logFile, _ = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
}

// truncateLogFile keeps only the last logFileTruncateSize bytes of the log file
// Must be called with logMu held
func (w *ALLSPWrapper) truncateLogFile() {
	path := GetLogPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	// Keep last portion
	if len(data) > logFileTruncateSize {
		data = data[len(data)-logFileTruncateSize:]
		// Find first newline to avoid partial lines
		if idx := bytes.IndexByte(data, '\n'); idx > 0 {
			data = data[idx+1:]
		}
	}

	// Write truncated content
	_ = os.WriteFile(path, data, 0644)
}

// cleanupOldLogs removes log files from dead processes that are older than logFileMaxAge
func (w *ALLSPWrapper) cleanupOldLogs() {
	pattern := GetLogPattern()
	currentLog := GetLogPath()

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}

	for _, path := range matches {
		// Skip our own log file
		if path == currentLog {
			continue
		}

		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		// Only delete if older than max age
		if time.Since(info.ModTime()) > logFileMaxAge {
			// Extract PID from filename to check if process is still running
			pid := extractPIDFromLogPath(path)
			if pid > 0 && isProcessRunning(pid) {
				// Process is still running, skip this file
				continue
			}

			// Delete the old log file (ignore errors - file may be locked)
			_ = os.Remove(path)
		}
	}
}

// extractPIDFromLogPath extracts the PID from a log file path like "al-lsp-wrapper-go-12345.log"
func extractPIDFromLogPath(path string) int {
	base := filepath.Base(path)
	// Pattern: al-lsp-wrapper-go-{pid}.log
	prefix := "al-lsp-wrapper-go-"
	suffix := ".log"

	if !strings.HasPrefix(base, prefix) || !strings.HasSuffix(base, suffix) {
		return 0
	}

	pidStr := base[len(prefix) : len(base)-len(suffix)]
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0
	}

	return pid
}

// isProcessRunning checks if a process with the given PID is still running
func isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// On Unix, FindProcess always succeeds, so we need to send signal 0 to check
	// On Windows, FindProcess also always succeeds, but signal 0 returns an error
	// for processes that don't exist. The syscall.Signal(0) approach works cross-platform.
	err = process.Signal(syscall.Signal(0))
	return err == nil
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
			} else {
				w.Log("WARNING: Received response for unknown request id=%d", id)
			}
			w.pendingMu.Unlock()
		} else if msg.IsRequest() {
			// Server-initiated request (e.g., client/registerCapability, workspace/configuration)
			w.Log("Received server request: method=%s id=%s", msg.Method, msg.GetIDString())
			w.handleServerRequest(msg)
		} else if msg.IsNotification() {
			// Forward notifications to client
			w.Log("Forwarding notification to client: %s", msg.Method)
			if err := WriteMessage(w.clientWriter, msg); err != nil {
				w.Log("Error forwarding notification: %v", err)
			}
		} else {
			w.Log("WARNING: Unclassified message from AL LSP: method=%s id=%s", msg.Method, msg.GetIDString())
		}
	}
}

// handleServerRequest handles requests sent from the AL LSP to the client.
// These must be responded to or the AL LSP will block waiting for a response.
func (w *ALLSPWrapper) handleServerRequest(msg *Message) {
	switch msg.Method {
	case "client/registerCapability", "client/unregisterCapability":
		// Acknowledge capability registration (required by protocol)
		resp := &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  json.RawMessage("null"),
		}
		if err := WriteMessage(w.stdin, resp); err != nil {
			w.Log("Error responding to %s: %v", msg.Method, err)
		}

	case "workspace/configuration":
		// Parse the configuration items to extract workspace paths
		var params struct {
			Items []struct {
				ScopeURI string `json:"scopeUri"`
				Section  string `json:"section"`
			} `json:"items"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			w.Log("Failed to parse workspace/configuration params: %v", err)
		}
		w.Log("workspace/configuration request: %d items", len(params.Items))

		// Return an array of null values (standard LSP response)
		results := make([]interface{}, len(params.Items))
		resultJSON, _ := json.Marshal(results)
		resp := &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  resultJSON,
		}
		if err := WriteMessage(w.stdin, resp); err != nil {
			w.Log("Error responding to workspace/configuration: %v", err)
		}

		// Like VS Code's AL extension, also send workspace/didChangeConfiguration
		// for each item's workspace. The AL LSP needs this to fully initialize.
		sentPaths := make(map[string]bool)
		for _, item := range params.Items {
			wsPath := ""
			if item.ScopeURI != "" {
				if p, err := FileURIToPath(item.ScopeURI); err == nil {
					wsPath = NormalizePath(p)
				}
			}
			if wsPath == "" && w.workspaceRoot != "" {
				wsPath = NormalizePath(w.workspaceRoot)
			}
			if wsPath == "" || sentPaths[wsPath] {
				continue
			}
			sentPaths[wsPath] = true

			w.Log("Sending workspace/didChangeConfiguration for: %s", wsPath)
			manifest := w.GetManifest(wsPath)
			settings := NewWorkspaceSettings(wsPath, manifest)
			configParams := DidChangeConfigurationParams{Settings: settings}
			if err := w.SendNotificationToLSP("workspace/didChangeConfiguration", configParams); err != nil {
				w.Log("Error sending didChangeConfiguration: %v", err)
			}
		}

	case "window/workDoneProgress/create":
		// Acknowledge progress creation
		resp := &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  json.RawMessage("null"),
		}
		if err := WriteMessage(w.stdin, resp); err != nil {
			w.Log("Error responding to %s: %v", msg.Method, err)
		}

	default:
		// Unknown server request — send empty success response to unblock the server
		w.Log("Unhandled server request: %s, sending empty response", msg.Method)
		resp := &Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  json.RawMessage("null"),
		}
		if err := WriteMessage(w.stdin, resp); err != nil {
			w.Log("Error responding to %s: %v", msg.Method, err)
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
			case "textDocument/didOpen", "textDocument/didClose", "textDocument/didChange", "textDocument/didSave":
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

	// Log AL LSP server capabilities
	w.Log("=== AL LSP SERVER CAPABILITIES (raw) ===")
	w.Log("%s", string(response.Result))
	w.Log("=== END AL LSP SERVER CAPABILITIES ===")

	w.initMu.Lock()
	w.initialized = true
	w.initMu.Unlock()

	// Modify capabilities to advertise extra capabilities (provided by al-call-hierarchy)
	modifiedResult := w.addExtraCapabilities(response.Result)

	// Return response to client
	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  modifiedResult,
	}, nil
}

// addExtraCapabilities adds codeLensProvider and callHierarchyProvider to server capabilities
func (w *ALLSPWrapper) addExtraCapabilities(result json.RawMessage) json.RawMessage {
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

	// Add codeLensProvider capability (provided by al-call-hierarchy)
	caps["codeLensProvider"] = map[string]interface{}{
		"resolveProvider": false,
	}

	// Add callHierarchyProvider capability (provided by al-call-hierarchy)
	caps["callHierarchyProvider"] = true

	w.Log("Added codeLensProvider and callHierarchyProvider capabilities to server capabilities")

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

// GetManifest returns the cached manifest for a project root, or parses app.json fresh
func (w *ALLSPWrapper) GetManifest(projectRoot string) *AppManifest {
	if m, ok := w.projectManifests[projectRoot]; ok {
		return m
	}

	appJsonPath := filepath.Join(projectRoot, "app.json")
	manifest := ParseAppManifest(appJsonPath)
	if manifest != nil {
		depCount := len(manifest.Dependencies)
		w.Log("Parsed app.json for %s: %d dependencies", projectRoot, depCount)
		w.projectManifests[projectRoot] = manifest
	} else {
		w.Log("Could not parse app.json for %s", projectRoot)
	}
	return manifest
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

	// Parse app.json manifest
	manifest := w.GetManifest(normalizedRoot)

	// Send workspace configuration
	settings := NewWorkspaceSettings(normalizedRoot, manifest)
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

	// Send al/loadManifest (like VS Code does)
	if manifest != nil && manifest.Raw != "" {
		loadParams := LoadManifestParams{
			ProjectFolder: normalizedRoot,
			Manifest:      manifest.Raw,
		}
		if _, err := w.SendRequestToLSP("al/loadManifest", loadParams); err != nil {
			w.Log("al/loadManifest failed (non-fatal): %v", err)
		} else {
			w.Log("al/loadManifest succeeded")
		}
	}

	// Set active workspace
	activeParams := NewActiveWorkspaceParams(normalizedRoot, manifest)
	if _, err := w.SendRequestToLSP("al/setActiveWorkspace", activeParams); err != nil {
		w.Log("Failed to set active workspace: %v", err)
	}

	// Wait for project to load
	w.waitForProjectLoad(normalizedRoot)

	w.initializedProjects[normalizedRoot] = true
	w.Log("Project initialized: %s", normalizedRoot)

	return nil
}

func (w *ALLSPWrapper) waitForProjectLoad(workspacePath string) {
	params := map[string]string{
		"workspacePath": workspacePath,
	}

	// Poll for project load status (up to 30 seconds)
	for i := 0; i < 30; i++ {
		resp, err := w.SendRequestToLSP("al/hasProjectClosureLoadedRequest", params)
		if err != nil {
			w.Log("Error checking project load status: %v", err)
			break
		}

		// Response is { loaded: boolean }
		var result struct {
			Loaded bool `json:"loaded"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			w.Log("Failed to parse project load response: %v (raw: %s)", err, string(resp.Result))
			break
		}
		if result.Loaded {
			w.Log("Project loaded successfully")
			return
		}

		time.Sleep(time.Second)
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
	w.callHierarchyServer.SetClientWriter(w.clientWriter)

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
