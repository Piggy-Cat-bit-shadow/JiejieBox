package nodestore

import (
	"os"
	"runtime"
	"strconv"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/stretchr/testify/require"
)

// This file is the measurement entry point for the store decision. It is skipped unless
// JIEJIEBOX_NODESTORE_MEASURE=1, because it forces collections and is not a correctness test.
//
// # What it compares
//
// Two shapes of the provider's change-detection state, at the same node count:
//
//	provider-snapshot  map[string]option.Outbound, the shape appliedOutbounds had. Every value is a
//	                   shallow copy whose Options interface pins the decoded options tree. The
//	                   document itself is dropped before the reading, exactly as the provider drops
//	                   the parsed document after an apply.
//	provider-store     the compact store: canonical records plus a tag -> index map, document dropped.
//
// and the same two for a static configuration whose decoded document is still owned by the Box:
//
//	static-snapshot    the document is kept alive in both rows; the delta is the extra map.
//	static-store       the document is kept alive; the delta is the store.
//
// Live heap is read after two forced collections with runtime.KeepAlive, like the existing
// memory-study tests. These are Go heap numbers, not device footprint.
//
// # Refresh peak
//
// A refresh holds the previous generation, the newly decoded document and the next generation at the
// same time. The test logs that peak too: a representation that wins in steady state but doubles the
// refresh peak has not won.

func requireMeasure(t *testing.T) {
	t.Helper()
	if os.Getenv("JIEJIEBOX_NODESTORE_MEASURE") == "" {
		t.Skip("measurement entry point: set JIEJIEBOX_NODESTORE_MEASURE=1")
	}
}

type heapReading struct {
	live    int64
	objects int64
	inuse   int64
}

func readHeap() heapReading {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return heapReading{
		live:    int64(stats.HeapAlloc),
		objects: int64(stats.HeapObjects),
		inuse:   int64(stats.HeapInuse),
	}
}

func (r heapReading) sub(base heapReading) heapReading {
	delta := func(after, before int64) int64 {
		if after >= before {
			return after - before
		}
		return -(before - after)
	}
	return heapReading{
		live:    delta(r.live, base.live),
		objects: delta(r.objects, base.objects),
		inuse:   delta(r.inuse, base.inuse),
	}
}

func measureTag(index int) string {
	return "provider/node-" + strconv.Itoa(index)
}

// trojanProviderNode is a realistic provider node: a Trojan outbound with TLS and ALPN, a distinct
// server, password and tag. About 220 canonical bytes.
func trojanProviderNode(index int) option.Outbound {
	return option.Outbound{
		Type: "trojan",
		Tag:  "node-" + strconv.Itoa(index),
		Options: &option.TrojanOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "server" + strconv.Itoa(index) + ".example.com", ServerPort: 443},
			Password:      "password-" + strconv.Itoa(index),
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{
					Enabled:    true,
					ServerName: "server" + strconv.Itoa(index) + ".example.com",
					ALPN:       []string{"h2", "http/1.1"},
				},
			},
		},
	}
}

func providerDocument(nodes int) []option.Outbound {
	document := make([]option.Outbound, 0, nodes)
	for index := 0; index < nodes; index++ {
		document = append(document, trojanProviderNode(index))
	}
	return document
}

func changedProviderDocument(nodes int) []option.Outbound {
	document := providerDocument(nodes)
	document[0] = option.Outbound{
		Type: "trojan",
		Tag:  "node-0",
		Options: &option.TrojanOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "changed.example.com", ServerPort: 443},
			Password:      "changed-password",
		},
	}
	return document
}

func providerSnapshotShape(document []option.Outbound) map[string]option.Outbound {
	snapshot := make(map[string]option.Outbound, len(document))
	for index, node := range document {
		snapshot[measureTag(index)] = node
	}
	return snapshot
}

func recordStoreShape(t *testing.T, document []option.Outbound) (*Store, map[string]uint32) {
	t.Helper()
	builder := NewBuilder(1)
	entries := make(map[string]uint32, len(document))
	for index, node := range document {
		payload, err := Encode(node)
		require.NoError(t, err)
		recordIndex, err := builder.Intern(payload)
		require.NoError(t, err)
		entries[measureTag(index)] = recordIndex
	}
	return builder.Seal(), entries
}

