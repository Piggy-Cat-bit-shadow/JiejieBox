package w8measure

// W2 —real relay copy path, measured through route.ConnectionManager.NewConnection.
//
// # What makes this the product path
//
// Every flow below is a REAL proxied connection created by route.ConnectionManager.
// NewConnection: the manager dials an outbound end, starts its two copy goroutines, and
// the copy engine (sing's bufio copy loop) reads the inbound socket. The measurement does
// not call the copy engine directly and does not call GrowthBudget.Admit directly; the
// only product objects it touches are the manager, its installed budget, and sockets.
//
// The budget is installed exactly as production installs it: manager.Start() reads the
// process guard from the start scope and builds resourceguard.NewGrowthBudget with
// DefaultGrowthConfig and the guard's Level as the pressure source. The harness then
// READS the budget's stats through a read-only reflection of the manager's private
// `growth` field (there is no exported accessor in production). That is a measurement
// lens, not a behaviour change: no field is written, and the harness fails loudly if the
// field is renamed rather than silently measuring nothing.
//
// # Scenarios
//
//	quietbulk   128 quiet bidirectional flows (256 directions) + one bulk flow that copies
//	            16 MiB each way, then a fresh flow for per-chunk latency. Answers: does a
//	            quiet direction hold a permit; does the bulk direction still grow; what does
//	            growth cost/earn at EQUAL completed bytes.
//	pressure    Armed: 128 grown directions, then a normal direction (must be refused) and an
//	            interactive direction (must draw the reserve), then Critical (new growth
//	            refused, existing permits NOT revoked).
//	geometry    Critical (zero ordinary plan): a geometry-required writer still grows
//	            immediately and is counted in GeometryNeeded instead of the ordinary cap.
//	bulkquiet   bulk first, then everything closed except the quiet flows: InUse returns to
//	            zero (bulk->quiet) and every byte is hash-verified.
//
// Metrics per row: GrowthStats snapshots, expanded direction count, completed bytes and
// sha256 per direction, wall time, upload/download read syscalls (probe) plus the process
// OS read/write operation deltas, p95/p99 chunk latency, Go heap and process RSS peak.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/route"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/buf"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Probe connection: the production read-waiter seam with counters.
// ---------------------------------------------------------------------------

type w8ProbeConn struct {
	net.Conn

	access     sync.Mutex
	options    N.ReadWaitOptions
	reads      int
	grownReads int
	expanded   int
	bytesRead  int64
	early      bool
}

// InitializeReadWaiter is the production ReadWaiter entry point: the copy loop calls it
// with the options it is about to read with, and calls it AGAIN with IncreaseBuffer=true
// once the direction has actually been allowed to grow. Counting the second form is how
// "did this direction expand" is observed without touching the budget.
func (c *w8ProbeConn) InitializeReadWaiter(options N.ReadWaitOptions) bool {
	c.access.Lock()
	c.options = options
	if options.IncreaseBuffer {
		c.expanded++
	}
	c.access.Unlock()
	return false
}

func (c *w8ProbeConn) WaitReadBuffer() (*buf.Buffer, error) {
	c.access.Lock()
	options := c.options
	c.access.Unlock()
	for {
		buffer := options.NewBuffer()
		readN, err := c.Conn.Read(buffer.FreeBytes())
		if readN > 0 {
			buffer.Truncate(readN)
			c.access.Lock()
			c.reads++
			c.bytesRead += int64(readN)
			if options.IncreaseBuffer {
				c.grownReads++
			}
			c.access.Unlock()
			return buffer, nil
		}
		buffer.Release()
		if err != nil {
			return nil, err
		}
	}
}

// EarlyCopyBufferGrowth is the production geometry seam (adapter.CopyBufferGrowthTuner),
// the same one protocol/naive's padded writer implements.
func (c *w8ProbeConn) EarlyCopyBufferGrowth() bool { return c.early }

func (c *w8ProbeConn) counters() (reads int, grown int, expanded int, bytes int64) {
	c.access.Lock()
	defer c.access.Unlock()
	return c.reads, c.grownReads, c.expanded, c.bytesRead
}

// ---------------------------------------------------------------------------
// Dialer and flow pair
// ---------------------------------------------------------------------------

type w8ProbeDialer struct{ pending chan net.Conn }

func (d *w8ProbeDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	select {
	case conn := <-d.pending:
		return conn, nil
	case <-time.After(10 * time.Second):
		return nil, io.ErrNoProgress
	}
}

