package wrapper

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
