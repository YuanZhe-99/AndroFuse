//go:build linux || android

package companion

import (
	"net"
	"os"
	"syscall"
)

// verifyLocalPeerCred checks kernel SO_PEERCRED UID on Linux/Android abstract unix sockets
// (`@androfuse-companion`) so unprivileged third-party apps on the same phone cannot initiate
// pairing requests (`INV-07`). Only root (0), ADB shell (`adbd` forward, UID 2000), or the
// owning Companion UID itself is accepted.
func verifyLocalPeerCred(raw net.Conn) bool {
	uconn, ok := raw.(*net.UnixConn)
	if !ok {
		return true
	}
	sc, err := uconn.SyscallConn()
	if err != nil {
		return false
	}
	var allowed bool
	var sockErr error
	ctrlErr := sc.Control(func(fd uintptr) {
		cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			sockErr = err
			return
		}
		selfUID := uint32(os.Getuid())
		allowed = cred.Uid == 0 || cred.Uid == 2000 || cred.Uid == selfUID
	})
	return ctrlErr == nil && sockErr == nil && allowed
}
