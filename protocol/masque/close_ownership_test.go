//go:build with_quic

package masque

// Close ownership for the endpoint object itself.
//
// The endpoint manager builds an endpoint and discards it when another endpoint claimed the tag
// first. That object has never been started, so the scope never saw it and never registered the
// masque client's Close: the HTTP client, its idle connections and its goroutines were released by
// nothing. These tests pin the two properties that make the discard safe - Close works without a
// Start, and a second Close is harmless - and that a closed endpoint refuses to dial.

import (
	"context"
	"net/netip"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func TestClientEndpointCloseWithoutStartIsSafeAndIdempotent(t *testing.T) {
	requireHTTP3Client(t)
	router := &fakeBootstrapRouter{answer: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}
	built, err := buildTestEndpoint(t, router, "masque.example", newBootstrapTLSOptions())
	require.NoError(t, err)
	clientEndpoint, isClient := built.(*ClientEndpoint)
	require.True(t, isClient)
	require.NotNil(t, clientEndpoint.httpClient)

	closed := make(chan error, 1)
	go func() { closed <- clientEndpoint.Close() }()
	select {
	case err = <-closed:
		require.NoError(t, err, "Close must work without a Start")
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung on an endpoint that was never started")
	}
	require.NoError(t, clientEndpoint.Close(), "repeated Close must be safe")

	// A closed endpoint must not reach for a transport it has released.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	conn, dialErr := clientEndpoint.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("127.0.0.1:9"))
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, dialErr, "a closed endpoint admitted a dial")
}
