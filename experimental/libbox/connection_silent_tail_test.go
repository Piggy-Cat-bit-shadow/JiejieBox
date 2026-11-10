//go:build with_clash_api

package libbox

// The full production chain for the silent-tail recovery: a real Box (its traffic manager exists only
// when the Clash API is configured), the real daemon.StartedService, real gRPC, the real CommandClient
// and the real Connections table. The handler is paused inside the first delivery, traffic is created
// while the daemon's send is blocked, and after release the consumer must converge to the manager's
// retained closed window.
//
// The build tag is not incidental: box.New installs the traffic manager only for the Clash API / API
// service / platform log writer, and the Clash API is a tagged build. The untagged suite still covers
// the daemon-side silent tail (daemon package) and the client heartbeat (libbox), which are the two
// halves of this chain that do not need a Box.

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxprovider "github.com/sagernet/sing-box/adapter/provider"
	boxservice "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/local"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// pausingHandler pauses the FIRST batch inside WriteConnectionEvents, so the client stream goroutine
// stops reading while the test creates traffic. That is what makes the daemon's send block and its
// per-subscription queue overflow.
type pausingHandler struct {
	connections *Connections

	access  sync.Mutex
	batches []*ConnectionEvents

	firstBatch  chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

func newPausingHandler(connections *Connections) *pausingHandler {
	return &pausingHandler{
		connections: connections,
		firstBatch:  make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (h *pausingHandler) WriteConnectionEvents(events *ConnectionEvents) {
	h.access.Lock()
	first := len(h.batches) == 0
	h.batches = append(h.batches, events)
	h.access.Unlock()
	if first {
		close(h.firstBatch)
		<-h.release
	}
	h.connections.ApplyEvents(events)
}

func (h *pausingHandler) releaseFirst() { h.releaseOnce.Do(func() { close(h.release) }) }

func (h *pausingHandler) snapshot() []*ConnectionEvents {
	h.access.Lock()
	defer h.access.Unlock()
	return append([]*ConnectionEvents(nil), h.batches...)
}

func (h *pausingHandler) Connected()                        {}
func (h *pausingHandler) Disconnected(string)               {}
func (h *pausingHandler) SetDefaultLogLevel(int32)          {}
func (h *pausingHandler) ClearLogs()                        {}
func (h *pausingHandler) WriteLogs(LogIterator)             {}
func (h *pausingHandler) WriteStatus(*StatusMessage)        {}
func (h *pausingHandler) WriteGroups(OutboundGroupIterator) {}
func (h *pausingHandler) WriteOutbounds(OutboundGroupItemIterator) {
}
func (h *pausingHandler) InitializeClashMode(StringIterator, string) {}
func (h *pausingHandler) UpdateClashMode(string)                     {}

// testOutbound is only used as the chain's identity; nothing dials through it.
type testOutbound struct{}

func (testOutbound) Type() string           { return "direct" }
func (testOutbound) Tag() string            { return "direct" }
func (testOutbound) Network() []string      { return []string{"tcp", "udp"} }
func (testOutbound) Dependencies() []string { return nil }
func (testOutbound) Close() error           { return nil }
func (testOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, net.ErrClosed
}
func (testOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// startSilentTailDaemon starts a REAL StartedService with a REAL Box whose traffic manager tracks
// connections. The Clash API option is what makes box.New install the traffic manager service.
func startSilentTailDaemon(t *testing.T) (*daemon.StartedService, *trafficcontrol.Manager) {
	t.Helper()
	workingPath, err := os.MkdirTemp("", "sing-box-silent-tail-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(workingPath) })
	dnsRegistry := dns.NewTransportRegistry()
	local.RegisterTransport(dnsRegistry)
	boxContext := box.Context(context.Background(),
		inbound.NewRegistry(),
		outbound.NewRegistry(),
		boxprovider.NewRegistry(),
		endpoint.NewRegistry(),
		dnsRegistry,
		boxservice.NewRegistry(),
		certificate.NewRegistry(),
	)
	boxContext = filemanager.WithDefault(boxContext, workingPath, workingPath, -1, -1)
	service := daemon.NewStartedService(daemon.ServiceOptions{Context: boxContext})
	t.Cleanup(service.Close)
	require.NoError(t, service.StartOrReloadService(context.Background(),
		`{"log":{"level":"error"},"experimental":{"clash_api":{}}}`, nil))
	require.NotNil(t, service.Instance())
	manager := service.Instance().TrafficManager()
	require.NotNil(t, manager, "the traffic manager must exist for the connection stream")
	return service, manager
}

func trackAndCloseLibboxTestConnection(t *testing.T, manager *trafficcontrol.Manager, outbound adapter.Outbound, metadata adapter.InboundContext) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	tracked := manager.RoutedConnection(context.Background(), clientConn, metadata, nil, outbound)
	require.NoError(t, tracked.Close())
	_ = serverConn.Close()
}

// TestTheFullChainRecoversFromASilentTailOverRealGRPC is the strongest form of the gap-A evidence:
// production StartedService on a real Box, real trafficcontrol.Manager, real gRPC, the real
// CommandClient and the real Connections table.
func TestTheFullChainRecoversFromASilentTailOverRealGRPC(t *testing.T) {
	service, manager := startSilentTailDaemon(t)
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, service)
	})

	connections := NewConnections()
	handler := newPausingHandler(connections)
	clientOptions := &CommandClientOptions{}
	clientOptions.AddCommand(CommandConnections)
	client := NewCommandClient(handler, clientOptions)
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	select {
	case <-handler.firstBatch:
	case <-time.After(10 * time.Second):
		t.Fatal("the client never received the baseline")
	}

	// Inflate each event so ONE drained batch exceeds the 64 KiB stream window: the daemon's send
	// then blocks deterministically and the events that arrive meanwhile overflow its
	// per-subscription queue. Without this the transport can absorb a small burst and no loss occurs,
	// which would make the test vacuous rather than failing.
	outbound := testOutbound{}
	connectionMetadata := adapter.InboundContext{
		OutboundChain: []adapter.Outbound{outbound},
		Domain:        strings.Repeat("silent-tail.", 256),
	}
	const connectionsCreated = 300
	for index := 0; index < connectionsCreated; index++ {
		trackAndCloseLibboxTestConnection(t, manager, outbound, connectionMetadata)
	}

	require.Len(t, manager.ClosedConnections(), connectionsCreated,
		"every created connection must be in the manager's closed list before the release")

	handler.releaseFirst()

	require.Eventually(t, func() bool {
		closedIDs, _ := rowsByState(connections, ConnectionStateClosed)
		activeIDs, _ := rowsByState(connections, ConnectionStateActive)
		return len(activeIDs) == 0 && len(closedIDs) == len(manager.ClosedConnections())
	}, 20*time.Second, 10*time.Millisecond, "the consumer must converge to the daemon's retained window")

	closedIDs, _ := rowsByState(connections, ConnectionStateClosed)
	retained := manager.ClosedConnections()
	require.NotEmpty(t, retained)
	retainedIDs := make(map[string]struct{}, len(retained))
	for _, metadata := range retained {
		retainedIDs[metadata.ID.String()] = struct{}{}
	}
	for _, id := range closedIDs {
		require.Contains(t, retainedIDs, id, "every consumer row must exist in the retained window")
	}

	var repaired bool
	for _, batch := range handler.snapshot() {
		if batch.Resynced {
			repaired = true
		}
	}
	require.True(t, repaired, "the recovery snapshot must be delivered with the resync marker")
}
