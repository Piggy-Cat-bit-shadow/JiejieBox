package route

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Local admission backpressure must not be recorded as evidence about the node.
//
// # The defect these tests were written against
//
// When a dial fails, route clears the selected URLTest leaf's measurement so the group stops
// preferring a node that just failed to dial. That is right for a path failure and wrong for this
// process's own admission decision: with the creation gate saturated (or a rate window armed), the
// outbound returns resourceguard.ErrQueueFull/ErrWaitTimeout/ErrRateLimited WITHOUT the far side
// having been tried at all. Clearing the measurement then makes a perfectly healthy member look
// unmeasured and can make the group re-select because this process was busy - local backpressure
// turned into node-health evidence.
//
// The exposure grew in round 1: transport.type=quic VLESS/VMess/Trojan creations now pass through the
// same gate, so a refusal on that path reaches this error handler where it previously could not.
//
// # Why the control test matters
//
// A test that only asserts "history survived" would also pass if history deletion were removed
// entirely. The control below pins that a real dial failure still clears it.

type admissionRejectingDialer struct {
	dialErr   error
	listenErr error
}

func (d admissionRejectingDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, d.dialErr
}

func (d admissionRejectingDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, d.listenErr
}

func newHealthManager(t *testing.T) (*ConnectionManager, *urltest.HistoryStorage) {
	t.Helper()
	storage := urltest.NewHistoryStorage()
	storage.StoreURLTestHistory("leaf-a", &adapter.URLTestHistory{Delay: 120})
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = manager.Close() })
	manager.SetURLTestHistory(storage)
	return manager, storage
}

func historyChain() []adapter.Outbound {
	return []adapter.Outbound{
		&historyStubGroup{tag: "auto"},
		&historyStubOutbound{tag: "leaf-a"},
	}
}

// TestLocalAdmissionRefusalKeepsTheNodeMeasurement is the regression for the TCP arm.
func TestLocalAdmissionRefusalKeepsTheNodeMeasurement(t *testing.T) {
	manager, storage := newHealthManager(t)
	inbound, inboundPeer := net.Pipe()
	t.Cleanup(func() { _ = inboundPeer.Close() })

	manager.NewConnection(context.Background(), admissionRejectingDialer{
		dialErr: resourceguard.ErrQueueFull,
	}, inbound, adapter.InboundContext{
		Destination:   M.ParseSocksaddrHostPort("example.com", 443),
		OutboundChain: historyChain(),
	}, nil)

	require.NotNil(t, storage.LoadURLTestHistory("leaf-a"),
		"a local admission refusal deleted a healthy node's measurement: the group will re-select "+
			"because this process was busy, not because the node failed")
}

// TestLocalAdmissionRefusalKeepsTheNodeMeasurementForUDP is the same rule on the non-connected UDP
// arm, which round 1 put under the setup gate.
func TestLocalAdmissionRefusalKeepsTheNodeMeasurementForUDP(t *testing.T) {
	manager, storage := newHealthManager(t)

	manager.NewPacketConnection(context.Background(), admissionRejectingDialer{
		listenErr: resourceguard.ErrWaitTimeout,
	}, packetConnStub{}, adapter.InboundContext{
		Destination:   M.ParseSocksaddrHostPort("stun.example", 3478),
		OutboundChain: historyChain(),
	}, nil)

	require.NotNil(t, storage.LoadURLTestHistory("leaf-a"),
		"a local admission timeout deleted a healthy node's measurement")
}

// TestRealDialFailureStillClearsTheNodeMeasurement is the control: the rule above must not be
// implemented by never deleting history.
func TestRealDialFailureStillClearsTheNodeMeasurement(t *testing.T) {
	manager, storage := newHealthManager(t)
	inbound, inboundPeer := net.Pipe()
	t.Cleanup(func() { _ = inboundPeer.Close() })

	manager.NewConnection(context.Background(), admissionRejectingDialer{
		dialErr: errors.New("connection refused"),
	}, inbound, adapter.InboundContext{
		Destination:   M.ParseSocksaddrHostPort("example.com", 443),
		OutboundChain: historyChain(),
	}, nil)

	require.Nil(t, storage.LoadURLTestHistory("leaf-a"),
		"a real dial failure must still clear the measurement; the local-rejection rule must not "+
			"disable the health update")
}

// TestDialTimeoutStillClearsTheNodeMeasurement is the boundary of the rule and the regression the
// full route suite caught: a TIMEOUT is not admission backpressure.
//
// The first version of this fix reused resourceguard.IsLocalRejection, which also covers
// context.DeadlineExceeded and context.Canceled. That downgraded a timed-out dial - a genuine path
// failure this fork logs at ERROR and clears health for - and broke
// TestConnectionNXDomainLoggingIsDebug. Only the three gate outcomes may skip the health update.
func TestDialTimeoutStillClearsTheNodeMeasurement(t *testing.T) {
	manager, storage := newHealthManager(t)
	inbound, inboundPeer := net.Pipe()
	t.Cleanup(func() { _ = inboundPeer.Close() })

	manager.NewConnection(context.Background(), admissionRejectingDialer{
		dialErr: context.DeadlineExceeded,
	}, inbound, adapter.InboundContext{
		Destination:   M.ParseSocksaddrHostPort("example.com", 443),
		OutboundChain: historyChain(),
	}, nil)

	require.Nil(t, storage.LoadURLTestHistory("leaf-a"),
		"a dial timeout was treated as admission backpressure: the healthy member keeps a stale "+
			"measurement and the failure disappears from the health ledger")
}
