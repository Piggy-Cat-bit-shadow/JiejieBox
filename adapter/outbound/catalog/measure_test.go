//go:build !race

package catalog_test

// Measurement harness for the A/B/C arms. It is opt-in (CATALOG_MEASURE_ROOT) and one process per
// sample, so a caller can run five independent subprocesses per arm without the test framework's
// other tests sharing the heap.
//
// A = eager constructor (the pre-existing cost); B = the same protocol behind an inline Lazy recipe
// with the full options value (the "just delay construction" control); C = shared record + descriptor
// (this round). B->C is the representation-layer result; A->C is the total.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/adapter/outbound/catalog"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/http"
	"github.com/sagernet/sing-box/protocol/socks"
	"github.com/sagernet/sing-box/protocol/trojan"
	N "github.com/sagernet/sing/common/network"
)

type measureRow struct {
	Arm         string `json:"arm"`
	Protocol    string `json:"protocol"`
	Nodes       int    `json:"nodes"`
	Sample      int    `json:"sample"`
	Millis      int64  `json:"millis"`
	LiveHeap    uint64 `json:"live_heap"`
	HeapObjects uint64 `json:"heap_objects"`
	TotalAlloc  uint64 `json:"total_alloc"`
	Records     int    `json:"records"`
	RecordBytes int    `json:"record_bytes"`
	RecordRefs  int    `json:"record_refs"`
	Err         string `json:"err,omitempty"`
}

func TestCatalogMeasureSample(t *testing.T) {
	if os.Getenv("CATALOG_MEASURE_ROOT") == "" {
		t.Skip("set CATALOG_MEASURE_ROOT to run the catalog measurement harness")
	}
	arm := os.Getenv("CATALOG_MEASURE_ARM")
	protocol := os.Getenv("CATALOG_MEASURE_PROTOCOL")
	nodes, err := strconv.Atoi(os.Getenv("CATALOG_MEASURE_NODES"))
	if err != nil || nodes <= 0 {
		t.Fatal("CATALOG_MEASURE_NODES must be a positive integer")
	}
	sample, _ := strconv.Atoi(os.Getenv("CATALOG_MEASURE_SAMPLE"))
	row := measureRow{Arm: arm, Protocol: protocol, Nodes: nodes, Sample: sample}
	started := time.Now()

	var store *catalog.Store
	var objects []adapter.Outbound
	switch arm {
	case "empty":
	case "A":
		for i := 0; i < nodes; i++ {
			object, buildErr := eagerOutbound(protocol, i)
			if buildErr != nil {
				row.Err = buildErr.Error()
				break
			}
			objects = append(objects, object)
		}
	case "B":
		for i := 0; i < nodes; i++ {
			object, buildErr := inlineLazyOutbound(protocol, i)
			if buildErr != nil {
				row.Err = buildErr.Error()
				break
			}
			objects = append(objects, object)
		}
	case "C":
		store = catalog.NewStore()
		constructor := catalog.NewConstructor(store, catalogProtocol(protocol))
		for i := 0; i < nodes; i++ {
			options := catalogOptions(protocol, i)
			object, handled, buildErr := constructor.Deferred(context.Background(), nil, log.NewNOPFactory().Logger(),
				fmt.Sprintf("node-%d", i), protocol, options, "")
			if buildErr != nil {
				row.Err = buildErr.Error()
				break
			}
			if !handled {
				row.Err = "shape not admitted"
				break
			}
			objects = append(objects, object)
		}
	default:
		t.Fatalf("unknown arm %q", arm)
	}
	row.Millis = time.Since(started).Milliseconds()
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	row.LiveHeap = stats.HeapAlloc
	row.HeapObjects = stats.HeapObjects
	row.TotalAlloc = stats.TotalAlloc
	if store != nil {
		catalogStats := store.Stats()
		row.Records = catalogStats.Records
		row.RecordBytes = catalogStats.Bytes
		row.RecordRefs = catalogStats.Refs
	}
	runtime.KeepAlive(objects)
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("MEASURE " + string(encoded))
}

