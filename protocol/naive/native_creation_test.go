//go:build with_naive_outbound

package naive

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

// fakeSocketDialer stands in for the outbound's real dialer chain at the native creation seam: it
// records every creation, can be made to fail, block or return after its context ended.
type fakeSocketDialer struct {
	access  sync.Mutex
	creates int

	// err, when set, is returned by every DialContext.
	err error
	// block, when non-nil, is waited on before a DialContext returns.
	block chan struct{}
	// ignoreContext makes a dial succeed even after its context was cancelled, which is how a
	// late creation is produced deterministically.
	ignoreContext bool
}

func (d *fakeSocketDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.access.Lock()
	d.creates++
	block := d.block
	err := d.err
	ignoreContext := d.ignoreContext
	d.access.Unlock()
	if block != nil {
		<-block
	}
	if !ignoreContext && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func (d *fakeSocketDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	conn, err := d.DialContext(ctx, N.NetworkUDP, destination)
	if err != nil {
		return nil, err
	}
	_ = conn.Close()
	return &fakePacketConn{}, nil
}

func (d *fakeSocketDialer) creations() int {
	d.access.Lock()
	defer d.access.Unlock()
	return d.creates
}

func (d *fakeSocketDialer) setError(err error) {
	d.access.Lock()
	defer d.access.Unlock()
	d.err = err
}

type fakePacketConn struct{ net.PacketConn }

func (c *fakePacketConn) ReadFrom(payload []byte) (int, net.Addr, error) {
	return 0, nil, errors.New("unused")
}

func (c *fakePacketConn) WriteTo(payload []byte, destination net.Addr) (int, error) {
	return len(payload), nil
}

func (c *fakePacketConn) Close() error { return nil }

func testGuard(t *testing.T, config resourceguard.Config) *resourceguard.Guard {
	t.Helper()
	return resourceguard.NewGuard(config, nil)
}

func creationConfig(limit, queue int, wait time.Duration) resourceguard.Config {
	return resourceguard.Config{
		Limit:           limit,
		Queue:           queue,
		Wait:            wait,
		DemoteAfter:     time.Millisecond,
		StarvationAfter: time.Millisecond,
	}
}

// TestNativeCreationCountsOnePerPhysicalConnection is the Q14 core assertion: one dialer entry is
// one creation, and no other layer's counter is consulted.
func TestNativeCreationCountsOnePerPhysicalConnection(t *testing.T) {
	parent := &fakeSocketDialer{}
	dialer := newNativeCreationDialer(parent, nil, "proxy.example:443")
	dialer.admissionWait = 10 * time.Millisecond

	for index := 0; index < 3; index++ {
		conn, err := dialer.DialContext(context.Background(), N.NetworkTCP, M.Socksaddr{})
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	}

	stats := dialer.ledger.stats()
	require.Equal(t, uint64(3), stats.Attempts)
	require.Equal(t, uint64(3), stats.Created)
	require.Equal(t, int64(0), stats.InFlight)
	require.Equal(t, int64(1), stats.MaxInFlight, "sequential creations never overlap")
	require.Equal(t, stats.Attempts, stats.Accounted(), "every attempt has exactly one outcome")
	require.Equal(t, 3, parent.creations(), "the wrapper must not add or drop a dial")
}

// TestNativeCreationFailureAndAdmissionAreDifferentFacts pins the accounting contract: a failed
// dial, a refused creation and a cancelled caller are three different outcomes, and only the first
// one is a statement about the proxy.
func TestNativeCreationFailureAndAdmissionAreDifferentFacts(t *testing.T) {
	parent := &fakeSocketDialer{}
	dialer := newNativeCreationDialer(parent, nil, "proxy.example:443")
	dialer.admissionWait = 10 * time.Millisecond

	parent.setError(errors.New("connect: connection refused"))
	_, err := dialer.DialContext(context.Background(), N.NetworkTCP, M.Socksaddr{})
	require.Error(t, err)

	stats := dialer.ledger.stats()
	require.Equal(t, uint64(1), stats.Failed)
	require.Zero(t, stats.Refused)
	require.Zero(t, stats.Created)
	require.Equal(t, stats.Attempts, stats.Accounted())
}

// TestAdmissionBoundsConcurrentNativeCreations drives the real guard: with a limit of one, a
// second creation waits and the high-water mark stays at one.
func TestAdmissionBoundsConcurrentNativeCreations(t *testing.T) {
	parent := &fakeSocketDialer{block: make(chan struct{})}
	guard := testGuard(t, creationConfig(1, 1, 5*time.Second))
	dialer := newNativeCreationDialer(parent, guard, "proxy.example:443")
	dialer.admissionWait = 5 * time.Second

	firstDone := make(chan error, 1)
	go func() {
		conn, err := dialer.DialContext(context.Background(), N.NetworkTCP, M.Socksaddr{})
		if conn != nil {
			_ = conn.Close()
		}
		firstDone <- err
	}()
	require.Eventually(t, func() bool { return parent.creations() == 1 }, time.Second, time.Millisecond,
		"the first creation must reach the parent dialer")
	require.Eventually(t, func() bool { return dialer.ledger.stats().InFlight == 1 }, time.Second, time.Millisecond)

	secondDone := make(chan error, 1)
	go func() {
		conn, err := dialer.DialContext(context.Background(), N.NetworkTCP, M.Socksaddr{})
		if conn != nil {
			_ = conn.Close()
		}
		secondDone <- err
	}()
	// The second creation is queued at the gate: the parent dialer must not see it yet, and it
	// must not be counted as an object being created while it holds no object.
	require.Eventually(t, func() bool {
		return guard.Stats().Gate.Waiters == 1
	}, time.Second, time.Millisecond, "the second attempt must be queued at the admission gate")
	require.Equal(t, 1, parent.creations(), "a queued creation must not reach the native dialer")
	require.Equal(t, int64(1), dialer.ledger.stats().InFlight, "a queued attempt holds no object yet")

	close(parent.block)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)

	stats := dialer.ledger.stats()
	require.Equal(t, uint64(2), stats.Created)
	require.Equal(t, int64(1), stats.MaxInFlight, "the gate bounds concurrency at its limit")
	require.Equal(t, 2, parent.creations())
}

