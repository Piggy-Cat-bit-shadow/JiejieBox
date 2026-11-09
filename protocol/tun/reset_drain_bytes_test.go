package tun

// What one network reset must do to the flows that already exist, measured in BYTES on the real
// userspace stack.
//
// The existing reset tests pin the merge and de-duplication of InterfaceUpdated events and that new
// flows are served after a reset; they do not carry payload through a flow that was established
// BEFORE the reset. That is the property the window's pass condition asks for, and the three
// branches below are all driven through the production objects:
//
//   - the real go stack (singtun.NewStack("go")) and its ResetNetwork, which is exactly what
//     Inbound.InterfaceUpdated -> resetNetwork invokes;
//   - the real route connection manager and traffic table, so "released" means the Activity entry
//     left and the close path ran exactly once;
//   - a device-side TCP conversation, so the payload is what an application would see.
//
// Branch 1: after a reset, a healthy flow still delivers bytes in both directions.
// Branch 2: a flow created after the reset gets a working path.
// Branch 3: a flow whose peer path is gone is released within the same bound the release tests
// use, and exactly one close path runs for it.

import (
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-tun/gtcpip/header"
	"github.com/stretchr/testify/require"
)

// drainProbeTarget is a loopback peer whose connections the test can kill, which is how a path is
// confirmed dead while a flow is established on it.
type drainProbeTarget struct {
	listener net.Listener
	access   sync.Mutex
	accepted []net.Conn
}

func startDrainProbeTarget(t *testing.T) *drainProbeTarget {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	target := &drainProbeTarget{listener: listener}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			target.access.Lock()
			target.accepted = append(target.accepted, conn)
			target.access.Unlock()
			go func() {
				defer conn.Close()
				buffer := make([]byte, 4096)
				for {
					read, readErr := conn.Read(buffer)
					if read > 0 {
						_, _ = conn.Write(buffer[:read])
					}
					if readErr != nil {
						return
					}
				}
			}()
		}
	}()
	return target
}

func (t *drainProbeTarget) address() string {
	return "127.0.0.1:" + strconv.Itoa(int(t.listener.Addr().(*net.TCPAddr).Port))
}

func (t *drainProbeTarget) port() uint16 {
	return uint16(t.listener.Addr().(*net.TCPAddr).Port)
}

// killEstablished aborts the established connections with a reset (linger 0), the way a path that
// fails reports it: the peer is gone, not politely finished. A FIN would only half-close the flow
// and leave it in the table, which is correct TCP behaviour but not "the path is dead".
func (t *drainProbeTarget) killEstablished() {
	t.access.Lock()
	conns := t.accepted
	t.accepted = nil
	t.access.Unlock()
	for _, conn := range conns {
		if tcpConn, isTCP := conn.(*net.TCPConn); isTCP {
			_ = tcpConn.SetLinger(0)
		}
		_ = conn.Close()
	}
}

// kill takes the peer away completely: no new connection either.
func (t *drainProbeTarget) kill() {
	_ = t.listener.Close()
	t.killEstablished()
}

// sendPayload writes one device-side data packet and returns the next device sequence number.
func sendPayload(t *testing.T, fixture *releaseProbeFixture, sourcePort, destinationPort uint16, deviceNext, stackNext uint32, payload []byte) uint32 {
	t.Helper()
	fixture.emit(t, datapathIPv4TCP(sourcePort, destinationPort, deviceNext, stackNext, header.TCPFlagAck, nil))
	fixture.emit(t, datapathIPv4TCP(sourcePort, destinationPort, deviceNext, stackNext, header.TCPFlagAck|header.TCPFlagPsh, payload))
	return deviceNext + uint32(len(payload))
}

