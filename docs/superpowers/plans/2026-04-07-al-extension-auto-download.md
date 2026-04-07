# AL Extension Auto-Download Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable the Go wrapper to automatically download and update the Microsoft AL Language extension from the VS Code Marketplace, so it works in environments without VS Code (Sublime Text, standalone).

**Architecture:** New `extension_download.go` handles marketplace API queries, .vsix download, and extraction. The existing path resolution in `paths.go` gains a 4th tier that checks `~/.al-language-server/extensions/{channel}/`. Background update goroutine runs during normal operation. Three new CLI flags control the feature.

**Tech Stack:** Go standard library only (net/http, archive/zip, encoding/json, os). No new dependencies.

---

### Task 1: Marketplace API Client

**Files:**
- Create: `al-language-server-go/wrapper/marketplace.go`
- Create: `al-language-server-go/wrapper/marketplace_test.go`

- [ ] **Step 1: Write test for parsing marketplace API response**

```go
// marketplace_test.go
package wrapper

import (
	"encoding/json"
	"testing"
)

func TestParseMarketplaceResponse_Release(t *testing.T) {
	// Real-shaped response from the marketplace API
	responseJSON := `{
		"results": [{
			"extensions": [{
				"versions": [
					{
						"version": "18.0.2190758",
						"properties": [
							{"key": "Microsoft.VisualStudio.Code.PreRelease", "value": "false"}
						]
					},
					{
						"version": "19.0.100000",
						"properties": [
							{"key": "Microsoft.VisualStudio.Code.PreRelease", "value": "true"}
						]
					}
				]
			}]
		}]
	}`

	var resp marketplaceResponse
	if err := json.Unmarshal([]byte(responseJSON), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	version, err := findLatestVersion(resp, "release")
	if err != nil {
		t.Fatalf("Expected to find release version, got error: %v", err)
	}
	if version != "18.0.2190758" {
		t.Errorf("Expected 18.0.2190758, got %s", version)
	}
}

func TestParseMarketplaceResponse_Prerelease(t *testing.T) {
	responseJSON := `{
		"results": [{
			"extensions": [{
				"versions": [
					{
						"version": "18.0.2190758",
						"properties": [
							{"key": "Microsoft.VisualStudio.Code.PreRelease", "value": "false"}
						]
					},
					{
						"version": "19.0.100000",
						"properties": [
							{"key": "Microsoft.VisualStudio.Code.PreRelease", "value": "true"}
						]
					}
				]
			}]
		}]
	}`

	var resp marketplaceResponse
	json.Unmarshal([]byte(responseJSON), &resp)

	version, err := findLatestVersion(resp, "prerelease")
	if err != nil {
		t.Fatalf("Expected to find prerelease version, got error: %v", err)
	}
	if version != "19.0.100000" {
		t.Errorf("Expected 19.0.100000, got %s", version)
	}
}

func TestParseMarketplaceResponse_NoMatchingChannel(t *testing.T) {
	responseJSON := `{
		"results": [{
			"extensions": [{
				"versions": [
					{
						"version": "18.0.2190758",
						"properties": [
							{"key": "Microsoft.VisualStudio.Code.PreRelease", "value": "false"}
						]
					}
				]
			}]
		}]
	}`

	var resp marketplaceResponse
	json.Unmarshal([]byte(responseJSON), &resp)

	_, err := findLatestVersion(resp, "prerelease")
	if err == nil {
		t.Fatal("Expected error when no prerelease version exists")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd al-language-server-go && go test ./wrapper/ -run TestParseMarketplace -v`
Expected: FAIL - types not defined

- [ ] **Step 3: Implement marketplace types and version finder**

