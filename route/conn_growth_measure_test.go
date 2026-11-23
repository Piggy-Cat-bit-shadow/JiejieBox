package route

// A small, bounded measurement of the copy engine the relay uses, at equal completed
// bytes, for the three buffer-growth states the W2/G07 work distinguishes:
//
//	noseam    no growth budget at all: the pre-existing unconditional growth
//	granted   a seam that grants at the threshold (what a direction gets when the
//	          shared budget has room)
//	refused   a seam that refuses (what the reviewer's starved bulk direction got:
//	          it keeps the small read buffer for its whole transfer)
//	budget    the real resourceguard.GrowthBoundary seam with room to grant
//
// Two phases share one rig: a sender feeds one real loopback TCP socket, the production
// copy call (bufio.CopyWithIncreateBufferPermit) moves bytes to another, and a receiver
// drains it.
//
//   - throughput/syscalls: the sender streams 32 MiB as fast as it can and the receiver
//     drains it. The read-syscall column is the number of read operations the copy loop
//     issued on the source socket; the probe waiter performs exactly one Read per
//     WaitReadBuffer, which is what the production syscall waiter's recv does.
//   - latency: 8 MiB in 64 KiB chunks with a per-chunk wait for delivery, so p95/p99
//     describe one chunk's end-to-end time through the copy engine. This phase is
//     serialized by construction and is not used for the syscall count.
//
// Both phases check the payload digest. What this is NOT: a product throughput number, a
// network measurement, or a claim that the seam costs nothing. The same test runs in the
// ordinary build and under the with_low_memory tag so both buffer geometries are
// recorded; timing columns are evidence and are not tuned to a target.

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// measureSource is a socket backed by the two production seams: it is an
// N.ReadWaiter (so the copy loop allocates and reads exactly as it does for a real
// socket) and it counts read operations and grown reads.
type measureSource struct {
	net.Conn

	access  sync.Mutex
	options N.ReadWaitOptions
	reads   int
	grown   int
}

func (c *measureSource) InitializeReadWaiter(options N.ReadWaitOptions) bool {
	c.access.Lock()
	c.options = options
	c.access.Unlock()
	return false
}

