package trojan

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Measurement entry point for GM-4 (cold ordinary nodes and on-demand expansion).
//
// # Why a skipped-by-default test rather than a benchmark nobody runs
//
// The numbers that decide this task are LIVE HEAP, live objects, GC scan bytes and per-node slopes
// at 0/100/1000/10000 nodes, in three activity states. They are too slow for every push but must be
// reproducible by anyone, so the entry point is a normal `go test -run` gated by
// `HAKO_GM4_MEASURE=1`. `HAKO_GM4_FULL=1` adds the 10000-node row; `HAKO_GM4_EVIDENCE_DIR` writes
// the raw CSVs (the log lines are the primary evidence).
//
// # What each number is, and is not
//
//   - live heap / objects / scan are read after TWO forced collections, and the objects are kept
//     alive through the read with runtime.KeepAlive. They are Go heap, not device footprint.
//   - the manager rows measure the real Manager + LifecycleGroup + per-node child Scope, because
//     that is what a Provider-sized profile actually holds. The direct rows match the root
//     cold_node fixture's construction style, so the two can be compared.
//   - the provider-snapshot row is the shape of `appliedOutbounds` (a map of option.Outbound with
//     the decoded options behind it), NOT a new copy this prototype would add. A lazy node does not
//     remove it: it holds the full configuration for change detection, and that is a different
//     owner (GM-6).
//
// Nothing here is a device measurement, and no number here is a claim about iOS phys_footprint.

func requireMeasurement(t *testing.T) {
	t.Helper()
	if os.Getenv("HAKO_GM4_MEASURE") == "" {
		t.Skip("measurement entry point: set HAKO_GM4_MEASURE=1 (and HAKO_GM4_FULL=1 for the 10000-node row)")
	}
}

type heapReading struct {
	live       int64
	objects    int64
	scanBytes  int64
	stackBytes int64
	goroutines int64
}

// sub returns the signed difference. Subtracting uint64 values directly underflows into an
// astronomical "saving" when the second reading is smaller, which is exactly the class of mistake
// this task's evidence rules forbid.
func (r heapReading) sub(base heapReading) heapReading {
	delta := func(after, before int64) int64 {
		if after >= before {
			return after - before
		}
		return -before + after
	}
	return heapReading{
		live:       delta(r.live, base.live),
		objects:    delta(r.objects, base.objects),
		scanBytes:  delta(r.scanBytes, base.scanBytes),
		stackBytes: delta(r.stackBytes, base.stackBytes),
		goroutines: delta(r.goroutines, base.goroutines),
	}
}

func readHeap() heapReading {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	samples := []metrics.Sample{
		{Name: "/gc/heap/live:bytes"},
		{Name: "/gc/heap/objects:objects"},
		{Name: "/gc/scan/heap:bytes"},
		{Name: "/memory/classes/heap/stacks:bytes"},
	}
	metrics.Read(samples)
	return heapReading{
		live:       int64(stats.HeapAlloc),
		objects:    int64(stats.HeapObjects),
		scanBytes:  int64(samples[2].Value.Uint64()),
		stackBytes: int64(samples[3].Value.Uint64()),
		goroutines: int64(runtime.NumGoroutine()),
	}
}

type measurementRecorder struct {
	path string
	rows [][]string
}

func newRecorder(t *testing.T, fileName string, header ...string) *measurementRecorder {
	t.Helper()
	recorder := &measurementRecorder{rows: [][]string{header}}
	dir := os.Getenv("HAKO_GM4_EVIDENCE_DIR")
	if dir == "" {
		return recorder
	}
	metricsDir := filepath.Join(dir, "metrics")
	require.NoError(t, os.MkdirAll(metricsDir, 0o755))
	recorder.path = filepath.Join(metricsDir, fileName)
	return recorder
}

func (r *measurementRecorder) add(row ...string) {
	r.rows = append(r.rows, row)
}

func (r *measurementRecorder) flush(t *testing.T) {
	t.Helper()
	if r.path == "" {
		return
	}
	var builder strings.Builder
	for _, row := range r.rows {
		builder.WriteString(strings.Join(row, ","))
		builder.WriteString("\n")
	}
	require.NoError(t, os.WriteFile(r.path, []byte(builder.String()), 0o644))
}

