package wrapper

import (
	"encoding/json"
	"sort"
	"sync"
)

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
	// uri -> backend -> that backend's last reported diagnostics (raw JSON).
	byURI map[string]map[string][]json.RawMessage
}

// NewDiagnosticMerger returns an empty merger.
func NewDiagnosticMerger() *DiagnosticMerger {
	return &DiagnosticMerger{byURI: make(map[string]map[string][]json.RawMessage)}
}

// Merge records backend's current diagnostics for uri and returns the union of
// all backends' diagnostics for that uri. Output order is deterministic
// (backends in sorted id order, diagnostics in the order each backend reported
// them). A non-nil empty slice is returned when the union is empty so callers
// can publish an explicit "cleared" array.
func (m *DiagnosticMerger) Merge(backend, uri string, diags []json.RawMessage) []json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()

	perBackend := m.byURI[uri]
	if perBackend == nil {
		perBackend = make(map[string][]json.RawMessage)
		m.byURI[uri] = perBackend
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

	out := map[string]interface{}{
		"uri":         pd.URI,
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
