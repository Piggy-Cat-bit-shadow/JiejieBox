//go:build with_quic

package httpclient

import (
	"context"
	stdTLS "crypto/tls"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The shared HTTP/3 client must be admitted before it dials.
//
// # The gap these tests were written against
//
// HTTP/1.1 and HTTP/2 connections from this package are charged through common/tls, which performs
// their handshake. HTTP/3 performs none of that: the Dial callback below opens a UDP socket and
// calls quic.DialEarlyConn directly. It was therefore the one physical transport this package could
// create without consulting the process budget at all, which is exactly the class of creation the
// 32-slot gate exists to bound.
//
// The test drives the Dial callback directly rather than through an http3 round trip: the property
// is "a refused creation never enters the UDP dial or the handshake", and reaching that through a
// full request would test the harness more than the seam.

type countingUDPDialer struct {
	calls int
	err   error
}

func (d *countingUDPDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	return nil, errors.New("test dialer has no socket")
}

func (d *countingUDPDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not a packet dialer")
}

func TestH3CreationIsRefusedBeforeTheDial(t *testing.T) {
	guard := resourceguard.NewGuard(resourceguard.Config{Limit: 1, Queue: 0, Wait: 100 * time.Millisecond}, nil)
	held, err := guard.Acquire(context.Background(), resourceguard.ClassNormal)
	require.NoError(t, err)
	defer held.Release()

	guardCtx := resourceguard.NewContext(context.Background(), guard)
	admission := h3AdmissionFromContext(guardCtx)
	require.NotNil(t, admission)

	dialer := &countingUDPDialer{}
	dial := newHTTP3DialFunc(dialer, nil, 0, admission)

	_, err = dial(context.Background(), "127.0.0.1:443", &stdTLS.Config{}, &quic.Config{})
	require.ErrorIs(t, err, resourceguard.ErrQueueFull,
		"an H3 creation ran while the process budget was saturated")
	require.Zero(t, dialer.calls, "the UDP socket was opened before admission")

	held.Release()
	sentinel := errors.New("refused by the test socket")
	dialer.err = sentinel
	_, err = dial(context.Background(), "127.0.0.1:443", &stdTLS.Config{}, &quic.Config{})
	require.ErrorIs(t, err, sentinel, "after admission the real creation path must run")
	require.Equal(t, 1, dialer.calls)

	// The counters include the permit this test holds to saturate the gate: requested 3 =
	// held-acquire + refused creation + admitted creation.
	stats := guard.Stats().Gate
	require.EqualValues(t, 3, stats.Requested)
	require.EqualValues(t, 1, stats.Refused)
	require.EqualValues(t, 2, stats.Admitted)
	require.EqualValues(t, 2, stats.Released)
	require.Zero(t, stats.Inflight)
}

// TestNilGuardKeepsTheOldPath pins the platform rule: a process without a budget keeps exactly the
// previous behaviour, one nil check.
func TestNilGuardKeepsTheOldPath(t *testing.T) {
	require.Nil(t, h3AdmissionFromContext(context.Background()))

	dialer := &countingUDPDialer{err: errors.New("no socket")}
	dial := newHTTP3DialFunc(dialer, nil, 0, nil)
	_, err := dial(context.Background(), "127.0.0.1:443", &stdTLS.Config{}, &quic.Config{})
	require.Error(t, err)
	require.Equal(t, 1, dialer.calls)
}
