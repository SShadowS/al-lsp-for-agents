package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
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

const dedupSaveCap = 256

// Load reads the dedup state from path. Missing or corrupt → fail open
// (returns nil, leaves state empty).
func (d *Dedup) Load(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]int64
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for fp, t := range m {
		d.entries[fp] = &dedupEntry{
			firstSeenAt: time.Unix(t, 0),
			lastSentAt:  time.Unix(t, 0),
		}
	}
	return nil
}

// Save writes the most recent dedupSaveCap entries to path. Used at
// shutdown and on debounced timer.
func (d *Dedup) Save(path string) error {
	d.mu.Lock()
	type kv struct {
		fp string
		t  int64
	}
	items := make([]kv, 0, len(d.entries))
	for fp, e := range d.entries {
		items = append(items, kv{fp, e.lastSentAt.Unix()})
	}
	d.mu.Unlock()
	if len(items) > dedupSaveCap {
		for i := 0; i < dedupSaveCap; i++ {
			best := i
			for j := i + 1; j < len(items); j++ {
				if items[j].t > items[best].t {
					best = j
				}
			}
			items[i], items[best] = items[best], items[i]
		}
		items = items[:dedupSaveCap]
	}
	out := make(map[string]int64, len(items))
	for _, kv := range items {
		out[kv.fp] = kv.t
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
