package androidhost

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"github.com/YuanZhe-99/AndroFuse/internal/companion"

	_ "github.com/YuanZhe-99/AndroFuse/locales"
)

func TestHostLifecycleAndPermissions(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	h.mu.Lock()
	h.cfg.ListenPort = 0
	h.mu.Unlock()

	if err := h.StartServer(); err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	if !h.Running() || h.ListenAddr() == "" {
		t.Fatalf("expected running server, addr=%q", h.ListenAddr())
	}

	h.SetPermission("file_transfer", true)
	h.SetPermission("media_bridge", true)
	h.SetWirelessADBPort(41235)
	oldTok := h.Config().Token
	newTok, err := h.RotateToken()
	if err != nil || newTok == "" || newTok == oldTok {
		t.Fatalf("RotateToken: new=%q old=%q err=%v", newTok, oldTok, err)
	}

	reloaded := New(dir, nil)
	if !reloaded.Config().Permissions.FileTransfer || !reloaded.Config().Permissions.MediaBridge || reloaded.Config().WirelessADBPort != 41235 || reloaded.Config().Token != newTok {
		t.Fatalf("persisted config mismatch: %+v", reloaded.Config())
	}

	if err := h.StopServer(); err != nil {
		t.Fatalf("StopServer: %v", err)
	}
	if h.Running() {
		t.Fatal("expected server stopped")
	}

	_, _ = h.issueToken(companion.PairRequest{ComputerID: "pc1", ClientName: "STUDIO-PC"})
	_, _ = h.issueToken(companion.PairRequest{ComputerID: "pc2", ClientName: "LAPTOP"})
	h.RenameComputer("pc2", "Travel laptop")
	for _, lang := range []string{"en", "zh-Hans"} {
		i18n.SetLocaleFunc(func() string { return lang })
		for _, dark := range []bool{false, true} {
			tt := ui.NewTester(func(c *ui.Context) {
				RenderCompanionView(c, h)
			}, 412, 1600)
			tt.SetDark(dark)
			tt.Frame()
			if tt.Image().Bounds().Dx() != 412 {
				t.Fatalf("unexpected mobile frame bounds: %v", tt.Image().Bounds())
			}
			if dir := os.Getenv("SCREENSHOT_DIR"); dir != "" {
				f, err := os.Create(filepath.Join(dir, fmt.Sprintf("phone-%s-dark%v.png", lang, dark)))
				if err == nil {
					_ = png.Encode(f, tt.Image())
					_ = f.Close()
				}
			}
		}
	}
}

// TestPhoneEditsItsOwnNetwork checks that the phone's own network editor keeps the form open with
// the reason when the network cannot be joined (outside Android there is no Keystore or embedded
// mesh), and that the Companion listens on one of the ports its device id gives.
func TestPhoneEditsItsOwnNetwork(t *testing.T) {
	h := New(t.TempDir(), nil)
	h.editMesh()
	h.saveMeshForm(meshForm{Name: "home", Secret: "s3cret-network", Servers: " tcp://relay.example:11010 \n\n"})
	h.mu.Lock()
	f := h.meshForm
	h.mu.Unlock()
	if f == nil || f.Err == "" {
		t.Fatalf("the editor should stay open with the reason: %+v", f)
	}
	if err := h.StartServer(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.StopServer() }()
	port := h.Config().ListenPort
	if !slices.Contains(companion.DevicePorts(h.meshID()), port) {
		t.Fatalf("listen port %d is not one of %v", port, companion.DevicePorts(h.meshID()))
	}
}

// TestPairedComputers checks one pairing per computer: each gets its own token, the old shared
// token still works once and puts its computer on the list, a rename survives pairing again,
// and forgetting a computer stops its token (and the shared one) at once.
func TestPairedComputers(t *testing.T) {
	h := New(t.TempDir(), nil)
	shared := h.Config().Token
	if !h.checkToken(companion.AuthRequest{Token: shared, ComputerID: "aa", ClientName: "OLD-PC"}) {
		t.Fatal("the shared token of an earlier pairing was refused")
	}
	tok, err := h.issueToken(companion.PairRequest{ComputerID: "bb", ClientName: "STUDIO"})
	if err != nil || tok == shared {
		t.Fatalf("issueToken = %q, %v", tok, err)
	}
	if !h.checkToken(companion.AuthRequest{Token: tok, ComputerID: "bb"}) || h.checkToken(companion.AuthRequest{Token: "wrong"}) {
		t.Fatal("token check is wrong")
	}
	h.RenameComputer("bb", "Desk")
	tok2, _ := h.issueToken(companion.PairRequest{ComputerID: "bb", ClientName: "STUDIO"})
	if h.checkToken(companion.AuthRequest{Token: tok}) || !h.checkToken(companion.AuthRequest{Token: tok2}) {
		t.Fatal("pairing again must replace the computer's token")
	}
	list := h.Computers()
	if len(list) != 2 {
		t.Fatalf("computers = %+v", list)
	}
	for _, c := range list {
		if c.ID == "bb" && c.shownName() != "Desk" {
			t.Fatalf("rename lost on pairing again: %+v", c)
		}
	}
	h.ForgetComputer("aa")
	if h.checkToken(companion.AuthRequest{Token: shared}) {
		t.Fatal("the shared token still works after its computer was forgotten")
	}
	if !h.checkToken(companion.AuthRequest{Token: tok2}) || len(h.Computers()) != 1 {
		t.Fatal("forgetting one computer touched another")
	}
}

// TestAutoStart checks that a paired phone starts the Companion service with the app, that one
// its user stopped stays off after a restart, and that an unpaired one does not start by itself.
func TestAutoStart(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	h.AutoStart()
	if h.Running() {
		t.Fatal("an unpaired phone started the service by itself")
	}
	_, _ = h.issueToken(companion.PairRequest{ComputerID: "pc1", ClientName: "PC"})
	h.AutoStart()
	if !h.Running() {
		t.Fatal("a paired phone did not start the service")
	}
	h.StopByUser()
	again := New(dir, nil)
	again.AutoStart()
	if again.Running() {
		t.Fatal("the service started although its user stopped it")
	}
	if err := again.StartServer(); err != nil || !again.Running() {
		t.Fatalf("starting by hand failed: %v", err)
	}
	_ = again.StopServer()
	third := New(dir, nil)
	third.AutoStart()
	defer func() { _ = third.StopServer() }()
	if !third.Running() {
		t.Fatal("starting by hand did not clear the stop")
	}
}
