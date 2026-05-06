package telemetry

import (
	"encoding/hex"
	"testing"
)

func TestNewSessionGeneratesUUIDAndSalt(t *testing.T) {
	s := NewSession()
	if len(s.ID) != 36 {
		t.Fatalf("expected UUID v4 string (36 chars), got %d chars: %q", len(s.ID), s.ID)
	}
	if len(s.Salt) != 32 {
		t.Fatalf("expected 32-byte salt, got %d bytes", len(s.Salt))
	}
	allZero := true
	for _, b := range s.Salt {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatalf("salt is all zeros, RNG failed")
	}
	_ = hex.EncodeToString(s.Salt)
}

func TestSessionsAreDistinct(t *testing.T) {
	a := NewSession()
	b := NewSession()
	if a.ID == b.ID {
		t.Fatalf("two sessions produced the same ID: %s", a.ID)
	}
	if string(a.Salt) == string(b.Salt) {
		t.Fatalf("two sessions produced the same salt")
	}
}