// measureShape returns the retained delta for each shape with the rules described in the file header.
func measureShape(t *testing.T, nodes int, shape string) heapReading {
	t.Helper()
	switch shape {
	case "provider-snapshot":
		before := readHeap()
		document := providerDocument(nodes)
		snapshot := providerSnapshotShape(document)
		document = nil
		reading := readHeap().sub(before)
		runtime.KeepAlive(snapshot)
		return reading
	case "provider-store":
		before := readHeap()
		document := providerDocument(nodes)
		store, entries := recordStoreShape(t, document)
		document = nil
		reading := readHeap().sub(before)
		runtime.KeepAlive(store)
		runtime.KeepAlive(entries)
		return reading
	case "static-snapshot":
		document := providerDocument(nodes)
		before := readHeap()
		snapshot := providerSnapshotShape(document)
		reading := readHeap().sub(before)
		runtime.KeepAlive(document)
		runtime.KeepAlive(snapshot)
		return reading
	case "static-store":
		document := providerDocument(nodes)
		before := readHeap()
		store, entries := recordStoreShape(t, document)
		reading := readHeap().sub(before)
		runtime.KeepAlive(document)
		runtime.KeepAlive(store)
		runtime.KeepAlive(entries)
		return reading
	default:
		t.Fatalf("unknown shape %s", shape)
		return heapReading{}
	}
}

// measureRefresh returns (peak over the settled first generation, settled delta after the first
// generation was released). Peak is measured while the previous generation, the newly decoded
// document and the next generation are all alive, with runtime.KeepAlive pinning them across the
// collection.
func measureRefresh(t *testing.T, nodes int, shape string) (peak, settled heapReading) {
	t.Helper()
	var (
		firstStore     *Store
		firstEntries   map[string]uint32
		firstSnapshot  map[string]option.Outbound
		secondStore    *Store
		secondEntries  map[string]uint32
		secondSnapshot map[string]option.Outbound
	)
	func() {
		document := providerDocument(nodes)
		if shape == "provider-store" {
			firstStore, firstEntries = recordStoreShape(t, document)
		} else {
			firstSnapshot = providerSnapshotShape(document)
		}
	}()
	settledFirst := readHeap()
	var peakReading heapReading
	func() {
		document := changedProviderDocument(nodes)
		if shape == "provider-store" {
			secondStore, secondEntries = recordStoreShape(t, document)
		} else {
			secondSnapshot = providerSnapshotShape(document)
		}
		// The document is the third allocation at the peak; KeepAlive pins it across the two
		// collections the reading performs.
		peakReading = readHeap()
		runtime.KeepAlive(document)
	}()
	runtime.KeepAlive(secondStore)
	runtime.KeepAlive(secondEntries)
	runtime.KeepAlive(secondSnapshot)
	peak = peakReading.sub(settledFirst)
	// Phase three: release the previous generation; only the new one and the document owner remain.
	runtime.KeepAlive(firstEntries)
	runtime.KeepAlive(firstSnapshot)
	if firstStore != nil {
		firstStore.Close()
	}
	firstStore, firstEntries, firstSnapshot = nil, nil, nil
	settledReading := readHeap()
	runtime.KeepAlive(secondStore)
	runtime.KeepAlive(secondEntries)
	runtime.KeepAlive(secondSnapshot)
	settled = settledReading.sub(settledFirst)
	return peak, settled
}

func TestMeasureNodeStore(t *testing.T) {
	requireMeasure(t)
	for _, nodes := range []int{0, 100, 1000} {
		for _, shape := range []string{"provider-snapshot", "provider-store", "static-snapshot", "static-store"} {
			reading := measureShape(t, nodes, shape)
			perNode := int64(0)
			if nodes > 0 {
				perNode = reading.live / int64(nodes)
			}
			t.Logf("MEASURED shape=%s nodes=%d live=%d objects=%d inuse=%d live_per_node=%d",
				shape, nodes, reading.live, reading.objects, reading.inuse, perNode)
		}
	}
	const refreshNodes = 1000
	for _, shape := range []string{"provider-snapshot", "provider-store"} {
		peak, settled := measureRefresh(t, refreshNodes, shape)
		t.Logf("MEASURED refresh shape=%s nodes=%d peak_live=%d peak_objects=%d peak_live_per_node=%d settled_live=%d",
			shape, refreshNodes, peak.live, peak.objects, peak.live/refreshNodes, settled.live)
	}
}
