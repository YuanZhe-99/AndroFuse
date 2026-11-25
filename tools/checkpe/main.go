// Command checkpe verifies that every Windows `.exe` and `.dll` in a build directory
// carries the expected native PE Machine header (`0xAA64` for ARM64, `0x8664` for amd64, `INV-10`),
// and that the desktop app `androfuse.exe` is a GUI-subsystem binary that never opens a console.
package main

import (
	"debug/pe"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	dir := flag.String("dir", "build", "directory to scan for .exe and .dll files")
	arch := flag.String("arch", "arm64", "expected architecture (arm64 or amd64)")
	flag.Parse()

	count, err := CheckDir(*dir, *arch)
	if err != nil {
		fmt.Fprintln(os.Stderr, "checkpe:", err)
		os.Exit(1)
	}
	fmt.Printf("checkpe %s: verified %d native PE binaries\n", *arch, count)
}

// ExpectedMachine returns the COFF/PE Machine constant for arch (`arm64` or `amd64`).
func ExpectedMachine(arch string) (uint16, error) {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "arm64":
		return pe.IMAGE_FILE_MACHINE_ARM64, nil
	case "amd64", "x64":
		return pe.IMAGE_FILE_MACHINE_AMD64, nil
	default:
		return 0, fmt.Errorf("unsupported PE architecture %q", arch)
	}
}

// guiBinaries lists executables that must use the Windows GUI subsystem: a console-subsystem
// desktop app opens a console (Windows Terminal on Windows 11) on every launch.
var guiBinaries = map[string]bool{"androfuse.exe": true}

// CheckDir recursively validates every `.exe` and `.dll` under dir matches arch (`INV-10`).
// Returns: the number of binaries checked.
// Notes: also rejects a console-subsystem build of any name in guiBinaries
// (build it with `-ldflags=-H=windowsgui` or `go tool mygo build`).
func CheckDir(dir, arch string) (int, error) {
	want, err := ExpectedMachine(arch)
	if err != nil {
		return 0, err
	}
	count := 0
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, wErr error) error {
		if wErr != nil {
			return wErr
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext != ".exe" && ext != ".dll" {
			return nil
		}
		f, err := pe.Open(p)
		if err != nil {
			return fmt.Errorf("open PE %s: %w", p, err)
		}
		got := f.Machine
		sub := subsystem(f)
		_ = f.Close()
		if got != want {
			return fmt.Errorf("%s has PE machine 0x%04X, expected 0x%04X (%s)", p, got, want, arch)
		}
		if guiBinaries[strings.ToLower(d.Name())] && sub != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
			return fmt.Errorf("%s uses PE subsystem %d, expected Windows GUI (%d); build with -ldflags=-H=windowsgui", p, sub, pe.IMAGE_SUBSYSTEM_WINDOWS_GUI)
		}
		count++
		return nil
	})
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, fmt.Errorf("no .exe or .dll files found in %s", dir)
	}
	return count, nil
}

// subsystem returns the PE optional-header Subsystem value of f, or 0 when it has none.
func subsystem(f *pe.File) uint16 {
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		return h.Subsystem
	case *pe.OptionalHeader32:
		return h.Subsystem
	}
	return 0
}
