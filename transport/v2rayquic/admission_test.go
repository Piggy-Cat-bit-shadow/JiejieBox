//go:build with_quic

package v2rayquic

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The shared QUIC creation is an expensive creation and must be admitted like one.
//
// # The gap these tests were written against
//
// This transport deliberately mirrors the shared offer of the pinned sing-quic clients (one creation,
// many waiters, per-waiter cancellation). Those clients were given a CreationAdmission hook when the
// process admission policy was introduced; this in-repo twin was not. Every stream of a
// `transport.type=quic` VLESS/VMess/Trojan outbound therefore created a full QUIC connection - UDP
// socket, TLS handshake, transport state, its goroutine - with nothing consulting the process budget.
//
// It is the same class of creation the policy was built for: a burst of flows to one node produces
// one creation, but a burst across a restart, a detour chain, or several outbounds produces as many
// as the demand asks for, and on a NetworkExtension that is exactly the footprint the 32-slot gate
// exists to bound.
//
// The unit is the PHYSICAL connection, not the stream: a reuse must not be charged, or the gate would
// limit flows instead of creations and a healthy session would queue behind itself.

// unusedDialer stands in for the outbound dialer. It must never be reached: every test below replaces
// the QUIC constructor, so a call here means the creation ran without admission somewhere unexpected.
type unusedDialer struct{}

var errUnusedDialer = errors.New("the underlying dialer must not be reached by these tests")

func (unusedDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errUnusedDialer
}

func (unusedDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errUnusedDialer
}

// admittedTestClient builds the PRODUCTION client from a context carrying the guard, then replaces
// only the QUIC constructor. The wiring under test (guard in the construction context -> admission
// hook on the creation) is therefore the production one.
func admittedTestClient(t *testing.T, guard *resourceguard.Guard, dial func(context.Context) (*clientConnection, error)) *Client {
	t.Helper()
	ctx := resourceguard.NewContext(context.Background(), guard)
	logger := log.NewNOPFactory().NewLogger("v2rayquic-admission")
	tlsConfig, err := tls.NewClient(context.Background(), logger, "localhost", option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: "localhost",
		Insecure:   true,
	})
	require.NoError(t, err)
	transport, err := NewClient(ctx, unusedDialer{}, M.ParseSocksaddrHostPort("localhost", 443), option.V2RayQUICOptions{}, tlsConfig)
	require.NoError(t, err)
	client, isClient := transport.(*Client)
	require.True(t, isClient, "NewClient must build the package client, got %T", transport)
	client.dialConn = dial
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// countingDial counts creations and records the context each one ran under.
type countingDial struct {
	access  sync.Mutex
	calls   int
	dialCtx []context.Context
	conn    *clientConnection
	err     error
}

func (d *countingDial) dial(ctx context.Context) (*clientConnection, error) {
	d.access.Lock()
	defer d.access.Unlock()
	d.calls++
	d.dialCtx = append(d.dialCtx, ctx)
	return d.conn, d.err
}

func (d *countingDial) callCount() int {
	d.access.Lock()
	defer d.access.Unlock()
	return d.calls
}

func (d *countingDial) lastContext() context.Context {
	d.access.Lock()
	defer d.access.Unlock()
	if len(d.dialCtx) == 0 {
		return nil
	}
	return d.dialCtx[len(d.dialCtx)-1]
}

// saturatedGuard returns a guard whose only slot is already held, plus the release for it.
func saturatedGuard(t *testing.T) (*resourceguard.Guard, func()) {
	t.Helper()
	guard := resourceguard.NewGuard(resourceguard.Config{
		Limit: 1,
		Queue: 0,
		Wait:  100 * time.Millisecond,
	}, nil)
	held, err := guard.Acquire(context.Background(), resourceguard.ClassNormal)
	require.NoError(t, err)
	return guard, held.Release
}

