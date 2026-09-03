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
	"runtime"
	"strconv"
	"strings"
	"sync"
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
	// ALExtensionPath is an optional explicit path to the MS AL extension.
	// When set, skips auto-discovery. Set via --al-extension-path flag.
	ALExtensionPath string

	// VSCodeMode disables textDocumentSync in the server capabilities so the
	// LanguageClient won't send didOpen/didChange/didClose. In VS Code the
	// MS AL extension already handles document sync; our wrapper only needs
	// on-demand file access via EnsureFileOpened. Set via --vscode flag.
	VSCodeMode bool

	// AutoDownloadALExtension enables automatic download of the AL extension
	// from the VS Code Marketplace. Set via --auto-download-al-extension flag.
	AutoDownloadALExtension bool

	// ALExtensionChannel selects the release channel ("release" or "prerelease").
	// Set via --al-extension-channel flag.
	ALExtensionChannel string

	// ForceUpdateALExtension bypasses the daily update check and downloads
	// the latest version immediately. Set via --force-update-al-extension flag.
	ForceUpdateALExtension bool

	// Launcher identifies the client that launched the wrapper: "vscode",
	// "claude-code", or "" (unknown). Used in duplicate-instance warnings to
	// tell users exactly which two clients have spawned conflicting wrappers.
	// Set via --launcher flag.
	Launcher string

	// NoDiagnostics forwards --no-diagnostics to al-sem, telling it not to
	// compute code-quality diagnostics at all. The VS Code client filters
	// them away client-side unless the user opts in, and computing them
	// costs a full analysis of every workspace root that gets opened.
	NoDiagnostics bool

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
	workspaceFolders    []WorkspaceFolder
	activeProject       string // Currently active project root (normalized path)

	// Request tracking
	requestID   int
	pendingMu   sync.Mutex
	pendingReqs map[int]chan *Message

	// Response queue for requests we sent to LSP
	responseMu    sync.Mutex
	responseQueue map[int]*Message

	// Handlers
	handlers []Handler

	// Call hierarchy server
	callHierarchyServer *CallHierarchyServer

	// diagMerger reconciles publishDiagnostics from the AL LS and the
	// al-call-hierarchy server so neither backend clobbers the other's
	// diagnostics for a URI (issue #20 symptom #2).
	diagMerger *DiagnosticMerger

	// almcp MCP server (backs al/symbolRelations and al/inspectPage)
	almcpServer *ALMcpServer

	// Preview cache: materializes .al from .app archives to make
	// dependency objects addressable via on-disk file:// URIs (so
	// Claude Code's filePath-existence check on the LSP tool surface
	// passes for documentSymbol on dependency objects).
	previewCache   *previewCache
	previewCacheMu sync.Mutex

	// Write mutex for AL LSP stdin (handleServerRequest and SendRequestToLSP
	// can write concurrently from different goroutines)
	stdinMu sync.Mutex

	// Logging
	logFile       *os.File
	logMu         sync.Mutex
	logWriteCount int

	// URI sanitation telemetry. Counts how many malformed file URIs the
	// wrapper has had to fix in outbound messages. See uri_sanitizer.go.
	uriStats uriSanitizationStats

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
		almcpServer:         NewALMcpServer(nil), // logFunc wired in Run()
	}
}

