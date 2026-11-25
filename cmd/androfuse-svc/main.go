// Command androfuse-svc is the headless per-user engine for AndroFuse.
// It provides version reporting, host capability self-checks, and read-only
// Samsung DeX / Miracast / ADB device probes via androfuse-core.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/core"
	"github.com/YuanZhe-99/AndroFuse/internal/dex"
	"github.com/YuanZhe-99/AndroFuse/internal/gui"
	"github.com/YuanZhe-99/AndroFuse/internal/lease"
	"github.com/YuanZhe-99/AndroFuse/internal/localapi"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
	"github.com/YuanZhe-99/AndroFuse/internal/vm"
)

type probeReport struct {
	Host     gui.HostDiagnostics          `json:"host"`
	Miracast *core.MiracastSinkTestResult `json:"miracast,omitempty"`
	Devices  []deviceProbeSummary         `json:"devices,omitempty"`
}

type deviceProbeSummary struct {
	Device  adb.Device         `json:"device"`
	Profile *adb.DeviceProfile `json:"profile,omitempty"`
	DeX     *dex.ProbeReport   `json:"dex,omitempty"`
	Error   string             `json:"error,omitempty"`
}

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
		stateDir := resolveStateDir()
		diag := gui.RunDiagnostics(stateDir, os.Getenv("ANDROFUSE_PROFILE"))
		if jsonOut {
			writeJSON(diag)
			return
		}
		printHostCheck(diag)
	case "probe":
		jsonOut := hasFlag(os.Args[2:], "--json")
		testSink := hasFlag(os.Args[2:], "--test-miracast")
		rep := runFullProbe(testSink)
		if jsonOut {
			writeJSON(rep)
			return
		}
		printHostCheck(rep.Host)
		if rep.Miracast != nil {
			fmt.Printf("Miracast sink test: port7236=%v started=%v wfd_open=%v ie_enable=%d ie_sink=%d cleaned=%v\n",
				rep.Miracast.Port7236Available, rep.Miracast.PublisherStarted, rep.Miracast.WFDHandleOpened,
				rep.Miracast.IEEnableError, rep.Miracast.IESinkError, rep.Miracast.CleanedUp)
		}
		for _, d := range rep.Devices {
			fmt.Printf("Device %s (%s, %s)\n", d.Device.MaskedID, d.Device.Model, d.Device.Transport)
			if d.Profile != nil {
				fmt.Printf("  Android %s (SDK %d) OneUI=%s SEM=%s SoC=%s UHID=%v\n",
					d.Profile.AndroidRelease, d.Profile.SDKInt, d.Profile.OneUIVersion,
					d.Profile.SemVersion, d.Profile.SoC, d.Profile.HasUHID)
			}
			if d.DeX != nil {
				fmt.Printf("  DeX SemWifiDisplayConfig: builder=%v connect=%v disconnect=%v\n",
					d.DeX.HasSemBuilder, d.DeX.HasConnectWithConfig, d.DeX.HasDisconnectWifiDisplay)
			}
			if d.Error != "" {
				fmt.Printf("  Probe error: %s\n", d.Error)
			}
		}
	case "run":
		if err := runDaemon(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		printUsage()
		os.Exit(2)
	}
}

func runDaemon(args []string) error {
	sockPath := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--socket" && i+1 < len(args) {
			sockPath = args[i+1]
			i++
		}
	}
	stateDir := resolveStateDir()
	profile := os.Getenv("ANDROFUSE_PROFILE")
	store, err := lease.Open(filepath.Join(stateDir, "leases.json"))
	if err != nil {
		return err
	}
	adbClient, _ := adb.FindClient()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	localapi.RecoverLeases(ctx, adbClient, store)

	vmMgr := vm.NewManager(vm.DefaultVMRoot(), adbClient, nil)
	defer vmMgr.Stop(context.Background())

	srv, err := localapi.Listen(localapi.Options{
		SocketPath: sockPath,
		StateDir:   stateDir,
		Profile:    profile,
		Leases:     store,
		ADB:        adbClient,
		VM:         vmMgr,
	})
	if err != nil {
		return err
	}
	fmt.Printf("androfuse-svc listening on %s\n", srv.SocketPath())
	return srv.Serve(ctx)
}

func runFullProbe(testMiracast bool) probeReport {
	stateDir := resolveStateDir()
	host := gui.RunDiagnostics(stateDir, os.Getenv("ANDROFUSE_PROFILE"))
	rep := probeReport{Host: host}

	if testMiracast {
		if libPath := gui.LocateCoreLibrary(); libPath != "" {
			if lib, err := core.Open(libPath); err == nil {
				defer lib.Stop()
				if _, err := lib.Start(core.Config{
					Version:  version.Version,
					StateDir: stateDir,
					Profile:  os.Getenv("ANDROFUSE_PROFILE"),
				}); err == nil {
					if mRes, err := lib.MiracastTestSink(); err == nil {
						rep.Miracast = &mRes
					}
				}
			}
		}
	}

	if client, err := adb.FindClient(); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if devs, err := client.Devices(ctx); err == nil {
			for _, dev := range devs {
				item := deviceProbeSummary{Device: dev}
				if dev.State != "device" {
					item.Error = "device state: " + dev.State
					rep.Devices = append(rep.Devices, item)
					continue
				}
				if prof, pErr := client.ProbeDevice(ctx, dev.Serial); pErr != nil {
					item.Error = pErr.Error()
				} else {
					item.Profile = &prof
				}
				if dexRep, dErr := dex.PushAndProbe(ctx, client, dev.Serial); dErr == nil {
					item.DeX = &dexRep
				}
				rep.Devices = append(rep.Devices, item)
			}
		}
	}
	return rep
}

func resolveStateDir() string {
	stateDir, err := os.UserConfigDir()
	if err != nil {
		stateDir = os.TempDir()
	}
	return filepath.Join(stateDir, "AndroFuse")
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func writeJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func printHostCheck(diag gui.HostDiagnostics) {
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
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: androfuse-svc <version|check [--json]|probe [--json]|run [--socket <path>]>")
}
