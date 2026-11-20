package hysteria2

// Q02's fourth arm (audit quadruple A): a dial that is IN FLIGHT when the platform reports an
// interface change.
//
// The three systems that are easy to test independently are the platform event, the generation gate and
// the carrier's reset; the fourth is the demand that is already inside the QUIC handshake when the
// reset runs. Its contract is not "the dial succeeds" - the path is gone - but "it fails as a network
// change, promptly, and it does not outlive the owner". A dial that keeps waiting on a dead path after
// the reset turns one interface flap into a user-visible stall of the handshake timeout.
//
// MEASURED at this HEAD: the dial returns in ~3 ms with the reset's own "network changed" error. The
// pinned sing-quic client's CloseWithError reaches pending offers (the P9 shape - an in-flight
// creation surviving its owner - does not occur here), so the four-way arm holds without any change to
// the carrier.
//
// # The test error this file hit first, recorded because it is the interesting trap
//
// The first version notified the listener WITHOUT advancing the coordinator's generation. The gate
// correctly treated that as a duplicate of the generation it had already applied, ran no reset at all,
// and the dial then outlived the notification by the full handshake timeout. That looked like the four-
// way defect this test exists to catch, and it was entirely the harness's fault - the production
// sequence is "the coordinator publishes the new generation, then the listener notifies" (the same
// order window B's gate tests use). The assertion is therefore only valid with the advance in place,
// and the failing shape without it is a property of the gate (a duplicate must not run a destructive
// action a second time), not of this arm.
//
// The path is simulated with a UDP socket that reads and never answers: it neither completes the
// handshake nor refuses it, which is what a path that has gone away looks like from here.

import (
	"context"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

func newInFlightSubject(t *testing.T, coordinator *runtimecoord.Coordinator, serverPort uint16) *Outbound {
	t.Helper()
	ctx := service.ContextWith[*runtimecoord.Coordinator](context.Background(), coordinator)
	created, err := NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("in-flight"), "hysteria2-in-flight",
		option.Hysteria2OutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: serverPort},
			Password:      "in-flight-password",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{Enabled: true, ServerName: "localhost", Insecure: true},
			},
		})
	require.NoError(t, err)
	outbound, isOutbound := created.(*Outbound)
	require.True(t, isOutbound)
	t.Cleanup(func() { _ = outbound.client.CloseWithError(net.ErrClosed) })
	t.Cleanup(func() { outbound.generation.Close() })
	return outbound
}

func TestANetworkChangeAbortsADialStuckOnTheDeadPath(t *testing.T) {
	blackhole, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer blackhole.Close()
	firstPacket := make(chan struct{})
	var once bool
	go func() {
		buffer := make([]byte, 4096)
		for {
			_, _, readErr := blackhole.ReadFrom(buffer)
			if readErr != nil {
				return
			}
			if !once {
				once = true
				close(firstPacket)
			}
		}
	}()
	serverPort := uint16(blackhole.LocalAddr().(*net.UDPAddr).Port)

	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	outbound := newInFlightSubject(t, coordinator, serverPort)

	dialDone := make(chan error, 1)
	started := time.Now()
	go func() {
		_, dialErr := outbound.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddrHostPort("example.com", 80))
		dialDone <- dialErr
	}()

	// The handshake is now on the wire; the path is dead but has not refused anything.
	select {
	case <-firstPacket:
	case <-time.After(5 * time.Second):
		t.Fatal("the dial never reached the wire, so there is no in-flight handshake to abort")
	}

	// The platform reports the interface change: the coordinator publishes the new generation first,
	// then the listener notifies. Without the advance the gate correctly treats the notification as a
	// duplicate for the generation it already applied and runs no reset at all.
	coordinator.Advance(1)
	outbound.InterfaceUpdated(context.Background())

	select {
	case dialErr := <-dialDone:
		require.Error(t, dialErr, "a dial on a path that has gone away must not succeed")
		t.Logf("the in-flight dial returned %v after the network change: %v", time.Since(started), dialErr)
	case <-time.After(5 * time.Second):
		stacks := make([]byte, 1<<20)
		n := runtime.Stack(stacks, true)
		t.Fatalf("the in-flight dial outlived the network change: the reset closed the carrier but the "+
			"demand inside the handshake kept waiting on the dead path\n%s", stacks[:n])
	}
}
