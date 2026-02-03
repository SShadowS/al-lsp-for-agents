package wrapper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createTestExtension creates a mock AL extension directory
func createTestExtension(t *testing.T, baseDir, variant, version string) string {
	extDir := filepath.Join(baseDir, variant, "extensions", "ms-dynamics-smb.al-"+version)
	if err := os.MkdirAll(extDir, 0755); err != nil {
		t.Fatalf("Failed to create test extension directory: %v", err)
	}
	return extDir
}

func TestFindALExtension_SingleDirectory(t *testing.T) {
	// Create temporary home directory
	tmpHome := t.TempDir()

	// Create an AL extension in .vscode/extensions
	expectedPath := createTestExtension(t, tmpHome, ".vscode", "17.0.1234")

	// Find the extension
	result, err := findALExtensionInHome(tmpHome)
	if err != nil {
		t.Fatalf("Expected to find extension, got error: %v", err)
	}

	if result != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, result)
	}
}

func TestFindALExtension_MultipleVersionsSameDirectory(t *testing.T) {
	tmpHome := t.TempDir()

	// Create multiple versions in .vscode/extensions
	createTestExtension(t, tmpHome, ".vscode", "16.0.100")
	createTestExtension(t, tmpHome, ".vscode", "17.0.200")
	expectedPath := createTestExtension(t, tmpHome, ".vscode", "17.1.50")

	result, err := findALExtensionInHome(tmpHome)
	if err != nil {
		t.Fatalf("Expected to find extension, got error: %v", err)
	}

	if result != expectedPath {
		t.Errorf("Expected newest version %s, got %s", expectedPath, result)
	}
}

func TestFindALExtension_NewestAcrossMultipleDirectories(t *testing.T) {
	tmpHome := t.TempDir()

	// Create extensions in different VS Code variant directories
	createTestExtension(t, tmpHome, ".vscode", "16.0.100")           // Stable, older
	createTestExtension(t, tmpHome, ".vscode-insiders", "17.0.200")  // Insiders, newer
	expectedPath := createTestExtension(t, tmpHome, ".cursor", "18.0.50") // Cursor, newest

	result, err := findALExtensionInHome(tmpHome)
	if err != nil {
		t.Fatalf("Expected to find extension, got error: %v", err)
	}

	if result != expectedPath {
		t.Errorf("Expected newest version from Cursor %s, got %s", expectedPath, result)
	}
}

func TestFindALExtension_OnlyInInsiders(t *testing.T) {
	tmpHome := t.TempDir()

	// Only create extension in VS Code Insiders
	expectedPath := createTestExtension(t, tmpHome, ".vscode-insiders", "17.0.1000")

	result, err := findALExtensionInHome(tmpHome)
	if err != nil {
		t.Fatalf("Expected to find extension in Insiders, got error: %v", err)
	}

	if result != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, result)
	}
}

func TestFindALExtension_OnlyInVSCodeServer(t *testing.T) {
	tmpHome := t.TempDir()

	// Only create extension in VS Code Server (Remote SSH, WSL, etc.)
	expectedPath := createTestExtension(t, tmpHome, ".vscode-server", "17.0.500")

	result, err := findALExtensionInHome(tmpHome)
	if err != nil {
		t.Fatalf("Expected to find extension in VS Code Server, got error: %v", err)
	}

	if result != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, result)
	}
}

func TestFindALExtension_OnlyInVSCodium(t *testing.T) {
	tmpHome := t.TempDir()

	// Only create extension in VSCodium
	expectedPath := createTestExtension(t, tmpHome, ".vscode-oss", "17.0.300")

	result, err := findALExtensionInHome(tmpHome)
	if err != nil {
		t.Fatalf("Expected to find extension in VSCodium, got error: %v", err)
	}

	if result != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, result)
	}
}

func TestFindALExtension_OnlyInCursor(t *testing.T) {
	tmpHome := t.TempDir()

	// Only create extension in Cursor
	expectedPath := createTestExtension(t, tmpHome, ".cursor", "17.0.999")

	result, err := findALExtensionInHome(tmpHome)
	if err != nil {
		t.Fatalf("Expected to find extension in Cursor, got error: %v", err)
	}

	if result != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, result)
	}
}

