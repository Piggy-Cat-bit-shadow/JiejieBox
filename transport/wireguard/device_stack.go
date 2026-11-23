package wireguard

import (
	"context"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/sagernet/sing-tun"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/wireguard-go/device"
	wgTun "github.com/sagernet/wireguard-go/tun"
)

var _ Device = (*stackDevice)(nil)
var _ attachmentProvider = (*stackDevice)(nil)

// attachmentProvider is implemented by a Device that can hand each wireguard-go Device a private
// tun view and can detach its peer routes. Only such a Device can release its heavy chain while
// keeping its flows; see stackDevice.
type attachmentProvider interface {
	newAttachment() wgTun.Device
	clearDevice()
}

// stackDevice owns the gVisor stack and the peer router. It is created at Initialize and lives for
// the endpoint's lifetime.
//
// # Why the wireguard-go Device is NOT this object any more
//
// The heavy part of a live inner chain is the wireguard-go Device, not the stack: the Device's
// RoutineReadFromTUN preallocates device.BatchSize() (128, from conn.IdealBatchSize) buffers of
// MaxMessageSize (65,535 B), which is 128 x 65,535 ~= 8.4 MB, plus its queues and per-peer workers.
// The gVisor stack costs ~47 KB and two goroutines (MEASURED, see wg_lazy_device_test.go).
//
// The stack also owns every established flow (tun.GoConn), and the product's suspension contract is
// reversible: a device pause may suspend an on-demand tunnel whose flows are open but silent
// (route/traffic_activity.go states that a COUNT is not activity), and the published platform
// contract is that a pause "never terminates an active flow". Destroying the stack would terminate
// them, so only the Device is destroyed on suspend; each Device gets a fresh stackAttachment that it
// reads/writes/events through, and a detached attachment can no longer reach the stack.
type stackDevice struct {
	stack        *tun.Go
	router       *peerRouter
	mtu          uint32
	inet4Address netip.Addr
	inet6Address netip.Addr
	// closed/events are only used by the Device methods kept for interface satisfaction; wireguard-go
	// talks to a stackAttachment, never to this object.
	closed    chan struct{}
	events    chan wgTun.Event
	closeOnce sync.Once
}

func newStackDevice(options DeviceOptions) (*stackDevice, error) {
	router := newPeerRouter(options)
	stack, err := newStack(options, router.memoryTun)
	if err != nil {
		router.close()
		return nil, err
	}
	inet4Address, inet6Address := deviceAddresses(options.Address)
	return &stackDevice{
		stack:        stack,
		router:       router,
		mtu:          options.MTU,
		inet4Address: inet4Address,
		inet6Address: inet6Address,
		closed:       make(chan struct{}),
		events:       make(chan wgTun.Event, 1),
	}, nil
}

// newAttachment hands one wireguard-go Device its private view of the stack.
//
// The attachment always exists before the Device is constructed, and it is the only object the
// Device's goroutines can call. A later suspend closes it; every method then fails with
// os.ErrClosed and, crucially, Write can no longer inject a decrypted packet into the shared stack,
// so a closed Device can never publish into a new one's flows.
func (w *stackDevice) newAttachment() wgTun.Device {
	attachment := &stackAttachment{
		owner:  w,
		events: make(chan wgTun.Event, 1),
		closed: make(chan struct{}),
	}
	// Same shape the old Start used: publish the up event before the Device exists; it is buffered
	// and consumed by the Device's own event worker.
	attachment.events <- wgTun.EventUp
	return attachment
}

// clearDevice detaches every peer route, so packets from flows that stay in the stack are dropped
// silently while no Device is published. Without this, an open flow's next packet would be handed to
// a Peer that belongs to a Device that is being closed.
func (w *stackDevice) clearDevice() {
	w.router.clearPeers()
}

func (w *stackDevice) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	bind, err := w.bindAddress(destination)
	if err != nil {
		return nil, err
	}
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		var tcpConn *tun.GoConn
		tcpConn, err = w.stack.DialTCP(ctx, bind, destination.AddrPort())
		if err != nil {
			return nil, err
		}
		tcpConn.SetKeepAliveConfig(net.KeepAliveConfig{Enable: true, Idle: 15 * time.Second, Interval: 15 * time.Second})
		return tcpConn, nil
	case N.NetworkUDP:
		var udpConn *tun.GoUDPConn
		udpConn, err = w.stack.DialUDP(netip.AddrPortFrom(bind, 0), destination.AddrPort())
		if err != nil {
			return nil, err
		}
		return udpConn, nil
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
}

