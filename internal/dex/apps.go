package dex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	cmd := fmt.Sprintf("CLASSPATH=%s app_process / com.androfuse.dexhelper.Main apps", RemoteHelperPath)
	out, err := client.Shell(callCtx, serial, cmd)
	if err != nil {
		return nil, fmt.Errorf("dex helper apps: %w (%s)", err, out)
	}
	return ParseAppsOutput(out, iconCacheDir)
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
