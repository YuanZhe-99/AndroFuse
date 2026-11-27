package adb

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxPlatformToolsZipBytes = 64 << 20 // 64 MiB safety cap on official archive downloads

func adbExeName() string {
	if runtime.GOOS == "windows" {
		return "adb.exe"
	}
	return "adb"
}

// DefaultPlatformToolsDir returns the per-user directory where AndroFuse stores auto-downloaded
// official platform-tools (`%LOCALAPPDATA%\AndroFuse\platform-tools` on Windows).
func DefaultPlatformToolsDir() string {
	if runtime.GOOS == "windows" {
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, "AndroFuse", "platform-tools")
		}
	}
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "AndroFuse", "platform-tools")
}

// PlatformToolsURL returns Google's official HTTPS platform-tools archive URL for goos.
func PlatformToolsURL(goos string) (string, error) {
	switch goos {
	case "windows":
		return "https://dl.google.com/android/repository/platform-tools-latest-windows.zip", nil
	case "linux":
		return "https://dl.google.com/android/repository/platform-tools-latest-linux.zip", nil
	case "darwin":
		return "https://dl.google.com/android/repository/platform-tools-latest-darwin.zip", nil
	default:
		return "", fmt.Errorf("unsupported operating system %q for platform-tools", goos)
	}
}

// InstallPlatformTools downloads the official Google platform-tools archive into destDir and
// returns an initialized ADB Client.
// Inputs: destDir defaults to DefaultPlatformToolsDir() when empty; onProgress receives 0..100.
// Side effects: HTTPS GET to dl.google.com, temporary file creation, atomic swap into destDir.
func InstallPlatformTools(ctx context.Context, destDir string, onProgress func(pct int)) (*Client, error) {
	if strings.TrimSpace(destDir) == "" {
		destDir = DefaultPlatformToolsDir()
	}
	rawURL, err := PlatformToolsURL(runtime.GOOS)
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(destDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("prepare platform-tools dir: %w", err)
	}

	tmpZip, err := os.CreateTemp(parent, "platform-tools-*.zip")
	if err != nil {
		return nil, fmt.Errorf("create temp archive: %w", err)
	}
	zipPath := tmpZip.Name()
	defer func() {
		_ = tmpZip.Close()
		_ = os.Remove(zipPath)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 4 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download platform-tools: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download platform-tools: HTTP %d", resp.StatusCode)
	}

	if onProgress != nil {
		onProgress(0)
	}
	total := resp.ContentLength
	var written int64
	lastPct := -1
	buf := make([]byte, 64*1024)
	limited := io.LimitReader(resp.Body, maxPlatformToolsZipBytes+1)
	for {
		n, rErr := limited.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > maxPlatformToolsZipBytes {
				return nil, fmt.Errorf("platform-tools archive exceeded %d MiB safety limit", maxPlatformToolsZipBytes>>20)
			}
			if _, wErr := tmpZip.Write(buf[:n]); wErr != nil {
				return nil, fmt.Errorf("write platform-tools archive: %w", wErr)
			}
			if onProgress != nil && total > 0 {
				pct := int(written * 95 / total)
				if pct > 95 {
					pct = 95
				}
				if pct != lastPct {
					lastPct = pct
					onProgress(pct)
				}
			}
		}
		if rErr == io.EOF {
			break
		}
		if rErr != nil {
			return nil, fmt.Errorf("read platform-tools archive: %w", rErr)
		}
	}
	if err := tmpZip.Close(); err != nil {
		return nil, fmt.Errorf("flush platform-tools archive: %w", err)
	}

	adbPath, err := ExtractPlatformToolsZip(zipPath, destDir)
	if err != nil {
		return nil, err
	}
	if onProgress != nil {
		onProgress(100)
	}
	return &Client{Path: adbPath}, nil
}

// ExtractPlatformToolsZip validates and extracts zipPath safely into destDir via an atomic swap,
// rejecting Zip-Slip path traversal entries and verifying that `adb` is present afterward.
// Returns: the full path to the extracted adb executable.
func ExtractPlatformToolsZip(zipPath, destDir string) (string, error) {
	destDir = filepath.Clean(destDir)
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("open platform-tools archive: %w", err)
	}
	defer zr.Close()

	parent := filepath.Dir(destDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	tmpDir := destDir + ".tmp"
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", fmt.Errorf("create extract staging dir: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	cleanTmp := filepath.Clean(tmpDir) + string(os.PathSeparator)
	var totalExtracted int64
	for _, f := range zr.File {
		relName := strings.TrimPrefix(filepath.ToSlash(f.Name), "platform-tools/")
		relName = strings.TrimPrefix(relName, "/")
		if relName == "" {
			continue
		}
		dst := filepath.Clean(filepath.Join(tmpDir, filepath.FromSlash(relName)))
		if !strings.HasPrefix(dst, cleanTmp) {
			return "", fmt.Errorf("zip entry %q escapes destination", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		mode := f.Mode() & 0o755
		if mode == 0 {
			mode = 0o644
		}
		if strings.EqualFold(filepath.Base(dst), "adb") || strings.EqualFold(filepath.Base(dst), "adb.exe") {
			mode = 0o755
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			_ = rc.Close()
			return "", err
		}
		n, copyErr := io.Copy(out, io.LimitReader(rc, maxPlatformToolsZipBytes))
		_ = rc.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		totalExtracted += n
		if totalExtracted > maxPlatformToolsZipBytes*2 {
			return "", fmt.Errorf("extracted platform-tools exceeded safety cap")
		}
	}

	adbPath := filepath.Join(tmpDir, adbExeName())
	if _, err := os.Stat(adbPath); err != nil {
		return "", fmt.Errorf("extracted archive missing %s: %w", adbExeName(), err)
	}

	_ = os.RemoveAll(destDir)
	if err := os.Rename(tmpDir, destDir); err != nil {
		return "", fmt.Errorf("finalize platform-tools dir: %w", err)
	}
	cleanup = false
	return filepath.Join(destDir, adbExeName()), nil
}