```go
// marketplace.go
package wrapper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	marketplaceAPIURL  = "https://marketplace.visualstudio.com/_apis/public/gallery/extensionquery"
	vspackageURLFormat = "https://marketplace.visualstudio.com/_apis/public/gallery/publishers/ms-dynamics-smb/vsextensions/al/%s/vspackage"
	alExtensionID      = "ms-dynamics-smb.al"
)

// marketplaceResponse is the top-level response from the VS Code Marketplace API
type marketplaceResponse struct {
	Results []marketplaceResult `json:"results"`
}

type marketplaceResult struct {
	Extensions []marketplaceExtension `json:"extensions"`
}

type marketplaceExtension struct {
	Versions []marketplaceVersion `json:"versions"`
}

type marketplaceVersion struct {
	Version    string                    `json:"version"`
	Properties []marketplaceProperty     `json:"properties"`
}

type marketplaceProperty struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// findLatestVersion finds the first version matching the given channel ("release" or "prerelease").
// The marketplace API returns versions in newest-first order, so the first match is the latest.
func findLatestVersion(resp marketplaceResponse, channel string) (string, error) {
	if len(resp.Results) == 0 || len(resp.Results[0].Extensions) == 0 {
		return "", fmt.Errorf("no extensions in marketplace response")
	}

	wantPrerelease := channel == "prerelease"

	for _, v := range resp.Results[0].Extensions[0].Versions {
		isPrerelease := false
		for _, p := range v.Properties {
			if p.Key == "Microsoft.VisualStudio.Code.PreRelease" && p.Value == "true" {
				isPrerelease = true
				break
			}
		}
		if isPrerelease == wantPrerelease {
			return v.Version, nil
		}
	}

	return "", fmt.Errorf("no %s version found for %s", channel, alExtensionID)
}

// queryMarketplace queries the VS Code Marketplace API for extension versions.
// The request includes flags to get version properties (needed to distinguish prerelease).
func queryMarketplace() (marketplaceResponse, error) {
	body := map[string]interface{}{
		"filters": []map[string]interface{}{
			{
				"criteria": []map[string]string{
					{"filterType": "7", "value": alExtensionID},
				},
				"pageSize": 1,
			},
		},
		// Flag 0x1 = IncludeVersions, 0x10 = IncludeVersionProperties
		"flags": 0x11,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return marketplaceResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", marketplaceAPIURL, bytes.NewReader(jsonBody))
	if err != nil {
		return marketplaceResponse{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json;api-version=6.0-preview.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return marketplaceResponse{}, fmt.Errorf("marketplace request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return marketplaceResponse{}, fmt.Errorf("marketplace returned status %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return marketplaceResponse{}, fmt.Errorf("failed to read response: %w", err)
	}

	var result marketplaceResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return marketplaceResponse{}, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// vspackageURL returns the download URL for a specific version
func vspackageURL(version string) string {
	return fmt.Sprintf(vspackageURLFormat, version)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd al-language-server-go && go test ./wrapper/ -run TestParseMarketplace -v`
Expected: All 3 tests PASS

- [ ] **Step 5: Commit**

```bash
git add al-language-server-go/wrapper/marketplace.go al-language-server-go/wrapper/marketplace_test.go
git commit -m "feat: add VS Code Marketplace API client for AL extension"
```

---

### Task 2: Metadata and Storage Management

**Files:**
- Create: `al-language-server-go/wrapper/extension_store.go`
- Create: `al-language-server-go/wrapper/extension_store_test.go`

- [ ] **Step 1: Write tests for metadata read/write and storage paths**

