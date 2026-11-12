package e2e

// Q17, narrow-MTU boundary, on the deterministic in-process stack.
//
// # What the real-device test proves and what it cannot run here
//
// e2e/tun_real_device_narrow_mtu_test.go (linux, /dev/net/tun) proves the device
// half on a kernel interface. It needs a TUN device, so it is not a regression a
// normal `go test ./e2e/` can run. This file covers the same MTU question on the
// MemoryTun fixture: the same production router, handler, outbound and stack, with
// a function instead of a kernel interface as the device. It is NOT evidence of a
// real cross-veth link and does not replace the device test.
//
// # What is asserted, and what "a definite result" means here
//
// The earlier detour test only checked that the datagram AFTER an oversized one
// still round-tripped, so "the oversized datagram was carried", "it was silently
// dropped" and "it wedged the flow" all passed. These cells assert the outcome
// itself, in both directions:
//
//  1. An inbound datagram larger than the device MTU is out of contract (a real
//     kernel fragments IPv4 before it reaches the device, and IPv6 refuses to
//     originate one). Measured: the stack carries it, the answer is fragmented at
//     the MTU, the fragments reassemble to the exact 2,000-byte answer, and the
//     next datagram is still correct.
//  2. A fitting request whose ANSWER is larger than the MTU must not produce an
//     oversized frame: every device-facing frame is at or under the MTU
//     (fragmentation), the answer reassembles byte for byte, and the flow is
//     still usable afterwards.
//
// The frame-level MTU property (no frame over the MTU) is what a device would
// otherwise have to discover by dropping packets.

import (
	"bytes"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-tun/gtcpip/header"

	"github.com/stretchr/testify/require"
)

// startSizedUDPReplier answers every datagram with a fixed payload, so a test can
// make the reply larger than the device MTU without a fragmenting application.
func startSizedUDPReplier(t *testing.T, size int, fill byte) uint16 {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	reply := bytes.Repeat([]byte{fill}, size)
	go func() {
		request := make([]byte, 65535)
		for {
			_, address, readErr := conn.ReadFrom(request)
			if readErr != nil {
				return
			}
			_, _ = conn.WriteTo(reply, address)
		}
	}()
	return uint16(conn.LocalAddr().(*net.UDPAddr).Port)
}