func (r heapReading) row(caseName string, nodes int, mode, state string) []string {
	return []string{
		caseName, fmt.Sprintf("%d", nodes), mode, state,
		fmt.Sprintf("%d", r.live), fmt.Sprintf("%d", r.objects),
		fmt.Sprintf("%d", r.scanBytes), fmt.Sprintf("%d", r.stackBytes), fmt.Sprintf("%d", r.goroutines),
	}
}

// coldTrojanOptionsFor is the representative ordinary node: literal server, TLS with no certificate
// material, no transport, no multiplex. It is deliberately the SAME shape the root cold-cost test
// uses, so the two measurements describe the same object.
func coldTrojanOptionsFor(server M.Socksaddr, index int) *option.TrojanOutboundOptions {
	return &option.TrojanOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server.AddrString(), ServerPort: server.Port},
		Password:      "cold-node-password",
		OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{
			Enabled:    true,
			Insecure:   true,
			ServerName: "cold.example",
		}},
	}
}

func coldTag(index int) string {
	return fmt.Sprintf("node-%05d", index)
}

// measureEndpointManager is an empty endpoint namespace: the Manager requires one for Start, and a
// nil would dereference.
type measureEndpointManager struct {
	adapter.EndpointManager
}

func (m *measureEndpointManager) Get(tag string) (adapter.Endpoint, bool) { return nil, false }
func (m *measureEndpointManager) Endpoints() []adapter.Endpoint           { return nil }
func (m *measureEndpointManager) StartEndpoint(endpoint adapter.Endpoint) error {
	return nil
}

// measureDirectOutbound is the fallback the Manager installs when no default outbound is declared.
type measureDirectOutbound struct {
	adapter.Outbound
}