func (d *w8ProbeDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

type w8RelayPair struct {
	app       net.Conn
	inbound   *w8ProbeConn
	outbound  *w8ProbeConn
	peer      net.Conn
	closeOnce sync.Once
}

func w8NewPair(t *testing.T, onTCP bool) *w8RelayPair {
	t.Helper()
	var app, inbound, peer, outbound net.Conn
	if onTCP {
		app, inbound = w8LoopbackPair(t)
		peer, outbound = w8LoopbackPair(t)
	} else {
		app, inbound = net.Pipe()
		peer, outbound = net.Pipe()
	}
	pair := &w8RelayPair{
		app:      app,
		inbound:  &w8ProbeConn{Conn: inbound},
		outbound: &w8ProbeConn{Conn: outbound},
		peer:     peer,
	}
	t.Cleanup(pair.close)
	return pair
}

func (p *w8RelayPair) close() {
	p.closeOnce.Do(func() {
		_ = p.app.Close()
		_ = p.peer.Close()
		_ = p.inbound.Close()
		_ = p.outbound.Close()
	})
}

func w8LoopbackPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			accepted <- nil
			return
		}
		accepted <- conn
	}()
	client, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	server := <-accepted
	require.NotNil(t, server, "loopback listener did not accept")
	return client, server
}

// ---------------------------------------------------------------------------
// Relay fixture: the production manager with its production budget
// ---------------------------------------------------------------------------

type w8Relay struct {
	manager *route.ConnectionManager
	scope   *adapter.Scope
	dialer  *w8ProbeDialer
	budget  *resourceguard.GrowthBudget
}

func w8NewRelay(t *testing.T, pressure *atomic.Int32) *w8Relay {
	t.Helper()
	logger := log.NewNOPFactory().Logger()
	ctx := context.Background()
	var guard *resourceguard.Guard
	if pressure != nil {
		guard = resourceguard.NewGuard(resourceguard.Config{
			Limit:              32,
			Queue:              256,
			Wait:               5 * time.Second,
			InteractiveReserve: 4,
			// The harness flips pressure levels inside one process; a production-length
			// demotion window would only make the A/B slower, not more faithful. The
			// BUDGET's derivation from the level is the product code under test.
			DemoteAfter:      time.Millisecond,
			StarvationAfter: time.Second,
		}, func() resourceguard.Level {
			return resourceguard.Level(pressure.Load())
		})
		ctx = resourceguard.NewContext(ctx, guard)
	}
	scope := adapter.NewScope(ctx, logger)
	manager := route.NewConnectionManager(logger)
	require.NoError(t, manager.Start(adapter.StartStateInitialize, scope))
	budget := w8GrowthBudget(t, manager)
	relay := &w8Relay{
		manager: manager,
		scope:   scope,
		dialer:  &w8ProbeDialer{pending: make(chan net.Conn, 4096)},
		budget:  budget,
	}
	t.Cleanup(func() {
		_ = manager.Close()
		_ = scope.Close()
	})
	return relay
}

// w8GrowthBudget reads the installed budget. There is no exported accessor by design
// (the policy is internal), so the harness reads the private pointer read-only. It fails
// the measurement rather than returning nil when the field is gone.
func w8GrowthBudget(t *testing.T, manager *route.ConnectionManager) *resourceguard.GrowthBudget {
	t.Helper()
	field := reflect.ValueOf(manager).Elem().FieldByName("growth")
	require.True(t, field.IsValid(), "ConnectionManager has no growth field; the harness lens must be updated")
	require.Equal(t, reflect.Ptr, field.Kind(), "ConnectionManager.growth is not a pointer")
	require.False(t, field.IsNil(), "the growth budget is not installed; manager.Start did not run the production wiring")
	return (*resourceguard.GrowthBudget)(unsafe.Pointer(field.Pointer()))
}

func (r *w8Relay) connect(t *testing.T, pair *w8RelayPair, class trafficclass.Class, earlyInbound bool) {
	t.Helper()
	pair.inbound.early = earlyInbound
	r.dialer.pending <- pair.outbound
	r.manager.NewConnection(context.Background(), r.dialer, pair.inbound,
		adapter.InboundContext{
			Destination:  M.ParseSocksaddrHostPort("w8.measure.example", 443),
			TrafficClass: class,
		}, nil)
}

// ---------------------------------------------------------------------------
// Payload and delivery tracking
// ---------------------------------------------------------------------------

func w8Payload(size int) ([]byte, string) {
	pattern := []byte("w8-relay-payload-0123456789abcdef")
	hash := sha256.New()
	payload := make([]byte, size)
	for offset := 0; offset < size; {
		n := copy(payload[offset:], pattern)
		_, _ = hash.Write(pattern[:n])
		offset += n
	}
	return payload, hex.EncodeToString(hash.Sum(nil))
}

type w8Tracker struct {
	access sync.Mutex
	total  int64
	hash   interface {
		Write([]byte) (int, error)
		Sum([]byte) []byte
	}
	signal chan struct{}
}

func w8NewTracker() *w8Tracker {
	return &w8Tracker{hash: sha256.New(), signal: make(chan struct{}, 1)}
}

