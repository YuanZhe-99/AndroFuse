package vm

import (
	"archive/zip"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const googleRepoManifestURL = "https://dl.google.com/android/repository/repository2-3.xml"

// CmdlineArchive identifies a verified Android command-line tools archive from repository2-3.xml.
type CmdlineArchive struct {
	URL          string
	ChecksumType string
	ChecksumHex  string
	Size         int64
}

type xmlRepoManifest struct {
	Packages []xmlRemotePackage `xml:"remotePackage"`
}

type xmlRemotePackage struct {
	Path     string       `xml:"path,attr"`
	Archives []xmlArchive `xml:"archives>archive"`
}

type xmlArchive struct {
	HostOS   string      `xml:"host-os"`
	Complete xmlComplete `xml:"complete"`
}

type xmlComplete struct {
	Size     int64       `xml:"size"`
	Checksum xmlChecksum `xml:"checksum"`
	URL      string      `xml:"url"`
}

type xmlChecksum struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

// ParseCmdlineToolsManifest parses Google's `repository2-3.xml` bytes and extracts the latest
// Windows `cmdline-tools;latest` download URL and checksum.
func ParseCmdlineToolsManifest(xmlBytes []byte) (CmdlineArchive, error) {
	var man xmlRepoManifest
	if err := xml.Unmarshal(xmlBytes, &man); err != nil {
		return CmdlineArchive{}, fmt.Errorf("parse Android repository manifest: %w", err)
	}
	var fallback *CmdlineArchive
	for _, pkg := range man.Packages {
		if pkg.Path != "cmdline-tools;latest" && !strings.HasPrefix(pkg.Path, "cmdline-tools;") {
			continue
		}
		for _, ar := range pkg.Archives {
			if !strings.EqualFold(strings.TrimSpace(ar.HostOS), "windows") {
				continue
			}
			urlStr := strings.TrimSpace(ar.Complete.URL)
			if urlStr == "" {
				continue
			}
			if !strings.HasPrefix(urlStr, "http") {
				urlStr = "https://dl.google.com/android/repository/" + strings.TrimLeft(urlStr, "/")
			}
			cand := CmdlineArchive{
				URL:          urlStr,
				ChecksumType: strings.ToLower(strings.TrimSpace(ar.Complete.Checksum.Type)),
				ChecksumHex:  strings.ToLower(strings.TrimSpace(ar.Complete.Checksum.Value)),
				Size:         ar.Complete.Size,
			}
			if cand.ChecksumType == "" {
				cand.ChecksumType = "sha1"
			}
			if pkg.Path == "cmdline-tools;latest" {
				return cand, nil
			}
			if fallback == nil {
				cp := cand
				fallback = &cp
			}
		}
	}
	if fallback != nil {
		return *fallback, nil
	}
	return CmdlineArchive{}, errors.New("Windows cmdline-tools package not found in Android repository manifest")
}

// Provision downloads (if missing) Android cmdline-tools, platform-tools, emulator, and the
// native-ABI system image into `%LOCALAPPDATA%\AndroFuse\vm` (or host SDK if configured),
// creating avdName cleanly with checksum verification (`INV-10`).
func (m *Manager) Provision(ctx context.Context, avdName string) error {
	abi, sysImgPkg, err := NativeABIForArch(runtime.GOARCH)
	if err != nil {
		m.failState(err)
		return err
	}
	avdName = strings.TrimSpace(avdName)
	if avdName == "" {
		avdName = DefaultAVDName
	}

	provCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	if m.state == StateDownloading || m.state == StateStarting {
		m.mu.Unlock()
		cancel()
		return fmt.Errorf("VM operation already in progress (%s)", m.state)
	}
	m.cancelProv = cancel
	m.state = StateDownloading
	m.activeAVD = avdName
	m.progressPct = 5
	m.progressMsg = fmt.Sprintf("Preparing native %s VM (%s)...", abi, avdName)
	m.lastErr = ""
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		m.cancelProv = nil
		m.mu.Unlock()
	}()
	m.emitStatus(provCtx)

	managedSDK := filepath.Join(m.vmRoot, "sdk")
	managedAVD := filepath.Join(m.vmRoot, "avd")
	if err := os.MkdirAll(managedSDK, 0o755); err != nil {
		m.failState(err)
		return err
	}
	if err := os.MkdirAll(managedAVD, 0o755); err != nil {
		m.failState(err)
		return err
	}

	sdkMgr, avdMgr, sdkRoot, avdHome := m.resolveSDKTools(managedSDK, managedAVD)
	if sdkMgr == "" || avdMgr == "" {
		m.setProgress(10, "Fetching Android repository manifest...")
		if err := m.installCmdlineTools(provCtx, managedSDK); err != nil {
			m.failState(err)
			return err
		}
		sdkMgr = filepath.Join(managedSDK, "cmdline-tools", "latest", "bin", sdkManagerName())
		avdMgr = filepath.Join(managedSDK, "cmdline-tools", "latest", "bin", avdManagerName())
		sdkRoot = managedSDK
		avdHome = managedAVD
	}

	m.setProgress(35, "Installing platform-tools, emulator & "+sysImgPkg+"...")
	if err := runToolWithLicenses(provCtx, sdkMgr, sdkRoot, avdHome,
		"--sdk_root="+sdkRoot, "platform-tools", "emulator", sysImgPkg); err != nil {
		m.failState(fmt.Errorf("sdkmanager install: %w", err))
		return err
	}

	m.setProgress(85, "Creating Android VM profile "+avdName+"...")
	if err := runAVDCreate(provCtx, avdMgr, sdkRoot, avdHome, avdName, sysImgPkg); err != nil {
		m.failState(fmt.Errorf("avdmanager create: %w", err))
		return err
	}
	configureAVDefaults(filepath.Join(avdHome, avdName+".avd", "config.ini"))

	m.mu.Lock()
	m.state = StateStopped
	m.progressPct = 100
	m.progressMsg = "Built-in Android VM ready (" + avdName + ")"
	m.lastErr = ""
	m.mu.Unlock()
	m.emitStatus(context.Background())
	return nil
}

