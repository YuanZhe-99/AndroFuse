//go:build windows

package sysproc

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW from the Win32 process creation flags.
const createNoWindow = 0x08000000

// HideConsole makes cmd start without a console window.
// Inputs: cmd, before Start/Run.
// Side effects: sets cmd.SysProcAttr (replacing any previous value).
// Notes: a console child of a GUI-subsystem parent otherwise gets a new console, which
// Windows 11 opens in Windows Terminal; HideWindow alone (SW_HIDE) is ignored by Terminal,
// so CREATE_NO_WINDOW is required. stdin/stdout pipes keep working.
func HideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
