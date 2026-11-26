package companion

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"net"
	"slices"
	"strconv"
)

// Listening ports (the phone's Companion server and the desktop's mesh listener) are drawn from
// this range: high, and below the dynamic (ephemeral) range 49152–65535 that Windows and Android
// use for outgoing connections.
const (
	PortMin = 20000
	PortMax = 48999
)

// PickPort returns prev when it is still free for both TCP and UDP, else a random free port in
// [PortMin, PortMax].
// Notes: a test bind on all interfaces also skips ports the OS reserves (Windows Hyper-V excluded
// ranges) or another program, such as a separate easytier-core, already holds. Keeping prev lets a
// paired peer's saved address keep working.
func PickPort(prev int) (int, error) {
	if prev >= PortMin && prev <= PortMax && portFree(prev) {
		return prev, nil
	}
	for range 50 {
		n, err := rand.Int(rand.Reader, big.NewInt(PortMax-PortMin+1))
		if err != nil {
			return 0, err
		}
		if p := PortMin + int(n.Int64()); portFree(p) {
			return p, nil
		}
	}
	return 0, errors.New("no free listening port")
}

// portFree reports whether port can be bound for both TCP and UDP on all interfaces.
func portFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = l.Close()
	pc, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = pc.Close()
	return true
}

// devicePortCount is how many ports DevicePorts gives.
const devicePortCount = 4

// DevicePorts are the ports a Companion listens on, in order of preference, for its device id
// (the id in its mesh hostname): drawn from the id, so a desktop that knows the id finds the port
// again after the phone had to move off its saved one, while different phones get different ports.
func DevicePorts(id string) []int {
	out := make([]int, 0, devicePortCount)
	for i := 0; len(out) < devicePortCount; i++ {
		sum := sha256.Sum256([]byte("androfuse-companion-port\x00" + id + "\x00" + strconv.Itoa(i)))
		p := PortMin + int(binary.BigEndian.Uint32(sum[:4])%uint32(PortMax-PortMin+1))
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// PickDevicePort returns the first free one of DevicePorts(id), else a random free port, which a
// desktop then only learns from the next adb connection.
func PickDevicePort(id string) (int, error) {
	for _, p := range DevicePorts(id) {
		if portFree(p) {
			return p, nil
		}
	}
	return PickPort(0)
}
