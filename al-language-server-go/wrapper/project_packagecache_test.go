package wrapper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDiscoverPackageCachePaths_OwnAlpackagesAlwaysFirst verifies that the
// project's own `.alpackages` is always returned as the first entry, as a
// relative path matching the VS Code AL extension's behavior.
func TestDiscoverPackageCachePaths_OwnAlpackagesAlwaysFirst(t *testing.T) {
	root := t.TempDir()
	paths := DiscoverPackageCachePaths(root)
	if len(paths) == 0 || paths[0] != "./.alpackages" {
		t.Errorf("expected first entry to be \"./.alpackages\", got %v", paths)
	}
}

// TestDiscoverPackageCachePaths_FindsAncestorAlpackages verifies the
// monorepo case: when a project lives at a/b/Cloud/ and a/b/.alpackages
// exists, the ancestor folder is added to the returned list.
//
// This is the fix for the AL LSP NullReferenceException-on-symbolSearch
// bug — Microsoft's LSP crashes when transitive deps aren't reachable
// from packageCachePaths.
func TestDiscoverPackageCachePaths_FindsAncestorAlpackages(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "DocumentOutput")
	cloud := filepath.Join(parent, "Cloud")
	parentPkg := filepath.Join(parent, ".alpackages")
	cloudPkg := filepath.Join(cloud, ".alpackages")
	for _, d := range []string{parentPkg, cloudPkg} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	paths := DiscoverPackageCachePaths(cloud)
	if len(paths) < 2 {
		t.Fatalf("expected at least 2 paths, got %v", paths)
	}
	// Second entry should point at the parent .alpackages.
	if !strings.EqualFold(filepath.Clean(paths[1]), filepath.Clean(parentPkg)) {
		t.Errorf("expected ancestor %s in result, got %v", parentPkg, paths)
	}
}

// TestDiscoverPackageCachePaths_StopsAtGitBoundary verifies that the walk
// stops once it crosses a `.git` directory — otherwise we'd pollute the
// path list with unrelated user directories.
func TestDiscoverPackageCachePaths_StopsAtGitBoundary(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	cloud := filepath.Join(repo, "Cloud")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cloud, ".alpackages"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a `.alpackages` ABOVE the git boundary that should NOT be picked up.
	above := filepath.Join(root, ".alpackages")
	if err := os.MkdirAll(above, 0o755); err != nil {
		t.Fatal(err)
	}

	paths := DiscoverPackageCachePaths(cloud)
	for _, p := range paths {
		if strings.EqualFold(filepath.Clean(p), filepath.Clean(above)) {
			t.Errorf("walked past git boundary; included %s in %v", above, paths)
		}
	}
}

// TestDiscoverPackageCachePaths_NoAncestorAlpackages verifies that a
// project without any monorepo-level cache just returns the project's
// own `.alpackages` — no panics, no duplicates, no error.
func TestDiscoverPackageCachePaths_NoAncestorAlpackages(t *testing.T) {
	root := t.TempDir()
	cloud := filepath.Join(root, "isolated")
	if err := os.MkdirAll(cloud, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := DiscoverPackageCachePaths(cloud)
	if len(paths) != 1 || paths[0] != "./.alpackages" {
		t.Errorf("expected single entry, got %v", paths)
	}
}

// TestDiscoverPackageCachePaths_DedupesSameDirectory makes sure we never
// list the same folder twice — guards against symlink loops and quirky
// path resolution.
func TestDiscoverPackageCachePaths_DedupesSameDirectory(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	cloud := filepath.Join(parent, "child")
	if err := os.MkdirAll(filepath.Join(parent, ".alpackages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cloud, ".alpackages"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := DiscoverPackageCachePaths(cloud)
	seen := map[string]bool{}
	for _, p := range paths {
		k := strings.ToLower(filepath.Clean(p))
		if seen[k] {
			t.Errorf("duplicate path in result: %s", p)
		}
		seen[k] = true
	}
}
