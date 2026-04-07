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
