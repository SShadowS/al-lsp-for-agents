package wrapper

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// SourceRootsEnvVar names folders (os.PathListSeparator separated) that are
// scanned for AL projects. Dependencies that one of those projects satisfies
// are resolved from source, the way VS Code resolves project references in
// a multi-root workspace. Unset means no scanning and no change in behavior.
const SourceRootsEnvVar = "AL_LSP_SOURCE_ROOTS"

// maxSourceScanDepth bounds the directory walk below each source root.
const maxSourceScanDepth = 6

type sourceProject struct {
	root     string // NormalizePath'd project folder
	manifest *AppManifest
}

// SourceIndex maps app ids to AL projects found under the source roots. It is
// built once at startup and read-only afterwards.
type SourceIndex struct {
	byID   map[string][]*sourceProject
	byRoot map[string]*sourceProject

	// (reference, parent) pairs already configured, like the AL extension's
	// loadedProjectsThatAreProjectReferences.
	sentMu sync.Mutex
	sent   map[[2]string]bool
}

// skipScanDirs are never AL project folders worth indexing.
var skipScanDirs = map[string]bool{
	".alpackages": true, ".git": true, "node_modules": true, ".vscode": true,
	".snapshots": true, ".netpackages": true,
}

// BuildSourceIndex scans roots for app.json files. Symlinked directories are
// not followed. Projects whose version doesn't parse (e.g. "$(app_currentVersion)"
// placeholders) are skipped, because the AL LS can't match them either.
func BuildSourceIndex(roots []string, logf func(string, ...any)) *SourceIndex {
	ix := &SourceIndex{
		byID:   map[string][]*sourceProject{},
		byRoot: map[string]*sourceProject{},
		sent:   map[[2]string]bool{},
	}
	skipped := 0
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		root = NormalizePath(root)
		base := strings.Count(root, string(filepath.Separator))
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != root && (skipScanDirs[strings.ToLower(d.Name())] ||
					strings.Count(path, string(filepath.Separator))-base > maxSourceScanDepth) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.EqualFold(d.Name(), "app.json") {
				return nil
			}
			dir := NormalizePath(filepath.Dir(path))
			if _, dup := ix.byRoot[strings.ToLower(dir)]; dup {
				return nil
			}
			m := ParseAppManifest(path)
			if m == nil || m.ID == "" {
				return nil
			}
			if !validALVersion(m.Version) {
				skipped++ // e.g. BC source with "$(app_currentVersion)" placeholders
				return nil
			}
			p := &sourceProject{root: dir, manifest: m}
			ix.byRoot[strings.ToLower(dir)] = p
			id := strings.ToLower(m.ID)
			ix.byID[id] = append(ix.byID[id], p)
			return nil
		})
	}
	for id, ps := range ix.byID {
		if len(ps) > 1 {
			sortCandidates(ps)
			names := make([]string, len(ps))
			for i, p := range ps {
				names[i] = p.root + "@" + p.manifest.Version
			}
			logf("source index: app id %s found %d times, preferring %s: %v", id, len(ps), ps[0].root, names)
		}
	}
	logf("source index: %d AL projects under %v (%d skipped: version not numeric)", len(ix.byRoot), roots, skipped)
	return ix
}

// sortCandidates orders projects sharing an app id: highest version first,
// then shortest path, then lexical path.
func sortCandidates(ps []*sourceProject) {
	sort.SliceStable(ps, func(i, j int) bool {
		if c := compareALVersions(ps[i].manifest.Version, ps[j].manifest.Version); c != 0 {
			return c > 0
		}
		if len(ps[i].root) != len(ps[j].root) {
			return len(ps[i].root) < len(ps[j].root)
		}
		return ps[i].root < ps[j].root
	})
}

// validALVersion reports whether v is a numeric AL version (1-4 parts).
func validALVersion(v string) bool {
	parts := strings.Split(strings.TrimSpace(v), ".")
	if len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.ParseUint(p, 10, 32); err != nil {
			return false
		}
	}
	return true
}

// references mirrors the AL extension's getProjectReferences/hasMatch: a
// dependency is a project reference when an indexed project has the same app
// id and a version >= the required one.
func (ix *SourceIndex) references(root string, m *AppManifest) []*sourceProject {
	if m == nil {
		return nil
	}
	self := strings.ToLower(NormalizePath(root))
	var out []*sourceProject
	for _, dep := range m.Dependencies {
		for _, c := range ix.byID[strings.ToLower(dep.GetAppID())] {
			if strings.ToLower(c.root) == self {
				continue
			}
			if !validALVersion(dep.Version) || compareALVersions(c.manifest.Version, dep.Version) >= 0 {
				out = append(out, c)
				break // candidates are sorted, first match is the preferred one
			}
		}
	}
	return out
}

