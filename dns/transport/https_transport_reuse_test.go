package transport

// The HTTPS DNS transport's pool is net/http's, not the query multiplexer's.
//
// net/http does not call the dialer when it reuses a socket, so the chain cannot be read at dial
// time. The connection carries it instead, and net/http reports which connection a request used;
// this test drives the REAL wrapper and a REAL server, so what it proves is the production reuse.

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// recordingDialer records one step into the exchange's recorder and dials for real.
type recordingDialer struct {
	tag    string
	dials  atomic.Int32
	target string
}

func (d *recordingDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.dials.Add(1)
	adapter.DialPathFromContext(ctx).RecordStep("", d.tag, 3, true)
	return net.Dial(network, d.target)
}

func (d *recordingDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

// TestAnHTTPTransportReusePublishesTheConnectionsChain is the HTTPS pool's attribution.
func TestAnHTTPTransportReusePublishesTheConnectionsChain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "ok")
	}))
	t.Cleanup(server.Close)

	fallback := new(atomic.Bool)
	fallback.Store(true) // the HTTP/1.1 half of the wrapper is the pool under test
	dialerFor := &recordingDialer{tag: "node-a", target: server.Listener.Addr().String()}
	wrapper := NewHTTPSTransportWrapper(dialerFor, M.ParseSocksaddr(server.Listener.Addr().String()), fallback)
	t.Cleanup(wrapper.Close)

	roundTrip := func(pathRecorder *adapter.DialPath) {
		request, err := http.NewRequestWithContext(
			adapter.WithDialPath(context.Background(), pathRecorder), http.MethodPost, server.URL, strings.NewReader("query"))
		require.NoError(t, err)
		response, err := wrapper.RoundTrip(request)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
	}

	first := adapter.NewDialPath()
	first.BeginDial()
	roundTrip(first)
	require.Len(t, first.Result().Steps, 1, "the first request dialled, and the dial was recorded")
	require.Equal(t, int32(1), dialerFor.dials.Load())

	second := adapter.NewDialPath()
	second.BeginDial()
	roundTrip(second)
	require.Equal(t, int32(1), dialerFor.dials.Load(),
		"the second request must have reused the pooled socket, or this proves nothing")

	reused := second.Result()
	require.True(t, reused.Replayed, "a reuse is published from the connection, not counted as a dial")
	require.Len(t, reused.Steps, 1)
	require.Equal(t, "node-a", reused.Steps[0].Tag)
	require.Equal(t, adapter.DialPathReasonNone, reused.Reason())
}
