package resourceguard

import (
	"io"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"

	"github.com/stretchr/testify/require"
)

// These tests take the threshold Admit answers with and run it through sing's real
// copy engine (bufio.CopyWithIncreateBuffer, the function route/conn.go uses), so
// the policy is proven at the boundary it changes rather than only as numbers:
//
//	CopyGrowthNever      the engine never switches to the larger buffer
//	CopyGrowthDefault    it switches only after DefaultIncreaseBufferAfter bytes
//	CopyGrowthImmediate  it switches after the first transfer
//
// A plain io.Reader/Writer pair is used on purpose: bufio.copyDirect only applies
// to syscall-capable ends, and this is the userspace copy loop the growth knob is
// in.

// readSizeRecorder records the buffer size of every read the copy engine asks for.
type readSizeRecorder struct {
	remaining int
	sizes     []int
}

func (r *readSizeRecorder) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	r.sizes = append(r.sizes, len(p))
	r.remaining -= n
	return n, nil
}

func (r *readSizeRecorder) maxRead() int {
	maximum := 0
	for _, size := range r.sizes {
		maximum = max(maximum, size)
	}
	return maximum
}

// countWriter consumes what the copy engine hands it and records the total.
type countWriter struct {
	total int64
}

func (w *countWriter) Write(p []byte) (int, error) {
	w.total += int64(len(p))
	return len(p), nil
}

// copyWithThreshold runs one copy of total bytes and returns the read sizes.
func copyWithThreshold(t *testing.T, threshold int64, total int) ([]int, int64) {
	t.Helper()
	reader := &readSizeRecorder{remaining: total}
	writer := &countWriter{}
	copied, err := bufio.CopyWithIncreateBuffer(writer, reader, threshold, bufio.DefaultBatchSize)
	require.NoError(t, err)
	require.Equal(t, int64(total), copied, "the copy engine must report every byte it moved")
	require.Equal(t, int64(total), writer.total)
	return reader.sizes, copied
}

// grownReads counts the reads that asked for more than the un-grown buffer, i.e.
// the reads the engine served after switching.
func grownReads(sizes []int) int {
	grown := 0
	for _, size := range sizes {
		if size > buf.BufferSize {
			grown++
		}
	}
	return grown
}

// TestCopyGrowthNeverKeepsTheSmallBuffer pins the refusal: a direction with no
// permit keeps the un-grown buffer for its whole life, which is what makes
// refusing growth a memory choice rather than a correctness one.
func TestCopyGrowthNeverKeepsTheSmallBuffer(t *testing.T) {
	sizes, _ := copyWithThreshold(t, CopyGrowthNever, 4*buf.BufferSize)
	require.Greater(t, len(sizes), 0)
	require.Equal(t, buf.BufferSize, sizes[0])
	require.Equal(t, 0, grownReads(sizes), "a never-grow threshold must not produce a grown read: %v", sizes)
}

// TestCopyGrowthDefaultGrowsOnlyAfterTheThreshold pins today's behaviour, which a
// granted permit must preserve byte for byte: the larger buffer appears only after
// DefaultIncreaseBufferAfter bytes have been copied.
func TestCopyGrowthDefaultGrowsOnlyAfterTheThreshold(t *testing.T) {
	sizes, _ := copyWithThreshold(t, CopyGrowthDefault, 2*int(bufio.DefaultIncreaseBufferAfter))
	require.Greater(t, grownReads(sizes), 0, "the default threshold must grow for a transfer this long: %v", sizes)

	copied := 0
	for index, size := range sizes {
		if size > buf.BufferSize {
			require.GreaterOrEqual(t, int64(copied), int64(bufio.DefaultIncreaseBufferAfter),
				"read %d already used the grown buffer after only %d bytes", index, copied)
			break
		}
		copied += size
	}
}

// TestCopyGrowthImmediateGrowsAfterTheFirstRead pins the geometry-required case: the
// first chunk still uses the un-grown buffer (that is how the engine starts), and
// every later one is grown.
func TestCopyGrowthImmediateGrowsAfterTheFirstRead(t *testing.T) {
	sizes, _ := copyWithThreshold(t, CopyGrowthImmediate, 2*buf.BufferSize)
	require.Greater(t, len(sizes), 1)
	require.Equal(t, buf.BufferSize, sizes[0], "the first read cannot be grown: the latch flips after it")
	require.Greater(t, sizes[1], buf.BufferSize, "the second read must be grown: %v", sizes)
}

// TestGrowthBudgetGrantedThresholdMatchesTheOldCopy is the equivalence the wiring
// needs: a granted permit answers exactly the threshold the route layer would have
// used without any budget, and the copy engine behaves identically because of it.
func TestGrowthBudgetGrantedThresholdMatchesTheOldCopy(t *testing.T) {
	var level atomic.Uint32
	budget := NewGrowthBudget(GrowthConfig{Flows: 1}, func() Level { return Level(level.Load()) })
	threshold, permit := budget.Admit(trafficclass.ClassDefault, false)
	require.NotNil(t, permit)
	require.Equal(t, CopyGrowthDefault, threshold)
	defer permit.Release()

	withBudget, _ := copyWithThreshold(t, threshold, 2*int(bufio.DefaultIncreaseBufferAfter))
	withoutBudget, _ := copyWithThreshold(t, bufio.DefaultIncreaseBufferAfter, 2*int(bufio.DefaultIncreaseBufferAfter))
	require.Equal(t, withoutBudget, withBudget, "a granted permit must not change how the copy engine reads")

	// And a refusal answers 0, which the same engine reads as "never grow".
	level.Store(uint32(LevelCritical))
	refusedThreshold, refusedPermit := budget.Admit(trafficclass.ClassDefault, false)
	require.Nil(t, refusedPermit)
	require.Equal(t, CopyGrowthNever, refusedThreshold)
	refusedSizes, _ := copyWithThreshold(t, refusedThreshold, 2*int(bufio.DefaultIncreaseBufferAfter))
	require.Equal(t, 0, grownReads(refusedSizes))
}
