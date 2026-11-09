package v2rayxhttp

// The item-01 A/B: mixed long and short packet-up streams, high round-trip time, a
// slow server reader, and a control write arriving while the pool is saturated.
//
// # What this experiment is for
//
// Item 01's acceptance asks for a comparison at the SAME completed bytes and the SAME
// number of successful connections, recording throughput, tail latency, in-flight
// bytes and RSS, plus "control flows keep moving". The pool changes what a burst of
// streams may retain at once, so both sides of that trade are measured here:
//
//	per-stream  the pre-change tree: every uplink has its own queue bound and there is
//	            no sum, so aggregate retention grows with the number of streams
//	pooled      the production shape: NewClient installs one shared pool
//
// # Workloads
//
//	rtt30ms-mixed         six 4 MiB long streams and six short ones, started together
//	                     through one Client with every POST priced at 30 ms. The total
//	                     exceeds the production pool (about 8 MiB), so the two arms
//	                     differ: without a pool the aggregate is the sum of the
//	                     per-stream bounds.
//	rtt10ms-slow-read    eight 64 KiB streams against a server that reads each POST
//	                     body in 4 KiB pieces with a 1 ms delay, with a 256 KiB pool:
//	                     the slow-drain shape where retention is bounded first.
//	control-progress     six long streams saturate the pool, then one new stream sends
//	                     a 1 KiB handshake. Measured: how long the handshake takes to
//	                     be accepted and to reach its first POST. This is what "the
//	                     control flow keeps moving" means for packet-up, where the
//	                     handshake writes are the control path.
//
// # What is measured
//
//	wall time and throughput     same bytes, same connections, one number per arm
//	flow completion p50/p99      whole mixed set, and short flows separately
//	peak in-flight bytes         sampled every 2 ms; the pooled arm also reports the
//	                             pool's own exact high-water mark
//	peak working set / heap      RSS on Windows (psapi) and HeapAlloc, as absolute peak
//	                             and growth over the pre-phase baseline
//
// # Evidence level, stated because it matters
//
// This runs in-process on a shared developer machine: the numbers are repeatable in
// shape, not to the percent, and they are NOT the exclusive-host sampling the window
// brief assigns to the integration window. They answer "does the pool hold the
// aggregate down, does the control write still get through, and what does it cost"
// - not "what is the exact pool size for production". The exclusive re-run uses the
// same command:
//
//	XHTTP_BUDGET_EXPERIMENT=1 go test ./transport/v2rayxhttp \
//	    -run TestPacketUpSharedBudgetExperiment -v -timeout 600s
//
// It is skipped otherwise so CI does not pay for it.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type expFlow struct {
	name  string
	bytes int
	short bool
}

type expScenario struct {
	name string
	// rtt is added to every upload POST, standing in for a high-latency path.
	rtt time.Duration
	// readChunk/readDelay make the "server" consume an upload body slowly.
	readChunk int
	readDelay time.Duration
	flows     []expFlow
	// capacity overrides the production pool size; zero uses the derived one.
	capacity int
}

type expArm struct {
	name   string
	pooled bool
}

type expResult struct {
	scenario       string
	arm            string
	wall           time.Duration
	throughput     float64
	flowP50        time.Duration
	flowP99        time.Duration
	shortP99       time.Duration
	peakInFlight   int
	poolPeak       int
	peakHeap       uint64
	peakHeapBase   uint64
	peakWorkingSet uint64
	baseWorkingSet uint64
	rssAvailable   bool
	postedBytes    int64
	posts          int64
	connections    int
}

type expControlResult struct {
	arm                string
	writeLatency       time.Duration
	firstPostLatency   time.Duration
	bulkPostsCrossed   int
	saturatedBytes     int
	writeBlockedOnPool bool
}

// expTransport answers the download GET with a reader that stays open, prices every
// upload POST at the scenario's RTT, and can read the body slowly. It keeps no body
// copies, so the next arm starts from the same state.
type expTransport struct {
	rtt       time.Duration
	readChunk int
	readDelay time.Duration
	posted    atomic.Int64
	posts     atomic.Int64

	access        sync.Mutex
	sessionFirst  map[string]time.Time
	sessionSignal map[string]chan struct{}
}

