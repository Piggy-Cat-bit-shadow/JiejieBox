package mieru

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// gatedDialer blocks inside the real underlay dial, so the test can prove the admission bound is
// applied AT the seam the SDK's newUnderlay reaches, not around a wrapper above it.
type gatedDialer struct {
	entered     chan struct{}
	release     chan struct{}
	inflight    atomic.Int32
	maxInflight atomic.Int32
	marked      atomic.Bool
}

func newGatedDialer() *gatedDialer {
	return &gatedDialer{entered: make(chan struct{}, 8), release: make(chan struct{})}
}

func (d *gatedDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	current := d.inflight.Add(1)
	defer d.inflight.Add(-1)
	for {
		high := d.maxInflight.Load()
		if current <= high || d.maxInflight.CompareAndSwap(high, current) {
			break
		}
	}
	d.marked.Store(resourceguard.HasHeldPermit(ctx))
	d.entered <- struct{}{}
	<-d.release
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func (d *gatedDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("unexpected ListenPacket")
}

// TestServerDialerAdmitsUnderlayCreation proves two things about the Mieru seam:
//
//   - the SDK's newUnderlay passes through one admission permit, so a second creation waits instead
//     of opening a second socket; and
//   - the context handed to the real dialer carries the held-permit marker, so a nested TLS-style
//     seam in the same logical dial cannot charge a second handshake.
func TestServerDialerAdmitsUnderlayCreation(t *testing.T) {
	guard := resourceguard.NewGuard(resourceguard.Config{
		Limit:           1,
		Queue:           4,
		Wait:            2 * time.Second,
		DemoteAfter:     time.Second,
		StarvationAfter: time.Second,
	}, func() resourceguard.Level { return resourceguard.LevelNormal })
	ctx := resourceguard.NewContext(context.Background(), guard)

	raw := newGatedDialer()
	serverDialer, err := newServerDialer(raw, "example.com", guard)
	require.NoError(t, err)

	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func() {
			conn, dialErr := serverDialer.DialContext(ctx, N.NetworkTCP, "example.com:443")
			if dialErr == nil {
				_ = conn.Close()
			}
			results <- dialErr
		}()
	}

	select {
	case <-raw.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the first underlay dial never reached the socket")
	}
	select {
	case <-raw.entered:
		t.Fatal("a second underlay dial reached the socket while the first held the only permit")
	case <-time.After(100 * time.Millisecond):
		// Correct: it is waiting for the permit.
	}

	close(raw.release)
	for index := 0; index < 2; index++ {
		require.NoError(t, <-results)
	}
	require.Equal(t, int32(1), raw.maxInflight.Load(), "the admission limit must hold at the socket")
	require.True(t, raw.marked.Load(), "the real dialer must see the held-permit marker")
	stats := guard.Stats().Gate
	require.Equal(t, uint64(2), stats.Admitted)
	require.Equal(t, uint64(2), stats.Released)
}

// TestAdmissionRejectionIsLocalForTheFallbackClassifier is the G02 classification contract for the
// Mieru path: when the SDK has flattened the underlay error into text, the typed admission refusal
// recorded by the dialer must still classify as LOCAL, so it cannot switch transports or blame the
// node for this process's own backpressure.
func TestAdmissionRejectionIsLocalForTheFallbackClassifier(t *testing.T) {
	ctx, observation := withDialObservation(context.Background())
	observeSocketDial(ctx, resourceguard.ErrQueueFull)

	flattened := errors.New("mieru: NewTCPUnderlay() failed: resource guard: admission queue is full")
	require.Equal(t, dialLocalFailure, classifyDialError(ctx, flattened, mieruTransport{}, observation))

	observeSocketDial(ctx, resourceguard.ErrRateLimited)
	flattened = errors.New("mieru: NewTCPUnderlay() failed: creation rate limited after fast failures")
	require.Equal(t, dialLocalFailure, classifyDialError(ctx, flattened, mieruTransport{}, observation))

	// A plain far-side failure recorded in the same ledger still classifies by its socket class, not
	// as local: the admission rule must not swallow the real evidence.
	observeSocketDial(ctx, errors.New("connect: connection refused"))
	class := classifyDialError(ctx, errors.New("mieru: NewTCPUnderlay() failed"), mieruTransport{}, observation)
	require.NotEqual(t, dialLocalFailure, class)
}