// TestAdmissionRefusalIsCountedAndShortCircuits pins the refusal branch: a full queue refuses
// without reaching the dialer, and the outcome is booked as an admission decision.
func TestAdmissionRefusalIsCountedAndShortCircuits(t *testing.T) {
	parent := &fakeSocketDialer{block: make(chan struct{})}
	guard := testGuard(t, creationConfig(1, 0, 50*time.Millisecond))
	dialer := newNativeCreationDialer(parent, guard, "proxy.example:443")
	dialer.admissionWait = 20 * time.Millisecond

	holderDone := make(chan struct{})
	go func() {
		defer close(holderDone)
		conn, _ := dialer.DialContext(context.Background(), N.NetworkTCP, M.Socksaddr{})
		if conn != nil {
			_ = conn.Close()
		}
	}()
	require.Eventually(t, func() bool { return parent.creations() == 1 }, time.Second, time.Millisecond)

	conn, err := dialer.DialContext(context.Background(), N.NetworkTCP, M.Socksaddr{})
	require.Nil(t, conn)
	require.Error(t, err)
	require.True(t, resourceguard.IsLocalRejection(err), "a refusal must stay identifiable as a local rejection: %v", err)

	stats := dialer.ledger.stats()
	require.Equal(t, uint64(1), stats.Refused)
	require.Zero(t, stats.Failed, "a refusal is not a dial failure")
	require.Equal(t, uint64(2), stats.Attempts, "the holder and the refused attempt both reached the seam")
	require.Equal(t, 1, parent.creations(), "the refused creation never reached the native dialer")

	close(parent.block)
	<-holderDone
	final := dialer.ledger.stats()
	require.Equal(t, uint64(1), final.Created, "the holder's dial succeeded after the block was released")
	require.Zero(t, final.InFlight)
	require.Equal(t, final.Attempts, final.Accounted(), "the refused attempt is accounted for exactly once")
}

// TestCancelledCallerIsCountedAsCancellation pins that the caller's own context is reported as a
// cancellation even though the gate exposes the same context error as a local rejection.
func TestCancelledCallerIsCountedAsCancellation(t *testing.T) {
	parent := &fakeSocketDialer{}
	guard := testGuard(t, creationConfig(1, 4, time.Second))
	dialer := newNativeCreationDialer(parent, guard, "proxy.example:443")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := dialer.DialContext(ctx, N.NetworkTCP, M.Socksaddr{})
	require.ErrorIs(t, err, context.Canceled)

	stats := dialer.ledger.stats()
	require.Equal(t, uint64(1), stats.Canceled)
	require.Zero(t, stats.Failed)
	require.Equal(t, 0, parent.creations())
}

// TestLateCreationIsRecorded pins the shutdown race this seam can actually see: a socket that
// arrives after the client lifetime ended is counted as late instead of silently becoming a
// creation like any other.
func TestLateCreationIsRecorded(t *testing.T) {
	parent := &fakeSocketDialer{ignoreContext: true}
	dialer := newNativeCreationDialer(parent, nil, "proxy.example:443")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn, err := dialer.DialContext(ctx, N.NetworkTCP, M.Socksaddr{})
	require.NoError(t, err, "the fake ignores the cancelled context on purpose")
	require.NoError(t, conn.Close())

	stats := dialer.ledger.stats()
	require.Equal(t, uint64(1), stats.Created)
	require.Equal(t, uint64(1), stats.LateCreated)
}

