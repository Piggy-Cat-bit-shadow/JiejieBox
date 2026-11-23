package wireguard

// Replay rejection on the real data plane (W5 proof 6).
//
// WireGuard's replay filter is a property of the pinned Device, not of the lazy release, but the
// proof has to be on the wire: a captured transport message is replayed through the same socket and
// the far end must not deliver a second inner packet. The test then sends a fresh packet to prove
// the silence was the filter and not a dead session.

import (
	"context"
	"crypto/ecdh"
	"encoding/hex"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/wireguard-go/conn"
	wgDevice "github.com/sagernet/wireguard-go/device"

	"github.com/stretchr/testify/require"
)

// replayBind is a conn.Bind that records every buffer the module sends and forwards it over a real
// UDP socket. Unlike the framing harness's bind it records EVERY message: AmneziaWG replaces the
// transport message type with a magic header, so filtering on the plain type would silently record
// nothing in the AWG variant.
type replayBind struct {
	socket   *net.UDPConn
	access   sync.Mutex
	captured chan []byte
}

func newReplayBind() *replayBind {
	return &replayBind{captured: make(chan []byte, 1024)}
}

func (b *replayBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)})
	if err != nil {
		return nil, 0, err
	}
	b.access.Lock()
	b.socket = socket
	b.access.Unlock()
	return []conn.ReceiveFunc{b.receive}, uint16(socket.LocalAddr().(*net.UDPAddr).Port), nil
}

func (b *replayBind) receive(packets [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
	b.access.Lock()
	socket := b.socket
	b.access.Unlock()
	if socket == nil {
		return 0, net.ErrClosed
	}
	count, addr, err := socket.ReadFromUDP(packets[0])
	if err != nil {
		return 0, err
	}
	sizes[0] = count
	eps[0] = remoteEndpoint(netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), uint16(addr.Port)))
	return 1, nil
}

func (b *replayBind) Send(bufs [][]byte, ep conn.Endpoint, offset int) error {
	b.access.Lock()
	socket := b.socket
	b.access.Unlock()
	if socket == nil {
		return net.ErrClosed
	}
	destination := netip.AddrPort(ep.(remoteEndpoint))
	for _, buf := range bufs {
		payload := buf[offset:]
		recorded := make([]byte, len(payload))
		copy(recorded, payload)
		select {
		case b.captured <- recorded:
		default:
		}
		if _, err := socket.WriteToUDPAddrPort(payload, destination); err != nil {
			return err
		}
	}
	return nil
}

func (b *replayBind) Close() error {
	b.access.Lock()
	defer b.access.Unlock()
	if b.socket == nil {
		return nil
	}
	err := b.socket.Close()
	b.socket = nil
	return err
}

func (b *replayBind) SetMark(mark uint32) error { return nil }

func (b *replayBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	addrPort, err := netip.ParseAddrPort(s)
	if err != nil {
		return nil, err
	}
	return remoteEndpoint(addrPort), nil
}

func (b *replayBind) BatchSize() int { return 1 }

func (b *replayBind) SetReservedForEndpoint(destination netip.AddrPort, reserved [3]byte) {}

// replayHarness is two real wireguard-go devices over loopback UDP, the client's bind recorded.
type replayHarness struct {
	clientTun  *framingTun
	serverTun  *framingTun
	capture    *replayBind
	client     *wgDevice.Device
	server     *wgDevice.Device
	serverPort uint16
}