// waitPayload waits until a frame from the stack to the device carries want, and reports whether
// it arrived inside the timeout.
func waitPayload(fixture *releaseProbeFixture, sourcePort, destinationPort uint16, want []byte, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		fixture.access.Lock()
		for _, frame := range fixture.frames {
			tcp, ok := datapathTCP(frame)
			if !ok || tcp.SourcePort() != destinationPort || tcp.DestinationPort() != sourcePort {
				continue
			}
			ipHeaderLength := int(frame[0]&0x0f) << 2
			tcpHeaderLength := int(tcp.DataOffset())
			if len(frame) < ipHeaderLength+tcpHeaderLength {
				continue
			}
			if contains(frame[ipHeaderLength+tcpHeaderLength:], want) {
				fixture.access.Unlock()
				return true
			}
		}
		fixture.access.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

func contains(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for index := 0; index+len(needle) <= len(haystack); index++ {
		matched := true
		for offset := 0; offset < len(needle); offset++ {
			if haystack[index+offset] != needle[offset] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// TestTUNResetKeepsAHealthyFlowAndServesANewOne is the pass-side of the reset: the reset is about
// the network under the flows, not about the flows themselves.
func TestTUNResetKeepsAHealthyFlowAndServesANewOne(t *testing.T) {
	target := startDrainProbeTarget(t)
	fixture := newReleaseProbeFixture(t, target.address(), false)

	const firstPort = 42000
	deviceNext, stackNext := fixture.establish(t, firstPort, target.port())
	fixture.waitTraffic(t, 1, 3*time.Second)

	// The production reset path: Inbound.InterfaceUpdated -> resetNetwork -> stack.ResetNetwork.
	fixture.stack.ResetNetwork()
	time.Sleep(20 * time.Millisecond)

	// The flow established BEFORE the reset still delivers its bytes, in both directions.
	beforeReset := []byte("established-before-the-reset")
	deviceNext = sendPayload(t, fixture, firstPort, target.port(), deviceNext, stackNext, beforeReset)
	require.True(t, waitPayload(fixture, firstPort, target.port(), beforeReset, 3*time.Second),
		"the flow established before the reset must still deliver its bytes after it")

	// A flow created AFTER the reset gets a working path of its own.
	const secondPort = 42001
	secondDeviceNext, secondStackNext := fixture.establish(t, secondPort, target.port())
	afterReset := []byte("established-after-the-reset")
	sendPayload(t, fixture, secondPort, target.port(), secondDeviceNext, secondStackNext, afterReset)
	require.True(t, waitPayload(fixture, secondPort, target.port(), afterReset, 3*time.Second),
		"a flow created after the reset must carry its bytes")

	// Both flows are live in the traffic table: the reset did not silently drop either of them.
	fixture.waitTraffic(t, 2, 3*time.Second)
}

// TestTUNResetReleasesAFlowWhosePathIsGone is the failure side: a flow whose peer path has failed
// is released within the bound the release tests use, exactly once, and a new flow still works.
func TestTUNResetReleasesAFlowWhosePathIsGone(t *testing.T) {
	target := startDrainProbeTarget(t)
	fixture := newReleaseProbeFixture(t, target.address(), false)

	const deadPort = 42002
	deviceNext, stackNext := fixture.establish(t, deadPort, target.port())
	fixture.waitTraffic(t, 1, 3*time.Second)
	_ = deviceNext
	_ = stackNext

	// The path under the flow is confirmed dead, then the network reset arrives.
	target.killEstablished()
	fixture.stack.ResetNetwork()

	fixture.waitTraffic(t, 0, 5*time.Second)
	fixture.waitClosed(t)

	// The stack still serves a NEW flow after the failed one was released: the dead flow did not
	// take the stack's ability to dial with it.
	const livePort = 42003
	revivedDeviceNext, revivedStackNext := fixture.establish(t, livePort, target.port())
	marker := []byte("new-flow-after-the-release")
	sendPayload(t, fixture, livePort, target.port(), revivedDeviceNext, revivedStackNext, marker)
	require.True(t, waitPayload(fixture, livePort, target.port(), marker, 3*time.Second),
		"a new flow must be served after the failed one was released")
}