func (m *Manager) setProgress(pct int, msg string) {
	m.mu.Lock()
	m.progressPct = pct
	m.progressMsg = msg
	m.mu.Unlock()
	m.emitStatus(context.Background())
}

func (m *Manager) resolveSDKTools(managedSDK, managedAVD string) (sdkMgr, avdMgr, sdkRoot, avdHome string) {
	mSDK := filepath.Join(managedSDK, "cmdline-tools", "latest", "bin", sdkManagerName())
	mAVD := filepath.Join(managedSDK, "cmdline-tools", "latest", "bin", avdManagerName())
	if fileExists(mSDK) && fileExists(mAVD) {
		return mSDK, mAVD, managedSDK, managedAVD
	}
	for _, host := range hostSDKCandidates() {
		hSDK := filepath.Join(host, "cmdline-tools", "latest", "bin", sdkManagerName())
		hAVD := filepath.Join(host, "cmdline-tools", "latest", "bin", avdManagerName())
		if fileExists(hSDK) && fileExists(hAVD) {
			return hSDK, hAVD, host, managedAVD
		}
	}
	return "", "", managedSDK, managedAVD
}

func (m *Manager) installCmdlineTools(ctx context.Context, sdkRoot string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleRepoManifestURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("manifest HTTP %s", resp.Status)
	}
	manBytes, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	arch, err := ParseCmdlineToolsManifest(manBytes)
	if err != nil {
		return err
	}

	tmpZip := filepath.Join(m.vmRoot, "cmdline-tools.zip.tmp")
	if err := m.downloadAndVerify(ctx, client, arch, tmpZip); err != nil {
		_ = os.Remove(tmpZip)
		return err
	}
	defer os.Remove(tmpZip)

	destDir := filepath.Join(sdkRoot, "cmdline-tools", "latest")
	_ = os.RemoveAll(destDir)
	m.setProgress(28, "Extracting Android SDK cmdline-tools...")
	return extractZipSubdir(tmpZip, "cmdline-tools", destDir)
}

