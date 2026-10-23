package e2e

import (
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing-tun/gtcpip/header"

	"github.com/stretchr/testify/require"
)

// The Direct Fast Path verdict, carried through the real product chain: real IPv4/TCP and UDP
// frames, the real Go stack, the real Router, a real direct outbound and a real peer.
//
// # What this file adds to the two existing traces
//
// protocol/tun/native_bypass_trace_test.go proves at the STACK level that an ActionBypass SYN is
// terminated in userspace exactly like an ActionAccept one. protocol/tun/native_bypass_dispatcher_test.go
// proves the shared dispatcher consumes neither. Both stop at the Handler.
//
// This file starts there: the Handler is protocol/tun.Inbound's delegation, driven through the real
// router's PreMatch by adapter.JudgeFlow, and the connection is then routed, dialled and copied by
// the real ConnectionManager into a real peer. It answers the question neither of those can: when
// the router marks a flow bypassable, do the BYTES still move, and through whom?
//
// The division of labour is deliberate. Packets are written straight into the stack, so this file
// exercises the Handler seam rather than ForwardStage.Dispatch; the dispatcher's treatment of the
// verdict is the other file's subject. Between them the chain is covered from the verdict to the
// bytes, and neither claims the other's half.
//
// # The four cases, and why each exists
//
//	A  fast_path absent   -> the router produces ActionBypass, and the flows must still work
//	B  fast_path true     -> the same verdict and the same bytes (true and unset are equivalent)
//	C  fast_path false    -> the router refuses the ATTEMPT, so the verdict is ActionAccept, and the
//	                         bytes are identical: the field controls the attempt, not the data path
//	D  a tracker attached -> the verdict becomes ActionAccept (BypassRefusedTracker), and the bytes
//	                         are identical. This is the second verdict-reversal control: a hard gate,
//	                         not the policy, is what flips it.
//
// A, B and C run on a box with no tracker, which is the state a production box without a dashboard
// API is in; the default e2e harness attaches one and would therefore put every flow in case D.
//
// # The platform limit
//
// Windows, like every platform this suite runs on, has no transport that honours ActionBypass: the
// go stack terminates the connection and calls the Handler. The Linux auto_redirect path - where
// nfqueue maps the verdict to NfRepeat with the output mark - cannot run here and is NOT_VERIFIED
// rather than assumed. What this test proves is exactly the Windows/macOS/gVisor half: a bypass
// verdict is not a bypass.

// fastPathDataplaneConfig is the smallest topology that can produce a Direct Fast Path verdict over
// real packets: one selector over the one direct outbound, one rule routing the peer's address and
// port into it, and a socks sink that must never be reached.
//
// The destination is a literal address on a local interface, because the fast path refuses a domain
// destination, a FakeIP destination, a rewritten destination and a sniffed domain. The peer listens
// on that interface, so the direct outbound's dial is a real connect to a real listener.
const fastPathDataplaneConfig = `{
  "log": {"level": "%s"},
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "socks", "tag": "remote", "server": "127.0.0.1", "server_port": %d, "version": "5"},
    {"type": "selector", "tag": "直连组", "outbounds": ["direct"], "default": "direct"%s}
  ],
  "route": {
    "rules": [
      {"ip_cidr": ["%s/32"], "port": [%d], "action": "route", "outbound": "直连组"}
    ],
    "final": "direct"
  }
}`

// verdictRecorder collects the actions the router produced for the flows the stack created.
type verdictRecorder struct {
	access  sync.Mutex
	actions []tun.FlowAction
}

func (r *verdictRecorder) record(action tun.FlowAction) {
	r.access.Lock()
	defer r.access.Unlock()
	r.actions = append(r.actions, action)
}

func (r *verdictRecorder) snapshot() []tun.FlowAction {
	r.access.Lock()
	defer r.access.Unlock()
	return append([]tun.FlowAction(nil), r.actions...)
}