func (d *w8Tracker) add(payload []byte) {
	d.access.Lock()
	d.total += int64(len(payload))
	_, _ = d.hash.Write(payload)
	d.access.Unlock()
	select {
	case d.signal <- struct{}{}:
	default:
	}
}

func (d *w8Tracker) totalBytes() int64 {
	d.access.Lock()
	defer d.access.Unlock()
	return d.total
}

// waitBytes blocks until at least target bytes have been observed. It is event driven:
// the tracker signals on every delivery, so a chunk's latency is the copy path's latency
// and not a polling interval. This matters for p95/p99: a 1 ms poll would put a 1-2 ms
// floor under every sample.
func (d *w8Tracker) waitBytes(target int64, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		if d.totalBytes() >= target {
			return true
		}
		select {
		case <-d.signal:
		case <-deadline:
			return d.totalBytes() >= target
		}
	}
}

func (d *w8Tracker) digest() string {
	d.access.Lock()
	defer d.access.Unlock()
	return hex.EncodeToString(d.hash.Sum(nil))
}

// w8Drain reads exactly size bytes from conn into a tracker and closes done.
func w8Drain(conn net.Conn, tracker *w8Tracker, done chan struct{}) {
	go func() {
		defer close(done)
		chunk := make([]byte, 256<<10)
		for {
			readN, err := conn.Read(chunk)
			if readN > 0 {
				tracker.add(chunk[:readN])
			}
			if err != nil {
				return
			}
		}
	}()
}

// ---------------------------------------------------------------------------
// Stats sampling
// ---------------------------------------------------------------------------

type w8StatsSampler struct {
	stop chan struct{}
	done chan struct{}

	peakInUse     atomic.Int64
	peakReserved  atomic.Int64
	peakRefused   atomic.Uint64
	lastInUse     atomic.Int64
	maxLevelValue atomic.Int32
}

func w8StartStatsSampler(budget *resourceguard.GrowthBudget, interval time.Duration) *w8StatsSampler {
	sampler := &w8StatsSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(sampler.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-sampler.stop:
				return
			case <-ticker.C:
				stats := budget.Stats()
				sampler.observe(stats)
			}
		}
	}()
	return sampler
}

func (s *w8StatsSampler) observe(stats resourceguard.GrowthStats) {
	for {
		current := s.peakInUse.Load()
		if int64(stats.InUse) <= current || s.peakInUse.CompareAndSwap(current, int64(stats.InUse)) {
			break
		}
	}
	for {
		current := s.peakReserved.Load()
		if int64(stats.ReservedInUse) <= current || s.peakReserved.CompareAndSwap(current, int64(stats.ReservedInUse)) {
			break
		}
	}
	for {
		current := s.peakRefused.Load()
		if stats.Refused <= current || s.peakRefused.CompareAndSwap(current, stats.Refused) {
			break
		}
	}
	s.lastInUse.Store(int64(stats.InUse))
	for {
		current := s.maxLevelValue.Load()
		if int32(stats.Level) <= current || s.maxLevelValue.CompareAndSwap(current, int32(stats.Level)) {
			break
		}
	}
}

func (s *w8StatsSampler) finish() (peakInUse, peakReserved int64, peakRefused uint64, maxLevel resourceguard.Level) {
	close(s.stop)
	<-s.done
	return s.peakInUse.Load(), s.peakReserved.Load(), s.peakRefused.Load(), resourceguard.Level(s.maxLevelValue.Load())
}