```go
// extension_store_test.go
package wrapper

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtensionStoreDir(t *testing.T) {
	store := NewExtensionStore("/fakehome")
	expected := filepath.Join("/fakehome", ".al-language-server")
	if store.BaseDir() != expected {
		t.Errorf("Expected %s, got %s", expected, store.BaseDir())
	}
}

func TestExtensionStorePaths(t *testing.T) {
	store := NewExtensionStore("/fakehome")

	channelDir := store.ChannelDir("release")
	expected := filepath.Join("/fakehome", ".al-language-server", "extensions", "release")
	if channelDir != expected {
		t.Errorf("Expected %s, got %s", expected, channelDir)
	}

	metadataPath := store.MetadataPath("prerelease")
	expected = filepath.Join("/fakehome", ".al-language-server", "extensions", "prerelease", "metadata.json")
	if metadataPath != expected {
		t.Errorf("Expected %s, got %s", expected, metadataPath)
	}

	cacheDir := store.CacheDir()
	expected = filepath.Join("/fakehome", ".al-language-server", "cache")
	if cacheDir != expected {
		t.Errorf("Expected %s, got %s", expected, cacheDir)
	}
}

func TestMetadataReadWrite(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewExtensionStore(tmpDir)

	now := time.Now().UTC().Truncate(time.Second)
	meta := &ExtensionMetadata{
		Version:       "18.0.2190758",
		Channel:       "release",
		LastCheckTime: now,
		DownloadedAt:  now,
		ExtensionDir:  "ms-dynamics-smb.al-18.0.2190758",
	}

	// Create the channel directory
	os.MkdirAll(store.ChannelDir("release"), 0755)

	if err := store.WriteMetadata("release", meta); err != nil {
		t.Fatalf("Failed to write metadata: %v", err)
	}

	loaded, err := store.ReadMetadata("release")
	if err != nil {
		t.Fatalf("Failed to read metadata: %v", err)
	}

	if loaded.Version != meta.Version {
		t.Errorf("Version: expected %s, got %s", meta.Version, loaded.Version)
	}
	if loaded.Channel != meta.Channel {
		t.Errorf("Channel: expected %s, got %s", meta.Channel, loaded.Channel)
	}
	if loaded.ExtensionDir != meta.ExtensionDir {
		t.Errorf("ExtensionDir: expected %s, got %s", meta.ExtensionDir, loaded.ExtensionDir)
	}
	if !loaded.LastCheckTime.Equal(meta.LastCheckTime) {
		t.Errorf("LastCheckTime: expected %v, got %v", meta.LastCheckTime, loaded.LastCheckTime)
	}
}

func TestMetadataReadNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewExtensionStore(tmpDir)

	_, err := store.ReadMetadata("release")
	if err == nil {
		t.Fatal("Expected error when metadata file doesn't exist")
	}
}

func TestNeedsUpdateCheck(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewExtensionStore(tmpDir)
	os.MkdirAll(store.ChannelDir("release"), 0755)

	// No metadata = needs check
	if !store.NeedsUpdateCheck("release") {
		t.Error("Expected NeedsUpdateCheck=true when no metadata exists")
	}

	// Recent check = no check needed
	meta := &ExtensionMetadata{
		Version:       "18.0.0",
		Channel:       "release",
		LastCheckTime: time.Now().UTC(),
		DownloadedAt:  time.Now().UTC(),
		ExtensionDir:  "ms-dynamics-smb.al-18.0.0",
	}
	store.WriteMetadata("release", meta)

	if store.NeedsUpdateCheck("release") {
		t.Error("Expected NeedsUpdateCheck=false when checked recently")
	}

	// Old check = needs check
	meta.LastCheckTime = time.Now().UTC().Add(-25 * time.Hour)
	store.WriteMetadata("release", meta)

	if !store.NeedsUpdateCheck("release") {
		t.Error("Expected NeedsUpdateCheck=true when last check was >24h ago")
	}
}

func TestExtensionPath(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewExtensionStore(tmpDir)

	channelDir := store.ChannelDir("release")
	extDir := filepath.Join(channelDir, "ms-dynamics-smb.al-18.0.2190758")
	os.MkdirAll(extDir, 0755)

	meta := &ExtensionMetadata{
		Version:       "18.0.2190758",
		Channel:       "release",
		LastCheckTime: time.Now().UTC(),
		DownloadedAt:  time.Now().UTC(),
		ExtensionDir:  "ms-dynamics-smb.al-18.0.2190758",
	}
	store.WriteMetadata("release", meta)

	path, err := store.ExtensionPath("release")
	if err != nil {
		t.Fatalf("Expected path, got error: %v", err)
	}
	if path != extDir {
		t.Errorf("Expected %s, got %s", extDir, path)
	}
}

func TestExtensionPathMissingDir(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewExtensionStore(tmpDir)
	os.MkdirAll(store.ChannelDir("release"), 0755)

	meta := &ExtensionMetadata{
		Version:      "18.0.0",
		Channel:      "release",
		ExtensionDir: "ms-dynamics-smb.al-18.0.0",
	}
	store.WriteMetadata("release", meta)

	// Extension dir doesn't exist on disk
	_, err := store.ExtensionPath("release")
	if err == nil {
		t.Fatal("Expected error when extension directory doesn't exist")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd al-language-server-go && go test ./wrapper/ -run TestExtensionStore -v && go test ./wrapper/ -run TestMetadata -v && go test ./wrapper/ -run TestNeedsUpdate -v && go test ./wrapper/ -run TestExtensionPath -v`
Expected: FAIL - types not defined

- [ ] **Step 3: Implement extension store**

