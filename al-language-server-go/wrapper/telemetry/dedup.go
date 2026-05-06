package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

const (
	dedupWindow = 60 * time.Second
	dedupMax    = 1000
)

// Fingerprint returns the stable fingerprint for an event.
func Fingerprint(eventName, joinedInputs string) string {
	h := sha256.Sum256([]byte(eventName + "|" + joinedInputs))
	return hex.EncodeToString(h[:])[:16]
}

type dedupEntry struct {
	firstSeenAt     time.Time
	lastSentAt      time.Time
	suppressedCount int
}

// Dedup tracks fingerprints in-memory for one process.
type Dedup struct {
	mu      sync.Mutex
	entries map[string]*dedupEntry
	now     func() time.Time
}

// NewDedup creates an empty in-memory dedup map.
func NewDedup() *Dedup {
	return &Dedup{
		entries: make(map[string]*dedupEntry),
		now:     time.Now,
	}
}

// ShouldSend returns true when the fingerprint is sendable now.
func (d *Dedup) ShouldSend(fp string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	e, ok := d.entries[fp]
	if !ok {
		if len(d.entries) >= dedupMax {
			d.evictOldestLocked(now)
		}
		d.entries[fp] = &dedupEntry{firstSeenAt: now, lastSentAt: now}
		return true
	}
	if now.Sub(e.lastSentAt) >= dedupWindow {
		e.lastSentAt = now
		e.suppressedCount = 0
		return true
	}
	e.suppressedCount++
	return false
}

// SuppressedSince returns the count suppressed since the last send.
func (d *Dedup) SuppressedSince(fp string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if e, ok := d.entries[fp]; ok {
		return e.suppressedCount
	}
	return 0
}

func (d *Dedup) evictOldestLocked(_ time.Time) {
	var oldest string
	var oldestT time.Time
	first := true
	for k, v := range d.entries {
		if first || v.firstSeenAt.Before(oldestT) {
			oldest = k
			oldestT = v.firstSeenAt
			first = false
		}
	}
	delete(d.entries, oldest)
}