// TestFastPathVerdictDoesNotChangeTheTUNDataPath is the product-level negative control.
func TestFastPathVerdictDoesNotChangeTheTUNDataPath(t *testing.T) {
	destination := localPeerAddress(t)
	echo := startEchoServer(t, "tcp", net.JoinHostPort(destination.String(), "0"))
	echoPort := uint16(echo.listener.Addr().(*net.TCPAddr).Port)
	sink := startSocksSink(t, "127.0.0.1:0")

	cases := []struct {
		name       string
		fastPath   string
		attachFlow bool
		wantAction tun.FlowAction
	}{
		{name: "absent", fastPath: "", wantAction: tun.ActionBypass},
		{name: "true", fastPath: `, "fast_path": true`, wantAction: tun.ActionBypass},
		// An explicit false is a decision: it withholds the attempt the intrinsic direct chain would
		// otherwise take. The flow is still routed to the same direct outbound - only the verdict
		// changes, which is the point of keeping the bytes assertions identical across all cases.
		{name: "false", fastPath: `, "fast_path": false`, wantAction: tun.ActionAccept},
		{name: "tracker-attached", fastPath: "", attachFlow: true, wantAction: tun.ActionAccept},
	}

	for index, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := fmt.Sprintf(fastPathDataplaneConfig, chainLogLevel,
				uint16(sink.listener.Addr().(*net.TCPAddr).Port), testCase.fastPath,
				destination.String(), echoPort)
			var running *chain
			if testCase.attachFlow {
				running = startChain(t, config)
			} else {
				running = startChainUntracked(t, config)
			}

			verdicts := &verdictRecorder{}
			judge := func(network uint8, source netip.AddrPort, destinationAddress netip.AddrPort, firstPacket []byte) tun.FlowVerdict {
				verdict := adapter.JudgeFlow(running.router(), adapter.InboundContext{
					Inbound:     "tun-in",
					InboundType: C.TypeTun,
				}, network, source, destinationAddress, firstPacket)
				verdicts.record(verdict.Action)
				return verdict
			}
			fixture := newTunFixtureWithJudge(t, running, netip.Addr{}, 0, judge)

			sourcePort := uint16(42000 + index)
			connection := &tcpOverTun{
				sourcePort:      sourcePort,
				destination:     destination,
				destinationPort: echoPort,
			}
			fixture.dialTCP(t, connection, 10*time.Second)
			connection.write(t, "direct-fast-path-payload")
			connection.expectEcho(t, "direct-fast-path-payload", 10*time.Second)

			actions := verdicts.snapshot()
			require.Len(t, actions, 1, "one TCP flow, one verdict")
			require.Equal(t, testCase.wantAction, actions[0],
				"case %s: the router must produce the verdict this case documents", testCase.name)
			require.GreaterOrEqual(t, fixture.handler.delegations.Load(), int64(1),
				"the stack must hand the flow to the handler: a natively carried flow never arrives "+
					"here, so this is the product-level proof that ActionBypass did not leave userspace")
			require.GreaterOrEqual(t, echo.accepts.Load(), int64(1),
				"the peer must have accepted a real connection")
			require.Greater(t, echo.bytes.Load(), int64(0), "and real bytes must have crossed")
			require.Empty(t, sink.seen(),
				"the flow is routed to direct; no proxied copy may reach the socks outbound")

			if testCase.attachFlow {
				flows := running.tracker.waitForFlows(t, 1, 10*time.Second)
				require.Equal(t, "直连组", flows[0].RouteOutbound)
				require.Equal(t, []string{"直连组", "direct"}, flows[0].OutboundChain,
					"the tracker still sees the flow, because the verdict that was refused was the "+
						"bypass attempt and not the connection")
			}
		})
	}
}

