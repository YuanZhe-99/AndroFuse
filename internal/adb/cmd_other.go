//go:build !windows

package adb

import "os/exec"

func hideConsoleWindow(_ *exec.Cmd) {}