func newReplayHarness(t *testing.T, awgLines string) *replayHarness {
	t.Helper()
	ctx := context.Background()
	logger := &wgDevice.Logger{
		Verbosef: func(format string, args ...any) {},
		Errorf:   func(format string, args ...any) { t.Logf("wireguard-go: "+format, args...) },
	}
	clientKey := mustGenerateKey(t)
	serverKey := mustGenerateKey(t)

	harness := &replayHarness{
		clientTun: newFramingTun(1408),
		serverTun: newFramingTun(1408),
		capture:   newReplayBind(),
	}
	t.Cleanup(func() {
		harness.client.Close()
		harness.server.Close()
	})
	serverConfig := replayDeviceConfig(serverKey, 0, clientKey.PublicKey(), "10.0.0.2/32", "")
	serverConfig = insertAWGLines(serverConfig, awgLines)
	harness.server = wgDevice.NewDevice(ctx, harness.serverTun, conn.NewStdNetBind(nil), logger, 2)
	require.NoError(t, harness.server.IpcSet(serverConfig))
	require.NoError(t, harness.server.Up())
	harness.serverPort = requireServerPort(t, harness.server)

	clientConfig := replayDeviceConfig(clientKey, 0, serverKey.PublicKey(), "10.0.0.1/32",
		"127.0.0.1:"+strconv.Itoa(int(harness.serverPort)))
	clientConfig = insertAWGLines(clientConfig, awgLines)
	harness.client = wgDevice.NewDevice(ctx, harness.clientTun, harness.capture, logger, 2)
	require.NoError(t, harness.client.IpcSet(clientConfig))
	require.NoError(t, harness.client.Up())
	return harness
}

func replayDeviceConfig(privateKey *ecdh.PrivateKey, listenPort uint16, peerPublicKey *ecdh.PublicKey, allowedIP string, endpoint string) string {
	config := "private_key=" + hex.EncodeToString(privateKey.Bytes())
	if listenPort != 0 {
		config += "\nlisten_port=" + strconv.Itoa(int(listenPort))
	}
	config += "\npublic_key=" + hex.EncodeToString(peerPublicKey.Bytes())
	if endpoint != "" {
		config += "\nendpoint=" + endpoint
	}
	config += "\nallowed_ip=" + allowedIP + "\n"
	return config
}

// insertAWGLines puts the device-global obfuscation lines before the first peer section.
func insertAWGLines(config string, awgLines string) string {
	if awgLines == "" {
		return config
	}
	return strings.Replace(config, "\npublic_key=", awgLines+"\npublic_key=", 1)
}

func (h *replayHarness) deliverOne(t *testing.T) {
	t.Helper()
	h.clientTun.inject(t, innerIPv4Packet(80))
	select {
	case <-h.serverTun.received:
	case <-time.After(5 * time.Second):
		t.Fatal("the session did not carry the probe packet")
	}
}

func (h *replayHarness) drainCaptures() {
	for {
		select {
		case <-h.capture.captured:
		default:
			return
		}
	}
}

// TestAReplayedTransportMessageIsRejectedByThePinnedFilter is the plain WireGuard replay proof.
func TestAReplayedTransportMessageIsRejectedByThePinnedFilter(t *testing.T) {
	runReplayProof(t, "")
}

// runReplayProof is the shared body; the AWG variant calls it with the obfuscation lines.
func runReplayProof(t *testing.T, awgLines string) {
	t.Helper()
	harness := newReplayHarness(t, awgLines)
	harness.deliverOne(t) // establish the session
	harness.drainCaptures()

	// Send one packet and capture its transport message. deliverOne consumes the far end's copy.
	harness.clientTun.inject(t, innerIPv4Packet(80))
	var captured []byte
	select {
	case captured = <-harness.capture.captured:
	case <-time.After(5 * time.Second):
		t.Fatal("no transport message was captured")
	}
	select {
	case <-harness.serverTun.received:
	case <-time.After(5 * time.Second):
		t.Fatal("the captured message was never delivered for the first time")
	}
	require.NotEmpty(t, captured)

	// Replay the exact bytes through the same source socket: the far end must drop it.
	harness.capture.access.Lock()
	socket := harness.capture.socket
	harness.capture.access.Unlock()
	require.NotNil(t, socket)
	_, err := socket.WriteToUDPAddrPort(captured, netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), harness.serverPort))
	require.NoError(t, err)
	select {
	case duplicate := <-harness.serverTun.received:
		t.Fatalf("a replayed transport message was delivered to the far end (%d bytes)", len(duplicate))
	case <-time.After(600 * time.Millisecond):
	}

	// Positive control: the session is alive, so the silence above was the replay filter.
	harness.deliverOne(t)
}
