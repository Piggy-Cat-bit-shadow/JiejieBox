package route

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The NON-connected UDP arm is a dial too.
//
// # The defect these tests were written against
//
// `dial_setup.go` states the contract: "begin - one per dial, from ConnectionManager.NewConnection /
// NewPacketConnection (the route layer's flow goroutine)". `NewConnection` honours it for every
// stream. `NewPacketConnection` honours it only on the `UDPConnect` arm.
//
// The other arm - `ListenPacket` / `ListenPacketWithDestination` / `ListenSerialNetworkPacket`, which
// is what an ordinary proxied UDP flow takes, because the TUN's NAT connection has a fixed peer but
// does not report `IsUDPConnect` - called the outbound with the caller's context and nothing else.
//
// Three properties were therefore missing on the path that carries the measured UDP burst:
//
//  1. a network transition did not cancel the setup, so a listen against a black-holed path kept its
//     NAT session, its goroutine and its retained first datagram beyond the transition that made the
//     path stale;
//  2. the resolved traffic class never reached the outbound, so the admission seam inside it could
//     not see an interactive flow and the interactive reserve was unreachable for UDP;
//  3. the dial gate that smooths a reconnect herd did not apply.
//
// # Why these are not merely "symmetry" tests
//
// A packet flow's setup is where the expensive shared session is created (UoT over AnyTLS, a QUIC
// session, a SOCKS UDP associate). Leaving that one arm uncancellable is the difference between a
// transition that reclaims its stale setup and one that waits for the outbound's own timeout - which
// is the failure dial_setup.go documents as the reason the gate exists at all.

// packetSetupRecorder records the context and the traffic class the non-connected arm handed to the
// outbound, then fails so the manager takes its error branch and no copy loop starts.
type packetSetupRecorder struct {
	access  sync.Mutex
	ctx     context.Context
	class   trafficclass.Class
	loaded  bool
	calls   int
	network string
}

func (d *packetSetupRecorder) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, errPacketSetupRecorded
}

func (d *packetSetupRecorder) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	d.access.Lock()
	d.calls++
	d.ctx = ctx
	d.class, d.loaded = trafficclass.ClassFromContext(ctx)
	d.access.Unlock()
	return nil, errPacketSetupRecorded
}

var errPacketSetupRecorded = io.ErrUnexpectedEOF

// TestNetworkTransitionCancelsAnInFlightPacketSetup is the lifecycle half.
//
// RED before the fix: the listen runs under the caller's background context, `Reclaim` has nothing to
// cancel, and NewPacketConnection never returns. GREEN with the setup gate: the stale generation's
// context ends the setup and the flow reports context.Canceled.
func TestNetworkTransitionCancelsAnInFlightPacketSetup(t *testing.T) {
	manager := transitionManager(t)
	dialer := newBlackholeTestDialer()

	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.NewPacketConnection(context.Background(), dialer, packetConnStub{}, adapter.InboundContext{
			Destination: M.ParseSocksaddrHostPort("stun.example", 3478),
		}, nil)
	}()

	setupContext := dialer.waitForDial(t)
	require.NoError(t, setupContext.Err(), "the packet setup must be in flight before the transition")

	manager.Reclaim(ReclaimNetworkTransition)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a network transition did not cancel the in-flight non-connected UDP setup: the flow's " +
			"NAT session, goroutine and retained datagram would outlive the path it was dialled on")
	}
	require.ErrorIs(t, dialer.dialError(), context.Canceled,
		"the cancelled packet setup must surface as a cancellation, not as a timeout or a path error")
	require.Error(t, setupContext.Err(), "the setup context itself must be released")
}

