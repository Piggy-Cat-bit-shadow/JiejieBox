//go:build with_clash_api

package libbox

// The reviewed snapshot-boundary failure: the daemon subscribes, starts building the Reset view, and
// events published while that view is being serialized are dropped. The old envelope read the live
// counter when it wrapped the Reset, so the first baseline said "136 lost, already accounted for" -
// but those 136 events were not in the snapshot. The next batch carried the same 136, the client saw
// no rise, and the table stayed at 33 rows while the manager held 101.
//
// The fix: a baseline carries 0; drops it cannot prove stay on the per-subscription counter and are
// announced by the next batch or the idle tick. These tests use the production daemon, manager, gRPC
// stream, client and table; the barrier is only a Rule.String() call in the real snapshot encoder.

import (
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/daemon"
	routeRule "github.com/sagernet/sing-box/route/rule"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// snapshotBarrierRule pauses the Nth Rule.String() call, i.e. inside the production snapshot encoder.
type snapshotBarrierRule struct {
	adapter.Rule
	pauseAt int64
	armed   atomic.Bool
	calls   atomic.Int64
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newSnapshotBarrierRule(pauseAt int64) *snapshotBarrierRule {
	rule := &snapshotBarrierRule{
		pauseAt: pauseAt,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	rule.armed.Store(true)
	return rule
}

// arm makes the NEXT String call the barrier's first call.
func (r *snapshotBarrierRule) arm() {
	r.calls.Store(0)
	r.armed.Store(true)
}

func (r *snapshotBarrierRule) String() string {
	if r.armed.Load() && r.calls.Add(1) == r.pauseAt {
		r.once.Do(func() { close(r.entered) })
		<-r.release
	}
	return "snapshot-barrier"
}

// armingServiceServer delegates every RPC and arms the rule when the SECOND subscription is created -
// the client's repair. The snapshot build follows immediately in the same goroutine, so the next
// Rule.String call is the recovery snapshot's first record, not an event-path encode.
type armingServiceServer struct {
	daemon.StartedServiceServer
	rule  *snapshotBarrierRule
	count atomic.Int64
}

func (s *armingServiceServer) SubscribeConnections(request *daemon.SubscribeConnectionsRequest, stream daemon.StartedService_SubscribeConnectionsServer) error {
	if s.count.Add(1) == 2 {
		s.rule.arm()
	}
	return s.StartedServiceServer.SubscribeConnections(request, stream)
}

func (r *snapshotBarrierRule) releasePause() {
	select {
	case <-r.release:
	default:
		close(r.release)
	}
}

func waitSnapshotBarrier(t *testing.T, rule *snapshotBarrierRule) {
	t.Helper()
	select {
	case <-rule.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the production snapshot encoder was never reached")
	}
}

func closedRowIDs(t *testing.T, connections *Connections) []string {
	t.Helper()
	ids, _ := rowsByState(connections, ConnectionStateClosed)
	return ids
}

// TestALossWhileTheInitialSnapshotIsBuiltIsRecovered covers the FIRST connect.
func TestALossWhileTheInitialSnapshotIsBuiltIsRecovered(t *testing.T) {
	service, manager := startSilentTailDaemon(t)
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, service)
	})
	outbound := testOutbound{}
	metadata := adapter.InboundContext{OutboundChain: []adapter.Outbound{outbound}}

	rule := newSnapshotBarrierRule(1)
	left, right := net.Pipe()
	anchor := manager.RoutedConnection(context.Background(), left, metadata, rule, outbound)
	require.NoError(t, anchor.Close())
	_ = right.Close()

	connections := NewConnections()
	handler := newPausingHandler(connections)
	handler.releaseFirst()
	options := &CommandClientOptions{}
	options.AddCommand(CommandConnections)
	client := NewCommandClient(handler, options)
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	waitSnapshotBarrier(t, rule)
	// The initial ClosedConnections slice has been captured; only its encoding is paused.
	for index := 0; index < 100; index++ {
		trackAndCloseLibboxTestConnection(t, manager, outbound, metadata)
	}
	require.Len(t, manager.ClosedConnections(), 101)
	rule.releasePause()

	require.Eventually(t, func() bool {
		return len(closedRowIDs(t, connections)) == len(manager.ClosedConnections())
	}, 10*time.Second, 5*time.Millisecond,
		"the loss outside the captured snapshot must be recovered in silence")

	batches := handler.snapshot()
	require.NotEmpty(t, batches)
	require.Zero(t, batches[0].Dropped(), "a baseline must not adopt the counter it cannot prove")
	var repaired int
	for _, batch := range batches {
		if batch.Resynced {
			repaired++
		}
	}
	require.Equal(t, 1, repaired, "exactly one recovery snapshot")
	require.Len(t, closedRowIDs(t, connections), 101)
}

