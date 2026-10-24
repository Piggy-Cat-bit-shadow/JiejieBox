package masque

import (
	"testing"

	"github.com/sagernet/sing/common/buf"

	"github.com/stretchr/testify/require"
)

type nonBlockingHandler struct {
	writes int
}

func (h *nonBlockingHandler) WriteInboundBuffers([]*buf.Buffer) error {
	h.writes++
	return nil
}

type blockingHandler struct {
	nonBlockingHandler
	waits int
}

func (h *blockingHandler) WaitWriteInboundBuffers([]*buf.Buffer) error {
	h.waits++
	return nil
}

// The session must use the blocking device write when the handler provides one, and keep the old
// non-blocking path for handlers that do not. This is the interface-compatibility half of the
// MASQUE backpressure wiring.
func TestWriteInboundBuffersPrefersTheBlockingHandler(t *testing.T) {
	blocking := &blockingHandler{}
	require.NoError(t, writeInboundBuffers(blocking, []*buf.Buffer{buf.As([]byte("x"))}))
	require.Equal(t, 1, blocking.waits)
	require.Zero(t, blocking.writes)

	nonBlocking := &nonBlockingHandler{}
	require.NoError(t, writeInboundBuffers(nonBlocking, []*buf.Buffer{buf.As([]byte("x"))}))
	require.Equal(t, 1, nonBlocking.writes)
}
