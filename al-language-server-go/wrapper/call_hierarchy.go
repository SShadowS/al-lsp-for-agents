package wrapper

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// CallHierarchyServer manages the al-call-hierarchy subprocess
type CallHierarchyServer struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      *bufio.Reader
	stderr      io.ReadCloser
	requestID   int
	initialized bool
	rootURI     string

	// NoDiagnostics forwards --no-diagnostics to al-sem so it skips
	// code-quality analysis entirely. Set from the wrapper's own flag.
	NoDiagnostics bool

	// notifStats aggregates forwarded notifications so a chatty backend
	// cannot bury the log (see notification_stats.go for the incident that
	// motivated this).
	notifStats *notificationStats

	mu          sync.Mutex
	pendingMu   sync.Mutex
	pendingReqs map[int]chan *Message

	// Client writer for forwarding notifications
	clientWriter   io.Writer
	clientWriterMu sync.Mutex

	// sanitizeOutbound, if set, is called on every message forwarded to the
	// client. The wrapper registers a callback that runs the URI sanitizer
	// and feeds rewrite counts into the shared uriSanitizationStats counter
	// so call-hierarchy malformation is visible alongside AL-LS malformation.
	sanitizeOutbound func(*Message)

	logFunc func(format string, args ...interface{})
}

// SetSanitizer registers a callback invoked on every message about to be
// forwarded to the client. Used by ALLSPWrapper to centralize URI sanitation.
func (s *CallHierarchyServer) SetSanitizer(fn func(*Message)) {
	s.sanitizeOutbound = fn
}

// NewCallHierarchyServer creates a new CallHierarchyServer
func NewCallHierarchyServer(logFunc func(format string, args ...interface{})) *CallHierarchyServer {
	return &CallHierarchyServer{
		pendingReqs: make(map[int]chan *Message),
		logFunc:     logFunc,
		notifStats:  newNotificationStats(),
	}
}

func (s *CallHierarchyServer) log(format string, args ...interface{}) {
	if s.logFunc != nil {
		s.logFunc("[CallHierarchy] "+format, args...)
	}
}

// SetClientWriter sets the client writer for forwarding notifications
func (s *CallHierarchyServer) SetClientWriter(writer io.Writer) {
	s.clientWriterMu.Lock()
	defer s.clientWriterMu.Unlock()
	s.clientWriter = writer
}

// FindExecutable finds the al-call-hierarchy executable
func (s *CallHierarchyServer) FindExecutable() string {
	// Get the directory of the current executable
	exePath, err := os.Executable()
	if err != nil {
		s.log("Failed to get executable path: %v", err)
		return ""
	}
	binDir := filepath.Dir(exePath)

	// Determine executable name based on platform
	var exeName string
	switch runtime.GOOS {
	case "windows":
		exeName = "al-call-hierarchy.exe"
	default:
		exeName = "al-call-hierarchy"
	}

	// Check in the same bin directory
	exePath = filepath.Join(binDir, exeName)
	if _, err := os.Stat(exePath); err == nil {
		s.log("Found al-call-hierarchy at: %s", exePath)
		return exePath
	}

	s.log("al-call-hierarchy not found at: %s", exePath)
	return ""
}

// Start starts the al-call-hierarchy process
func (s *CallHierarchyServer) Start(executable string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil {
		return fmt.Errorf("al-call-hierarchy already running")
	}

	s.log("Starting al-call-hierarchy: %s", executable)

	args := []string{"--no-watcher"}
	if s.NoDiagnostics {
		// The client discards code-quality diagnostics, so computing them
		// would be a full analysis of every opened root for nothing.
		args = append(args, "--no-diagnostics")
	}
	s.log("Starting al-call-hierarchy with args: %v", args)
	s.cmd = exec.Command(executable, args...)

	var err error
	s.stdin, err = s.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	stdoutPipe, err := s.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	s.stdout = bufio.NewReader(stdoutPipe)

	s.stderr, err = s.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start al-call-hierarchy: %w", err)
	}

	s.log("al-call-hierarchy started (PID: %d)", s.cmd.Process.Pid)

	// Add to Windows job object for automatic cleanup
	addProcessToJob(s.cmd.Process)

	// Read stderr in background
	go s.readStderr()

	// Read responses in background
	go s.readResponses()

	return nil
}

