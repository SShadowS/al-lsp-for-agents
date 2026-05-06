package telemetry

import "sync/atomic"

var (
	globalClient  atomic.Pointer[Client]
	globalSession atomic.Pointer[Session]
)

// SetGlobal registers the process-wide client + session for use by code
// that doesn't have direct wrapper access (paths.go, lockfile.go, etc).
// Safe to call once at wrapper init; subsequent calls overwrite.
func SetGlobal(c *Client, s *Session) {
	globalClient.Store(c)
	globalSession.Store(s)
}

// TrackGlobalConfigError emits a config.error event using the global
// client. No-op if the global wasn't set.
func TrackGlobalConfigError(subsystem, errorCode string) {
	c := globalClient.Load()
	s := globalSession.Load()
	if c == nil || s == nil {
		return
	}
	c.TrackConfigError(s, subsystem, errorCode)
}
