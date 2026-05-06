package telemetry

import "strings"

// ClassifyPanic maps a recovered panic value's string form to a closed
// enum bucket. Never echoes the input.
func ClassifyPanic(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "nil pointer dereference"), strings.Contains(s, "invalid memory address"):
		return "nil-deref"
	case strings.Contains(s, "send on closed channel"), strings.Contains(s, "close of closed channel"):
		return "channel-closed"
	case strings.Contains(s, "index out of range"):
		return "index-out-of-range"
	case strings.Contains(s, "divide by zero"):
		return "divide-by-zero"
	case strings.Contains(s, "slice bounds out of range"):
		return "slice-bounds"
	default:
		return "runtime-error-other"
	}
}

// ClassifyStderr maps an AL LS stderr line to a closed enum.
func ClassifyStderr(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "panic"), strings.Contains(s, "nullref"):
		return "panic"
	case strings.Contains(s, "already declared"):
		return "already-declared"
	case strings.Contains(s, "is not a file uri"), strings.Contains(s, "malformed uri"):
		return "uri-malformed"
	case strings.Contains(s, "out of memory"):
		return "oom"
	default:
		return "other"
	}
}

// ClassifyLSPError maps a JSON-RPC error to a closed enum.
func ClassifyLSPError(code int, msg string) string {
	msgL := strings.ToLower(msg)
	switch {
	case strings.Contains(msgL, "timed out"), strings.Contains(msgL, "deadline"):
		return "timeout"
	case code == -32602:
		return "invalid-params"
	case code == -32601:
		return "not-found"
	case code <= -32000 && code >= -32099:
		return "server-error"
	default:
		return "other"
	}
}

// ClassifyDownloadError maps a download/network error message to a closed
// enum.
func ClassifyDownloadError(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "no such host"), strings.Contains(s, "dns"):
		return "dns"
	case strings.Contains(s, "x509"), strings.Contains(s, "certificate"):
		return "tls"
	case strings.Contains(s, "deadline exceeded"), strings.Contains(s, "timed out"):
		return "timeout"
	case strings.Contains(s, "status"):
		return "http-status"
	case strings.Contains(s, "checksum"):
		return "checksum"
	case strings.Contains(s, "no space left"):
		return "disk"
	default:
		return "other"
	}
}

// ClassifyPerfBucket buckets a duration in milliseconds into the spec's
// fixed thresholdBucket values.
func ClassifyPerfBucket(durationMs int) string {
	switch {
	case durationMs < 5000:
		return "<5s"
	case durationMs < 10000:
		return "5s"
	case durationMs < 30000:
		return "10s"
	case durationMs < 60000:
		return "30s"
	default:
		return "60s+"
	}
}