```go
// extension_store.go
package wrapper

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	updateCheckInterval = 24 * time.Hour
)

// ExtensionMetadata tracks the downloaded AL extension state for a channel
type ExtensionMetadata struct {
	Version       string    `json:"version"`
	Channel       string    `json:"channel"`
	LastCheckTime time.Time `json:"lastCheckTime"`
	DownloadedAt  time.Time `json:"downloadedAt"`
	ExtensionDir  string    `json:"extensionDir"`
}

// ExtensionStore manages the downloaded AL extension storage
type ExtensionStore struct {
	homeDir string
}

// NewExtensionStore creates a store rooted at homeDir/.al-language-server
func NewExtensionStore(homeDir string) *ExtensionStore {
	return &ExtensionStore{homeDir: homeDir}
}

// BaseDir returns the root storage directory
func (s *ExtensionStore) BaseDir() string {
	return filepath.Join(s.homeDir, ".al-language-server")
}

// ChannelDir returns the directory for a specific channel
func (s *ExtensionStore) ChannelDir(channel string) string {
	return filepath.Join(s.BaseDir(), "extensions", channel)
}

// MetadataPath returns the path to the metadata file for a channel
func (s *ExtensionStore) MetadataPath(channel string) string {
	return filepath.Join(s.ChannelDir(channel), "metadata.json")
}

// CacheDir returns the temporary download cache directory
func (s *ExtensionStore) CacheDir() string {
	return filepath.Join(s.BaseDir(), "cache")
}

// ReadMetadata reads the metadata file for a channel
func (s *ExtensionStore) ReadMetadata(channel string) (*ExtensionMetadata, error) {
	data, err := os.ReadFile(s.MetadataPath(channel))
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	var meta ExtensionMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	return &meta, nil
}

// WriteMetadata writes the metadata file for a channel
func (s *ExtensionStore) WriteMetadata(channel string, meta *ExtensionMetadata) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	return os.WriteFile(s.MetadataPath(channel), data, 0644)
}

// NeedsUpdateCheck returns true if the last check was more than 24 hours ago (or never)
func (s *ExtensionStore) NeedsUpdateCheck(channel string) bool {
	meta, err := s.ReadMetadata(channel)
	if err != nil {
		return true
	}
	return time.Since(meta.LastCheckTime) > updateCheckInterval
}

// ExtensionPath returns the full path to the downloaded extension directory,
// or an error if no extension is downloaded for this channel
func (s *ExtensionStore) ExtensionPath(channel string) (string, error) {
	meta, err := s.ReadMetadata(channel)
	if err != nil {
		return "", fmt.Errorf("no downloaded extension for channel %s: %w", channel, err)
	}

	path := filepath.Join(s.ChannelDir(channel), meta.ExtensionDir)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("extension directory missing: %s", path)
	}

	return path, nil
}

// CleanCache removes the cache directory
func (s *ExtensionStore) CleanCache() {
	os.RemoveAll(s.CacheDir())
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd al-language-server-go && go test ./wrapper/ -run "TestExtensionStore|TestMetadata|TestNeedsUpdate|TestExtensionPath" -v`
Expected: All tests PASS

- [ ] **Step 5: Commit**

```bash
git add al-language-server-go/wrapper/extension_store.go al-language-server-go/wrapper/extension_store_test.go
git commit -m "feat: add extension store for managing downloaded AL extensions"
```

---

### Task 3: VSIX Download and Extraction

**Files:**
- Modify: `al-language-server-go/wrapper/extension_store.go`
- Create: `al-language-server-go/wrapper/extension_download.go`
- Create: `al-language-server-go/wrapper/extension_download_test.go`

- [ ] **Step 1: Write test for vsix extraction**

A .vsix file is a zip archive. The actual extension contents are inside an `extension/` subdirectory within the zip. We need to extract that subdirectory to the target path.

```go
// extension_download_test.go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd al-language-server-go && go test ./wrapper/ -run TestExtractVsix -v`
Expected: FAIL - `extractVsix` not defined

- [ ] **Step 3: Implement vsix extraction and download**