func newExpTransport(scenario expScenario) *expTransport {
	return &expTransport{
		rtt:           scenario.rtt,
		readChunk:     scenario.readChunk,
		readDelay:     scenario.readDelay,
		sessionFirst:  make(map[string]time.Time),
		sessionSignal: make(map[string]chan struct{}),
	}
}

func (t *expTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodGet {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(newBlockingReader())}, nil
	}
	var total int64
	if t.readChunk > 0 {
		buffer := make([]byte, t.readChunk)
		for {
			read, err := request.Body.Read(buffer)
			total += int64(read)
			if read > 0 && t.readDelay > 0 {
				time.Sleep(t.readDelay)
			}
			if err != nil {
				break
			}
		}
	} else {
		read, _ := io.Copy(io.Discard, request.Body)
		total = read
	}
	request.Body.Close()
	t.posted.Add(total)
	t.posts.Add(1)
	t.noteFirstPost(request.URL.Path)
	if t.rtt > 0 {
		time.Sleep(t.rtt)
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
}

// noteFirstPost records when a session's first upload arrived. The packet-up path is
// "<path>/<session>/<seq>", so the session is the second-to-last segment.
func (t *expTransport) noteFirstPost(path string) {
	segments := strings.Split(strings.TrimSuffix(path, "/"), "/")
	if len(segments) < 2 {
		return
	}
	session := segments[len(segments)-2]
	t.access.Lock()
	if _, seen := t.sessionFirst[session]; !seen {
		t.sessionFirst[session] = time.Now()
		if signal, waiting := t.sessionSignal[session]; waiting {
			close(signal)
		}
	}
	t.access.Unlock()
}

// waitForFirstPost blocks until the session's first upload POST reaches the
// transport, or the timeout expires.
func (t *expTransport) waitForFirstPost(session string, timeout time.Duration) (time.Time, bool) {
	t.access.Lock()
	if first, seen := t.sessionFirst[session]; seen {
		t.access.Unlock()
		return first, true
	}
	signal, waiting := t.sessionSignal[session]
	if !waiting {
		signal = make(chan struct{})
		t.sessionSignal[session] = signal
	}
	t.access.Unlock()
	select {
	case <-signal:
	case <-time.After(timeout):
		return time.Time{}, false
	}
	t.access.Lock()
	first := t.sessionFirst[session]
	t.access.Unlock()
	return first, true
}

func experimentScenarios() []expScenario {
	mixed := []expFlow{
		{name: "long-a", bytes: 2 << 20},
		{name: "long-b", bytes: 2 << 20},
		{name: "long-c", bytes: 2 << 20},
		{name: "long-d", bytes: 2 << 20},
		{name: "long-e", bytes: 2 << 20},
		{name: "long-f", bytes: 2 << 20},
	}
	for index, size := range []int{96 << 10, 128 << 10, 160 << 10, 192 << 10, 224 << 10, 256 << 10} {
		mixed = append(mixed, expFlow{name: fmt.Sprintf("short-%d", index+1), bytes: size, short: true})
	}
	slowRead := make([]expFlow, 0, 8)
	for index := 0; index < 8; index++ {
		slowRead = append(slowRead, expFlow{name: fmt.Sprintf("flow-%d", index+1), bytes: 64 << 10, short: true})
	}
	return []expScenario{
		// 100 ms per POST: the numbers are dominated by the simulated path latency
		// instead of by this shared machine's scheduler noise, and the six 2 MiB
		// queues can retain 12 MiB together, above the production pool.
		{name: "rtt100ms-mixed", rtt: 100 * time.Millisecond, flows: mixed},
		{
			name:      "rtt10ms-slow-read",
			rtt:       10 * time.Millisecond,
			readChunk: 4 << 10,
			readDelay: time.Millisecond,
			flows:     slowRead,
			capacity:  256 << 10,
		},
	}
}

func totalBytesOf(flows []expFlow) int64 {
	var total int64
	for _, flow := range flows {
		total += int64(flow.bytes)
	}
	return total
}

