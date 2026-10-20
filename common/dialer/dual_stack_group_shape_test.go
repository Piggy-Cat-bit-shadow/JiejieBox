package dialer

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

// DS-02, as a measurement rather than an assumption.
//
// # The production shape under test
//
// When a flow's destination was resolved (the `resolve` action), the resolved addresses reach
// `DialSerialNetwork` and are handed to whatever outbound the route chose. `direct` implements
// ParallelNetworkDialer, so those addresses race. A group (`selector`, `urltest`, `loadbalance`)
// does not implement it, so the same list falls through to the serial loop: address one, wait for
// its dial to finish or time out, then address two.
//
// The two doubles below stand in for exactly those shapes. The "black hole" family never answers,
// so the only question is whether the healthy family is attempted concurrently or after the dead
// one has burned its timeout.

type measuredDialer struct {
	blackhole netip.Addr
	healthy   netip.Addr

	access   sync.Mutex
	attempts []netip.Addr
	starts   []time.Duration
	ends     []time.Duration
	start    time.Time
}

func (d *measuredDialer) record(address netip.Addr) {
	d.access.Lock()
	d.attempts = append(d.attempts, address)
	d.starts = append(d.starts, time.Since(d.start))
	d.access.Unlock()
}

func (d *measuredDialer) finish() {
	d.access.Lock()
	d.ends = append(d.ends, time.Since(d.start))
	d.access.Unlock()
}

func (d *measuredDialer) dialOne(ctx context.Context, address netip.Addr) (net.Conn, error) {
	d.record(address)
	defer d.finish()
	if address == d.blackhole {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	left, right := net.Pipe()
	_ = right.Close()
	return left, nil
}

func (d *measuredDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return d.dialOne(ctx, destination.Addr)
}

func (d *measuredDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("packet is not part of this measurement")
}

func (d *measuredDialer) summary() (attempts []netip.Addr, starts, ends []time.Duration) {
	d.access.Lock()
	defer d.access.Unlock()
	round := func(values []time.Duration) []time.Duration {
		rounded := make([]time.Duration, len(values))
		for index, value := range values {
			rounded[index] = value.Round(time.Millisecond)
		}
		return rounded
	}
	return append([]netip.Addr(nil), d.attempts...), round(d.starts), round(d.ends)
}

// parallelMeasuredDialer is the direct-shaped double: it answers the richer surface, so
// DialSerialNetwork races the addresses instead of walking them.
type parallelMeasuredDialer struct {
	*measuredDialer
}

func (d *parallelMeasuredDialer) DialParallelNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	results := make(chan result, len(destinationAddresses))
	for _, address := range destinationAddresses {
		go func(target netip.Addr) {
			conn, err := d.dialOne(ctx, target)
			results <- result{conn, err}
		}(address)
	}
	var lastErr error
	for range destinationAddresses {
		outcome := <-results
		if outcome.err == nil {
			return outcome.conn, nil
		}
		lastErr = outcome.err
	}
	return nil, lastErr
}

func (d *parallelMeasuredDialer) ListenSerialNetworkPacket(context.Context, M.Socksaddr, []netip.Addr, *C.NetworkStrategy, []C.InterfaceType, []C.InterfaceType, time.Duration) (net.PacketConn, netip.Addr, error) {
	return nil, netip.Addr{}, errors.New("packet is not part of this measurement")
}

// TestGroupShapeDialsAddressesSerially is the measurement DS-02 was missing.
func TestGroupShapeDialsAddressesSerially(t *testing.T) {
	blackhole := netip.MustParseAddr("2001:db8::1")
	healthy := netip.MustParseAddr("192.0.2.1")
	addresses := []netip.Addr{blackhole, healthy}

	run := func(t *testing.T, useParallel bool) *measuredDialer {
		t.Helper()
		base := &measuredDialer{blackhole: blackhole, healthy: healthy, start: time.Now()}
		var dialer N.Dialer = base
		if useParallel {
			dialer = &parallelMeasuredDialer{measuredDialer: base}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		start := time.Now()
		conn, err := DialSerialNetwork(ctx, dialer, "tcp", M.SocksaddrFrom(blackhole, 443), addresses, nil, nil, nil, 50*time.Millisecond)
		elapsed := time.Since(start)
		require.NoError(t, err)
		require.NotNil(t, conn)
		_ = conn.Close()
		attempts, starts, ends := base.summary()
		t.Logf("parallel=%v attempts=%v start(s)=%v finish(s)=%v elapsed=%v",
			useParallel, attempts, starts, ends, elapsed.Round(time.Millisecond))
		return base
	}

	t.Run("direct shape races", func(t *testing.T) {
		direct := run(t, true)
		_, starts, _ := direct.summary()
		require.Len(t, starts, 2)
		require.Less(t, starts[1], 50*time.Millisecond,
			"the healthy family is attempted immediately, not after the dead one times out")
	})
	t.Run("group shape serialises", func(t *testing.T) {
		group := run(t, false)
		_, starts, ends := group.summary()
		require.Len(t, starts, 2, "a serial dialer walks the candidates")
		require.GreaterOrEqual(t, starts[1]+10*time.Millisecond, ends[0],
			"the second attempt only starts after the first one gives up, which is the cost this measurement is about")
	})
}
