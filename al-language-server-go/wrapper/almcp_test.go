package wrapper

import (
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
