package wrapper

import (
	"archive/zip"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// previewCache materializes .al source files from dependency .app archives
// so that Claude Code's LSP tool surface — which does a filesystem
// existence check on `filePath` before forwarding the request — can
// successfully invoke documentSymbol/hover/etc. on dependency objects.
//
// Without this cache, Microsoft AL Language Server returns al-preview:/
// URIs from goToDefinition, and Claude Code rejects any subsequent
// LSP call against those URIs with "File does not exist" before the
// wrapper sees the request. The Phase A fallback in DocumentSymbolHandler
// never runs.
//
// Mechanism:
//
//	1. Wrapper intercepts goToDefinition responses. al-preview:/ URIs
//	   are rewritten to file:// URIs pointing at extracted .al sources.
//	2. The extracted .al lives outside the workspace, under
//	   <UserCacheDir>/al-lsp-for-agents/preview-cache/<wsHash>/
//	   <App>/<Type>/<Id>/<Name>.al — deterministic from the al-preview URI.
//	   Keeping it outside the workspace is critical: if the materialized
//	   .al files were inside the workspace, the AL LSP's project scanner
//	   would pick them up and report every dependency object as a
//	   duplicate declaration (the same family of bugs as issue #17).
//	3. Subsequent requests on the cache file path are recognized via
//	   resolveCachePath, and the wrapper routes them as if the
//	   original al-preview URI were still in play.
//
// The materialized files are real AL source — the same content the AL
// LSP would render through its al-preview content provider — so agents
// can also read/grep them directly without any special handling.
type previewCache struct {
	workspaceRoot string
	root          string // resolved cache root (outside workspace)

	mu     sync.Mutex
	hits   map[string]string // originalURI → cache file path
	misses map[string]struct{}
}

func newPreviewCache(workspaceRoot string) *previewCache {
	return &previewCache{
		workspaceRoot: workspaceRoot,
		root:          computeCacheRoot(workspaceRoot),
		hits:          make(map[string]string),
		misses:        make(map[string]struct{}),
	}
}

// computeCacheRoot returns the per-workspace cache directory. It lives
// under the user cache dir so the AL LSP never sees the materialized
// files as part of the project. Workspaces are keyed by SHA1 of their
// absolute path so multiple checkouts don't collide.
func computeCacheRoot(workspaceRoot string) string {
	abs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		abs = workspaceRoot
	}
	sum := sha1.Sum([]byte(strings.ToLower(filepath.ToSlash(abs))))
	wsKey := hex.EncodeToString(sum[:8])

	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "al-lsp-for-agents", "preview-cache", wsKey)
}

// alPreviewRegex matches al-preview:/[/[/]]allang/<App>/<Type>/<Id>/<Name>.dal
// (tolerating one, two, or three slashes after the scheme — different
// AL LSP versions emit different forms).
var alPreviewRegex = regexp.MustCompile(`^al-preview:/{1,3}(?:allang/)?(.+)\.dal$`)

// parseALPreviewURI extracts (appName, objectType, objectID, objectName)
// from an al-preview:/allang/<App>/<Type>/<Id>/<Name>.dal URI.
//
// App names and object names may themselves contain `/`, so we anchor on
// the position of the first ObjectType-shaped segment (e.g. "Codeunit",
// "Table") — everything before that is the app name; the segment after
// is the object id; everything after that is the object name.
//
// Returns ok=false on any malformed input.
func parseALPreviewURI(uri string) (app, objType, objID, objName string, ok bool) {
	m := alPreviewRegex.FindStringSubmatch(uri)
	if m == nil {
		return
	}
	segments := strings.Split(m[1], "/")
	if len(segments) < 4 {
		return
	}

	// Locate the ObjectType segment.
	typeIdx := -1
	for i := 1; i < len(segments); i++ {
		dec, err := url.PathUnescape(segments[i])
		if err != nil {
			dec = segments[i]
		}
		if isObjectTypeName(dec) {
			// Next segment must look like an integer ID.
			if i+1 >= len(segments) {
				continue
			}
			nextDec, _ := url.PathUnescape(segments[i+1])
			if _, err := strconv.ParseInt(nextDec, 10, 64); err == nil {
				typeIdx = i
				break
			}
		}
	}
	if typeIdx < 0 || typeIdx+2 > len(segments)-1 {
		return
	}

	appParts := segments[:typeIdx]
	objTypeRaw, _ := url.PathUnescape(segments[typeIdx])
	objIDRaw, _ := url.PathUnescape(segments[typeIdx+1])
	nameParts := segments[typeIdx+2:]

	decodedAppParts := make([]string, len(appParts))
	for i, p := range appParts {
		decodedAppParts[i], _ = url.PathUnescape(p)
	}
	decodedNameParts := make([]string, len(nameParts))
	for i, p := range nameParts {
		decodedNameParts[i], _ = url.PathUnescape(p)
	}

	app = strings.Join(decodedAppParts, "/")
	objType = objTypeRaw
	objID = objIDRaw
	objName = strings.Join(decodedNameParts, "/")
	ok = app != "" && objType != "" && objName != ""
	return
}

