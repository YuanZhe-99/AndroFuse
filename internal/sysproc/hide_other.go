//go:build !windows

package sysproc

import "os/exec"

// HideConsole is a no-op outside Windows, where child processes never open console windows.
func HideConsole(*exec.Cmd) {}
