package sniff

import (
	"bytes"
	"testing"

	"github.com/sagernet/sing/common/buf"
	"github.com/stretchr/testify/require"
)

// The growth helper is the one place that touches the ownership of the sniff buffer, so its
// properties are pinned here directly rather than only through the peek loop:
//
//   - it moves every byte and returns a buffer whose capacity is the requested ceiling;
//   - it refuses (and returns the original) when the limit cannot hold the payload;
//   - it does not return a buffer to the pool while somebody still holds a reference, because the
//     array is then still reachable through that reference.
func TestGrowStreamBufferKeepsEveryByte(t *testing.T) {
	payload := bytes.Repeat([]byte{0x17, 0x03, 0x03}, 900)
	current := buf.NewSize(1024)
	_, err := current.Write(payload[:1024])
	require.NoError(t, err)

	before := StreamSniffGrowthCount()
	grown := growStreamBuffer(current, 4096)
	require.Equal(t, before+1, StreamSniffGrowthCount(), "a successful growth is counted once")

	require.NotSame(t, current, grown, "sing buffers are fixed-capacity: growth replaces the object")
	require.Equal(t, 4096, grown.Cap())
	require.Equal(t, 1024, grown.Len())
	require.Equal(t, payload[:1024], grown.Bytes())
	require.Equal(t, 3072, grown.FreeLen(), "the free space behind the payload is what the next read fills")
	grown.Release()
}

func TestGrowStreamBufferRefusesWithoutLosingBytes(t *testing.T) {
	payload := []byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")
	current := buf.NewSize(1024)
	_, err := current.Write(payload)
	require.NoError(t, err)

	before := StreamSniffGrowthCount()
	same := growStreamBuffer(current, 1024)
	require.Same(t, current, same, "a limit that cannot hold more must not produce a new owner")
	require.Equal(t, before, StreamSniffGrowthCount(), "a refused growth is not a growth")
	require.Equal(t, payload, same.Bytes())
	same.Release()
}

// A referenced buffer is never handed back to the pool by a growth: buf.Release checks the
// reference count and returns without clearing, so the array stays attached to its holder. The
// payload must survive the copy for that holder as well - a growth that zeroed the old array would
// corrupt whatever the borrow was reading.
func TestGrowStreamBufferDoesNotPoolAReferencedBuffer(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 1536)
	current := buf.NewSize(2048)
	_, err := current.Write(payload)
	require.NoError(t, err)
	current.IncRef()

	grown := growStreamBuffer(current, 8192)
	require.Equal(t, 8192, grown.Cap())
	require.Equal(t, payload, grown.Bytes())
	require.Equal(t, 2048, current.Cap(), "the referenced buffer keeps its array instead of being pooled")
	require.Equal(t, payload, current.Bytes())

	// Once the borrow is gone the buffer pools normally: Release clears it instead of returning the
	// array twice.
	current.DecRef()
	current.Release()
	require.Zero(t, current.Cap())
	grown.Release()
}
