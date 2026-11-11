package mieru

// Stage-2 coupling test: the single-transport dial path must take the same
// generation check as the fallback path.
//
// InterfaceUpdated drains in-flight dials before stopping a generation, but the
// drain is bounded: a dial that outlives it can still return a connection created
// for the network that was just left. The fallback path already refused that
// connection; the single-transport path returned it unchanged, so the route could
// receive a session the outbound no longer owned.

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

type generationConn struct {
	net.Conn
	closed atomic.Int32
}

func (c *generationConn) Close() error {
	c.closed.Add(1)
	return nil
}

func TestSingleTransportDialRejectsAConnectionFromAnotherGeneration(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	conn := &generationConn{}
	client := &fakeClient{dialFunc: func(context.Context, net.Addr) (net.Conn, error) {
		close(started)
		<-release
		return conn, nil
	}}
	outboundInstance := &Outbound{
		logger:     nopLogger(),
		started:    true,
		generation: 1,
		primary:    fakeRuntimeForTest("primary", client),
	}
	spec, err := toNetAddrSpec(M.ParseSocksaddr("192.0.2.61:80"), N.NetworkTCP)
	require.NoError(t, err)

	dialResult := make(chan error, 1)
	go func() {
		_, dialErr := outboundInstance.dial(context.Background(), spec)
		dialResult <- dialErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the dial never reached the transport")
	}
	// The network changes while the dial is in flight, after the drain gave up.
	outboundInstance.access.Lock()
	outboundInstance.generation = 2
	outboundInstance.access.Unlock()
	close(release)

	require.ErrorIs(t, <-dialResult, errFallbackStale)
	require.Equal(t, int32(1), conn.closed.Load(),
		"the stale connection must be closed exactly once so its session cannot outlive the generation")
}

func TestSingleTransportDialAcceptsAConnectionFromItsOwnGeneration(t *testing.T) {
	conn := &generationConn{}
	client := &fakeClient{dialFunc: func(context.Context, net.Addr) (net.Conn, error) {
		return conn, nil
	}}
	outboundInstance := &Outbound{
		logger:     nopLogger(),
		started:    true,
		generation: 3,
		primary:    fakeRuntimeForTest("primary", client),
	}
	spec, err := toNetAddrSpec(M.ParseSocksaddr("192.0.2.62:80"), N.NetworkTCP)
	require.NoError(t, err)

	dialed, dialErr := outboundInstance.dial(context.Background(), spec)
	require.NoError(t, dialErr)
	require.Same(t, conn, dialed)
	require.Zero(t, conn.closed.Load())
}