// TestBypassVerdictStillDeliversUDP is the datagram half: the close of the same question on the
// network where the accept entry carries the verdict into the UDP NAT session.
func TestBypassVerdictStillDeliversUDP(t *testing.T) {
	destination := localPeerAddress(t)
	echo := startUDPEchoServer(t, "udp", net.JoinHostPort(destination.String(), "0"))
	echoPort := uint16(echo.conn.LocalAddr().(*net.UDPAddr).Port)
	sink := startSocksSink(t, "127.0.0.1:0")

	config := fmt.Sprintf(fastPathDataplaneConfig, chainLogLevel,
		uint16(sink.listener.Addr().(*net.TCPAddr).Port), "", destination.String(), echoPort)
	running := startChainUntracked(t, config)

	verdicts := &verdictRecorder{}
	judge := func(network uint8, source netip.AddrPort, destinationAddress netip.AddrPort, firstPacket []byte) tun.FlowVerdict {
		verdict := adapter.JudgeFlow(running.router(), adapter.InboundContext{
			Inbound:     "tun-in",
			InboundType: C.TypeTun,
		}, network, source, destinationAddress, firstPacket)
		verdicts.record(verdict.Action)
		return verdict
	}
	fixture := newTunFixtureWithJudge(t, running, netip.Addr{}, 0, judge)

	fixture.emit(t, udpFrame(netip.MustParseAddr(tunDeviceAddress), 42050, destination, echoPort, []byte("bypass-udp")))
	reply := fixture.waitForUDPPayload(t, 42050, echoPort, 10*time.Second)
	require.Equal(t, "bypass-udp", string(reply),
		"the datagram must come back through the stack; a bypassed flow would never produce a reply "+
			"in this process")

	actions := verdicts.snapshot()
	require.NotEmpty(t, actions)
	for _, action := range actions {
		require.Equal(t, tun.ActionBypass, action,
			"the UDP flow is bypass-eligible, and the verdict must not change where its bytes go")
	}
	require.Empty(t, sink.seen(), "no proxied copy")
}

// TestFastPathMultiSegmentPayloadIsIntact is the byte-integrity half of the verdict control.
//
// The small payloads in the cases above prove the path works; this one proves it carries a
// multi-segment stream unchanged while the router has marked the flow bypassable. Every byte is
// compared, not just the length, because a truncated or reordered copy is exactly what a fallback
// bug would produce and a length check would miss it.
func TestFastPathMultiSegmentPayloadIsIntact(t *testing.T) {
	destination := localPeerAddress(t)
	echo := startEchoServer(t, "tcp", net.JoinHostPort(destination.String(), "0"))
	echoPort := uint16(echo.listener.Addr().(*net.TCPAddr).Port)
	sink := startSocksSink(t, "127.0.0.1:0")

	config := fmt.Sprintf(fastPathDataplaneConfig, chainLogLevel,
		uint16(sink.listener.Addr().(*net.TCPAddr).Port), "", destination.String(), echoPort)
	running := startChainUntracked(t, config)

	verdicts := &verdictRecorder{}
	judge := func(network uint8, source netip.AddrPort, destinationAddress netip.AddrPort, firstPacket []byte) tun.FlowVerdict {
		verdict := adapter.JudgeFlow(running.router(), adapter.InboundContext{
			Inbound:     "tun-in",
			InboundType: C.TypeTun,
		}, network, source, destinationAddress, firstPacket)
		verdicts.record(verdict.Action)
		return verdict
	}
	fixture := newTunFixtureWithJudge(t, running, netip.Addr{}, 0, judge)

	connection := &tcpOverTun{sourcePort: 42090, destination: destination, destinationPort: echoPort}
	fixture.dialTCP(t, connection, 10*time.Second)

	payload := make([]byte, 8192)
	for index := range payload {
		payload[index] = byte(index % 251)
	}
	connection.writeChunks(t, payload, 1024)
	echoed := connection.collectEchoBytes(t, len(payload), 10*time.Second)
	require.Equal(t, payload, echoed,
		"an 8 KiB multi-segment stream must arrive byte-identical through the bypassed flow")

	actions := verdicts.snapshot()
	require.Len(t, actions, 1)
	require.Equal(t, tun.ActionBypass, actions[0],
		"the flow under test must be the bypass-eligible one, or the integrity result is about a "+
			"different path")
	require.Empty(t, sink.seen(), "no proxied copy")
}

// writeChunks sends payload in chunkSize segments, advancing the sequence number exactly as one
// write of the whole payload would.
func (c *tcpOverTun) writeChunks(t *testing.T, payload []byte, chunkSize int) {
	t.Helper()
	for offset := 0; offset < len(payload); offset += chunkSize {
		end := offset + chunkSize
		if end > len(payload) {
			end = len(payload)
		}
		c.write(t, string(payload[offset:end]))
	}
}

