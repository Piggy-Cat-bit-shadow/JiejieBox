package v2rayhttp

// The network-generation ownership of the HTTP/2 pool.
//
// # The gap these tests pin
//
// `http2.Transport` keeps established connections and every DialContext hands out a stream on one
// of them. A physical network change is published by advancing the runtimecoord epoch before the
// listeners run, so a demand that arrives in that window was still served by a connection built for
// the previous network: the pool looked healthy and nothing at the hand-out compared generations
// (audit Q12/F-15, "no protocol reads the epoch at pool hand-out").
//
// # What is required, stated as three observable behaviours
//
//  1. Registration is owned by the pool: a client built with a coordinator registers one resource,
//     a published generation makes that resource stale, and Close unregisters it.
//  2. An idle connection of a previous generation is never lent to a new demand, and a BUSY one is
//     not cut to achieve that: the new demand opens a fresh connection while the stream that is
//     already running finishes, and the drained generation closes when its last stream ends.
//  3. A creation that started before the change and finished after it is discarded rather than
//     published as the current generation; the next demand therefore retries on the new network.
//
// The tests use a real TLS + HTTP/2 server and the production constructor, so they observe
// connection count at the server rather than a mock's bookkeeping.

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// generationListener counts every accepted TCP connection and every close, which is how the tests
// observe "a new connection was really opened" and "the drained generation really closed" without
// reading transport internals.
type generationListener struct {
	net.Listener
	accepted *atomic.Int32
	closed   *atomic.Int32
}

func (l *generationListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.accepted.Add(1)
	return &generationConn{Conn: conn, closed: l.closed}, nil
}

type generationConn struct {
	net.Conn
	closed *atomic.Int32
	once   sync.Once
}

func (c *generationConn) Close() error {
	c.once.Do(func() { c.closed.Add(1) })
	return c.Conn.Close()
}

func newGenerationServer(t *testing.T, handler http.Handler) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	accepted := new(atomic.Int32)
	closed := new(atomic.Int32)
	server.Listener = &generationListener{Listener: server.Listener, accepted: accepted, closed: closed}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, accepted, closed
}

func newGenerationClient(t *testing.T, ctx context.Context, address string) *Client {
	t.Helper()
	tlsConfig, err := tls.NewClientWithOptions(tls.ClientOptions{
		Context:       ctx,
		Logger:        log.NewNOPFactory().NewLogger("generation"),
		ServerAddress: address,
		Options:       option.OutboundTLSOptions{Enabled: true, Insecure: true},
	})
	require.NoError(t, err)
	instance, err := NewClient(ctx, N.SystemDialer, M.ParseSocksaddr(address), option.V2RayHTTPOptions{}, tlsConfig)
	require.NoError(t, err)
	client, isClient := instance.(*Client)
	require.True(t, isClient)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func generationTestContext(t *testing.T) (context.Context, *runtimecoord.Coordinator) {
	t.Helper()
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	return service.ContextWith[*runtimecoord.Coordinator](context.Background(), coordinator), coordinator
}

func readExactly(t *testing.T, conn net.Conn, length int) string {
	t.Helper()
	buffer := make([]byte, length)
	_, err := io.ReadFull(conn, buffer)
	require.NoError(t, err)
	return string(buffer)
}

// TestPreviousGenerationIdlePoolIsNotLent is the audit's concrete case: the pooled connection is
// idle, a new generation is published, and the next demand arrives before the listener phase. It
// must not be handed that connection.
func TestPreviousGenerationIdlePoolIsNotLent(t *testing.T) {
	ctx, coordinator := generationTestContext(t)
	server, accepted, _ := newGenerationServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	client := newGenerationClient(t, ctx, server.Listener.Addr().String())

	first, err := client.DialContext(ctx)
	require.NoError(t, err)
	require.Equal(t, "ok", readExactly(t, first, 2))
	require.NoError(t, first.Close())
	require.Equal(t, int32(1), accepted.Load(), "the first demand must open exactly one connection")

	coordinator.Advance(1)

	second, err := client.DialContext(ctx)
	require.NoError(t, err)
	require.Equal(t, "ok", readExactly(t, second, 2))
	require.NoError(t, second.Close())
	require.Equal(t, int32(2), accepted.Load(),
		"a demand in the transition window must not be lent the previous generation's connection")
}

// TestBusyGenerationDrainsWithoutCuttingItsStream is the other half of the same rule, and the reason
// the idle connection may not simply be closed: a stream that is already running belongs to the
// old generation and must finish, while new demands move to a fresh generation.
func TestBusyGenerationDrainsWithoutCuttingItsStream(t *testing.T) {
	ctx, coordinator := generationTestContext(t)
	releaseSecondChunk := make(chan struct{})
	var requests atomic.Int32
	server, accepted, closed := newGenerationServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if requests.Add(1) == 1 {
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			_, _ = writer.Write([]byte("one"))
			writer.(http.Flusher).Flush()
			<-releaseSecondChunk
			_, _ = writer.Write([]byte("two"))
			writer.(http.Flusher).Flush()
			return
		}
		_, _ = writer.Write([]byte("fresh"))
	}))
	client := newGenerationClient(t, ctx, server.Listener.Addr().String())

	busy, err := client.DialContext(ctx)
	require.NoError(t, err)
	require.Equal(t, "one", readExactly(t, busy, 3))
	require.Equal(t, int32(1), accepted.Load())

	coordinator.Advance(1)

	fresh, err := client.DialContext(ctx)
	require.NoError(t, err)
	require.Equal(t, "fresh", readExactly(t, fresh, 5))
	require.Equal(t, int32(2), accepted.Load(),
		"a new demand must open a new connection even while the previous generation still carries a stream")

	// The previous generation is drained, not killed: the stream that was already running keeps
	// delivering its remaining bytes.
	close(releaseSecondChunk)
	require.Equal(t, "two", readExactly(t, busy, 3),
		"draining the previous generation must not cut the stream that is already using it")

	require.NoError(t, busy.Close())
	require.Eventually(t, func() bool { return closed.Load() == 1 }, 5*time.Second, 10*time.Millisecond,
		"the drained generation must close once its last stream ends")
	require.NoError(t, fresh.Close())
}

