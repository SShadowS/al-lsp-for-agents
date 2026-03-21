//go:build !windows

package wrapper

import (
	"os"
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
