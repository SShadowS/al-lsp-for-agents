package wrapper

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMcpBackendArgs(t *testing.T) {
	nuget := mcpBackend{kind: "nuget", command: "al"}
	got := nuget.args("C:/proj", "C:/proj/.alpackages")
	want := []string{"launchmcpserver", "C:/proj", "--packagecachepath", "C:/proj/.alpackages", "--transport", "stdio", "--nolog"}
	if !equalStrings(got, want) {
		t.Fatalf("nuget args = %v, want %v", got, want)
	}

	bundled := mcpBackend{kind: "bundled", command: "almcp"}
	got = bundled.args("C:/proj", "C:/proj/.alpackages")
	want = []string{"--transport", "stdio", "--projects", "C:/proj", "--packagecachepath", "C:/proj/.alpackages", "--nolog"}
	if !equalStrings(got, want) {
		t.Fatalf("bundled args = %v, want %v", got, want)
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
