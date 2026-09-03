//go:build windows

package wrapper

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	processQueryLimitedInformation = 0x1000
	th32csSnapprocess              = 0x00000002
	maxPath                        = 260
)

var (
	modKernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procCreateToolhelp32Snapshot   = modKernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW            = modKernel32.NewProc("Process32FirstW")
	procProcess32NextW             = modKernel32.NewProc("Process32NextW")
	procQueryFullProcessImageNameW = modKernel32.NewProc("QueryFullProcessImageNameW")
)

type processEntry32 struct {
	Size              uint32
	Usage             uint32
	ProcessID         uint32
	DefaultHeapID     uintptr
	ModuleID          uint32
	Threads           uint32
	ParentProcessID   uint32
	PriorityClassBase int32
	Flags             uint32
	ExeFile           [maxPath]uint16
}

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

// getProcessExePath returns the full executable path of the given PID.
// Returns empty string on failure (process gone, access denied, etc.).
func getProcessExePath(pid int) string {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(h)

	var buf [maxPath * 2]uint16
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageNameW.Call(
		uintptr(h),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:size])
}

// getProcessExeName returns just the executable filename (e.g. "al-lsp-wrapper.exe")
// of the given PID, lowercased. Returns empty on failure.
func getProcessExeName(pid int) string {
	p := getProcessExePath(pid)
	if p == "" {
		return ""
	}
	return strings.ToLower(filepath.Base(p))
}

// getParentPid returns the parent PID of the given PID.
// Returns 0 on failure.
func getParentPid(pid int) int {
	snap, _, _ := procCreateToolhelp32Snapshot.Call(th32csSnapprocess, 0)
	if snap == 0 || snap == uintptr(syscall.InvalidHandle) {
		return 0
	}
	defer syscall.CloseHandle(syscall.Handle(snap))

	var entry processEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	r, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	if r == 0 {
		return 0
	}
	for {
		if int(entry.ProcessID) == pid {
			return int(entry.ParentProcessID)
		}
		r, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&entry)))
		if r == 0 {
			return 0
		}
	}
}

// processMemoryCounters mirrors PROCESS_MEMORY_COUNTERS (psapi.h). Only the
// working-set fields are read; the rest are present so the struct size the
// API validates against is correct.
type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

var (
	modPsapi                 = syscall.NewLazyDLL("psapi.dll")
	procGetProcessMemoryInfo = modPsapi.NewProc("GetProcessMemoryInfo")
)

// processMemoryMB returns (currentMB, peakMB) working set for a PID.
// ok is false when the process is gone or the query is not permitted —
// memory reporting is diagnostics only and must never be load-bearing.
func processMemoryMB(pid int) (current, peak uint64, ok bool) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return 0, 0, false
	}
	defer syscall.CloseHandle(h)

	var c processMemoryCounters
	c.CB = uint32(unsafe.Sizeof(c))
	r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.CB))
	if r == 0 {
		return 0, 0, false
	}
	const mb = 1024 * 1024
	return uint64(c.WorkingSetSize) / mb, uint64(c.PeakWorkingSetSize) / mb, true
}
