package anytls

// The Q10 contract, end to end: creation is admitted, reuse is not.
//
// The admission budget bounds session CREATION - connect, TLS handshake, first write. The client
// first tries to reuse an idle session and only creates when that fails, so the permit has to be
// asked for at the creation seam. These tests observe both paths with the only permit held:
//
//   - a reuse must return while the gate is saturated (it borrows an established session);
//   - a creation must wait for the gate, then proceed when the permit is released;
//   - a nested dial that already holds a permit must not ask again;
//   - a failed creation must return its permit.
//
// The pure-seam tests use a plain-TCP AnyTLS service and a client whose DialOut does no TLS, so the
// timing they measure can only come from this seam; the TLS handshake has its own admission gate and
// would otherwise mask whether the session seam waits.

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	singanytls "github.com/sagernet/sing-anytls"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func newAdmissionGuard() *resourceguard.Guard {
	return resourceguard.NewGuard(resourceguard.Config{
		Limit:              1,
		Queue:              1,
		Wait:               5 * time.Second,
		InteractiveReserve: 0,
		DemoteAfter:        time.Second,
		StarvationAfter:    time.Second,
	}, func() resourceguard.Level { return resourceguard.LevelNormal })
}

func newAdmittedOutbound(t *testing.T, port uint16, guard *resourceguard.Guard) (*Outbound, context.Context) {
	t.Helper()
	ctx := context.Background()
	if guard != nil {
		ctx = resourceguard.NewContext(ctx, guard)
	}
	created, err := NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("admission-anytls"), "anytls-admission",
		option.AnyTLSOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
			Password:      carrierPassword,
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{
					Enabled:    true,
					ServerName: "localhost",
					Insecure:   true,
				},
			},
		})
	require.NoError(t, err)
	outbound, isOutbound := created.(*Outbound)
	require.True(t, isOutbound)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().NewLogger("admission-anytls-scope"))
	require.NoError(t, outbound.Start(adapter.StartStateInitialize, scope))
	t.Cleanup(func() { _ = scope.Close() })
	return outbound, ctx
}

func admissionDestination() M.Socksaddr {
	return M.Socksaddr{Fqdn: "example.com", Port: 80}
}

// idleForReuse closes the only stream of a session and gives the client a moment to put the session
// on its idle list. The release is synchronous in stream.Close; the wait keeps the test from
// racing a goroutine that is only scheduled later.
func idleForReuse(t *testing.T, conn net.Conn) {
	t.Helper()
	require.NoError(t, conn.Close())
	time.Sleep(20 * time.Millisecond)
}

