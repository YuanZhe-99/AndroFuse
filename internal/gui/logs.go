package gui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
)

// writeLogs writes what helps diagnose a problem into one text file: the version, this computer's
// checks, the devices (serials and addresses masked), the networks' states and the local
// assistant's recent actions. It never writes a secret, token or full address (`INV-08`).
func writeLogs(path string, a *App) error {
	a.mu.Lock()
	d, devs, mcpSt := a.diag, append([]DiscoveredDevice(nil), a.devices...), a.mcpStatus
	views := make(map[string]netView, len(a.netViews))
	for id, v := range a.netViews {
		views[id] = v
	}
	notice, compNotice := a.deviceNotice, a.compNotice
	a.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "AndroFuse %s — %s\n\n", version.Version, time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "[this computer]\nos=%s build=%d arch=%s emulated=%v\ncore=%v abi=%d adb=%v %s\nh264hw=%v hevchw=%v\n",
		d.SelfCheck.OS, d.SelfCheck.OSBuild, d.SelfCheck.NativeArch, d.SelfCheck.IsEmulated,
		d.CoreLoaded, d.CoreABI, d.ADBFound, d.ADBVersion, d.SelfCheck.H264HWDecoder, d.SelfCheck.HEVCHWDecoder)
	for _, n := range d.SelfCheck.Notes {
		fmt.Fprintf(&b, "note: %s\n", n)
	}
	b.WriteString("\n[devices]\n")
	for _, dv := range devs {
		var trs []string
		for _, tr := range dv.Transports {
			trs = append(trs, fmt.Sprintf("%s/%s/%s", tr.Transport, adb.MaskEndpoint(tr.Serial), tr.State))
		}
		fmt.Fprintf(&b, "%s maker=%s model=%s android=%s emulator=%v route=%s transports=%s probe_err=%q\n",
			adb.MaskSerial(dv.HardwareHash), dv.Profile.Manufacturer, dv.Profile.Model, dv.Profile.AndroidRelease,
			isEmulator(dv), dv.Route.Label, strings.Join(trs, ","), dv.ProbeErr)
	}
	b.WriteString("\n[networks]\n")
	for _, n := range a.networks() {
		v := views[n.ID]
		fmt.Fprintf(&b, "%s join=%v running=%v peers=%d nat=%s err=%q\n", n.ID, n.Join, v.Status.Running, len(v.Info.Peers), v.Info.NAT, v.Err+v.Status.LastError)
	}
	b.WriteString("\n[local assistant]\n")
	fmt.Fprintf(&b, "enabled=%v\n", mcpSt.Enabled)
	for _, e := range mcpSt.Audit {
		fmt.Fprintf(&b, "%s %s %s allowed=%v %s\n", e.Timestamp.Format(time.RFC3339), e.Method, e.Tool, e.Allowed, e.Outcome)
	}
	fmt.Fprintf(&b, "\n[notices]\ndevices=%q\ncompanion=%q\n", notice, compNotice)
	return os.WriteFile(path, []byte(b.String()), 0o600)
}
