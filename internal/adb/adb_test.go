package adb

import "testing"

func TestMaskAndHash(t *testing.T) {
	if got := MaskSerial("ABC123456XYZ"); got != "ABC****XYZ" {
		t.Fatalf("MaskSerial = %q", got)
	}
	if got := MaskSerial("short"); got != "****" {
		t.Fatalf("MaskSerial(short) = %q", got)
	}
	if h := DeviceHash("ABC123456XYZ"); len(h) != 16 {
		t.Fatalf("DeviceHash length = %d, want 16", len(h))
	}
}

func TestParseDevicesOutputAndProbe(t *testing.T) {
	raw := `* daemon started successfully
List of devices attached
ABC123456XYZ           device product:fold_prod model:SM_F971U1 device:fold_dev transport_id:1
adb-XYZ._adb-tls-connect._tcp device product:p2 model:Pixel device:p2 transport_id:2
`
	devs := ParseDevicesOutput(raw)
	if len(devs) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devs))
	}
	if devs[0].Transport != TransportUSB || devs[0].MaskedID != "ABC****XYZ" {
		t.Fatalf("unexpected usb device: %+v", devs[0])
	}
	if devs[1].Transport != TransportTLSWiFi {
		t.Fatalf("unexpected tls wifi device: %+v", devs[1])
	}

	probeRaw := `MFR=samsung
MOD=SM-F971U1
REL=17
SDK=37
ONEUI=90000
SEM=3701
SOC=SM8850
UHID=1
DESKPKG=0
mCurrentStatus=WifiDisplayStatus{featureState=2, scanState=0, activeDisplayState=0}
mDisplayId=0
mDisplayId=1
---CODECS---
AVC=            <Limit name="concurrent-instances" max="16" />
HEVC=            <Limit name="concurrent-instances" max="16" />
`
	p := ParseDeviceProbe("ABC123456XYZ", probeRaw)
	if p.Manufacturer != "samsung" || p.SDKInt != 37 || !p.HasUHID || p.HasDeskLauncher {
		t.Fatalf("unexpected profile: %+v", p)
	}
	if p.AVCEncoderMax != 16 || p.HEVCEncoderMax != 16 || p.WifiDisplayState != 0 {
		t.Fatalf("unexpected codecs/wfd: %+v", p)
	}
	if len(p.ActiveDisplays) != 2 || p.ActiveDisplays[0] != 0 || p.ActiveDisplays[1] != 1 {
		t.Fatalf("unexpected ActiveDisplays: %+v", p.ActiveDisplays)
	}
}

func TestWirelessMDNSMaskEndpointAndApps(t *testing.T) {
	if got := MaskEndpoint("192.168.12.34:41235"); got != "192.*.*.*:41235" {
		t.Fatalf("MaskEndpoint(ipv4) = %q", got)
	}
	if got := MaskEndpoint("[fe80::1]:37123"); got != "[****]:37123" {
		t.Fatalf("MaskEndpoint(ipv6) = %q", got)
	}

	rawDevs := `List of devices attached
emulator-5554          device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:4
192.168.12.34:41235    device product:q2q model:SM_F971U1 device:q2q transport_id:5
192.168.12.34:5555     device product:q2q model:SM_F971U1 device:q2q transport_id:6
`
	devs := ParseDevicesOutput(rawDevs)
	if len(devs) != 3 {
		t.Fatalf("expected 3 devices, got %d", len(devs))
	}
	if devs[0].Transport != TransportEmulator {
		t.Fatalf("expected emulator transport, got %q", devs[0].Transport)
	}
	if devs[1].Transport != TransportTLSWiFi || devs[1].MaskedID != "192.*.*.*:41235" {
		t.Fatalf("expected tls_wifi masked endpoint, got %+v", devs[1])
	}
	if devs[2].Transport != TransportTCP {
		t.Fatalf("expected tcp fallback transport for :5555, got %q", devs[2].Transport)
	}

	mdnsRaw := `List of discovered mdns services
adb-XYZ0001-abCDeF      _adb-tls-connect._tcp   192.168.12.34:41235
adb-XYZ0001-pair        _adb-tls-pairing._tcp   192.168.12.34:39111
other-svc               _http._tcp              192.168.12.34:8080
`
	svcs := ParseMDNSServices(mdnsRaw)
	if len(svcs) != 2 || !svcs[0].CanConnect || !svcs[1].CanPair {
		t.Fatalf("unexpected mDNS parse: %+v", svcs)
	}
	if svcs[0].MaskedAddr != "192.*.*.*:41235" {
		t.Fatalf("unexpected mDNS masked addr: %q", svcs[0].MaskedAddr)
	}

	appsRaw := `Activity #0:
  com.android.settings/.Settings
  priority=0
com.sec.android.app.popupcalculator/.Calculator
com.sec.android.app.clockpackage/.ClockPackage
package:com.example.demo
`
	apps := ParseLaunchableApps(appsRaw)
	if len(apps) != 4 {
		t.Fatalf("expected 4 apps, got %d (%+v)", len(apps), apps)
	}
}

