//go:build windows

package localapi

import (
	"errors"
	"fmt"
	"net"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SIO_AF_UNIX_GETPEERPID (_WSAIOR('x', 256) / IOC_VENDOR) queries the peer PID on Windows AF_UNIX sockets.
const sioAFUnixGetPeerPID = 0x58000100

// verifySameUserPeer confirms the connecting AF_UNIX peer process belongs to the
// same Windows user SID as the current process (`SIO_AF_UNIX_GETPEERPID`).
func verifySameUserPeer(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("expected AF_UNIX connection")
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return fmt.Errorf("syscall conn: %w", err)
	}

	var peerPID uint32
	var ioctlErr error
	if cErr := rc.Control(func(fd uintptr) {
		var bytesRet uint32
		ioctlErr = windows.WSAIoctl(
			windows.Handle(fd),
			sioAFUnixGetPeerPID,
			nil,
			0,
			(*byte)(unsafe.Pointer(&peerPID)),
			uint32(unsafe.Sizeof(peerPID)),
			&bytesRet,
			nil,
			0,
		)
	}); cErr != nil {
		return fmt.Errorf("raw socket control: %w", cErr)
	}
	if ioctlErr != nil {
		return fmt.Errorf("SIO_AF_UNIX_GETPEERPID: %w", ioctlErr)
	}
	if peerPID == 0 {
		return errors.New("peer PID unavailable")
	}
	if peerPID == uint32(os.Getpid()) {
		return nil
	}

	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, peerPID)
	if err != nil {
		return fmt.Errorf("open peer process %d: %w", peerPID, err)
	}
	defer windows.CloseHandle(proc)

	var peerTok windows.Token
	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &peerTok); err != nil {
		return fmt.Errorf("open peer token: %w", err)
	}
	defer peerTok.Close()

	peerUser, err := peerTok.GetTokenUser()
	if err != nil {
		return fmt.Errorf("peer token user: %w", err)
	}

	selfTok := windows.GetCurrentProcessToken()
	selfUser, err := selfTok.GetTokenUser()
	if err != nil {
		return fmt.Errorf("self token user: %w", err)
	}

	if !windows.EqualSid(peerUser.User.Sid, selfUser.User.Sid) {
		return errors.New("AF_UNIX peer user SID mismatch")
	}
	return nil
}