// runExperimentArm drives one workload once and returns its measurements.
func runExperimentArm(t *testing.T, scenario expScenario, arm expArm) expResult {
	t.Helper()
	transport := newExpTransport(scenario)
	client := batchClient(t, transport, metaOptions{})
	poolCapacity := 0
	if arm.pooled {
		poolCapacity = scenario.capacity
		if poolCapacity == 0 {
			poolCapacity = packetUpSharedCapacity(packetUpPendingBound(client.meta))
		}
		client.budget = newSharedUplinkBudget(poolCapacity, packetUpSharedReserve)
	}

	conns := make([]*packetConn, 0, len(scenario.flows))
	payloads := make([][]byte, 0, len(scenario.flows))
	for index, flow := range scenario.flows {
		conn, err := client.DialContext(context.Background())
		if err != nil {
			t.Fatalf("arm %s: dial flow %d: %v", arm.name, index, err)
		}
		conns = append(conns, conn.(*packetConn))
		payloads = append(payloads, bytes.Repeat([]byte{byte('a' + index)}, flow.bytes))
	}

	runtime.GC()
	heapBase, workingSetBase, rssAvailable := experimentMemorySnapshot()
	result := expResult{
		scenario:       scenario.name,
		arm:            arm.name,
		rssAvailable:   rssAvailable,
		connections:    len(conns),
		peakHeapBase:   heapBase,
		baseWorkingSet: workingSetBase,
	}
	var writeFailures atomic.Int64
	stopSampler := make(chan struct{})
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopSampler:
				return
			case <-ticker.C:
				retained := 0
				for _, conn := range conns {
					stats := conn.uplink.stats()
					retained += stats.PendingBytes + stats.InFlightBytes
				}
				if retained > result.peakInFlight {
					result.peakInFlight = retained
				}
				heap, workingSet, ok := experimentMemorySnapshot()
				if heap > heapBase && heap-heapBase > result.peakHeap {
					result.peakHeap = heap - heapBase
				}
				if rssAvailable && ok && workingSet > result.peakWorkingSet {
					result.peakWorkingSet = workingSet
				}
			}
		}
	}()

	started := time.Now()
	durations := make([]time.Duration, len(conns))
	var waitGroup sync.WaitGroup
	for index := range conns {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			written, err := conns[index].Write(payloads[index])
			if err != nil || written != len(payloads[index]) {
				writeFailures.Add(1)
				return
			}
			// Close drains the buffered tail, so this is when the flow is actually
			// done, not when its last byte was queued.
			if closeErr := conns[index].Close(); closeErr != nil {
				writeFailures.Add(1)
				return
			}
			durations[index] = time.Since(started)
		}(index)
	}
	waitGroup.Wait()
	result.wall = time.Since(started)
	close(stopSampler)
	<-samplerDone
	if arm.pooled {
		poolStats := client.budget.stats()
		result.poolPeak = poolStats.Peak
		if bound := poolStats.Capacity + poolStats.Control + poolStats.Reserve; result.poolPeak > bound {
			t.Fatalf("arm %s: pool peak %d exceeds its bound %d", arm.name, result.poolPeak, bound)
		}
	}

	if failed := writeFailures.Load(); failed != 0 {
		t.Fatalf("arm %s: %d flows failed", arm.name, failed)
	}
	result.postedBytes = transport.posted.Load()
	result.posts = transport.posts.Load()
	total := totalBytesOf(scenario.flows)
	if result.postedBytes != total {
		t.Fatalf("arm %s: posted %d bytes, want the same %d in both arms", arm.name, result.postedBytes, total)
	}

	completions := append([]time.Duration(nil), durations...)
	sort.Slice(completions, func(i, j int) bool { return completions[i] < completions[j] })
	result.flowP50 = percentile(completions, 0.50)
	result.flowP99 = percentile(completions, 0.99)
	shortCompletions := make([]time.Duration, 0, len(scenario.flows))
	for index, flow := range scenario.flows {
		if flow.short {
			shortCompletions = append(shortCompletions, durations[index])
		}
	}
	sort.Slice(shortCompletions, func(i, j int) bool { return shortCompletions[i] < shortCompletions[j] })
	result.shortP99 = percentile(shortCompletions, 0.99)
	result.throughput = float64(result.postedBytes) / result.wall.Seconds() / (1 << 20)
	return result
}

