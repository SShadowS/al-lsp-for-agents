//go:build windows

package wrapper

import "syscall"

const processQueryLimitedInformation = 0x1000

// isProcessRunning checks if a process with the given PID is still running.
// On Windows, uses OpenProcess which fails for non-existent processes.
func isProcessRunning(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	syscall.CloseHandle(h)
	return true
}
