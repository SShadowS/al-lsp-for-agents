package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestNoOpClientWhenConnStringEmpty(t *testing.T) {
	c, err := NewClient(ClientConfig{ConnString: "", DumpPath: "", Level: LevelErrors})
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled() {
		t.Errorf("client must be no-op with empty conn string")
	}
	c.TrackPanic(nil, "x", nil)
}

func TestDumpModeWritesEnvelopesToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dump.jsonl")
	c, err := NewClient(ClientConfig{ConnString: "InstrumentationKey=fake", DumpPath: path, Level: LevelErrors})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSession()
	c.TrackPanic(s, "runtime error: invalid memory address", []Frame{{Function: "f", Line: 1}})
	c.WaitDrain(2 * time.Second)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ev PanicEvent
	if err := json.Unmarshal(raw[:len(raw)-1], &ev); err != nil {
		t.Fatalf("unmarshal: %v; raw=%s", err, raw)
	}
	if ev.MessageSignature != "nil-deref" {
		t.Errorf("messageSignature = %q", ev.MessageSignature)
	}
}

func TestPostsToIngestionEndpoint(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c, err := NewClient(ClientConfig{
		ConnString: "IngestionEndpoint=" + srv.URL + ";InstrumentationKey=fake",
		Level:      LevelErrors,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSession()
	c.TrackPanic(s, "boom", nil)
	c.WaitDrain(2 * time.Second)
	if atomic.LoadInt32(&calls) == 0 {
		t.Errorf("expected POST to ingestion endpoint")
	}
}

func TestSyncSendBlocksUntilPosted(t *testing.T) {
	var got int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.StoreInt32(&got, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c, err := NewClient(ClientConfig{
		ConnString: "IngestionEndpoint=" + srv.URL + ";InstrumentationKey=fake",
		Level:      LevelErrors,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSession()
	c.TrackPanicSync(s, "boom", nil, 1*time.Second)
	if atomic.LoadInt32(&got) != 1 {
		t.Errorf("sync send did not actually POST before returning")
	}
}

func TestRespectsConsentLevel(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c, err := NewClient(ClientConfig{
		ConnString: "IngestionEndpoint=" + srv.URL + ";InstrumentationKey=fake",
		Level:      LevelErrors,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSession()
	c.TrackPerfOutlier(s, "textDocument/hover", 7000)
	c.WaitDrain(500 * time.Millisecond)
	if atomic.LoadInt32(&calls) != 0 {
		t.Errorf("perf.outlier sent at LevelErrors")
	}
}
