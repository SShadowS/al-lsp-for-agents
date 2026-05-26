package wrapper

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync/atomic"
)

// looksLikeMalformedFileURI returns true for URI strings the wrapper has
// observed coming from the Microsoft AL Language Server (.NET) when a path
// was canonicalized through Windows' extended-length form (\\?\).
//
// The two shapes we rewrite:
//   - "file://%3F\C:\..."  — hybrid emitted by .NET System.Uri on \\?\C:\...
//   - "\\?\C:\..."          — bare extended path passed where a URI was expected
func looksLikeMalformedFileURI(s string) bool {
	return strings.HasPrefix(s, "file://%3F") || strings.HasPrefix(s, `\\?\`)
}

// uriSanitizerWalk holds the in-flight state of a single sanitation pass.
// It captures the first (original, normalized) pair so callers can log a
// representative sample without re-walking the tree.
type uriSanitizerWalk struct {
	count      int
	sampleOrig string
	sampleNorm string
}

func (w *uriSanitizerWalk) recordSample(original, normalized string) {
	if w.sampleOrig == "" {
		w.sampleOrig = original
		w.sampleNorm = normalized
	}
}

// sanitizeURIsInTree walks a decoded JSON tree, rewriting malformed file URIs.
// Map keys are also rewritten (WorkspaceEdit.changes is keyed by URI). Returns
// the (possibly replaced) value.
//
// The walk is field-agnostic: it operates on any string value that matches
// the malformed signature, no matter where in the message it appears. This
// avoids having to enumerate every LSP field that carries a URI and stays
// correct as the protocol grows.
func sanitizeURIsInTree(v interface{}, w *uriSanitizerWalk) interface{} {
	switch x := v.(type) {
	case map[string]interface{}:
		var rebuiltKeys map[string]string
		for k, val := range x {
			// Always write back: for maps/slices the recursive call mutates
			// in place and returns the same reference; for strings the cost
			// is one assignment. A `newVal != val` check would panic on
			// uncomparable map/slice values.
			x[k] = sanitizeURIsInTree(val, w)
			if looksLikeMalformedFileURI(k) {
				normalized := NormalizeFileURI(k)
				if rebuiltKeys == nil {
					rebuiltKeys = make(map[string]string)
				}
				rebuiltKeys[k] = normalized
				w.count++
				w.recordSample(k, normalized)
			}
		}
		// Apply key renames after iteration to avoid mutating during range.
		for oldKey, newKey := range rebuiltKeys {
			x[newKey] = x[oldKey]
			delete(x, oldKey)
		}
		return x
	case []interface{}:
		for i, val := range x {
			x[i] = sanitizeURIsInTree(val, w)
		}
		return x
	case string:
		if looksLikeMalformedFileURI(x) {
			normalized := NormalizeFileURI(x)
			w.count++
			w.recordSample(x, normalized)
			return normalized
		}
		return x
	default:
		return v
	}
}

// sanitizeRawMessage rewrites malformed file URIs in a JSON-RPC params or
// result payload. Returns the (possibly unchanged) raw bytes and the walk
// state with count + first sample. Errors leave the original bytes intact
// — the caller is expected to forward the original so a sanitizer bug never
// blocks message flow.
//
// Fast path: messages without "%3F" or escaped "\\?\" in their bytes cannot
// contain a malformed URI we care about. We short-circuit there to avoid
// the cost of a full JSON unmarshal on the steady-state happy path.
func sanitizeRawMessage(raw json.RawMessage) (json.RawMessage, uriSanitizerWalk, error) {
	var walk uriSanitizerWalk
	if len(raw) == 0 {
		return raw, walk, nil
	}
	// `\\?\` inside a JSON string literal is encoded as `\\\\?\\` (each
	// backslash doubled). Both shapes are cheap to byte-scan for.
	if !bytes.Contains(raw, []byte("%3F")) && !bytes.Contains(raw, []byte(`\\\\?\\`)) {
		return raw, walk, nil
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw, walk, err
	}
	newV := sanitizeURIsInTree(v, &walk)
	if walk.count == 0 {
		return raw, walk, nil
	}
	out, err := json.Marshal(newV)
	if err != nil {
		return raw, walk, err
	}
	return out, walk, nil
}

// SanitizeOutboundMessage rewrites malformed file URIs in a message's Params
// and Result fields in place. Returns total count and a representative
// (original, normalized) sample from the rewrites. Errors during sanitation
// are swallowed: the original bytes survive and the caller can still ship
// the (possibly broken) message rather than dropping it.
func SanitizeOutboundMessage(msg *Message) (count int, sampleOrig, sampleNorm string) {
	if msg == nil {
		return 0, "", ""
	}
	if len(msg.Params) > 0 {
		if newRaw, walk, err := sanitizeRawMessage(msg.Params); err == nil && walk.count > 0 {
			msg.Params = newRaw
			count += walk.count
			if sampleOrig == "" {
				sampleOrig, sampleNorm = walk.sampleOrig, walk.sampleNorm
			}
		}
	}
	if len(msg.Result) > 0 {
		if newRaw, walk, err := sanitizeRawMessage(msg.Result); err == nil && walk.count > 0 {
			msg.Result = newRaw
			count += walk.count
			if sampleOrig == "" {
				sampleOrig, sampleNorm = walk.sampleOrig, walk.sampleNorm
			}
		}
	}
	return count, sampleOrig, sampleNorm
}

// uriSanitizationStats tracks how often the wrapper has had to fix a
// malformed URI emitted by an upstream component. Logs the first hit loudly
// (so upstream bugs are visible — see CLAUDE.md "no silent workarounds"),
// then samples to avoid log spam, then totals at shutdown.
type uriSanitizationStats struct {
	total uint64
}

// record updates the counter and emits a log line when appropriate.
// direction: e.g. "AL-LS->client" or "call-hierarchy->client".
// method:    the LSP method on the message, for triage.
// count:     number of rewrites in this single message.
// sample:    one (original, normalized) pair from this message, for logging.
func (s *uriSanitizationStats) record(
	logFn func(format string, args ...interface{}),
	direction, method string,
	count int,
	originalSample, normalizedSample string,
) {
	prev := atomic.AddUint64(&s.total, uint64(count)) - uint64(count)
	after := prev + uint64(count)
	if prev == 0 {
		logFn("[URI-FIX] Detected malformed Windows extended-path URI from upstream (%s %s); normalized %d in this message. Future occurrences sampled. original=%q normalized=%q",
			direction, method, count, originalSample, normalizedSample)
		return
	}
	// Log every 100th occurrence at the cumulative-total granularity.
	if after/100 != prev/100 {
		logFn("[URI-FIX] sanitized %d more (total %d) in %s %s; sample original=%q normalized=%q",
			count, after, direction, method, originalSample, normalizedSample)
	}
}

// Total returns the total number of URI rewrites observed in the session.
func (s *uriSanitizationStats) Total() uint64 {
	return atomic.LoadUint64(&s.total)
}
