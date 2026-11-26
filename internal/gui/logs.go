package gui

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/version"
	L "github.com/YuanZhe-99/AndroFuse/locales"
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

// errLogMax is how many recent failures Save logs keeps.
const errLogMax = 50

// errLog holds the recent failures in full for Save logs, while the interface shows plain words.
type errLog struct {
	mu      sync.Mutex
	entries []string
}

// ipv4 matches an IPv4 address, which the log masks unless it is loopback (`INV-08`).
var ipv4 = regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}\b`)

// failed keeps err, with what was being done, for Save logs and returns what the interface says
// instead: plain words for the failure, then where the details are.
// Notes: takes its own lock, so it may run inside a.update.
func (a *App) failed(plain string, err error) string {
	msg := ipv4.ReplaceAllStringFunc(err.Error(), func(ip string) string {
		if strings.HasPrefix(ip, "127.") {
			return ip
		}
		return "x.x.x.x"
	})
	a.errs.mu.Lock()
	a.errs.entries = append(a.errs.entries, time.Now().Format(time.RFC3339)+" "+plain+": "+msg)
	if len(a.errs.entries) > errLogMax {
		a.errs.entries = a.errs.entries[len(a.errs.entries)-errLogMax:]
	}
	a.errs.mu.Unlock()
	return plain + " " + L.FailSeeLogs()
}

// recentErrors returns a copy of the kept failures, oldest first.
func (a *App) recentErrors() []string {
	a.errs.mu.Lock()
	defer a.errs.mu.Unlock()
	return append([]string(nil), a.errs.entries...)
}
