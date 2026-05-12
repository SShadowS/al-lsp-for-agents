package wrapper

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var jsonMarshal = json.Marshal

func TestParseALPreviewURI(t *testing.T) {
	cases := []struct {
		name        string
		uri         string
		wantApp     string
		wantType    string
		wantID      string
		wantName    string
		wantOK      bool
	}{
		{
			name:     "standard 3-slash form",
			uri:      "al-preview:///allang/Base%20Application/Codeunit/1535/Approvals%20Mgmt..dal",
			wantApp:  "Base Application",
			wantType: "Codeunit",
			wantID:   "1535",
			wantName: "Approvals Mgmt.",
			wantOK:   true,
		},
		{
			name:     "single slash form",
			uri:      "al-preview:/allang/My%20App/Table/50000/My%20Table.dal",
			wantApp:  "My App",
			wantType: "Table",
			wantID:   "50000",
			wantName: "My Table",
			wantOK:   true,
		},
		{
			name:   "missing allang prefix",
			uri:    "al-preview:///Base%20Application/Codeunit/1535/Approvals%20Mgmt..dal",
			wantApp: "Base Application",
			wantType: "Codeunit",
			wantID:  "1535",
			wantName: "Approvals Mgmt.",
			wantOK: true,
		},
		{
			name:   "not an al-preview URI",
			uri:    "file:///c:/work/foo.al",
			wantOK: false,
		},
		{
			name:   "malformed: missing segments",
			uri:    "al-preview:///allang/justthis.dal",
			wantOK: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app, ty, id, name, ok := parseALPreviewURI(c.uri)
			if ok != c.wantOK {
				t.Fatalf("ok=%v want %v (got app=%q type=%q id=%q name=%q)", ok, c.wantOK, app, ty, id, name)
			}
			if !c.wantOK {
				return
			}
			if app != c.wantApp || ty != c.wantType || id != c.wantID || name != c.wantName {
				t.Errorf("got (app=%q type=%q id=%q name=%q) want (%q %q %q %q)",
					app, ty, id, name, c.wantApp, c.wantType, c.wantID, c.wantName)
			}
		})
	}
}

func TestPreviewCache_ResolveCachePath_Roundtrip(t *testing.T) {
	tmp := t.TempDir()
	c := newPreviewCache(tmp)
	cachePath := c.cachePathFor("Base Application", "Codeunit", "1535", "Approvals Mgmt.")

	uri, ok := c.resolveCachePath(cachePath)
	if !ok {
		t.Fatalf("resolveCachePath returned !ok for %q", cachePath)
	}
	want := "al-preview:/allang/Base Application/Codeunit/1535/Approvals Mgmt..dal"
	if uri != want {
		t.Errorf("got URI %q want %q", uri, want)
	}
}

func TestPreviewCache_ResolveCachePath_RejectsOutsidePath(t *testing.T) {
	tmp := t.TempDir()
	c := newPreviewCache(tmp)
	other := filepath.Join(tmp, "some-other-folder", "foo.al")
	if _, ok := c.resolveCachePath(other); ok {
		t.Errorf("expected resolveCachePath to reject path outside cache root")
	}
}

func TestCompareALVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"27.0.46665.47126", "26.0.0.0", 1},
		{"26.0.0.0", "27.0.46665.47126", -1},
		{"27.0.46665.47126", "27.0.46665.47126", 0},
		{"27.0", "27.0.0.0", 0},
		{"27.1", "27.0.999.999", 1},
	}
	for _, c := range cases {
		got := compareALVersions(c.a, c.b)
		if got != c.want {
			t.Errorf("compareALVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestExtractVersionFromAppFilename(t *testing.T) {
	got := extractVersionFromAppFilename("Microsoft_Base Application_27.0.46665.47126.app")
	if got != "27.0.46665.47126" {
		t.Errorf("got %q want %q", got, "27.0.46665.47126")
	}
	if got := extractVersionFromAppFilename("noversion.app"); got != "" {
		t.Errorf("expected empty for missing underscore, got %q", got)
	}
}

func TestSourceFileNameCandidates(t *testing.T) {
	cands := sourceFileNameCandidates("Approvals Mgmt.", "Codeunit")
	want := []string{
		"Approvals Mgmt..Codeunit.al",
		"ApprovalsMgmt..Codeunit.al",
		"Approvals-Mgmt..Codeunit.al",
	}
	if len(cands) != len(want) {
		t.Fatalf("got %d candidates, want %d (%v)", len(cands), len(want), cands)
	}
	for i, w := range want {
		if cands[i] != w {
			t.Errorf("candidate %d: got %q want %q", i, cands[i], w)
		}
	}
}

// buildFakeApp writes a minimal .app archive: 40-byte NAVX header + ZIP
// containing the given files. Used to exercise materialize() end-to-end.
func buildFakeApp(t *testing.T, outPath string, files map[string]string) {
	t.Helper()

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	header := make([]byte, 40)
	copy(header[:4], []byte("NAVX"))
	binary.LittleEndian.PutUint32(header[4:8], 1)

	f, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("create %s: %v", outPath, err)
	}
	defer f.Close()
	if _, err := f.Write(header); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := f.Write(zipBuf.Bytes()); err != nil {
		t.Fatalf("write zip: %v", err)
	}
}

func TestPreviewCache_Materialize(t *testing.T) {
	workspace := t.TempDir()
	pkgCache := filepath.Join(workspace, ".alpackages")
	if err := os.MkdirAll(pkgCache, 0o755); err != nil {
		t.Fatal(err)
	}

	const objSrc = "codeunit 1535 \"Approvals Mgmt.\"\n{\n}\n"
	appPath := filepath.Join(pkgCache, "Microsoft_Base Application_27.0.46665.47126.app")
	buildFakeApp(t, appPath, map[string]string{
		"NavxManifest.xml":                 "<App />",
		"src/Approvals Mgmt..Codeunit.al":  objSrc,
	})

	c := newPreviewCache(workspace)
	uri := "al-preview:///allang/Base%20Application/Codeunit/1535/Approvals%20Mgmt..dal"

	mapped, err := c.materialize(uri, []string{pkgCache})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if !strings.HasPrefix(mapped, "file://") {
		t.Errorf("expected file:// URI, got %q", mapped)
	}
	cachePath, err := FileURIToPath(mapped)
	if err != nil {
		t.Fatalf("FileURIToPath(%q): %v", mapped, err)
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read cache file: %v", err)
	}
	if string(data) != objSrc {
		t.Errorf("cache content mismatch:\ngot:  %q\nwant: %q", string(data), objSrc)
	}

	// Round-trip via resolveCachePath should recover an al-preview URI
	// pointing back to the same object.
	roundTrip, ok := c.resolveCachePath(cachePath)
	if !ok {
		t.Fatalf("resolveCachePath returned !ok for %q", cachePath)
	}
	app, ty, id, name, parseOK := parseALPreviewURI(roundTrip)
	if !parseOK {
		t.Fatalf("round-trip URI not parseable: %q", roundTrip)
	}
	if app != "Base Application" || ty != "Codeunit" || id != "1535" || name != "Approvals Mgmt." {
		t.Errorf("round-trip mismatch: app=%q type=%q id=%q name=%q", app, ty, id, name)
	}

	// Second materialize hits the cache (file present + memoized).
	mapped2, err := c.materialize(uri, []string{pkgCache})
	if err != nil {
		t.Fatalf("materialize (2nd call): %v", err)
	}
	if mapped2 != mapped {
		t.Errorf("idempotency: 2nd materialize gave %q want %q", mapped2, mapped)
	}
}

func TestPreviewCache_Materialize_PrefersNewerVersion(t *testing.T) {
	workspace := t.TempDir()
	pkgCache := filepath.Join(workspace, ".alpackages")
	if err := os.MkdirAll(pkgCache, 0o755); err != nil {
		t.Fatal(err)
	}

	// Old version contains a stale copy.
	oldPath := filepath.Join(pkgCache, "Microsoft_Base Application_26.0.0.0.app")
	buildFakeApp(t, oldPath, map[string]string{
		"src/Approvals Mgmt..Codeunit.al": "stale\n",
	})

	// New version contains the fresh copy.
	newPath := filepath.Join(pkgCache, "Microsoft_Base Application_27.0.46665.47126.app")
	const fresh = "fresh\n"
	buildFakeApp(t, newPath, map[string]string{
		"src/Approvals Mgmt..Codeunit.al": fresh,
	})

	c := newPreviewCache(workspace)
	uri := "al-preview:///allang/Base%20Application/Codeunit/1535/Approvals%20Mgmt..dal"
	mapped, err := c.materialize(uri, []string{pkgCache})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	cachePath, _ := FileURIToPath(mapped)
	data, _ := os.ReadFile(cachePath)
	if string(data) != fresh {
		t.Errorf("expected newer version content, got %q", string(data))
	}
}

func TestPreviewCache_Materialize_MissingApp(t *testing.T) {
	workspace := t.TempDir()
	c := newPreviewCache(workspace)
	_, err := c.materialize(
		"al-preview:///allang/Nonexistent/Codeunit/1/Foo.dal",
		[]string{filepath.Join(workspace, ".alpackages")},
	)
	if err == nil {
		t.Fatalf("expected error for missing app, got nil")
	}
}

func TestTranslateCachePathToVirtual(t *testing.T) {
	workspace := t.TempDir()
	w := newMockWrapper()
	w.previewCache = newPreviewCache(workspace)
	w.workspaceFolders = []WorkspaceFolder{{URI: PathToFileURI(workspace), Name: "ws"}}

	// Real file URI (not in cache) → no translation.
	realURI := PathToFileURI(filepath.Join(workspace, "src", "Codeunit.al"))
	out, ok := translateCachePathToVirtual(w, realURI)
	if ok || out != realURI {
		t.Errorf("non-cache path: got (%q, %v), expected (%q, false)", out, ok, realURI)
	}

	// Already-virtual URI → no translation (caller handles directly).
	virtual := "al-preview:///allang/Base%20Application/Codeunit/1535/Foo.dal"
	out, ok = translateCachePathToVirtual(w, virtual)
	if ok || out != virtual {
		t.Errorf("virtual URI: got (%q, %v), expected (%q, false)", out, ok, virtual)
	}

	// Cache path → translated to al-preview URI.
	cachePath := w.previewCache.cachePathFor("Base Application", "Codeunit", "1535", "Approvals Mgmt.")
	cacheURI := PathToFileURI(cachePath)
	out, ok = translateCachePathToVirtual(w, cacheURI)
	if !ok {
		t.Fatalf("cache path: expected ok=true, got false (out=%q)", out)
	}
	if !strings.HasPrefix(out, "al-preview:") {
		t.Errorf("expected al-preview URI, got %q", out)
	}
}

// docSymbolMsg builds a textDocument/documentSymbol Message targeting uri.
func docSymbolMsg(t *testing.T, uri string) *Message {
	t.Helper()
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
	}
	raw, err := jsonMarshal(params)
	if err != nil {
		t.Fatal(err)
	}
	id := json.RawMessage(`1`)
	return &Message{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/documentSymbol",
		Params:  raw,
	}
}

