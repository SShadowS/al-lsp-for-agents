package telemetry

import "testing"

func TestClassifyPanic(t *testing.T) {
	cases := []struct{ in, want string }{
		{"runtime error: invalid memory address or nil pointer dereference", "nil-deref"},
		{"send on closed channel", "channel-closed"},
		{"runtime error: index out of range [3] with length 2", "index-out-of-range"},
		{"runtime error: integer divide by zero", "divide-by-zero"},
		{"some unknown panic message", "runtime-error-other"},
		{"", "runtime-error-other"},
	}
	for _, c := range cases {
		if got := ClassifyPanic(c.in); got != c.want {
			t.Errorf("ClassifyPanic(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClassifyStderr(t *testing.T) {
	cases := []struct{ in, want string }{
		{"AL_LS panic: nullref at line 12", "panic"},
		{"workspace already declared", "already-declared"},
		{`path uri 'C:\Foo' is not a file uri`, "uri-malformed"},
		{"random unrecognized output", "other"},
		{"", "other"},
	}
	for _, c := range cases {
		if got := ClassifyStderr(c.in); got != c.want {
			t.Errorf("ClassifyStderr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClassifyLSPError(t *testing.T) {
	cases := []struct {
		code int
		msg  string
		want string
	}{
		{-32603, "operation timed out", "timeout"},
		{-32602, "invalid params", "invalid-params"},
		{-32601, "method not found", "not-found"},
		{-32000, "server error", "server-error"},
		{0, "anything", "other"},
	}
	for _, c := range cases {
		if got := ClassifyLSPError(c.code, c.msg); got != c.want {
			t.Errorf("ClassifyLSPError(%d, %q) = %q, want %q", c.code, c.msg, got, c.want)
		}
	}
}

func TestClassifyDownloadError(t *testing.T) {
	cases := []struct{ in, want string }{
		{"dial tcp: lookup marketplace.visualstudio.com: no such host", "dns"},
		{"x509: certificate has expired", "tls"},
		{"context deadline exceeded", "timeout"},
		{"unexpected status 404", "http-status"},
		{"checksum mismatch", "checksum"},
		{"no space left on device", "disk"},
		{"weird thing", "other"},
	}
	for _, c := range cases {
		if got := ClassifyDownloadError(c.in); got != c.want {
			t.Errorf("ClassifyDownloadError(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClassifierOutputsAreClosed(t *testing.T) {
	weirdInputs := []string{
		`C:\Users\Bob\secret\path.al with embedded /home/alice text`,
		"<script>alert(1)</script>",
		"GUID 11111111-2222-3333-4444-555555555555",
	}
	for _, in := range weirdInputs {
		out := ClassifyPanic(in)
		if out == in {
			t.Errorf("ClassifyPanic echoed input: %q", in)
		}
		out = ClassifyStderr(in)
		if out == in {
			t.Errorf("ClassifyStderr echoed input: %q", in)
		}
	}
}