// readStderr drains stderr to prevent blocking.
//
// It also samples the child's memory whenever al-sem reports finishing a
// root's program snapshot. That is the exact moment this process's memory
// steps up (each root retains its own dependency closure), and there is at
// most one such line per configured root, so the sampling is bounded by
// workspace size rather than by traffic. A memory report was the single
// thing missing when diagnosing the 17 GB multi-root incident: the log
// described everything except the resource being asked about.
func (s *CallHierarchyServer) readStderr() {
	scanner := bufio.NewScanner(s.stderr)
	for scanner.Scan() {
		line := scanner.Text()
		s.log("stderr: %s", line)
		if strings.Contains(line, snapshotBuiltMarker) {
			s.logMemory("after root snapshot build")
		}
	}
}

// snapshotBuiltMarker is al-sem's per-root "snapshot is ready" log text.
// Matched as a substring rather than parsed: if al-sem's wording drifts we
// silently lose a diagnostic sample, never correctness.
const snapshotBuiltMarker = "Built program snapshot for workspace root"

// logMemory reports the al-sem child's working set. Silent when the process
// is gone or the platform cannot report it — diagnostics must never fail a
// running session.
func (s *CallHierarchyServer) logMemory(when string) {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	if cur, peak, ok := processMemoryMB(cmd.Process.Pid); ok {
		s.log("memory %s: working set %d MB (peak %d MB)", when, cur, peak)
	}
}

// LogSessionSummary emits the end-of-session tallies: what was forwarded and
// what it cost. One line each, only when there is something to report.
func (s *CallHierarchyServer) LogSessionSummary() {
	if s.notifStats != nil {
		if summary := s.notifStats.Summary(); summary != "" {
			s.log("session totals: forwarded notifications %s", summary)
		}
	}
	s.logMemory("at shutdown")
}

// readResponses reads responses from the al-call-hierarchy process
func (s *CallHierarchyServer) readResponses() {
	for {
		msg, err := ReadMessage(s.stdout)
		if err != nil {
			if err == io.EOF {
				s.log("al-call-hierarchy connection closed")
			} else {
				s.log("Error reading from al-call-hierarchy: %v", err)
			}
			return
		}

		if msg.IsResponse() {
			id := msg.GetIDInt()
			s.pendingMu.Lock()
			if ch, ok := s.pendingReqs[id]; ok {
				ch <- msg
				delete(s.pendingReqs, id)
			}
			s.pendingMu.Unlock()
		} else if msg.IsNotification() {
			// Forward notifications (like textDocument/publishDiagnostics) to client
			s.clientWriterMu.Lock()
			writer := s.clientWriter
			s.clientWriterMu.Unlock()

			if writer != nil {
				// Aggregated, not per-message: al-sem publishes one
				// diagnostics notification per file per swap, which on a
				// large workspace is thousands of identical log lines.
				if shouldLog, total := s.notifStats.record(msg.Method); shouldLog {
					s.log("Forwarding notifications to client: %s (session total %d)", msg.Method, total)
				}
				if s.sanitizeOutbound != nil {
					s.sanitizeOutbound(msg)
				}
				if err := WriteMessage(writer, msg); err != nil {
					s.log("Error forwarding notification: %v", err)
				}
			}
		}
	}
}

// IsAlive checks if the process is still running
func (s *CallHierarchyServer) IsAlive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil && s.cmd.ProcessState == nil
}

// IsInitialized returns whether the server is initialized
func (s *CallHierarchyServer) IsInitialized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized
}

