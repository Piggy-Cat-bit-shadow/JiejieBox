package group

// The network-change shape of "old evidence must not overwrite new evidence", driven through the
// real round entry point.
//
// A round that was already probing when the network changed finishes after the round the change
// started. Its result describes the path that existed BEFORE the change, so it must not become the
// last result: the group selects from that layer, and a pre-change delay written over a
// post-change one would make the selection describe a path that no longer exists.
//
// The round is driven directly rather than through a started group because the point is the
// ordering of two overlapping attempts, which a serialised group round would not produce.

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// heldProbeOutbound holds a health probe inside its dial until the test releases it.
type heldProbeOutbound struct {
	adapter.Outbound
	tag string

	started     chan struct{}
	gate        chan struct{}
	once        atomic.Bool
	destination string
}

func newHeldProbeOutbound(tag string, destination string) *heldProbeOutbound {
	return &heldProbeOutbound{tag: tag, destination: destination, started: make(chan struct{}), gate: make(chan struct{})}
}

func (o *heldProbeOutbound) Type() string      { return "held-probe" }
func (o *heldProbeOutbound) Tag() string       { return o.tag }
func (o *heldProbeOutbound) Network() []string { return []string{N.NetworkTCP} }

func (o *heldProbeOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if o.once.CompareAndSwap(false, true) {
		close(o.started)
	}
	select {
	case <-o.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// The stale attempt's connection goes to the SLOW listener, so the delay it would store is
	// unmistakable: the two attempts cannot be told apart by their timestamps alone once the
	// completion clock is the one being written.
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, o.destination)
}

func (o *heldProbeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

func (o *heldProbeOutbound) release() { close(o.gate) }

// directProbeOutbound dials the probe target directly, so it measures the same listener.
type directProbeOutbound struct {
	adapter.Outbound
	tag string
}

func (o *directProbeOutbound) Type() string      { return "direct-probe" }
func (o *directProbeOutbound) Tag() string       { return o.tag }
func (o *directProbeOutbound) Network() []string { return []string{N.NetworkTCP} }

func (o *directProbeOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, destination.String())
}

func (o *directProbeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

func TestASlowerPreChangeAttemptCannotOverwriteTheNewerRoundsResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	link := server.URL + "/generate_204"

	// The stale attempt's own listener answers slowly, so its delay can never be mistaken for the
	// newer attempt's once the surviving entry is read back.
	slowServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(150 * time.Millisecond)
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(slowServer.Close)

	history := urltest.NewHistoryStorage()
	logger := log.NewNOPFactory().NewLogger("ordering")
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{}}
	ctx := context.Background()

	// The attempt that was already running when the path changed.
	stale := newHeldProbeOutbound("node-a", slowServer.Listener.Addr().String())
	staleRound := make(chan map[string]uint16, 1)
	go func() {
		staleRound <- URLTestOutboundsWithTarget(ctx, manager, history, logger,
			[]adapter.Outbound{stale}, link, urltest.ExpectedStatus{}, 0, true, TestHistoryHealth)
	}()
	select {
	case <-stale.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the stale attempt never dialled")
	}

	// The replacement object carries the SAME tag, so both attempts write to one history entry -
	// which is exactly what a same-tag member replacement or a network change produces.
	beforeFresh := time.Now()
	fresh := URLTestOutboundsWithTarget(ctx, manager, history, logger,
		[]adapter.Outbound{&directProbeOutbound{tag: "node-a"}}, link, urltest.ExpectedStatus{}, 0, true, TestHistoryHealth)
	require.Len(t, fresh, 1, "the newer round measured the member")

	// The stale attempt finishes last and must lose.
	stale.release()
	select {
	case <-staleRound:
	case <-time.After(10 * time.Second):
		t.Fatal("the stale attempt never finished")
	}

	stored := history.LoadURLTestHistory("node-a")
	require.NotNil(t, stored)
	require.Less(t, stored.Delay, uint16(100),
		"the stored result must come from the attempt that STARTED after the change; a slow pre-change attempt finishing last must not replace it")
	require.False(t, stored.Time.Before(beforeFresh),
		"the surviving entry was started by the newer attempt")
}
