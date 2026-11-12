package v2raygrpclite

// Q12 for the gRPC pool: the same three observable behaviours protocol/v2rayhttp pins, on the
// transport the audit found had NO generation owner at all.
//
//  1. The pool owns its registration: a client built with a coordinator registers one resource, a
//     published generation makes it stale, and Close unregisters it.
//  2. A previous generation's pooled connection is not lent to a new stream, and a BUSY previous
//     generation is drained rather than cut: the new stream opens a fresh connection while the one
//     already running finishes.
//  3. A stream that was being established when the generation changed is discarded and reports a
//     retryable error, so the next demand starts on the current network.
//
// The server is a real TLS + HTTP/2 server, so these tests observe accepted connections instead of a
// mock's bookkeeping.

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

type countingListener struct {
	net.Listener
	accepted *atomic.Int32
}

func (l *countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.accepted.Add(1)
	return conn, nil
}

// writeGRPCMessage writes one gRPC message frame: the 5-byte prefix (compression flag and length),
// the protobuf field tag the transport expects, then the uvarint length and the payload.
func writeGRPCMessage(writer http.ResponseWriter, payload string) {
	frame := []byte{0x00, 0, 0, 0, 0, 0x0A}
	frame = binary.AppendUvarint(frame, uint64(len(payload)))
	frame = append(frame, payload...)
	_, _ = writer.Write(frame)
	if flusher, isFlusher := writer.(http.Flusher); isFlusher {
		flusher.Flush()
	}
}

func newGRPCServer(t *testing.T, handler http.Handler) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	accepted := new(atomic.Int32)
	server.Listener = &countingListener{Listener: server.Listener, accepted: accepted}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, accepted
}

func newGRPCGenerationContext(t *testing.T) (context.Context, *runtimecoord.Coordinator) {
	t.Helper()
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	return service.ContextWith[*runtimecoord.Coordinator](context.Background(), coordinator), coordinator
}

func newGRPCGenerationClient(t *testing.T, ctx context.Context, address string) *Client {
	t.Helper()
	tlsConfig, err := tls.NewClientWithOptions(tls.ClientOptions{
		Context:       ctx,
		Logger:        log.NewNOPFactory().NewLogger("grpc-generation"),
		ServerAddress: address,
		Options:       option.OutboundTLSOptions{Enabled: true, Insecure: true},
	})
	require.NoError(t, err)
	instance := NewClient(ctx, N.SystemDialer, M.ParseSocksaddr(address), option.V2RayGRPCOptions{}, tlsConfig)
	client, isClient := instance.(*Client)
	require.True(t, isClient)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func readGRPCExactly(t *testing.T, conn net.Conn, length int) string {
	t.Helper()
	buffer := make([]byte, length)
	_, err := io.ReadFull(conn, buffer)
	require.NoError(t, err)
	return string(buffer)
}

// TestTheGRPCPoolDoesNotLendAPreviousGeneration is the audit's concrete case for this pool.
func TestTheGRPCPoolDoesNotLendAPreviousGeneration(t *testing.T) {
	t.Parallel()
	ctx, coordinator := newGRPCGenerationContext(t)
	server, accepted := newGRPCServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeGRPCMessage(writer, "ok")
	}))
	client := newGRPCGenerationClient(t, ctx, server.Listener.Addr().String())

	first, err := client.DialContext(ctx)
	require.NoError(t, err)
	require.Equal(t, "ok", readGRPCExactly(t, first, 2))
	require.NoError(t, first.Close())
	require.Equal(t, int32(1), accepted.Load())

	coordinator.Advance(1)

	second, err := client.DialContext(ctx)
	require.NoError(t, err)
	require.Equal(t, "ok", readGRPCExactly(t, second, 2))
	require.NoError(t, second.Close())
	require.Equal(t, int32(2), accepted.Load(),
		"a stream in the transition window must not be lent the previous generation's connection")
}