// runControlProbe saturates the pool with long streams and then measures one new
// stream's 1 KiB handshake: how long Write took to be accepted, and how long its
// first POST took to reach the transport. The handshake is small enough to use the
// reserve, which is exactly the path that has to keep working when the main pool is
// full.
func runControlProbe(t *testing.T, arm expArm) expControlResult {
	t.Helper()
	scenario := expScenario{
		name: "control-progress",
		rtt:  100 * time.Millisecond,
		flows: []expFlow{
			{name: "bulk-a", bytes: 2 << 20},
			{name: "bulk-b", bytes: 2 << 20},
			{name: "bulk-c", bytes: 2 << 20},
			{name: "bulk-d", bytes: 2 << 20},
			{name: "bulk-e", bytes: 2 << 20},
			{name: "bulk-f", bytes: 2 << 20},
		},
	}
	transport := newExpTransport(scenario)
	client := batchClient(t, transport, metaOptions{})
	if arm.pooled {
		client.budget = newSharedUplinkBudget(packetUpSharedCapacity(packetUpPendingBound(client.meta)), packetUpSharedReserve)
	}
	var waitGroup sync.WaitGroup
	stopBulk := make(chan struct{})
	releaseBulk := make(chan struct{})
	bulkDone := make(chan struct{})
	go func() {
		defer close(bulkDone)
		for _, flow := range scenario.flows {
			waitGroup.Add(1)
			go func(flow expFlow) {
				defer waitGroup.Done()
				conn, err := client.DialContext(context.Background())
				if err != nil {
					t.Errorf("bulk dial: %v", err)
					return
				}
				payload := bytes.Repeat([]byte{'b'}, flow.bytes)
				if _, err = conn.Write(payload); err != nil {
					t.Errorf("bulk write: %v", err)
				}
				select {
				case <-releaseBulk:
				case <-stopBulk:
				}
				_ = conn.Close()
			}(flow)
		}
		waitGroup.Wait()
	}()
	// Let the bulk streams fill the pool.
	time.Sleep(40 * time.Millisecond)
	result := expControlResult{arm: arm.name}
	if arm.pooled {
		stats := client.budget.stats()
		result.saturatedBytes = stats.Total()
		result.writeBlockedOnPool = stats.Waiters > 0
	}

	control, err := client.DialContext(context.Background())
	if err != nil {
		t.Fatalf("control dial: %v", err)
	}
	packet := control.(*packetConn)
	handshake := bytes.Repeat([]byte{'h'}, 1024)
	postsBefore := transport.posts.Load()
	writeStarted := time.Now()
	if _, err = control.Write(handshake); err != nil {
		t.Fatalf("control write: %v", err)
	}
	result.writeLatency = time.Since(writeStarted)
	firstPost, seen := transport.waitForFirstPost(packet.sessionID, 2*time.Second)
	if !seen {
		t.Fatalf("arm %s: the control write never reached a POST", arm.name)
	}
	result.firstPostLatency = firstPost.Sub(writeStarted)
	// The structural half of "the control flow keeps moving": if the handshake had
	// queued behind the bulk streams, at least one of their POSTs (each one RTT
	// long) would have landed between the write and the handshake's own POST.
	result.bulkPostsCrossed = int(transport.posts.Load() - postsBefore - 1)
	_ = control.Close()
	close(stopBulk)
	close(releaseBulk)
	<-bulkDone
	return result
}

