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
