// Command checkpe verifies that every Windows `.exe` and `.dll` in a build directory
// carries the expected native PE Machine header (`0xAA64` for ARM64, `0x8664` for amd64, `INV-10`).
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

// CheckDir recursively validates every `.exe` and `.dll` under dir matches arch (`INV-10`).
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
		_ = f.Close()
		if got != want {
			return fmt.Errorf("%s has PE machine 0x%04X, expected 0x%04X (%s)", p, got, want, arch)
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