func w8WaitFor(t *testing.T, timeout time.Duration, what string, condition func() bool) time.Duration {
	t.Helper()
	started := time.Now()
	for !condition() {
		if time.Since(started) > timeout {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(time.Millisecond)
	}
	return time.Since(started)
}

func w8StatsRow(prefix string, stats resourceguard.GrowthStats) map[string]any {
	return map[string]any{
		prefix + "_level":            stats.Level.String(),
		prefix + "_flows":            stats.Flows,
		prefix + "_reserve":          stats.Reserve,
		prefix + "_in_use":           stats.InUse,
		prefix + "_reserved_in_use":  stats.ReservedInUse,
		prefix + "_peak":             stats.Peak,
		prefix + "_granted":          stats.Granted,
		prefix + "_released":         stats.Released,
		prefix + "_refused":          stats.Refused,
		prefix + "_geometry_needed":  stats.GeometryNeeded,
		prefix + "_double_releases":  stats.DoubleReleases,
		prefix + "_identity_holds":   stats.InUse == int(stats.Granted-stats.Released),
	}
}

// ---------------------------------------------------------------------------
// Scenario: quietbulk / bulkquiet
// ---------------------------------------------------------------------------

// w8BulkTransfer streams size bytes in BOTH directions of one bulk flow, measures wall
// time, verifies both payload hashes, and returns the digest and per-direction counters.
type w8BulkResult struct {
	elapsed       time.Duration
	uploadDigest  string
	downloadDigest string
	uploadReads   int
	uploadGrown   int
	uploadExpanded int
	downloadReads int
	downloadGrown int
	downloadExpanded int
}

func w8RunBulkTransfer(t *testing.T, relay *w8Relay, class trafficclass.Class, size int) *w8BulkResult {
	t.Helper()
	pair := w8NewPair(t, true)
	relay.connect(t, pair, class, false)

	uploadPayload, uploadDigest := w8Payload(size)
	downloadPayload, downloadDigest := w8Payload(size)

	uploadTracker := w8NewTracker()
	downloadTracker := w8NewTracker()
	uploadDrained := make(chan struct{})
	downloadDrained := make(chan struct{})
	w8Drain(pair.peer, uploadTracker, uploadDrained)
	w8Drain(pair.app, downloadTracker, downloadDrained)

	uploadWritten := make(chan error, 1)
	downloadWritten := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := pair.app.Write(uploadPayload)
		uploadWritten <- err
	}()
	go func() {
		_, err := pair.peer.Write(downloadPayload)
		downloadWritten <- err
	}()
	require.NoError(t, <-uploadWritten, "the upload payload was not accepted")
	require.NoError(t, <-downloadWritten, "the download payload was not accepted")
	require.True(t, uploadTracker.waitBytes(int64(size), 60*time.Second), "upload direction did not reach the far side")
	require.True(t, downloadTracker.waitBytes(int64(size), 60*time.Second), "download direction did not reach the far side")
	elapsed := time.Since(started)

	// The far side must see the EXACT payload, not merely the byte count. Closing both
	// sockets ends the two copy directions and returns their permits.
	pair.close()
	<-uploadDrained
	<-downloadDrained
	require.EqualValues(t, size, uploadTracker.totalBytes(), "upload direction completed a different number of bytes")
	require.EqualValues(t, size, downloadTracker.totalBytes(), "download direction completed a different number of bytes")
	require.Equal(t, uploadDigest, uploadTracker.digest(), "upload payload changed in the relay copy path")
	require.Equal(t, downloadDigest, downloadTracker.digest(), "download payload changed in the relay copy path")

	// Direction ownership: the upload copy reads the INBOUND socket (conn -> remoteConn),
	// the download copy reads the OUTBOUND socket (remoteConn -> conn). That mapping is
	// what the growth counters below are attributed to.
	uploadReads, uploadGrown, uploadExpanded, _ := pair.inbound.counters()
	downloadReads, downloadGrown, downloadExpanded, _ := pair.outbound.counters()
	return &w8BulkResult{
		elapsed:          elapsed,
		uploadDigest:     uploadDigest,
		downloadDigest:   downloadDigest,
		uploadReads:      uploadReads,
		uploadGrown:      uploadGrown,
		uploadExpanded:   uploadExpanded,
		downloadReads:    downloadReads,
		downloadGrown:    downloadGrown,
		downloadExpanded: downloadExpanded,
	}
}

// w8ChunkLatency measures one chunk's end-to-end time through the relay copy path: write
// a fixed-size chunk on the application side and wait until the far application side has
// observed it. This is the only ordering that gives a per-chunk latency; it is serialized
// by construction and is not used for syscall counts.
func w8ChunkLatency(t *testing.T, relay *w8Relay, class trafficclass.Class, totalSize int, chunkSize int) (p95, p99 time.Duration, samples []time.Duration) {
	t.Helper()
	pair := w8NewPair(t, true)
	relay.connect(t, pair, class, false)
	tracker := w8NewTracker()
	drained := make(chan struct{})
	w8Drain(pair.peer, tracker, drained)

	payload, _ := w8Payload(totalSize)
	chunks := totalSize / chunkSize
	latencies := make([]time.Duration, 0, chunks)
	for index := 0; index < chunks; index++ {
		started := time.Now()
		_, err := pair.app.Write(payload[index*chunkSize : (index+1)*chunkSize])
		require.NoError(t, err)
		target := int64(index+1) * int64(chunkSize)
		require.True(t, tracker.waitBytes(target, 60*time.Second), "chunk %d was never delivered", index)
		latencies = append(latencies, time.Since(started))
	}
	pair.close()
	<-drained
	return w8Percentile(latencies, 0.95), w8Percentile(latencies, 0.99), latencies
}

