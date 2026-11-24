package gui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
)

// HostDiagnostics captures live environment checks without blocking the UI
// thread or faking unsupported capabilities.
type HostDiagnostics struct {
	CoreLoaded bool
	CoreABI    uint32
	CoreError  string
	SelfCheck  core.SelfCheck
	ADBFound   bool
	ADBVersion string
	ADBPath    string
	CheckedAt  time.Time
}

// RunDiagnostics probes the Rust core library (when available) and local ADB
// installation using argument-vector execution without invoking a shell.
func RunDiagnostics(stateDir, profile string) HostDiagnostics {
	out := HostDiagnostics{
		CheckedAt: time.Now(),
		SelfCheck: core.SelfCheck{
			OS:          runtime.GOOS,
			ProcessArch: runtime.GOARCH,
			NativeArch:  runtime.GOARCH,
		},
	}

	if libPath := locateCoreLibrary(); libPath != "" {
		lib, err := core.Open(libPath)
		if err != nil {
			out.CoreError = err.Error()
		} else {
			defer lib.Stop()
			if st, err := lib.Start(core.Config{
				Version:  version.Version,
				StateDir: stateDir,
				Profile:  profile,
			}); err != nil {
				out.CoreError = err.Error()
			} else if sc, err := lib.SelfCheck(); err != nil {
				out.CoreError = err.Error()
			} else {
				out.CoreLoaded = true
				out.CoreABI = st.ABI
				out.SelfCheck = sc
			}
		}
	}

	adbPath, adbVer := probeADB()
	out.ADBFound = adbPath != ""
	out.ADBPath = adbPath
	out.ADBVersion = adbVer
	return out
}

// LocateCoreLibrary finds androfuse_core next to the running binary, via
// ANDROFUSE_CORE_LIB, or in local development Cargo target directories.
func LocateCoreLibrary() string {
	return locateCoreLibrary()
}

func locateCoreLibrary() string {
	if env := strings.TrimSpace(os.Getenv("ANDROFUSE_CORE_LIB")); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env
		}
	}
	name := core.LibraryName()
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			core.LibraryPath(dir),
			core.LibraryPath(filepath.Join(dir, "bin")),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "core", "target", "release", name),
			filepath.Join(wd, "core", "target", "debug", name),
		)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func probeADB() (string, string) {
	if runtime.GOOS == "android" {
		return "", ""
	}
	var candidates []string
	if p, err := exec.LookPath("adb"); err == nil {
		candidates = append(candidates, p)
	}
	if runtime.GOOS == "windows" {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			candidates = append(candidates, filepath.Join(local, "Android", "Sdk", "platform-tools", "adb.exe"))
		}
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := exec.CommandContext(ctx, c, "version")
		hideConsoleWindow(cmd)
		raw, err := cmd.Output()
		cancel()
		if err != nil {
			return c, "detected"
		}
		line := strings.TrimSpace(strings.Split(string(raw), "\n")[0])
		return c, line
	}
	return "", ""
}