// TestHeldPermitIsNotAdmittedTwice pins the re-entrancy rule: a creation inside a creation holds
// one permit, not two.
func TestHeldPermitIsNotAdmittedTwice(t *testing.T) {
	parent := &fakeSocketDialer{}
	guard := testGuard(t, creationConfig(4, 4, time.Second))
	dialer := newNativeCreationDialer(parent, guard, "proxy.example:443")

	ctx := resourceguard.WithHeldPermit(context.Background())
	conn, err := dialer.DialContext(ctx, N.NetworkTCP, M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	require.Equal(t, uint64(0), guard.Stats().Gate.Requested, "a held permit must not ask the gate again")
	require.Equal(t, uint64(1), dialer.ledger.stats().Created)
}

// TestResidencyLedgerNamesTheBoundary pins the honest report: creation is real, lifetime is not,
// and the record says so instead of returning a precise zero.
func TestResidencyLedgerNamesTheBoundary(t *testing.T) {
	parent := &fakeSocketDialer{block: make(chan struct{})}
	dialer := newNativeCreationDialer(parent, nil, "proxy.example:443")
	outbound := &Outbound{nativeCreation: dialer}

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, _ := dialer.DialContext(context.Background(), N.NetworkTCP, M.Socksaddr{})
		if conn != nil {
			_ = conn.Close()
		}
	}()
	require.Eventually(t, func() bool { return dialer.ledger.stats().InFlight == 1 }, time.Second, time.Millisecond)

	ledger := outbound.ResidencyLedger()
	require.True(t, ledger.Answered)
	require.False(t, ledger.Exact, "lifetime counts are not observable, so the record must not claim exactness")
	require.Equal(t, uint64(1), ledger.Creating)
	require.Zero(t, ledger.Active, "no state beyond creation is observed")
	require.Zero(t, ledger.Terminated, "no close callback exists at this seam")
	require.NotEmpty(t, ledger.Note)
	require.Contains(t, ledger.Note, "close")

	close(parent.block)
	<-done
	require.Zero(t, outbound.ResidencyLedger().Creating)
}

// TestOutboundWithoutNativeCreationAnswersUnknown pins the not-implemented branch instead of a
// zero ledger.
func TestOutboundWithoutNativeCreationAnswersUnknown(t *testing.T) {
	outbound := &Outbound{}
	ledger := outbound.ResidencyLedger()
	require.False(t, ledger.Answered)
	require.NotEmpty(t, ledger.Note)
}

// TestConcurrentCreationsConserveAttempts runs creations from many goroutines and requires the
// counters to stay balanced: no attempt is lost, and no attempt is counted twice.
func TestConcurrentCreationsConserveAttempts(t *testing.T) {
	parent := &fakeSocketDialer{}
	guard := testGuard(t, creationConfig(8, 32, time.Second))
	dialer := newNativeCreationDialer(parent, guard, "proxy.example:443")

	const workers = 16
	var waitGroup sync.WaitGroup
	var failures atomic.Int64
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			// Half the creations fail, so both outcomes are exercised under concurrency.
			if index%2 == 0 {
				parent.setError(errors.New("simulated dial failure"))
			} else {
				parent.setError(nil)
			}
			conn, err := dialer.DialContext(context.Background(), N.NetworkTCP, M.Socksaddr{})
			if err != nil {
				failures.Add(1)
				return
			}
			_ = conn.Close()
		}(index)
	}
	waitGroup.Wait()

	stats := dialer.ledger.stats()
	require.Equal(t, uint64(workers), stats.Attempts)
	require.Equal(t, stats.Attempts, stats.Accounted(), "attempts must equal the sum of outcomes")
	require.Equal(t, int64(0), stats.InFlight)
	require.Equal(t, int64(failures.Load()), int64(stats.Failed))
	require.LessOrEqual(t, stats.MaxInFlight, int64(8), "the gate limit bounds the high-water mark")
}

// TestPacketSocketCreationIsOneCreation pins that the UDP half of the seam is counted too, so a
// QUIC socket cannot be created outside the budget.
func TestPacketSocketCreationIsOneCreation(t *testing.T) {
	parent := &fakeSocketDialer{}
	dialer := newNativeCreationDialer(parent, nil, "proxy.example:443")

	packetConn, err := dialer.ListenPacket(context.Background(), M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, packetConn.Close())

	stats := dialer.ledger.stats()
	require.Equal(t, uint64(1), stats.Attempts)
	require.Equal(t, uint64(1), stats.Created)
	require.Equal(t, stats.Attempts, stats.Accounted())
}

var _ adapter.ResidencyLedgerReporter = (*Outbound)(nil)
var _ N.Dialer = (*nativeCreationDialer)(nil)