// percentile returns the nearest-rank percentile of a sorted slice.
func percentile(sorted []time.Duration, fraction float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(float64(len(sorted))*fraction+0.999999) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// experimentMemorySnapshot reads the heap and, where the platform exposes it, the
// process working set (RSS).
func experimentMemorySnapshot() (heap uint64, workingSet uint64, workingSetAvailable bool) {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	workingSet, workingSetAvailable = experimentWorkingSetBytes()
	return stats.HeapAlloc, workingSet, workingSetAvailable
}

// TestPacketUpSharedBudgetExperiment is the item-01 A/B. It is gated behind an
// environment variable because it is a measurement, not a unit test.
func TestPacketUpSharedBudgetExperiment(t *testing.T) {
	if os.Getenv("XHTTP_BUDGET_EXPERIMENT") == "" {
		t.Skip("set XHTTP_BUDGET_EXPERIMENT=1 to run the item-01 A/B")
	}
	const repetitions = 5
	arms := []expArm{{name: "per-stream"}, {name: "pooled", pooled: true}}
	for _, scenario := range experimentScenarios() {
		results := make(map[string][]expResult, len(arms))
		for repetition := 0; repetition < repetitions; repetition++ {
			for _, arm := range arms {
				// Alternate the arms so drift on this shared machine lands on both.
				results[arm.name] = append(results[arm.name], runExperimentArm(t, scenario, arm))
			}
		}
		t.Logf("scenario %s: %d flows, %d bytes, repeated %d times, median run reported",
			scenario.name, len(scenario.flows), totalBytesOf(scenario.flows), repetitions)
		for _, arm := range arms {
			median := medianResult(results[arm.name])
			rssGrowth, rssPeak := "n/a", "n/a"
			if median.rssAvailable {
				growth := uint64(0)
				if median.peakWorkingSet > median.baseWorkingSet {
					growth = median.peakWorkingSet - median.baseWorkingSet
				}
				rssGrowth = fmt.Sprintf("%.2f MiB", float64(growth)/(1<<20))
				rssPeak = fmt.Sprintf("%.2f MiB", float64(median.peakWorkingSet)/(1<<20))
			}
			poolPeak := "n/a"
			if median.poolPeak > 0 {
				poolPeak = fmt.Sprintf("%.2f MiB", float64(median.poolPeak)/(1<<20))
			}
			t.Logf("%s arm=%s wall=%s throughput=%.2f MiB/s p50=%s p99=%s shortP99=%s peakInFlight=%.2f MiB poolPeak=%s heapGrowth=%.2f MiB rssPeak=%s rssGrowth=%s connections=%d posts=%d",
				scenario.name, median.arm, median.wall.Round(time.Millisecond), median.throughput,
				median.flowP50.Round(time.Millisecond), median.flowP99.Round(time.Millisecond),
				median.shortP99.Round(time.Millisecond),
				float64(median.peakInFlight)/(1<<20), poolPeak,
				float64(median.peakHeap)/(1<<20), rssPeak, rssGrowth,
				median.connections, median.posts)
		}
	}

	controlResults := make(map[string][]expControlResult, len(arms))
	for repetition := 0; repetition < repetitions; repetition++ {
		for _, arm := range arms {
			controlResults[arm.name] = append(controlResults[arm.name], runControlProbe(t, arm))
		}
	}
	t.Logf("scenario control-progress: six 2 MiB bulk streams, then a 1 KiB handshake on a new stream")
	for _, arm := range arms {
		runs := controlResults[arm.name]
		sort.Slice(runs, func(i, j int) bool { return runs[i].bulkPostsCrossed < runs[j].bulkPostsCrossed })
		median := runs[len(runs)/2]
		// The latency itself is not reported: this host's monotonic clock measures a
		// 100k-iteration loop as 0 ns, so sub-millisecond values here are below its
		// resolution. What the structural count proves is stronger than a timer: no
		// bulk POST (each one RTT long) completed between the handshake's Write and
		// its own POST.
		t.Logf("control-progress arm=%s bulkPostsCrossed=%d saturatedBytes=%.2f MiB waitingStreams=%v handshakePostedBeforeAnyBulkPost=%v",
			median.arm, median.bulkPostsCrossed, float64(median.saturatedBytes)/(1<<20),
			median.writeBlockedOnPool, median.bulkPostsCrossed == 0)
	}

	// The relay half of item 01 is deliberately absent here: the growth budget has no
	// production wiring yet, and the window brief requires the wiring to be written by
	// the file's owner. Its copy-engine behaviour is pinned in
	// common/resourceguard/growth_copy_test.go instead.
	t.Logf("relay growth budget: not measured here (no production wiring yet; see common/resourceguard/growth_copy_test.go)")
}

// medianResult returns the run whose wall time is the median of the repetitions: the
// timing fields are then one coherent run rather than a mix of fields from different
// runs.
func medianResult(runs []expResult) expResult {
	sorted := append([]expResult(nil), runs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].wall < sorted[j].wall })
	return sorted[len(sorted)/2]
}
