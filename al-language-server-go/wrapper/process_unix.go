//go:build !windows

package wrapper

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// isProcessRunning checks if a process with the given PID is still running.
// On Unix, Signal(0) returns nil if the process exists.
func isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// getProcessExePath returns the full executable path of the given PID via
// /proc/<pid>/exe. Returns empty string on failure.
func getProcessExePath(pid int) string {
	target, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return ""
	}
	return target
}

// getProcessExeName returns just the executable filename of the given PID,
// lowercased. Returns empty on failure.
func getProcessExeName(pid int) string {
	p := getProcessExePath(pid)
	if p == "" {
		return ""
	}
	return strings.ToLower(filepath.Base(p))
}

// getParentPid returns the parent PID of the given PID via /proc/<pid>/stat.
// Returns 0 on failure.
//
// /proc/<pid>/stat format: pid (comm) state ppid ...
// The comm field can contain spaces and parens, so we find the last ')' and
// parse fields after that.
func getParentPid(pid int) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	s := string(data)
	end := strings.LastIndex(s, ")")
	if end == -1 || end+2 >= len(s) {
		return 0
	}
	fields := strings.Fields(s[end+2:])
	// fields[0] = state, fields[1] = ppid
	if len(fields) < 2 {
		return 0
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return ppid
}

// processMemoryMB returns (currentMB, peakMB) resident set for a PID by
// reading /proc/<pid>/status. ok is false when unavailable (a non-Linux
// unix, or the process is gone) — memory reporting is diagnostics only and
// must never be load-bearing.
func processMemoryMB(pid int) (current, peak uint64, ok bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, 0, false
	}
	readKB := func(prefix string) (uint64, bool) {
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, false
			}
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, false
			}
			return kb, true
		}
		return 0, false
	}
	cur, okCur := readKB("VmRSS:")
	pk, okPeak := readKB("VmHWM:")
	if !okCur {
		return 0, 0, false
	}
	if !okPeak {
		pk = cur
	}
	return cur / 1024, pk / 1024, true
}
