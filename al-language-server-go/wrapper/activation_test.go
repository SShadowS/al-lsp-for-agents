package wrapper

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeALLS wires w.stdin to an in-process pipe that records every message the
// wrapper sends the AL LS and answers requests with {"loaded":true}.
type fakeALLS struct {
	mu      sync.Mutex
	methods []string
}

func newFakeALLS(w *ALLSPWrapper) *fakeALLS {
	f := &fakeALLS{}
	pr, pw := io.Pipe()
	w.stdin = pw
	go func() {
		r := bufio.NewReader(pr)
		for {
			msg, err := ReadMessage(r)
			if err != nil {
				return
			}
			f.mu.Lock()
			f.methods = append(f.methods, msg.Method)
			f.mu.Unlock()
			if msg.ID == nil {
				continue
			}
			var id int
			json.Unmarshal(*msg.ID, &id)
			w.pendingMu.Lock()
			ch := w.pendingReqs[id]
			w.pendingMu.Unlock()
			if ch != nil {
				ch <- &Message{JSONRPC: "2.0", ID: msg.ID, Result: json.RawMessage(`{"loaded":true}`)}
			}
		}
	}()
	return f
}

// take returns what the fake has recorded once it holds n messages (or after
// 2 s), then resets. The pipe write returns before the reader has recorded.
func (f *fakeALLS) take(n int) []string {
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		f.mu.Lock()
		got := len(f.methods)
		f.mu.Unlock()
		if got >= n {
			break
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.methods
	f.methods = nil
	return out
}

// An edit in a non-active project must switch the AL LS to that project before
// the edit reaches it, or the AL LS never publishes diagnostics for it.
func TestDocumentEventActivatesProject(t *testing.T) {
	root := t.TempDir()
	projA, projB := filepath.Join(root, "A"), filepath.Join(root, "B")
	for _, p := range []string{projA, projB} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, p, "app.json", `{"id":"x","name":"x","publisher":"P","version":"1.0.0.0"}`)
	}

	w := &ALLSPWrapper{
		openedFiles:         make(map[string]bool),
		initializedProjects: map[string]bool{NormalizePath(projA): true, NormalizePath(projB): true},
		projectManifests:    make(map[string]*AppManifest),
		pendingReqs:         make(map[int]chan *Message),
		responseQueue:       make(map[int]*Message),
		activeProject:       NormalizePath(projA),
	}
	fake := newFakeALLS(w)

	send := func(method string) {
		params, _ := json.Marshal(map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": PathToFileURI(filepath.Join(projB, "x.al")), "version": 2},
		})
		if _, err := w.handleMessage(&Message{JSONRPC: "2.0", Method: method, Params: params}); err != nil {
			t.Fatal(err)
		}
	}

	send("textDocument/didChange")
	got := fake.take(2)
	if len(got) != 2 || got[0] != "al/setActiveWorkspace" || got[1] != "textDocument/didChange" {
		t.Fatalf("want [al/setActiveWorkspace textDocument/didChange], got %v", got)
	}
	if w.activeProject != NormalizePath(projB) {
		t.Fatalf("activeProject = %q, want B", w.activeProject)
	}

	// Already active: forwarded as-is, no second activation.
	send("textDocument/didOpen")
	// Wait for 2 so a spurious activation would be caught, not raced past.
	if got := fake.take(2); len(got) != 1 || got[0] != "textDocument/didOpen" {
		t.Fatalf("want [textDocument/didOpen], got %v", got)
	}
}

// newTwoProjectWrapper returns a wrapper over two on-disk AL projects A and B,
// neither initialized nor active, wired to a fake AL LS.
func newTwoProjectWrapper(t *testing.T) (w *ALLSPWrapper, fake *fakeALLS, projA, projB string) {
	root := t.TempDir()
	projA, projB = filepath.Join(root, "A"), filepath.Join(root, "B")
	for _, p := range []string{projA, projB} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, p, "app.json", `{"id":"x","name":"x","publisher":"P","version":"1.0.0.0"}`)
	}
	w = &ALLSPWrapper{
		openedFiles:         make(map[string]bool),
		initializedProjects: make(map[string]bool),
		projectManifests:    make(map[string]*AppManifest),
		pendingReqs:         make(map[int]chan *Message),
		responseQueue:       make(map[int]*Message),
	}
	return w, newFakeALLS(w), projA, projB
}

// indexOf returns the position of method in got, or -1.
func indexOf(got []string, method string) int {
	for i, m := range got {
		if m == method {
			return i
		}
	}
	return -1
}

// Every project's first activation must wait for its closure to load, not
// only the session's first project. Otherwise requests right after a switch
// to a second project are answered from a half-loaded closure.
func TestFirstActivationOfEachProjectWaitsForLoad(t *testing.T) {
	w, fake, projA, projB := newTwoProjectWrapper(t)

	for _, p := range []string{projA, projB} {
		if err := w.EnsureProjectInitialized(filepath.Join(p, "x.al")); err != nil {
			t.Fatal(err)
		}
		// didChangeConfiguration, didOpen(app.json), loadManifest,
		// setActiveWorkspace, hasProjectClosureLoaded
		got := fake.take(5)
		act, wait := indexOf(got, "al/setActiveWorkspace"), indexOf(got, "al/hasProjectClosureLoadedRequest")
		if act < 0 || wait < act {
			t.Fatalf("%s: want setActiveWorkspace then hasProjectClosureLoadedRequest, got %v", filepath.Base(p), got)
		}
	}

	// Switching back to A (already loaded once) activates without waiting.
	if err := w.EnsureProjectInitialized(filepath.Join(projA, "x.al")); err != nil {
		t.Fatal(err)
	}
	if got := fake.take(2); len(got) != 1 || got[0] != "al/setActiveWorkspace" {
		t.Fatalf("re-activation: want [al/setActiveWorkspace], got %v", got)
	}
}

