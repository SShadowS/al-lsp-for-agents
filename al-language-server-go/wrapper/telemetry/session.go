package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// SchemaVersion is the constant carried on every emitted envelope.
// Bump when fields/semantics change; update tests + dashboards together.
const SchemaVersion = 1

// Session is per-process state used by the telemetry pipeline.
// It is created at wrapper start and never persisted.
type Session struct {
	ID   string // RFC-4122 v4 UUID, used as sessionId in events
	Salt []byte // 32-byte HMAC key for symbol/GUID hashing; never sent
}

// NewSession returns a fresh per-process Session.
// Panics if the OS RNG fails (extremely unlikely; not recoverable).
func NewSession() *Session {
	return &Session{
		ID:   randomUUIDv4(),
		Salt: randomBytes(32),
	}
}

func randomBytes(n int) []byte {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("telemetry: RNG failed: %v", err))
	}
	return buf
}

func randomUUIDv4() string {
	b := randomBytes(16)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}