// isObjectTypeName returns true for AL object-type identifiers that can
// appear in al-preview URIs (mirrors al-call-hierarchy's ObjectType).
func isObjectTypeName(s string) bool {
	switch strings.ToLower(s) {
	case "codeunit", "table", "page", "report", "query", "xmlport",
		"enum", "interface", "controladdin", "pageextension",
		"tableextension", "enumextension", "permissionset",
		"permissionsetextension":
		return true
	}
	return false
}

// cacheRoot returns the resolved cache root directory (outside the
// workspace, under the user cache dir).
func (c *previewCache) cacheRoot() string {
	return c.root
}

// cachePathFor maps an al-preview URI to its deterministic on-disk path.
// Filesystem-safe segment names are produced by replacing forbidden
// characters with `_`.
func (c *previewCache) cachePathFor(app, objType, objID, objName string) string {
	return filepath.Join(
		c.cacheRoot(),
		sanitizePathSegment(app),
		sanitizePathSegment(objType),
		sanitizePathSegment(objID),
		sanitizePathSegment(objName)+".al",
	)
}

var pathSegmentForbidden = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func sanitizePathSegment(s string) string {
	return pathSegmentForbidden.ReplaceAllString(s, "_")
}

// materialize extracts the .al source for an al-preview URI from the
// corresponding .app archive, writing it to the cache and returning the
// file:// URI. Idempotent — re-uses the existing cache file if present
// and non-empty.
func (c *previewCache) materialize(originalURI string, packageCachePaths []string) (string, error) {
	c.mu.Lock()
	if hit, ok := c.hits[originalURI]; ok {
		c.mu.Unlock()
		return PathToFileURI(hit), nil
	}
	if _, missed := c.misses[originalURI]; missed {
		c.mu.Unlock()
		return "", fmt.Errorf("previewCache: previously failed to materialize %s", originalURI)
	}
	c.mu.Unlock()

	app, objType, objID, objName, ok := parseALPreviewURI(originalURI)
	if !ok {
		return "", fmt.Errorf("previewCache: cannot parse al-preview URI %q", originalURI)
	}

	cachePath := c.cachePathFor(app, objType, objID, objName)

	// Re-use existing file when present.
	if info, err := os.Stat(cachePath); err == nil && info.Size() > 0 {
		c.recordHit(originalURI, cachePath)
		return PathToFileURI(cachePath), nil
	}

	// Locate the matching .app archive and extract the source.
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return "", fmt.Errorf("previewCache: mkdir: %w", err)
	}
	content, foundAppFile, err := c.findAndExtractSource(app, objType, objName, packageCachePaths)
	if err != nil {
		c.recordMiss(originalURI)
		return "", err
	}
	if err := os.WriteFile(cachePath, content, 0o644); err != nil {
		return "", fmt.Errorf("previewCache: write %s: %w", cachePath, err)
	}
	c.recordHit(originalURI, cachePath)
	_ = foundAppFile // currently unused; retained for future diagnostics
	return PathToFileURI(cachePath), nil
}

func (c *previewCache) recordHit(originalURI, cachePath string) {
	c.mu.Lock()
	c.hits[originalURI] = cachePath
	delete(c.misses, originalURI)
	c.mu.Unlock()
}

func (c *previewCache) recordMiss(originalURI string) {
	c.mu.Lock()
	c.misses[originalURI] = struct{}{}
	c.mu.Unlock()
}

// resolveCachePath returns (originalURI, true) when filePath is a cache
// file under .al-preview-cache. The caller can route subsequent ops on
// this path through the Phase A virtual-URI handlers.
//
// Path round-trip: <cache>/<App>/<Type>/<Id>/<Name>.al →
// al-preview:/allang/<App>/<Type>/<Id>/<Name>.dal. The sanitized cache
// segments preserve the original names (we only substitute filesystem-
// forbidden characters; AL identifiers don't typically use them).
func (c *previewCache) resolveCachePath(filePath string) (string, bool) {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return "", false
	}
	root, err := filepath.Abs(c.cacheRoot())
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 4 {
		return "", false
	}
	app := parts[0]
	objType := parts[1]
	objID := parts[2]
	nameBase := strings.TrimSuffix(parts[3], ".al")
	uri := fmt.Sprintf("al-preview:/allang/%s/%s/%s/%s.dal", app, objType, objID, nameBase)
	return uri, true
}

// findAndExtractSource searches package cache paths for a .app archive
// whose NavxManifest matches `app`, opens it as a zip, and extracts the
// .al file matching the given object. Returns the file content + the
// .app file path. Falls back to the first archive that contains a
// matching .al when manifest matching is inconclusive.
func (c *previewCache) findAndExtractSource(
	app, objType, objName string,
	packageCachePaths []string,
) ([]byte, string, error) {
	candidates := c.findAppCandidates(app, packageCachePaths)
	if len(candidates) == 0 {
		return nil, "", fmt.Errorf("previewCache: no .app archive matched app %q in %v",
			app, packageCachePaths)
	}

	wantPattern := sourceFileNameCandidates(objName, objType)

	for _, appPath := range candidates {
		content, err := readSourceFromArchive(appPath, wantPattern)
		if err == nil {
			return content, appPath, nil
		}
	}
	return nil, "", fmt.Errorf("previewCache: %s %q not found in any of %d candidate apps",
		objType, objName, len(candidates))
}

