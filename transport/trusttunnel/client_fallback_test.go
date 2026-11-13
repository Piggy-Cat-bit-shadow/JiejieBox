package trusttunnel

// The HTTP/3 -> HTTP/2 recovery of one session creation (quic_fallback_to_h2).
//
// These tests drive the two legs through the client's own seams, so a case can hold an attempt open,
// fail it, or count the attempts without a handshake and without waiting out a real budget. What they
// pin is the contract the feature is bought for:
//
//   - a disabled fallback keeps the strict behaviour (no HTTP/2 attempt, ever);
//   - one failed HTTP/3 establishment produces ONE HTTP/2 session, and the session carries traffic;
//   - a caller's cancellation is not a verdict about the node (no fallback, no cooldown);
//   - the HTTP/3 attempt is bounded, and a shorter caller deadline is not extended;
//   - the cooldown keeps later sessions off UDP, expires by the clock, and the next REAL demand
//     probes HTTP/3 again by retiring the healthy HTTP/2 session it was using;
//   - one shared creation never multiplies into per-demand attempts.

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// fallbackHeaders is the destination used by every case: the session is faked, so only its shape
// matters.
var fallbackDestination = M.ParseSocksaddr("example.com:443")

// newFallbackTestClient returns a client with the fallback enabled and the two legs replaced. The
// clock is virtual so the cooldown can expire without a wait; the returned pointer is that clock in
// unix nanoseconds.
func newFallbackTestClient(t *testing.T, h3, h2 func(context.Context) (sessionConn, error)) (*Client, *atomic.Int64) {
	t.Helper()
	if !quicIncluded {
		t.Skip("HTTP/3 is not in this build")
	}
	_, clientTLS := newTestTLS(t)
	client, err := NewClient(ClientOptions{
		Dialer:       &testDialer{},
		Server:       M.ParseSocksaddr("127.0.0.1:1"),
		TLSConfig:    clientTLS,
		Protocol:     ProtocolHTTP3,
		QUICFallback: true,
		Username:     testUsername,
		Password:     testPassword,
		AppName:      "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	client.h3Budget = 40 * time.Millisecond
	client.h3Cooldown = time.Second
	clock := new(atomic.Int64)
	clock.Store(time.Unix(1_700_000_000, 0).UnixNano())
	client.now = func() time.Time { return time.Unix(0, clock.Load()) }
	if h3 != nil {
		client.connectH3 = h3
	}
	if h2 != nil {
		client.connectH2 = h2
	}
	return client, clock
}

// dialFallback opens one CONNECT through the client and closes it, returning the error.
func dialFallback(t *testing.T, ctx context.Context, client *Client) error {
	t.Helper()
	conn, err := client.DialContext(ctx, fallbackDestination)
	if err != nil {
		return err
	}
	require.NoError(t, conn.Close())
	return nil
}

func TestFallbackDisabledKeepsStrictHTTP3(t *testing.T) {
	var h2Calls atomic.Int32
	client, _ := newFallbackTestClient(t,
		func(context.Context) (sessionConn, error) { return nil, errors.New("quic handshake: udp unreachable") },
		func(context.Context) (sessionConn, error) { h2Calls.Add(1); return newFakeSession(), nil },
	)
	client.quicFallback = false

	err := dialFallback(t, context.Background(), client)
	require.ErrorContains(t, err, "udp unreachable")
	require.Zero(t, h2Calls.Load(), "without the option an HTTP/3 node never opens an HTTP/2 session")
}

func TestFallbackOpensHTTP2WhenTheHTTP3AttemptFails(t *testing.T) {
	var h3Calls, h2Calls atomic.Int32
	client, _ := newFallbackTestClient(t,
		func(context.Context) (sessionConn, error) {
			h3Calls.Add(1)
			return nil, errors.New("quic handshake: udp unreachable")
		},
		func(context.Context) (sessionConn, error) { h2Calls.Add(1); return newFakeSession(), nil },
	)

	require.NoError(t, dialFallback(t, context.Background(), client))
	require.EqualValues(t, 1, h3Calls.Load())
	require.EqualValues(t, 1, h2Calls.Load())
	require.True(t, client.h3Cordoned(), "the failed attempt arms the provisional verdict")

	// The published session is the HTTP/2 one: the next demand is carried by it, not by a new creation.
	require.NoError(t, dialFallback(t, context.Background(), client))
	require.EqualValues(t, 1, h3Calls.Load())
	require.EqualValues(t, 1, h2Calls.Load())
}

// An aborted creation - Close, or the Reset a network change performs - says nothing about the node:
// it must neither open the HTTP/2 leg nor arm the cooldown.
func TestFallbackDoesNotFireOnAnAbortedCreation(t *testing.T) {
	entered := make(chan struct{}, 1)
	left := make(chan struct{})
	var h3Calls, h2Calls atomic.Int32
	client, _ := newFallbackTestClient(t,
		func(ctx context.Context) (sessionConn, error) {
			if h3Calls.Add(1) == 1 {
				entered <- struct{}{}
				<-ctx.Done()
				close(left)
				return nil, ctx.Err()
			}
			return nil, errors.New("udp unreachable")
		},
		func(context.Context) (sessionConn, error) { h2Calls.Add(1); return newFakeSession(), nil },
	)
	client.h3Budget = 10 * time.Second

	dialDone := make(chan error, 1)
	go func() {
		conn, err := client.DialContext(context.Background(), fallbackDestination)
		if err == nil {
			_ = conn.Close()
		}
		dialDone <- err
	}()
	<-entered
	client.Reset() // exactly what the outbound does when the network changes
	<-left
	require.Error(t, <-dialDone)
	require.Zero(t, h2Calls.Load(), "an aborted creation is not a statement about the node's UDP path")
	require.False(t, client.h3Cordoned(), "and it must not arm the cooldown")

	require.NoError(t, dialFallback(t, context.Background(), client))
	require.EqualValues(t, 2, h3Calls.Load(), "the next demand still prefers HTTP/3")
	require.EqualValues(t, 1, h2Calls.Load(), "and falls back only on a real failure")
}

func TestFallbackBoundsTheHTTP3AttemptAndStillOpensHTTP2(t *testing.T) {
	var budgetSeen atomic.Int64
	client, _ := newFallbackTestClient(t,
		func(ctx context.Context) (sessionConn, error) {
			if deadline, ok := ctx.Deadline(); ok {
				budgetSeen.Store(int64(time.Until(deadline)))
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
		func(context.Context) (sessionConn, error) { return newFakeSession(), nil },
	)
	client.h3Budget = 60 * time.Millisecond

	started := time.Now()
	require.NoError(t, dialFallback(t, context.Background(), client))
	elapsed := time.Since(started)
	require.GreaterOrEqual(t, elapsed, 50*time.Millisecond, "the bounded attempt really ran")
	require.Less(t, elapsed, 5*time.Second, "and it did not wait out a QUIC idle timeout")
	require.LessOrEqual(t, time.Duration(budgetSeen.Load()), 80*time.Millisecond,
		"the attempt ran under its own budget")
}

// A creation whose context has less time than the HTTP/3 budget gets that time - not more - so a short
// caller deadline stays short instead of being extended by the fallback's own cap.
func TestFallbackHonoursAShorterCreationDeadline(t *testing.T) {
	var budgetSeen atomic.Int64
	client, _ := newFallbackTestClient(t,
		func(ctx context.Context) (sessionConn, error) {
			if deadline, ok := ctx.Deadline(); ok {
				budgetSeen.Store(int64(time.Until(deadline)))
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
		func(ctx context.Context) (sessionConn, error) {
			// The HTTP/2 leg has only what the creation had left; here the seam waits it out too, so
			// the creation ends on the caller's deadline however the two legs interleave.
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)
	client.h3Budget = 10 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_, err := client.connectSession(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.LessOrEqual(t, time.Duration(budgetSeen.Load()), 60*time.Millisecond,
		"the HTTP/3 attempt may not outlive the creation")
}

// With time to spare the HTTP/2 leg really gets it: the budget caps the HTTP/3 attempt, it does not
// consume the caller's deadline.
func TestFallbackLeavesTheRemainingTimeToHTTP2(t *testing.T) {
	var h2Remaining atomic.Int64
	client, _ := newFallbackTestClient(t,
		func(ctx context.Context) (sessionConn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		func(ctx context.Context) (sessionConn, error) {
			if deadline, ok := ctx.Deadline(); ok {
				h2Remaining.Store(int64(time.Until(deadline)))
			}
			return newFakeSession(), nil
		},
	)
	client.h3Budget = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	conn, err := client.connectSession(ctx)
	require.NoError(t, err)
	require.NoError(t, conn.close())
	require.Greater(t, time.Duration(h2Remaining.Load()), 200*time.Millisecond,
		"the HTTP/2 leg runs with what the creation still had")
	require.True(t, usedFallback(conn), "and the session is marked as the fallback's own")
}

func TestFallbackCordonsHTTP3AndProbesAgainByDemand(t *testing.T) {
	var h3Calls, h2Calls atomic.Int32
	client, clock := newFallbackTestClient(t,
		func(context.Context) (sessionConn, error) { h3Calls.Add(1); return nil, errors.New("udp unreachable") },
		func(context.Context) (sessionConn, error) { h2Calls.Add(1); return newFakeSession(), nil },
	)

	// First creation: HTTP/3 attempted, HTTP/2 published.
	require.NoError(t, dialFallback(t, context.Background(), client))
	require.EqualValues(t, 1, h3Calls.Load())
	require.EqualValues(t, 1, h2Calls.Load())

	// Drop the session without a network change: the cooldown, not Reset, governs the next creation.
	client.RetireSuspect()
	require.NoError(t, dialFallback(t, context.Background(), client))
	require.EqualValues(t, 1, h3Calls.Load(), "inside the cooldown new sessions go straight to HTTP/2")
	require.EqualValues(t, 2, h2Calls.Load())

	// The cooldown expires by the clock, and the next REAL demand retries HTTP/3 by retiring the
	// healthy HTTP/2 session instead of cutting its streams.
	clock.Add(int64(2 * time.Second))
	require.NoError(t, dialFallback(t, context.Background(), client))
	require.EqualValues(t, 2, h3Calls.Load(), "an expired cooldown means one new HTTP/3 probe")
	require.EqualValues(t, 3, h2Calls.Load(), "which falls back again because the path is still down")
}

func TestFallbackResetForgetsTheVerdict(t *testing.T) {
	var h3Calls atomic.Int32
	client, _ := newFallbackTestClient(t,
		func(context.Context) (sessionConn, error) { h3Calls.Add(1); return nil, errors.New("udp unreachable") },
		func(context.Context) (sessionConn, error) { return newFakeSession(), nil },
	)
	require.NoError(t, dialFallback(t, context.Background(), client))
	require.True(t, client.h3Cordoned())

	// A network change is a new generation: the outbound calls Reset, and the verdict about the old
	// network must not survive it.
	client.Reset()
	require.False(t, client.h3Cordoned())
	require.NoError(t, dialFallback(t, context.Background(), client))
	require.EqualValues(t, 2, h3Calls.Load(), "the new network may try HTTP/3 again")
}

func TestFallbackHTTP2SuccessClearsTheVerdict(t *testing.T) {
	var h3Calls atomic.Int32
	client, _ := newFallbackTestClient(t,
		func(context.Context) (sessionConn, error) {
			if h3Calls.Add(1) == 1 {
				return nil, errors.New("udp unreachable")
			}
			return newFakeSession(), nil
		},
		func(context.Context) (sessionConn, error) { return newFakeSession(), nil },
	)
	require.NoError(t, dialFallback(t, context.Background(), client))
	require.True(t, client.h3Cordoned())

	// Force the next creation past the cooldown; the HTTP/3 leg now succeeds and the verdict is dropped.
	client.Reset()
	client.h3CordonUntil.Store(0)
	require.NoError(t, dialFallback(t, context.Background(), client))
	require.False(t, client.h3Cordoned())
}

func TestFallbackReportsBothLegs(t *testing.T) {
	client, _ := newFallbackTestClient(t,
		func(context.Context) (sessionConn, error) { return nil, errors.New("quic handshake: udp unreachable") },
		func(context.Context) (sessionConn, error) { return nil, errors.New("dial tcp: connection refused") },
	)
	err := dialFallback(t, context.Background(), client)
	require.ErrorContains(t, err, "udp unreachable", "the reason HTTP/2 was tried")
	require.ErrorContains(t, err, "connection refused", "and the reason the session failed")
	require.True(t, client.h3Cordoned())
}

func TestFallbackKeepsOneSharedCreation(t *testing.T) {
	release := make(chan struct{})
	var h3Calls, h2Calls atomic.Int32
	client, _ := newFallbackTestClient(t,
		func(context.Context) (sessionConn, error) {
			h3Calls.Add(1)
			<-release
			return nil, errors.New("udp unreachable")
		},
		func(context.Context) (sessionConn, error) { h2Calls.Add(1); return newFakeSession(), nil },
	)
	client.h3Budget = 10 * time.Second

	var waitGroup sync.WaitGroup
	for range 5 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			conn, err := client.DialContext(context.Background(), fallbackDestination)
			if err == nil {
				_ = conn.Close()
			}
		}()
	}
	require.Eventually(t, func() bool { return h3Calls.Load() == 1 }, time.Second, time.Millisecond)
	close(release)
	waitGroup.Wait()
	require.EqualValues(t, 1, h3Calls.Load(), "one logical creation is one HTTP/3 attempt")
	require.LessOrEqual(t, h2Calls.Load(), int32(1), "and at most one HTTP/2 session")
}

// The HTTP/2 leg must offer h2 and not the HTTP/3 leg's protocol; the client keeps them in separate
// configurations for that reason.
func TestFallbackKeepsTheHTTP2LegsOwnALPN(t *testing.T) {
	if !quicIncluded {
		t.Skip("HTTP/3 is not in this build")
	}
	_, clientTLS := newTestTLS(t)
	client, err := NewClient(ClientOptions{
		Dialer:       &testDialer{},
		Server:       M.ParseSocksaddr("127.0.0.1:1"),
		TLSConfig:    clientTLS,
		Protocol:     ProtocolHTTP3,
		QUICFallback: true,
		Username:     testUsername,
		Password:     testPassword,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.Equal(t, []string{ProtocolHTTP3, ProtocolHTTP2}, client.tlsConfig.NextProtos(),
		"the HTTP/3 leg offers both, in preference order")
	require.NotNil(t, client.tlsConfigH2)
	require.Equal(t, []string{ProtocolHTTP2}, client.tlsConfigH2.NextProtos(),
		"and the HTTP/2 leg offers only h2")
}

// The recovered session must be a REAL HTTP/2 session: this case starts the node's TCP/H2 service,
// fails the HTTP/3 leg deterministically, and moves payload in both directions - a TCP stream and a
// UDP association - over the session the fallback published. A Dial that returns nil would not prove
// that the stream can still be read and written.
func TestFallbackCarriesRealPayloadOverTheRecoveredHTTP2Session(t *testing.T) {
	if !quicIncluded {
		t.Skip("HTTP/3 is not in this build")
	}
	server := startTestServer(t, ProtocolHTTP2)
	echo := startEchoServer(t)
	var h3Calls atomic.Int32
	client := server.newClient(t, ProtocolHTTP3, testPassword, &testDialer{}, func(options *ClientOptions) {
		options.QUICFallback = true
	})
	client.connectH3 = func(context.Context) (sessionConn, error) {
		h3Calls.Add(1)
		return nil, errors.New("udp blocked (test)")
	}
	ctx := testContext(t)

	conn, err := client.DialContext(ctx, echo)
	require.NoError(t, err)
	client.access.Lock()
	fallbackSession := client.session != nil && client.session.fallbackH2
	client.access.Unlock()
	require.True(t, fallbackSession, "the published session must be the recovery's own")
	echoRoundTrip(t, conn, 256*1024)
	require.NoError(t, conn.Close())

	packetConn, err := client.ListenPacket(ctx)
	require.NoError(t, err)
	defer packetConn.Close()
	destination := M.ParseSocksaddr("127.0.0.1:5353")
	payload := bytes.Repeat([]byte{7}, 1200)
	_, err = packetConn.WriteTo(payload, destination.UDPAddr())
	require.NoError(t, err)
	received := make([]byte, 65535)
	n, source, err := packetConn.ReadFrom(received)
	require.NoError(t, err)
	require.Equal(t, destination, M.SocksaddrFromNet(source))
	require.Equal(t, payload, received[:n], "the UDP association really carried the datagram")
	require.EqualValues(t, 1, h3Calls.Load(), "one recovery served both demands")
}