// TestSaturatedAdmissionRefusesTheSharedCreation is the regression.
//
// RED before the fix: with the single slot held, the offer still ran the constructor, because nothing
// asked for permission. GREEN: the refusal reaches the waiter and no QUIC construction happens.
func TestSaturatedAdmissionRefusesTheSharedCreation(t *testing.T) {
	guard, release := saturatedGuard(t)
	defer release()

	dial := &countingDial{}
	client := admittedTestClient(t, guard, dial.dial)

	conn, err := client.offer(context.Background())
	require.Nil(t, conn)
	require.ErrorIs(t, err, resourceguard.ErrQueueFull,
		"a saturated process budget must refuse the shared QUIC creation, not start it anyway")
	require.Zero(t, dial.callCount(),
		"the QUIC constructor ran without admission: the expensive part is the creation, so it must be "+
			"inside the permit")
}

// TestEstablishedSessionIsReusedWithoutASecondPermit pins the unit of admission.
//
// A reuse is not a creation. Charging it would turn the creation budget into a flow budget, park
// healthy traffic behind a gate that has nothing to admit, and make the counters describe streams
// instead of the objects the policy exists to bound.
func TestEstablishedSessionIsReusedWithoutASecondPermit(t *testing.T) {
	guard := resourceguard.NewGuard(resourceguard.Config{Limit: 4, Queue: 4, Wait: time.Second}, nil)
	conn, _ := fakeConnection()
	dial := &countingDial{conn: conn}
	client := admittedTestClient(t, guard, dial.dial)

	created, err := client.offer(context.Background())
	require.NoError(t, err)
	require.Same(t, conn, created)

	reused, err := client.offer(context.Background())
	require.NoError(t, err)
	require.Same(t, conn, reused)

	require.Equal(t, 1, dial.callCount(), "the second demand reused the session but dialled again")
	stats := guard.Stats().Gate
	require.EqualValues(t, 1, stats.Admitted, "a reuse was charged as a creation")
	require.EqualValues(t, 1, stats.Released,
		"the permit must be released when the creation returns: it bounds CREATION concurrency, not the "+
			"lifetime of the session")
}

// TestTheSharedCreationSeesTheTriggeringFlowClass pins the class propagation.
//
// The demand that starts the creation carries the flow's resolved class. The shared dial must run with
// it, or an interactive flow that happens to open the session is charged as background work and the
// interactive reserve the policy keeps for it is unreachable.
func TestTheSharedCreationSeesTheTriggeringFlowClass(t *testing.T) {
	guard := resourceguard.NewGuard(resourceguard.Config{Limit: 4, Queue: 4, Wait: time.Second}, nil)
	conn, _ := fakeConnection()
	dial := &countingDial{conn: conn}
	client := admittedTestClient(t, guard, dial.dial)

	waiterCtx := trafficclass.WithClass(context.Background(), trafficclass.ClassInteractive)
	_, err := client.offer(waiterCtx)
	require.NoError(t, err)

	class, loaded := trafficclass.ClassFromContext(dial.lastContext())
	require.True(t, loaded, "the creation ran without the triggering flow's values")
	require.Equal(t, trafficclass.ClassInteractive, class)
}

// TestAnAlreadyAdmittedDialIsNotChargedTwice keeps the nested-seam invariant: a logical dial that
// already holds a permit must not take a second one for the connection it creates underneath.
//
// It passes on the pre-fix base too, because no admission existed at all; it is here so the fix
// cannot be written as an unconditional acquisition.
func TestAnAlreadyAdmittedDialIsNotChargedTwice(t *testing.T) {
	guard, release := saturatedGuard(t)
	defer release()

	dial := &countingDial{}
	conn, _ := fakeConnection()
	dial.conn = conn
	client := admittedTestClient(t, guard, dial.dial)

	heldCtx := resourceguard.WithHeldPermit(context.Background())
	created, err := client.offer(heldCtx)
	require.NoError(t, err, "a dial that already holds a permit must not block on the gate it already holds")
	require.Same(t, conn, created)
	require.Equal(t, 1, dial.callCount())
}
