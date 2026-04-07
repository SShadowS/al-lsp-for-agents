package wrapper

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

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
	vsixPath := filepath.Join(cacheDir, fmt.Sprintf("al-extension-%s.vsix", channel))

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

	if meta != nil {
		logFn("New version available: %s -> %s", meta.Version, version)
	} else {
		logFn("New version available: %s", version)
	}
	_, err = s.DownloadAndInstall(channel, logFn)
	if err != nil {
		logFn("Warning: failed to update AL extension: %v", err)
		return ""
	}

	return version
}

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
	if err := os.MkdirAll(s.ChannelDir(channel), 0755); err != nil {
		return fmt.Errorf("failed to create channel directory: %w", err)
	}

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
