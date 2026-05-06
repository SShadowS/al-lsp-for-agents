package telemetry

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// SourceMode tells Scrub which class of input it is processing. Callers MUST
// pick one explicitly; there is no default.
type SourceMode int

const (
	SourceALText SourceMode = iota
	SourceStderr
	SourceJSONRPC
	SourceURL
	SourcePath
)

// ScrubContext carries known substitutions for a single wrapper process.
// It is built once at telemetry init and passed by pointer.
type ScrubContext struct {
	HomeDir        string
	WorkspaceRoots []string
	TempDir        string
	Salt           []byte
}

const maxScrubLen = 1024

// guidRe matches RFC-4122 style GUIDs (case-insensitive, hyphenated).
var guidRe = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

// objectIDRe matches "Codeunit 50104", "Page 123", etc.
var objectIDRe = regexp.MustCompile(`\b(Codeunit|Page|Table|Report|XmlPort|Query|Enum|Interface)\s+(\d+)\b`)

// alIdentifierRe matches dotted AL identifiers starting with an uppercase letter.
var alIdentifierRe = regexp.MustCompile(`\b[A-Z][A-Za-z0-9_]*(\.[A-Z_][A-Za-z0-9_]*)*\b`)

// alObjectKeywords is the set of AL object-type keywords that appear as part of
// object declarations (e.g. "Codeunit 1"). These are preserved verbatim so that
// objectIDRe substitutions for MS-range objects remain intact.
var alObjectKeywords = map[string]bool{
	"Codeunit":  true,
	"Page":      true,
	"Table":     true,
	"Report":    true,
	"XmlPort":   true,
	"Query":     true,
	"Enum":      true,
	"Interface": true,
}

// urlAllowlist is the set of hosts whose host portion may be preserved.
var urlAllowlist = map[string]bool{
	"marketplace.visualstudio.com":  true,
	"github.com":                    true,
	"objects.githubusercontent.com": true,
}

// leakRe is the runtime safety-net regex applied AFTER full scrub. Anything
// matching here means the pipeline failed; the caller MUST drop the event.
//
// Pattern notes (Go RE2, raw strings):
//   - [\\][\\]Users  — matches \\Users (double backslash + Users, UNC-style paths)
//   - [\\]+Users     — matches \Users (single backslash, Windows roaming profiles)
//   - /Users/        — Unix macOS home prefix
//   - /home/         — Unix Linux home prefix
//   - [A-Z]:[\\]     — Windows drive-letter path (C:\...)
//   - GUID           — raw GUID not replaced by scrub (8hex-4hex prefix)
var leakRe = regexp.MustCompile(`[\\]+Users|/Users/[^/<]|/home/[^/<]|[A-Z]:[\\][A-Za-z]|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}`)

// Scrub runs the source-mode-aware pipeline on input.
func Scrub(input string, mode SourceMode, ctx *ScrubContext) string {
	if input == "" {
		return ""
	}
	s := stripControl(input)
	s = applyCommonRules(s, ctx)
	switch mode {
	case SourceALText:
		s = scrubALText(s, ctx)
	case SourceStderr:
		s = scrubStderr(s, ctx)
	case SourceJSONRPC:
		s = scrubJSONRPC(s, ctx)
	case SourceURL:
		s = scrubURL(s, ctx)
	case SourcePath:
		s = scrubPath(s, ctx)
	default:
		s = "<unknown-mode>"
	}
	if len(s) > maxScrubLen {
		s = s[:maxScrubLen] + "..."
	}
	return s
}

func stripControl(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\t' || r == '\n' || (r >= 0x20 && r != 0x7f) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func applyCommonRules(s string, ctx *ScrubContext) string {
	if ctx == nil {
		return s
	}
	// Workspace roots before home dir: workspace path is more specific.
	for _, r := range ctx.WorkspaceRoots {
		if r != "" {
			s = strings.ReplaceAll(s, r, "<WS>")
		}
	}
	if ctx.HomeDir != "" {
		s = strings.ReplaceAll(s, ctx.HomeDir, "<HOME>")
	}
	if ctx.TempDir != "" {
		s = strings.ReplaceAll(s, ctx.TempDir, "<TMP>")
	}
	s = guidRe.ReplaceAllStringFunc(s, func(match string) string {
		return "<guid:" + hashWithSalt(ctx.Salt, match) + ">"
	})
	return s
}

func scrubALText(s string, ctx *ScrubContext) string {
	// objectIDRe first: preserves MS-range "Codeunit 1" verbatim, buckets
	// customer-range "Codeunit 50104" → "Codeunit <id:50xxx>".
	s = objectIDRe.ReplaceAllStringFunc(s, func(match string) string {
		groups := objectIDRe.FindStringSubmatch(match)
		if len(groups) < 3 {
			return match
		}
		n, err := strconv.Atoi(groups[2])
		if err != nil {
			return match
		}
		switch ClassifyObjectID(n) {
		case RangeMSReserved, RangeMSTest:
			return match // preserve verbatim
		case RangeCustomer:
			return groups[1] + " <id:" + CustomerBucket(n) + ">"
		default:
			return groups[1] + " <id:unknown>"
		}
	})

	// alIdentifierRe second: hash any uppercase-leading identifier not in the
	// BC allowlist. AL object-type keywords (Codeunit, Page, …) are exempt so
	// that MS-range substitutions produced above survive intact.
	s = alIdentifierRe.ReplaceAllStringFunc(s, func(match string) string {
		if alObjectKeywords[match] {
			return match
		}
		if IsAllowedBCSymbol(match) {
			return match
		}
		return "<sym:" + hashWithSalt(ctx.Salt, match) + ">"
	})
	return s
}

func scrubStderr(s string, ctx *ScrubContext) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ' ' || r == '\t' || r == '\n':
			b.WriteRune(r)
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func scrubJSONRPC(s string, ctx *ScrubContext) string {
	re := regexp.MustCompile(`"[^"\\]*(?:\\.[^"\\]*)*"`)
	s = re.ReplaceAllString(s, `"<str>"`)
	return s
}

func scrubURL(s string, ctx *ScrubContext) string {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "<url:invalid>"
	}
	host := u.Hostname()
	if !urlAllowlist[host] {
		return "<url:other>"
	}
	return u.Scheme + "://" + host
}

func scrubPath(s string, ctx *ScrubContext) string {
	s = regexp.MustCompile(`(?i)^[A-Z]:\\`).ReplaceAllString(s, "<DRIVE>\\")
	return s
}

func hashWithSalt(salt []byte, in string) string {
	if len(salt) == 0 {
		return "NOSALT"
	}
	mac := hmac.New(sha256.New, salt)
	mac.Write([]byte(in))
	return hex.EncodeToString(mac.Sum(nil))[:8]
}

// ContainsLeak runs the post-scrub safety net. If it returns true, the
// caller MUST drop the event and log locally.
func ContainsLeak(s string) bool {
	return leakRe.MatchString(s)
}