func TestFindALExtension_NotFound_ErrorListsAllPaths(t *testing.T) {
	tmpHome := t.TempDir()

	// Don't create any extensions - should get error listing all searched paths
	_, err := findALExtensionInHome(tmpHome)
	if err == nil {
		t.Fatal("Expected error when no extension found")
	}

	errMsg := err.Error()

	// Verify error message contains all VS Code variant paths
	expectedVariants := []string{
		".vscode",
		".vscode-insiders",
		".vscode-server",
		".vscode-server-insiders",
		".vscode-oss",
		".cursor",
	}

	for _, variant := range expectedVariants {
		if !strings.Contains(errMsg, variant) {
			t.Errorf("Error message should contain %q, got: %s", variant, errMsg)
		}
	}
}

func TestFindALExtension_IgnoresNonMatchingDirectories(t *testing.T) {
	tmpHome := t.TempDir()

	// Create directories that don't match the pattern
	vscodeExts := filepath.Join(tmpHome, ".vscode", "extensions")
	os.MkdirAll(vscodeExts, 0755)

	// Non-matching directories (should be ignored)
	os.MkdirAll(filepath.Join(vscodeExts, "ms-dynamics-smb.al"), 0755)         // Missing version
	os.MkdirAll(filepath.Join(vscodeExts, "ms-dynamics-smb.al-abc"), 0755)     // Non-numeric version
	os.MkdirAll(filepath.Join(vscodeExts, "other-extension-1.0.0"), 0755)      // Different extension
	os.MkdirAll(filepath.Join(vscodeExts, "ms-dynamics-smb.al-17.0"), 0755)    // Incomplete version

	// One valid extension
	expectedPath := createTestExtension(t, tmpHome, ".vscode", "17.0.100")

	result, err := findALExtensionInHome(tmpHome)
	if err != nil {
		t.Fatalf("Expected to find extension, got error: %v", err)
	}

	if result != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, result)
	}
}

func TestFindALExtension_VersionSortingEdgeCases(t *testing.T) {
	testCases := []struct {
		name     string
		versions []string
		expected string
	}{
		{
			name:     "major version wins",
			versions: []string{"16.9.9999", "17.0.0"},
			expected: "17.0.0",
		},
		{
			name:     "minor version wins when major equal",
			versions: []string{"17.0.9999", "17.1.0"},
			expected: "17.1.0",
		},
		{
			name:     "patch version wins when major and minor equal",
			versions: []string{"17.1.100", "17.1.200"},
			expected: "17.1.200",
		},
		{
			name:     "handles large version numbers",
			versions: []string{"17.0.1998613", "17.0.1998612"},
			expected: "17.0.1998613",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpHome := t.TempDir()

			for _, version := range tc.versions {
				createTestExtension(t, tmpHome, ".vscode", version)
			}

			result, err := findALExtensionInHome(tmpHome)
			if err != nil {
				t.Fatalf("Expected to find extension, got error: %v", err)
			}

			expectedPath := filepath.Join(tmpHome, ".vscode", "extensions", "ms-dynamics-smb.al-"+tc.expected)
			if result != expectedPath {
				t.Errorf("Expected %s, got %s", expectedPath, result)
			}
		})
	}
}

func TestVSCodeExtensionDirs_ContainsAllVariants(t *testing.T) {
	// Verify all expected variants are in the list
	expectedDirs := map[string]bool{
		".vscode/extensions":                 false,
		".vscode-insiders/extensions":        false,
		".vscode-server/extensions":          false,
		".vscode-server-insiders/extensions": false,
		".vscode-oss/extensions":             false,
		".cursor/extensions":                 false,
	}

	for _, dir := range vsCodeExtensionDirs {
		if _, exists := expectedDirs[dir]; exists {
			expectedDirs[dir] = true
		}
	}

	for dir, found := range expectedDirs {
		if !found {
			t.Errorf("Expected vsCodeExtensionDirs to contain %q", dir)
		}
	}
}
