//go:build with_xhttp && with_quic

package v2rayxhttp

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

// The XHTTP HTTP/3 path must be admitted before it dials.
//
// HTTP/1.1 and HTTP/2 XHTTP connections are charged through common/tls. HTTP/3 bypasses that dialer:
// it opens a UDP socket and calls quic.DialEarlyConn directly. The two siblings therefore had
// different creation budgets - the H2 route capped at 32 concurrent creations, the H3 route unbounded
// - which is the asymmetry this test closes.

type xhttpCountingDialer struct {
	calls int
	err   error
}

func (d *xhttpCountingDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	return nil, errors.New("test dialer has no socket")
}

func (d *xhttpCountingDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not a packet dialer")
}

func TestH3CreationIsRefusedBeforeTheUDPDial(t *testing.T) {
	guard := resourceguard.NewGuard(resourceguard.Config{Limit: 1, Queue: 0, Wait: 100 * time.Millisecond}, nil)
	held, err := guard.Acquire(context.Background(), resourceguard.ClassNormal)
	require.NoError(t, err)
	defer held.Release()

	guardCtx := resourceguard.NewContext(context.Background(), guard)
	admission := h3AdmissionFromContext(guardCtx)
	require.NotNil(t, admission)

	dialer := &xhttpCountingDialer{}
	owner := &http3XmuxConn{}
	dial := newHTTP3DialFunc(dialer, M.ParseSocksaddrHostPort("127.0.0.1", 443), &stdTLS.Config{}, nil, admission, owner)

	_, err = dial(context.Background(), "127.0.0.1:443", &stdTLS.Config{}, &quic.Config{})
	require.ErrorIs(t, err, resourceguard.ErrQueueFull,
		"an H3 XHTTP creation ran while the process budget was saturated")
	require.Zero(t, dialer.calls, "the UDP socket was opened before admission")
	require.Nil(t, owner.conn.Load(), "a refused creation must not be published into the pooled conn")

	held.Release()
	sentinel := errors.New("refused by the test socket")
	dialer.err = sentinel
	_, err = dial(context.Background(), "127.0.0.1:443", &stdTLS.Config{}, &quic.Config{})
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, 1, dialer.calls)

	// The counters include the permit this test holds to saturate the gate.
	stats := guard.Stats().Gate
	require.EqualValues(t, 3, stats.Requested)
	require.EqualValues(t, 1, stats.Refused)
	require.EqualValues(t, 2, stats.Admitted)
	require.EqualValues(t, 2, stats.Released)
	require.Zero(t, stats.Inflight)
}

// TestH3RefusalIsClassifiedAsLocalRejection keeps the route-level consequence right: a refused H3
// creation must look like this process's backpressure, not like a dead node.
func TestH3RefusalIsClassifiedAsLocalRejection(t *testing.T) {
	guard := resourceguard.NewGuard(resourceguard.Config{Limit: 1, Queue: 0, Wait: time.Millisecond}, nil)
	held, err := guard.Acquire(context.Background(), resourceguard.ClassNormal)
	require.NoError(t, err)
	defer held.Release()

	admission := h3AdmissionFromContext(resourceguard.NewContext(context.Background(), guard))
	dialer := &xhttpCountingDialer{}
	dial := newHTTP3DialFunc(dialer, M.ParseSocksaddrHostPort("127.0.0.1", 443), &stdTLS.Config{}, nil, admission, &http3XmuxConn{})
	_, err = dial(context.Background(), "127.0.0.1:443", &stdTLS.Config{}, &quic.Config{})
	require.True(t, resourceguard.IsLocalRejection(err),
		"an admission refusal must stay classified as local backpressure, not as a path failure")
}
