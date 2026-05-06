package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// sessionIDRe masks the legitimate per-process session UUID before the
// ContainsLeak safety net runs. Per design (see PRIVACY.md, schemaVersion=1),
// the sessionId is intentionally transmitted as plaintext: it is a random
// UUID generated per process, never persisted, and not tied to user
// identity. The leak safety net's GUID arm exists to catch *unscrubbed*
// GUIDs leaking from user content (app IDs, file URIs); masking the
// known-safe sessionId field prevents false-positive event drops.
var sessionIDRe = regexp.MustCompile(`"sessionId"\s*:\s*"[^"]*"`)

// maskSessionID replaces the sessionId value in marshalled JSON with a
// placeholder before the ContainsLeak check. The field is kept so the JSON
// remains valid for other purposes.
func maskSessionID(s string) string {
	return sessionIDRe.ReplaceAllString(s, `"sessionId":"<sid>"`)
}

// ClientConfig configures a Client. Empty ConnString -> no-op client.
type ClientConfig struct {
	ConnString string
	DumpPath   string
	Level      ConsentLevel
	DedupPath  string
	Logf       func(format string, args ...interface{})
}

// Client is the goroutine-safe telemetry sink.
type Client struct {
	cfg          ClientConfig
	enabled      atomic.Bool
	endpoint     string
	instrumKey   string
	dump         *os.File
	dumpMu       sync.Mutex
	queue        chan envelope
	drainDone    chan struct{}
	closeOnce    sync.Once
	httpClient   *http.Client
	dedup        *Dedup
	perProcessMu sync.Mutex
	perProcessSent int
}

const (
	queueCap          = 100
	perProcessHardCap = 500
)

type envelope struct {
	name        string
	fingerprint string
	payload     interface{}
}

// NewClient returns a Client. Empty ConnString and empty DumpPath returns a
// non-enabled, no-op-safe Client.
func NewClient(cfg ClientConfig) (*Client, error) {
	c := &Client{
		cfg:        cfg,
		dedup:      NewDedup(),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	if cfg.ConnString == "" && cfg.DumpPath == "" {
		return c, nil
	}
	c.enabled.Store(true)
	c.endpoint, c.instrumKey = parseConnString(cfg.ConnString)
	if cfg.DumpPath != "" {
		f, err := os.OpenFile(cfg.DumpPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return nil, fmt.Errorf("open dump file: %w", err)
		}
		c.dump = f
	}
	if cfg.DedupPath != "" {
		_ = c.dedup.Load(cfg.DedupPath)
	}
	c.queue = make(chan envelope, queueCap)
	c.drainDone = make(chan struct{})
	go c.drain()
	return c, nil
}

// Enabled reports whether the client will actually send anything.
func (c *Client) Enabled() bool { return c != nil && c.enabled.Load() }

// WaitDrain blocks until the queue drains or timeout elapses.
// Safe to call multiple times (subsequent calls return immediately after first
// drain completes).
func (c *Client) WaitDrain(timeout time.Duration) {
	if c == nil || !c.enabled.Load() {
		return
	}
	c.closeOnce.Do(func() {
		close(c.queue)
	})
	select {
	case <-c.drainDone:
	case <-time.After(timeout):
	}
	c.dumpMu.Lock()
	if c.dump != nil {
		_ = c.dump.Close()
		c.dump = nil
	}
	c.dumpMu.Unlock()
	if c.cfg.DedupPath != "" {
		_ = c.dedup.Save(c.cfg.DedupPath)
	}
}

func parseConnString(s string) (endpoint, key string) {
	for _, part := range strings.Split(s, ";") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "IngestionEndpoint":
			endpoint = strings.TrimSuffix(kv[1], "/")
		case "InstrumentationKey":
			key = kv[1]
		}
	}
	if endpoint == "" {
		endpoint = "https://dc.services.visualstudio.com"
	}
	return
}

// drain runs the background sender goroutine.
func (c *Client) drain() {
	defer close(c.drainDone)
	for env := range c.queue {
		c.send(env)
	}
}

