// Command fetchdeps downloads verified upstream build dependencies into .dev/cache/ without
// checking binary payloads into version control:
//
//   - scrcpy-server v5.0.1 (Apache-2.0), pushed to devices at runtime;
//   - protoc v36.2 (BSD-3-Clause), a build-time tool that the embedded EasyTier mesh and its
//     protobuf crates need; .cargo/config.toml points cargo at it.
//
// With -check-upstream it instead reports whether a newer EasyTier release than the one pinned in
// core/androfuse-core/Cargo.toml exists, so the embedded mesh is bumped with each app update.
package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	scrcpyServerVersion = "v5.0.1"
	scrcpyServerURL     = "https://github.com/Genymobile/scrcpy/releases/download/v5.0.1/scrcpy-server-v5.0.1"
	scrcpyServerSHA256  = "764eb6f79811d5211fe9df341120882ba9994c7a61b897d7bf3fb662e53bc536"
	scrcpyServerName    = "scrcpy-server-v5.0.1"

	protocVersion = "36.2"

	// easyTierTag and easyTierRev pin the embedded EasyTier release (PLAN §9.1-1).
	easyTierTag  = "v2.7.1"
	easyTierRev  = "0d9c52b169dc2112687ddefcf571cd01d383193c"
	easyTierRepo = "https://github.com/EasyTier/EasyTier"
)

// protocAssets maps GOOS/GOARCH to the official protoc release archive and its SHA-256
// (from GitHub's release asset digests). Windows on ARM64 runs the x64 build-time tool under
// emulation; it is never shipped, so INV-10 is unaffected.
var protocAssets = map[string]struct{ name, sha256 string }{
	"windows/amd64": {"protoc-36.2-win64.zip", "f0c128dc0d8492eceece83bb459a4c0e316764b929ffbf1aa416357fd644edd3"},
	"windows/arm64": {"protoc-36.2-win64.zip", "f0c128dc0d8492eceece83bb459a4c0e316764b929ffbf1aa416357fd644edd3"},
	"linux/amd64":   {"protoc-36.2-linux-x86_64.zip", "121f6c7afe1d4d0e3ea6aab9432038599250134cbf4474cb1167d2c7decd4278"},
	"linux/arm64":   {"protoc-36.2-linux-aarch_64.zip", "8b8f18bd2b30346efbc698dd5a73dd7c805f3ef8380f6dfc95c768f3f1852f6a"},
}

// main fetches the cached dependencies, or checks upstream EasyTier with -check-upstream.
//
// Side effects: downloads up to ~5 MB over HTTPS on cache miss and writes to .dev/cache/.
func main() {
	checkUpstream := flag.Bool("check-upstream", false, "report whether a newer EasyTier release than the pinned one exists (exit 2 if so)")
	flag.Parse()

	root, err := findRepoRoot()
	if err != nil {
		fail(err)
	}
	if *checkUpstream {
		os.Exit(runUpstreamCheck(root))
	}
	cacheDir := filepath.Join(root, ".dev", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		fail(err)
	}
	if err := fetchFile(scrcpyServerURL, filepath.Join(cacheDir, scrcpyServerName), scrcpyServerSHA256); err != nil {
		fail(err)
	}
	fmt.Printf("verified %s %s\n", scrcpyServerName, scrcpyServerVersion)
	if err := fetchProtoc(cacheDir); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "fetchdeps:", err)
	os.Exit(1)
}

// fetchFile ensures dst exists with the pinned digest, downloading it on a cache miss.
func fetchFile(url, dst, wantHex string) error {
	if ok, _, err := verifyFile(dst, wantHex); err == nil && ok {
		return nil
	}
	fmt.Printf("downloading %s -> %s\n", url, dst)
	return downloadAndVerify(url, dst, wantHex)
}

