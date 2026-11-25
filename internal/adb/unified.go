package adb

import "sort"

// UnifiedDevice collapses multiple ADB transports (e.g. USB wired and TLS Wi-Fi)
// pointing at the same physical Android hardware into a single logical device (`INV-02`).
type UnifiedDevice struct {
	HardwareHash string        `json:"hardware_hash"`
	Primary      Device        `json:"primary"`
	Transports   []Device      `json:"transports"`
	Profile      DeviceProfile `json:"profile"`
	Probed       bool          `json:"probed"`
}

// TransportPriority ranks ADB transports from lowest latency / highest stability to fallback.
func TransportPriority(t Transport) int {
	switch t {
	case TransportUSB:
		return 0
	case TransportEmulator:
		return 1
	case TransportTLSWiFi:
		return 2
	case TransportTCP:
		return 3
	default:
		return 4
	}
}

// MergeDevices deduplicates devs by probed HardwareHash (`ro.serialno` hash) when online,
// sorting each device's transports by TransportPriority and choosing Transports[0] as Primary (`INV-02`).
//
// Inputs: raw `adb devices -l` slice and optional per-serial DeviceProfile map (may be nil).
// Returns: deterministically ordered UnifiedDevice slice keeping offline/unauthorized entries distinct.
func MergeDevices(devs []Device, profiles map[string]DeviceProfile) []UnifiedDevice {
	groups := make(map[string]*UnifiedDevice, len(devs))
	order := make([]string, 0, len(devs))

	for _, d := range devs {
		key := d.DeviceHash
		var prof DeviceProfile
		hasProf := false
		if profiles != nil && d.State == "device" {
			if p, ok := profiles[d.Serial]; ok {
				prof = p
				hasProf = true
				if p.HardwareHash != "" {
					key = p.HardwareHash
				}
			}
		}
		if d.State != "device" {
			// Keep offline/unauthorized targets separate until ADB authorizes and probes them.
			key = "unready:" + string(d.Transport) + ":" + d.DeviceHash
		}
		g, exists := groups[key]
		if !exists {
			hw := key
			if hasProf && prof.HardwareHash != "" {
				hw = prof.HardwareHash
			} else if d.State != "device" {
				hw = d.DeviceHash
			}
			g = &UnifiedDevice{
				HardwareHash: hw,
			}
			groups[key] = g
			order = append(order, key)
		}
		g.Transports = append(g.Transports, d)
		if hasProf && (!g.Probed || TransportPriority(d.Transport) < TransportPriority(g.Primary.Transport) || g.Primary.Serial == "") {
			g.Profile = prof
			g.Probed = true
		}
	}

	out := make([]UnifiedDevice, 0, len(order))
	for _, k := range order {
		g := groups[k]
		sort.Slice(g.Transports, func(i, j int) bool {
			pi := TransportPriority(g.Transports[i].Transport)
			pj := TransportPriority(g.Transports[j].Transport)
			if pi != pj {
				return pi < pj
			}
			return g.Transports[i].Serial < g.Transports[j].Serial
		})
		g.Primary = g.Transports[0]
		if g.Probed {
			if p, ok := profiles[g.Primary.Serial]; ok {
				g.Profile = p
			}
		}
		out = append(out, *g)
	}

	sort.Slice(out, func(i, j int) bool {
		pi := TransportPriority(out[i].Primary.Transport)
		pj := TransportPriority(out[j].Primary.Transport)
		if pi != pj {
			return pi < pj
		}
		if out[i].Primary.Model != out[j].Primary.Model {
			return out[i].Primary.Model < out[j].Primary.Model
		}
		return out[i].HardwareHash < out[j].HardwareHash
	})
	return out
}

// HasTransport reports whether this unified device currently exposes transport t in an online state.
func (u UnifiedDevice) HasTransport(t Transport) bool {
	for _, d := range u.Transports {
		if d.State == "device" && d.Transport == t {
			return true
		}
	}
	return false
}

// AlternateTransport returns the highest-priority online transport whose Serial differs from excludeSerial.
func (u UnifiedDevice) AlternateTransport(excludeSerial string) (Device, bool) {
	for _, d := range u.Transports {
		if d.State == "device" && d.Serial != excludeSerial {
			return d, true
		}
	}
	return Device{}, false
}
