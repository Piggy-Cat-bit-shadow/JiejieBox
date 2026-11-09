package urltest

// Interaction fairness at the entry point the Box really uses: the origin marker a caller already
// carries decides its queue position.
//
// The coordinator decides the order (its own tests pin the policy). What is proved here is that a
// measurement submitted by periodic background work is ADMITTED as background and not as
// interactive, and that a burst of interactive submissions cannot starve it - the property that
// matters when a user presses "test" while a phone's health check is running.

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// orderedDialer records the order in which measurements reach the network.
type orderedDialer struct {
	access sync.Mutex
	order  []string
	labels map[string]string
}

func (d *orderedDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	label, loaded := d.labels[destination.String()]
	if !loaded {
		label = destination.String()
	}
	d.access.Lock()
	d.order = append(d.order, label)
	d.access.Unlock()
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, destination.String())
}

func (d *orderedDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

func (d *orderedDialer) observed() []string {
	d.access.Lock()
	defer d.access.Unlock()
	return append([]string(nil), d.order...)
}

type probeNode struct {
	label  string
	key    MeasurementKey
	server *httptest.Server
}

func newProbeNodes(t *testing.T, labels ...string) []probeNode {
	t.Helper()
	nodes := make([]probeNode, 0, len(labels))
	for _, label := range labels {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(server.Close)
		link := server.URL + "/generate_204"
		nodes = append(nodes, probeNode{
			label:  label,
			server: server,
			key:    MeasurementKey{Node: label, Target: link, Purpose: PurposeHealth},
		})
	}
	return nodes
}

func (n probeNode) link() string { return n.server.URL + "/generate_204" }

func TestAnInteractiveProbeJumpsAQueuedBackgroundOne(t *testing.T) {
	nodes := newProbeNodes(t, "background", "interactive")
	dialer := &orderedDialer{labels: map[string]string{
		nodes[0].server.Listener.Addr().String(): nodes[0].label,
		nodes[1].server.Listener.Addr().String(): nodes[1].label,
	}}

	coordinator := NewCoordinator(1)
	boxCtx := ContextWithMeasurementShare(ContextWithCoordinator(context.Background(), coordinator), NewMeasurementShare())

	// Occupy the only slot, so both measurements below really queue.
	releaseSlot, err := coordinator.Acquire(boxCtx)
	require.NoError(t, err)

	type outcome struct{ err error }
	background := make(chan outcome, 1)
	interactive := make(chan outcome, 1)
	go func() {
		_, err := Measure(adapter.ContextWithBackgroundProbe(boxCtx),
			MeasureOptions{Link: nodes[0].link(), Key: &nodes[0].key}, dialer)
		background <- outcome{err: err}
	}()
	go func() {
		_, err := Measure(boxCtx, MeasureOptions{Link: nodes[1].link(), Key: &nodes[1].key}, dialer)
		interactive <- outcome{err: err}
	}()
	require.Eventually(t, func() bool { return coordinator.Queued() == 2 }, time.Second, time.Millisecond,
		"both measurements must be waiting before the slot is released")

	releaseSlot()
	require.NoError(t, (<-interactive).err)
	require.NoError(t, (<-background).err)
	require.Equal(t, []string{"interactive", "background"}, dialer.observed(),
		"an explicit caller is admitted ahead of queued background work")
	require.Zero(t, coordinator.InFlight())
	require.Zero(t, coordinator.Queued())
}

func TestABackgroundProbeIsAgedInBehindAnInteractiveBurst(t *testing.T) {
	const interactiveCount = 4
	labels := []string{"background"}
	for index := 0; index < interactiveCount; index++ {
		labels = append(labels, "interactive-"+string(rune('a'+index)))
	}
	nodes := newProbeNodes(t, labels...)
	labelsByAddr := make(map[string]string, len(nodes))
	for _, node := range nodes {
		labelsByAddr[node.server.Listener.Addr().String()] = node.label
	}
	dialer := &orderedDialer{labels: labelsByAddr}

	coordinator := NewCoordinator(1)
	boxCtx := ContextWithMeasurementShare(ContextWithCoordinator(context.Background(), coordinator), NewMeasurementShare())
	releaseSlot, err := coordinator.Acquire(boxCtx)
	require.NoError(t, err)

	background := make(chan error, 1)
	go func() {
		_, err := Measure(adapter.ContextWithBackgroundProbe(boxCtx),
			MeasureOptions{Link: nodes[0].link(), Key: &nodes[0].key}, dialer)
		background <- err
	}()
	interactive := make(chan error, interactiveCount)
	for index := 1; index <= interactiveCount; index++ {
		node := nodes[index]
		go func() {
			_, err := Measure(boxCtx, MeasureOptions{Link: node.link(), Key: &node.key}, dialer)
			interactive <- err
		}()
	}
	require.Eventually(t, func() bool { return coordinator.Queued() == interactiveCount+1 }, time.Second, time.Millisecond)

	releaseSlot()
	require.NoError(t, <-background, "background work must still be served")
	for index := 0; index < interactiveCount; index++ {
		require.NoError(t, <-interactive)
	}

	observed := dialer.observed()
	require.Len(t, observed, interactiveCount+1)
	backgroundIndex := -1
	for index, label := range observed {
		if label == "background" {
			backgroundIndex = index
		}
	}
	require.GreaterOrEqual(t, backgroundIndex, 0)
	require.LessOrEqual(t, backgroundIndex, 3,
		"the background probe must be aged in after a bounded number of interactive grants, not starved to the end: %v", observed)
	require.Zero(t, coordinator.InFlight())
	require.Zero(t, coordinator.Queued())
}
