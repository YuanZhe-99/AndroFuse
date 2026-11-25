package androidhost

import (
	"testing"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

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
	h.SetWirelessADBPort(41235)
	oldTok := h.Config().Token
	newTok, err := h.RotateToken()
	if err != nil || newTok == "" || newTok == oldTok {
		t.Fatalf("RotateToken: new=%q old=%q err=%v", newTok, oldTok, err)
	}

	reloaded := New(dir, nil)
	if !reloaded.Config().Permissions.FileTransfer || reloaded.Config().WirelessADBPort != 41235 || reloaded.Config().Token != newTok {
		t.Fatalf("persisted config mismatch: %+v", reloaded.Config())
	}

	if err := h.StopServer(); err != nil {
		t.Fatalf("StopServer: %v", err)
	}
	if h.Running() {
		t.Fatal("expected server stopped")
	}

	for _, lang := range []string{"en", "zh-Hans"} {
		i18n.SetLocaleFunc(func() string { return lang })
		for _, dark := range []bool{false, true} {
			tt := ui.NewTester(func(c *ui.Context) {
				RenderCompanionView(c, h)
			}, 412, 915)
			tt.SetDark(dark)
			tt.Frame()
			if tt.Image().Bounds().Dx() != 412 {
				t.Fatalf("unexpected mobile frame bounds: %v", tt.Image().Bounds())
			}
		}
	}
}
