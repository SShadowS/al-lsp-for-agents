package wrapper

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// createTestVsix creates a minimal .vsix (zip) with an extension/ subdirectory
func createTestVsix(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Failed to create vsix: %v", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)

	// Add a file inside extension/ (this is where vsix content lives)
	binDir := "win32"
	if runtime.GOOS == "linux" {
		binDir = "linux"
	}

	fw, _ := w.Create("extension/bin/" + binDir + "/Microsoft.Dynamics.Nav.EditorServices.Host.exe")
	fw.Write([]byte("fake-binary"))

	fw2, _ := w.Create("extension/package.json")
	fw2.Write([]byte(`{"name": "al", "version": "18.0.2190758"}`))

	// Add a file outside extension/ (should be ignored)
	fw3, _ := w.Create("[Content_Types].xml")
	fw3.Write([]byte("<Types/>"))

	w.Close()
}

func TestExtractVsix(t *testing.T) {
	tmpDir := t.TempDir()
	vsixPath := filepath.Join(tmpDir, "test.vsix")
	createTestVsix(t, vsixPath)

	targetDir := filepath.Join(tmpDir, "extracted")

	err := extractVsix(vsixPath, targetDir)
	if err != nil {
		t.Fatalf("Failed to extract vsix: %v", err)
	}

	// Check that extension/ contents were extracted (without the extension/ prefix)
	packageJSON := filepath.Join(targetDir, "package.json")
	if _, err := os.Stat(packageJSON); os.IsNotExist(err) {
		t.Error("Expected package.json to be extracted")
	}

	// Check binary was extracted
	binDir := "win32"
	if runtime.GOOS == "linux" {
		binDir = "linux"
	}
	binaryPath := filepath.Join(targetDir, "bin", binDir, "Microsoft.Dynamics.Nav.EditorServices.Host.exe")
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		t.Error("Expected binary to be extracted")
	}

	// Content_Types.xml should NOT be in targetDir (it's outside extension/)
	contentTypes := filepath.Join(targetDir, "[Content_Types].xml")
	if _, err := os.Stat(contentTypes); err == nil {
		t.Error("Content_Types.xml should not be extracted")
	}
}

func TestExtractVsixMissingFile(t *testing.T) {
	err := extractVsix("/nonexistent/file.vsix", "/tmp/out")
	if err == nil {
		t.Fatal("Expected error for missing vsix file")
	}
}
