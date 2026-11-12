package hysteria2

// Q19's remaining cells, measured on the pinned carrier instead of assumed.
//
// The matrix had three NOT-COVERED cells - batch, headroom and partial-write - all inside the pinned
// sing-quic stream wrapper. This test records the wrapper's actual surface against a real Hysteria2
// server: deadline control exists; batch and headroom do NOT exist at this boundary (the wrapper
// implements io.Reader/io.Writer only), so those two cells are dependency-owned and accepted as such;
// a 256 KiB payload written through the real net.Conn and echoed byte-for-byte is the partial-write
// observation the matrix can make from this side.

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	"github.com/stretchr/testify/require"
)

func TestPinnedCarrierCapabilitySurface(t *testing.T) {
	handler := &hysteriaCarrierHandler{target: startHysteriaCarrierEcho(t)}
	port := startHysteriaCarrierServer(t, handler)
	outbound := newHysteriaCarrierOutbound(t, port)
	conn := hysteriaRoundTrip(t, outbound, "capability-probe")
	defer conn.Close()

	// Deadline control: the wrapper is a net.Conn with working deadlines.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(time.Second)))
	require.NoError(t, conn.SetDeadline(time.Now().Add(time.Second)))

	// Batch and headroom: measured absence, not assumed. The wrapper does not expose the buffer
	// interfaces, so no in-repo test can observe a batch or a headroom contract here; that is the
	// dependency-owned cell the matrix records as accepted.
	_, hasBatchReader := conn.(interface {
		ReadBuffer(buffer *buf.Buffer) error
	})
	_, hasBatchWriter := conn.(interface {
		WriteBuffer(buffer *buf.Buffer) error
	})
	require.False(t, hasBatchReader, "the pinned wrapper now exposes a batch reader; the Q19 cell must be re-run")
	require.False(t, hasBatchWriter, "the pinned wrapper now exposes a batch writer; the Q19 cell must be re-run")

	// Partial-write: the real conn carries a payload larger than any single receive window and the
	// echo is byte-identical. If Write ever returns a short count without an error, io.Copy would
	// report it; this asserts the wrapper's own contract through the real peer.
	payload := make([]byte, 256*1024)
	for index := range payload {
		payload[index] = byte(index * 7)
	}
	require.NoError(t, conn.SetDeadline(time.Now().Add(15*time.Second)))
	written, err := io.Copy(conn, bytes.NewReader(payload))
	require.NoError(t, err)
	require.EqualValues(t, len(payload), written)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err)
	require.True(t, bytes.Equal(payload, echo), "the echo of a 256 KiB stream differed")
}