// fetchProtoc unpacks the pinned protoc for this host into .dev/cache/protoc/ (bin/ + include/).
// Notes: on Windows it also writes bin/protoc (no extension) so the single PROTOC path in
// core/.cargo/config.toml works on every host.
func fetchProtoc(cacheDir string) error {
	asset, ok := protocAssets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return fmt.Errorf("no pinned protoc for %s/%s; install protoc %s and set PROTOC", runtime.GOOS, runtime.GOARCH, protocVersion)
	}
	zipPath := filepath.Join(cacheDir, asset.name)
	url := "https://github.com/protocolbuffers/protobuf/releases/download/v" + protocVersion + "/" + asset.name
	if err := fetchFile(url, zipPath, asset.sha256); err != nil {
		return err
	}
	dir := filepath.Join(cacheDir, "protoc")
	stamp := filepath.Join(dir, ".version")
	if b, err := os.ReadFile(stamp); err == nil && strings.TrimSpace(string(b)) == asset.sha256 {
		fmt.Printf("cached protoc %s in %s\n", protocVersion, dir)
		return nil
	}
	_ = os.RemoveAll(dir)
	if err := unzip(zipPath, dir); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		if err := copyFile(filepath.Join(dir, "bin", "protoc.exe"), filepath.Join(dir, "bin", "protoc")); err != nil {
			return err
		}
	}
	if err := os.WriteFile(stamp, []byte(asset.sha256+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("verified protoc %s -> %s\n", protocVersion, dir)
	return nil
}

// unzip extracts archive into dir, rejecting entries that would escape it.
func unzip(archive, dir string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		dst := filepath.Join(dir, filepath.FromSlash(f.Name))
		if !strings.HasPrefix(dst, filepath.Clean(dir)+string(os.PathSeparator)) {
			return fmt.Errorf("zip entry %q escapes %s", f.Name, dir)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode()|0o644)
		if err != nil {
			rc.Close()
			return err
		}
		_, cErr := io.Copy(out, rc)
		rc.Close()
		if err := out.Close(); cErr == nil {
			cErr = err
		}
		if cErr != nil {
			return cErr
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}

// runUpstreamCheck compares the pinned EasyTier release with the newest upstream tag.
// Returns: process exit code — 0 when current, 2 when a newer release exists, 1 on error.
func runUpstreamCheck(root string) int {
	manifest, err := os.ReadFile(filepath.Join(root, "core", "androfuse-core", "Cargo.toml"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fetchdeps:", err)
		return 1
	}
	if !strings.Contains(string(manifest), easyTierRev) {
		fmt.Fprintf(os.Stderr, "fetchdeps: core/androfuse-core/Cargo.toml does not pin EasyTier rev %s (%s); update easyTierTag/easyTierRev together\n", easyTierRev, easyTierTag)
		return 1
	}
	out, err := exec.Command("git", "ls-remote", "--tags", easyTierRepo).Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fetchdeps: git ls-remote:", err)
		return 1
	}
	latest := latestReleaseTag(string(out))
	if latest == "" {
		fmt.Fprintln(os.Stderr, "fetchdeps: no release tags found upstream")
		return 1
	}
	if compareVersions(latest, easyTierTag) > 0 {
		fmt.Printf("EasyTier %s is available (pinned %s). Bump the easytier-ffi rev in core/androfuse-core/Cargo.toml and easyTierTag/easyTierRev here, then rerun the mesh tests.\n", latest, easyTierTag)
		return 2
	}
	fmt.Printf("EasyTier %s is the latest release\n", easyTierTag)
	return 0
}

// latestReleaseTag returns the highest `vX.Y.Z` tag in `git ls-remote --tags` output.
func latestReleaseTag(lsRemote string) string {
	best := ""
	for _, line := range strings.Split(lsRemote, "\n") {
		_, ref, ok := strings.Cut(strings.TrimSpace(line), "refs/tags/")
		if !ok || strings.HasSuffix(ref, "^{}") || parseVersion(ref) == nil {
			continue
		}
		if best == "" || compareVersions(ref, best) > 0 {
			best = ref
		}
	}
	return best
}

// parseVersion parses `vX.Y.Z` into numbers, or returns nil for pre-releases and other tags.
func parseVersion(tag string) []int {
	parts := strings.Split(strings.TrimPrefix(tag, "v"), ".")
	if !strings.HasPrefix(tag, "v") || len(parts) != 3 {
		return nil
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		nums[i] = n
	}
	return nums
}

// compareVersions orders two `vX.Y.Z` tags (unparsable tags sort lowest).
func compareVersions(a, b string) int {
	va, vb := parseVersion(a), parseVersion(b)
	switch {
	case va == nil && vb == nil:
		return 0
	case va == nil:
		return -1
	case vb == nil:
		return 1
	}
	for i := range 3 {
		if va[i] != vb[i] {
			if va[i] > vb[i] {
				return 1
			}
			return -1
		}
	}
	return 0
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
	client := &http.Client{Timeout: 120 * time.Second}
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
