//go:build with_quic

package v2rayquic

// Pool hand-out across a network generation change (audit F-15 / Q12).
//
// The transport pools ONE QUIC connection. The network manager claims a new generation at the
// start of a transition and only then runs the listener phase that calls Reset; a demand arriving
// in between used to join the connection built for the network that just ended. These tests drive
// the production client against a real QUIC endpoint and a real coordinator, and they pin the three
// boundaries the prompt asks for:
//
//	an IDLE generation is not handed out: it is detached and closed, the demand dials the new one;
//	a BUSY generation is not handed out either, but it is drained rather than killed - its open
//	stream keeps carrying acknowledged bytes and closes itself when the last one ends;
//	a dial that SPANS the change is discarded instead of being published as the new generation.
//
// The negative control at the bottom proves the guard is inert without a coordinator, which is the
// shape a transport built by an embedder or by one of the older tests has.

import (
	"context"
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

// newGenerationClient builds the production client with a runtime coordinator in its context.
func newGenerationClient(t *testing.T, server *migrationServer, dialer N.Dialer, coordinator *runtimecoord.Coordinator) *Client {
	t.Helper()
	ctx := context.Background()
	if coordinator != nil {
		ctx = service.ContextWith[*runtimecoord.Coordinator](ctx, coordinator)
	}
	logger := log.NewNOPFactory().NewLogger("v2rayquic-generation")
	tlsConfig, err := tls.NewClient(context.Background(), logger, "localhost", option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: "localhost",
		Insecure:   true,
	})
	require.NoError(t, err)
	transport, err := NewClient(ctx, dialer, M.SocksaddrFromNet(server.addr), option.V2RayQUICOptions{}, tlsConfig)
	require.NoError(t, err)
	client, isClient := transport.(*Client)
	require.True(t, isClient, "NewClient must build the package client, got %T", transport)
	if coordinator != nil {
		require.NotNil(t, client.registration, "a client built with a coordinator must carry a registration")
	}
	return client
}

// waitSocketClosed waits for the socket's close, which happens when the connection that owns it
// ends: the close travels through the path-cleanup task rather than being synchronous with the
// retirement decision.
func waitSocketClosed(t *testing.T, socket *recordingConn) {
	t.Helper()
	require.Eventually(t, func() bool { return socket.closes.Load() == 1 },
		migrationSettle, time.Millisecond, "the retired generation's socket was never closed exactly once")
}

// TestAnIdleGenerationIsNotHandedToTheNextDemand is the case the audit named.
func TestAnIdleGenerationIsNotHandedToTheNextDemand(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}}
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	client := newGenerationClient(t, server, dialer, coordinator)
	t.Cleanup(func() { _ = client.Close() })

	first := openStream(t, client)
	sendRecord(t, first, 0, "before-the-change")
	require.NoError(t, first.Close())
	require.Equal(t, 1, dialer.count(), "the first demand dialled one generation")

	// The network changed: the generation is claimed before any listener runs. This is the exact
	// state a demand can arrive in.
	coordinator.Advance(1)

	second := openStream(t, client)
	sendRecord(t, second, 0, "after-the-change")

	require.Equal(t, 2, dialer.count(),
		"the demand after the change reused the pooled connection of the previous generation")
	require.Equal(t, 2, int(server.handshakes.Load()),
		"the endpoint saw one handshake: the old generation was handed out again")
	waitSocketClosed(t, dialer.socket(0))
}

// TestABusyGenerationDrainsInsteadOfBeingKilled is the other half: the old generation is not lent
// to new work, and it is not destroyed under the flow already running on it.
func TestABusyGenerationDrainsInsteadOfBeingKilled(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}}
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	client := newGenerationClient(t, server, dialer, coordinator)
	t.Cleanup(func() { _ = client.Close() })

	first := openStream(t, client)
	sendRecord(t, first, 0, "before-the-change")

	coordinator.Advance(1)

	second := openStream(t, client)
	sendRecord(t, second, 0, "after-the-change")
	require.Equal(t, 2, dialer.count(),
		"new work was multiplexed onto the previous generation's connection")
	require.EqualValues(t, 0, dialer.socket(0).closes.Load(),
		"the previous generation was closed under a stream that was still running")

	// The flow on the retired generation is still a flow: it keeps carrying acknowledged bytes
	// after the change, and only its own end releases the generation.
	sendRecord(t, first, 1, "still-running-on-the-old-generation")
	require.NoError(t, first.Close())
	waitSocketClosed(t, dialer.socket(0))
}

// TestADialThatSpansTheChangeIsNotPublished is the late-result boundary: a connection whose
// handshake completes after the generation moved was established on the path that just ended, so
// it is released instead of published, and the next demand starts cleanly.
func TestADialThatSpansTheChangeIsNotPublished(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}}
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	client := newGenerationClient(t, server, dialer, coordinator)
	t.Cleanup(func() { _ = client.Close() })

	// One dial, with the generation changing while the handshake is in flight. The hook runs
	// inside the client's own creation path, which is the only place that can order the two events
	// deterministically: the generation is read before it and compared after it.
	realDial := client.dialConn
	client.dialConn = func(ctx context.Context) (*clientConnection, error) {
		coordinator.Advance(1)
		return realDial(ctx)
	}
	ctx, cancel := context.WithTimeout(context.Background(), migrationSettle)
	defer cancel()
	stream, err := client.DialContext(ctx)
	require.Error(t, err, "a connection established across a generation change must not be published")
	require.Contains(t, err.Error(), "network changed while the connection was being established")
	require.Nil(t, stream)
	require.Equal(t, 1, dialer.count(), "the spanning dial created exactly one socket")
	waitSocketClosed(t, dialer.socket(0))

	// The next demand starts entirely on the current generation and works.
	client.dialConn = realDial
	recovered := openStream(t, client)
	sendRecord(t, recovered, 0, "after-the-change")
	require.Equal(t, 2, dialer.count())
	require.EqualValues(t, 1, server.streamCount.Load(),
		"the discarded connection must not have opened a stream")
}

// TestWithoutACoordinatorThePoolBehavesAsBefore is the negative control: no coordinator means no
// generation to compare, so a second demand joins the pooled connection instead of dialing.
func TestWithoutACoordinatorThePoolBehavesAsBefore(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}}
	client := newGenerationClient(t, server, dialer, nil)
	t.Cleanup(func() { _ = client.Close() })
	require.Nil(t, client.registration)

	first := openStream(t, client)
	sendRecord(t, first, 0, "first")
	require.NoError(t, first.Close())

	second := openStream(t, client)
	sendRecord(t, second, 0, "second")
	require.Equal(t, 1, dialer.count(),
		"without a coordinator the pool must keep its historical reuse behaviour")
	require.Equal(t, 1, int(server.handshakes.Load()))
}
