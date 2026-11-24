// Command androfuse-svc is the headless per-user engine for AndroFuse.
// In M0 it provides version reporting and real host capability self-checks
// via the Rust core library (core/androfuse-core).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/YuanZhe-99/AndroFuse/internal/gui"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version.Version)
	case "check":
		jsonOut := len(os.Args) > 2 && os.Args[2] == "--json"
		stateDir, err := os.UserConfigDir()
		if err != nil {
			stateDir = os.TempDir()
		}
		diag := gui.RunDiagnostics(filepath.Join(stateDir, "AndroFuse"), os.Getenv("ANDROFUSE_PROFILE"))
		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(diag); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		fmt.Printf("AndroFuse %s\n", version.Version)
		fmt.Printf("Core loaded: %v (ABI v%d)\n", diag.CoreLoaded, diag.CoreABI)
		if diag.CoreError != "" {
			fmt.Printf("Core error: %s\n", diag.CoreError)
		}
		fmt.Printf("Arch: process=%s native=%s emulated=%v os=%s build=%d\n",
			diag.SelfCheck.ProcessArch, diag.SelfCheck.NativeArch, diag.SelfCheck.IsEmulated,
			diag.SelfCheck.OS, diag.SelfCheck.OSBuild)
		fmt.Printf("Media Foundation: present=%v h264_hw=%v hevc_hw=%v\n",
			diag.SelfCheck.MediaFoundationPresent, diag.SelfCheck.H264HWDecoder, diag.SelfCheck.HEVCHWDecoder)
		fmt.Printf("Wi-Fi Direct: api=%v miracast_ie=%v\n",
			diag.SelfCheck.WiFiDirectAPI, diag.SelfCheck.MiracastIEProvider)
		fmt.Printf("ADB: found=%v (%s)\n", diag.ADBFound, diag.ADBVersion)
	default:
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: androfuse-svc <version|check [--json]>")
}
