//go:build linux && go1.25 && badlinkname

package ktls

import (
	"errors"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/common/badtls"
	"github.com/sagernet/sing/common/buf"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// TestDeferReadErrorAfterDataKeepsBufferedBytes pins the non-EOF half of the delayed-delivery
// contract. The slow Read path returns (n, err) when a record read fails after application data
// was already available; WaitReadBuffer cannot return a buffer with an error, so it must return
// the data now and the exact error next. Releasing the buffer here is what lost the tail.
func TestDeferReadErrorAfterDataKeepsBufferedBytes(t *testing.T) {
	buffer := buf.NewSize(4)
	_, err := buffer.Write([]byte("tail"))
	require.NoError(t, err)
	sentinel := errors.New("non-eof record error")
	connection := &Conn{readWaitOptions: N.ReadWaitOptions{}}

	returned, returnedErr := connection.deferReadErrorAfterData(buffer, sentinel)
	require.NoError(t, returnedErr, "the data must be delivered with nil error")
	require.Same(t, buffer, returned)
	require.Equal(t, []byte("tail"), returned.Bytes(), "the already-read bytes must survive")
	require.ErrorIs(t, connection.pendingReadErr, sentinel, "the exact error must be queued for the next call")
	returned.Release()
}

// TestWaitReadBufferDeliversTheQueuedErrorOnce proves the next call consumes the queued error and
// returns no buffer, rather than repeating the data or dropping the error.
func TestWaitReadBufferDeliversTheQueuedErrorOnce(t *testing.T) {
	sentinel := errors.New("non-eof record error")
	connection := &Conn{
		rawConn: &badtls.RawConn{
			In: &badtls.RawHalfConn{Mutex: &sync.Mutex{}},
		},
		pendingReadErr: sentinel,
	}

	buffer, err := connection.WaitReadBuffer()
	require.Nil(t, buffer)
	require.ErrorIs(t, err, sentinel)
	require.NoError(t, connection.pendingReadErr, "the queued error must be consumed once")
}