```go
// extension_download.go
package wrapper

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// extractVsix extracts the extension/ subdirectory from a .vsix file to targetDir.
// Only files under the extension/ prefix are extracted, with that prefix stripped.
func extractVsix(vsixPath, targetDir string) error {
	r, err := zip.OpenReader(vsixPath)
	if err != nil {
		return fmt.Errorf("failed to open vsix: %w", err)
	}
	defer r.Close()

	const prefix = "extension/"

	for _, f := range r.File {
		// Only extract files under extension/
		if !strings.HasPrefix(f.Name, prefix) {
			continue
		}

		// Strip the extension/ prefix
		relPath := strings.TrimPrefix(f.Name, prefix)
		if relPath == "" {
			continue
		}

		targetPath := filepath.Join(targetDir, filepath.FromSlash(relPath))

		if f.FileInfo().IsDir() {
			os.MkdirAll(targetPath, 0755)
			continue
		}

		// Create parent directories
		os.MkdirAll(filepath.Dir(targetPath), 0755)

		outFile, err := os.Create(targetPath)
		if err != nil {
			return fmt.Errorf("failed to create %s: %w", targetPath, err)
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return fmt.Errorf("failed to open %s in vsix: %w", f.Name, err)
		}

		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return fmt.Errorf("failed to extract %s: %w", f.Name, err)
		}
	}

	return nil
}

// downloadFile downloads a URL to a local file path
func downloadFile(url, destPath string) error {
	os.MkdirAll(filepath.Dir(destPath), 0755)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", destPath, err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write download: %w", err)
	}

	return nil
}

// DownloadAndInstall downloads the AL extension for the given channel and installs it.
// Returns the extension directory path.
func (s *ExtensionStore) DownloadAndInstall(channel string, logFn func(string, ...interface{})) (string, error) {
	logFn("Querying VS Code Marketplace for AL extension (%s channel)...", channel)

	resp, err := queryMarketplace()
	if err != nil {
		return "", fmt.Errorf("failed to query marketplace: %w", err)
	}

	version, err := findLatestVersion(resp, channel)
	if err != nil {
		return "", fmt.Errorf("failed to find version: %w", err)
	}

	logFn("Latest %s version: %s", channel, version)

	// Check if we already have this version
	meta, metaErr := s.ReadMetadata(channel)
	if metaErr == nil && meta.Version == version {
		logFn("Already have version %s, updating check time", version)
		meta.LastCheckTime = time.Now().UTC()
		s.WriteMetadata(channel, meta)
		path, err := s.ExtensionPath(channel)
		if err == nil {
			return path, nil
		}
		// Fall through to download if directory is missing
	}

	// Download
	cacheDir := s.CacheDir()
	os.MkdirAll(cacheDir, 0755)
	vsixPath := filepath.Join(cacheDir, "al-extension.vsix")

	url := vspackageURL(version)
	logFn("Downloading AL extension v%s from marketplace...", version)
	if err := downloadFile(url, vsixPath); err != nil {
		return "", fmt.Errorf("failed to download extension: %w", err)
	}

	// Extract
	extDirName := fmt.Sprintf("ms-dynamics-smb.al-%s", version)
	channelDir := s.ChannelDir(channel)
	targetDir := filepath.Join(channelDir, extDirName)

	// Remove existing extension dir if present (from a previous version)
	if meta != nil && meta.ExtensionDir != "" && meta.ExtensionDir != extDirName {
		oldDir := filepath.Join(channelDir, meta.ExtensionDir)
		logFn("Removing old extension: %s", oldDir)
		os.RemoveAll(oldDir)
	}

	os.MkdirAll(channelDir, 0755)
	logFn("Extracting to %s...", targetDir)
	if err := extractVsix(vsixPath, targetDir); err != nil {
		os.RemoveAll(targetDir)
		return "", fmt.Errorf("failed to extract extension: %w", err)
	}

	// Write metadata
	now := time.Now().UTC()
	newMeta := &ExtensionMetadata{
		Version:       version,
		Channel:       channel,
		LastCheckTime: now,
		DownloadedAt:  now,
		ExtensionDir:  extDirName,
	}
	if err := s.WriteMetadata(channel, newMeta); err != nil {
		return "", fmt.Errorf("failed to write metadata: %w", err)
	}

	// Clean cache
	s.CleanCache()

	logFn("AL extension v%s (%s) installed successfully", version, channel)
	return targetDir, nil
}

// CheckAndUpdate checks for updates and downloads if newer version is available.
// Returns the installed version string (or empty if no update was needed/available).
func (s *ExtensionStore) CheckAndUpdate(channel string, logFn func(string, ...interface{})) string {
	logFn("Checking for AL extension updates (%s channel)...", channel)

	resp, err := queryMarketplace()
	if err != nil {
		logFn("Warning: failed to check for updates: %v", err)
		return ""
	}

	version, err := findLatestVersion(resp, channel)
	if err != nil {
		logFn("Warning: failed to find version: %v", err)
		return ""
	}

	meta, err := s.ReadMetadata(channel)
	if err == nil && meta.Version == version {
		logFn("AL extension is up to date (v%s)", version)
		meta.LastCheckTime = time.Now().UTC()
		s.WriteMetadata(channel, meta)
		return ""
	}

	logFn("New version available: %s -> %s", meta.Version, version)
	_, err = s.DownloadAndInstall(channel, logFn)
	if err != nil {
		logFn("Warning: failed to update AL extension: %v", err)
		return ""
	}

	return version
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd al-language-server-go && go test ./wrapper/ -run TestExtractVsix -v`
Expected: All tests PASS

