package wrapper

import (
	"os"
	"testing"
)

func TestResolveALExtensionPath_ExplicitFlag(t *testing.T) {
	path, err := ResolveALExtensionPath("/explicit/path/to/al-extension", false, "release")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/explicit/path/to/al-extension" {
		t.Errorf("expected /explicit/path/to/al-extension, got %s", path)
	}
}

func TestResolveALExtensionPath_EnvVar(t *testing.T) {
	os.Setenv("AL_EXTENSION_PATH", "/env/path/to/al-extension")
	defer os.Unsetenv("AL_EXTENSION_PATH")

	path, err := ResolveALExtensionPath("", false, "release")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/env/path/to/al-extension" {
		t.Errorf("expected /env/path/to/al-extension, got %s", path)
	}
}

func TestResolveALExtensionPath_EmptyFallsBackToDiscovery(t *testing.T) {
	os.Unsetenv("AL_EXTENSION_PATH")
	_, err := ResolveALExtensionPath("", false, "release")
	if err == nil {
		t.Log("AL extension found via auto-discovery (test machine has it installed)")
	}
}

func TestResolveALExtensionPath_AltExtDirEnvWins(t *testing.T) {
	// Ephemeral dir that LOOKS like an AL extension layout enough to
	// be returned. Just create the directory; resolver should pick it
	// because the env var was set, regardless of contents.
	dir := t.TempDir()

	t.Setenv("AL_LSP_ALT_EXT_DIR", dir)

	got, err := ResolveALExtensionPath("", false, "release")
	if err != nil {
		t.Fatalf("ResolveALExtensionPath: %v", err)
	}
	if got != dir {
		t.Errorf("got %q, want %q (from AL_LSP_ALT_EXT_DIR)", got, dir)
	}
}
