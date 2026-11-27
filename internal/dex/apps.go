package dex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
)

type rawAppRecord struct {
	Package    string `json:"package"`
	Activity   string `json:"activity"`
	Label      string `json:"label"`
	System     bool   `json:"system"`
	Resizeable bool   `json:"resizeable"`
	IconB64    string `json:"icon_b64"`
}

// ListAppsWithIcons pushes androfuse-dex-helper.jar if needed, queries real localized
// launcher app labels, resizeability flags, and 96x96 PNG icons from user 0, and
// caches decoded icon PNGs into iconCacheDir.
//
// Inputs: ctx, adb client, device serial, local directory for cached PNG icons.
// Returns: deterministically sorted AppEntry slice with local IconPath populated.
// Notes: when an app's icon aborts the helper (it prints AFDEX1|ICON|pkg before each), the
// helper runs again with --resume pkg, at most three times, and that app is listed without one.
func ListAppsWithIcons(ctx context.Context, client *adb.Client, serial, iconCacheDir string) ([]adb.AppEntry, error) {
	if client == nil {
		return nil, fmt.Errorf("adb client is required")
	}
	jar, err := FindHelperJar()
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()

	if err := client.PushFile(callCtx, serial, jar, RemoteHelperPath); err != nil {
		return nil, fmt.Errorf("push dex helper for apps: %w", err)
	}
	_, _ = client.Shell(callCtx, serial, "chmod 600 "+RemoteHelperPath+" 2>/dev/null || true")
	var all strings.Builder
	resume := ""
	for range 4 {
		cmd := fmt.Sprintf("CLASSPATH=%s app_process / com.androfuse.dexhelper.Main apps", RemoteHelperPath)
		if resume != "" {
			cmd += " --resume " + resume
		}
		out, err := client.Shell(callCtx, serial, cmd)
		all.WriteString(out)
		all.WriteByte('\n')
		if err == nil || strings.Contains(out, "AFDEX1|APPS_DONE|") {
			break
		}
		// The helper died drawing an icon: list that app without one and go on after it.
		next := abortedIcon(out)
		if next == "" || next == resume {
			if strings.Contains(all.String(), "AFDEX1|APP|") {
				break
			}
			return nil, fmt.Errorf("dex helper apps: %w (%s)", err, out)
		}
		resume = next
	}
	return ParseAppsOutput(all.String(), iconCacheDir)
}

// packageName matches an Android package name, the only thing passed back to the phone's shell.
var packageName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)

// abortedIcon returns the package whose icon the helper was drawing when it stopped: the last
// output line when it is an AFDEX1|ICON line, else "".
func abortedIcon(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(l, "AFDEX1|") {
			continue // a native abort message after the marker
		}
		if pkg, ok := strings.CutPrefix(l, "AFDEX1|ICON|"); ok && packageName.MatchString(pkg) {
			return pkg
		}
		return ""
	}
	return ""
}

// ParseAppsOutput parses `AFDEX1|APP|{json}` lines emitted by `Main apps`, filters
// noisy WebAPK/test packages, writes PNG icons into iconCacheDir, and sorts by label.
func ParseAppsOutput(raw, iconCacheDir string) ([]adb.AppEntry, error) {
	if iconCacheDir != "" {
		if err := os.MkdirAll(iconCacheDir, 0o755); err != nil {
			return nil, fmt.Errorf("create icon cache dir: %w", err)
		}
	}

	seen := make(map[string]bool)
	var apps []adb.AppEntry
	sawMarker := false

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "AFDEX1|APPS_DONE|") {
			sawMarker = true
			continue
		}
		payload, ok := strings.CutPrefix(line, "AFDEX1|APP|")
		if !ok {
			continue
		}
		sawMarker = true
		var rec rawAppRecord
		if err := json.Unmarshal([]byte(payload), &rec); err != nil {
			continue
		}
		pkg := strings.TrimSpace(rec.Package)
		if pkg == "" || adb.IsNoisyAppPackage(pkg) || seen[pkg] {
			continue
		}
		seen[pkg] = true

		label := strings.TrimSpace(rec.Label)
		if label == "" {
			label = pkg
		}
		var iconPath string
		if iconCacheDir != "" && rec.IconB64 != "" {
			if pngBytes, err := base64.StdEncoding.DecodeString(rec.IconB64); err == nil && len(pngBytes) > 0 {
				dst := filepath.Join(iconCacheDir, sanitizePkgFilename(pkg)+".png")
				tmp := dst + ".tmp"
				if err := os.WriteFile(tmp, pngBytes, 0o600); err == nil {
					if err := os.Rename(tmp, dst); err == nil {
						iconPath = dst
					} else {
						_ = os.Remove(tmp)
					}
				}
			}
		}

		apps = append(apps, adb.AppEntry{
			Package:    pkg,
			Activity:   strings.TrimSpace(rec.Activity),
			Label:      label,
			IconPath:   iconPath,
			System:     rec.System,
			Resizeable: rec.Resizeable,
		})
	}

	if !sawMarker {
		return nil, fmt.Errorf("missing AFDEX1|APP output from dex helper")
	}

	sort.Slice(apps, func(i, j int) bool {
		li := strings.ToLower(apps[i].Label)
		lj := strings.ToLower(apps[j].Label)
		if li != lj {
			return li < lj
		}
		return apps[i].Package < apps[j].Package
	})
	return apps, nil
}

func sanitizePkgFilename(pkg string) string {
	var b strings.Builder
	b.Grow(len(pkg))
	for i := 0; i < len(pkg); i++ {
		c := pkg[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "app"
	}
	return b.String()
}