// Run starts the wrapper
func (w *ALLSPWrapper) Run() error {
	// Setup logging
	if err := w.setupLogging(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to setup logging: %v\n", err)
	}

	w.Log("AL LSP Wrapper (Go) starting...")
	// One-line session header: the facts every triage starts from, together
	// rather than scattered across the first fifty lines. `os.Args[1:]` is
	// the launcher's flags verbatim, which is how you tell (for example)
	// whether --no-diagnostics was actually in effect.
	w.Log("session: pid=%d os=%s/%s launcher=%q flags=%v",
		os.Getpid(), runtime.GOOS, runtime.GOARCH, w.Launcher, os.Args[1:])

	channel := w.ALExtensionChannel
	if channel == "" {
		channel = "release"
	}

	// Resolve AL extension path (explicit flag > env var > auto-discovery > downloaded)
	extensionPath, err := ResolveALExtensionPath(w.ALExtensionPath, w.AutoDownloadALExtension, channel)
	if err != nil && w.AutoDownloadALExtension {
		// First run: blocking download
		w.Log("AL extension not found locally, downloading...")
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return fmt.Errorf("failed to get home directory: %w", homeErr)
		}
		store := NewExtensionStore(home)
		extensionPath, err = store.DownloadAndInstall(channel, w.Log)
		if err != nil {
			return fmt.Errorf("failed to download AL extension: %w", err)
		}
	}
	if err != nil {
		w.Log("Failed to find AL extension: %v", err)
		return fmt.Errorf("AL extension not found: %w", err)
	}
	w.Log("Found AL extension: %s", extensionPath)

	// Point the almcp manager at the resolved extension (for the bundled fallback).
	w.almcpServer.logFunc = w.Log
	if !w.almcpServer.SetExtensionPath(extensionPath) {
		w.Log("almcp backend not found (nuget al tool absent and no bundled almcp); al/symbolRelations and al/inspectPage will error until available")
	}

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

	// Background update check (non-blocking)
	if w.AutoDownloadALExtension {
		go func() {
			home, err := os.UserHomeDir()
			if err != nil {
				w.Log("Warning: cannot check for updates: %v", err)
				return
			}
			store := NewExtensionStore(home)

			if w.ForceUpdateALExtension || store.NeedsUpdateCheck(channel) {
				newVersion := store.CheckAndUpdate(channel, w.Log)
				if newVersion != "" {
					w.Log("AL extension updated to v%s — restart to use new version", newVersion)
				}
			}
		}()
	}

	// Setup client communication
	w.clientReader = bufio.NewReader(os.Stdin)
	w.clientWriter = os.Stdout
	if w.diagMerger == nil {
		w.diagMerger = NewDiagnosticMerger()
	}

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
	w.removeLockFile()
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

// isProcessRunning is implemented per-platform in process_windows.go and process_unix.go

func (w *ALLSPWrapper) readStderr() {
	scanner := bufio.NewScanner(w.stderr)
	for scanner.Scan() {
		w.Log("[AL LSP stderr] %s", scanner.Text())
	}
}

// writeToClient sanitizes the message and writes it to the client. All
// outbound traffic to the client must go through this function so that
// malformed URIs from upstream (AL LS, call-hierarchy) never reach the
// editor — see uri_sanitizer.go for the rewrite rules.
func (w *ALLSPWrapper) writeToClient(msg *Message) error {
	if n, orig, norm := SanitizeOutboundMessage(msg); n > 0 {
		w.uriStats.record(w.Log, "AL-LS->client", msg.Method, n, orig, norm)
	}
	// Merge AL LS diagnostics with the al-call-hierarchy server's so neither
	// backend's publishDiagnostics erases the other for a URI (issue #20 #2).
	// Runs after sanitization so the URI key matches the call-hierarchy path,
	// which sanitizes with the same function.
	if msg != nil && msg.Method == "textDocument/publishDiagnostics" && w.diagMerger != nil {
		w.diagMerger.MergePublishDiagnostics(diagBackendALLS, msg)
	}
	return WriteMessage(w.clientWriter, msg)
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
			if msg.Method == "textDocument/publishDiagnostics" {
				w.logPublishDiagnostics(msg.Params)
			}
			if err := w.writeToClient(msg); err != nil {
				w.Log("Error forwarding notification: %v", err)
			}
		} else {
			w.Log("WARNING: Unclassified message from AL LSP: method=%s id=%s", msg.Method, msg.GetIDString())
		}
	}
}

