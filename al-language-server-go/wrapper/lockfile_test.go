package wrapper

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNormalizeLauncher(t *testing.T) {
	cases := map[string]LauncherID{
		"vscode":       LauncherVSCode,
		"VSCode":       LauncherVSCode,
		" vs-code ":    LauncherVSCode,
		"claude-code":  LauncherClaudeCode,
		"Claude":       LauncherClaudeCode,
		"claudecode":   LauncherClaudeCode,
		"":             LauncherUnknown,
		"random":       LauncherUnknown,
	}
	for in, want := range cases {
		if got := normalizeLauncher(in); got != want {
			t.Errorf("normalizeLauncher(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsWrapperExe(t *testing.T) {
	good := []string{"al-lsp-wrapper", "al-lsp-wrapper.exe", "AL-LSP-WRAPPER.EXE"}
	bad := []string{"", "code.exe", "claude.exe", "al-call-hierarchy.exe"}
	for _, s := range good {
		if !isWrapperExe(s) {
			t.Errorf("isWrapperExe(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if isWrapperExe(s) {
			t.Errorf("isWrapperExe(%q) = true, want false", s)
		}
	}
}

func TestLockFileRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.json")

	in := &LockInfo{
		Version:       lockFileVersion,
		PID:           12345,
		Launcher:      "vscode",
		ExePath:       `C:\Users\me\al-lsp-wrapper.exe`,
		ExeName:       "al-lsp-wrapper.exe",
		ParentPID:     999,
		ParentExeName: "code.exe",
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
		WorkspaceRoot: `C:\work\project`,
	}
	if err := writeLockFile(path, in); err != nil {
		t.Fatalf("writeLockFile: %v", err)
	}

	out, err := readLockFile(path)
	if err != nil {
		t.Fatalf("readLockFile: %v", err)
	}
	if out.PID != in.PID || out.Launcher != in.Launcher || out.ExePath != in.ExePath {
		t.Errorf("roundtrip mismatch: got %+v want %+v", out, in)
	}
	if out.LauncherID() != LauncherVSCode {
		t.Errorf("LauncherID() = %q, want vscode", out.LauncherID())
	}
}

func TestReadLegacyPidFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.pid")

	if err := os.WriteFile(path, []byte("4242\n"), 0644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	out, err := readLockFile(path)
	if err != nil {
		t.Fatalf("readLockFile legacy: %v", err)
	}
	if out.PID != 4242 {
		t.Errorf("legacy PID = %d, want 4242", out.PID)
	}
	if out.LauncherID() != LauncherUnknown {
		t.Errorf("legacy launcher = %q, want unknown", out.LauncherID())
	}
}

func TestReadLockFileEmptyAndMalformed(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.lock")
	if err := os.WriteFile(empty, []byte("   \n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readLockFile(empty); err == nil {
		t.Error("expected error for empty lock")
	}

	bad := filepath.Join(dir, "bad.lock")
	if err := os.WriteFile(bad, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readLockFile(bad); err == nil {
		t.Error("expected error for malformed JSON")
	}

	missing := filepath.Join(dir, "no-such-file.lock")
	if _, err := readLockFile(missing); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestInspectLockOwnerSelfAndStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.json")

	// Self-owned lock.
	self := os.Getpid()
	info := &LockInfo{Version: lockFileVersion, PID: self, Launcher: "vscode"}
	if err := writeLockFile(path, info); err != nil {
		t.Fatal(err)
	}
	if status, _ := inspectLockOwner(path, self); status != lockOwnerSelf {
		t.Errorf("self status = %v, want lockOwnerSelf", status)
	}

	// Stale: PID guaranteed not to be a wrapper (PID 1 is init/system).
	// Even if PID 1 is "running", its exe name will not match wrapper names.
	staleInfo := &LockInfo{Version: lockFileVersion, PID: 1, Launcher: "vscode", ExeName: "definitely-not-wrapper"}
	if err := writeLockFile(path, staleInfo); err != nil {
		t.Fatal(err)
	}
	status, _ := inspectLockOwner(path, self)
	// We don't assert exact stale vs live here because PID 1's introspection
	// is platform-dependent (privileged on Linux), but it must not be Self.
	if status == lockOwnerSelf {
		t.Errorf("PID 1 lock should not be classified as self")
	}

	// Definitely-stale: a PID we know is dead (a freshly used+exited child
	// would be ideal, but using a very high PID is a portable proxy).
	deadInfo := &LockInfo{Version: lockFileVersion, PID: 0x7FFFFFFE, Launcher: "vscode"}
	if err := writeLockFile(path, deadInfo); err != nil {
		t.Fatal(err)
	}
	status, _ = inspectLockOwner(path, self)
	if status != lockOwnerStale {
		t.Errorf("dead PID status = %v, want lockOwnerStale", status)
	}

	// No file at all.
	missing := filepath.Join(dir, "missing.lock")
	status, _ = inspectLockOwner(missing, self)
	if status != lockOwnerNone {
		t.Errorf("missing file status = %v, want lockOwnerNone", status)
	}
}

func TestInferLauncherFromParent(t *testing.T) {
	cases := map[string]LauncherID{
		"code.exe":          LauncherVSCode,
		"Code.exe":          LauncherVSCode,
		"code-insiders.exe": LauncherVSCode,
		"code":              LauncherVSCode,
		"claude.exe":        LauncherClaudeCode,
		"claude-code-cli":   LauncherClaudeCode,
		"explorer.exe":      LauncherUnknown,
		"":                  LauncherUnknown,
	}
	for in, want := range cases {
		if got := inferLauncherFromParent(in); got != want {
			t.Errorf("inferLauncherFromParent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanLauncher(t *testing.T) {
	if got := humanLauncher(LauncherVSCode, ""); !strings.Contains(got, "VS Code") {
		t.Errorf("VS Code label missing: %q", got)
	}
	if got := humanLauncher(LauncherClaudeCode, ""); !strings.Contains(got, "Claude Code") {
		t.Errorf("Claude Code label missing: %q", got)
	}
	if got := humanLauncher(LauncherUnknown, ""); !strings.Contains(got, "unidentified") {
		t.Errorf("unidentified label missing: %q", got)
	}
	if got := humanLauncher(LauncherUnknown, "code.exe"); !strings.Contains(got, "code.exe") {
		t.Errorf("parent hint missing: %q", got)
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{2 * time.Minute, "2m"},
		{90 * time.Minute, "1h30m"},
		{25 * time.Hour, "1d1h"},
	}
	for _, c := range cases {
		if got := humanDuration(c.d); got != c.want {
			t.Errorf("humanDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestBuildDuplicateWarningMatrix verifies the message text changes with
// the launcher combination. Each combo must produce a distinct, actionable
// message body so users do not need to ask "which two are conflicting?".
func TestBuildDuplicateWarningMatrix(t *testing.T) {
	mk := func(launcher string, pid int) *LockInfo {
		return &LockInfo{
			Version:       lockFileVersion,
			PID:           pid,
			Launcher:      launcher,
			ExeName:       "al-lsp-wrapper.exe",
			ExePath:       `C:\bin\al-lsp-wrapper.exe`,
			ParentPID:     pid + 100,
			ParentExeName: "code.exe",
			StartedAt:     time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339),
		}
	}

	cases := []struct {
		name         string
		self         *LockInfo
		other        *LockInfo
		mustContain  []string
	}{
		{
			name:  "vscode_and_claude_code",
			self:  mk("vscode", 1),
			other: mk("claude-code", 2),
			mustContain: []string{
				"VS Code extension",
				"Claude Code plugin",
				"/plugin",
			},
		},
		{
			name:  "two_vscode",
			self:  mk("vscode", 1),
			other: mk("vscode", 2),
			mustContain: []string{
				"second VS Code",
				"two VS Code windows",
			},
		},
		{
			name:  "two_claude_code",
			self:  mk("claude-code", 1),
			other: mk("claude-code", 2),
			mustContain: []string{
				"second Claude Code",
				"al-language-server-go",
			},
		},
		{
			name: "other_unknown_with_parent_hint",
			self: mk("vscode", 1),
			other: &LockInfo{
				Version: lockFileVersion, PID: 99, Launcher: "",
				ExeName: "al-lsp-wrapper.exe", ParentExeName: "claude.exe",
				StartedAt: time.Now().UTC().Format(time.RFC3339),
			},
			mustContain: []string{
				"Claude Code plugin",
				"VS Code extension",
			},
		},
		{
			name: "other_unknown_unidentifiable",
			self: mk("vscode", 1),
			other: &LockInfo{
				Version: lockFileVersion, PID: 99, Launcher: "",
				ExeName: "al-lsp-wrapper.exe", ParentExeName: "explorer.exe",
				StartedAt: time.Now().UTC().Format(time.RFC3339),
			},
			mustContain: []string{
				"could not be identified",
				"PID 99",
				"explorer.exe",
			},
		},
		{
			name: "self_unknown_other_known",
			self: &LockInfo{
				Version: lockFileVersion, PID: 1, Launcher: "",
				ExeName: "al-lsp-wrapper.exe",
				StartedAt: time.Now().UTC().Format(time.RFC3339),
			},
			other: mk("vscode", 2),
			mustContain: []string{
				"without a --launcher tag",
				"VS Code extension",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := buildDuplicateWarning(c.self, c.other)
			for _, want := range c.mustContain {
				if !strings.Contains(msg, want) {
					t.Errorf("message missing %q. Full message:\n%s", want, msg)
				}
			}
			// Every message must include both PIDs so the user can act.
			if !strings.Contains(msg, "PID "+strconv.Itoa(c.self.PID)) {
				t.Errorf("message missing self PID %d:\n%s", c.self.PID, msg)
			}
			if !strings.Contains(msg, "PID "+strconv.Itoa(c.other.PID)) {
				t.Errorf("message missing other PID %d:\n%s", c.other.PID, msg)
			}
		})
	}
}

func TestLockFilePathStableAndDistinct(t *testing.T) {
	a1 := lockFilePath(`C:\Work\Project`)
	a2 := lockFilePath(`C:\Work\Project`)
	if a1 != a2 {
		t.Errorf("same input must produce same lock path:\n  a1=%s\n  a2=%s", a1, a2)
	}

	b := lockFilePath(`C:\Work\Other`)
	if a1 == b {
		t.Errorf("different workspaces must produce different lock paths:\n  a=%s\n  b=%s", a1, b)
	}
}