func (c *Client) send(env envelope) {
	raw, err := json.Marshal(env.payload)
	if err != nil {
		return
	}
	if ContainsLeak(maskSessionID(string(raw))) {
		c.logf("telemetry: leak safety net rejected event %s", env.name)
		return
	}
	c.dumpMu.Lock()
	if c.dump != nil {
		_, _ = c.dump.Write(raw)
		_, _ = c.dump.Write([]byte{'\n'})
		c.dumpMu.Unlock()
		return
	}
	c.dumpMu.Unlock()
	if c.endpoint == "" {
		return
	}
	url := c.endpoint + "/v2/track"
	req, err := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(raw))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if c.instrumKey != "" {
		req.Header.Set("X-Ms-Instrumentation-Key", c.instrumKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logf("telemetry: send error: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		c.logf("telemetry: 4xx %d, disabling", resp.StatusCode)
		c.enabled.Store(false)
	}
}

func (c *Client) logf(format string, args ...interface{}) {
	if c.cfg.Logf != nil {
		c.cfg.Logf(format, args...)
	}
}

// enqueue puts an envelope on the queue if consent + dedup permit.
func (c *Client) enqueue(name, fingerprint string, payload interface{}) {
	if c == nil || !c.enabled.Load() {
		return
	}
	if !EventAllowed(name, c.cfg.Level) {
		return
	}
	c.perProcessMu.Lock()
	over := c.perProcessSent >= perProcessHardCap
	c.perProcessMu.Unlock()
	if over && !isExceptionEvent(name) {
		return
	}
	if !c.dedup.ShouldSend(fingerprint) {
		return
	}
	c.perProcessMu.Lock()
	c.perProcessSent++
	c.perProcessMu.Unlock()
	select {
	case c.queue <- envelope{name: name, fingerprint: fingerprint, payload: payload}:
	default:
		c.logf("telemetry: queue full, dropping %s", name)
	}
}

func isExceptionEvent(name string) bool {
	switch name {
	case "wrapper.panic", "al_ls.failure", "download.failure":
		return true
	}
	return false
}

// TrackPanic enqueues a wrapper.panic event (async).
func (c *Client) TrackPanic(s *Session, panicMsg string, frames []Frame) {
	if c == nil || !c.enabled.Load() || s == nil {
		return
	}
	ev := BuildPanicEvent(s, c.cfg.Level, panicMsg, frames)
	fp := Fingerprint(ev.Name, ev.MessageSignature+"|"+ev.Site)
	c.enqueue(ev.Name, fp, ev)
}

// TrackPanicSync POSTs the panic event synchronously with timeout.
// Use only from a recover() handler about to exit the process.
func (c *Client) TrackPanicSync(s *Session, panicMsg string, frames []Frame, timeout time.Duration) {
	if c == nil || !c.enabled.Load() || s == nil {
		return
	}
	ev := BuildPanicEvent(s, c.cfg.Level, panicMsg, frames)
	raw, _ := json.Marshal(ev)
	if ContainsLeak(maskSessionID(string(raw))) {
		c.logf("telemetry: leak safety net rejected sync panic event")
		return
	}
	c.dumpMu.Lock()
	if c.dump != nil {
		_, _ = c.dump.Write(raw)
		_, _ = c.dump.Write([]byte{'\n'})
		c.dumpMu.Unlock()
		return
	}
	c.dumpMu.Unlock()
	if c.endpoint == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/v2/track", bytes.NewReader(raw))
	if err != nil {
		c.logf("telemetry: sync panic envelope (build req failed): %s", raw)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if c.instrumKey != "" {
		req.Header.Set("X-Ms-Instrumentation-Key", c.instrumKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logf("telemetry: sync panic POST failed: %v; envelope: %s", err, raw)
		return
	}
	resp.Body.Close()
}

// TrackALLSFailure enqueues an al_ls.failure event.
func (c *Client) TrackALLSFailure(s *Session, subtype string, exitCode int, lastStderrLine string) {
	if c == nil || !c.enabled.Load() || s == nil {
		return
	}
	ev := BuildALLSFailureEvent(s, c.cfg.Level, subtype, exitCode, lastStderrLine)
	fp := Fingerprint(ev.Name, ev.Subtype+"|"+fmt.Sprint(ev.ExitCode)+"|"+ev.StderrSignature)
	c.enqueue(ev.Name, fp, ev)
}

// TrackLSPRequestError enqueues a lsp.request_error event.
func (c *Client) TrackLSPRequestError(s *Session, method string, code int, msg string, durationMs int) {
	if c == nil || !c.enabled.Load() || s == nil {
		return
	}
	ev := BuildLSPRequestErrorEvent(s, c.cfg.Level, method, code, msg, durationMs)
	fp := Fingerprint(ev.Name, ev.Method+"|"+fmt.Sprint(ev.ErrorCode))
	c.enqueue(ev.Name, fp, ev)
}

// TrackCapabilityGap enqueues a lsp.capability_gap event.
func (c *Client) TrackCapabilityGap(s *Session, method, reason string) {
	if c == nil || !c.enabled.Load() || s == nil {
		return
	}
	ev := BuildLSPCapabilityGapEvent(s, c.cfg.Level, method, reason)
	fp := Fingerprint(ev.Name, ev.Method+"|"+ev.Reason)
	c.enqueue(ev.Name, fp, ev)
}

// TrackMSBug enqueues an ms_bug.fingerprint event.
func (c *Client) TrackMSBug(s *Session, bug *MSBugPattern, patternID string) {
	if c == nil || !c.enabled.Load() || s == nil || bug == nil {
		return
	}
	ev := BuildMSBugEvent(s, c.cfg.Level, bug, patternID)
	fp := Fingerprint(ev.Name, ev.BugID)
	c.enqueue(ev.Name, fp, ev)
}

// TrackPerfOutlier enqueues a perf.outlier event.
func (c *Client) TrackPerfOutlier(s *Session, method string, durationMs int) {
	if c == nil || !c.enabled.Load() || s == nil {
		return
	}
	ev := BuildPerfOutlierEvent(s, c.cfg.Level, method, durationMs)
	fp := Fingerprint(ev.Name, ev.Method+"|"+ev.ThresholdBucket)
	c.enqueue(ev.Name, fp, ev)
}

// TrackDownloadFailure enqueues a download.failure event.
func (c *Client) TrackDownloadFailure(s *Session, stage, errMsg string, httpStatus int, urlHost string) {
	if c == nil || !c.enabled.Load() || s == nil {
		return
	}
	ev := BuildDownloadFailureEvent(s, c.cfg.Level, stage, errMsg, httpStatus, urlHost)
	fp := Fingerprint(ev.Name, ev.Stage+"|"+fmt.Sprint(ev.HTTPStatus)+"|"+ev.ErrorClass)
	c.enqueue(ev.Name, fp, ev)
}

// TrackConfigError enqueues a config.error event.
func (c *Client) TrackConfigError(s *Session, subsystem, errorCode string) {
	if c == nil || !c.enabled.Load() || s == nil {
		return
	}
	ev := BuildConfigErrorEvent(s, c.cfg.Level, subsystem, errorCode)
	fp := Fingerprint(ev.Name, ev.Subsystem+"|"+ev.ErrorCode)
	c.enqueue(ev.Name, fp, ev)
}