// workspace/symbol carries no document. With nothing active it must activate
// (and wait for) the project found at initialize before searching.
func TestEnsureAnyProjectActiveUsesInitProject(t *testing.T) {
	w, fake, projA, _ := newTwoProjectWrapper(t)
	w.initProjectRoot = projA

	if err := w.EnsureAnyProjectActive(); err != nil {
		t.Fatal(err)
	}
	if got := fake.take(5); indexOf(got, "al/hasProjectClosureLoadedRequest") < 0 {
		t.Fatalf("want activation with load wait, got %v", got)
	}
	if w.activeProject != NormalizePath(projA) {
		t.Fatalf("activeProject = %q, want A", w.activeProject)
	}

	// Already active: nothing sent.
	if err := w.EnsureAnyProjectActive(); err != nil {
		t.Fatal(err)
	}
	if got := fake.take(1); len(got) != 0 {
		t.Fatalf("want nothing sent, got %v", got)
	}
}

// References from Core must see uses in Leasing, which only loads when a
// project above it (Test) is activated. The wrapper activates Test once, not
// Leasing (Test's closure covers it) and not the unrelated Other, then
// switches back to Core.
func TestEnsureDependentsLoadedActivatesTopDependent(t *testing.T) {
	root := t.TempDir()
	app := func(name, id string, deps ...string) {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		var ds []map[string]string
		for _, d := range deps {
			ds = append(ds, map[string]string{"id": d, "name": d, "publisher": "P", "version": "1.0.0.0"})
		}
		body, _ := json.Marshal(map[string]interface{}{"id": id, "name": name, "publisher": "P", "version": "1.0.0.0", "dependencies": ds})
		writeTestFile(t, p, "app.json", string(body))
	}
	app("Core", "core")
	app("Leasing", "leasing", "core")
	app("Test", "test", "leasing", "core")
	app("Other", "other")

	core := NormalizePath(filepath.Join(root, "Core"))
	w := &ALLSPWrapper{
		openedFiles:         make(map[string]bool),
		initializedProjects: map[string]bool{core: true},
		projectManifests:    make(map[string]*AppManifest),
		pendingReqs:         make(map[int]chan *Message),
		responseQueue:       make(map[int]*Message),
		activeProject:       core,
		workspaceFolders:    []WorkspaceFolder{{URI: PathToFileURI(root), Name: "ws"}},
	}
	fake := newFakeALLS(w)
	coreFile := filepath.Join(root, "Core", "x.al")

	if err := w.EnsureDependentsLoaded(coreFile); err != nil {
		t.Fatal(err)
	}
	// Test: didChangeConfiguration, didOpen(app.json), loadManifest,
	// setActiveWorkspace, hasProjectClosureLoaded; then back to Core:
	// setActiveWorkspace.
	got := fake.take(6)
	var activations []string
	for _, m := range got {
		if m == "al/setActiveWorkspace" {
			activations = append(activations, m)
		}
	}
	if len(activations) != 2 || indexOf(got, "al/hasProjectClosureLoadedRequest") < 0 {
		t.Fatalf("want Test activated with a load wait, then Core again; got %v", got)
	}
	if !w.initializedProjects[NormalizePath(filepath.Join(root, "Test"))] {
		t.Fatal("Test was not activated")
	}
	for _, skip := range []string{"Leasing", "Other"} {
		if w.initializedProjects[NormalizePath(filepath.Join(root, skip))] {
			t.Fatalf("%s should not be activated", skip)
		}
	}
	if w.activeProject != core {
		t.Fatalf("activeProject = %q, want Core back", w.activeProject)
	}

	// Once per session: dependents are loaded now, nothing is sent.
	if err := w.EnsureDependentsLoaded(coreFile); err != nil {
		t.Fatal(err)
	}
	if got := fake.take(1); len(got) != 0 {
		t.Fatalf("second call: want nothing sent, got %v", got)
	}
}

// A client-sent al/setActiveWorkspace must update the wrapper's cache, or the
// next request for the previously cached project skips a needed re-activation.
func TestSetActiveWorkspaceHandlerUpdatesActiveProject(t *testing.T) {
	m := newMockWrapper()
	params, _ := json.Marshal(map[string]interface{}{
		"currentWorkspaceFolderPath": map[string]interface{}{"uri": "file:///c%3A/projects/leasing", "name": "leasing"},
	})
	id := json.RawMessage(`1`)
	(&SetActiveWorkspaceHandler{}).Handle(&Message{JSONRPC: "2.0", ID: &id, Method: "al/setActiveWorkspace", Params: params}, m)

	if want := NormalizePath("c:/projects/leasing"); NormalizePath(m.activeProject) != want {
		t.Fatalf("activeProject = %q, want %q", m.activeProject, want)
	}
}
