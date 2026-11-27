package companion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSanitizeFileNameAndToken(t *testing.T) {
	tok, err := GeneratePairingToken()
	if err != nil || len(tok) != 8 {
		t.Fatalf("GeneratePairingToken = %q err=%v", tok, err)
	}
	if _, err := SanitizeFileName("../secret.txt"); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
	if _, err := SanitizeFileName("dir/file.apk"); err == nil {
		t.Fatal("expected slash path to be rejected")
	}
	got, err := SanitizeFileName("notes.txt")
	if err != nil || got != "notes.txt" {
		t.Fatalf("SanitizeFileName(notes.txt) = %q err=%v", got, err)
	}
}

func TestTLSCompanionAuthAndP1Roundtrip(t *testing.T) {
	perms := Permissions{
		Telemetry:     true,
		Clipboard:     true,
		Notifications: false, // Intentionally disabled first to verify INV-09 permission gate
		FileTransfer:  true,
		AppContinuity: true,
		DisplayGuard:  true,
		ADBBeacon:     true,
	}
	var gotClip ClipboardMessage
	srv, err := NewServer("PAIR8888", "SM-F971U1", "17", 37, Handler{
		GetPermissions: func() Permissions { return perms },
		GetStatus: func() DeviceStatus {
			return DeviceStatus{
				BatteryPct:        87,
				Charging:          true,
				TempC:             34.5,
				NetworkType:       "wifi",
				ForegroundPackage: "com.android.chrome",
				WirelessADBPort:   41235,
				Timestamp:         time.Now().Unix(),
			}
		},
		OnClipboard: func(m ClipboardMessage) error {
			gotClip = m
			return nil
		},
		OnAppContinuity: func(req AppContinuityRequest) (AppContinuityResponse, error) {
			return AppContinuityResponse{OK: true, Package: "com.android.chrome", Message: req.Action}, nil
		},
		OnFilePush: func(name string, data []byte, shaHex string) (FilePushResponse, error) {
			return FilePushResponse{SavedPath: "/sdcard/Download/" + name, SHA256: shaHex, BytesWritten: int64(len(data))}, nil
		},
		OnDisplayGuard: func(_ DisplayGuardRequest) (DisplayGuardResponse, error) {
			return DisplayGuardResponse{CleanedCount: 1, Message: "cleaned residual overlay"}, nil
		},
		OnListApps: func() ([]DaemonAppEntry, error) {
			return []DaemonAppEntry{{Package: "com.whatsapp", Label: "WhatsApp", Resizeable: true}}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := srv.Listen("127.0.0.1:0"); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()

	callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
	defer callCancel()

	if _, err := Dial(callCtx, srv.Addr(), "WRONGTKN", srv.Fingerprint()); err == nil {
		t.Fatal("expected invalid token to be rejected")
	}
	if _, err := Dial(callCtx, srv.Addr(), "PAIR8888", "00:11:22"); err == nil {
		t.Fatal("expected mismatched cert fingerprint to be rejected")
	}

	c, err := Dial(callCtx, srv.Addr(), "PAIR8888", srv.Fingerprint())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if c.PeerInfo().DeviceModel != "SM-F971U1" || c.PeerInfo().SDKInt != 37 {
		t.Fatalf("unexpected PeerInfo: %+v", c.PeerInfo())
	}
	st, err := c.GetStatus(callCtx)
	if err != nil || st.BatteryPct != 87 || st.WirelessADBPort != 41235 || st.ForegroundPackage != "com.android.chrome" {
		t.Fatalf("unexpected GetStatus: %+v err=%v", st, err)
	}
	if err := c.SendClipboard(callCtx, 7, "hello from windows"); err != nil || gotClip.Text != "hello from windows" {
		t.Fatalf("SendClipboard mismatch: got=%+v err=%v", gotClip, err)
	}
	if err := c.ReplyNotification(callCtx, "n1", "ok"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected notification reply disabled when permission off, got %v", err)
	}
	perms.Notifications = true
	if err := c.ReplyNotification(callCtx, "n1", "ok"); err != nil {
		t.Fatalf("ReplyNotification after opt-in: %v", err)
	}
	cont, err := c.RequestContinuity(callCtx, "handoff_to_pc", "")
	if err != nil || !cont.OK || cont.Package != "com.android.chrome" {
		t.Fatalf("RequestContinuity: %+v err=%v", cont, err)
	}
	apps, err := c.ListApps(callCtx)
	if err != nil || len(apps) != 1 || apps[0].Package != "com.whatsapp" {
		t.Fatalf("ListApps: %+v err=%v", apps, err)
	}
	payload := []byte("androfuse-p1-file-transfer")
	fRes, err := c.PushFile(callCtx, "sample.txt", payload)
	wantSum := sha256.Sum256(payload)
	if err != nil || fRes.SHA256 != hex.EncodeToString(wantSum[:]) || fRes.BytesWritten != int64(len(payload)) {
		t.Fatalf("PushFile: %+v err=%v", fRes, err)
	}
	guard, err := c.RunDisplayGuard(callCtx)
	if err != nil || guard.CleanedCount != 1 {
		t.Fatalf("RunDisplayGuard: %+v err=%v", guard, err)
	}
}

// TestADBPairingAndMeshJoin checks the adb pairing path end to end over loopback: the phone's
// answer gates the token, a declined request gets nothing, and an authenticated desktop can hand
// over its mesh.
func TestADBPairingAndMeshJoin(t *testing.T) {
	allow := true
	var asked PairRequest
	var joined MeshJoinRequest
	srv, err := NewServer("PAIRADB1", "SM-F971U1", "17", 37, Handler{
		GetPermissions: func() Permissions { return DefaultPermissions() },
		OnPairRequest: func(r PairRequest) bool {
			asked = r
			return allow
		},
		OnMeshJoin: func(r MeshJoinRequest) (MeshJoinResponse, error) {
			joined = r
			return MeshJoinResponse{Running: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	defer srv.Close()

	res, err := RequestPairing(ctx, srv.Addr(), "Desk", "123456")
	if err != nil {
		t.Fatalf("RequestPairing: %v", err)
	}
	if res.Token != "PAIRADB1" || res.CertFingerprint != srv.Fingerprint() || asked.Code != "123456" || asked.ClientName != "Desk" {
		t.Fatalf("pairing result %+v asked %+v", res, asked)
	}

	allow = false
	if _, err := RequestPairing(ctx, srv.Addr(), "Desk", "654321"); err == nil || !strings.Contains(err.Error(), "declined") {
		t.Fatalf("declined pairing must fail, got %v", err)
	}

	c, err := Dial(ctx, srv.Addr(), res.Token, res.CertFingerprint)
	if err != nil {
		t.Fatalf("Dial with paired token: %v", err)
	}
	defer c.Close()
	jr, err := c.JoinMesh(ctx, MeshJoinRequest{NetworkName: "home", NetworkSecret: "s3cret", Peers: []string{"tcp://relay:11010"}})
	if err != nil || !jr.Running || joined.NetworkName != "home" || joined.NetworkSecret != "s3cret" {
		t.Fatalf("JoinMesh = %+v, %v; handler got %+v", jr, err, joined)
	}
}

// TestTunnelReachesOnlyTheWirelessDebuggingPort checks that ServeTunnels pipes bytes to the port
// the phone allows, and that another port or a phone without the permission is refused.
func TestTunnelReachesOnlyTheWirelessDebuggingPort(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); c.Close() }()
		}
	}()
	adbPort := echo.Addr().(*net.TCPAddr).Port
	perms := Permissions{ADBBeacon: true}
	srv, err := NewServer("TUNNEL01", "Pixel", "16", 36, Handler{
		GetPermissions: func() Permissions { return perms },
		OnTunnel: func(port int) (net.Conn, error) {
			if port != adbPort {
				return nil, fmt.Errorf("port %d is not Wireless debugging", port)
			}
			return net.Dial("tcp", echo.Addr().String())
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	defer srv.Close()

	local, err := ServeTunnels(ctx, srv.Addr(), "TUNNEL01", srv.Fingerprint(), adbPort)
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", local)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte("CNXN")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "CNXN" {
		t.Fatalf("tunnel echo = %q, %v", buf, err)
	}

	if _, err := DialTunnel(ctx, srv.Addr(), "TUNNEL01", srv.Fingerprint(), adbPort+1); err == nil {
		t.Fatal("a port other than Wireless debugging must be refused")
	}
	perms.ADBBeacon = false
	if _, err := DialTunnel(ctx, srv.Addr(), "TUNNEL01", srv.Fingerprint(), adbPort); err == nil {
		t.Fatal("a tunnel without the port helper permission must be refused")
	}
}

// TestShareStreamsOnlyWithPermission checks that share.open hands over the phone's stream and
// input.key reaches the handler, and that both are refused without the permission.
func TestShareStreamsOnlyWithPermission(t *testing.T) {
	perms := Permissions{ScreenShare: true}
	var gotKey string
	srv, err := NewServer("SHARE001", "Pixel", "16", 36, Handler{
		GetPermissions: func() Permissions { return perms },
		OnShare: func() (net.Conn, error) {
			a, b := net.Pipe()
			go func() { _, _ = a.Write([]byte("H264")); a.Close() }()
			return b, nil
		},
		OnKey: func(action string) error { gotKey = action; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	defer srv.Close()

	stream, err := DialShare(ctx, srv.Addr(), "SHARE001", srv.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(stream, buf); err != nil || string(buf) != "H264" {
		t.Fatalf("share stream = %q, %v", buf, err)
	}
	stream.Close()
	c, err := Dial(ctx, srv.Addr(), "SHARE001", srv.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Key(ctx, "back"); err != nil || gotKey != "back" {
		t.Fatalf("Key = %v, handler got %q", err, gotKey)
	}
	perms.ScreenShare = false
	if err := c.Key(ctx, "home"); err == nil {
		t.Fatal("input.key without the permission must be refused")
	}
	if _, err := DialShare(ctx, srv.Addr(), "SHARE001", srv.Fingerprint()); err == nil {
		t.Fatal("share.open without the permission must be refused")
	}
}