// TestW8W2QuietBulk is the main W2 A/B: 128 quiet bidirectional flows, then a bulk flow
// at equal completed bytes (16 MiB each way, 3 iterations), then a per-chunk latency pass.
func TestW8W2QuietBulk(t *testing.T) {
	if testing.Short() {
		t.Skip("moves >100 MiB through the relay copy path")
	}
	quietFlows := w8EnvInt(t, "W8_QUIET_FLOWS", 128)
	bulkSize := w8EnvInt(t, "W8_BULK_BYTES", 16<<20)
	iterations := w8EnvInt(t, "W8_BULK_ITERATIONS", 3)

	row := map[string]any{
		"case":        "w2",
		"scenario":    "quietbulk",
		"quiet_flows": quietFlows,
		"bulk_bytes":  bulkSize,
		"iterations":  iterations,
	}
	defer func() {
		if t.Failed() {
			row["failed"] = true
		}
		w8Emit(t, row)
	}()

	before := w8Begin()
	relay := w8NewRelay(t, nil)
	row["heap_before"] = before.mem
	row["proc_before"] = before.proc

	// Phase 1: 128 quiet bidirectional flows. They exchange no bytes.
	quietStart := w8BeginAttributed()
	quietPairs := make([]*w8RelayPair, 0, quietFlows)
	for index := 0; index < quietFlows; index++ {
		pair := w8NewPair(t, false)
		relay.connect(t, pair, trafficclass.ClassDefault, false)
		quietPairs = append(quietPairs, pair)
	}
	time.Sleep(100 * time.Millisecond)
	quietStats := relay.budget.Stats()
	quietProc := quietStart.delta()
	quietMem := w8ReadMem()
	row["quiet_stats"] = w8StatsRow("quiet", quietStats)
	row["quiet_proc_read_ops"] = quietProc.ReadOps
	row["quiet_proc_write_ops"] = quietProc.WriteOps
	row["quiet_proc_read_bytes"] = quietProc.ReadBytes
	row["quiet_proc_write_bytes"] = quietProc.WriteBytes
	row["quiet_heap_objects"] = quietMem.HeapObjects
	row["quiet_heap_alloc"] = quietMem.HeapAlloc

	// Phase 2: the bulk flow, while every quiet flow is still open.
	bulkStart := w8BeginAttributed()
	bulkSampler := w8StartStatsSampler(relay.budget, 2*time.Millisecond)
	throughputs := make([]float64, 0, iterations)
	elapsedList := make([]float64, 0, iterations)
	grownUpload := 0
	grownDownload := 0
	var lastBulk *w8BulkResult
	for iteration := 0; iteration < iterations; iteration++ {
		result := w8RunBulkTransfer(t, relay, trafficclass.ClassBulk, bulkSize)
		mibPerSecond := float64(bulkSize) / result.elapsed.Seconds() / (1 << 20)
		throughputs = append(throughputs, mibPerSecond)
		elapsedList = append(elapsedList, float64(result.elapsed.Nanoseconds())/1e6)
		grownUpload += result.uploadExpanded
		grownDownload += result.downloadExpanded
		lastBulk = result
		t.Logf("iteration %d: %s each way in %v (%.1f MiB/s one direction), upload_reads=%d download_reads=%d upload_expanded=%d download_expanded=%d",
			iteration, w8FormatBytes(int64(bulkSize)), result.elapsed, mibPerSecond,
			result.uploadReads, result.downloadReads, result.uploadExpanded, result.downloadExpanded)
	}
	bulkPeakInUse, bulkPeakReserved, bulkPeakRefused, bulkMaxLevel := bulkSampler.finish()
	bulkStats := relay.budget.Stats()
	bulkProc := bulkStart.delta()
	bulkMem := w8ReadMem()

	throughputMedian, throughputLow, throughputHigh := w8Median(throughputs)
	row["bulk_throughput_mib_s"] = throughputMedian
	row["bulk_throughput_low"] = throughputLow
	row["bulk_throughput_high"] = throughputHigh
	row["bulk_elapsed_ms_median"] = func() float64 { median, _, _ := w8Median(elapsedList); return median }()
	row["bulk_upload_reads"] = lastBulk.uploadReads
	row["bulk_download_reads"] = lastBulk.downloadReads
	row["bulk_upload_grown_reads"] = lastBulk.uploadGrown
	row["bulk_download_grown_reads"] = lastBulk.downloadGrown
	row["bulk_expanded_directions"] = grownUpload + grownDownload
	row["bulk_stats_after"] = w8StatsRow("bulk_after", bulkStats)
	row["bulk_sampled_peak_in_use"] = bulkPeakInUse
	row["bulk_sampled_peak_reserved"] = bulkPeakReserved
	row["bulk_sampled_peak_refused"] = bulkPeakRefused
	row["bulk_sampled_max_level"] = bulkMaxLevel.String()
	row["bulk_proc_read_ops"] = bulkProc.ReadOps
	row["bulk_proc_write_ops"] = bulkProc.WriteOps
	row["bulk_proc_read_bytes"] = bulkProc.ReadBytes
	row["bulk_proc_write_bytes"] = bulkProc.WriteBytes
	row["bulk_heap_alloc"] = bulkMem.HeapAlloc
	row["bulk_heap_objects"] = bulkMem.HeapObjects
	row["bulk_total_alloc"] = bulkMem.TotalAlloc
	row["bulk_peak_rss"] = bulkProc.PeakRSS

	// Phase 3: per-chunk latency on a fresh bulk flow, same geometry.
	latencyP95, latencyP99, latencySamples := w8ChunkLatency(t, relay, trafficclass.ClassBulk, 8<<20, 64<<10)
	row["latency_samples"] = len(latencySamples)
	row["latency_p95_us"] = latencyP95.Microseconds()
	row["latency_p99_us"] = latencyP99.Microseconds()
	row["latency_max_us"] = w8Percentile(latencySamples, 1.0).Microseconds()

	// Phase 4: bulk -> quiet. Every bulk flow is closed; the 128 quiet flows stay open.
	// The observable is the bulk directions' permits returning: InUse must drop back to
	// whatever the quiet flows hold. On the reference side that is 256 (every direction
	// was charged at connect); on the candidate it is 0. Asserting "== 0" here would
	// encode the candidate's behaviour as the reference's contract.
	quietHeld := quietStats.InUse
	drainedAfter := w8WaitFor(t, 30*time.Second, "the bulk permits to return while the quiet flows stay open", func() bool {
		return relay.budget.Stats().InUse <= quietHeld
	})
	finalStats := relay.budget.Stats()
	row["bulk_to_quiet_drain_ms"] = float64(drainedAfter.Nanoseconds()) / 1e6
	row["quiet_permits_held_after_bulk"] = finalStats.InUse
	row["quiet_permits_held_after_bulk_equals_quiet_phase"] = finalStats.InUse == quietHeld
	row["final_stats"] = w8StatsRow("final", finalStats)
	row["final_rss"] = w8ReadProc().RSS
	row["final_peak_rss"] = w8ReadProc().PeakRSS
	row["final_heap_alloc"] = w8ReadMem().HeapAlloc
	row["final_heap_objects"] = w8ReadMem().HeapObjects
	row["final_goroutines"] = runtime.NumGoroutine()
	row["goroutines_peak_after_bulk"] = runtime.NumGoroutine()

	// Quiet directions must never have expanded, in either generation of the wiring.
	quietExpanded := 0
	for _, pair := range quietPairs {
		_, _, inboundExpanded, _ := pair.inbound.counters()
		_, _, outboundExpanded, _ := pair.outbound.counters()
		quietExpanded += inboundExpanded + outboundExpanded
	}
	row["quiet_expanded_directions"] = quietExpanded
	require.Zero(t, quietExpanded, "a quiet direction that copied zero bytes expanded its buffer")

	// The budget's own identity is the contract this file checks; the policy numbers
	// themselves are recorded, not asserted, because they are what the A/B compares.
	require.Zero(t, finalStats.DoubleReleases, "a growth permit was released twice")
	require.Equal(t, finalStats.InUse, int(finalStats.Granted-finalStats.Released),
		"Granted - Released != InUse")
	if quietStats.InUse == 0 {
		require.Zero(t, quietStats.Granted, "a quiet direction was granted growth")
	} else {
		// The reference side (pre-W2 wiring) charges every direction at connect time.
		// That is the defect being measured, not an assertion this harness can make.
		t.Logf("reference behaviour: %d quiet directions hold %d growth permits before any byte was copied",
			quietFlows*2, quietStats.InUse)
	}
	for _, pair := range quietPairs {
		pair.close()
	}
	w8WaitFor(t, 30*time.Second, "all permits to return", func() bool { return relay.budget.Stats().InUse == 0 })
	afterAll := relay.budget.Stats()
	row["after_close_stats"] = w8StatsRow("after_close", afterAll)
	row["after_close_heap_alloc"] = w8ReadMem().HeapAlloc
	row["after_close_heap_objects"] = w8ReadMem().HeapObjects
	row["after_close_rss"] = w8ReadProc().RSS
	row["after_close_peak_rss"] = w8ReadProc().PeakRSS
	row["after_close_goroutines"] = runtime.NumGoroutine()
	require.Zero(t, afterAll.InUse, "permits survived after every flow was closed")
	require.Zero(t, afterAll.DoubleReleases, "a permit was released twice")
}

