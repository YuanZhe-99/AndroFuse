//go:build !windows

package gui

// terminateInstallDirProcesses is a no-op off Windows because POSIX systems permit renaming
// directories holding running executables.
func terminateInstallDirProcesses(string) {}