func (m *Manager) downloadAndVerify(ctx context.Context, client *http.Client, arch CmdlineArchive, dstPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, arch.URL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download cmdline-tools: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download cmdline-tools HTTP %s", resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer out.Close()

	h256 := sha256.New()
	h1 := sha1.New()
	mw := io.MultiWriter(out, h256, h1)

	total := arch.Size
	if total <= 0 {
		total = resp.ContentLength
	}
	buf := make([]byte, 64*1024)
	var done int64
	lastPct := 12
	for {
		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := mw.Write(buf[:n]); wErr != nil {
				return wErr
			}
			done += int64(n)
			if total > 0 {
				pct := 12 + int(done*15/total)
				if pct > lastPct {
					lastPct = pct
					m.setProgress(pct, fmt.Sprintf("Downloading SDK tools (%d/%d MB)...", done>>20, total>>20))
				}
			}
		}
		if rErr == io.EOF {
			break
		}
		if rErr != nil {
			return rErr
		}
	}

	if arch.ChecksumHex != "" {
		got := ""
		switch arch.ChecksumType {
		case "sha256":
			got = hex.EncodeToString(h256.Sum(nil))
		default:
			got = hex.EncodeToString(h1.Sum(nil))
		}
		if !strings.EqualFold(got, arch.ChecksumHex) {
			return fmt.Errorf("%s checksum mismatch: got %s want %s", arch.ChecksumType, got, arch.ChecksumHex)
		}
	}
	return nil
}

func extractZipSubdir(zipPath, stripPrefix, destDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	cleanDest := filepath.Clean(destDir)
	prefix := strings.TrimSuffix(stripPrefix, "/") + "/"

	for _, f := range zr.File {
		rel := strings.TrimPrefix(f.Name, prefix)
		rel = filepath.Clean(filepath.FromSlash(rel))
		if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			continue
		}
		outPath := filepath.Join(cleanDest, rel)
		if !strings.HasPrefix(outPath, cleanDest+string(filepath.Separator)) {
			return fmt.Errorf("unsafe zip entry %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(outPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode()|0o644)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(dst, io.LimitReader(rc, 512<<20))
		closeErr := dst.Close()
		_ = rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func runToolWithLicenses(ctx context.Context, toolPath, sdkRoot, avdHome string, args ...string) error {
	cmd := exec.CommandContext(ctx, toolPath, args...)
	hideConsoleWindow(cmd)
	cmd.Env = buildSDKEvn(sdkRoot, avdHome)
	cmd.Stdin = strings.NewReader(strings.Repeat("y\n", 16))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runAVDCreate(ctx context.Context, avdMgr, sdkRoot, avdHome, avdName, sysImgPkg string) error {
	cmd := exec.CommandContext(ctx, avdMgr, "create", "avd", "--force", "--name", avdName, "--package", sysImgPkg, "--device", "pixel_6")
	hideConsoleWindow(cmd)
	cmd.Env = buildSDKEvn(sdkRoot, avdHome)
	cmd.Stdin = strings.NewReader("no\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func configureAVDefaults(configPath string) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	extras := []string{
		"hw.keyboard=yes",
		"hw.ramSize=4096",
		"hw.lcd.width=1080",
		"hw.lcd.height=2400",
		"hw.lcd.density=420",
		"disk.dataPartition.size=4096M",
	}
	text := string(raw)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	for _, line := range extras {
		key, _, _ := strings.Cut(line, "=")
		if !strings.Contains(text, key+"=") {
			text += line + "\n"
		}
	}
	_ = os.WriteFile(configPath, []byte(text), 0o600)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func sdkManagerName() string {
	if runtime.GOOS == "windows" {
		return "sdkmanager.bat"
	}
	return "sdkmanager"
}

func avdManagerName() string {
	if runtime.GOOS == "windows" {
		return "avdmanager.bat"
	}
	return "avdmanager"
}