func (w *stackDevice) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	bind, err := w.bindAddress(destination)
	if err != nil {
		return nil, err
	}
	udpConn, err := w.stack.ListenUDP(netip.AddrPortFrom(bind, 0))
	if err != nil {
		return nil, err
	}
	return udpConn, nil
}

func (w *stackDevice) bindAddress(destination M.Socksaddr) (netip.Addr, error) {
	if destination.IsIPv4() {
		if !w.inet4Address.IsValid() {
			return netip.Addr{}, E.New("missing IPv4 local address")
		}
		return w.inet4Address, nil
	}
	if !w.inet6Address.IsValid() {
		return netip.Addr{}, E.New("missing IPv6 local address")
	}
	return w.inet6Address, nil
}

func (w *stackDevice) Inet4Address() netip.Addr {
	return w.inet4Address
}

func (w *stackDevice) Inet6Address() netip.Addr {
	return w.inet6Address
}

func (w *stackDevice) UpstreamPort() any {
	return w.stack
}

func (w *stackDevice) SetDevice(device *device.Device, peers []*device.Peer) {
	w.router.setPeers(device.AllowedIPs(), peers)
}

func (w *stackDevice) Start() error {
	return w.stack.Start()
}

func (w *stackDevice) File() *os.File {
	return nil
}

func (w *stackDevice) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	<-w.closed
	return 0, os.ErrClosed
}

func (w *stackDevice) Write(bufs [][]byte, offset int) (int, error) {
	packets := make([][]byte, 0, len(bufs))
	for _, packet := range bufs {
		packets = append(packets, packet[offset:])
	}
	return w.router.memoryTun.WritePackets(packets)
}

func (w *stackDevice) Flush() error {
	return nil
}

func (w *stackDevice) MTU() (int, error) {
	return int(w.mtu), nil
}

func (w *stackDevice) Name() (string, error) {
	return "sing-box", nil
}

func (w *stackDevice) Events() <-chan wgTun.Event {
	return w.events
}

func (w *stackDevice) Close() error {
	var err error
	w.closeOnce.Do(func() {
		close(w.closed)
		close(w.events)
		err = E.Errors(w.stack.Close(), w.router.close())
	})
	return err
}

func (w *stackDevice) BatchSize() int {
	return 1
}

// stackAttachment is one wireguard-go Device's private view of a stackDevice.
//
// It exists so "destroy the Device" and "destroy the stack" can be two different events. A
// suspended endpoint closes the attachment and the Device, while the stack keeps its flows; the
// next demand creates a new attachment for a new Device. See stackDevice's comment for why that
// split is the contract.
type stackAttachment struct {
	owner     *stackDevice
	events    chan wgTun.Event
	closed    chan struct{}
	closeOnce sync.Once
}

func (a *stackAttachment) File() *os.File {
	return nil
}

// Read blocks exactly as the old stackDevice.Read did: this architecture injects outbound packets
// through the peer router, so the wireguard-go TUN reader never receives one. It only has to stay
// blocked until Close, and Close is what unblocks it.
func (a *stackAttachment) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	<-a.closed
	return 0, os.ErrClosed
}

func (a *stackAttachment) Write(bufs [][]byte, offset int) (int, error) {
	select {
	case <-a.closed:
		return 0, os.ErrClosed
	default:
	}
	packets := make([][]byte, 0, len(bufs))
	for _, packet := range bufs {
		packets = append(packets, packet[offset:])
	}
	return a.owner.router.memoryTun.WritePackets(packets)
}

func (a *stackAttachment) Flush() error {
	return nil
}

func (a *stackAttachment) MTU() (int, error) {
	return int(a.owner.mtu), nil
}

func (a *stackAttachment) Name() (string, error) {
	return "sing-box", nil
}

func (a *stackAttachment) Events() <-chan wgTun.Event {
	return a.events
}

func (a *stackAttachment) BatchSize() int {
	return 1
}

func (a *stackAttachment) Close() error {
	a.closeOnce.Do(func() {
		close(a.closed)
		close(a.events)
	})
	return nil
}