// Stop stops the al-call-hierarchy process
func (s *CallHierarchyServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd == nil {
		return
	}

	s.log("Stopping al-call-hierarchy...")

	// Try graceful shutdown
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}

	s.cmd = nil
	s.initialized = false
	s.log("al-call-hierarchy stopped")
}

// SendRequest sends a request and waits for response
func (s *CallHierarchyServer) SendRequest(method string, params interface{}) (*Message, error) {
	if !s.IsAlive() {
		return nil, fmt.Errorf("al-call-hierarchy not running")
	}

	s.mu.Lock()
	s.requestID++
	id := s.requestID
	s.mu.Unlock()

	msg, err := NewRequest(id, method, params)
	if err != nil {
		return nil, err
	}

	// Create response channel
	respChan := make(chan *Message, 1)
	s.pendingMu.Lock()
	s.pendingReqs[id] = respChan
	s.pendingMu.Unlock()

	// Send request
	s.log("Sending request: method=%s id=%d", method, id)
	if err := WriteMessage(s.stdin, msg); err != nil {
		s.pendingMu.Lock()
		delete(s.pendingReqs, id)
		s.pendingMu.Unlock()
		return nil, err
	}

	// Wait for response with timeout
	select {
	case resp := <-respChan:
		s.log("Received response: id=%d", id)
		return resp, nil
	case <-time.After(30 * time.Second):
		s.pendingMu.Lock()
		delete(s.pendingReqs, id)
		s.pendingMu.Unlock()
		return nil, fmt.Errorf("timeout waiting for response to %s", method)
	}
}

// SendNotification sends a notification (no response expected)
func (s *CallHierarchyServer) SendNotification(method string, params interface{}) error {
	if !s.IsAlive() {
		return fmt.Errorf("al-call-hierarchy not running")
	}

	msg, err := NewNotification(method, params)
	if err != nil {
		return err
	}

	s.log("Sending notification: %s", method)
	return WriteMessage(s.stdin, msg)
}

// Initialize initializes the al-call-hierarchy server
func (s *CallHierarchyServer) Initialize(rootURI string, workspaceFolders []WorkspaceFolder) error {
	s.mu.Lock()
	if s.initialized {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	s.rootURI = rootURI

	params := map[string]interface{}{
		"processId":        os.Getpid(),
		"capabilities":     map[string]interface{}{},
		"rootUri":          rootURI,
		"workspaceFolders": workspaceFolders,
	}

	response, err := s.SendRequest("initialize", params)
	if err != nil {
		return fmt.Errorf("initialize request failed: %w", err)
	}

	// Log al-call-hierarchy server capabilities
	s.log("=== CALL HIERARCHY SERVER CAPABILITIES ===")
	s.log("%s", string(response.Result))
	s.log("=== END CALL HIERARCHY SERVER CAPABILITIES ===")

	if response.Error != nil {
		return fmt.Errorf("initialize error: %s", response.Error.Message)
	}

	// Check capabilities
	var result struct {
		Capabilities struct {
			CallHierarchyProvider bool `json:"callHierarchyProvider"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(response.Result, &result); err == nil {
		if !result.Capabilities.CallHierarchyProvider {
			return fmt.Errorf("al-call-hierarchy does not support callHierarchyProvider")
		}
	}

	// Send initialized notification
	if err := s.SendNotification("initialized", nil); err != nil {
		return fmt.Errorf("failed to send initialized notification: %w", err)
	}

	s.mu.Lock()
	s.initialized = true
	s.mu.Unlock()

	s.log("Initialized successfully")
	return nil
}

// Request sends a call hierarchy request
func (s *CallHierarchyServer) Request(method string, params interface{}) (*Message, error) {
	if !s.IsInitialized() {
		return nil, fmt.Errorf("al-call-hierarchy not initialized")
	}

	return s.SendRequest(method, params)
}

// Shutdown gracefully shuts down the server
func (s *CallHierarchyServer) Shutdown() {
	if s.IsAlive() {
		// Try to send shutdown request
		s.SendRequest("shutdown", nil)
		s.SendNotification("exit", nil)
	}
	s.Stop()
}