func (o *measureDirectOutbound) Type() string           { return C.TypeDirect }
func (o *measureDirectOutbound) Tag() string            { return "direct" }
func (o *measureDirectOutbound) Network() []string      { return []string{"tcp", "udp"} }
func (o *measureDirectOutbound) Dependencies() []string { return nil }
func (o *measureDirectOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, os.ErrInvalid
}
func (o *measureDirectOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

// measureDirectHolding constructs `nodes` nodes directly (no Manager) and starts them through ONE
// scope, which is the root cold-cost fixture's shape. It is the number comparable to the published
// 2.04 MiB / 1000 eager nodes.
func measureDirectHolding(t *testing.T, nodes int, deferred bool, withTransport bool) heapReading {
	t.Helper()
	ctx := context.Background()
	logger := log.NewNOPFactory().NewLogger("measure")
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	defer scope.Close()
	server := M.ParseSocksaddrHostPort("198.51.100.7", 443)
	before := readHeap()
	created := make([]adapter.Outbound, 0, nodes)
	for index := range nodes {
		options := coldTrojanOptionsFor(server, index)
		if withTransport {
			options.Transport = &option.V2RayTransportOptions{Type: C.V2RayTransportTypeWebsocket}
		}
		var node adapter.Outbound
		var err error
		if deferred {
			var handled bool
			node, handled, err = deferredTrojanOutbound(ctx, nil, logger, coldTag(index), C.TypeTrojan, options, "")
			require.True(t, handled, "the measurement corpus must be eligible")
		} else {
			node, err = NewOutbound(ctx, nil, logger, coldTag(index), *options)
		}
		require.NoError(t, err)
		created = append(created, node)
	}
	for _, stage := range adapter.ListStartStages {
		for _, node := range created {
			require.NoError(t, node.(adapter.Lifecycle).Start(stage, scope))
		}
	}
	reading := readHeap()
	after := reading.sub(before)
	runtime.KeepAlive(created)
	runtime.KeepAlive(scope)
	return after
}

// measureManagerHolding is the real shape: Manager.Create for every node, then the four Start
// stages through the manager's LifecycleGroup. It includes the manager index and one child Scope per
// node, which is what a Provider-sized profile really holds.
func measureManagerHolding(t *testing.T, nodes int, deferred bool) heapReading {
	return measureManagerHoldingStages(t, nodes, deferred, true)
}

// measureManagerHoldingStages splits the manager cost: Create publishes the object and the index,
// Start adds the per-node LifecycleGroup child Scope. The split is what attributes the per-node
// scaffolding, which is the OTHER half of the cold-node cost.
func measureManagerHoldingStages(t *testing.T, nodes int, deferred bool, start bool) heapReading {
	t.Helper()
	ctx := context.Background()
	registry := outbound.NewRegistry()
	RegisterOutbound(registry)
	manager := outbound.NewManager(registry, &measureEndpointManager{}, "")
	manager.Initialize(func() (adapter.Outbound, error) { return &measureDirectOutbound{}, nil })
	if deferred {
		manager.SetDeferredOutboundConstructor(DeferredConstructor())
	}
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	defer scope.Close()
	logger := log.NewNOPFactory().NewLogger("measure")
	server := M.ParseSocksaddrHostPort("198.51.100.7", 443)
	before := readHeap()
	for index := range nodes {
		require.NoError(t, manager.Create(ctx, nil, logger, coldTag(index), C.TypeTrojan, coldTrojanOptionsFor(server, index)))
	}
	if start {
		for _, stage := range adapter.ListStartStages {
			require.NoError(t, manager.Start(stage, scope))
		}
	}
	reading := readHeap().sub(before)
	runtime.KeepAlive(manager)
	runtime.KeepAlive(scope)
	return reading
}

// measureProviderSnapshot is the shape of the provider's appliedOutbounds map: one option.Outbound
// per node with the full decoded options behind it. It is NOT something the lazy layer removes; the
// point of measuring it is to state how much of the cold cost lives in the configuration snapshot
// rather than in the runtime object.
func measureProviderSnapshot(t *testing.T, nodes int) heapReading {
	t.Helper()
	server := M.ParseSocksaddrHostPort("198.51.100.7", 443)
	before := readHeap()
	snapshot := make(map[string]option.Outbound, nodes)
	for index := range nodes {
		snapshot[coldTag(index)] = option.Outbound{
			Type:    C.TypeTrojan,
			Tag:     coldTag(index),
			Options: coldTrojanOptionsFor(server, index),
		}
	}
	reading := readHeap().sub(before)
	runtime.KeepAlive(snapshot)
	return reading
}

// measureRecipeFloor is the cost of the descriptor layer alone: one immutable recipe per node.
func measureRecipeFloor(t *testing.T, nodes int) heapReading {
	t.Helper()
	ctx := context.Background()
	server := M.ParseSocksaddrHostPort("198.51.100.7", 443)
	before := readHeap()
	recipes := make([]lazyRecipe, 0, nodes)
	for index := range nodes {
		recipe, reason := newLazyRecipe(ctx, coldTrojanOptionsFor(server, index))
		require.NotNil(t, recipe, reason)
		recipes = append(recipes, *recipe)
	}
	reading := readHeap().sub(before)
	runtime.KeepAlive(recipes)
	return reading
}

// measureMembershipIndex is the group/namespace index shape: a tag list plus a membership set.
func measureMembershipIndex(t *testing.T, nodes int) heapReading {
	t.Helper()
	before := readHeap()
	tags := make([]string, 0, nodes)
	membership := make(map[string]bool, nodes)
	for index := range nodes {
		tag := coldTag(index)
		tags = append(tags, tag)
		membership[tag] = true
	}
	reading := readHeap().sub(before)
	runtime.KeepAlive(tags)
	runtime.KeepAlive(membership)
	return reading
}

// TestMeasureColdHolding is the 0/100/1000/10000 table.
func TestMeasureColdHolding(t *testing.T) {
	requireMeasurement(t)
	// The deferred object's own size, so a per-node delta can be attributed to the struct instead of
	// guessed at: a few added words can move the object into the next allocator size class, which
	// costs more than the words themselves.
	t.Logf("MEASURED size deferred-object=%d B recipe=%d B",
		unsafe.Sizeof(outbound.LazyOutbound[lazyRecipe]{}), unsafe.Sizeof(lazyRecipe{}))
	sizes := []int{0, 100, 1000}
	if os.Getenv("HAKO_GM4_FULL") != "" {
		sizes = append(sizes, 10000)
	}
	recorder := newRecorder(t, "gm4-holding.csv",
		"case", "nodes", "mode", "state", "live_bytes", "objects", "scan_bytes", "stack_bytes", "goroutines")
	defer recorder.flush(t)
	for _, nodes := range sizes {
		nodes := nodes
		t.Run(fmt.Sprintf("N=%d", nodes), func(t *testing.T) {
			directEager := measureDirectHolding(t, nodes, false, false)
			directLazy := measureDirectHolding(t, nodes, true, false)
			managerEagerCreate := measureManagerHoldingStages(t, nodes, false, false)
			managerLazyCreate := measureManagerHoldingStages(t, nodes, true, false)
			managerEager := measureManagerHolding(t, nodes, false)
			managerLazy := measureManagerHolding(t, nodes, true)
			snapshot := measureProviderSnapshot(t, nodes)
			recipes := measureRecipeFloor(t, nodes)
			membership := measureMembershipIndex(t, nodes)
			for name, reading := range map[string]heapReading{
				"direct-eager":              directEager,
				"direct-lazy":               directLazy,
				"manager-eager-create-only": managerEagerCreate,
				"manager-lazy-create-only":  managerLazyCreate,
				"manager-eager":             managerEager,
				"manager-lazy":              managerLazy,
				"provider-snapshot":         snapshot,
				"recipe-floor":              recipes,
				"membership-index":          membership,
			} {
				recorder.rows = append(recorder.rows, reading.row("holding", nodes, name, "never-dialed"))
				t.Logf("MEASURED holding N=%d mode=%s state=never-dialed live=%+d objects=%+d scan=%+d stack=%+d goroutines=%+d",
					nodes, name, reading.live, reading.objects, reading.scanBytes, reading.stackBytes, reading.goroutines)
			}
		})
	}
}

// TestMeasureNodeAttribution splits the eager per-node cost into TLS/dialer/transport at N=1000, so
// the report can say what a deferred node stops paying and what the configuration snapshot keeps.
func TestMeasureNodeAttribution(t *testing.T) {
	requireMeasurement(t)
	const nodes = 1000
	recorder := newRecorder(t, "gm4-attribution.csv",
		"case", "nodes", "mode", "state", "live_bytes", "objects", "scan_bytes", "stack_bytes", "goroutines")
	defer recorder.flush(t)
	plain := measurePlainTCPHolding(t, nodes)
	tlsOnly := measureDirectHolding(t, nodes, false, false)
	lazy := measureDirectHolding(t, nodes, true, false)
	transport := measureDirectHolding(t, nodes, false, true)
	rows := map[string]heapReading{
		"eager-plain-tcp":       plain,
		"eager-tls":             tlsOnly,
		"eager-tls-and-ws":      transport,
		"lazy-tls-recipe-owned": lazy,
	}
	for name, reading := range rows {
		recorder.rows = append(recorder.rows, reading.row("attribution", nodes, name, "never-dialed"))
		t.Logf("MEASURED attribution N=%d mode=%s live=%+d objects=%+d scan=%+d",
			nodes, name, reading.live, reading.objects, reading.scanBytes)
	}
}

func measurePlainTCPHolding(t *testing.T, nodes int) heapReading {
	t.Helper()
	ctx := context.Background()
	logger := log.NewNOPFactory().NewLogger("measure")
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	defer scope.Close()
	server := M.ParseSocksaddrHostPort("198.51.100.7", 443)
	before := readHeap()
	created := make([]adapter.Outbound, 0, nodes)
	for index := range nodes {
		options := coldTrojanOptionsFor(server, index)
		options.TLS = nil
		node, err := NewOutbound(ctx, nil, logger, coldTag(index), *options)
		require.NoError(t, err)
		created = append(created, node)
	}
	for _, stage := range adapter.ListStartStages {
		for _, node := range created {
			require.NoError(t, node.(adapter.Lifecycle).Start(stage, scope))
		}
	}
	reading := readHeap().sub(before)
	runtime.KeepAlive(created)
	runtime.KeepAlive(scope)
	return reading
}

// waitForQuiescence confirms the network side of a measurement actually finished. The poll is a
// readiness confirmation, not a replacement for one: a fixed sleep would either be too short (and
// count live connection objects as retained state) or too long (and report a number no product
// reaches).
func waitForQuiescence(t *testing.T, baseline int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > baseline+8 {
		if time.Now().After(deadline) {
			t.Logf("WARNING: goroutines did not settle: %d (baseline %d)", runtime.NumGoroutine(), baseline)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func dialColdNode(t *testing.T, node adapter.Outbound) {
	t.Helper()
	conn, err := node.DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "destination.example", Port: 80})
	require.NoError(t, err)
	_, err = conn.Write([]byte("x"))
	require.NoError(t, err)
	buffer := make([]byte, 1)
	_, err = conn.Read(buffer)
	require.NoError(t, err)
	require.Equal(t, byte('x'), buffer[0])
	require.NoError(t, conn.Close())
}

func dialColdNodesParallel(t *testing.T, nodes []adapter.Outbound) {
	t.Helper()
	const workers = 16
	work := make(chan adapter.Outbound)
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for range workers {
		go func() {
			defer waitGroup.Done()
			for node := range work {
				dialColdNode(t, node)
			}
		}()
	}
	for _, node := range nodes {
		work <- node
	}
	close(work)
	waitGroup.Wait()
}

// TestMeasureThreeStates compares never-dialled, a few nodes used, and every node probed once and
// then idle - the three states requirement 1 asks for. It also measures the eviction recovery, which
// is the difference between "lazy" and "lazy with a real idle policy".
func TestMeasureThreeStates(t *testing.T) {
	requireMeasurement(t)
	const nodes = 1000
	recorder := newRecorder(t, "gm4-states.csv",
		"case", "nodes", "mode", "state", "live_bytes", "objects", "scan_bytes", "stack_bytes", "goroutines")
	defer recorder.flush(t)

	server := startTrojanEchoServer(t, "cold-node-password", "h2")
	serverAddr := server.addr()

	baselineGoroutines := runtime.NumGoroutine()

	setup := func(deferred bool) (*outbound.Manager, *adapter.Scope, []adapter.Outbound, []*lazyTrojanOutbound) {
		ctx := context.Background()
		registry := outbound.NewRegistry()
		RegisterOutbound(registry)
		manager := outbound.NewManager(registry, &measureEndpointManager{}, "")
		manager.Initialize(func() (adapter.Outbound, error) { return &measureDirectOutbound{}, nil })
		if deferred {
			manager.SetDeferredOutboundConstructor(DeferredConstructor())
			// This case measures the UNBOUNDED probe state - every node expanded at once - and then
			// what the policy does about it. Installing the seam arms the default budget, which would
			// bound the probe while it ran and leave nothing to reclaim; that bounded product shape is
			// TestMeasureBoundedProbe.
			manager.DisableDeferredReclaim()
		}
		scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
		t.Cleanup(func() { _ = scope.Close() })
		logger := log.NewNOPFactory().NewLogger("measure")
		for index := range nodes {
			options := coldTrojanOptionsFor(serverAddr, index)
			require.NoError(t, manager.Create(ctx, nil, logger, coldTag(index), C.TypeTrojan, options))
		}
		for _, stage := range adapter.ListStartStages {
			require.NoError(t, manager.Start(stage, scope))
		}
		all := manager.Outbounds()
		measured := make([]adapter.Outbound, 0, nodes)
		lazyNodes := make([]*lazyTrojanOutbound, 0, nodes)
		for _, node := range all {
			if node.Tag() == "direct" {
				continue
			}
			measured = append(measured, node)
			if lazyNode, isLazy := node.(*lazyTrojanOutbound); isLazy {
				lazyNodes = append(lazyNodes, lazyNode)
			}
		}
		require.Len(t, measured, nodes)
		return manager, scope, measured, lazyNodes
	}

	// Lazy: never-dialled -> 3 used -> all probed -> all evicted.
	// Lazy: never-dialled -> 3 used -> all probed -> all evicted. Every row is a delta from the SAME
	// baseline, taken before the manager exists, so the rows are directly comparable.
	baseline := readHeap()
	manager, scope, measured, lazyNodes := setup(true)
	never := readHeap().sub(baseline)
	runtime.KeepAlive(manager)
	require.Len(t, lazyNodes, nodes)
	dialColdNodesParallel(t, measured[:3])
	waitForQuiescence(t, baselineGoroutines)
	few := readHeap().sub(baseline)
	dialColdNodesParallel(t, measured[3:])
	waitForQuiescence(t, baselineGoroutines)
	full := readHeap().sub(baseline)
	// The reclaim policy, at its DEFAULT budget, is what turns "every node was expanded by the probe"
	// back into "the subscription is cold again". One sweep is deliberately BOUNDED (the policy's
	// MaxEvictionsPerSweep), so convergence is "as many sweeps as the bound requires" - which is what
	// the product's triggers (demand, idle walk, pressure) also do.
	manager.SetDeferredReclaimPolicy(outbound.DefaultDeferredReclaimPolicy())
	reclaimStart := time.Now()
	totalEvicted := 0
	sweeps := 0
	var reclaim outbound.ReclaimResult
	for sweeps < 64 {
		reclaim = manager.ReclaimDeferredExpansions(context.Background(), outbound.ReclaimCauseExplicit)
		totalEvicted += reclaim.Evicted
		sweeps++
		if manager.DeferredExpansionCount() <= reclaim.Target {
			break
		}
	}
	reclaimElapsed := time.Since(reclaimStart)
	require.Equal(t, nodes-reclaim.Target, totalEvicted)
	require.LessOrEqual(t, manager.DeferredExpansionCount(), reclaim.Target)
	afterReclaim := readHeap().sub(baseline)
	runtime.KeepAlive(measured)
	runtime.KeepAlive(scope)
	perEviction := reclaimElapsed / time.Duration(max(totalEvicted, 1))
	t.Logf("MEASURED reclaim N=%d target=%d evicted=%d sweeps=%d total=%s perEviction=%s estimated=%d budgetBytes=%d",
		nodes, reclaim.Target, totalEvicted, sweeps, reclaimElapsed, perEviction, reclaim.EstimatedBytes, reclaim.BudgetBytes)
	require.LessOrEqual(t, afterReclaim.live-never.live, int64(reclaim.Target)*reclaimTargetEstimate+256*1024,
		"after reclaiming, the live heap must sit within the budget of the never-dialled state")
	// The floor: releasing the budget's own survivors too, so the report can state the fully cold
	// state as well as the policy's steady state.
	evicted := 0
	for _, node := range lazyNodes {
		if node.Evict() {
			evicted++
		}
	}
	require.Equal(t, reclaim.Target, evicted)
	afterEvict := readHeap().sub(baseline)
	t.Logf("MEASURED evict-all N=%d evicted=%d", nodes, evicted)

	for name, reading := range map[string]heapReading{
		"never-dialled":        never,
		"few-used":             few,
		"full-probe-then-idle": full,
		"full-probe-reclaimed": afterReclaim,
		"full-probe-evicted":   afterEvict,
	} {
		recorder.rows = append(recorder.rows, reading.row("three-states", nodes, "manager-lazy", name))
		t.Logf("MEASURED three-states N=%d mode=manager-lazy state=%s live=%+d objects=%+d scan=%+d",
			nodes, name, reading.live, reading.objects, reading.scanBytes)
	}

	// Eager control: the same three states, so the difference is the only claim.
	eagerBaseline := readHeap()
	eagerManager, eagerScope, eagerMeasured, _ := setup(false)
	eagerNever := readHeap().sub(eagerBaseline)
	runtime.KeepAlive(eagerManager)
	dialColdNodesParallel(t, eagerMeasured[:3])
	waitForQuiescence(t, baselineGoroutines)
	eagerFew := readHeap().sub(eagerBaseline)
	dialColdNodesParallel(t, eagerMeasured[3:])
	waitForQuiescence(t, baselineGoroutines)
	eagerFull := readHeap().sub(eagerBaseline)
	runtime.KeepAlive(eagerMeasured)
	runtime.KeepAlive(eagerScope)
	for name, reading := range map[string]heapReading{
		"never-dialled":        eagerNever,
		"few-used":             eagerFew,
		"full-probe-then-idle": eagerFull,
	} {
		recorder.rows = append(recorder.rows, reading.row("three-states", nodes, "manager-eager", name))
		t.Logf("MEASURED three-states N=%d mode=manager-eager state=%s live=%+d objects=%+d scan=%+d",
			nodes, name, reading.live, reading.objects, reading.scanBytes)
	}

	// One live session through an ALREADY EXPANDED eager node, to isolate connection state from the
	// node itself. A session through a brand-new lazy node would fold the first build into the
	// number; that cost is reported by the latency row instead.
	sessionBaseline := readHeap()
	conn, err := eagerMeasured[0].DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "destination.example", Port: 80})
	require.NoError(t, err)
	_, err = conn.Write([]byte("session"))
	require.NoError(t, err)
	buffer := make([]byte, 7)
	_, err = conn.Read(buffer)
	require.NoError(t, err)
	session := readHeap().sub(sessionBaseline)
	recorder.rows = append(recorder.rows, session.row("three-states", 1, "manager-eager", "one-live-session"))
	t.Logf("MEASURED session N=1 mode=manager-eager live=%+d objects=%+d", session.live, session.objects)
	require.NoError(t, conn.Close())
}

