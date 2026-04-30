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
