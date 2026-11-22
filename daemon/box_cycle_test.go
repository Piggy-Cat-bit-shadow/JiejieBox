package daemon

// 30 night-cycle harness: repeated New -> Start -> Close of a real Box with HTTP/1.x Catalog
// nodes. It is opt-in and records natural settled values; it does not force GC in the product.
// The assertion is intentionally conservative: after the last close the goroutine count must not
// grow monotonically with the number of cycles.

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func cycleProfile(count int) string {
	var outbounds strings.Builder
	for index := 0; index < count; index++ {
		if index > 0 {
			outbounds.WriteByte(',')
		}
		fmt.Fprintf(&outbounds, `{"type":"http","tag":"n%d","server":"192.0.2.10","server_port":%d,"version":1}`, index, 10000+index)
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

func TestBoxCycleSettles(t *testing.T) {
	rounds, _ := strconv.Atoi(os.Getenv("CYCLE_ROUNDS"))
	if rounds <= 0 {
		t.Skip("set CYCLE_ROUNDS to run the Box cycle harness")
	}
	nodes := 10
	if raw := os.Getenv("CYCLE_NODES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			t.Skip("CYCLE_NODES must be positive")
		}
		nodes = parsed
	}
	profile := cycleProfile(nodes)
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	baselineGoroutines := runtime.NumGoroutine()
	for round := 1; round <= rounds; round++ {
		roundStarted := time.Now()
		ctx := crossSystemContext(t, t.TempDir(), nil)
		service := NewStartedService(ServiceOptions{Context: ctx})
		if err := service.StartOrReloadService(context.Background(), profile, nil); err != nil {
			service.Close()
			t.Fatalf("round %d: %v", round, err)
		}
		if err := service.CloseService(); err != nil {
			service.Close()
			t.Fatalf("round %d close: %v", round, err)
		}
		service.Close()
		if round%10 == 0 || round == rounds {
			runtime.GC()
			var current runtime.MemStats
			runtime.ReadMemStats(&current)
			fmt.Printf(`CYCLE {"round":%d,"nodes":%d,"goroutines":%d,"live_heap":%d,"total_alloc":%d,"millis":%d}`+"\n",
				round, nodes, runtime.NumGoroutine(), current.HeapAlloc, current.TotalAlloc-baseline.TotalAlloc,
				time.Since(roundStarted).Milliseconds())
		}
	}
	runtime.GC()
	finalGoroutines := runtime.NumGoroutine()
	if finalGoroutines > baselineGoroutines+8 {
		t.Fatalf("goroutines grew from %d to %d over %d cycles", baselineGoroutines, finalGoroutines, rounds)
	}
}
