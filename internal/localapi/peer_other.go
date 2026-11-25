//go:build !windows

package localapi

import "net"

func verifySameUserPeer(_ net.Conn) error {
	return nil
}
