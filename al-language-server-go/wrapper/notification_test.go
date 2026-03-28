package wrapper

import (
	"bytes"
	"encoding/json"
	"testing"
)

// nopWriteCloser wraps a bytes.Buffer to satisfy io.WriteCloser for tests.
type nopWriteCloser struct {
	bytes.Buffer
}

func (nopWriteCloser) Close() error { return nil }

// newTestWrapper creates a minimal ALLSPWrapper suitable for calling
// handleMessage without real sub-processes. stdin is a buffer (captures
// forwarded notifications), callHierarchyServer is nil, logging is off.
func newTestWrapper() (*ALLSPWrapper, *nopWriteCloser) {
	w := New()
	buf := &nopWriteCloser{}
	w.stdin = buf
	return w, buf
}

func didOpenMessage(uri string) *Message {
	params, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        uri,
			"languageId": "al",
			"version":    1,
			"text":       "// empty",
		},
	})
	return &Message{
		JSONRPC: "2.0",
		Method:  "textDocument/didOpen",
		Params:  params,
	}
}

func didCloseMessage(uri string) *Message {
	params, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": uri,
		},
	})
	return &Message{
		JSONRPC: "2.0",
		Method:  "textDocument/didClose",
		Params:  params,
	}
}

func didChangeMessage(uri string) *Message {
	params, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":     uri,
			"version": 2,
		},
		"contentChanges": []map[string]interface{}{
			{"text": "// changed"},
		},
	})
	return &Message{
		JSONRPC: "2.0",
		Method:  "textDocument/didChange",
		Params:  params,
	}
}

// ---------------------------------------------------------------------------
// Tests for file tracking in handleMessage (issue #17 fix)
// ---------------------------------------------------------------------------

func TestHandleMessage_DidOpen_TracksFile(t *testing.T) {
	w, _ := newTestWrapper()

	uri := "file:///c%3A/projects/test/Customer.Table.al"
	w.handleMessage(didOpenMessage(uri))

	expected := NormalizePath("c:/projects/test/Customer.Table.al")
	if !w.openedFiles[expected] {
		t.Errorf("didOpen should track file in openedFiles\n  expected key: %s\n  openedFiles: %v", expected, w.openedFiles)
	}
}

func TestHandleMessage_DidClose_UntracksFile(t *testing.T) {
	w, _ := newTestWrapper()

	uri := "file:///c%3A/projects/test/Customer.Table.al"
	normalized := NormalizePath("c:/projects/test/Customer.Table.al")

	// Pre-populate as opened
	w.openedFiles[normalized] = true

	w.handleMessage(didCloseMessage(uri))

	if w.openedFiles[normalized] {
		t.Errorf("didClose should remove file from openedFiles\n  key: %s\n  openedFiles: %v", normalized, w.openedFiles)
	}
}

func TestHandleMessage_DidOpen_PreventsEnsureDuplicate(t *testing.T) {
	// This is the core issue #17 scenario:
	// 1. VS Code sends didOpen (via LanguageClient) -> wrapper forwards to AL LSP
	// 2. Tool handler calls EnsureFileOpened -> should NOT send a second didOpen
	w, buf := newTestWrapper()

	uri := "file:///c%3A/projects/test/Customer.Table.al"
	w.handleMessage(didOpenMessage(uri))

	// Record how much was written to stdin (the forwarded didOpen)
	afterFirstOpen := buf.Len()
	if afterFirstOpen == 0 {
		t.Fatal("didOpen should have been forwarded to AL LSP (written to stdin)")
	}

	// Now EnsureFileOpened should see the file is already tracked and skip
	normalized := NormalizePath("c:/projects/test/Customer.Table.al")
	if !w.openedFiles[normalized] {
		t.Fatal("openedFiles should contain the file after didOpen")
	}

	// EnsureFileOpened checks openedFiles and returns early if true.
	// We can't call EnsureFileOpened directly (it reads from disk),
	// but we verify the guard condition that prevents the duplicate.
}

func TestHandleMessage_DidChange_DoesNotTrack(t *testing.T) {
	// didChange should NOT add to openedFiles — only didOpen does that
	w, _ := newTestWrapper()

	uri := "file:///c%3A/projects/test/Customer.Table.al"
	w.handleMessage(didChangeMessage(uri))

	normalized := NormalizePath("c:/projects/test/Customer.Table.al")
	if w.openedFiles[normalized] {
		t.Error("didChange should not add file to openedFiles")
	}
}

func TestHandleMessage_DidOpenClose_Cycle(t *testing.T) {
	w, _ := newTestWrapper()

	uri := "file:///c%3A/projects/test/Customer.Table.al"
	normalized := NormalizePath("c:/projects/test/Customer.Table.al")

	// Open
	w.handleMessage(didOpenMessage(uri))
	if !w.openedFiles[normalized] {
		t.Fatal("file should be tracked after didOpen")
	}

	// Close
	w.handleMessage(didCloseMessage(uri))
	if w.openedFiles[normalized] {
		t.Fatal("file should be untracked after didClose")
	}

	// Reopen
	w.handleMessage(didOpenMessage(uri))
	if !w.openedFiles[normalized] {
		t.Fatal("file should be tracked again after second didOpen")
	}
}

// ---------------------------------------------------------------------------
// Tests for notification forwarding
// ---------------------------------------------------------------------------

func TestHandleMessage_NotificationForwardedToALLSP(t *testing.T) {
	w, buf := newTestWrapper()

	uri := "file:///c%3A/projects/test/Customer.Table.al"
	w.handleMessage(didOpenMessage(uri))

	if buf.Len() == 0 {
		t.Error("didOpen notification should be forwarded to AL LSP via stdin")
	}
}

func TestHandleMessage_NonDocumentNotification_NoTracking(t *testing.T) {
	w, _ := newTestWrapper()

	// A random notification should not affect openedFiles
	params, _ := json.Marshal(map[string]interface{}{"settings": map[string]interface{}{}})
	msg := &Message{
		JSONRPC: "2.0",
		Method:  "workspace/didChangeConfiguration",
		Params:  params,
	}
	w.handleMessage(msg)

	if len(w.openedFiles) != 0 {
		t.Errorf("non-document notification should not affect openedFiles: %v", w.openedFiles)
	}
}

// ---------------------------------------------------------------------------
// Tests for extractTextDocumentURI
// ---------------------------------------------------------------------------

func TestExtractTextDocumentURI(t *testing.T) {
	tests := []struct {
		name     string
		params   string
		expected string
	}{
		{
			name:     "didOpen params",
			params:   `{"textDocument":{"uri":"file:///c%3A/test/foo.al","languageId":"al","version":1,"text":""}}`,
			expected: "file:///c%3A/test/foo.al",
		},
		{
			name:     "didClose params",
			params:   `{"textDocument":{"uri":"file:///c%3A/test/foo.al"}}`,
			expected: "file:///c%3A/test/foo.al",
		},
		{
			name:     "empty params",
			params:   `{}`,
			expected: "",
		},
		{
			name:     "missing textDocument",
			params:   `{"other":"value"}`,
			expected: "",
		},
		{
			name:     "invalid JSON",
			params:   `not json`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTextDocumentURI(json.RawMessage(tt.params))
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}
