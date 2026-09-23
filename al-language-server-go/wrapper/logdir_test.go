package wrapper

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestGetLogDir_HonorsTempEnv: the log goes where the OS temp dir points
// (TMPDIR on Linux/macOS, TEMP on Windows), not a hardcoded /tmp.
func TestGetLogDir_HonorsTempEnv(t *testing.T) {
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("TMP", dir)
		t.Setenv("TEMP", dir)
	} else {
		t.Setenv("TMPDIR", dir)
	}
	if got := GetLogDir(); !strings.EqualFold(filepath.Clean(got), filepath.Clean(dir)) {
		t.Errorf("GetLogDir() = %q, want %q", got, dir)
	}
}
