package androidhost

import (
	"context"
	"fmt"
	"image/png"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestDaemonStatusAndView(t *testing.T) {
	h := New(t.TempDir(), nil)
	h.mu.Lock()
	h.daemonReady = true
	h.daemonPID = 4321
	h.daemonTrustedDisplay = true
	h.daemonFDVerified = true
	h.mu.Unlock()

	st := h.Status()
	if !st.DaemonReady || st.DaemonPID != 4321 || !st.DaemonTrustedDisplay || !st.DaemonFDVerified {
		t.Fatalf("unexpected daemon status fields: %+v", st)
	}
	for _, lang := range []string{"en", "zh-Hans"} {
		i18n.SetLocaleFunc(func() string { return lang })
		for _, dark := range []bool{false, true} {
			for _, sz := range [][2]int{{360, 780}, {760, 780}} {
				tt := ui.NewTester(func(c *ui.Context) {
					RenderCompanionView(c, h)
				}, sz[0], sz[1])
				tt.SetDark(dark)
				tt.Frame()
				if tt.Image().Bounds().Dx() != sz[0] {
					t.Fatalf("unexpected Companion frame bounds: %v", tt.Image().Bounds())
				}
			}
		}
	}
}

func TestStartServerConcurrentNoBindConflict(t *testing.T) {
	h := New(t.TempDir(), nil)
	defer func() { _ = h.StopServer() }()

	const callers = 8
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- h.StartServer()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent StartServer returned error: %v", err)
		}
	}
	if got := h.LastError(); got != "" {
		t.Fatalf("expected empty LastError, got %q", got)
	}
	if !h.Running() || h.ListenPort() <= 0 {
		t.Fatalf("expected running server with bound port, running=%v port=%d", h.Running(), h.ListenPort())
	}
	if err := h.StopServer(); err != nil {
		t.Fatalf("StopServer: %v", err)
	}
}

func TestOnNetworkChangedDebounce(t *testing.T) {
	h := New(t.TempDir(), nil)
	var rebinds atomic.Int32
	h.mu.Lock()
	h.cfg.Mesh = &MeshSettings{NetworkName: "test-mesh"}
	h.meshRebindDelay = 40 * time.Millisecond
	h.onMeshRebind = func() { rebinds.Add(1) }
	h.mu.Unlock()

	// Initial network discovery should not trigger a rebind.
	h.onNetworkChanged("cellular")
	time.Sleep(80 * time.Millisecond)
	if got := rebinds.Load(); got != 0 {
		t.Fatalf("initial network callback triggered %d rebinds, want 0", got)
	}

	// Dropping to "none" should not trigger a rebind either.
	h.onNetworkChanged("none")
	time.Sleep(80 * time.Millisecond)
	if got := rebinds.Load(); got != 0 {
		t.Fatalf("none callback triggered %d rebinds, want 0", got)
	}

	// Rapid flutter -> wifi should debounce to 1 rebind after meshRebindDelay.
	h.onNetworkChanged("cellular")
	h.onNetworkChanged("wifi")
	time.Sleep(120 * time.Millisecond)
	if got := rebinds.Load(); got != 1 {
		t.Fatalf("transition triggered %d rebinds, want 1", got)
	}
}

func TestStatusReportsWirelessPortEvenWhenTunnelDisabled(t *testing.T) {
	h := New(t.TempDir(), nil)
	if !h.Config().Permissions.ADBBeacon || !h.Config().Permissions.ScreenShare {
		t.Fatalf("expected ADBBeacon and ScreenShare enabled by default: %+v", h.Config().Permissions)
	}
	h.SetPermission("adb_beacon", false)
	h.SetWirelessADBPort(39543)
	h.mu.Lock()
	h.cfg.ListenPort = 43856
	h.cfg.Standby = true
	h.a11yOn = true
	h.apkHash = "deadbeef"
	h.mu.Unlock()
	st := h.Status()
	if st.WirelessADBPort != 39543 || st.ADBTunnelAllowed || !st.ScreenShareAllowed || st.AppVersion == "" || st.CompanionPort != 43856 || !st.StandbyRunning || !st.AccessibilityOn || st.AppBuildHash != "deadbeef" {
		t.Fatalf("Status() missing full phone state: %+v", st)
	}
}