// TestLateCreationOfThePreviousGenerationIsDiscarded pins the late-result rule: a creation that
// began before the change and returned after it is not evidence that the pool is current, so it is
// discarded and the next demand starts again on the new network.
func TestLateCreationOfThePreviousGenerationIsDiscarded(t *testing.T) {
	ctx, coordinator := generationTestContext(t)
	entered := make(chan struct{})
	reply := make(chan struct{})
	server, _, _ := newGenerationServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(entered)
		<-reply
		_, _ = writer.Write([]byte("late"))
	}))
	client := newGenerationClient(t, ctx, server.Listener.Addr().String())

	conn, err := client.DialContext(ctx)
	require.NoError(t, err)
	<-entered

	coordinator.Advance(1)
	close(reply)

	_, readErr := io.ReadAll(conn)
	require.Error(t, readErr, "a result from the previous generation must be discarded, not handed out")
	require.Contains(t, readErr.Error(), "network changed")
	require.True(t, client.generationStale(),
		"a discarded late result must not acknowledge the new generation")
}

// TestPoolOwnsAndReleasesItsGenerationRegistration pins the ownership contract: one registration per
// pool, a published generation is visible before any listener runs, and Close unregisters it so a
// torn-down pool is never reached by a later sweep.
func TestPoolOwnsAndReleasesItsGenerationRegistration(t *testing.T) {
	ctx, coordinator := generationTestContext(t)
	server, _, _ := newGenerationServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	client := newGenerationClient(t, ctx, server.Listener.Addr().String())

	require.NotNil(t, client.registration, "the pool must be the registration's owner")
	require.False(t, client.generationStale())

	coordinator.Advance(1)
	require.True(t, client.generationStale(),
		"a published generation must make the registration stale before any listener ran")

	require.NoError(t, client.Close())
	require.True(t, client.registration.Closed(),
		"closing the pool must unregister it from the coordinator")
}

// TestRegistrationCanBeAbsent keeps the embedder contract: no coordinator means no registration and
// every generation check is a no-op, which is the previous behaviour exactly.
func TestRegistrationCanBeAbsent(t *testing.T) {
	server, accepted, _ := newGenerationServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	client := newGenerationClient(t, context.Background(), server.Listener.Addr().String())

	require.Nil(t, client.registration)
	require.False(t, client.generationStale())

	conn, err := client.DialContext(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ok", readExactly(t, conn, 2))
	require.NoError(t, conn.Close())
	require.Equal(t, int32(1), accepted.Load())
}

var _ = strings.TrimSpace