// TestW8W2Pressure covers pressure tightening, the interactive reserve, and the rule that
// an existing grown direction is not revoked when the level rises.
func TestW8W2Pressure(t *testing.T) {
	if testing.Short() {
		t.Skip("keeps 128 grown directions live")
	}
	flows := w8EnvInt(t, "W8_PRESSURE_FLOWS", 128)
	size := w8EnvInt(t, "W8_PRESSURE_BYTES", 700<<10)

	row := map[string]any{
		"case":          "w2",
		"scenario":      "pressure",
		"pressure_flows": flows,
		"pressure_bytes": size,
	}
	defer func() {
		if t.Failed() {
			row["failed"] = true
		}
		w8Emit(t, row)
	}()

	var level atomic.Int32
	level.Store(int32(resourceguard.LevelArmed))
	relay := w8NewRelay(t, &level)
	row["armed_initial"] = w8StatsRow("armed", relay.budget.Stats())

	// Fill the Armed ordinary cap with grown directions. Each flow uploads past the copy
	// engine's growth threshold on its INBOUND direction and stays open, so its permit is
	// held by an enlarged buffer that is still live.
	held := make([]*w8RelayPair, 0, flows+4)
	for index := 0; index < flows; index++ {
		pair := w8NewPair(t, true)
		relay.connect(t, pair, trafficclass.ClassBulk, false)
		held = append(held, pair)
		payload, _ := w8Payload(size)
		_, err := pair.app.Write(payload)
		require.NoError(t, err)
		_, _ = io.CopyN(io.Discard, pair.peer, int64(size))
	}
	armedStats := relay.budget.Stats()
	row["armed_filled"] = w8StatsRow("armed_filled", armedStats)
	require.Greater(t, armedStats.InUse, 0, "no direction grew under Armed; the scenario measured nothing")

	// A normal-priority direction must be refused while the cap is held.
	refusedBefore := relay.budget.Stats().Refused
	normalPair := w8NewPair(t, true)
	relay.connect(t, normalPair, trafficclass.ClassBulk, false)
	normalPayload, _ := w8Payload(size)
	_, err := normalPair.app.Write(normalPayload)
	require.NoError(t, err)
	_, _ = io.CopyN(io.Discard, normalPair.peer, int64(size))
	normalReads, normalGrown, normalExpanded, _ := normalPair.inbound.counters()
	row["normal_refused_grown"] = normalGrown
	row["normal_refused_expanded"] = normalExpanded
	row["normal_refused_reads"] = normalReads
	row["normal_refused_delta"] = relay.budget.Stats().Refused - refusedBefore
	require.Zero(t, normalExpanded, "a normal direction grew while the Armed cap was full")

	// An interactive direction may draw the reserve.
	interactivePair := w8NewPair(t, true)
	relay.connect(t, interactivePair, trafficclass.ClassInteractive, false)
	interactivePayload, _ := w8Payload(size)
	_, err = interactivePair.app.Write(interactivePayload)
	require.NoError(t, err)
	_, _ = io.CopyN(io.Discard, interactivePair.peer, int64(size))
	_, interactiveGrown, interactiveExpanded, _ := interactivePair.inbound.counters()
	reservedStats := relay.budget.Stats()
	row["interactive_expanded"] = interactiveExpanded
	row["interactive_grown_reads"] = interactiveGrown
	row["interactive_stats"] = w8StatsRow("interactive", reservedStats)
	require.NotZero(t, interactiveExpanded, "the interactive reserve did not admit a high-priority direction at the Armed cap")
	require.Greater(t, reservedStats.ReservedInUse, 0, "the interactive direction was charged to the ordinary cap, not the reserve")

	// Pressure rises to Critical: no new growth, and no existing permit is revoked.
	level.Store(int32(resourceguard.LevelCritical))
	criticalBefore := relay.budget.Stats()
	require.Equal(t, reservedStats.InUse, criticalBefore.InUse,
		"raising pressure revoked an existing grown direction; the buffer is still live")
	criticalPair := w8NewPair(t, true)
	relay.connect(t, criticalPair, trafficclass.ClassBulk, false)
	criticalPayload, _ := w8Payload(size)
	_, err = criticalPair.app.Write(criticalPayload)
	require.NoError(t, err)
	_, _ = io.CopyN(io.Discard, criticalPair.peer, int64(size))
	_, criticalGrown, criticalExpanded, _ := criticalPair.inbound.counters()
	criticalStats := relay.budget.Stats()
	row["critical_expanded"] = criticalExpanded
	row["critical_grown_reads"] = criticalGrown
	row["critical_stats"] = w8StatsRow("critical", criticalStats)
	row["critical_in_use_unchanged"] = criticalStats.InUse == criticalBefore.InUse
	require.Zero(t, criticalExpanded, "a direction grew at Critical pressure")
	require.Equal(t, criticalBefore.InUse, criticalStats.InUse, "Critical revoked or added permits")

	// Recovery: back to Normal, a fresh direction grows again once permits return.
	level.Store(int32(resourceguard.LevelNormal))
	w8WaitFor(t, 5*time.Second, "the guard to demote to Normal", func() bool {
		return relay.budget.Stats().Level == resourceguard.LevelNormal
	})
	for _, pair := range held {
		pair.close()
	}
	normalPair.close()
	interactivePair.close()
	criticalPair.close()
	w8WaitFor(t, 60*time.Second, "all permits to return", func() bool { return relay.budget.Stats().InUse == 0 })
	recoveredPair := w8NewPair(t, true)
	relay.connect(t, recoveredPair, trafficclass.ClassBulk, false)
	recoveredPayload, _ := w8Payload(size)
	_, err = recoveredPair.app.Write(recoveredPayload)
	require.NoError(t, err)
	_, _ = io.CopyN(io.Discard, recoveredPair.peer, int64(size))
	_, _, recoveredExpanded, _ := recoveredPair.inbound.counters()
	recoveredStats := relay.budget.Stats()
	row["recovered_expanded"] = recoveredExpanded
	row["recovered_stats"] = w8StatsRow("recovered", recoveredStats)
	require.NotZero(t, recoveredExpanded, "growth did not recover after the permits returned")
	recoveredPair.close()
	w8WaitFor(t, 30*time.Second, "the recovered permit to return", func() bool { return relay.budget.Stats().InUse == 0 })
	finalStats := relay.budget.Stats()
	row["final_stats"] = w8StatsRow("final", finalStats)
	row["final_rss"] = w8ReadProc().RSS
	row["final_heap_alloc"] = w8ReadMem().HeapAlloc
	row["final_heap_objects"] = w8ReadMem().HeapObjects
	row["final_goroutines"] = runtime.NumGoroutine()
	require.Zero(t, finalStats.DoubleReleases)
	require.Equal(t, finalStats.InUse, int(finalStats.Granted-finalStats.Released))
}

