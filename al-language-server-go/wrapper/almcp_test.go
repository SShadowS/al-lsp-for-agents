package wrapper

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestMcpBackendArgs(t *testing.T) {
	// Use a path with no os.PathListSeparator so it stays a single cache flag
	// on every platform (Windows ';' vs Linux ':').
	const proj = "/proj"
	const cache = "/proj/.alpackages"

	nuget := mcpBackend{kind: "nuget", command: "al"}
	got := nuget.args(proj, cache)
	want := []string{"launchmcpserver", proj, "--packagecachepath", cache, "--transport", "stdio", "--nolog"}
	if !equalStrings(got, want) {
		t.Fatalf("nuget args = %v, want %v", got, want)
	}

	bundled := mcpBackend{kind: "bundled", command: "almcp"}
	got = bundled.args(proj, cache)
	want = []string{"--transport", "stdio", "--projects", proj, "--packagecachepath", cache, "--nolog"}
	if !equalStrings(got, want) {
		t.Fatalf("bundled args = %v, want %v", got, want)
	}
}

func TestMcpBackendArgsMultipleCachePaths(t *testing.T) {
	// Multiple cache paths must become repeated --packagecachepath flags, NOT a
	// single separator-joined value: the Microsoft almcp backends do not split
	// the value, so a joined string points at one nonexistent path and returns
	// no symbols (regression: al_inspectpage returned an empty control tree).
	sep := string(os.PathListSeparator)
	cache := "/proj/.alpackages" + sep + "/repo/.alpackages"

	nuget := mcpBackend{kind: "nuget", command: "al"}
	got := nuget.args("/proj", cache)
	want := []string{
		"launchmcpserver", "/proj",
		"--packagecachepath", "/proj/.alpackages",
		"--packagecachepath", "/repo/.alpackages",
		"--transport", "stdio", "--nolog",
	}
	if !equalStrings(got, want) {
		t.Fatalf("nuget multi-cache args = %v, want %v", got, want)
	}

	bundled := mcpBackend{kind: "bundled", command: "almcp"}
	got = bundled.args("/proj", cache)
	want = []string{
		"--transport", "stdio", "--projects", "/proj",
		"--packagecachepath", "/proj/.alpackages",
		"--packagecachepath", "/repo/.alpackages",
		"--nolog",
	}
	if !equalStrings(got, want) {
		t.Fatalf("bundled multi-cache args = %v, want %v", got, want)
	}
}

func TestGetALMcpExecutable(t *testing.T) {
	got := GetALMcpExecutable("/ext")
	var want string
	switch runtime.GOOS {
	case "windows":
		want = filepath.Join("/ext", "bin", "win32", "almcp.exe")
	case "darwin":
		want = filepath.Join("/ext", "bin", "darwin", "almcp")
	default:
		want = filepath.Join("/ext", "bin", "linux", "almcp")
	}
	if got != want {
		t.Fatalf("GetALMcpExecutable = %q, want %q", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func buildFakeAlmcp(t *testing.T) string {
	t.Helper()
	src := filepath.Join("testdata", "fakealmcp", "main.go")
	out := filepath.Join(t.TempDir(), "fakealmcp")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build fake almcp: %v", err)
	}
	return out
}

func TestALMcpServerLifecycle(t *testing.T) {
	fake := buildFakeAlmcp(t)

	s := NewALMcpServer(func(string, ...interface{}) {})
	s.backend = mcpBackend{kind: "bundled", command: fake} // inject fake
	if err := s.EnsureRunning("C:/proj", "C:/proj/.alpackages"); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if !s.HasTool("al_symbolrelations") {
		t.Fatalf("expected al_symbolrelations in capability set")
	}
	res, err := s.CallTool("al_symbolrelations", map[string]interface{}{"parameters": map[string]string{"symbolName": "Customer"}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError")
	}
	s.Shutdown()
}

func TestALMcpServerEnsureProjectAddsSecond(t *testing.T) {
	fake := buildFakeAlmcp(t)

	s := NewALMcpServer(func(string, ...interface{}) {})
	s.backend = mcpBackend{kind: "bundled", command: fake}

	projA := "C:/projA"
	projB := "C:/projB"

	// First call: spawns server with projA.
	if err := s.EnsureProject(projA, projA+"/.alpackages"); err != nil {
		t.Fatalf("EnsureProject(A): %v", err)
	}
	firstCmd := s.cmd

	// Second call with same project: no respawn, no AddProject needed.
	if err := s.EnsureProject(projA, projA+"/.alpackages"); err != nil {
		t.Fatalf("EnsureProject(A) again: %v", err)
	}
	if s.cmd != firstCmd {
		t.Fatalf("expected same process after second EnsureProject(A)")
	}

	// Third call with new project: still same process, AddProject called.
	if err := s.EnsureProject(projB, projB+"/.alpackages"); err != nil {
		t.Fatalf("EnsureProject(B): %v", err)
	}
	if s.cmd != firstCmd {
		t.Fatalf("expected same process after EnsureProject(B) — should not respawn")
	}

	// Both projects must be in the set.
	s.mu.Lock()
	hasA := s.projects[projA]
	hasB := s.projects[projB]
	s.mu.Unlock()
	if !hasA {
		t.Fatalf("projA not in projects set")
	}
	if !hasB {
		t.Fatalf("projB not in projects set after EnsureProject(B)")
	}

	s.Shutdown()
}

func TestALMcpServerRespawnAfterCrash(t *testing.T) {
	fake := buildFakeAlmcp(t)

	s := NewALMcpServer(func(string, ...interface{}) {})
	s.backend = mcpBackend{kind: "bundled", command: fake} // inject fake
	if err := s.EnsureRunning("C:/proj", "C:/proj/.alpackages"); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}

	// Simulate an unexpected child death (no Shutdown).
	if err := s.cmd.Process.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	// Wait for the background reaper to mark the child exited (~2s cap).
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		exited := s.exited
		s.mu.Unlock()
		if exited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child never marked exited after kill (background Wait not reaping)")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Respawning must succeed, not hang on a stale "already running" guard.
	if err := s.EnsureRunning("C:/proj", "C:/proj/.alpackages"); err != nil {
		t.Fatalf("EnsureRunning after crash: %v", err)
	}
	if !s.HasTool("al_symbolrelations") {
		t.Fatalf("expected al_symbolrelations after respawn")
	}
	res, err := s.CallTool("al_symbolrelations", map[string]interface{}{"parameters": map[string]string{"symbolName": "Customer"}})
	if err != nil {
		t.Fatalf("CallTool after respawn: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError after respawn")
	}
	s.Shutdown()
}
