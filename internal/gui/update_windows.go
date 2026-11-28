//go:build windows

package gui

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// terminateInstallDirProcesses ends any running process (other than the current process) whose
// executable lives inside installDir so Windows directory renames during an in-place update are
// not blocked by lingering child tools such as bundled adb.exe or androfuse-svc.exe.
// Inputs: installDir is the application install directory path.
// Side effects: calls TerminateProcess and waits up to 2 seconds per matched process handle.
func terminateInstallDirProcesses(installDir string) {
	installDir = strings.TrimSpace(installDir)
	if installDir == "" {
		return
	}
	prefix := strings.ToLower(filepath.Clean(installDir)) + string(filepath.Separator)

	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(snap) }()

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return
	}

	selfPID := uint32(os.Getpid())
	var handles []windows.Handle
	buf := make([]uint16, 1024)
	for {
		if entry.ProcessID != 0 && entry.ProcessID != selfPID {
			h, oErr := windows.OpenProcess(
				windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE,
				false,
				entry.ProcessID,
			)
			if oErr == nil {
				n := uint32(len(buf))
				if qErr := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); qErr == nil && n > 0 {
					img := strings.ToLower(filepath.Clean(windows.UTF16ToString(buf[:n])))
					if strings.HasPrefix(img, prefix) {
						if windows.TerminateProcess(h, 1) == nil {
							handles = append(handles, h)
							h = 0
						}
					}
				}
				if h != 0 {
					_ = windows.CloseHandle(h)
				}
			}
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}

	for _, h := range handles {
		_, _ = windows.WaitForSingleObject(h, 2000)
		_ = windows.CloseHandle(h)
	}
}