func TestMergeDevicesMultiTransport(t *testing.T) {
	raw := `List of devices attached
192.168.1.50:41235     device product:q2q model:SM_F971U1 device:q2q transport_id:2
R3CW1234567            device product:q2q model:SM_F971U1 device:q2q transport_id:1
R5CT9999999            unauthorized transport_id:3
emulator-5554          device product:sdk_gphone64_arm64 model:Pixel_9a device:emu64a transport_id:4
`
	devs := ParseDevicesOutput(raw)
	foldUSB := ParseDeviceProbe("R3CW1234567", "HWSER=R3CW1234567\nMFR=samsung\nMOD=SM-F971U1\nONEUI=90000\nAVC=            <Limit name=\"concurrent-instances\" max=\"16\" />\n")
	foldWiFi := ParseDeviceProbe("192.168.1.50:41235", "HWSER=R3CW1234567\nMFR=samsung\nMOD=SM-F971U1\nONEUI=90000\n")
	emuProf := ParseDeviceProbe("emulator-5554", "HWSER=EMULATOR5554\nMFR=Google\nMOD=Pixel_9a\n")

	if foldUSB.HardwareHash == "" || foldUSB.HardwareHash != foldWiFi.HardwareHash {
		t.Fatalf("expected matching HardwareHash across USB and Wi-Fi: usb=%q wifi=%q", foldUSB.HardwareHash, foldWiFi.HardwareHash)
	}

	merged := MergeDevices(devs, map[string]DeviceProfile{
		"R3CW1234567":        foldUSB,
		"192.168.1.50:41235": foldWiFi,
		"emulator-5554":      emuProf,
	})
	if len(merged) != 3 {
		t.Fatalf("expected 3 unified devices (Fold merged, emulator, unauthorized), got %d: %+v", len(merged), merged)
	}

	var fold *UnifiedDevice
	for i := range merged {
		if merged[i].HardwareHash == foldUSB.HardwareHash {
			fold = &merged[i]
			break
		}
	}
	if fold == nil {
		t.Fatalf("merged Fold entry missing")
	}
	if fold.Primary.Transport != TransportUSB || len(fold.Transports) != 2 {
		t.Fatalf("expected USB primary with 2 transports, got %+v", *fold)
	}
	if !fold.HasTransport(TransportUSB) || !fold.HasTransport(TransportTLSWiFi) {
		t.Fatalf("expected both USB and TLS Wi-Fi transports on unified card: %+v", fold.Transports)
	}
	alt, ok := fold.AlternateTransport(fold.Primary.Serial)
	if !ok || alt.Serial != "192.168.1.50:41235" {
		t.Fatalf("expected AlternateTransport to return TLS Wi-Fi endpoint, got ok=%v alt=%+v", ok, alt)
	}
}
