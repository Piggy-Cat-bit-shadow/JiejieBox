package route

import (
	"errors"
	"io"
	"testing"

	"github.com/sagernet/sing/common/buf"

	"github.com/stretchr/testify/require"
)

type cachedBuffersReader struct {
	buffers []*buf.Buffer
}

func (r *cachedBuffersReader) Read([]byte) (int, error) { return 0, io.EOF }

func (r *cachedBuffersReader) ReadCached() *buf.Buffer {
	if len(r.buffers) == 0 {
		return nil
	}
	next := r.buffers[0]
	r.buffers = r.buffers[1:]
	return next
}

type failAfterWriter struct {
	writes int
	failAt int
}

func (w *failAfterWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, errors.New("injected cached write failure")
	}
	return len(p), nil
}

// A cached write that fails after bytes have already been handed to the outbound is a terminal
// outcome, not a rejection. drainCachedSource must return the error immediately: retrying the
// failed buffer, continuing to the next one, or falling back to the copy loops would replay bytes
// the peer may already have received. L12 of the adversarial matrix.
func TestCachedDrainStopsAtTheFirstWriteFailure(t *testing.T) {
	first := buf.NewSize(5)
	_, err := first.Write([]byte("first"))
	require.NoError(t, err)
	second := buf.NewSize(6)
	_, err = second.Write([]byte("second"))
	require.NoError(t, err)
	third := buf.NewSize(5)
	_, err = third.Write([]byte("third"))
	require.NoError(t, err)

	source := &cachedBuffersReader{buffers: []*buf.Buffer{first, second, third}}
	remote := &failAfterWriter{failAt: 2}

	_, _, drainErr := drainCachedSource(source, remote)
	require.Error(t, drainErr)
	require.Equal(t, 2, remote.writes,
		"the failing buffer must not be retried and the third buffer must not be written")
}

// The success path still drains every cached buffer in order and only then reports no error.
func TestCachedDrainWritesEveryBufferInOrder(t *testing.T) {
	first := buf.NewSize(3)
	_, err := first.Write([]byte("one"))
	require.NoError(t, err)
	second := buf.NewSize(3)
	_, err = second.Write([]byte("two"))
	require.NoError(t, err)

	source := &cachedBuffersReader{buffers: []*buf.Buffer{first, second}}
	remote := &failAfterWriter{failAt: 0}

	_, _, drainErr := drainCachedSource(source, remote)
	require.NoError(t, drainErr)
	require.Equal(t, 2, remote.writes)
}
