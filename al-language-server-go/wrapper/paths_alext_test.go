package wrapper

import (
	"os"
	"testing"
)

func TestResolveALExtensionPath_ExplicitFlag(t *testing.T) {
	path, err := ResolveALExtensionPath("/explicit/path/to/al-extension")
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

	path, err := ResolveALExtensionPath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/env/path/to/al-extension" {
		t.Errorf("expected /env/path/to/al-extension, got %s", path)
	}
}

func TestResolveALExtensionPath_EmptyFallsBackToDiscovery(t *testing.T) {
	os.Unsetenv("AL_EXTENSION_PATH")
	_, err := ResolveALExtensionPath("")
	if err == nil {
		t.Log("AL extension found via auto-discovery (test machine has it installed)")
	}
}