// waitForUDPDatagramReassembled waits for every IPv4 fragment of one answer to
// the device, reassembles them and returns the UDP payload. It holds the tunnel
// to its MTU at the same time: no device-facing frame may exceed it.
//
// gtcpip's IPv4.FragmentOffset reports the fragment offset in BYTES (it shifts
// the wire field left by 3), not in the 8-byte units of the raw RFC field; the
// first version of this helper multiplied it again and produced a 10,800-byte
// "datagram", which is exactly the kind of silent unit error this assertion
// exists to catch.
func (f *tunFixture) waitForUDPDatagramReassembled(t *testing.T, sourcePort, destinationPort uint16, mtu int, timeout time.Duration) []byte {
	t.Helper()
	deviceAddress := netip.MustParseAddr(tunDeviceAddress)
	answerAddress := netip.MustParseAddr(tunUDPAddress)
	deadline := time.Now().Add(timeout)
	for {
		var (
			sealed     bool
			total      int
			datagram   []byte
			covered    []bool
			frameCount int
		)
		f.access.Lock()
		// The answer to assert is identified by its FIRST fragment: only that one
		// carries the UDP header, so the ports are the only discriminator between
		// this cell's answer and one the same fixture produced earlier.
		var (
			wantedID uint16
			foundID  bool
		)
		for _, frame := range f.frames {
			ip, transport, ok := parseFrame(frame)
			if !ok || ip.TransportProtocol() != header.UDPProtocolNumber {
				continue
			}
			if ip.FragmentOffset() != 0 || len(transport) < header.UDPMinimumSize {
				continue
			}
			udp := header.UDP(transport)
			if udp.SourcePort() != destinationPort || udp.DestinationPort() != sourcePort {
				continue
			}
			wantedID = ip.ID()
			foundID = true
			break
		}
		for _, frame := range f.frames {
			if len(frame) > mtu {
				f.access.Unlock()
				f.dump(t)
				t.Fatalf("the stack emitted a %d-byte frame into a %d-byte tunnel", len(frame), mtu)
			}
			ip, _, ok := parseFrame(frame)
			if !ok || ip.TransportProtocol() != header.UDPProtocolNumber {
				continue
			}
			sourceAddress := ip.SourceAddress()
			destinationAddress := ip.DestinationAddress()
			source, sourceOK := netip.AddrFromSlice(sourceAddress.AsSlice())
			destination, destinationOK := netip.AddrFromSlice(destinationAddress.AsSlice())
			if !sourceOK || !destinationOK || source != answerAddress || destination != deviceAddress {
				continue
			}
			if !foundID || ip.ID() != wantedID {
				continue
			}
			frameCount++
			offset := int(ip.FragmentOffset())
			payload := ip.Payload()
			if need := offset + len(payload); need > len(datagram) {
				datagram = append(datagram, make([]byte, need-len(datagram))...)
				covered = append(covered, make([]bool, need-len(covered))...)
			}
			copy(datagram[offset:], payload)
			for index := offset; index < offset+len(payload); index++ {
				covered[index] = true
			}
			if !ip.More() {
				sealed = true
				total = offset + len(payload)
			}
		}
		f.access.Unlock()
		if sealed {
			complete := total > 0
			for index := 0; index < total; index++ {
				if index >= len(covered) || !covered[index] {
					complete = false
					break
				}
			}
			if complete {
				packet := datagram[:total]
				if len(packet) < header.UDPMinimumSize {
					f.dump(t)
					t.Fatalf("the reassembled datagram is shorter than a UDP header: %d", len(packet))
				}
				udp := header.UDP(packet)
				if udp.SourcePort() != destinationPort || udp.DestinationPort() != sourcePort {
					f.dump(t)
					t.Fatalf("the reassembled datagram belongs to %d -> %d", udp.SourcePort(), udp.DestinationPort())
				}
				if frameCount > 1 {
					t.Logf("the %d-byte IP payload crossed the %d-byte tunnel as %d fragments", total, mtu, frameCount)
				}
				return append([]byte(nil), packet[header.UDPMinimumSize:]...)
			}
		}
		if time.Now().After(deadline) {
			f.dump(t)
			t.Fatalf("the stack never delivered a complete reassembled datagram for %d -> %d", sourcePort, destinationPort)
		}
		select {
		case <-f.frameSignal:
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// TestTunNarrowMTUOversizedInboundDatagramIsCarriedInFragments is the inbound
// half: an out-of-contract oversized datagram is carried, its oversized answer is
// fragmented at the MTU and reassembles to the exact bytes, and the next datagram
// is still correct.
func TestTunNarrowMTUOversizedInboundDatagramIsCarriedInFragments(t *testing.T) {
	echo := startUDPEchoServer(t, "udp", "127.0.0.1:0")
	echoPort := uint16(echo.conn.LocalAddr().(*net.UDPAddr).Port)
	entryPort := freePort(t)
	exitPort := freePort(t)
	startChain(t, fmt.Sprintf(detourSocksServerConfig, chainLogLevel, exitPort))
	startChain(t, fmt.Sprintf(detourSocksServerConfig, chainLogLevel, entryPort))
	running := startChain(t, fmt.Sprintf(tunUDPDetourConfig, chainLogLevel, entryPort, exitPort, `, "detour": "entry"`, echoPort))
	fixture := newTunFixtureWithMTU(t, running, 1280)
	require.Equal(t, 1280, fixture.device.MTU())

	oversized := bytes.Repeat([]byte("o"), 2000)
	fixture.emit(t, udpFrame(netip.MustParseAddr(tunDeviceAddress), 40061, netip.MustParseAddr(tunUDPAddress), echoPort, oversized))
	reply := fixture.waitForUDPDatagramReassembled(t, 40061, echoPort, 1280, 10*time.Second)
	require.Len(t, reply, len(oversized), "the oversized answer must arrive complete and reassembled")
	require.Equal(t, oversized, reply, "the answer must reproduce the oversized datagram byte for byte")

	// The next datagram is still correct: the oversized one is not a wedge.
	payload := bytes.Repeat([]byte("d"), 1000)
	fixture.emit(t, udpFrame(netip.MustParseAddr(tunDeviceAddress), 40062, netip.MustParseAddr(tunUDPAddress), echoPort, payload))
	require.Equal(t, payload, fixture.waitForUDPPayload(t, 40062, echoPort, 10*time.Second),
		"the datagram after the oversized one must still round-trip")
}

// TestTunNarrowMTUOversizedReplyIsFragmented is the outbound half: a fitting
// request whose answer is larger than the MTU must be fragmented, never emitted as
// one oversized frame, and the flow must stay usable.
func TestTunNarrowMTUOversizedReplyIsFragmented(t *testing.T) {
	const replySize = 2000
	replierPort := startSizedUDPReplier(t, replySize, 'L')
	entryPort := freePort(t)
	exitPort := freePort(t)
	startChain(t, fmt.Sprintf(detourSocksServerConfig, chainLogLevel, exitPort))
	startChain(t, fmt.Sprintf(detourSocksServerConfig, chainLogLevel, entryPort))
	running := startChain(t, fmt.Sprintf(tunUDPDetourConfig, chainLogLevel, entryPort, exitPort, `, "detour": "entry"`, replierPort))
	fixture := newTunFixtureWithMTU(t, running, 1280)
	require.Equal(t, 1280, fixture.device.MTU())

	fixture.emit(t, udpFrame(netip.MustParseAddr(tunDeviceAddress), 40071, netip.MustParseAddr(tunUDPAddress), replierPort, []byte("ask")))
	reply := fixture.waitForUDPDatagramReassembled(t, 40071, replierPort, 1280, 10*time.Second)
	require.Len(t, reply, replySize, "the oversized answer must arrive complete after reassembly")
	require.Equal(t, bytes.Repeat([]byte{'L'}, replySize), reply,
		"the reassembled fragments must reproduce the answer byte for byte")

	// The flow still works afterwards: an oversized answer did not break it.
	fixture.emit(t, udpFrame(netip.MustParseAddr(tunDeviceAddress), 40072, netip.MustParseAddr(tunUDPAddress), replierPort, []byte("ask")))
	second := fixture.waitForUDPDatagramReassembled(t, 40072, replierPort, 1280, 10*time.Second)
	require.Len(t, second, replySize, "the second answer must be complete as well")
}