// hoverMsg builds a textDocument/hover Message targeting uri.
func hoverMsg(t *testing.T, uri string) *Message {
	t.Helper()
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": uri},
		"position":     map[string]interface{}{"line": 0, "character": 0},
	}
	raw, err := jsonMarshal(params)
	if err != nil {
		t.Fatal(err)
	}
	id := json.RawMessage(`1`)
	return &Message{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "textDocument/hover",
		Params:  raw,
	}
}

func TestDocumentSymbolHandler_TranslatesCachePath(t *testing.T) {
	workspace := t.TempDir()
	w := newMockWrapper()
	w.previewCache = newPreviewCache(workspace)
	w.workspaceFolders = []WorkspaceFolder{{URI: PathToFileURI(workspace), Name: "ws"}}

	cachePath := w.previewCache.cachePathFor("Base Application", "Codeunit", "1535", "Approvals Mgmt.")
	cacheURI := PathToFileURI(cachePath)
	msg := docSymbolMsg(t, cacheURI)

	h := &DocumentSymbolHandler{}
	_, errResp := h.Handle(msg, w)
	if errResp != nil {
		t.Fatalf("unexpected error response: %v", errResp.Error)
	}

	// AL LSP must have seen the al-preview URI, not the cache URI.
	if len(w.lspRequests) == 0 {
		t.Fatalf("no LSP requests captured")
	}
	last := w.lspRequests[len(w.lspRequests)-1]
	if last.Method != "textDocument/documentSymbol" {
		t.Fatalf("expected documentSymbol request, got %s", last.Method)
	}
	bz, _ := jsonMarshal(last.Params)
	got := string(bz)
	if !strings.Contains(got, "al-preview:") {
		t.Errorf("expected al-preview URI in forwarded params, got %s", got)
	}
	if strings.Contains(got, ".al-preview-cache") {
		t.Errorf("forwarded params still contain cache path: %s", got)
	}
}

func TestHoverHandler_TranslatesCachePath(t *testing.T) {
	workspace := t.TempDir()
	w := newMockWrapper()
	w.previewCache = newPreviewCache(workspace)
	w.workspaceFolders = []WorkspaceFolder{{URI: PathToFileURI(workspace), Name: "ws"}}

	cachePath := w.previewCache.cachePathFor("Base Application", "Codeunit", "1535", "Approvals Mgmt.")
	cacheURI := PathToFileURI(cachePath)
	msg := hoverMsg(t, cacheURI)

	h := &HoverHandler{}
	_, errResp := h.Handle(msg, w)
	if errResp != nil {
		t.Fatalf("unexpected error response: %v", errResp.Error)
	}

	if len(w.lspRequests) == 0 {
		t.Fatalf("no LSP requests captured")
	}
	// First call out of HoverHandler.Handle is textDocument/hover.
	first := w.lspRequests[0]
	if first.Method != "textDocument/hover" {
		t.Fatalf("expected hover as first request, got %s", first.Method)
	}
	bz, _ := jsonMarshal(first.Params)
	got := string(bz)
	if !strings.Contains(got, "al-preview:") {
		t.Errorf("expected al-preview URI in forwarded params, got %s", got)
	}
	if strings.Contains(got, ".al-preview-cache") {
		t.Errorf("forwarded params still contain cache path: %s", got)
	}
}

func TestTranslateCachePathToVirtual_NoCache(t *testing.T) {
	w := newMockWrapper()
	// previewCache nil — handler must just return input unchanged.
	uri := PathToFileURI("c:/work/foo.al")
	out, ok := translateCachePathToVirtual(w, uri)
	if ok {
		t.Errorf("expected ok=false when cache is nil, got true")
	}
	if out != uri {
		t.Errorf("expected unchanged URI, got %q", out)
	}
}