- [ ] **Step 5: Commit**

```bash
git add al-language-server-go/wrapper/extension_download.go al-language-server-go/wrapper/extension_download_test.go
git commit -m "feat: add vsix download and extraction for AL extension"
```

---

### Task 4: Update Path Resolution and Wrapper Startup

**Files:**
- Modify: `al-language-server-go/wrapper/paths.go` — add tier 4
- Modify: `al-language-server-go/wrapper/wrapper.go` — add fields, update `Run()`
- Modify: `al-language-server-go/wrapper/paths_test.go` — test tier 4
- Modify: `al-language-server-go/main.go` — add CLI flags

- [ ] **Step 1: Write test for tier 4 path resolution**

Add to `paths_test.go`:

```go
func TestResolveALExtensionPath_DownloadedExtension(t *testing.T) {
	tmpHome := t.TempDir()

	// Create a downloaded extension in ~/.al-language-server/extensions/release/
	store := NewExtensionStore(tmpHome)
	channelDir := store.ChannelDir("release")
	extDir := filepath.Join(channelDir, "ms-dynamics-smb.al-18.0.2190758")
	os.MkdirAll(extDir, 0755)

	meta := &ExtensionMetadata{
		Version:       "18.0.2190758",
		Channel:       "release",
		ExtensionDir:  "ms-dynamics-smb.al-18.0.2190758",
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
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd al-language-server-go && go test ./wrapper/ -run "TestResolveALExtensionPath_Downloaded|TestResolveALExtensionPath_VSCode|TestResolveALExtensionPath_AutoDownload" -v`
Expected: FAIL - `resolveALExtensionPathWithHome` not defined

- [ ] **Step 3: Update path resolution in paths.go**

Add the new internal function and update the public one. Add to `paths.go`:

```go
// resolveALExtensionPathWithHome is the internal implementation for testing.
// Priority: 1. explicit path, 2. env var, 3. VS Code auto-discovery, 4. downloaded extension
func resolveALExtensionPathWithHome(explicitPath, home string, autoDownload bool, channel string) (string, error) {
	if explicitPath != "" {
		return explicitPath, nil
	}

	if envPath := os.Getenv("AL_EXTENSION_PATH"); envPath != "" {
		return envPath, nil
	}

	// Tier 3: VS Code auto-discovery
	if path, err := findALExtensionInHome(home); err == nil {
		return path, nil
	}

	// Tier 4: Downloaded extension (only if auto-download is enabled)
	if autoDownload {
		store := NewExtensionStore(home)
		if path, err := store.ExtensionPath(channel); err == nil {
			return path, nil
		}
	}

	if autoDownload {
		return "", fmt.Errorf("AL extension not found. Use --force-update-al-extension to download it")
	}
	return "", fmt.Errorf("AL extension not found. Install it in VS Code, set --al-extension-path, or use --auto-download-al-extension to download it automatically")
}
```

Update `ResolveALExtensionPath` to call the new function:

Replace the existing `ResolveALExtensionPath` function with:

```go
// ResolveALExtensionPath resolves the AL extension path using this priority:
// 1. Explicit path from --al-extension-path flag (if non-empty)
// 2. AL_EXTENSION_PATH environment variable (if set)
// 3. Auto-discovery via FindALExtension()
// 4. Downloaded extension (if autoDownload is true)
func ResolveALExtensionPath(explicitPath string, autoDownload bool, channel string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return resolveALExtensionPathWithHome(explicitPath, home, autoDownload, channel)
}
```

- [ ] **Step 4: Update wrapper.go — add new fields and startup logic**

Add fields to the `ALLSPWrapper` struct in `wrapper.go`:

```go
// AutoDownloadALExtension enables automatic download of the AL extension
// from the VS Code Marketplace. Set via --auto-download-al-extension flag.
AutoDownloadALExtension bool

// ALExtensionChannel selects the release channel ("release" or "prerelease").
// Set via --al-extension-channel flag.
ALExtensionChannel string

// ForceUpdateALExtension bypasses the daily update check and downloads
// the latest version immediately. Set via --force-update-al-extension flag.
ForceUpdateALExtension bool
```

Update the path resolution call in `Run()` (around line 111) from:

```go
extensionPath, err := ResolveALExtensionPath(w.ALExtensionPath)
```

to:

