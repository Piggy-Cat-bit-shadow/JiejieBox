package naive

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/sagernet/sing/common/buf"

	"github.com/stretchr/testify/require"
)

// AUDIT: the padding REGION must be zeros on the wire.
//
// The Naive layout is
//
//	[2-byte data size][1-byte padding size][data][zeros[padding size]]
//
// so the trailing bytes are not free space: they are protocol bytes whose value
// is fixed to zero. That property is also a security property, because the
// buffer being framed is usually a POOLED one: if the padding is written by
// extending the buffer without zeroing, the frame carries whatever the previous
// owner left behind.
//
// This is not theoretical. It happened on both sides of this protocol:
//
//   - cronet-go#6  "Naive payload padding leaks stale buffer contents instead of zeros"
//   - sing-box#4002 (fixed by #4001), the same defect in the Go server codec
//
// The current writers use `WriteZeroN`, so there is no live leak. What was
// missing is a test that FAILS if the zeroing is replaced by a plain extension -
// the exact edit that caused both incidents. The mutation is recorded in the
// implementation report; run it by replacing the two `WriteZeroN(paddingSize)`
// calls in inbound_conn.go with `Extend(paddingSize)` and watching these tests
// go red.
//
// Coverage:
//
//  1. writeFrame        - the allocating path (used by writeChunked)
//  2. writeBufferWithPadding - the in-place path on a CALLER-owned pooled buffer,
//     poisoned with 0xAA so a non-zeroing writer is visible
//  3. writeChunked      - the large-payload path, every frame of the window
//
// Every padding size in 0..255 is exercised, because 0 is the case where the
// assertion is vacuous and must still not be wrong, and 255 is the largest
// value the frame geometry must accommodate.

// fixedPadding returns a paddingConn whose draw is fixed, so a test can drive
// every value of the one-byte field.
func fixedPadding(size int) *paddingConn {
	return &paddingConn{
		enabled:     true,
		paddingSize: func() int { return size },
	}
}

// requireZeroPadding decodes one frame from the front of frame and returns the
// payload and the number of bytes consumed, asserting the padding bytes are
// zero.
func requireZeroPadding(t *testing.T, frame []byte) (payload []byte, consumed int) {
	t.Helper()
	require.GreaterOrEqual(t, len(frame), 3, "a frame must carry its 3-byte header")
	dataSize := int(binary.BigEndian.Uint16(frame[:2]))
	paddingSize := int(frame[2])
	require.Equal(t, 3+dataSize+paddingSize, len(frame),
		"frame length must be header + data + declared padding")
	payload = frame[3 : 3+dataSize]
	padding := frame[3+dataSize:]
	for i, b := range padding {
		require.Zero(t, b,
			"padding byte %d (of %d) is %#x; the Naive layout requires zeros, "+
				"and a non-zero byte here means the frame leaked pooled buffer contents",
			i, paddingSize, b)
	}
	return payload, len(frame)
}

// TestAuditPaddingBytesAreAlwaysZeroWriteFrame drives the allocating writer over
// the full padding range.
func TestAuditPaddingBytesAreAlwaysZeroWriteFrame(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5A}, 1400)
	for paddingSize := 0; paddingSize <= 255; paddingSize++ {
		var out bytes.Buffer
		written, err := fixedPadding(paddingSize).writeFrame(&out, payload, paddingSize)
		require.NoError(t, err, "padding size %d", paddingSize)
		require.Equal(t, len(payload), written)

		frame := out.Bytes()
		got, consumed := requireZeroPadding(t, frame)
		require.Equal(t, payload, got, "payload must not be rewritten by padding")
		require.Equal(t, len(frame), consumed)
	}
}