// collectEchoBytes reads device-facing payload segments for this flow until total bytes have
// arrived, acknowledging each one so the stack's send window stays open.
//
// It is deliberately stateful: waitForTCPPayload returns the FIRST matching segment each call, so a
// loop over it would read the same segment repeatedly and "prove" a one-segment transfer.
func (c *tcpOverTun) collectEchoBytes(t *testing.T, total int, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	collected := make([]byte, 0, total)
	for len(collected) < total {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d echoed bytes arrived before the deadline", len(collected), total)
		}
		segment, payload := c.nextEchoSegment()
		if payload == nil {
			select {
			case <-c.fixture.frameSignal:
			case <-time.After(2 * time.Millisecond):
			}
			continue
		}
		collected = append(collected, payload...)
		c.nextAck = segment.SequenceNumber() + uint32(len(payload))
		c.fixture.emit(t, tcpFrame(netip.MustParseAddr(tunDeviceAddress), c.sourcePort,
			c.destination, c.destinationPort, c.nextSeq, c.nextAck, header.TCPFlagAck, nil))
	}
	return collected
}

// nextEchoSegment returns the device-facing segment whose sequence is the next one expected, or nil.
func (c *tcpOverTun) nextEchoSegment() (header.TCP, []byte) {
	c.fixture.access.Lock()
	defer c.fixture.access.Unlock()
	for _, frame := range c.fixture.frames {
		ip, transport, ok := parseFrame(frame)
		if !ok || ip.TransportProtocol() != header.TCPProtocolNumber {
			continue
		}
		segment := header.TCP(transport)
		if segment.SourcePort() != c.destinationPort || segment.DestinationPort() != c.sourcePort {
			continue
		}
		payload := transport[segment.DataOffset():]
		if len(payload) == 0 || segment.SequenceNumber() != c.nextAck {
			continue
		}
		return segment, append([]byte(nil), payload...)
	}
	return nil, nil
}

// localPeerAddress finds a literal address on a local interface that the host can dial and that the
// TUN stack will accept as a global-unicast destination.
//
// The fast path refuses rewrites, so the test cannot route a fake destination to loopback; the peer
// has to be at the address the packet names. Candidates are PROBED rather than assumed: a machine
// can list an address on an interface that is up but cannot complete a loopback connection through
// it (a container bridge, an address on a VPN that is still settling), and failing such a test would
// report an environment fact as a product defect. The first candidate that accepts a real TCP
// connection to a listener on itself is used; if none does, the test says so rather than passing.
func localPeerAddress(t *testing.T) netip.Addr {
	t.Helper()
	interfaces, err := net.Interfaces()
	require.NoError(t, err)
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, addressErr := networkInterface.Addrs()
		if addressErr != nil {
			continue
		}
		for _, address := range addresses {
			prefix, parseErr := netip.ParsePrefix(address.String())
			if parseErr != nil {
				continue
			}
			candidate := prefix.Addr().Unmap()
			if !candidate.Is4() || !candidate.IsGlobalUnicast() || candidate.IsLoopback() || candidate.IsLinkLocalUnicast() {
				continue
			}
			if addressAcceptsLoopbackConnection(candidate) {
				return candidate
			}
		}
	}
	t.Skip("no non-loopback global-unicast IPv4 address on this machine accepts a connection to " +
		"itself: the direct fast path refuses a rewritten destination, so a real peer cannot be " +
		"reached over the in-memory TUN. This is a NOT_VERIFIED result for the end-to-end half, " +
		"not a pass")
	return netip.Addr{}
}

// addressAcceptsLoopbackConnection proves the address is usable as a peer before a test relies on
// it: bind a listener to it, connect to it, and close both.
func addressAcceptsLoopbackConnection(address netip.Addr) bool {
	listener, err := net.Listen("tcp", net.JoinHostPort(address.String(), "0"))
	if err != nil {
		return false
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	accepted := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			connection.Close()
		}
		close(accepted)
	}()
	connection, err := net.DialTimeout("tcp", net.JoinHostPort(address.String(), fmt.Sprint(port)), 2*time.Second)
	if err != nil {
		return false
	}
	connection.Close()
	select {
	case <-accepted:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}
