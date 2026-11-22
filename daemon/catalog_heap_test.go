package daemon

// Full-Box heap harness for the Catalog A/C comparison. It is opt-in and one process per sample:
// the service is started with N static SOCKS nodes, the live heap is read after two GCs, and the
// process exits. Run the same file in a baseline worktree (no catalog) and in the candidate; the
// difference is the real Box-level cost of the representation, not a descriptor microbenchmark.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type catalogHeapRow struct {
	Arm         string `json:"arm"`
	Protocol    string `json:"protocol"`
	Nodes       int    `json:"nodes"`
	Sample      int    `json:"sample"`
	Millis      int64  `json:"millis"`
	LiveHeap    uint64 `json:"live_heap"`
	HeapObjects uint64 `json:"heap_objects"`
	TotalAlloc  uint64 `json:"total_alloc"`
	Records     int    `json:"records"`
	Refs        int    `json:"refs"`
}

func TestCatalogBoxHeapSample(t *testing.T) {
	nodes, err := strconv.Atoi(os.Getenv("CATALOG_HEAP_NODES"))
	if err != nil || nodes <= 0 {
		t.Skip("set CATALOG_HEAP_NODES to run the Box heap harness")
	}
	arm := os.Getenv("CATALOG_HEAP_ARM")
	protocol := os.Getenv("CATALOG_HEAP_PROTOCOL")
	if protocol == "" {
		protocol = "socks"
	}
	sample, _ := strconv.Atoi(os.Getenv("CATALOG_HEAP_SAMPLE"))
	var outbounds strings.Builder
	for i := 0; i < nodes; i++ {
		if i > 0 {
			outbounds.WriteByte(',')
		}
		switch protocol {
		case "http":
			fmt.Fprintf(&outbounds, `{"type":"http","tag":"n%d","server":"192.0.2.2","server_port":%d,"version":1}`, i, 10000+i)
		default:
			fmt.Fprintf(&outbounds, `{"type":"socks","tag":"n%d","server":"192.0.2.1","server_port":%d,"version":"5"}`, i, 10000+i)
		}
	}
	profile := fmt.Sprintf(`{
  "log": {"disabled": true},
  "outbounds": [%s, {"type":"direct","tag":"direct"}],
  "route": {"final": "direct"}
}`, outbounds.String())

	started := time.Now()
	ctx := crossSystemContext(t, t.TempDir(), nil)
	service := NewStartedService(ServiceOptions{Context: ctx})
	if err := service.StartOrReloadService(context.Background(), profile, nil); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	runtime.GC()
	runtime.GC()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	row := catalogHeapRow{
		Arm: arm, Protocol: protocol, Nodes: nodes, Sample: sample, Millis: elapsed.Milliseconds(),
		LiveHeap: mem.HeapAlloc, HeapObjects: mem.HeapObjects, TotalAlloc: mem.TotalAlloc,
	}
	if instance := service.Instance(); instance != nil {
		if method := reflect.ValueOf(instance.Box()).MethodByName("CatalogStats"); method.IsValid() {
			results := method.Call(nil)
			if len(results) == 1 {
				stats := results[0]
				row.Records = int(stats.FieldByName("Records").Int())
				row.Refs = int(stats.FieldByName("Refs").Int())
			}
		}
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("HEAP " + string(encoded))
	if err := service.CloseService(); err != nil {
		t.Fatal(err)
	}
	service.Close()
}
