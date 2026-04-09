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

func TestResolveALExtensionPath_DownloadedExtension(t *testing.T) {
	tmpHome := t.TempDir()

	// Create a downloaded extension in ~/.al-language-server/extensions/release/
	store := NewExtensionStore(tmpHome)
	channelDir := store.ChannelDir("release")
	extDir := filepath.Join(channelDir, "ms-dynamics-smb.al-18.0.2190758")
	os.MkdirAll(extDir, 0755)

	meta := &ExtensionMetadata{
		Version:      "18.0.2190758",
		Channel:      "release",
		ExtensionDir: "ms-dynamics-smb.al-18.0.2190758",
	}
	store.WriteMetadata("release", meta)

	// No VS Code dirs exist, no explicit path, no env var
	// Tier 4 should find the downloaded extension
	path, err := resolveALExtensionPathWithHome("", tmpHome, true, "release")
	if err != nil {
		t.Fatalf("Expected to find downloaded extension, got error: %v", err)
	}

	if path != extDir {
		t.Errorf("Expected %s, got %s", extDir, path)
	}
}

func TestResolveALExtensionPath_VSCodeWinsOverDownloaded(t *testing.T) {
	tmpHome := t.TempDir()

	// Create a VS Code extension (tier 3)
	vscodeExt := createTestExtension(t, tmpHome, ".vscode", "17.0.100")

	// Create a downloaded extension (tier 4)
	store := NewExtensionStore(tmpHome)
	channelDir := store.ChannelDir("release")
	extDir := filepath.Join(channelDir, "ms-dynamics-smb.al-18.0.2190758")
	os.MkdirAll(extDir, 0755)
	meta := &ExtensionMetadata{
		Version:      "18.0.2190758",
		Channel:      "release",
		ExtensionDir: "ms-dynamics-smb.al-18.0.2190758",
	}
	store.WriteMetadata("release", meta)

	// Tier 3 (VS Code) should win
	path, err := resolveALExtensionPathWithHome("", tmpHome, true, "release")
	if err != nil {
		t.Fatalf("Expected to find extension, got error: %v", err)
	}

	if path != vscodeExt {
		t.Errorf("Expected VS Code path %s, got %s", vscodeExt, path)
	}
	_ = extDir // suppress unused
}

func TestResolveALExtensionPath_AutoDownloadDisabled(t *testing.T) {
	tmpHome := t.TempDir()

	// Create a downloaded extension (tier 4)
	store := NewExtensionStore(tmpHome)
	channelDir := store.ChannelDir("release")
	extDir := filepath.Join(channelDir, "ms-dynamics-smb.al-18.0.2190758")
	os.MkdirAll(extDir, 0755)
	meta := &ExtensionMetadata{
		Version:      "18.0.2190758",
		Channel:      "release",
		ExtensionDir: "ms-dynamics-smb.al-18.0.2190758",
	}
	store.WriteMetadata("release", meta)

	// With auto-download disabled, tier 4 should NOT be checked
	_, err := resolveALExtensionPathWithHome("", tmpHome, false, "release")
	if err == nil {
		t.Fatal("Expected error when auto-download is disabled and no VS Code extension exists")
	}
	_ = extDir // suppress unused
}

func TestIsVirtualURI(t *testing.T) {
	tests := []struct {
		uri      string
		expected bool
	}{
		{"al-preview:/allang/SomeApp/Codeunit/123/MyCodeunit.dal", true},
		{"al-preview:/allang/Continia LLM Azure OpenAI/Codeunit/7761/AOAI Chat Completion Params.dal", true},
		{"file:///c:/projects/myapp/src/MyCodeunit.al", false},
		{"file:///home/user/projects/myapp/src/MyCodeunit.al", false},
		{"C:\\projects\\myapp\\src\\MyCodeunit.al", false},
		{"/home/user/projects/myapp/src/MyCodeunit.al", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			result := IsVirtualURI(tt.uri)
			if result != tt.expected {
				t.Errorf("IsVirtualURI(%q) = %v, want %v", tt.uri, result, tt.expected)
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