// findAppCandidates returns .app file paths whose filename matches the
// app name (case-insensitive, allowing the standard "Publisher_App_Version.app"
// convention). Results sorted by version descending so latest is first.
func (c *previewCache) findAppCandidates(app string, packageCachePaths []string) []string {
	appLC := strings.ToLower(app)
	var found []struct {
		path    string
		version string
	}
	seen := make(map[string]bool)

	for _, p := range packageCachePaths {
		dir := p
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(c.workspaceRoot, dir)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(strings.ToLower(name), ".app") {
				continue
			}
			// Filename format: Publisher_App_Version.app
			// Match by checking if "_<App>_" appears (case-insensitive).
			if !strings.Contains(strings.ToLower(name), "_"+appLC+"_") {
				continue
			}
			full := filepath.Join(dir, name)
			if seen[strings.ToLower(full)] {
				continue
			}
			seen[strings.ToLower(full)] = true

			// Extract version from filename for sorting.
			version := extractVersionFromAppFilename(name)
			found = append(found, struct {
				path    string
				version string
			}{full, version})
		}
	}

	sort.Slice(found, func(i, j int) bool {
		return compareALVersions(found[i].version, found[j].version) > 0
	})

	out := make([]string, len(found))
	for i, f := range found {
		out[i] = f.path
	}
	return out
}

// extractVersionFromAppFilename pulls "27.0.46665.47126" out of
// "Microsoft_Base Application_27.0.46665.47126.app".
func extractVersionFromAppFilename(filename string) string {
	name := strings.TrimSuffix(filename, ".app")
	lastUnderscore := strings.LastIndex(name, "_")
	if lastUnderscore < 0 {
		return ""
	}
	return name[lastUnderscore+1:]
}

// compareALVersions returns 1 if a > b, -1 if a < b, 0 if equal.
func compareALVersions(a, b string) int {
	aparts := versionParts(a)
	bparts := versionParts(b)
	maxLen := len(aparts)
	if len(bparts) > maxLen {
		maxLen = len(bparts)
	}
	for i := 0; i < maxLen; i++ {
		var av, bv uint64
		if i < len(aparts) {
			av = aparts[i]
		}
		if i < len(bparts) {
			bv = bparts[i]
		}
		if av > bv {
			return 1
		}
		if av < bv {
			return -1
		}
	}
	return 0
}

func versionParts(v string) []uint64 {
	parts := strings.Split(v, ".")
	out := make([]uint64, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			out = append(out, 0)
			continue
		}
		out = append(out, n)
	}
	return out
}

// sourceFileNameCandidates returns the set of filename patterns under
// `src/` that could match the given object. AL source filenames are
// conventionally `<NameWithoutSpaces>.<ObjectType>.al`, but Microsoft
// also uses `<Name>.<Type>.al` (spaces preserved) in some apps.
func sourceFileNameCandidates(objName, objType string) []string {
	candidates := []string{
		objName + "." + objType + ".al",
		strings.ReplaceAll(objName, " ", "") + "." + objType + ".al",
	}
	// Also tolerate hyphens being substituted for the space (rare).
	if strings.Contains(objName, " ") {
		candidates = append(candidates,
			strings.ReplaceAll(objName, " ", "-")+"."+objType+".al",
		)
	}
	return candidates
}

// readSourceFromArchive opens .app as zip, skipping the 40-byte NAVX
// header, and returns the bytes of the first `src/**/<name>` matching
// any of `wantNames` (case-insensitive on basename).
func readSourceFromArchive(appPath string, wantNames []string) ([]byte, error) {
	const navxHeaderSize = 40

	f, err := os.Open(appPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() <= navxHeaderSize {
		return nil, fmt.Errorf("file too small to be a .app: %d bytes", info.Size())
	}

	// Skip the NAVX header by wrapping the file with a SectionReader.
	sr := io.NewSectionReader(f, navxHeaderSize, info.Size()-navxHeaderSize)
	zr, err := zip.NewReader(sr, sr.Size())
	if err != nil {
		return nil, fmt.Errorf("open %s as zip: %w", appPath, err)
	}

	wantSet := make(map[string]bool, len(wantNames))
	for _, n := range wantNames {
		wantSet[strings.ToLower(n)] = true
	}

	for _, file := range zr.File {
		base := strings.ToLower(filepath.Base(file.Name))
		if !wantSet[base] {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", file.Name, err)
		}
		data, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return data, nil
	}
	return nil, fmt.Errorf("no matching source file in %s (wanted any of %v)", appPath, wantNames)
}