// TestALossWhileTheRecoverySnapshotIsBuiltIsRecovered covers the SUBSCRIBE-AFTER-LOSS path: the first
// snapshot is complete for its view, the client repairs, and the repair snapshot itself is built while
// more connections close. Its baseline must not swallow that second loss either.
func TestALossWhileTheRecoverySnapshotIsBuiltIsRecovered(t *testing.T) {
	service, manager := startSilentTailDaemon(t)
	outbound := testOutbound{}
	metadata := adapter.InboundContext{OutboundChain: []adapter.Outbound{outbound}}

	// The anchor pauses the INITIAL snapshot. The records created during that pause use a rule that is
	// disarmed until the client's repair subscription starts, so the recovery snapshot is the first
	// encoder to reach it.
	anchorRule := newSnapshotBarrierRule(1)
	recordRule := newSnapshotBarrierRule(1)
	recordRule.armed.Store(false)
	server := &armingServiceServer{StartedServiceServer: service, rule: recordRule}
	address := bridgeTestServer(t, func(grpcServer *grpc.Server) {
		daemon.RegisterStartedServiceServer(grpcServer, server)
	})

	left, right := net.Pipe()
	anchor := manager.RoutedConnection(context.Background(), left, metadata, anchorRule, outbound)
	require.NoError(t, anchor.Close())
	_ = right.Close()

	connections := NewConnections()
	handler := newPausingHandler(connections)
	handler.releaseFirst()
	options := &CommandClientOptions{}
	options.AddCommand(CommandConnections)
	client := NewCommandClient(handler, options)
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	waitSnapshotBarrier(t, anchorRule)
	for index := 0; index < 100; index++ {
		leftRecord, rightRecord := net.Pipe()
		record := manager.RoutedConnection(context.Background(), leftRecord, metadata, recordRule, outbound)
		require.NoError(t, record.Close())
		_ = rightRecord.Close()
	}
	anchorRule.releasePause()

	// The client repairs; the wrapper arms the rule before the real service builds the new snapshot.
	waitSnapshotBarrier(t, recordRule)
	// The recovery snapshot captured 101 records before this pause; these 50 are outside its view.
	for index := 0; index < 50; index++ {
		leftRecord, rightRecord := net.Pipe()
		record := manager.RoutedConnection(context.Background(), leftRecord, metadata, recordRule, outbound)
		require.NoError(t, record.Close())
		_ = rightRecord.Close()
	}
	require.Len(t, manager.ClosedConnections(), 151)
	recordRule.releasePause()

	require.Eventually(t, func() bool {
		return len(closedRowIDs(t, connections)) == len(manager.ClosedConnections())
	}, 15*time.Second, 5*time.Millisecond,
		"a loss during the recovery snapshot must be recovered too")

	var repaired int
	for _, batch := range handler.snapshot() {
		t.Logf("recovery wire sequence=%d reset=%t dropped=%d resynced=%t events=%d", batch.Sequence(), batch.Reset, batch.Dropped(), batch.Resynced, len(batch.events))
		if batch.Resynced {
			repaired++
		}
	}
	require.GreaterOrEqual(t, repaired, 2, "the repair snapshot itself must be repaired")
	require.Len(t, closedRowIDs(t, connections), 151)
}

// descriptionRule exposes a production NewDomainRegexItem description: ~5 KiB per record, the size
// that made a 1,000-record Reset ~5.4 MiB and exceeded the default 4 MiB receive limit.
type descriptionRule struct {
	adapter.Rule
	item *routeRule.DomainRegexItem
}

func (r descriptionRule) String() string { return r.item.String() }

// TestAThousandFiveKiBRulesArriveOverTheDefaultGRPCClient is the capacity half of the snapshot work:
// the real Box, the real manager's 1,000-record retention window, ~5 KiB production rule
// descriptions, the real gRPC server and the real client dial options. The snapshot is fragmented
// under the default limit and the consumer receives every retained ID.
func TestAThousandFiveKiBRulesArriveOverTheDefaultGRPCClient(t *testing.T) {
	service, manager := startSilentTailDaemon(t)
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, service)
	})
	outbound := testOutbound{}
	metadata := adapter.InboundContext{OutboundChain: []adapter.Outbound{outbound}}
	expressions := "^(" + strings.Repeat("subdomain[0-9]+\\.example\\.org|", 175) + "example\\.org)$"
	item, err := routeRule.NewDomainRegexItem([]string{expressions})
	require.NoError(t, err)
	rule := descriptionRule{item: item}
	for index := 0; index < 1000; index++ {
		left, right := net.Pipe()
		connection := manager.RoutedConnection(context.Background(), left, metadata, rule, outbound)
		require.NoError(t, connection.Close())
		_ = right.Close()
	}
	require.Len(t, manager.ClosedConnections(), 1000)

	connections := NewConnections()
	handler := &countingHandler{connections: connections}
	options := &CommandClientOptions{}
	options.AddCommand(CommandConnections)
	client := NewCommandClient(handler, options)
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	require.Eventually(t, func() bool {
		closedIDs, _ := rowsByState(connections, ConnectionStateClosed)
		return len(closedIDs) == 1000
	}, 15*time.Second, 5*time.Millisecond, "every retained record must arrive over the default gRPC limit")
	require.Zero(t, handler.disconnected.Load(), "a valid retained window must not exhaust the client")
}
