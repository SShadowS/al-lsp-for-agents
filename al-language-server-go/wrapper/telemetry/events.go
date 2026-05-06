package telemetry

import "runtime"

// ConsentLevel is the active opt-out level.
type ConsentLevel string

const (
	LevelOff    ConsentLevel = "off"
	LevelErrors ConsentLevel = "errors"
	LevelFull   ConsentLevel = "full"
)

// WrapperVersion and ALExtensionVersion are populated at build time / on
// init by the wrapper. Default values keep tests deterministic.
var (
	WrapperVersion     = "dev"
	ALExtensionVersion = "unknown"
)

// Frame is a single stack frame captured via runtime.Callers.
type Frame struct {
	Function string `json:"function"`
	Line     int    `json:"line"`
}

// Envelope is the common shape carried by every emitted event.
type Envelope struct {
	SchemaVersion      int          `json:"schemaVersion"`
	Name               string       `json:"name"`
	WrapperVersion     string       `json:"wrapperVersion"`
	ALExtensionVersion string       `json:"alExtensionVersion"`
	OS                 string       `json:"os"`
	Arch               string       `json:"arch"`
	Launcher           string       `json:"launcher,omitempty"`
	SessionID          string       `json:"sessionId"`
	ConsentLevel       ConsentLevel `json:"consentLevel"`
}

// NewEnvelope creates the common shell. Callers add per-event fields.
func NewEnvelope(s *Session, name string, level ConsentLevel) Envelope {
	return Envelope{
		SchemaVersion:      SchemaVersion,
		Name:               name,
		WrapperVersion:     WrapperVersion,
		ALExtensionVersion: ALExtensionVersion,
		OS:                 runtime.GOOS,
		Arch:               runtime.GOARCH,
		SessionID:          s.ID,
		ConsentLevel:       level,
	}
}

// PanicEvent (A) — wrapper panic recovered.
type PanicEvent struct {
	Envelope
	ExceptionType    string  `json:"exceptionType"`
	MessageSignature string  `json:"messageSignature"`
	StackFrames      []Frame `json:"stackFrames"`
	Site             string  `json:"site,omitempty"`
}

func BuildPanicEvent(s *Session, level ConsentLevel, panicMsg string, frames []Frame) PanicEvent {
	site := ""
	if len(frames) > 0 {
		site = frames[0].Function
	}
	return PanicEvent{
		Envelope:         NewEnvelope(s, "wrapper.panic", level),
		ExceptionType:    "recovered_panic",
		MessageSignature: ClassifyPanic(panicMsg),
		StackFrames:      frames,
		Site:             site,
	}
}

// ALLSFailureEvent (B) — inner AL LS crash/hang/timeout.
type ALLSFailureEvent struct {
	Envelope
	Subtype         string `json:"subtype"`
	ExitCode        int    `json:"exitCode,omitempty"`
	StderrSignature string `json:"stderrSignature"`
}

func BuildALLSFailureEvent(s *Session, level ConsentLevel, subtype string, exitCode int, lastStderrLine string) ALLSFailureEvent {
	return ALLSFailureEvent{
		Envelope:        NewEnvelope(s, "al_ls.failure", level),
		Subtype:         subtype,
		ExitCode:        exitCode,
		StderrSignature: ClassifyStderr(lastStderrLine),
	}
}

// LSPRequestErrorEvent (C, opt-in only).
type LSPRequestErrorEvent struct {
	Envelope
	Method     string `json:"method"`
	ErrorCode  int    `json:"errorCode"`
	ErrorClass string `json:"errorClass"`
	DurationMs int    `json:"durationMs"`
}

func BuildLSPRequestErrorEvent(s *Session, level ConsentLevel, method string, code int, msg string, durationMs int) LSPRequestErrorEvent {
	return LSPRequestErrorEvent{
		Envelope:   NewEnvelope(s, "lsp.request_error", level),
		Method:     method,
		ErrorCode:  code,
		ErrorClass: ClassifyLSPError(code, msg),
		DurationMs: durationMs,
	}
}

// LSPCapabilityGapEvent (D, opt-in only).
type LSPCapabilityGapEvent struct {
	Envelope
	Method string `json:"method"`
	Reason string `json:"reason"`
}

func BuildLSPCapabilityGapEvent(s *Session, level ConsentLevel, method, reason string) LSPCapabilityGapEvent {
	return LSPCapabilityGapEvent{
		Envelope: NewEnvelope(s, "lsp.capability_gap", level),
		Method:   method,
		Reason:   reason,
	}
}

// MSBugEvent (E).
type MSBugEvent struct {
	Envelope
	BugID     string `json:"bugId"`
	IssueURL  string `json:"issueUrl,omitempty"`
	PatternID string `json:"patternId"`
}

func BuildMSBugEvent(s *Session, level ConsentLevel, bug *MSBugPattern, patternID string) MSBugEvent {
	return MSBugEvent{
		Envelope:  NewEnvelope(s, "ms_bug.fingerprint", level),
		BugID:     bug.ID,
		IssueURL:  bug.IssueURL,
		PatternID: patternID,
	}
}

// PerfOutlierEvent (F, opt-in only).
type PerfOutlierEvent struct {
	Envelope
	Method          string `json:"method"`
	DurationMs      int    `json:"durationMs"`
	ThresholdBucket string `json:"thresholdBucket"`
}

func BuildPerfOutlierEvent(s *Session, level ConsentLevel, method string, durationMs int) PerfOutlierEvent {
	return PerfOutlierEvent{
		Envelope:        NewEnvelope(s, "perf.outlier", level),
		Method:          method,
		DurationMs:      durationMs,
		ThresholdBucket: ClassifyPerfBucket(durationMs),
	}
}

// DownloadFailureEvent (G).
type DownloadFailureEvent struct {
	Envelope
	Stage      string `json:"stage"`
	ErrorClass string `json:"errorClass"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
	URLHost    string `json:"urlHost,omitempty"`
}

func BuildDownloadFailureEvent(s *Session, level ConsentLevel, stage, errMsg string, httpStatus int, urlHost string) DownloadFailureEvent {
	return DownloadFailureEvent{
		Envelope:   NewEnvelope(s, "download.failure", level),
		Stage:      stage,
		ErrorClass: ClassifyDownloadError(errMsg),
		HTTPStatus: httpStatus,
		URLHost:    urlHost,
	}
}

// ConfigErrorEvent (H, opt-in only).
type ConfigErrorEvent struct {
	Envelope
	Subsystem string `json:"subsystem"`
	ErrorCode string `json:"errorCode"`
}

func BuildConfigErrorEvent(s *Session, level ConsentLevel, subsystem, errorCode string) ConfigErrorEvent {
	return ConfigErrorEvent{
		Envelope:  NewEnvelope(s, "config.error", level),
		Subsystem: subsystem,
		ErrorCode: errorCode,
	}
}

// maxFrames caps the captured stack length to limit envelope size.
const maxFrames = 32

// CaptureFrames returns up to maxFrames stack frames starting `skip` frames
// above the caller. Uses runtime.Callers + FuncForPC (function name + line
// only). Never uses debug.Stack(); never includes argument values.
func CaptureFrames(skip int) []Frame {
	pcs := make([]uintptr, maxFrames)
	n := runtime.Callers(skip+2, pcs)
	if n == 0 {
		return nil
	}
	frames := runtime.CallersFrames(pcs[:n])
	out := make([]Frame, 0, n)
	for {
		f, more := frames.Next()
		out = append(out, Frame{Function: f.Function, Line: f.Line})
		if !more {
			break
		}
	}
	return out
}