// reclaimTargetEstimate is the per-expansion size the reclaim budget is derived from (the measured
// 2.9 KiB, rounded up). The acceptance checks compare the reclaimed heap against the SAME number the
// policy uses, so they cannot pass by relying on a different assumption than the code.
const reclaimTargetEstimate = int64(3 * 1024)

// TestMeasureBoundedProbe is the product shape: the reclaim budget is armed BEFORE the probe, so the
// expansion set is bounded WHILE a round expands every node instead of only after it. It answers the
// audit's "全量测速后不淘汰会使原型变大" directly: with the policy on, a 1000-node probe never holds
// more than the budget of expansions, and the heap after the round is the never-dialled heap plus the
// budget.
func TestMeasureBoundedProbe(t *testing.T) {
	requireMeasurement(t)
	const nodes = 1000
	recorder := newRecorder(t, "gm4-bounded-probe.csv",
		"case", "nodes", "mode", "state", "live_bytes", "objects", "scan_bytes", "stack_bytes", "goroutines")
	defer recorder.flush(t)

	server := startTrojanEchoServer(t, "cold-node-password", "h2")
	serverAddr := server.addr()
	baselineGoroutines := runtime.NumGoroutine()

	ctx := context.Background()
	registry := outbound.NewRegistry()
	RegisterOutbound(registry)
	manager := outbound.NewManager(registry, &measureEndpointManager{}, "")
	manager.Initialize(func() (adapter.Outbound, error) { return &measureDirectOutbound{}, nil })
	manager.SetDeferredOutboundConstructor(DeferredConstructor())
	policy := outbound.DefaultDeferredReclaimPolicy()
	manager.SetDeferredReclaimPolicy(policy)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = scope.Close() })
	logger := log.NewNOPFactory().NewLogger("measure")

	baseline := readHeap()
	for index := range nodes {
		require.NoError(t, manager.Create(ctx, nil, logger, coldTag(index), C.TypeTrojan, coldTrojanOptionsFor(serverAddr, index)))
	}
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, manager.Start(stage, scope))
	}
	measured := make([]adapter.Outbound, 0, nodes)
	for _, node := range manager.Outbounds() {
		if node.Tag() == "direct" {
			continue
		}
		measured = append(measured, node)
	}
	require.Len(t, measured, nodes)
	never := readHeap().sub(baseline)

	dialColdNodesParallel(t, measured)
	waitForQuiescence(t, baselineGoroutines)
	// A sweep may still be in flight: wait for the budget to hold before reading the heap.
	require.Eventually(t, func() bool {
		return manager.DeferredExpansionCount() <= policy.MaxExpanded
	}, 30*time.Second, 10*time.Millisecond)
	bounded := readHeap().sub(baseline)
	expanded := manager.DeferredExpansionCount()
	runtime.KeepAlive(manager)
	runtime.KeepAlive(measured)
	runtime.KeepAlive(scope)

	t.Logf("MEASURED bounded-probe N=%d expandedAfterRound=%d budget=%d neverLive=%d boundedLive=%d objects=%d",
		nodes, expanded, policy.MaxExpanded, never.live, bounded.live, bounded.objects)
	require.LessOrEqual(t, expanded, policy.MaxExpanded, "the budget must hold while the round runs")
	require.LessOrEqual(t, bounded.live-never.live, int64(policy.MaxExpanded)*reclaimTargetEstimate+256*1024,
		"the heap after a bounded probe must stay within the budget of the never-dialled state")

	for name, reading := range map[string]heapReading{
		"never-dialled":           never,
		"full-probe-budget-armed": bounded,
	} {
		recorder.rows = append(recorder.rows, reading.row("bounded-probe", nodes, "manager-lazy", name))
		t.Logf("MEASURED bounded-probe N=%d state=%s live=%+d objects=%+d scan=%+d",
			nodes, name, reading.live, reading.objects, reading.scanBytes)
	}
}

