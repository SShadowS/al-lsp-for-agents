package wrapper

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// notificationStats aggregates forwarded notifications instead of logging one
// line each.
//
// Motivation is concrete: in a real customer log (a 33-root workspace, issue
// "lsp uses huge amount of memory") 1848 of 2081 lines were a single repeated
// "Forwarding notification to client: textDocument/publishDiagnostics" line
// carrying no URI, no count, and no timing. It buried the 124 lines that
// actually explained anything, and still did not answer the question being
// asked. Aggregating keeps the same information — how many, of what, over
// what period — in a handful of lines.
//
// Follows the same first/every-Nth/session-total shape as
// [uriSanitizationStats] so log consumers see one idiom, not two.
type notificationStats struct {
	mu     sync.Mutex
	counts map[string]uint64
	total  uint64
}

func newNotificationStats() *notificationStats {
	return &notificationStats{counts: map[string]uint64{}}
}

// logEvery is the cumulative granularity at which a progress line is emitted.
// Chosen so a full workspace analysis (hundreds to low thousands of
// notifications) leaves a handful of breadcrumbs rather than a wall.
const notificationLogEvery = 500

// record counts one forwarded notification and returns true when the caller
// should emit a log line for it (the first of its kind, or every Nth overall).
func (s *notificationStats) record(method string) (shouldLog bool, total uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.total
	s.total++
	s.counts[method]++
	first := s.counts[method] == 1
	crossed := s.total/notificationLogEvery != prev/notificationLogEvery
	return first || crossed, s.total
}

// Summary renders the per-method tally, busiest first, for the session-end
// line. Empty string when nothing was forwarded, so the caller can skip the
// line entirely rather than log a zero.
func (s *notificationStats) Summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.total == 0 {
		return ""
	}
	type kv struct {
		method string
		n      uint64
	}
	pairs := make([]kv, 0, len(s.counts))
	for m, n := range s.counts {
		pairs = append(pairs, kv{m, n})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].n != pairs[j].n {
			return pairs[i].n > pairs[j].n
		}
		return pairs[i].method < pairs[j].method
	})
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf("%s=%d", p.method, p.n))
	}
	return strings.Join(parts, " ")
}
