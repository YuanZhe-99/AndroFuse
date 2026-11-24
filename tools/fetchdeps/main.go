// Command fetchdeps downloads verified upstream binary dependencies (such as
// Apache-2.0 scrcpy-server-v5.0.1) into .dev/cache/ without checking binary
// payloads into version control.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	scrcpyServerVersion = "v5.0.1"
	scrcpyServerURL     = "https://github.com/Genymobile/scrcpy/releases/download/v5.0.1/scrcpy-server-v5.0.1"
	scrcpyServerSHA256  = "764eb6f79811d5211fe9df341120882ba9994c7a61b897d7bf3fb662e53bc536"
	scrcpyServerName    = "scrcpy-server-v5.0.1"
)

// main ensures .dev/cache/scrcpy-server-v5.0.1 exists and matches its pinned SHA-256 digest.
//
// Side effects: Downloads up to ~734 KB over HTTPS on cache miss and writes to .dev/cache/.
func main() {
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cacheDir := filepath.Join(root, ".dev", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	dst := filepath.Join(cacheDir, scrcpyServerName)
	if ok, sum, err := verifyFile(dst, scrcpyServerSHA256); err == nil && ok {
		fmt.Printf("cached: %s (sha256=%s)\n", dst, sum)
		return
	}

	fmt.Printf("downloading %s -> %s\n", scrcpyServerURL, dst)
	if err := downloadAndVerify(scrcpyServerURL, dst, scrcpyServerSHA256); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("verified %s %s (%s)\n", scrcpyServerName, scrcpyServerVersion, scrcpyServerSHA256)
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above working directory")
		}
		dir = parent
	}
}

func verifyFile(path, wantHex string) (bool, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	return got == wantHex, got, nil
}

func downloadAndVerify(url, dst, wantHex string) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %s", url, resp.Status)
	}

	tmp := dst + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(f, io.TeeReader(resp.Body, h)); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		_ = os.Remove(tmp)
		return fmt.Errorf("SHA-256 mismatch for %s: got %s want %s", dst, got, wantHex)
	}
	return os.Rename(tmp, dst)
}