// TestW8W2Geometry pins the geometry-required exemption: at Critical pressure the ordinary
// plan is zero, so an ordinary direction cannot grow, but a writer whose framing needs the
// early larger buffer still does - and is counted as GeometryNeeded rather than charged to
// the ordinary cap.
func TestW8W2Geometry(t *testing.T) {
	var level atomic.Int32
	level.Store(int32(resourceguard.LevelCritical))
	relay := w8NewRelay(t, &level)

	row := map[string]any{"case": "w2", "scenario": "geometry"}
	defer func() {
		if t.Failed() {
			row["failed"] = true
		}
		w8Emit(t, row)
	}()

	size := w8EnvInt(t, "W8_GEOMETRY_BYTES", 200<<10)

	// Control: ordinary direction at Critical must not grow.
	ordinary := w8NewPair(t, true)
	relay.connect(t, ordinary, trafficclass.ClassBulk, false)
	ordinaryPayload, _ := w8Payload(size)
	_, err := ordinary.app.Write(ordinaryPayload)
	require.NoError(t, err)
	_, _ = io.CopyN(io.Discard, ordinary.peer, int64(size))
	_, ordinaryGrown, ordinaryExpanded, _ := ordinary.inbound.counters()
	row["ordinary_expanded"] = ordinaryExpanded
	row["ordinary_grown_reads"] = ordinaryGrown
	require.Zero(t, ordinaryExpanded, "an ordinary direction grew at Critical pressure")

	// Geometry: the outbound end declares EarlyCopyBufferGrowth, exactly as a padded
	// writer does. The upload copy feeds it, so its threshold is 1 and it must grow.
	geometry := w8NewPair(t, true)
	geometry.outbound.early = true
	relay.connect(t, geometry, trafficclass.ClassBulk, false)
	geometryPayload, _ := w8Payload(size)
	_, err = geometry.app.Write(geometryPayload)
	require.NoError(t, err)
	_, _ = io.CopyN(io.Discard, geometry.peer, int64(size))
	_, geometryGrown, geometryExpanded, _ := geometry.inbound.counters()
	stats := relay.budget.Stats()
	row["geometry_expanded"] = geometryExpanded
	row["geometry_grown_reads"] = geometryGrown
	row["geometry_stats"] = w8StatsRow("geometry", stats)
	row["geometry_needed"] = stats.GeometryNeeded
	require.NotZero(t, geometryExpanded, "a geometry-required direction was refused at Critical pressure")
	require.Greater(t, stats.GeometryNeeded, uint64(0), "the geometry exemption was not counted")

	ordinary.close()
	geometry.close()
	w8WaitFor(t, 30*time.Second, "all permits to return", func() bool { return relay.budget.Stats().InUse == 0 })
	finalStats := relay.budget.Stats()
	row["final_stats"] = w8StatsRow("final", finalStats)
	row["final_heap_alloc"] = w8ReadMem().HeapAlloc
	row["final_heap_objects"] = w8ReadMem().HeapObjects
	row["final_rss"] = w8ReadProc().RSS
	row["final_goroutines"] = runtime.NumGoroutine()
	require.Zero(t, finalStats.DoubleReleases)
}