```go
channel := w.ALExtensionChannel
if channel == "" {
	channel = "release"
}

extensionPath, err := ResolveALExtensionPath(w.ALExtensionPath, w.AutoDownloadALExtension, channel)
if err != nil && w.AutoDownloadALExtension {
	// First run: blocking download
	w.Log("AL extension not found locally, downloading...")
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		return fmt.Errorf("failed to get home directory: %w", homeErr)
	}
	store := NewExtensionStore(home)
	extensionPath, err = store.DownloadAndInstall(channel, w.Log)
	if err != nil {
		return fmt.Errorf("failed to download AL extension: %w", err)
	}
}
if err != nil {
	w.Log("Failed to find AL extension: %v", err)
	return fmt.Errorf("AL extension not found: %w", err)
}
w.Log("Found AL extension: %s", extensionPath)
```

After the AL LSP process starts and goroutines are launched (after `addProcessToJob`, around line 154), add the background update logic:

```go
// Background update check (non-blocking)
if w.AutoDownloadALExtension {
	go func() {
		home, err := os.UserHomeDir()
		if err != nil {
			w.Log("Warning: cannot check for updates: %v", err)
			return
		}
		store := NewExtensionStore(home)

		if w.ForceUpdateALExtension || store.NeedsUpdateCheck(channel) {
			newVersion := store.CheckAndUpdate(channel, w.Log)
			if newVersion != "" {
				w.Log("AL extension updated to v%s — restart to use new version", newVersion)
			}
		}
	}()
}
```

- [ ] **Step 5: Update main.go — add CLI flags**

```go
autoDownload := flag.Bool("auto-download-al-extension", false, "Automatically download the AL extension from the VS Code Marketplace")
alExtensionChannel := flag.String("al-extension-channel", "release", "AL extension channel: release or prerelease")
forceUpdate := flag.Bool("force-update-al-extension", false, "Force download the latest AL extension version")
```

And wire them up after `flag.Parse()` in the normal wrapper mode section:

```go
w.AutoDownloadALExtension = *autoDownload
w.ALExtensionChannel = *alExtensionChannel
w.ForceUpdateALExtension = *forceUpdate
```

- [ ] **Step 6: Run all tests**

Run: `cd al-language-server-go && go test ./wrapper/ -v`
Expected: All tests PASS (existing + new)

- [ ] **Step 7: Commit**

```bash
git add al-language-server-go/main.go al-language-server-go/wrapper/paths.go al-language-server-go/wrapper/paths_test.go al-language-server-go/wrapper/wrapper.go
git commit -m "feat: integrate auto-download into path resolution and wrapper startup"
```

---

### Task 5: Sublime Text Integration

**Files:**
- Modify: `U:\Git\sublime-lsp-al\plugin.py` — pass new flags
- Modify: `U:\Git\sublime-lsp-al\LSP-AL.sublime-settings` — add channel setting

- [ ] **Step 1: Update LSP-AL.sublime-settings**

Add `al_extension_channel` setting and pass the auto-download flags:

```json
{
    "selector": "source.al",
    "command": [
        "${wrapper_path}",
        "--auto-download-al-extension",
        "--al-extension-channel",
        "${al_extension_channel}"
    ],
    "initializationOptions": {},
    "settings": {},
    "al_extension_channel": "release"
}
```

- [ ] **Step 2: Update plugin.py — add variables and commands**

Add `al_extension_channel` to `additional_variables()`:

```python
@classmethod
def additional_variables(cls) -> dict[str, str] | None:
    settings = sublime.load_settings("LSP-AL.sublime-settings")
    channel = settings.get("al_extension_channel", "release")
    return {
        "wrapper_path": cls.wrapper_path(),
        "al_extension_channel": channel,
    }
```

Add command palette commands. Add these classes after the `LSPAL` class:

```python
class LspAlSwitchChannelCommand(sublime_plugin.WindowCommand):
    """Switch between release and prerelease AL extension channels."""

    def run(self) -> None:
        settings = sublime.load_settings("LSP-AL.sublime-settings")
        current = settings.get("al_extension_channel", "release")
        new_channel = "prerelease" if current == "release" else "release"
        settings.set("al_extension_channel", new_channel)
        sublime.save_settings("LSP-AL.sublime-settings")
        sublime.status_message("LSP-AL: Switched to {} channel — restart language server to apply".format(new_channel))


class LspAlForceUpdateCommand(sublime_plugin.WindowCommand):
    """Force update the AL extension by restarting the server with --force-update."""

    def run(self) -> None:
        sublime.status_message("LSP-AL: Restarting server with force update...")
        # The LSP plugin handles restart; we modify the command temporarily
        # A cleaner approach: store a flag and read it in additional_variables
        settings = sublime.load_settings("LSP-AL.sublime-settings")
        settings.set("_force_update", True)
        sublime.save_settings("LSP-AL.sublime-settings")
        self.window.run_command("lsp_restart_server", {"config_name": SESSION_NAME})
```