func TestPairingPortTunnelAndStatusFields(t *testing.T) {
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
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	pairPort := echo.Addr().(*net.TCPAddr).Port

	h := New(t.TempDir(), nil)
	if err := h.StartServer(); err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	defer func() { _ = h.StopServer() }()

	h.SetWirelessADBPairPort(pairPort)
	st := h.Status()
	if st.WirelessADBPairPort != pairPort || !st.ADBTunnelAllowed || !st.ScreenShareAllowed {
		t.Fatalf("unexpected pairing/permission telemetry: %+v", st)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := companion.DialTunnel(ctx, h.ListenAddr(), h.Config().Token, h.srv.Fingerprint(), pairPort)
	if err != nil {
		t.Fatalf("DialTunnel on active pairing port failed: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("PAIR")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "PAIR" {
		t.Fatalf("tunnel echo = %q, err=%v", buf, err)
	}
	if _, err := companion.DialTunnel(ctx, h.ListenAddr(), h.Config().Token, h.srv.Fingerprint(), pairPort+1); err == nil {
		t.Fatal("expected arbitrary port to be rejected by OnTunnel")
	}
}

func TestPairPromptChoicesAndMeshOverwriteGuard(t *testing.T) {
	h := New(t.TempDir(), nil)
	if got := h.Config().Token; got == "ANDROFUS" || len(got) != 32 {
		t.Fatalf("expected 32-char CSPRNG token on fresh host, got len=%d (%q)", len(got), got)
	}

	// Correct 3-choice confirmation succeeds and renders cleanly across locales and themes.
	resCh := make(chan bool, 1)
	go func() {
		resCh <- h.askPair(companion.PairRequest{ClientName: "STUDIO-PC", Code: "428193"})
	}()
	var choices [3]string
	var ok bool
	for range 40 {
		_, choices, ok = h.PendingPairChoices()
		if ok {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !ok || !slices.Contains(choices[:], "428193") {
		t.Fatalf("PendingPairChoices missing real code: ok=%v choices=%v", ok, choices)
	}
	for _, lang := range []string{"en", "zh-Hans"} {
		i18n.SetLocaleFunc(func() string { return lang })
		for _, dark := range []bool{false, true} {
			tt := ui.NewTester(func(c *ui.Context) {
				RenderCompanionView(c, h)
			}, 360, 780)
			tt.SetDark(dark)
			tt.Frame()
		}
	}
	h.AnswerPairChoice("428193")
	if !<-resCh {
		t.Fatal("expected matching AnswerPairChoice to succeed")
	}

	// Tapping a decoy number rejects pairing.
	go func() {
		resCh <- h.askPair(companion.PairRequest{ClientName: "STUDIO-PC", Code: "112233"})
	}()
	ok = false
	for range 40 {
		_, choices, ok = h.PendingPairChoices()
		if ok {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !ok {
		t.Fatal("expected second pairing prompt")
	}
	decoy := ""
	for _, c := range choices {
		if c != "112233" {
			decoy = c
			break
		}
	}
	h.AnswerPairChoice(decoy)
	if <-resCh {
		t.Fatal("expected decoy choice to reject pairing")
	}

	// Remote JoinMesh must refuse silently overwriting a different already-joined network.
	h.mu.Lock()
	h.cfg.Mesh = &MeshSettings{NetworkName: "net-a"}
	h.mu.Unlock()
	jRes, err := h.JoinMesh(companion.MeshJoinRequest{NetworkName: "net-b", NetworkSecret: "other-secret"})
	if err != nil {
		t.Fatalf("JoinMesh error: %v", err)
	}
	if jRes.Running || !strings.Contains(jRes.Message, "net-a") {
		t.Fatalf("expected overwrite guard naming net-a, got %+v", jRes)
	}
	if h.Config().Mesh == nil || h.Config().Mesh.NetworkName != "net-a" {
		t.Fatalf("expected net-a preserved, got %+v", h.Config().Mesh)
	}

	// Pairing port TTL expires after 3 minutes cleanly.
	h.SetWirelessADBPairPort(42345)
	if st := h.Status(); st.WirelessADBPairPort != 42345 {
		t.Fatalf("expected active pair port 42345, got %d", st.WirelessADBPairPort)
	}
	h.mu.Lock()
	h.wirelessADBPairAt = time.Now().Add(-4 * time.Minute)
	h.mu.Unlock()
	if st := h.Status(); st.WirelessADBPairPort != 0 {
		t.Fatalf("expected expired pair port to read as 0, got %d", st.WirelessADBPairPort)
	}
}
