//go:build !linux && !android

package companion

import "net"

// verifyLocalPeerCred verifies abstract unix domain socket peer UIDs on Linux/Android; on other
// platforms local loopback gating is handled by `isLocal`.
func verifyLocalPeerCred(_ net.Conn) bool {
	return true
}
