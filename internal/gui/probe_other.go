//go:build !windows

package gui

import "os/exec"

func hideConsoleWindow(*exec.Cmd) {}
