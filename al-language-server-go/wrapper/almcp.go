package wrapper

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// mcpBackend describes how to launch an almcp MCP server.
type mcpBackend struct {
	kind    string // "nuget" | "bundled"
	command string // executable path
}

// args builds the spawn arguments for the given project + package cache.
func (b mcpBackend) args(projectDir, pkgCache string) []string {
	if b.kind == "nuget" {
		return []string{"launchmcpserver", projectDir, "--packagecachepath", pkgCache, "--transport", "stdio", "--nolog"}
	}
	return []string{"--transport", "stdio", "--projects", projectDir, "--packagecachepath", pkgCache, "--nolog"}
}

// findNugetALTool locates the nuget `al` dotnet global tool, if installed.
func findNugetALTool() string {
	name := "al"
	if runtime.GOOS == "windows" {
		name = "al.exe"
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".dotnet", "tools", name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if lp, err := exec.LookPath(name); err == nil {
		return lp
	}
	return ""
}

// discoverMcpBackend prefers the nuget al tool (15 tools incl al_inspectpage),
// falling back to the extension-bundled almcp (12 tools, no al_inspectpage).
func discoverMcpBackend(extensionPath string) (mcpBackend, bool) {
	if al := findNugetALTool(); al != "" {
		return mcpBackend{kind: "nuget", command: al}, true
	}
	bundled := GetALMcpExecutable(extensionPath)
	if _, err := os.Stat(bundled); err == nil {
		return mcpBackend{kind: "bundled", command: bundled}, true
	}
	return mcpBackend{}, false
}

// ALMcpServer owns one almcp child process and an MCP client to it.
type ALMcpServer struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	client     *MCPClient
	tools      map[string]bool
	projectDir string
	backend    mcpBackend
	logFunc    func(format string, args ...interface{})
}

func NewALMcpServer(logFunc func(format string, args ...interface{})) *ALMcpServer {
	return &ALMcpServer{tools: map[string]bool{}, logFunc: logFunc}
}

func (s *ALMcpServer) log(format string, args ...interface{}) {
	if s.logFunc != nil {
		s.logFunc("[almcp] "+format, args...)
	}
}

// SetExtensionPath resolves the backend once the extension path is known.
// Returns false when no almcp backend can be found.
func (s *ALMcpServer) SetExtensionPath(extensionPath string) bool {
	b, ok := discoverMcpBackend(extensionPath)
	if ok {
		s.mu.Lock()
		s.backend = b
		s.mu.Unlock()
		s.log("backend: kind=%s command=%s", b.kind, b.command)
	}
	return ok
}

// EnsureRunning lazily spawns + initializes almcp for the given project.
func (s *ALMcpServer) EnsureRunning(projectDir, pkgCache string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil && s.cmd.ProcessState == nil {
		return nil
	}
	if s.backend.command == "" {
		return fmt.Errorf("no almcp backend available")
	}

	s.cmd = exec.Command(s.backend.command, s.backend.args(projectDir, pkgCache)...)
	stdin, err := s.cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := s.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := s.cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := s.cmd.Start(); err != nil {
		s.cmd = nil
		return fmt.Errorf("failed to start almcp: %w", err)
	}
	s.log("started pid=%d (%s)", s.cmd.Process.Pid, s.backend.kind)
	addProcessToJob(s.cmd.Process)
	go drainReader(stderr, s.log)

	s.client = NewMCPClient(stdin, bufio.NewReader(stdout), s.logFunc)
	if err := s.client.Initialize("al-lsp-wrapper"); err != nil {
		s.stopLocked()
		return err
	}
	names, err := s.client.ListTools()
	if err != nil {
		s.stopLocked()
		return err
	}
	s.tools = map[string]bool{}
	for _, n := range names {
		s.tools[n] = true
	}
	s.projectDir = projectDir
	s.log("ready, tools=%v", names)
	return nil
}

// HasTool reports whether the running backend exposes the named tool.
func (s *ALMcpServer) HasTool(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tools[name]
}

// CallTool forwards to the MCP client.
func (s *ALMcpServer) CallTool(name string, args interface{}) (*ToolCallResult, error) {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return nil, fmt.Errorf("almcp not running")
	}
	return client.CallTool(name, args)
}

// AddProject registers an additional project with the running server.
func (s *ALMcpServer) AddProject(projectDir string) {
	if s.HasTool("al_addproject") {
		_, _ = s.CallTool("al_addproject", map[string]interface{}{"projectPath": projectDir})
	}
}

func (s *ALMcpServer) stopLocked() {
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	s.cmd = nil
	s.client = nil
	s.tools = map[string]bool{}
}

// Shutdown terminates the almcp process.
func (s *ALMcpServer) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil {
		return
	}
	s.log("stopping almcp")
	s.stopLocked()
}

func drainReader(r io.Reader, log func(string, ...interface{})) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		log("stderr: %s", sc.Text())
	}
}
