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

	mu         sync.Mutex
	pendingMu  sync.Mutex
	pendingReqs map[int]chan *Message

	logFunc func(format string, args ...interface{})
}

// NewCallHierarchyServer creates a new CallHierarchyServer
func NewCallHierarchyServer(logFunc func(format string, args ...interface{})) *CallHierarchyServer {
	return &CallHierarchyServer{
		pendingReqs: make(map[int]chan *Message),
		logFunc:     logFunc,
	}
}

func (s *CallHierarchyServer) log(format string, args ...interface{}) {
	if s.logFunc != nil {
		s.logFunc("[CallHierarchy] "+format, args...)
	}
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

	s.cmd = exec.Command(executable)

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

// readStderr drains stderr to prevent blocking
func (s *CallHierarchyServer) readStderr() {
	scanner := bufio.NewScanner(s.stderr)
	for scanner.Scan() {
		s.log("stderr: %s", scanner.Text())
	}
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