// TestAuditPaddingBytesAreAlwaysZeroInPlace is the pooled-buffer case: the
// caller's buffer has its free area filled with a marker byte, so padding that
// merely extends into it would be caught.
func TestAuditPaddingBytesAreAlwaysZeroInPlace(t *testing.T) {
	const marker = 0xAA
	const payloadLen = 1400
	payload := bytes.Repeat([]byte{0x33}, payloadLen)

	for paddingSize := 0; paddingSize <= 255; paddingSize++ {
		// Exactly the geometry the copy path hands a padded writer:
		// 3 bytes of front headroom, the payload, and 255 bytes of rear
		// headroom, plus 8 guard bytes so an over-write past the frame is
		// visible too.
		const guard = 8
		buffer := buf.NewSize(3 + payloadLen + 255 + guard)
		defer buffer.Release()
		buffer.Advance(3)
		_, err := buffer.Write(payload)
		require.NoError(t, err)
		for i, b := range buffer.FreeBytes() {
			require.Zero(t, b, "the pooled buffer must start with free space")
			_ = i
			break
		}
		free := buffer.FreeBytes()
		for i := range free {
			free[i] = marker
		}

		var out bytes.Buffer
		require.NoError(t, fixedPadding(paddingSize).writeBufferWithPadding(&out, buffer),
			"padding size %d", paddingSize)

		frame := out.Bytes()
		got, consumed := requireZeroPadding(t, frame)
		require.Equal(t, payload, got)
		require.Equal(t, len(frame), consumed)

		// Everything past the frame belongs to the caller's buffer and must be
		// untouched: the writer may only consume the headroom it declared and
		// the padding it declared, never a byte more.
		remaining := buffer.FreeBytes()
		require.Len(t, remaining, 255+guard-paddingSize,
			"the padding write must stop exactly at the frame boundary")
		for i, b := range remaining {
			require.Equal(t, byte(marker), b,
				"byte %d past the frame was overwritten: the frame wrote past its declared padding", i)
		}
		require.Equal(t, 0, buffer.Start(), "the frame header consumes the front headroom")
	}
}

// TestAuditPaddingBytesAreAlwaysZeroChunked covers the path taken when a write
// is too large to frame in place: writeChunked segments it, and every one of the
// first paddingCount frames must still carry zero padding.
func TestAuditPaddingBytesAreAlwaysZeroChunked(t *testing.T) {
	// Larger than maxFrameSize-3-255 so the in-place path refuses it, and large
	// enough that the eight-frame padding window is fully consumed before the
	// payload ends - which is what makes the "rest is raw" half testable.
	payload := bytes.Repeat([]byte{0x77}, 1<<20)
	for _, paddingSize := range []int{0, 1, 127, 254, 255} {
		var out bytes.Buffer
		connection := fixedPadding(paddingSize)
		written, err := connection.writeChunked(&out, payload)
		require.NoError(t, err)
		require.Equal(t, len(payload), written)

		rest := out.Bytes()
		var totalPayload int
		frames := 0
		for len(rest) > 0 && frames < paddingCount {
			require.GreaterOrEqual(t, len(rest), 3, "frame %d must carry its header", frames)
			dataSize := int(binary.BigEndian.Uint16(rest[:2]))
			paddingSize := int(rest[2])
			frameLen := 3 + dataSize + paddingSize
			require.GreaterOrEqual(t, len(rest), frameLen, "frame %d is truncated", frames)
			got, consumed := requireZeroPadding(t, rest[:frameLen])
			totalPayload += len(got)
			rest = rest[consumed:]
			frames++
		}
		require.Equal(t, paddingCount, frames,
			"a payload this large must be split into the full padding window")
		// The remainder after the window is raw: raw length must account for
		// the rest of the payload exactly.
		totalPayload += len(rest)
		require.Equal(t, len(payload), totalPayload)
	}
}

// TestAuditPaddingZeroIsTheEmptyCase pins the degenerate draw: padding 0 means
// the frame has no trailing bytes, so there is nothing to zero and nothing to
// assert beyond the length - which the helper already checks. It exists so a
// future "skip the zero fill when paddingSize == 0" shortcut is a deliberate,
// tested decision rather than an accident.
func TestAuditPaddingZeroIsTheEmptyCase(t *testing.T) {
	var out bytes.Buffer
	connection := fixedPadding(0)
	_, err := connection.writeFrame(&out, []byte("abc"), 0)
	require.NoError(t, err)
	require.Equal(t, []byte{0, 3, 0, 'a', 'b', 'c'}, out.Bytes())
}
