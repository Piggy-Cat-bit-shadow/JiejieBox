//go:build with_quic

package masque

// The phase-cost ledger for a non-system MASQUE client endpoint (task C1 step 1).
//
// What is measured, and why these five phases:
//
//	p0-baseline   the fixture's own cost, so every later number is a delta from it
//	p1-constructed NewClientEndpoint, nothing started - what an UNUSED node costs
//	p2-initialized StartStateInitialize: this is where device.New builds the memory TUN and
//	              the userspace Go stack
//	p3-configured  the first server configuration: device.UpdateConfiguration + device.Start
//	p4-firstdata    one dial and one UDP socket through the inner stack
//	p5-idle        after every flow is closed, five seconds later
//
// The question this exists to answer is whether the stack cost sits in Initialize (where the
// current code pays it for every configured node) or somewhere later. The design follows the
// answer, so it is measured before anything is changed.
//
// Forced GC is used for attribution only; nothing here runs on a product path.
//
// The test is gated by JJB_COST=1 because it forces two full GCs per phase and is not a
// regression gate. JJB_COST_OUT names a JSONL file to append to; JJB_COST_PROFILE_DIR, when
// set, receives heap profiles at p2 and p3.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	transportmasque "github.com/sagernet/sing-box/transport/masque"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

type phaseCost struct {
	Phase       string  `json:"phase"`
	WallMs      float64 `json:"wall_ms"`
	HeapAlloc   uint64  `json:"heap_alloc"`
	HeapObjects uint64  `json:"heap_objects"`
	TotalAlloc  uint64  `json:"total_alloc"`
	HeapInuse   uint64  `json:"heap_inuse"`
	HeapSys     uint64  `json:"heap_sys"`
	StackInuse  uint64  `json:"stack_inuse"`
	Goroutines  int     `json:"goroutines"`
}

func costPhase(t *testing.T, recorder *[]phaseCost, phase string, started time.Time, profileDir string) {
	t.Helper()
	wall := time.Since(started)
	// Two collections: a forced GC is not guaranteed to be the one that retires the phase's
	// floating garbage, and a single one can leave a delta that belongs to the PREVIOUS phase.
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	*recorder = append(*recorder, phaseCost{
		Phase:       phase,
		WallMs:      float64(wall.Microseconds()) / 1000,
		HeapAlloc:   stats.HeapAlloc,
		HeapObjects: stats.HeapObjects,
		TotalAlloc:  stats.TotalAlloc,
		HeapInuse:   stats.HeapInuse,
		HeapSys:     stats.HeapSys,
		StackInuse:  stats.StackInuse,
		Goroutines:  runtime.NumGoroutine(),
	})
	if profileDir != "" {
		path := filepath.Join(profileDir, "R-heap-"+phase+".pprof")
		file, err := os.Create(path)
		require.NoError(t, err)
		require.NoError(t, pprof.WriteHeapProfile(file))
		require.NoError(t, file.Close())
	}
}

// silentPacketWriter is the inner stack's outbound sink for this measurement: there is no tunnel
// session, so the buffers are released exactly as the production writer would release them when no
// session is ready.
func silentPacketWriter(packetBuffers []*buf.Buffer) error {
	buf.ReleaseMulti(packetBuffers)
	return nil
}

func TestEndpointPhaseCost(t *testing.T) {
	if os.Getenv("JJB_COST") != "1" {
		t.Skip("set JJB_COST=1 to run the phase-cost measurement")
	}
	profileDir := os.Getenv("JJB_COST_PROFILE_DIR")

	var recorded []phaseCost
	started := time.Now()
	logger := log.NewNOPFactory().Logger()
	clientOptions := option.MASQUEClientEndpointOptions{
		ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: 1},
		Version:       3,
		Path:          "/",
		Username:      "cost",
		Password:      "cost-password",
	}
	clientOptions.TLS = &option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: "example.test",
		Insecure:   true,
	}
	clientContext := service.ContextWithDefaultRegistry(context.Background())
	clientContext = service.ContextWith[adapter.NetworkManager](clientContext, &stubNetworkManager{})
	costPhase(t, &recorded, "p0-baseline", started, "")

	started = time.Now()
	created, err := NewClientEndpoint(clientContext, nil, logger, "cost-client", clientOptions)
	require.NoError(t, err)
	clientEndpoint := created.(*ClientEndpoint)
	costPhase(t, &recorded, "p1-constructed", started, "")

	scope := adapter.NewScope(context.Background(), logger)
	started = time.Now()
	require.NoError(t, clientEndpoint.Start(adapter.StartStateInitialize, scope))
	costPhase(t, &recorded, "p2-initialized", started, profileDir)
	require.NotNil(t, clientEndpoint.device)

	// The packet writer is normally installed by StartStateInitialize itself; overriding it keeps
	// any outbound byte from reaching the (absent) tunnel session during the measurement.
	clientEndpoint.device.SetPacketWriter(silentPacketWriter)

	started = time.Now()
	require.NoError(t, clientEndpoint.UpdateConfiguration(transportmasque.Configuration{
		Address: []netip.Prefix{netip.MustParsePrefix("10.42.0.2/32")},
	}))
	costPhase(t, &recorded, "p3-configured", started, profileDir)

	started = time.Now()
	dialCtx, cancelDial := context.WithTimeout(context.Background(), 300*time.Millisecond)
	dialConn, dialErr := clientEndpoint.device.DialContext(dialCtx, N.NetworkTCP,
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 9))
	cancelDial()
	if dialConn != nil {
		_ = dialConn.Close()
	}
	// A dial through a userspace stack without a peer is EXPECTED to fail; the measurement is the
	// cost of reaching that failure, not its success.
	t.Logf("first TCP dial through the inner stack returned err=%v", dialErr)
	listenCtx, cancelListen := context.WithTimeout(context.Background(), 300*time.Millisecond)
	packetConn, listenErr := clientEndpoint.device.ListenPacket(listenCtx,
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 53))
	cancelListen()
	if packetConn != nil {
		_ = packetConn.Close()
	}
	t.Logf("first UDP socket through the inner stack returned err=%v", listenErr)
	costPhase(t, &recorded, "p4-firstdata", started, profileDir)

	// Idle: everything the measurement opened is closed, and the stack had five seconds to run its
	// own timeouts and release what it holds.
	time.Sleep(5 * time.Second)
	costPhase(t, &recorded, "p5-idle", started, "")

	_ = scope.Close()

	writeCostSamples(t, recorded)
	for _, sample := range recorded {
		t.Logf("%-16s wall=%8.2fms heap=%9d objs=%8d total=%10d goroutines=%d",
			sample.Phase, sample.WallMs, sample.HeapAlloc, sample.HeapObjects,
			sample.TotalAlloc, sample.Goroutines)
	}
}