Update `additional_variables` to handle force update:

```python
@classmethod
def additional_variables(cls) -> dict[str, str] | None:
    settings = sublime.load_settings("LSP-AL.sublime-settings")
    channel = settings.get("al_extension_channel", "release")
    variables = {
        "wrapper_path": cls.wrapper_path(),
        "al_extension_channel": channel,
    }

    # Check for one-time force update flag
    if settings.get("_force_update", False):
        settings.erase("_force_update")
        sublime.save_settings("LSP-AL.sublime-settings")
        variables["force_update"] = "--force-update-al-extension"
    else:
        variables["force_update"] = ""

    return variables
```

And update the settings command to include the optional force update flag:

```json
{
    "command": [
        "${wrapper_path}",
        "--auto-download-al-extension",
        "--al-extension-channel",
        "${al_extension_channel}",
        "${force_update}"
    ]
}
```

Note: empty string args may need filtering. If Sublime's LSP plugin passes empty strings as args, we should handle that in the wrapper (ignore empty args) or filter in `additional_variables`. Test this during implementation.

Also add the `import sublime_plugin` to the imports at the top of `plugin.py`.

- [ ] **Step 3: Create command palette entries**

Create `U:\Git\sublime-lsp-al\Default.sublime-commands`:

```json
[
    {
        "caption": "LSP-AL: Switch Release Channel",
        "command": "lsp_al_switch_channel"
    },
    {
        "caption": "LSP-AL: Force Update AL Extension",
        "command": "lsp_al_force_update"
    }
]
```

- [ ] **Step 4: Test manually**

1. Build the Go wrapper: `cd al-language-server-go && go build -trimpath -ldflags="-s -w" -o ../al-language-server-go-windows/bin/al-lsp-wrapper.exe .`
2. Copy the binary to the Sublime package server dir
3. Open Sublime Text, open an AL project
4. Verify the wrapper starts and downloads the extension on first run
5. Verify subsequent starts skip the download
6. Test switching channels via command palette
7. Test force update via command palette

- [ ] **Step 5: Commit**

```bash
cd U:/Git/sublime-lsp-al
git add plugin.py LSP-AL.sublime-settings Default.sublime-commands
git commit -m "feat: add auto-download flags and channel switching commands"
```

---

### Task 6: Build and Integration Test

**Files:**
- No new files — rebuild and verify

- [ ] **Step 1: Build Go wrapper for both platforms**

```bash
cd U:/Git/claude-code-lsps/al-language-server-go

# Windows
go build -trimpath -ldflags="-s -w" -o ../al-language-server-go-windows/bin/al-lsp-wrapper.exe .

# Linux
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o ../al-language-server-go-linux/bin/al-lsp-wrapper .
```

- [ ] **Step 2: Run all Go tests**

Run: `cd U:/Git/claude-code-lsps/al-language-server-go && go test ./wrapper/ -v`
Expected: All tests PASS

- [ ] **Step 3: Test the wrapper manually with auto-download**

```bash
# Delete any existing downloaded extension (clean slate)
rm -rf ~/.al-language-server

# Run with auto-download (first run, should block and download)
./al-language-server-go-windows/bin/al-lsp-wrapper.exe --auto-download-al-extension --al-extension-channel release
```

Verify in wrapper logs that:
- Marketplace was queried
- Extension was downloaded and extracted
- Wrapper started with the downloaded extension

- [ ] **Step 4: Test daily check skip**

Run the wrapper again immediately. Verify in logs that:
- No marketplace query was made (lastCheckTime is recent)
- Wrapper started using existing extension

- [ ] **Step 5: Test force update**

```bash
./al-language-server-go-windows/bin/al-lsp-wrapper.exe --auto-download-al-extension --force-update-al-extension
```

Verify in logs that marketplace was queried despite recent check.

- [ ] **Step 6: Commit updated binaries**

```bash
cd U:/Git/claude-code-lsps
git add al-language-server-go-windows/bin/al-lsp-wrapper.exe al-language-server-go-linux/bin/al-lsp-wrapper
git commit -m "build: update wrapper binaries with auto-download support"
```