// logPublishDiagnostics logs diagnostic details emitted by the AL LSP.
// Used to investigate issues #15/#17: is the AL LSP producing "already
// declared" errors that leak through to the client?
func (w *ALLSPWrapper) logPublishDiagnostics(params json.RawMessage) {
	var pd struct {
		URI         string `json:"uri"`
		Diagnostics []struct {
			Severity int    `json:"severity"`
			Source   string `json:"source"`
			Code     any    `json:"code"`
			Message  string `json:"message"`
			Range    struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"range"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(params, &pd); err != nil {
		w.Log("  [publishDiagnostics] parse error: %v", err)
		return
	}
	w.Log("  [publishDiagnostics] uri=%s count=%d", pd.URI, len(pd.Diagnostics))
	for _, d := range pd.Diagnostics {
		msg := d.Message
		if len(msg) > 200 {
			msg = msg[:200]
		}
		w.Log("    sev=%d source=%q code=%v line=%d msg=%q",
			d.Severity, d.Source, d.Code, d.Range.Start.Line+1, msg)
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
		if err := w.writeToLSP(resp); err != nil {
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
		if err := w.writeToLSP(resp); err != nil {
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
		if err := w.writeToLSP(resp); err != nil {
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
		if err := w.writeToLSP(resp); err != nil {
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
				w.writeToClient(errResp)
			}
			continue
		}

		// Send response if any
		if response != nil {
			w.Log("Sending response to client: id=%s", response.GetIDString())
			if err := w.writeToClient(response); err != nil {
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
		// Shutdown call hierarchy server first. The session summary must be
		// emitted BEFORE the child dies — it reports that child's memory,
		// which is unreadable once the process is gone.
		if w.callHierarchyServer != nil {
			w.callHierarchyServer.LogSessionSummary()
			w.callHierarchyServer.Shutdown()
		}
		if w.almcpServer != nil {
			w.almcpServer.Shutdown()
		}
		if total := w.uriStats.Total(); total > 0 {
			w.Log("[URI-FIX] session totals: %d malformed file URIs normalized before reaching the client", total)
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

		// Track file open/close state so EnsureFileOpened doesn't send
		// duplicate didOpen to the AL LSP. Without this, VS Code's
		// LanguageClient sends didOpen (forwarded here) but openedFiles
		// isn't updated, so a later tool invocation sends it again.
		// See: https://github.com/SShadowS/al-lsp-for-agents/issues/17
		switch msg.Method {
		case "textDocument/didOpen":
			if uri := extractTextDocumentURI(msg.Params); uri != "" {
				if path, err := FileURIToPath(uri); err == nil {
					w.openedFiles[NormalizePath(path)] = true
				}
			}
		case "textDocument/didClose":
			if uri := extractTextDocumentURI(msg.Params); uri != "" {
				if path, err := FileURIToPath(uri); err == nil {
					delete(w.openedFiles, NormalizePath(path))
				}
			}
		}

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

	// Extract workspace folders (multi-root support)
	if len(params.WorkspaceFolders) > 0 {
		w.workspaceFolders = params.WorkspaceFolders
		w.Log("Workspace folders (%d):", len(params.WorkspaceFolders))
		for _, folder := range params.WorkspaceFolders {
			w.Log("  - %s (%s)", folder.Name, folder.URI)
		}
	} else if w.workspaceRoot != "" {
		// Single-root fallback: construct one folder from rootUri
		w.workspaceFolders = []WorkspaceFolder{
			{URI: PathToFileURI(w.workspaceRoot), Name: filepath.Base(w.workspaceRoot)},
		}
		w.Log("Single workspace folder (from rootUri): %s", w.workspaceRoot)
	}

	// Find app.json to determine AL project root — search all workspace folders
	projectRoot := ""
	for _, folder := range w.workspaceFolders {
		if folderPath, err := FileURIToPath(folder.URI); err == nil {
			appJson := FindAppJSON(folderPath, 5)
			if appJson != "" {
				projectRoot = filepath.Dir(appJson)
				w.Log("Found AL project at: %s (from folder %s)", projectRoot, folder.Name)
				break
			}
		}
	}
	if projectRoot == "" && w.workspaceRoot != "" {
		// Fallback: try workspaceRoot directly
		appJson := FindAppJSON(w.workspaceRoot, 5)
		if appJson != "" {
			projectRoot = filepath.Dir(appJson)
			w.Log("Found AL project at: %s (from rootUri)", projectRoot)
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

	// Add all workspace folders to AL LS initialize params
	if len(w.workspaceFolders) > 0 {
		initParams.WorkspaceFolders = w.workspaceFolders
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

	// Check for another wrapper instance on the same workspace
	w.checkDuplicateInstance()

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

	// Remove executeCommandProvider to avoid "al/runCodeAction already exists" conflict
	// when running alongside the MS AL extension in VS Code
	delete(caps, "executeCommandProvider")

	// Remove semanticTokensProvider — the MS AL LSP's implementation crashes with
	// NullReferenceException, causing 30s timeouts. The MS AL extension handles
	// semantic tokens directly when present.
	delete(caps, "semanticTokensProvider")

	// In VS Code mode, disable document sync so the LanguageClient won't send
	// didOpen/didChange/didClose. This prevents interference when other extensions
	// (like AL Test Runner) bulk-open files, which would trigger our AL LSP
	// instance to process them and potentially conflict with the MS AL extension.
	// Our tools use EnsureFileOpened for on-demand access — no auto-sync needed.
	// See: https://github.com/SShadowS/al-lsp-for-agents/issues/17
	if w.VSCodeMode {
		caps["textDocumentSync"] = 0 // TextDocumentSyncKind.None
		w.Log("VS Code mode: disabled textDocumentSync (tools use EnsureFileOpened)")
	}

	w.Log("Modified server capabilities: added codeLens+callHierarchy, removed executeCommand+semanticTokens")

	modifiedResult, err := json.Marshal(initResult)
	if err != nil {
		w.Log("Failed to marshal modified capabilities: %v", err)
		return result
	}

	return modifiedResult
}

// checkDuplicateInstance detects whether another al-lsp-wrapper instance is
// already serving this workspace and, if so, surfaces an explicit warning
// identifying both clients (VS Code extension, Claude Code plugin, or
// unidentified). The warning text is built from a fixed launcher matrix so
// users get actionable guidance without needing to follow up.
//
// Detection is hardened against PID reuse by verifying that the recorded
// PID's live executable is one of the known wrapper binaries; mismatched
// or vanished PIDs are treated as stale and overwritten.
func (w *ALLSPWrapper) checkDuplicateInstance() {
	if w.workspaceRoot == "" {
		return
	}

	lockPath := lockFilePath(w.workspaceRoot)
	selfPID := os.Getpid()
	selfExePath, _ := os.Executable()
	selfExeName := strings.ToLower(filepath.Base(selfExePath))
	selfParentPID := getParentPid(selfPID)
	selfParentExe := ""
	if selfParentPID > 0 {
		selfParentExe = getProcessExeName(selfParentPID)
	}
	selfLauncher := normalizeLauncher(w.Launcher)

	self := &LockInfo{
		Version:       lockFileVersion,
		PID:           selfPID,
		Launcher:      string(selfLauncher),
		ExePath:       selfExePath,
		ExeName:       selfExeName,
		ParentPID:     selfParentPID,
		ParentExeName: selfParentExe,
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
		WorkspaceRoot: w.workspaceRoot,
	}

	status, other := inspectLockOwner(lockPath, selfPID)
	switch status {
	case lockOwnerLive:
		w.warnAboutDuplicate(self, other)
	case lockOwnerStale:
		w.Log("Lock file at %s is stale (PID %d not a live wrapper); overwriting", lockPath, other.PID)
	}

	if err := writeLockFile(lockPath, self); err != nil {
		w.Log("WARNING: failed to write lock file %s: %v", lockPath, err)
		return
	}
	w.Log("Lock file written: %s (PID %d, launcher=%q)", lockPath, selfPID, self.Launcher)
}

// warnAboutDuplicate emits a window/showMessage warning describing both
// wrapper instances. The text is selected from a launcher matrix so the
// user knows exactly which two clients conflict and how to resolve it.
func (w *ALLSPWrapper) warnAboutDuplicate(self, other *LockInfo) {
	msg := buildDuplicateWarning(self, other)
	w.Log("WARNING: duplicate wrapper detected. self=PID %d launcher=%q | other=PID %d launcher=%q exe=%q parent=%q started=%q",
		self.PID, self.Launcher, other.PID, other.Launcher, other.ExeName, other.ParentExeName, other.StartedAt)
	w.SendNotificationToClient("window/showMessage", map[string]interface{}{
		"type":    2, // Warning
		"message": msg,
	})
}

// buildDuplicateWarning composes the user-facing warning text. The matrix
// covers every launcher combination so the message is always specific:
// the user should never need to ask "which two are conflicting?".
func buildDuplicateWarning(self, other *LockInfo) string {
	selfID := self.LauncherID()
	otherID := other.LauncherID()

	// Fallback: when the other instance reports launcher="" but its parent
	// process clearly identifies it (e.g. spawned by Code.exe), upgrade the
	// label so the user sees a concrete name instead of "unidentified".
	otherInferred := otherID
	if otherInferred == LauncherUnknown {
		otherInferred = inferLauncherFromParent(other.ParentExeName)
	}

	otherDesc := describeInstance(other, otherInferred)
	selfDesc := describeInstance(self, selfID)

	var headline string
	switch {
	case selfID == LauncherVSCode && otherInferred == LauncherClaudeCode,
		selfID == LauncherClaudeCode && otherInferred == LauncherVSCode:
		headline = "AL LSP for Agents: both the VS Code extension and the Claude Code plugin are running an AL Language Server for this workspace. " +
			"This produces duplicate diagnostics and can surface false \"already declared\" compiler errors. " +
			"Keep one and disable the other:\n" +
			"  - To disable in VS Code: Extensions panel > \"AL LSP for Agents\" > Disable (Workspace).\n" +
			"  - To disable in Claude Code: run /plugin and uninstall \"al-language-server-go-windows\" or \"al-language-server-go-linux\"."
	case selfID == LauncherVSCode && otherInferred == LauncherVSCode:
		headline = "AL LSP for Agents: a second VS Code instance is already running the AL LSP wrapper for this workspace. " +
			"This usually means you have the same folder open in two VS Code windows (or an old window did not exit cleanly). " +
			"Close the duplicate VS Code window. If none is visible, end the other wrapper PID listed below from Task Manager."
	case selfID == LauncherClaudeCode && otherInferred == LauncherClaudeCode:
		headline = "AL LSP for Agents: a second Claude Code session is already running the AL LSP wrapper for this workspace. " +
			"This happens if multiple Claude Code instances are open on the same project, or both the production and dev marketplace plugins are installed. " +
			"Close the duplicate Claude Code session, or run /plugin and disable one of the al-language-server-go-* plugins."
	case otherInferred == LauncherUnknown:
		headline = "AL LSP for Agents: another al-lsp-wrapper process is running for this workspace, but its launching client could not be identified. " +
			"This may be a standalone invocation, an older plugin version that does not pass --launcher, or a leftover process from a crashed editor. " +
			"Identify the process from the details below and end it before continuing."
	case selfID == LauncherUnknown:
		headline = "AL LSP for Agents: this wrapper was launched without a --launcher tag, but another known instance is already running for this workspace. " +
			"Stop one of them to avoid duplicate AL Language Servers."
	default:
		headline = "AL LSP for Agents: another al-lsp-wrapper instance is running for this workspace. Disable one of the launching clients."
	}

	return headline + "\n\n" +
		"This instance: " + selfDesc + "\n" +
		"Other instance: " + otherDesc
}

// describeInstance formats a single instance for the warning body. Includes
// the human-readable launcher label, PID, exe path, parent process, and
// how long it has been running so the user can identify and act on it.
func describeInstance(info *LockInfo, id LauncherID) string {
	parts := []string{humanLauncher(id, info.ParentExeName)}
	parts = append(parts, fmt.Sprintf("PID %d", info.PID))
	if info.ExePath != "" {
		parts = append(parts, "exe "+info.ExePath)
	} else if info.ExeName != "" {
		parts = append(parts, "exe "+info.ExeName)
	}
	if info.ParentExeName != "" && info.ParentPID > 0 {
		parts = append(parts, fmt.Sprintf("launched by %s (PID %d)", info.ParentExeName, info.ParentPID))
	}
	if t := info.startedAtTime(); !t.IsZero() {
		age := time.Since(t).Round(time.Second)
		if age < 0 {
			age = 0
		}
		parts = append(parts, fmt.Sprintf("started %s ago", humanDuration(age)))
	}
	return strings.Join(parts, ", ")
}

// inferLauncherFromParent maps a parent process exe name (lowercased) to
// the most likely launcher. Used when an instance reports launcher="" but
// we can identify its parent process. Conservative: returns Unknown unless
// the parent is a clear, well-known editor binary.
func inferLauncherFromParent(parentExe string) LauncherID {
	p := strings.ToLower(parentExe)
	switch {
	case p == "code.exe" || p == "code" || p == "code-insiders.exe" || p == "code-insiders":
		return LauncherVSCode
	case p == "claude.exe" || p == "claude" || strings.HasPrefix(p, "claude-code"):
		return LauncherClaudeCode
	default:
		return LauncherUnknown
	}
}

// humanDuration formats a Duration with a single unit (m / h / d) suitable
// for end-user display. Sub-minute durations show as seconds.
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		days := int(d.Hours()) / 24
		return fmt.Sprintf("%dd%dh", days, int(d.Hours())%24)
	}
}

// removeLockFile removes the workspace lock file on shutdown, but only if
// it still belongs to this process. This protects against deleting a lock
// file that a newer wrapper instance has already taken over.
func (w *ALLSPWrapper) removeLockFile() {
	if w.workspaceRoot == "" {
		return
	}
	lockPath := lockFilePath(w.workspaceRoot)
	info, err := readLockFile(lockPath)
	if err != nil {
		return
	}
	if info.PID == os.Getpid() {
		if err := os.Remove(lockPath); err != nil {
			w.Log("Failed to remove lock file %s: %v", lockPath, err)
			return
		}
		w.Log("Lock file removed: %s", lockPath)
	}
}

// SendNotificationToClient sends a notification to the client (VS Code / Claude Code)
func (w *ALLSPWrapper) SendNotificationToClient(method string, params interface{}) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		w.Log("Failed to marshal notification params: %v", err)
		return
	}
	msg := &Message{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
	}
	if err := w.writeToClient(msg); err != nil {
		w.Log("Failed to send notification to client: %v", err)
	}
}

// SendRequestToLSP sends a request to the AL LSP and waits for response (30s timeout)
func (w *ALLSPWrapper) SendRequestToLSP(method string, params interface{}) (*Message, error) {
	return w.SendRequestToLSPWithTimeout(method, params, 30*time.Second)
}

// SendRequestToLSPWithTimeout sends a request to the AL LSP with a custom timeout
func (w *ALLSPWrapper) SendRequestToLSPWithTimeout(method string, params interface{}, timeout time.Duration) (*Message, error) {
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
	if err := w.writeToLSP(msg); err != nil {
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
	case <-time.After(timeout):
		w.pendingMu.Lock()
		delete(w.pendingReqs, id)
		w.pendingMu.Unlock()
		return nil, fmt.Errorf("timeout waiting for response to %s (after %v)", method, timeout)
	}
}

// SendNotificationToLSP sends a notification to the AL LSP
func (w *ALLSPWrapper) SendNotificationToLSP(method string, params interface{}) error {
	msg, err := NewNotification(method, params)
	if err != nil {
		return err
	}

	w.Log("Sending notification to AL LSP: %s", method)
	return w.writeToLSP(msg)
}

// writeToLSP writes a message to the AL LSP stdin with mutex protection.
// This is needed because handleServerRequest (readFromLSP goroutine) and
// SendRequestToLSP (readFromClient goroutine) can write concurrently.
func (w *ALLSPWrapper) writeToLSP(msg *Message) error {
	w.stdinMu.Lock()
	defer w.stdinMu.Unlock()
	return WriteMessage(w.stdin, msg)
}

// extractTextDocumentURI extracts the textDocument.uri from raw JSON-RPC params.
// Used for didOpen/didClose/didChange/didSave notifications.
func extractTextDocumentURI(params json.RawMessage) string {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if json.Unmarshal(params, &p) == nil {
		return p.TextDocument.URI
	}
	return ""
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

// EnsureProjectInitialized ensures the project for a file is initialized and active.
// One-time setup (loadManifest, didChangeConfiguration) is cached per project.
// Workspace activation (al/setActiveWorkspace) is sent whenever the active project changes.
func (w *ALLSPWrapper) EnsureProjectInitialized(filePath string) error {
	projectRoot := GetProjectRoot(filePath)
	if projectRoot == "" {
		w.Log("No AL project found for: %s", filePath)
		return nil // Not an error - might not be an AL file
	}

	normalizedRoot := NormalizePath(projectRoot)

	// One-time initialization: workspace config, manifest, app.json
	if !w.initializedProjects[normalizedRoot] {
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

		// Send al/loadManifest (like VS Code does) — use longer timeout for large projects
		if manifest != nil && manifest.Raw != "" {
			loadParams := LoadManifestParams{
				ProjectFolder: normalizedRoot,
				Manifest:      manifest.Raw,
			}
			if _, err := w.SendRequestToLSPWithTimeout("al/loadManifest", loadParams, 60*time.Second); err != nil {
				w.Log("al/loadManifest failed (non-fatal): %v", err)
			} else {
				w.Log("al/loadManifest succeeded")
			}
		}

		w.initializedProjects[normalizedRoot] = true
		w.Log("Project initialized: %s", normalizedRoot)
	}

	// Activation: switch the AL LS to this project if it's not already active
	if w.activeProject != normalizedRoot {
		w.Log("Activating project: %s (was: %s)", normalizedRoot, w.activeProject)

		manifest := w.GetManifest(normalizedRoot)
		folderIndex := w.getWorkspaceFolderIndex(normalizedRoot)
		activeParams := NewActiveWorkspaceParams(normalizedRoot, manifest, folderIndex)
		if _, err := w.SendRequestToLSPWithTimeout("al/setActiveWorkspace", activeParams, 60*time.Second); err != nil {
			w.Log("Failed to set active workspace: %v", err)
		}

		// Wait for project to load (only on first activation)
		if w.activeProject == "" {
			w.waitForProjectLoad(normalizedRoot)
		}

		w.activeProject = normalizedRoot
	}

	return nil
}

// getWorkspaceFolderIndex returns the index of a project root in the workspace folders list.
// Returns 0 if not found (safe default).
func (w *ALLSPWrapper) getWorkspaceFolderIndex(normalizedRoot string) int {
	rootURI := PathToFileURI(normalizedRoot)
	for i, folder := range w.workspaceFolders {
		if folder.URI == rootURI {
			return i
		}
		// Also try comparing by path
		if path, err := FileURIToPath(folder.URI); err == nil {
			if NormalizePath(path) == normalizedRoot {
				return i
			}
		}
	}
	return 0
}

// UpdateWorkspaceFolders adds and removes workspace folders from internal state.
func (w *ALLSPWrapper) UpdateWorkspaceFolders(added []WorkspaceFolder, removed []WorkspaceFolder) {
	// Remove folders
	for _, r := range removed {
		for i, f := range w.workspaceFolders {
			if f.URI == r.URI {
				w.workspaceFolders = append(w.workspaceFolders[:i], w.workspaceFolders[i+1:]...)
				break
			}
		}
	}
	// Add folders
	w.workspaceFolders = append(w.workspaceFolders, added...)
	w.Log("Updated workspace folders (%d total)", len(w.workspaceFolders))
}

func (w *ALLSPWrapper) waitForProjectLoad(workspacePath string) {
	params := map[string]string{
		"workspacePath": workspacePath,
	}

	// Poll for project load status (up to 2 minutes)
	for i := 0; i < 120; i++ {
		resp, err := w.SendRequestToLSPWithTimeout("al/hasProjectClosureLoadedRequest", params, 60*time.Second)
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
			w.Log("Project loaded successfully (after %d polls)", i+1)
			return
		}

		if (i+1)%10 == 0 {
			w.Log("Still waiting for project load... (%d seconds)", i+1)
		}
		time.Sleep(time.Second)
	}

	w.Log("Timeout waiting for project load (2 minutes), continuing anyway")
}

// GetCallHierarchyServer returns the call hierarchy server
func (w *ALLSPWrapper) GetCallHierarchyServer() *CallHierarchyServer {
	return w.callHierarchyServer
}

// GetALMcpServer returns the almcp MCP server manager.
func (w *ALLSPWrapper) GetALMcpServer() *ALMcpServer {
	return w.almcpServer
}

// WorkspaceFolders returns a snapshot of the current workspace folders.
// Handlers use this to locate the project root for cache materialization
// (al-preview URI → file:// cache path).
func (w *ALLSPWrapper) WorkspaceFolders() []WorkspaceFolder {
	out := make([]WorkspaceFolder, len(w.workspaceFolders))
	copy(out, w.workspaceFolders)
	return out
}

// PreviewCache lazily constructs and returns the previewCache rooted at
// the first workspace folder. Returns nil when no workspace folder is
// known yet (the wrapper hasn't received `initialize` or didOpen).
func (w *ALLSPWrapper) PreviewCache() *previewCache {
	w.previewCacheMu.Lock()
	defer w.previewCacheMu.Unlock()
	if w.previewCache != nil {
		return w.previewCache
	}
	folders := w.workspaceFolders
	if len(folders) == 0 {
		return nil
	}
	root, err := FileURIToPath(folders[0].URI)
	if err != nil || root == "" {
		return nil
	}
	w.previewCache = newPreviewCacheForWorkspace(root)
	return w.previewCache
}

// startCallHierarchyServer starts the al-call-hierarchy server
func (w *ALLSPWrapper) startCallHierarchyServer() {
	w.callHierarchyServer = NewCallHierarchyServer(w.Log)
	w.callHierarchyServer.NoDiagnostics = w.NoDiagnostics
	w.callHierarchyServer.SetClientWriter(w.clientWriter)
	w.callHierarchyServer.SetSanitizer(func(msg *Message) {
		if n, orig, norm := SanitizeOutboundMessage(msg); n > 0 {
			w.uriStats.record(w.Log, "call-hierarchy->client", msg.Method, n, orig, norm)
		}
		// Merge with AL LS diagnostics so a later AL LS publish (e.g. triggered
		// by prepareCallHierarchy) can't erase these, and vice versa (#20 #2).
		if msg != nil && msg.Method == "textDocument/publishDiagnostics" && w.diagMerger != nil {
			w.diagMerger.MergePublishDiagnostics(diagBackendCallHierarchy, msg)
		}
	})

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

	// Initialize with all workspace folders
	workspacePath := w.workspaceRoot
	if workspacePath == "" {
		workspacePath, _ = os.Getwd()
	}
	workspaceURI := PathToFileURI(workspacePath)

	// Use stored workspace folders (multi-root) or construct single folder
	workspaceFolders := w.workspaceFolders
	if len(workspaceFolders) == 0 {
		workspaceFolders = []WorkspaceFolder{
			{URI: workspaceURI, Name: filepath.Base(workspacePath)},
		}
	}

	w.Log("Initializing call hierarchy with %d workspace folders", len(workspaceFolders))
	for _, folder := range workspaceFolders {
		w.Log("  - %s (%s)", folder.Name, folder.URI)
	}
	if err := w.callHierarchyServer.Initialize(workspaceURI, workspaceFolders); err != nil {
		w.Log("Failed to initialize al-call-hierarchy: %v", err)
		w.callHierarchyServer.Shutdown()
		w.callHierarchyServer = nil
		return
	}

	w.Log("al-call-hierarchy server ready")
}