func percentile(durations []time.Duration, fraction float64) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(float64(len(sorted)-1) * fraction)
	return sorted[index]
}

// TestMeasureLatencyAndRebuild compares a cold first dial (build + connect), a warm dial, and the
// rebuild cost the eviction policy would pay again. The build is also measured directly so the
// report can separate "construction" from "TLS handshake".
func TestMeasureLatencyAndRebuild(t *testing.T) {
	requireMeasurement(t)
	recorder := newRecorder(t, "gm4-latency.csv", "case", "iterations", "rounds", "median_ns", "min_ns", "max_ns")
	defer recorder.flush(t)
	server := startTrojanEchoServer(t, "cold-node-password", "h2")
	serverAddr := server.addr()
	ctx := context.Background()
	logger := log.NewNOPFactory().NewLogger("measure")

	timeDial := func(node adapter.Outbound) time.Duration {
		started := time.Now()
		conn, err := node.DialContext(ctx, "tcp", M.Socksaddr{Fqdn: "destination.example", Port: 80})
		require.NoError(t, err)
		_, err = conn.Write([]byte("ping"))
		require.NoError(t, err)
		buffer := make([]byte, 4)
		_, err = conn.Read(buffer)
		require.NoError(t, err)
		_ = conn.Close()
		return time.Since(started)
	}

	// Cold vs warm dial: the same nodes, first dial then second dial. The difference is the cost a
	// deferred node adds to the FIRST connection only.
	const coldSamples = 40
	coldNodes := make([]*lazyTrojanOutbound, 0, coldSamples)
	for index := range coldSamples {
		created, handled, err := deferredTrojanOutbound(ctx, nil, logger, coldTag(index), C.TypeTrojan, coldTrojanOptionsFor(serverAddr, index), "")
		require.NoError(t, err)
		require.True(t, handled)
		node := created.(*lazyTrojanOutbound)
		scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
		require.NoError(t, node.Start(adapter.StartStateInitialize, scope))
		t.Cleanup(func() { _ = scope.Close() })
		coldNodes = append(coldNodes, node)
	}
	var coldDurations []time.Duration
	for _, node := range coldNodes {
		coldDurations = append(coldDurations, timeDial(node))
	}
	var warmDurations []time.Duration
	for _, node := range coldNodes {
		warmDurations = append(warmDurations, timeDial(node))
	}
	recordSample := func(name string, durations []time.Duration) {
		recorder.rows = append(recorder.rows, []string{
			name,
			fmt.Sprintf("%d", len(durations)),
			"1",
			fmt.Sprintf("%d", percentile(durations, 0.50).Nanoseconds()),
			fmt.Sprintf("%d", percentile(durations, 0.0).Nanoseconds()),
			fmt.Sprintf("%d", percentile(durations, 1.0).Nanoseconds()),
		})
		t.Logf("MEASURED latency case=%s samples=%d p50=%s min=%s max=%s",
			name, len(durations), percentile(durations, 0.50), percentile(durations, 0.0), percentile(durations, 1.0))
	}
	recordSample("cold-first-dial", coldDurations)
	recordSample("warm-dial", warmDurations)

	// Batch timing for the operations too fast for a single clock read. Each round runs
	// `iterations` operations and the median round is the reported per-operation cost.
	measureBatch := func(name string, iterations int, operation func(index int)) {
		const rounds = 5
		for index := range 20 {
			operation(index)
		}
		perOperation := make([]time.Duration, 0, rounds)
		for range rounds {
			started := time.Now()
			for index := range iterations {
				operation(index)
			}
			perOperation = append(perOperation, time.Since(started)/time.Duration(iterations))
		}
		recorder.rows = append(recorder.rows, []string{
			name,
			fmt.Sprintf("%d", iterations),
			fmt.Sprintf("%d", rounds),
			fmt.Sprintf("%d", percentile(perOperation, 0.50).Nanoseconds()),
			fmt.Sprintf("%d", percentile(perOperation, 0.0).Nanoseconds()),
			fmt.Sprintf("%d", percentile(perOperation, 1.0).Nanoseconds()),
		})
		t.Logf("MEASURED latency case=%s iterations=%d rounds=%d median=%s min=%s max=%s",
			name, iterations, rounds, percentile(perOperation, 0.50), percentile(perOperation, 0.0), percentile(perOperation, 1.0))
	}

	// Rebuild cost: the deferred builder is the SAME constructor the eager path uses. Pre-built
	// inputs keep the derivation out of the timed region.
	const prebuilt = 256
	optionValues := make([]option.TrojanOutboundOptions, 0, prebuilt)
	optionPointers := make([]*option.TrojanOutboundOptions, 0, prebuilt)
	for index := range prebuilt {
		options := coldTrojanOptionsFor(serverAddr, index)
		recipe, reason := newLazyRecipe(ctx, options)
		require.NotNil(t, recipe, reason)
		optionValues = append(optionValues, recipe.outboundOptions())
		optionPointers = append(optionPointers, options)
	}
	measureBatch("rebuild-constructor", 1000, func(index int) {
		node, err := NewOutbound(ctx, nil, logger, coldTag(index%prebuilt), optionValues[index%prebuilt])
		require.NoError(t, err)
		runtime.KeepAlive(node)
	})
	measureBatch("descriptor-create", 5000, func(index int) {
		recipe, reason := newLazyRecipe(ctx, optionPointers[index%prebuilt])
		require.NotNil(t, recipe, reason)
		runtime.KeepAlive(recipe)
	})
}