func catalogProtocol(protocol string) catalog.Protocol {
	switch protocol {
	case C.TypeSOCKS:
		return socks.CatalogProtocol()
	case C.TypeHTTP:
		return http.CatalogProtocol()
	case C.TypeTrojan:
		return trojan.CatalogProtocol()
	}
	panic("unknown protocol " + protocol)
}

func catalogOptions(protocol string, index int) any {
	switch protocol {
	case C.TypeSOCKS:
		return socksOptionsAt(index)
	case C.TypeHTTP:
		return httpOptionsAt(index)
	case C.TypeTrojan:
		return trojanOptionsAt(index)
	}
	panic("unknown protocol " + protocol)
}

func socksOptionsAt(index int) *option.SOCKSOutboundOptions {
	return &option.SOCKSOutboundOptions{
		ServerOptions: option.ServerOptions{Server: "192.0.2.1", ServerPort: uint16(10000 + index)},
		Version:       "5",
		Network:       option.NetworkList(N.NetworkTCP + "\n" + N.NetworkUDP),
	}
}

func httpOptionsAt(index int) *option.HTTPOutboundOptions {
	return &option.HTTPOutboundOptions{
		ServerOptions: option.ServerOptions{Server: "192.0.2.2", ServerPort: uint16(10000 + index)},
		Version:       1,
	}
}

func trojanOptionsAt(index int) *option.TrojanOutboundOptions {
	return &option.TrojanOutboundOptions{
		ServerOptions: option.ServerOptions{Server: "192.0.2.3", ServerPort: uint16(10000 + index)},
		Password:      "secret",
		OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{
			Enabled:    true,
			ServerName: "example.com",
		}},
	}
}

func eagerOutbound(protocol string, index int) (adapter.Outbound, error) {
	logger := log.NewNOPFactory().Logger()
	switch protocol {
	case C.TypeSOCKS:
		return socks.NewOutbound(context.Background(), nil, logger, fmt.Sprintf("node-%d", index), *socksOptionsAt(index))
	case C.TypeHTTP:
		return http.NewOutbound(context.Background(), nil, logger, fmt.Sprintf("node-%d", index), *httpOptionsAt(index))
	case C.TypeTrojan:
		return trojan.NewOutbound(context.Background(), nil, logger, fmt.Sprintf("node-%d", index), *trojanOptionsAt(index))
	}
	return nil, fmt.Errorf("unknown protocol %s", protocol)
}

func inlineLazyOutbound(protocol string, index int) (adapter.Outbound, error) {
	logger := log.NewNOPFactory().Logger()
	tag := fmt.Sprintf("node-%d", index)
	switch protocol {
	case C.TypeSOCKS:
		recipe := *socksOptionsAt(index)
		owner := outbound.NewAdapter(C.TypeSOCKS, tag, []string{N.NetworkTCP, N.NetworkUDP}, nil)
		return outbound.NewLazyOutbound[option.SOCKSOutboundOptions](owner, logger, context.Background(), recipe,
			func(ctx context.Context, value option.SOCKSOutboundOptions) (adapter.Outbound, error) {
				return socks.NewOutbound(ctx, nil, logger, tag, value)
			}), nil
	case C.TypeHTTP:
		recipe := *httpOptionsAt(index)
		owner := outbound.NewAdapter(C.TypeHTTP, tag, []string{N.NetworkTCP, N.NetworkUDP}, nil)
		return outbound.NewLazyOutbound[option.HTTPOutboundOptions](owner, logger, context.Background(), recipe,
			func(ctx context.Context, value option.HTTPOutboundOptions) (adapter.Outbound, error) {
				return http.NewOutbound(ctx, nil, logger, tag, value)
			}), nil
	case C.TypeTrojan:
		constructor := trojan.DeferredConstructor()
		object, handled, err := constructor(context.Background(), nil, logger, tag, C.TypeTrojan, trojanOptionsAt(index), "")
		if err != nil {
			return nil, err
		}
		if !handled {
			return nil, fmt.Errorf("trojan lazy shape not admitted")
		}
		return object, nil
	}
	return nil, fmt.Errorf("unknown protocol %s", protocol)
}
