package wrapper

import (
	"encoding/json"
	"testing"
	"time"
)

// resetSymbolSearchState clears the package-level cold-start/warning flags and
// shrinks the retry backoffs so tests are deterministic and fast. It returns a
// restore func for the backoffs.
func resetSymbolSearchState(t *testing.T) {
	t.Helper()
	symbolSearchEverReturnedResults.Store(false)
	emptyWorkspaceSymbolWarned.Store(false)
	orig := coldSymbolSearchBackoffs
	coldSymbolSearchBackoffs = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() {
		coldSymbolSearchBackoffs = orig
		symbolSearchEverReturnedResults.Store(false)
		emptyWorkspaceSymbolWarned.Store(false)
	})
}

func symbolSearchResponse(names ...string) *Message {
	resp := ALSymbolSearchResponse{Symbols: make([]ALSymbol, 0, len(names))}
	for _, n := range names {
		resp.Symbols = append(resp.Symbols, ALSymbol{Name: n, Kind: "Table", Path: "/x/" + n + ".al"})
	}
	b, _ := json.Marshal(resp)
	return &Message{JSONRPC: "2.0", Result: b}
}

func newWorkspaceSymbolMessage(query string) *Message {
	params, _ := json.Marshal(WorkspaceSymbolParams{Query: query})
	id := json.RawMessage("1")
	return &Message{JSONRPC: "2.0", ID: &id, Method: "workspace/symbol", Params: params}
}

func countSymbolSearchRequests(m *mockWrapper) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.lspRequests {
		if c.Method == "al/symbolSearch" {
			n++
		}
	}
	return n
}

// Cold index: the first few al/symbolSearch calls return empty, then results
// appear. The handler should retry through the warmup and return the results.
func TestWorkspaceSymbol_RetriesWhileIndexCold(t *testing.T) {
	resetSymbolSearchState(t)

	m := newMockWrapper()
	calls := 0
	m.lspResponder = func(method string, params interface{}) (*Message, error) {
		calls++
		if calls < 3 {
			return symbolSearchResponse(), nil // cold: empty
		}
		return symbolSearchResponse("TEST Customer"), nil // warm
	}

	h := &WorkspaceSymbolHandler{}
	resp, errResp := h.Handle(newWorkspaceSymbolMessage("Customer"), m)
	if errResp != nil {
		t.Fatalf("unexpected error response: %v", errResp.Error)
	}

	var symbols []SymbolInformation
	if err := json.Unmarshal(resp.Result, &symbols); err != nil {
		t.Fatalf("bad result json: %v", err)
	}
	if len(symbols) != 1 || symbols[0].Name != "TEST Customer" {
		t.Fatalf("expected 1 symbol TEST Customer after warmup, got %+v", symbols)
	}
	if got := countSymbolSearchRequests(m); got != 3 {
		t.Fatalf("expected 3 al/symbolSearch attempts (2 cold + 1 warm), got %d", got)
	}
	if !symbolSearchEverReturnedResults.Load() {
		t.Fatal("expected warm flag to be set after results returned")
	}
}

// Once the index has warmed (flag set), a 0-result search is a genuine miss:
// return [] immediately with no retry.
func TestWorkspaceSymbol_NoRetryOnceWarm(t *testing.T) {
	resetSymbolSearchState(t)
	symbolSearchEverReturnedResults.Store(true) // simulate prior successful search

	m := newMockWrapper()
	m.lspResponder = func(method string, params interface{}) (*Message, error) {
		return symbolSearchResponse(), nil // always empty
	}

	h := &WorkspaceSymbolHandler{}
	resp, errResp := h.Handle(newWorkspaceSymbolMessage("NoSuchSymbol"), m)
	if errResp != nil {
		t.Fatalf("unexpected error response: %v", errResp.Error)
	}
	var symbols []SymbolInformation
	if err := json.Unmarshal(resp.Result, &symbols); err != nil {
		t.Fatalf("bad result json: %v", err)
	}
	if len(symbols) != 0 {
		t.Fatalf("expected genuine empty miss, got %+v", symbols)
	}
	if got := countSymbolSearchRequests(m); got != 1 {
		t.Fatalf("expected exactly 1 attempt when warm, got %d", got)
	}
}

// Cold and stays empty: handler gives up after exhausting backoffs and returns
// [] without looping forever.
func TestWorkspaceSymbol_GivesUpAfterColdRetries(t *testing.T) {
	resetSymbolSearchState(t)

	m := newMockWrapper()
	m.lspResponder = func(method string, params interface{}) (*Message, error) {
		return symbolSearchResponse(), nil // always empty
	}

	h := &WorkspaceSymbolHandler{}
	resp, _ := h.Handle(newWorkspaceSymbolMessage("Customer"), m)
	var symbols []SymbolInformation
	json.Unmarshal(resp.Result, &symbols)
	if len(symbols) != 0 {
		t.Fatalf("expected empty result, got %+v", symbols)
	}
	// 1 initial attempt + len(backoffs) retries.
	want := 1 + len(coldSymbolSearchBackoffs)
	if got := countSymbolSearchRequests(m); got != want {
		t.Fatalf("expected %d attempts, got %d", want, got)
	}
}

// Empty query never reaches the AL LS and warns the client once.
func TestWorkspaceSymbol_EmptyQueryWarnsAndReturnsEmpty(t *testing.T) {
	resetSymbolSearchState(t)

	m := newMockWrapper()
	h := &WorkspaceSymbolHandler{}
	resp, errResp := h.Handle(newWorkspaceSymbolMessage("   "), m)
	if errResp != nil {
		t.Fatalf("unexpected error response: %v", errResp.Error)
	}
	var symbols []SymbolInformation
	if err := json.Unmarshal(resp.Result, &symbols); err != nil {
		t.Fatalf("bad result json: %v", err)
	}
	if len(symbols) != 0 {
		t.Fatalf("expected empty result for blank query, got %+v", symbols)
	}
	if got := countSymbolSearchRequests(m); got != 0 {
		t.Fatalf("blank query must not hit the AL LS, got %d requests", got)
	}
	m.mu.Lock()
	warnings := len(m.clientNotifications)
	m.mu.Unlock()
	if warnings != 1 {
		t.Fatalf("expected exactly 1 client warning, got %d", warnings)
	}
}