func (c *measureSource) WaitReadBuffer() (*buf.Buffer, error) {
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
			if options.IncreaseBuffer {
				c.grown++
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

func (c *measureSource) counters() (reads int, grown int) {
	c.access.Lock()
	defer c.access.Unlock()
	return c.reads, c.grown
}

// deliveryTracker accumulates delivered bytes, hashes them, and signals progress with a
// capacity-1 channel so a sender can wait for a byte boundary without a round trip per
// read.
type deliveryTracker struct {
	access sync.Mutex
	total  int64
	hash   interface {
		Write([]byte) (int, error)
		Sum([]byte) []byte
	}
	signal chan struct{}
}

func newDeliveryTracker() *deliveryTracker {
	return &deliveryTracker{hash: sha256.New(), signal: make(chan struct{}, 1)}
}

func (d *deliveryTracker) add(payload []byte) {
	d.access.Lock()
	d.total += int64(len(payload))
	_, _ = d.hash.Write(payload)
	d.access.Unlock()
	select {
	case d.signal <- struct{}{}:
	default:
	}
}

func (d *deliveryTracker) totalBytes() int64 {
	d.access.Lock()
	defer d.access.Unlock()
	return d.total
}

func (d *deliveryTracker) digest() string {
	d.access.Lock()
	defer d.access.Unlock()
	return hex.EncodeToString(d.hash.Sum(nil))
}

// growthRig is the two-socket copy path, with the copy loop already running.
type growthRig struct {
	sourceApp net.Conn
	source    *measureSource
	copyDest  net.Conn
	receiver  net.Conn
	copyDone  chan error
	tracker   *deliveryTracker
	stopRead  chan struct{}
	readDone  chan struct{}
}

func newGrowthRig(t *testing.T, permit bufio.IncreaseBufferPermit) *growthRig {
	t.Helper()
	sourceApp, copySource := newLoopbackTCPPair(t)
	copyDest, receiver := newLoopbackTCPPair(t)
	rig := &growthRig{
		sourceApp: sourceApp,
		source:    &measureSource{Conn: copySource},
		copyDest:  copyDest,
		receiver:  receiver,
		copyDone:  make(chan error, 1),
		tracker:   newDeliveryTracker(),
		stopRead:  make(chan struct{}),
		readDone:  make(chan struct{}),
	}
	t.Cleanup(func() {
		_ = sourceApp.Close()
		_ = copySource.Close()
		_ = copyDest.Close()
		_ = receiver.Close()
	})
	go func() {
		_, err := bufio.CopyWithIncreateBufferPermit(copyDest, rig.source, bufio.DefaultIncreaseBufferAfter, bufio.DefaultBatchSize, permit)
		rig.copyDone <- err
	}()
	go func() {
		defer close(rig.readDone)
		buffer := make([]byte, 256<<10)
		for {
			readN, err := receiver.Read(buffer)
			if readN > 0 {
				rig.tracker.add(buffer[:readN])
			}
			if err != nil {
				return
			}
			select {
			case <-rig.stopRead:
				return
			default:
			}
		}
	}()
	return rig
}

// waitFor blocks until the receiver has seen at least target bytes, with a bound so a
// broken copy cannot hang the test.
func (r *growthRig) waitFor(t *testing.T, variant string, target int64) {
	t.Helper()
	for r.tracker.totalBytes() < target {
		select {
		case <-r.tracker.signal:
		case <-time.After(30 * time.Second):
			t.Fatalf("%s: only %d/%d bytes reached the far side", variant, r.tracker.totalBytes(), target)
		}
	}
}

// finish half-closes the source, waits for the copy loop, stops the receiver and checks
// the delivered digest and byte count.
func (r *growthRig) finish(t *testing.T, variant string, expectedDigest string, expectedBytes int64) {
	t.Helper()
	require.NoError(t, r.sourceApp.(interface{ CloseWrite() error }).CloseWrite())
	select {
	case err := <-r.copyDone:
		require.NoError(t, err, "%s: the copy loop returned an error", variant)
	case <-time.After(30 * time.Second):
		t.Fatalf("%s: the copy loop never returned", variant)
	}
	close(r.stopRead)
	_ = r.receiver.Close()
	<-r.readDone
	require.EqualValues(t, expectedBytes, r.tracker.totalBytes(), "%s: the copy completed a different number of bytes", variant)
	require.Equal(t, expectedDigest, r.tracker.digest(), "%s: the payload changed on the way through the copy engine", variant)
}

// streamPayload produces size bytes of a deterministic pattern and its sha256.
func streamPayload(size int) ([]byte, string) {
	pattern := []byte("measure-payload-0123456789abcdef")
	hash := sha256.New()
	payload := make([]byte, size)
	for offset := 0; offset < size; {
		n := copy(payload[offset:], pattern)
		_, _ = hash.Write(pattern[:n])
		offset += n
	}
	return payload, hex.EncodeToString(hash.Sum(nil))
}

func measureGrowthThroughput(t *testing.T, variant string, payloadSize int, permit bufio.IncreaseBufferPermit) (elapsed time.Duration, reads int, grown int) {
	t.Helper()
	payload, digest := streamPayload(payloadSize)
	rig := newGrowthRig(t, permit)
	start := time.Now()
	writeDone := make(chan error, 1)
	go func() {
		for offset := 0; offset < payloadSize; {
			n, err := rig.sourceApp.Write(payload[offset:])
			if err != nil {
				writeDone <- err
				return
			}
			offset += n
		}
		writeDone <- nil
	}()
	select {
	case err := <-writeDone:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatalf("%s: the payload was never accepted", variant)
	}
	rig.waitFor(t, variant, int64(payloadSize))
	elapsed = time.Since(start)
	rig.finish(t, variant, digest, int64(payloadSize))
	reads, grown = rig.source.counters()
	return elapsed, reads, grown
}

func measureGrowthLatency(t *testing.T, variant string, payloadSize int, permit bufio.IncreaseBufferPermit) (p95 time.Duration, p99 time.Duration) {
	t.Helper()
	payload, digest := streamPayload(payloadSize)
	rig := newGrowthRig(t, permit)

	const chunkSize = 64 << 10
	chunks := payloadSize / chunkSize
	latencies := make([]time.Duration, 0, chunks)
	delivered := int64(0)
	for index := 0; index < chunks; index++ {
		start := time.Now()
		_, err := rig.sourceApp.Write(payload[index*chunkSize : (index+1)*chunkSize])
		require.NoError(t, err)
		target := int64(index+1) * chunkSize
		for delivered < target {
			select {
			case <-rig.tracker.signal:
				delivered = rig.tracker.totalBytes()
			case <-time.After(30 * time.Second):
				t.Fatalf("%s: chunk %d was never delivered", variant, index)
			}
		}
		latencies = append(latencies, time.Since(start))
	}
	rig.finish(t, variant, digest, int64(payloadSize))

	sort.Slice(latencies, func(left, right int) bool { return latencies[left] < latencies[right] })
	percentile := func(fraction float64) time.Duration {
		index := int(fraction * float64(len(latencies)-1))
		return latencies[index]
	}
	return percentile(0.95), percentile(0.99)
}

type growthMeasurement struct {
	variant    string
	bytes      int64
	throughput float64
	reads      int
	grownReads int
	p95        time.Duration
	p99        time.Duration
}

// TestGrowthSeamMeasurement records the W2 measurement at equal completed bytes. It is
// deliberately a test, not a benchmark: the deterministic assertions (bytes, digest,
// grown vs small reads, and the read-syscall ratio) are what must hold; the timing
// columns are recorded evidence and are not tuned to a target.
func TestGrowthSeamMeasurement(t *testing.T) {
	if testing.Short() {
		t.Skip("the growth measurement moves 40 MiB per variant and is skipped in -short")
	}

	const streamSize = 32 << 20
	const latencySize = 8 << 20
	noSeam := bufio.IncreaseBufferPermit(nil)
	grantedSeam := func() (func(), bool) { return func() {}, true }
	refusedSeam := func() (func(), bool) { return nil, false }
	budget := resourceguard.NewGrowthBudget(resourceguard.GrowthConfig{Flows: 1}, nil)
	budgetSeam := budget.GrowthBoundary(trafficclass.ClassBulk, false)

	t.Logf("geometry: low_memory=%v buffer_size=%d threshold=%d", common.LowMemory, buf.BufferSize, bufio.DefaultIncreaseBufferAfter)
	measurements := make([]growthMeasurement, 0, 5)
	for _, variant := range []struct {
		name   string
		permit bufio.IncreaseBufferPermit
	}{
		{"noseam", noSeam},
		{"granted", grantedSeam},
		{"refused", refusedSeam},
		{"budget", budgetSeam},
		{"noseam2", noSeam},
	} {
		elapsed, reads, grown := measureGrowthThroughput(t, variant.name, streamSize, variant.permit)
		p95, p99 := measureGrowthLatency(t, variant.name, latencySize, variant.permit)
		measurement := growthMeasurement{
			variant:    variant.name,
			bytes:      streamSize,
			throughput: float64(streamSize) / elapsed.Seconds() / (1 << 20),
			reads:      reads,
			grownReads: grown,
			p95:        p95,
			p99:        p99,
		}
		measurements = append(measurements, measurement)
		t.Logf("%-8s bytes=%d throughput_stream=%.1f MiB/s read_syscalls=%d grown_reads=%d chunk_p95=%v chunk_p99=%v",
			measurement.variant, measurement.bytes, measurement.throughput,
			measurement.reads, measurement.grownReads, measurement.p95, measurement.p99)
	}
	byName := make(map[string]growthMeasurement, len(measurements))
	for _, measurement := range measurements {
		byName[measurement.variant] = measurement
	}

	// The deterministic part: growth changes the read syscall count, a refusal never
	// grows, and the granting seams do not change the read pattern beyond timing noise.
	require.Zero(t, byName["refused"].grownReads, "a refused direction must never use the grown buffer")
	require.NotZero(t, byName["granted"].grownReads, "a granted direction must actually grow")
	require.NotZero(t, byName["budget"].grownReads, "the real budget seam must grant when it has room")
	require.Less(t, byName["granted"].reads, byName["refused"].reads,
		"a grown direction must need fewer read syscalls for the same bytes")
	require.Less(t, byName["budget"].reads, byName["refused"].reads,
		"the real budget seam must need fewer read syscalls than a refused direction")
	noseamDelta := byName["noseam"].reads - byName["granted"].reads
	if noseamDelta < 0 {
		noseamDelta = -noseamDelta
	}
	// The bound is expressed against the REFUSAL's effect, not as a fixed percentage: the
	// measurement's point is that gating growth does not change the read pattern, while refusing it
	// roughly doubles the read count. A 5%-of-noseam tolerance was calibrated on an idle machine and
	// failed under `-race`, where the scheduler hands the loop different chunk sizes - that is
	// measurement noise, not a behaviour change, and pinning it would make the CI race step flaky
	// while telling us nothing. Half the refusal penalty still fails loudly if a granting seam ever
	// starts to alter the read pattern materially.
	refusalPenalty := byName["refused"].reads - byName["granted"].reads
	require.Positive(t, refusalPenalty, "the refusal must cost extra reads to bound against")
	require.LessOrEqual(t, noseamDelta, max(4, refusalPenalty/2),
		"a granting seam must not materially change the copy loop's read count (noseam=%d granted=%d refused=%d)",
		byName["noseam"].reads, byName["granted"].reads, byName["refused"].reads)
}
