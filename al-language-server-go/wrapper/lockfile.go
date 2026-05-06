package wrapper

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SShadowS/al-lsp-for-agents/al-language-server-go/wrapper/telemetry"
)

// lockFileVersion is the current schema version for the JSON lock file.
const lockFileVersion = 2

// wrapperExeNames lists known executable basenames (lowercased) used by
// al-lsp-wrapper across platforms. PID-reuse false positives are filtered
// by checking that the live process at the recorded PID has one of these
// names.
var wrapperExeNames = []string{"al-lsp-wrapper.exe", "al-lsp-wrapper"}

// LauncherID identifies the client that spawned the wrapper.
type LauncherID string

const (
	LauncherVSCode     LauncherID = "vscode"
	LauncherClaudeCode LauncherID = "claude-code"
	LauncherUnknown    LauncherID = ""
)

// normalizeLauncher coerces arbitrary strings to a known LauncherID. Unknown
// values become LauncherUnknown so downstream message logic can rely on a
// closed enum.
func normalizeLauncher(s string) LauncherID {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "vscode", "vs-code", "vsc":
		return LauncherVSCode
	case "claude-code", "claude_code", "claude", "claudecode":
		return LauncherClaudeCode
	default:
		return LauncherUnknown
	}
}

// humanLauncher returns a user-facing label for a launcher ID, optionally
// enriched with a parent-process hint when the launcher itself is unknown.
func humanLauncher(id LauncherID, parentExeName string) string {
	switch id {
	case LauncherVSCode:
		return "VS Code extension (\"AL LSP for Agents\")"
	case LauncherClaudeCode:
		return "Claude Code plugin (\"al-language-server-go-*\")"
	default:
		if parentExeName != "" {
			return fmt.Sprintf("unidentified client (parent process: %s)", parentExeName)
		}
		return "unidentified client"
	}
}

// LockInfo describes a single wrapper instance's claim on a workspace.
// Persisted as JSON in the OS temp directory.
type LockInfo struct {
	Version       int    `json:"version"`
	PID           int    `json:"pid"`
	Launcher      string `json:"launcher"`
	ExePath       string `json:"exePath"`
	ExeName       string `json:"exeName"`
	ParentPID     int    `json:"parentPid"`
	ParentExeName string `json:"parentExeName"`
	StartedAt     string `json:"startedAt"`
	WorkspaceRoot string `json:"workspaceRoot"`
}

// startedAtTime parses StartedAt back into a time.Time. Returns zero time on
// failure.
func (l *LockInfo) startedAtTime() time.Time {
	if l.StartedAt == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, l.StartedAt)
	if err != nil {
		return time.Time{}
	}
	return t
}

// LauncherID returns the parsed launcher enum.
func (l *LockInfo) LauncherID() LauncherID {
	return normalizeLauncher(l.Launcher)
}

// isWrapperExe reports whether the recorded ExeName matches a known wrapper
// binary. Used to detect PID-reuse false positives: even if a process with
// the recorded PID is alive, if its exe doesn't match, the lock is stale.
func isWrapperExe(exeName string) bool {
	en := strings.ToLower(exeName)
	for _, n := range wrapperExeNames {
		if en == n {
			return true
		}
	}
	return false
}

// lockFilePath computes the workspace-specific lock path under the OS temp dir.
// The workspace root is normalized then sha256-hashed (truncated) to produce
// a stable, filesystem-safe identifier.
func lockFilePath(workspaceRoot string) string {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(NormalizePath(workspaceRoot))))[:16]
	return filepath.Join(os.TempDir(), fmt.Sprintf("al-lsp-wrapper-%s.pid", hash))
}

// readLockFile reads and parses a lock file. Supports both the v2 JSON format
// and the legacy raw-PID format (a single integer); legacy entries return a
// LockInfo with only PID populated and Launcher="" so callers treat them as
// "unknown launcher".
func readLockFile(path string) (*LockInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("lock file is empty")
	}

	if strings.HasPrefix(trimmed, "{") {
		var info LockInfo
		if err := json.Unmarshal([]byte(trimmed), &info); err != nil {
			// parse-error: lock file JSON is malformed (truncated write or disk corruption)
			telemetry.TrackGlobalConfigError("lockfile", "parse-error")
			return nil, fmt.Errorf("parse JSON lock: %w", err)
		}
		return &info, nil
	}

	pid, err := strconv.Atoi(trimmed)
	if err != nil {
		// parse-error: legacy lock file is neither JSON nor a plain integer PID
		telemetry.TrackGlobalConfigError("lockfile", "parse-error")
		return nil, fmt.Errorf("legacy lock not an integer: %w", err)
	}
	return &LockInfo{Version: 1, PID: pid}, nil
}

// writeLockFile writes a LockInfo as pretty-printed JSON. The write is
// atomic on the same filesystem: write to a temp file in the same directory,
// then rename over the destination.
func writeLockFile(path string, info *LockInfo) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "al-lsp-wrapper-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// lockOwnerStatus describes the result of inspecting a lock file's owner.
type lockOwnerStatus int

const (
	lockOwnerNone   lockOwnerStatus = iota // no readable lock file
	lockOwnerSelf                          // lock belongs to current process
	lockOwnerLive                          // another live wrapper owns it
	lockOwnerStale                         // file exists but owner is gone or not a wrapper
)

// inspectLockOwner reads the lock at path and classifies its owner. The
// returned LockInfo is non-nil for lockOwnerLive and lockOwnerStale.
//
// Liveness check is hardened: a process must be running AND its exe basename
// must match a known wrapper binary. This eliminates false positives caused
// by PID reuse (where an unrelated process happens to have the recorded PID).
//
// For legacy locks (no exe info recorded), liveness check uses the live
// process's actual exe name as the source of truth.
func inspectLockOwner(path string, selfPID int) (lockOwnerStatus, *LockInfo) {
	info, err := readLockFile(path)
	if err != nil {
		return lockOwnerNone, nil
	}
	if info.PID == selfPID {
		return lockOwnerSelf, info
	}
	if info.PID <= 0 {
		return lockOwnerStale, info
	}
	if !isProcessRunning(info.PID) {
		return lockOwnerStale, info
	}

	liveExeName := getProcessExeName(info.PID)
	if liveExeName == "" {
		// Can't introspect the process — most likely access denied, but the
		// process exists. Trust the recorded exe name if we have one, else
		// treat as stale to avoid false alarms from unrelated processes.
		if isWrapperExe(info.ExeName) {
			return lockOwnerLive, info
		}
		return lockOwnerStale, info
	}
	if !isWrapperExe(liveExeName) {
		return lockOwnerStale, info
	}

	// Backfill live exe info if the file lacked it (legacy format).
	if info.ExeName == "" {
		info.ExeName = liveExeName
	}
	return lockOwnerLive, info
}