// TestTheGRPCBusyGenerationDrainsInsteadOfBeingCut is the other half: a stream that is already
// running finishes on the generation it started on.
func TestTheGRPCBusyGenerationDrainsInsteadOfBeingCut(t *testing.T) {
	t.Parallel()
	ctx, coordinator := newGRPCGenerationContext(t)
	releaseSecondChunk := make(chan struct{})
	server, accepted := newGRPCServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeGRPCMessage(writer, "one")
		<-releaseSecondChunk
		writeGRPCMessage(writer, "two")
	}))
	client := newGRPCGenerationClient(t, ctx, server.Listener.Addr().String())

	busy, err := client.DialContext(ctx)
	require.NoError(t, err)
	require.Equal(t, "one", readGRPCExactly(t, busy, 3))
	require.Equal(t, int32(1), accepted.Load())

	coordinator.Advance(1)
	// The pool is now stale and the retired generation still carries the stream above.
	client.retireGeneration()
	draining := client.ResidencyLedger()
	require.EqualValues(t, 1, draining.Draining, "the busy generation must be draining, not gone")

	close(releaseSecondChunk)
	require.Equal(t, "two", readGRPCExactly(t, busy, 3),
		"retiring a generation must not cut the stream already running on it")
	require.NoError(t, busy.Close())
	require.Eventually(t, func() bool {
		return client.ResidencyLedger().Draining == 0
	}, 5*time.Second, 10*time.Millisecond, "the drained generation must be released once its stream ends")
}

// TestTheGRPCLateStreamIsDiscarded pins the late-result rule.
func TestTheGRPCLateStreamIsDiscarded(t *testing.T) {
	t.Parallel()
	ctx, coordinator := newGRPCGenerationContext(t)
	entered := make(chan struct{})
	reply := make(chan struct{})
	server, _ := newGRPCServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(entered)
		<-reply
		writeGRPCMessage(writer, "late")
	}))
	client := newGRPCGenerationClient(t, ctx, server.Listener.Addr().String())

	conn, err := client.DialContext(ctx)
	require.NoError(t, err)
	<-entered

	coordinator.Advance(1)
	close(reply)

	_, readErr := io.ReadAll(conn)
	require.Error(t, readErr, "a stream established on the previous generation must be discarded")
	require.Contains(t, readErr.Error(), "network changed")
	require.True(t, client.generationStale(), "a discarded late stream must not acknowledge the new generation")
}

// TestTheGRPCPoolOwnsAndReleasesItsRegistration pins ownership: one registration, stale before any
// listener ran, removed by Close.
func TestTheGRPCPoolOwnsAndReleasesItsRegistration(t *testing.T) {
	t.Parallel()
	ctx, coordinator := newGRPCGenerationContext(t)
	server, _ := newGRPCServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeGRPCMessage(writer, "ok")
	}))
	client := newGRPCGenerationClient(t, ctx, server.Listener.Addr().String())

	require.NotNil(t, client.registration, "the pool must be the registration's owner")
	require.False(t, client.generationStale())
	coordinator.Advance(1)
	require.True(t, client.generationStale())
	require.NoError(t, client.Close())
	require.True(t, client.registration.Closed(), "closing the pool must unregister it")
}

// TestTheGRPCEmbedderWithoutACoordinatorKeepsItsBehaviour keeps the embedder contract: no coordinator,
// no registration, and every generation check is a no-op.
func TestTheGRPCEmbedderWithoutACoordinatorKeepsItsBehaviour(t *testing.T) {
	t.Parallel()
	server, accepted := newGRPCServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeGRPCMessage(writer, "ok")
	}))
	client := newGRPCGenerationClient(t, context.Background(), server.Listener.Addr().String())
	require.Nil(t, client.registration)
	require.False(t, client.generationStale())

	conn, err := client.DialContext(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ok", readGRPCExactly(t, conn, 2))
	require.NoError(t, conn.Close())
	require.Equal(t, int32(1), accepted.Load())
}

var _ = sync.Once{}
