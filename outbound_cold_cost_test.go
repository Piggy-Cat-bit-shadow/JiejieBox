package box

import (
	"context"
	"runtime"
	"strconv"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/trojan"

	"github.com/stretchr/testify/require"
)

// A node that is configured but never dialled must not cost a thread.
//
// # Why this is a test rather than an audit finding
//
// A 100 000-node profile is not one node repeated: it is 100 000 heap structs, and whether each one
// also owns a goroutine, a socket or a timer decides whether the profile is possible at all. An audit
// answered that question once, statically, for every protocol in `protocol/` - and a static answer
// decays the moment somebody adds a `go func()` to a constructor. This pins the half that must not
// change, on the representative light kind: constructed, started through all four stages, never
// dialled, and the process goroutine count does not move.
//
// # What it deliberately does not cover
//
// The five endpoint families that DO hold runtime resources before first dial (wireguard, tailscale,
// openvpn, openconnect, masque) are excluded on purpose. Asserting their current cost would ratchet
// behaviour the next round is meant to remove; their cost is recorded in
// KNOWLEDGE/reports/core/HAKO_SCALE_ARCHITECTURE_AUDIT_2026-10-17.md instead.
//
// `trojan` stands for the light kinds because it has the full shape - a dialer, a TLS config, a
// transport object - without any of them being eager. A protocol that acquired a goroutine here would
// fail this test the moment it did.
//
// Note what is NOT called: no `Close`. An outbound has no Close of its own - it registers its cleanup
// on the Scope during Start, and `scope.Close()` is the only owner. That is the lifecycle the test
// also has to respect, so the fixture closes the scope and nothing else.

const coldNodeCount = 1000

// coldTrojanOptions is a legal node: TLS on, no certificate (so no per-node cert pool), no multiplex,
// no transport that could bind anything.
func coldTrojanOptions(port uint16) option.TrojanOutboundOptions {
	options := option.TrojanOutboundOptions{
		ServerOptions: option.ServerOptions{
			Server:     "198.51.100.7",
			ServerPort: port,
		},
		Password: "cold-node-password",
	}
	options.TLS = &option.OutboundTLSOptions{
		Enabled:    true,
		Insecure:   true,
		ServerName: "cold.example",
	}
	return options
}

// TestAConstructedNodeCostsNoThread is the invariant.
func TestAConstructedNodeCostsNoThread(t *testing.T) {
	// The instrument, proved before it is used: a goroutine that exists must move the count, or a
	// count that does not move proves nothing.
	baseline := runtime.NumGoroutine()
	release := make(chan struct{})
	entered := make(chan struct{})
	go func() {
		close(entered)
		<-release
	}()
	<-entered
	require.Greater(t, runtime.NumGoroutine(), baseline,
		"the goroutine census cannot see a goroutine that is running, so it cannot support the "+
			"assertion below")
	close(release)

	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	// Both baselines are taken BEFORE anything is constructed: a node's cost is what the profile pays
	// for it, and taking the heap baseline afterwards measured a delta of zero by construction - which
	// is how the first version of this file reported "0 bytes per node" for a thousand nodes.
	beforeGoroutines := runtime.NumGoroutine()
	beforeHeap := coldNodeHeapLive()
	nodes := make([]adapter.Lifecycle, 0, coldNodeCount)
	for index := range coldNodeCount {
		created, err := trojan.NewOutbound(
			context.Background(),
			nil,
			log.NewNOPFactory().NewLogger("cold-trojan"),
			"cold-"+strconv.Itoa(index),
			coldTrojanOptions(uint16(20000+index%1000)),
		)
		require.NoError(t, err, "construct node ", index)
		lifecycle, isLifecycle := created.(adapter.Lifecycle)
		require.True(t, isLifecycle, "node ", index, " must be startable")
		nodes = append(nodes, lifecycle)
	}
	require.LessOrEqual(t, runtime.NumGoroutine(), beforeGoroutines,
		"constructing %d nodes spawned a goroutine. A constructor describes a node; starting a worker "+
			"there means every node in the profile pays for it before anything is selected",
		coldNodeCount)

	for _, stageCase := range []struct {
		stage  adapter.StartStage
		reason string
	}{
		{adapter.StartStateInitialize, "constructing runtime resources at Initialize"},
		{adapter.StartStateStart, "binding a socket at Start"},
		{adapter.StartStatePostStart, "starting a worker at PostStart"},
		{adapter.StartStateStarted, "starting a worker at Started"},
	} {
		for index, node := range nodes {
			require.NoError(t, node.Start(stageCase.stage, scope),
				"node ", index, " failed at ", stageCase.stage.String())
		}
		require.LessOrEqual(t, runtime.NumGoroutine(), beforeGoroutines,
			"%d never-dialled nodes gained goroutines at %s (%s). A cold node is a description, "+
				"not a running protocol: whatever this stage started is being paid for by every node "+
				"in the profile whether or not it is ever selected",
			coldNodeCount, stageCase.stage.String(), stageCase.reason)
	}

	grown := coldNodeHeapLive()
	// `KeepAlive` is what makes the heap reading mean anything. The compiler is free to treat `nodes`
	// as dead after its last syntactic use, and MEASURED, without this line the collection inside
	// `coldNodeHeapLive` reclaimed all thousand nodes first: the census reported "+10 464 bytes,
	// 10 per node" for a thousand trojan outbounds that each measure ~2 KB. Reading a heap delta that
	// depends on an object still being live requires saying so.
	runtime.KeepAlive(nodes)
	require.NoError(t, scope.Close(), "the scope must close every node without a panic")

	perNode := int64(0)
	delta := int64(0)
	if grown > beforeHeap {
		delta = int64(grown - beforeHeap)
		perNode = delta / coldNodeCount
	}
	t.Logf("MEASURED %d constructed+started, never dialled trojan nodes: goroutines %d -> %d, "+
		"live heap +%d bytes (%d per node)", coldNodeCount, beforeGoroutines, runtime.NumGoroutine(),
		delta, perNode)

	require.Greater(t, perNode, int64(0),
		"the heap census saw nothing change while %d nodes were built, so it is not measuring the "+
			"nodes at all", coldNodeCount)
	// A trojan node is a dialer, a TLS config and a name; 8 KiB each is an order of magnitude of
	// headroom over the measurement and still far below anything that would justify a lazy descriptor.
	require.Less(t, perNode, int64(8<<10),
		"a never-dialled node costs %d bytes of live heap; a light node is a description and should "+
			"be of order a KiB", perNode)
}

// coldNodeHeapLive is live heap after two forced collections, in bytes.
func coldNodeHeapLive() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}
