package dex

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/adb"
	"github.com/YuanZhe-99/AndroFuse/internal/sysproc"
)

// AppGuardSession supervises a running `androfuse-dex-helper.jar appguard <displayId> <pkg>`
// process on the phone under UID 2000 so Independent App Windows stay awake while the physical
// screen is off and receive Bounded Back navigation.
type AppGuardSession struct {
	displayID  int
	pkg        string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	onRootBack func()

	opMu    sync.Mutex
	mu      sync.Mutex
	closed  bool
	doneCh  chan struct{}
	replyCh chan string
}

// StartAppGuard pushes and launches `Main appguard <displayID> <pkg>` on serial over ADB shell
// and waits until the virtual display wake locks and task listener are active (`AFAPP1|READY`).
//
// Inputs: active ADB client/serial, virtual displayID (> 0), target package, and optional
// callback invoked asynchronously when root-level Back is guarded.
// Returns: live AppGuardSession handle ready for bounded Back requests and cleanup on Stop.
func StartAppGuard(
	ctx context.Context,
	client *adb.Client,
	serial string,
	displayID int,
	pkg string,
	onRootBack func(),
) (*AppGuardSession, error) {
	if client == nil {
		return nil, errors.New("adb client is required")
	}
	if displayID <= 0 {
		return nil, fmt.Errorf("appguard requires virtual displayID > 0, got %d", displayID)
	}
	pkg = strings.TrimSpace(strings.TrimPrefix(pkg, "+"))
	if pkg == "" {
		return nil, errors.New("appguard requires non-empty package name")
	}

	jar, err := FindHelperJar()
	if err != nil {
		return nil, err
	}
	if err := client.PushFile(ctx, serial, jar, RemoteHelperPath); err != nil {
		return nil, fmt.Errorf("push dex helper for appguard: %w", err)
	}
	_, _ = client.Shell(ctx, serial, "chmod 600 "+RemoteHelperPath+" 2>/dev/null || true")

	shellCmd := fmt.Sprintf("CLASSPATH=%s app_process / com.androfuse.dexhelper.Main appguard %d %s",
		RemoteHelperPath, displayID, pkg)
	cmd := exec.Command(client.Path, "-s", serial, "shell", shellCmd)
	sysproc.HideConsole(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start appguard: %w", err)
	}

	sess := &AppGuardSession{
		displayID:  displayID,
		pkg:        pkg,
		cmd:        cmd,
		stdin:      stdin,
		onRootBack: onRootBack,
		doneCh:     make(chan struct{}),
		replyCh:    make(chan string, 16),
	}

	readyCh := make(chan error, 1)
	go func() {
		defer close(sess.doneCh)
		defer close(sess.replyCh)
		readySent := false
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "AFAPP1|READY|") {
				if !readySent {
					readySent = true
					readyCh <- nil
				}
				continue
			}
			if payload, isErr := strings.CutPrefix(line, "AFDEX1|ERR|"); isErr {
				if !readySent {
					readySent = true
					readyCh <- fmt.Errorf("appguard error: %s", payload)
				}
				continue
			}
			if strings.HasPrefix(line, "AFAPP1|ROOT_BACK|") {
				if sess.onRootBack != nil {
					go sess.onRootBack()
				}
				continue
			}
			if strings.HasPrefix(line, "AFAPP1|BACK|") || strings.HasPrefix(line, "AFAPP1|PONG|") {
				select {
				case sess.replyCh <- line:
				default:
				}
			}
		}
		if !readySent {
			readyCh <- fmt.Errorf("appguard exited before READY (stderr=%s)", strings.TrimSpace(stderrBuf.String()))
		}
	}()

	waitCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	select {
	case <-waitCtx.Done():
		sess.Stop()
		return nil, fmt.Errorf("timed out waiting for appguard READY: %w", waitCtx.Err())
	case rErr := <-readyCh:
		if rErr != nil {
			sess.Stop()
			return nil, rErr
		}
		return sess, nil
	}
}

// Back performs a Bounded Back check on the supervised virtual display and injects KEYCODE_BACK
// when inner navigation is active.
//
// Returns: acted=true if Back was delivered, acted=false if blocked safely at the app's main screen.
func (g *AppGuardSession) Back(timeout time.Duration) (bool, error) {
	if g == nil {
		return false, errors.New("appguard is nil")
	}
	g.opMu.Lock()
	defer g.opMu.Unlock()

	g.mu.Lock()
	if g.closed || g.stdin == nil {
		g.mu.Unlock()
		return false, errors.New("appguard closed")
	}
	// Drain any stale replies before issuing a new command.
	for {
		select {
		case <-g.replyCh:
		default:
			goto writeCmd
		}
	}
writeCmd:
	_, err := io.WriteString(g.stdin, "BACK\n")
	g.mu.Unlock()
	if err != nil {
		return false, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return false, errors.New("timed out waiting for appguard BACK reply")
		case line, ok := <-g.replyCh:
			if !ok {
				return false, errors.New("appguard closed")
			}
			if line == "AFAPP1|BACK|OK" {
				return true, nil
			}
			if line == "AFAPP1|BACK|ROOT" {
				return false, nil
			}
		}
	}
}

// Stop tears down the remote appguard process, releasing its display/CPU wake locks idempotently (`INV-03`).
func (g *AppGuardSession) Stop() {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	stdin := g.stdin
	cmd := g.cmd
	g.mu.Unlock()

	if stdin != nil {
		_, _ = io.WriteString(stdin, "STOP\n")
		_ = stdin.Close()
	}
	select {
	case <-g.doneCh:
	case <-time.After(2 * time.Second):
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-g.doneCh
	}
	if cmd != nil {
		_ = cmd.Wait()
	}
}
