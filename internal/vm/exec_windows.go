//go:build windows

package vm

import (
	"os/exec"
	"syscall"
)

// hideConsoleWindow sets CREATE_NO_WINDOW so headless VM/SDK tools never flash a console on Windows.
func hideConsoleWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
}
