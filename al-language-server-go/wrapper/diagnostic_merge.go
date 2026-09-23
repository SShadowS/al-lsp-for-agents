package wrapper

import (
	"encoding/json"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// caseInsensitivePaths is true where file paths compare case-insensitively.
// al-call-hierarchy case-folds paths on Windows, so its URIs differ in case
// from the AL LS URIs for the same file.
var caseInsensitivePaths = runtime.GOOS == "windows"

// uriKey is the merge key for a URI: percent-decoded, and case-folded where
// paths are case-insensitive.
func uriKey(uri string) string {
	if d, err := url.PathUnescape(uri); err == nil {
		uri = d
	}
	if caseInsensitivePaths {
		uri = strings.ToLower(uri)
	}
	return uri
}

// Backend identifiers for diagnostic sources funnelled to the client.
const (
	diagBackendALLS          = "al-ls"          // Microsoft AL Language Server
	diagBackendCallHierarchy = "call-hierarchy" // al-call-hierarchy server
)

// DiagnosticMerger reconciles textDocument/publishDiagnostics from the multiple
// backends the wrapper fronts (Microsoft AL LS + al-call-hierarchy) into a
// single per-URI set sent to the client.
//
// LSP publishDiagnostics is last-writer-wins per URI per connection: a backend's
// notification REPLACES every diagnostic the client holds for that URI. Because
// the wrapper presents both backends over one client connection, forwarding each
// backend's notification verbatim makes them clobber one another — whichever
// publishes last for a URI wins, and the other backend's diagnostics vanish.
// That is issue #20's symptom #2: running prepareCallHierarchy makes the AL LS
// re-analyse and republish a file, erasing the al-call-hierarchy diagnostics
// that were published earlier for the same URI.
//
// The merger keeps the last diagnostics each backend reported per URI and emits
// their union, so neither backend can erase the other. A backend clearing its
// own diagnostics (empty array) only removes its own contribution.
type DiagnosticMerger struct {
	mu sync.Mutex
	// uriKey(uri) -> backend -> that backend's last reported diagnostics (raw JSON).
	byURI map[string]map[string][]json.RawMessage
	// uriKey(uri) -> the URI the client (didOpen) or the AL LS uses for that
	// file. Other backends' publishes go out under it, so the client sees one
	// URI per file.
	preferred map[string]string
}

// NewDiagnosticMerger returns an empty merger.
func NewDiagnosticMerger() *DiagnosticMerger {
	return &DiagnosticMerger{
		byURI:     make(map[string]map[string][]json.RawMessage),
		preferred: make(map[string]string),
	}
}

// Merge records backend's current diagnostics for uri and returns the union of
// all backends' diagnostics for that uri. Output order is deterministic
// (backends in sorted id order, diagnostics in the order each backend reported
// them). A non-nil empty slice is returned when the union is empty so callers
// can publish an explicit "cleared" array.
func (m *DiagnosticMerger) Merge(backend, uri string, diags []json.RawMessage) []json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := uriKey(uri)
	perBackend := m.byURI[key]
	if perBackend == nil {
		perBackend = make(map[string][]json.RawMessage)
		m.byURI[key] = perBackend
	}
	if len(diags) == 0 {
		delete(perBackend, backend)
	} else {
		perBackend[backend] = diags
	}

	backends := make([]string, 0, len(perBackend))
	for b := range perBackend {
		backends = append(backends, b)
	}
	sort.Strings(backends)

	merged := make([]json.RawMessage, 0)
	for _, b := range backends {
		merged = append(merged, perBackend[b]...)
	}
	return merged
}

// MergePublishDiagnostics rewrites a textDocument/publishDiagnostics message in
// place so its diagnostics array is the union across backends for the message's
// URI. backend identifies which server produced msg. Returns false (leaving msg
// untouched) when the message is not a parseable publishDiagnostics payload.
func (m *DiagnosticMerger) MergePublishDiagnostics(backend string, msg *Message) bool {
	if msg == nil || len(msg.Params) == 0 {
		return false
	}
	var pd struct {
		URI         string            `json:"uri"`
		Diagnostics []json.RawMessage `json:"diagnostics"`
		Version     *int              `json:"version,omitempty"`
	}
	if err := json.Unmarshal(msg.Params, &pd); err != nil || pd.URI == "" {
		return false
	}

	merged := m.Merge(backend, pd.URI, pd.Diagnostics)

	uri := pd.URI
	m.mu.Lock()
	if backend == diagBackendALLS {
		m.preferred[uriKey(uri)] = uri
	} else if p, ok := m.preferred[uriKey(uri)]; ok {
		uri = p
	}
	m.mu.Unlock()

	out := map[string]interface{}{
		"uri":         uri,
		"diagnostics": merged,
	}
	if pd.Version != nil {
		out["version"] = *pd.Version
	}
	rewritten, err := json.Marshal(out)
	if err != nil {
		return false
	}
	msg.Params = rewritten
	return true
}

// PreferURI records the client's URI for a file (from didOpen), so diagnostics
// from any backend are published under it.
func (m *DiagnosticMerger) PreferURI(uri string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.preferred[uriKey(uri)] = uri
}
