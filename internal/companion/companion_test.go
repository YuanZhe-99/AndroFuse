package companion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