// TestEndpointScaleCost measures the same endpoint at N nodes in ONE process, which is the shape
// the question actually has: many configured-but-unused nodes.
//
// It exists because a single-node delta can be dominated by one-off allocations, and because the
// per-node slope is what decides whether deferring construction is worth its semantic cost. The
// release phase is measured too: an endpoint that is closed must give the memory back.
func TestEndpointScaleCost(t *testing.T) {
	if os.Getenv("JJB_COST") != "1" {
		t.Skip("set JJB_COST=1 to run the phase-cost measurement")
	}
	nodeCount := 100
	if raw := os.Getenv("JJB_COST_NODES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		require.NoError(t, err)
		nodeCount = parsed
	}
	var recorded []phaseCost
	logger := log.NewNOPFactory().Logger()
	clientContext := service.ContextWithDefaultRegistry(context.Background())
	clientContext = service.ContextWith[adapter.NetworkManager](clientContext, &stubNetworkManager{})
	started := time.Now()
	costPhase(t, &recorded, "n0-baseline", started, "")

	endpoints := make([]*ClientEndpoint, 0, nodeCount)
	scopes := make([]*adapter.Scope, 0, nodeCount)
	started = time.Now()
	for index := 0; index < nodeCount; index++ {
		options := option.MASQUEClientEndpointOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: 1},
			Version:       3,
			Path:          "/",
			Username:      "cost",
			Password:      "cost-password",
		}
		options.TLS = &option.OutboundTLSOptions{Enabled: true, ServerName: "example.test", Insecure: true}
		created, err := NewClientEndpoint(clientContext, nil, logger, fmt.Sprintf("cost-%d", index), options)
		require.NoError(t, err)
		endpoints = append(endpoints, created.(*ClientEndpoint))
	}
	costPhase(t, &recorded, "n1-constructed", started, "")

	started = time.Now()
	for _, endpoint := range endpoints {
		scope := adapter.NewScope(context.Background(), logger)
		require.NoError(t, endpoint.Start(adapter.StartStateInitialize, scope))
		endpoint.device.SetPacketWriter(silentPacketWriter)
		scopes = append(scopes, scope)
	}
	costPhase(t, &recorded, "n2-initialized", started, "")

	started = time.Now()
	for index, endpoint := range endpoints {
		require.NoError(t, endpoint.UpdateConfiguration(transportmasque.Configuration{
			Address: []netip.Prefix{netip.MustParsePrefix(fmt.Sprintf("10.42.%d.%d/32", index/250, index%250+2))},
		}))
	}
	costPhase(t, &recorded, "n3-configured", started, "")

	// Close every endpoint through its scope, exactly as the Box does, and then give the stacks a
	// moment to unwind before the release sample.
	started = time.Now()
	for _, scope := range scopes {
		_ = scope.Close()
	}
	time.Sleep(2 * time.Second)
	costPhase(t, &recorded, "n4-released", started, "")

	writeCostSamples(t, recorded)
	for _, sample := range recorded {
		t.Logf("%-16s wall=%8.2fms heap=%9d objs=%8d goroutines=%d",
			sample.Phase, sample.WallMs, sample.HeapAlloc, sample.HeapObjects, sample.Goroutines)
	}
}

func writeCostSamples(t *testing.T, recorded []phaseCost) {
	t.Helper()
	if outPath := os.Getenv("JJB_COST_OUT"); outPath != "" {
		file, err := os.OpenFile(outPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		require.NoError(t, err)
		encoder := json.NewEncoder(file)
		for _, sample := range recorded {
			require.NoError(t, encoder.Encode(sample))
		}
		require.NoError(t, file.Close())
	}
	for _, sample := range recorded {
		t.Logf("%-16s wall=%8.2fms heap=%9d objs=%8d total=%10d goroutines=%d",
			sample.Phase, sample.WallMs, sample.HeapAlloc, sample.HeapObjects,
			sample.TotalAlloc, sample.Goroutines)
	}
}