// TestPacketSetupCarriesTheTrafficClass is the admission half.
//
// The class is resolved once at the route layer and reaches every admission seam through the dial
// context (trafficclass.WithClass). The connected arm attaches it; the non-connected arm did not, so
// an interactive UDP flow was charged as background work and could never use the interactive reserve
// that common/resourceguard exists to keep for it.
func TestPacketSetupCarriesTheTrafficClass(t *testing.T) {
	manager := transitionManager(t)
	recorder := &packetSetupRecorder{}

	manager.NewPacketConnection(context.Background(), recorder, packetConnStub{}, adapter.InboundContext{
		Destination:  M.ParseSocksaddrHostPort("api.example", 443),
		TrafficClass: trafficclass.ClassInteractive,
	}, nil)

	recorder.access.Lock()
	defer recorder.access.Unlock()
	require.Equal(t, 1, recorder.calls, "the non-connected arm must dial exactly once")
	require.True(t, recorder.loaded,
		"the resolved traffic class must travel to the outbound's admission seam, or UDP can never use the "+
			"interactive reserve")
	require.Equal(t, trafficclass.ClassInteractive, recorder.class)
}

// establishedPacketDialer returns a real UDP socket as the packet connection and records the setup
// context it was handed.
type establishedPacketDialer struct {
	conn   net.PacketConn
	access sync.Mutex
	ctx    context.Context
}

func (d *establishedPacketDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, errPacketSetupRecorded
}

func (d *establishedPacketDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	d.access.Lock()
	d.ctx = ctx
	d.access.Unlock()
	return d.conn, nil
}

func (d *establishedPacketDialer) setupContext() context.Context {
	d.access.Lock()
	defer d.access.Unlock()
	return d.ctx
}

// blockingPacketSource blocks the upload copy until Close, so the flow stays established for the
// duration of the assertions instead of ending when the source returns EOF.
type blockingPacketSource struct {
	release   chan struct{}
	closeOnce sync.Once
}

func newBlockingPacketSource() *blockingPacketSource {
	return &blockingPacketSource{release: make(chan struct{})}
}

func (s *blockingPacketSource) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	<-s.release
	return M.Socksaddr{}, io.EOF
}

func (s *blockingPacketSource) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	buffer.Release()
	return nil
}

func (s *blockingPacketSource) ReadFrom(p []byte) (int, net.Addr, error) {
	<-s.release
	return 0, nil, io.EOF
}

func (s *blockingPacketSource) WriteTo(p []byte, addr net.Addr) (int, error) {
	return len(p), nil
}

func (s *blockingPacketSource) Close() error {
	s.closeOnce.Do(func() { close(s.release) })
	return nil
}

func (s *blockingPacketSource) LocalAddr() net.Addr                { return &net.UDPAddr{} }
func (s *blockingPacketSource) SetDeadline(t time.Time) error      { return nil }
func (s *blockingPacketSource) SetReadDeadline(t time.Time) error  { return nil }
func (s *blockingPacketSource) SetWriteDeadline(t time.Time) error { return nil }

// TestEstablishedPacketFlowSurvivesATransition pins the ownership transfer on the non-connected arm.
//
// The setup context must be released as soon as the listen returns - leaving it live would keep one
// dead generation registration per packet flow - and a transition arriving afterwards must not reach
// the established socket through it. Both halves are asserted: the context is done, and the socket is
// still usable after a transition.
func TestEstablishedPacketFlowSurvivesATransition(t *testing.T) {
	manager := transitionManager(t)
	sink, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	dialer := &establishedPacketDialer{conn: sink}
	source := newBlockingPacketSource()
	t.Cleanup(func() { _ = source.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.NewPacketConnection(context.Background(), dialer, source, adapter.InboundContext{
			Destination: M.SocksaddrFromNet(sink.LocalAddr()),
		}, nil)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the packet setup did not establish")
	}

	setupContext := dialer.setupContext()
	require.NotNil(t, setupContext)
	require.ErrorIs(t, setupContext.Err(), context.Canceled,
		"establishment must release the setup context: leaving it live would keep a generation registration "+
			"per packet flow")

	manager.Reclaim(ReclaimNetworkTransition)
	time.Sleep(50 * time.Millisecond)

	require.NoError(t, sink.SetReadDeadline(time.Now()),
		"a network transition after establishment must not close the packet flow through the setup context")
}