// closure mirrors getClosure: the active project plus every project reachable
// through project references, active first.
func (ix *SourceIndex) closure(root string, m *AppManifest) []string {
	root = NormalizePath(root)
	seen := map[string]bool{}
	var order []string
	var walk func(r string, man *AppManifest)
	walk = func(r string, man *AppManifest) {
		k := strings.ToLower(r)
		if seen[k] {
			return
		}
		seen[k] = true
		order = append(order, r)
		for _, ref := range ix.references(r, man) {
			walk(ref.root, ref.manifest)
		}
	}
	walk(root, m)
	return order
}

func referenceDefinitions(refs []*sourceProject) []ProjectReferenceDefinition {
	out := make([]ProjectReferenceDefinition, 0, len(refs))
	for _, r := range refs {
		out = append(out, ProjectReferenceDefinition{
			AppID:     r.manifest.ID,
			Name:      r.manifest.Name,
			Publisher: r.manifest.Publisher,
			Version:   r.manifest.Version,
		})
	}
	return out
}

// ApplyToActive fills the closure and project references into the settings
// for an active project. A project without source references keeps the
// settings NewWorkspaceSettings produced.
func (ix *SourceIndex) ApplyToActive(s *WorkspaceSettings, m *AppManifest) {
	refs := ix.references(s.WorkspacePath, m)
	if len(refs) == 0 {
		return
	}
	s.ExpectedProjectReferenceDefinitions = referenceDefinitions(refs)
	s.ActiveWorkspaceClosure = ix.closure(s.WorkspacePath, m)
}

// PendingReferenceSettings returns the workspace/didChangeConfiguration
// settings the AL extension sends after al/activeProjectLoaded
// (loadProjectReferences): one per (reference, parent) pair, walking the
// references depth-first from the active project. Pairs returned earlier are
// not returned again.
//
// Sending one config per parent matters: with nested references (A->B->C)
// and C configured only for A, the AL LS still answers navigation but never
// publishes diagnostics.
func (ix *SourceIndex) PendingReferenceSettings(active string, m *AppManifest) []*WorkspaceSettings {
	ix.sentMu.Lock()
	defer ix.sentMu.Unlock()
	var out []*WorkspaceSettings
	visited := map[string]bool{}
	var walk func(parent string, man *AppManifest)
	walk = func(parent string, man *AppManifest) {
		if visited[strings.ToLower(parent)] {
			return
		}
		visited[strings.ToLower(parent)] = true
		for _, ref := range ix.references(parent, man) {
			key := [2]string{strings.ToLower(ref.root), strings.ToLower(parent)}
			if !ix.sent[key] {
				ix.sent[key] = true
				s := NewWorkspaceSettings(ref.root, ref.manifest)
				p := parent
				s.SetActiveWorkspace = false
				s.DependencyParentWorkspacePath = &p
				s.ExpectedProjectReferenceDefinitions = referenceDefinitions(ix.references(ref.root, ref.manifest))
				s.ActiveWorkspaceClosure = []string{}
				out = append(out, s)
			}
			walk(ref.root, ref.manifest)
		}
	}
	walk(NormalizePath(active), m)
	return out
}

// sourceRootsFromEnv returns the configured source roots, or nil when unset.
func sourceRootsFromEnv() []string {
	v := strings.TrimSpace(os.Getenv(SourceRootsEnvVar))
	if v == "" {
		return nil
	}
	return filepath.SplitList(v)
}

// workspaceSettings returns the settings for a project, with source project
// references applied when AL_LSP_SOURCE_ROOTS is set.
func (w *ALLSPWrapper) workspaceSettings(root string, m *AppManifest) *WorkspaceSettings {
	s := NewWorkspaceSettings(root, m)
	if w.sourceIndex != nil {
		w.sourceIndex.ApplyToActive(s, m)
	}
	return s
}

// sendReferenceConfigs handles al/activeProjectLoaded params
// ({"activeProjectFolder": "<uri>"}).
func (w *ALLSPWrapper) sendReferenceConfigs(raw json.RawMessage) {
	var p struct {
		ActiveProjectFolder string `json:"activeProjectFolder"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.ActiveProjectFolder == "" {
		w.Log("al/activeProjectLoaded: no activeProjectFolder in %s", string(raw))
		return
	}
	folder, err := FileURIToPath(p.ActiveProjectFolder)
	if err != nil {
		w.Log("al/activeProjectLoaded: bad folder URI %q: %v", p.ActiveProjectFolder, err)
		return
	}
	folder = NormalizePath(folder)
	m := ParseAppManifest(filepath.Join(folder, "app.json"))
	for _, s := range w.sourceIndex.PendingReferenceSettings(folder, m) {
		w.Log("source refs: configuring %s (parent %s)", s.WorkspacePath, *s.DependencyParentWorkspacePath)
		if err := w.SendNotificationToLSP("workspace/didChangeConfiguration", DidChangeConfigurationParams{Settings: s}); err != nil {
			w.Log("source refs: didChangeConfiguration for %s failed: %v", s.WorkspacePath, err)
		}
	}
}