// startPlainAnyTLSServer serves the pinned sing-anytls service over plain TCP. It exists so the
// creation seam can be measured without the TLS handshake's own admission gate.
func startPlainAnyTLSServer(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	service, err := singanytls.NewService(carrierPassword, singanytls.ServiceOptions{Handler: discardHandler{}})
	require.NoError(t, err)
	accepted := new(atomic.Int32)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			accepted.Add(1)
			go func() {
				_ = service.NewConnection(context.Background(), conn, M.SocksaddrFromNet(conn.RemoteAddr()), func(error) {
					_ = conn.Close()
				})
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return listener.Addr().String(), accepted
}

func newPlainAdmissionClient(t *testing.T, address string, guard *resourceguard.Guard) *singanytls.Client {
	t.Helper()
	client, err := singanytls.NewClient(singanytls.ClientOptions{
		Password: carrierPassword,
		DialOut: func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", address)
		},
		AdmitCreation: admitSessionCreation(guard, address),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestReuseDoesNotWaitForACreationPermit is the audit's case on the production outbound: a healthy
// reuse must not be parked behind a saturated creation budget.
func TestReuseDoesNotWaitForACreationPermit(t *testing.T) {
	guard := newAdmissionGuard()
	port := startCarrierServer(t, &carrierHandler{target: startCarrierEcho(t)})
	outbound, ctx := newAdmittedOutbound(t, port, guard)

	first, err := outbound.DialContext(ctx, N.NetworkTCP, admissionDestination())
	require.NoError(t, err)
	idleForReuse(t, first)

	// Saturation: the only permit is held by something unrelated to the reuse below.
	held, err := guard.Acquire(ctx, resourceguard.ClassNormal)
	require.NoError(t, err)
	started := time.Now()
	second, err := outbound.DialContext(ctx, N.NetworkTCP, admissionDestination())
	elapsed := time.Since(started)
	require.NoError(t, err)
	require.Less(t, elapsed, time.Second,
		"a reuse waited %v for a creation permit it does not need", elapsed)
	require.Equal(t, 1, guard.Stats().Gate.Inflight,
		"the reuse must not have acquired a second permit")
	held.Release()
	require.NoError(t, second.Close())
}

// TestCreationAdmissionSeamWaitsForAPermit is the other half, isolated to the session seam: a
// creation must wait while the gate is saturated and proceed when the permit is released.
func TestCreationAdmissionSeamWaitsForAPermit(t *testing.T) {
	guard := newAdmissionGuard()
	address, _ := startPlainAnyTLSServer(t)
	client := newPlainAdmissionClient(t, address, guard)
	ctx := resourceguard.NewContext(context.Background(), guard)

	held, err := guard.Acquire(ctx, resourceguard.ClassNormal)
	require.NoError(t, err)
	started := time.Now()
	returned := make(chan error, 1)
	go func() {
		conn, dialErr := client.DialContext(ctx, admissionDestination())
		if conn != nil {
			_ = conn.Close()
		}
		returned <- dialErr
	}()
	select {
	case dialErr := <-returned:
		t.Fatalf("the creation seam returned after %v with the gate saturated (err=%v)", time.Since(started), dialErr)
	case <-time.After(300 * time.Millisecond):
		// Expected: the creation is waiting for the permit.
	}
	held.Release()
	select {
	case dialErr := <-returned:
		require.NoError(t, dialErr)
	case <-time.After(5 * time.Second):
		t.Fatal("the creation never returned after the permit was released")
	}
	require.Zero(t, guard.Stats().Gate.Inflight)
}

// TestCreationAdmissionSeamDoesNotAskOnReuse pins the same split at the seam: a reuse returns while
// the gate is saturated and does not open a second session.
func TestCreationAdmissionSeamDoesNotAskOnReuse(t *testing.T) {
	guard := newAdmissionGuard()
	address, accepted := startPlainAnyTLSServer(t)
	client := newPlainAdmissionClient(t, address, guard)
	ctx := resourceguard.NewContext(context.Background(), guard)

	first, err := client.DialContext(ctx, admissionDestination())
	require.NoError(t, err)
	require.Eventually(t, func() bool { return accepted.Load() == 1 }, 2*time.Second, time.Millisecond)
	idleForReuse(t, first)

	held, err := guard.Acquire(ctx, resourceguard.ClassNormal)
	require.NoError(t, err)
	started := time.Now()
	second, err := client.DialContext(ctx, admissionDestination())
	elapsed := time.Since(started)
	require.NoError(t, err)
	require.Less(t, elapsed, time.Second,
		"a reuse waited %v for a creation permit it does not need", elapsed)
	require.Equal(t, 1, guard.Stats().Gate.Inflight,
		"the reuse must not have acquired a second permit")
	require.Equal(t, int32(1), accepted.Load(), "a reuse must not create a second session")
	held.Release()
	require.NoError(t, second.Close())
}

// TestNestedCreationDoesNotAskTheGateAgain pins the detour/chain rule on the new seam: a creation
// that already runs under a held permit must not queue behind its own cohort.
func TestNestedCreationDoesNotAskTheGateAgain(t *testing.T) {
	guard := newAdmissionGuard()
	ctx := context.Background()
	held, err := guard.Acquire(ctx, resourceguard.ClassNormal)
	require.NoError(t, err)
	before := guard.Stats().Gate.Requested

	hook := admitSessionCreation(guard, "nested")
	require.NotNil(t, hook)
	ran := false
	require.NoError(t, hook(resourceguard.WithHeldPermit(ctx), func(context.Context) error {
		ran = true
		return nil
	}))
	require.True(t, ran)
	require.Equal(t, before, guard.Stats().Gate.Requested,
		"a nested creation asked the gate again instead of reusing the held permit")
	require.Equal(t, 1, guard.Stats().Gate.Inflight)
	held.Release()
}

// TestFailedCreationReleasesThePermit keeps the budget honest across the new seam.
func TestFailedCreationReleasesThePermit(t *testing.T) {
	guard := newAdmissionGuard()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	require.NoError(t, listener.Close())
	outbound, ctx := newAdmittedOutbound(t, port, guard)

	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err = outbound.DialContext(dialCtx, N.NetworkTCP, admissionDestination())
	require.Error(t, err)
	require.Zero(t, guard.Stats().Gate.Inflight, "a failed creation leaked its permit")
}

// TestCreationAdmissionIsWiredOnlyWithAGuard pins the two wiring states: with a process budget the
// client gets the hook, without one it keeps the SDK's plain path.
func TestCreationAdmissionIsWiredOnlyWithAGuard(t *testing.T) {
	port := startCarrierServer(t, &carrierHandler{target: startCarrierEcho(t)})
	withGuard, _ := newAdmittedOutbound(t, port, newAdmissionGuard())
	require.NotNil(t, withGuard.clientOptions.AdmitCreation)

	withoutGuard, _ := newAdmittedOutbound(t, port, nil)
	require.Nil(t, withoutGuard.clientOptions.AdmitCreation)
}
