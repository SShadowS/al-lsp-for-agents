package wrapper

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeApp(t *testing.T, dir, id, name, version string, deps ...[2]string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var ds []string
	for _, d := range deps {
		ds = append(ds, fmt.Sprintf(`{"id":%q,"name":"x","publisher":"P","version":%q}`, d[0], d[1]))
	}
	body := fmt.Sprintf(`{"id":%q,"name":%q,"publisher":"P","version":%q,"dependencies":[%s]}`,
		id, name, version, strings.Join(ds, ","))
	if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return NormalizePath(dir)
}

func quietLog(string, ...any) {}

// nested fixture: A -> B -> C, and A -> C directly.
func nestedFixture(t *testing.T) (root, a, b, c string) {
	root = t.TempDir()
	c = writeApp(t, filepath.Join(root, "RepoC", "App"), "c", "C", "30.0.0.0")
	b = writeApp(t, filepath.Join(root, "RepoB", "Cloud"), "b", "B", "30.0.0.0", [2]string{"c", "30.0.0.0"})
	a = writeApp(t, filepath.Join(root, "RepoA", "Cloud"), "a", "A", "30.0.0.0",
		[2]string{"b", "30.0.0.0"}, [2]string{"c", "29.0.0.0"}, [2]string{"missing", "1.0.0.0"})
	return
}

func TestSourceIndex_ClosureAndReferences(t *testing.T) {
	root, a, b, c := nestedFixture(t)
	ix := BuildSourceIndex([]string{root}, quietLog)
	m := ParseAppManifest(filepath.Join(a, "app.json"))

	s := NewWorkspaceSettings(a, m)
	ix.ApplyToActive(s, m)

	if want := []string{a, b, c}; !reflect.DeepEqual(s.ActiveWorkspaceClosure, want) {
		t.Errorf("closure = %v, want %v", s.ActiveWorkspaceClosure, want)
	}
	var ids []string
	for _, r := range s.ExpectedProjectReferenceDefinitions {
		ids = append(ids, r.AppID)
	}
	if want := []string{"b", "c"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("refs = %v, want %v (missing dep must not be listed)", ids, want)
	}
}

// TestSourceIndex_PendingReferenceSettingsPerParent guards the trap found in
// the spike: every (reference, parent) pair needs its own config, or the AL LS
// silently stops publishing diagnostics for nested closures.
func TestSourceIndex_PendingReferenceSettingsPerParent(t *testing.T) {
	root, a, b, c := nestedFixture(t)
	ix := BuildSourceIndex([]string{root}, quietLog)
	m := ParseAppManifest(filepath.Join(a, "app.json"))

	got := ix.PendingReferenceSettings(a, m)
	var pairs []string
	for _, s := range got {
		if s.SetActiveWorkspace {
			t.Errorf("reference config for %s has setActiveWorkspace=true", s.WorkspacePath)
		}
		if len(s.ActiveWorkspaceClosure) != 0 {
			t.Errorf("reference config for %s has a closure: %v", s.WorkspacePath, s.ActiveWorkspaceClosure)
		}
		pairs = append(pairs, filepath.Base(filepath.Dir(s.WorkspacePath))+"<-"+filepath.Base(filepath.Dir(*s.DependencyParentWorkspacePath)))
	}
	if want := []string{"RepoB<-RepoA", "RepoC<-RepoB", "RepoC<-RepoA"}; !reflect.DeepEqual(pairs, want) {
		t.Errorf("pairs = %v, want %v", pairs, want)
	}
	// B's own reference to C must be declared in B's config.
	if refs := got[0].ExpectedProjectReferenceDefinitions; len(refs) != 1 || refs[0].AppID != "c" {
		t.Errorf("B config refs = %v, want [c]", refs)
	}
	if again := ix.PendingReferenceSettings(a, m); len(again) != 0 {
		t.Errorf("second call returned %d configs, want 0", len(again))
	}
	_ = b
	_ = c
}

func TestSourceIndex_VersionRulesAndDuplicates(t *testing.T) {
	root := t.TempDir()
	// Too old for the dependency: must not match.
	writeApp(t, filepath.Join(root, "Old", "App"), "old", "Old", "26.0.0.0")
	// Placeholder version: skipped entirely.
	writeApp(t, filepath.Join(root, "BC", "Base"), "base", "Base", "$(app_currentVersion)")
	// Duplicate id: highest version wins, then shortest path.
	writeApp(t, filepath.Join(root, "ConnectorApp", "App"), "conn", "Conn", "30.0.0.0")
	writeApp(t, filepath.Join(root, "ContiniaBase", "Apps", "Connector", "App"), "conn", "Conn", "30.0.0.0")
	writeApp(t, filepath.Join(root, "Legacy", "Conn"), "conn", "Conn", "29.0.0.0")
	a := writeApp(t, filepath.Join(root, "Main", "Cloud"), "a", "A", "30.0.0.0",
		[2]string{"old", "30.0.0.0"}, [2]string{"base", "28.0.0.0"}, [2]string{"conn", "30.0.0.0"})

	ix := BuildSourceIndex([]string{root}, quietLog)
	refs := ix.references(a, ParseAppManifest(filepath.Join(a, "app.json")))
	if len(refs) != 1 {
		t.Fatalf("refs = %d, want 1 (only conn)", len(refs))
	}
	if want := NormalizePath(filepath.Join(root, "ConnectorApp", "App")); refs[0].root != want {
		t.Errorf("duplicate resolved to %s, want %s", refs[0].root, want)
	}
}

func TestSourceIndex_SkipsAlpackagesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	writeApp(t, filepath.Join(root, "Repo", ".alpackages", "x"), "pkg", "Pkg", "30.0.0.0")
	outside := t.TempDir()
	writeApp(t, filepath.Join(outside, "BC", "App"), "linked", "Linked", "30.0.0.0")
	if err := os.Symlink(filepath.Join(outside, "BC"), filepath.Join(root, "BC")); err != nil {
		t.Logf("symlink not available (%v); checking .alpackages only", err)
	}
	ix := BuildSourceIndex([]string{root}, quietLog)
	if len(ix.byRoot) != 0 {
		t.Errorf("expected nothing indexed, got %v", ix.byRoot)
	}
}

// TestSourceIndex_NoReferencesKeepsSettings: a project with nothing to
// resolve from source gets exactly the settings it gets without the feature.
func TestSourceIndex_NoReferencesKeepsSettings(t *testing.T) {
	root := t.TempDir()
	a := writeApp(t, filepath.Join(root, "Solo"), "a", "A", "1.0.0.0", [2]string{"elsewhere", "1.0.0.0"})
	m := ParseAppManifest(filepath.Join(a, "app.json"))
	want, _ := json.Marshal(NewWorkspaceSettings(a, m))

	s := NewWorkspaceSettings(a, m)
	BuildSourceIndex([]string{root}, quietLog).ApplyToActive(s, m)
	got, _ := json.Marshal(s)
	if string(got) != string(want) {
		t.Errorf("settings changed:\n got %s\nwant %s", got, want)
	}
}

func TestParseAppManifest_StripsBOM(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.json")
	if err := os.WriteFile(p, append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"id":"x","version":"1.0.0.0"}`)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if m := ParseAppManifest(p); m == nil || m.ID != "x" {
		t.Errorf("BOM app.json not parsed: %+v", m)
	}
}
