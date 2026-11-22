package daemon

// G08 host harness: whole-Box reload peak. It is opt-in and one process per sample. The service is
// started with N static SOCKS nodes, then StartOrReloadService replaces the whole configuration K
// times with one node changed. A sampler records HeapAlloc during each reload so the transient
// old+new overlap is visible, not only the settled heap.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func reloadProfile(nodes int, changedPort int) string {
	var outbounds strings.Builder
	for i := 0; i < nodes; i++ {
		if i > 0 {
			outbounds.WriteByte(',')
		}
		port := 10000 + i
		if i == 0 {
			port = changedPort
		}
		fmt.Fprintf(&outbounds, `{"type":"socks","tag":"n%d","server":"192.0.2.1","server_port":%d,"version":"5"}`, i, port)
	}
	list := outbounds.String()
	if list != "" {
		list += ","
	}
	return fmt.Sprintf(`{
  "log": {"disabled": true},
  "outbounds": [%s{"type":"direct","tag":"direct"}],
  "route": {"final": "direct"}
}`, list)
}

func TestMemoryReloadPeakSample(t *testing.T) {
	nodes, _ := strconv.Atoi(os.Getenv("G08_NODES"))
	rounds, _ := strconv.Atoi(os.Getenv("G08_ROUNDS"))
	if nodes <= 0 || rounds <= 0 {
		t.Skip("set G08_NODES and G08_ROUNDS to run the reload peak harness")
	}
	sample, _ := strconv.Atoi(os.Getenv("G08_SAMPLE"))
	ctx := crossSystemContext(t, t.TempDir(), nil)
	service := NewStartedService(ServiceOptions{Context: ctx})
	if err := service.StartOrReloadService(context.Background(), reloadProfile(nodes, 10000), nil); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = service.CloseService()
		service.Close()
	}()

	for round := 1; round <= rounds; round++ {
		var stop atomic.Bool
		var peak atomic.Uint64
		var sampler sync.WaitGroup
		sampler.Add(1)
		go func() {
			defer sampler.Done()
			var stats runtime.MemStats
			for !stop.Load() {
				runtime.ReadMemStats(&stats)
				for {
					old := peak.Load()
					if stats.HeapAlloc <= old || peak.CompareAndSwap(old, stats.HeapAlloc) {
						break
					}
				}
				time.Sleep(time.Millisecond)
			}
		}()
		started := time.Now()
		if err := service.StartOrReloadService(context.Background(), reloadProfile(nodes, 20000+round), nil); err != nil {
			stop.Store(true)
			sampler.Wait()
			t.Fatal(err)
		}
		elapsed := time.Since(started)
		stop.Store(true)
		sampler.Wait()
		runtime.GC()
		var settled runtime.MemStats
		runtime.ReadMemStats(&settled)
		row, _ := json.Marshal(map[string]any{
			"nodes": nodes, "round": round, "sample": sample, "millis": elapsed.Milliseconds(),
			"peak": peak.Load(), "settled": settled.HeapAlloc,
		})
		fmt.Println("RELOAD " + string(row))
	}
}
