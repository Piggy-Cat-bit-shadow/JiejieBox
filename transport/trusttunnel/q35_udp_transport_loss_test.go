package trusttunnel

// Q35, transport-level loss matrix: the loss is injected into the TUNNEL
// transport, one direction at a time, and the production client session and
// service are the ones under test.
//
// # Why this matrix exists next to the handler blackhole
//
// q35_udp_blackhole_test.go blackholes at the HANDLER: the service received the
// datagram and did not answer. That shape leaves ingress intact. The other shapes
// are on the path, and the handler cannot produce them:
//
//   - INGRESS loss (client -> service): the client's write succeeds, the frame
//     never enters the tunnel, the service never sees the datagram and creates no
//     association for it;
//   - EGRESS loss (service -> client): the service receives and answers, the
//     answer never reaches the caller, so the counters must show a SENT reply
//     where the caller saw nothing;
//   - reclaim while the path is lossy: the association expires, and the datagram
//     that arrives afterwards is served by a NEW association.
//
// Each cell therefore has exactly one candidate cause.
//
// # Where the injection sits, and what that models
//
// The loss is applied at the FRAME boundary of the client's _udp2 stream: one
// encoded datagram frame is written/dropped or read/discarded as a whole. That is
// the datagram-level loss a UDP path produces ("this datagram was lost"), and it
// is deliberately not applied below TLS: dropping arbitrary bytes under a TLS
// record stream does not model packet loss, it models corruption, and the peer
// answers `tls: bad record MAC` (measured while building this test). Framing the
// drop at the frame boundary keeps the stream aligned and the failure
// attributable.
//
// A sustained link outage additionally retransmits on QUIC and stalls the TCP
// window on HTTP/2; that behaviour is not modelled here and no cell claims it.
// What is claimed is that both directions of the path are handled as their own
// failure with distinguishable counters, on both transports.
//
// # The deadline capability is measured per transport
//
// HTTP/2 has no stream read deadline and says so (NeedAdditionalReadDeadline
// true, SetReadDeadline os.ErrInvalid); HTTP/3 does have one. A consumer must read
// that declaration instead of assuming either way - the route sniff does exactly
// that, see route/route.go and transport/trusttunnel/service_udp.go.

import (
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// lossyFrameWriter drops whole client frames while its flag is set. Dropping a
// whole encoded frame is what a lost datagram looks like to the tunnel.
type lossyFrameWriter struct {
	inner   io.Writer
	drop    *atomic.Bool
	dropped *atomic.Int64
}

func (w *lossyFrameWriter) Write(p []byte) (int, error) {
	if w.drop.Load() {
		w.dropped.Add(1)
		return len(p), nil
	}
	return w.inner.Write(p)
}

// frameReaderCell reads server frames for one cell. It consumes whole frames, so
// a dropped frame leaves the stream aligned; replies for earlier cells are
// reported to the caller instead of being discarded here.
type frameReaderCell struct {
	replies chan receivedDatagram
	dropped atomic.Int64
}

func startFrameReaderCell(stream *rawStream, dropReads *atomic.Bool) *frameReaderCell {
	cell := &frameReaderCell{replies: make(chan receivedDatagram, 16)}
	go func() {
		reader := frameReader{reader: stream.reader}
		for {
			buffer := buf.NewSize(MaxPayloadSize)
			source, _, err := reader.ReadServerFrame(buffer)
			if err != nil {
				cell.replies <- receivedDatagram{err: err}
				buffer.Release()
				return
			}
			if dropReads.Load() {
				cell.dropped.Add(1)
				buffer.Release()
				continue
			}
			payload := append([]byte(nil), buffer.Bytes()...)
			buffer.Release()
			cell.replies <- receivedDatagram{payload: payload, source: source}
		}
	}()
	return cell
}

func (c *frameReaderCell) expectPayload(t *testing.T, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	var seen []string
	for {
		select {
		case reply := <-c.replies:
			require.NoError(t, reply.err)
			if string(reply.payload) == want {
				return
			}
			seen = append(seen, string(reply.payload))
		case <-deadline:
			t.Fatalf("no reply %q within %s; replies seen: %v", want, timeout, seen)
		}
	}
}

func (c *frameReaderCell) expectNothing(t *testing.T, window time.Duration, context string) {
	t.Helper()
	select {
	case reply := <-c.replies:
		t.Fatalf("%s: a reply %q arrived where the path was down", context, reply.payload)
	case <-time.After(window):
	}
}

// TestUDPTransportLossMatrix is the direction-by-direction fault injection.
func TestUDPTransportLossMatrix(t *testing.T) {
	protocols := []string{ProtocolHTTP2}
	if q35HasQUIC {
		protocols = append(protocols, ProtocolHTTP3)
	}
	for _, protocol := range protocols {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			handler := &testHandler{}
			// The idle window is long enough that the cells which assert a LIVE
			// association cannot be raced by the sweep, and short enough that the
			// reclaim cell does not dominate the run.
			server := startTestService(t, protocol, handler, UDPLimits{IdleTimeout: 1500 * time.Millisecond})
			client := server.newClient(t, protocol, testPassword, &testDialer{}, nil)

			// The deadline capability contract, on its own stream so the matrix
			// below is unaffected by a timed-out read.
			deadlineConn, err := client.ListenPacket(testContext(t))
			require.NoError(t, err)
			if protocol == ProtocolHTTP2 {
				require.True(t, deadlineConn.NeedAdditionalReadDeadline(),
					"H2 has no stream deadline and must declare it")
				require.ErrorIs(t, deadlineConn.SetReadDeadline(time.Now().Add(time.Second)), os.ErrInvalid,
					"an unsupported deadline must be reported, not silently ignored")
			} else {
				require.False(t, deadlineConn.NeedAdditionalReadDeadline(),
					"H3 streams support a read deadline, so no external timer is needed")
				require.NoError(t, deadlineConn.SetReadDeadline(time.Now().Add(150*time.Millisecond)))
				started := time.Now()
				_, readErr := deadlineConn.ReadPacket(buf.NewSize(MaxPayloadSize))
				require.ErrorIs(t, readErr, os.ErrDeadlineExceeded)
				require.Less(t, time.Since(started), 3*time.Second)
				require.NoError(t, deadlineConn.SetReadDeadline(time.Time{}))
			}
			require.NoError(t, deadlineConn.Close())

			stream := openRawUDPStream(t, client)
			var dropWrites, dropReads atomic.Bool
			writer := &lossyFrameWriter{inner: stream.writer, drop: &dropWrites, dropped: new(atomic.Int64)}
			stream.writer = writer
			droppedWrites := writer.dropped
			droppedWrites.Store(0)
			reader := startFrameReaderCell(stream, &dropReads)

			first := M.ParseSocksaddr("127.0.0.1:5353")
			second := M.ParseSocksaddr("127.0.0.1:5354")

			// Warm-up: the tunnel carries a datagram before any loss is applied,
			// so a later failure is attributable to the injected loss.
			writeRawClientFrame(t, stream, []byte("warm"), first)
			reader.expectPayload(t, "warm", 10*time.Second)
			stats := waitForUDPStats(t, server.service, "the warm-up", func(stats UDPStats) bool {
				return stats.DatagramsReceived == 1 && stats.Associations == 1
			})

			// ---- Cell A: ingress loss, on a destination that has no association
			// yet. The write is accepted, the frame is dropped, the service never
			// sees it and must NOT create an association for it.
			dropWrites.Store(true)
			writeRawClientFrame(t, stream, []byte("ingress-lost"), second)
			reader.expectNothing(t, 400*time.Millisecond, "ingress loss")
			require.EqualValues(t, 1, droppedWrites.Load(), "the frame was not dropped at the transport")
			stats = server.service.UDPStats()
			require.EqualValues(t, 1, stats.DatagramsReceived,
				"the ingress-lost datagram must not be counted as received: %+v", stats)
			require.EqualValues(t, 1, stats.Associations,
				"no association may be created for a datagram that never arrived: %+v", stats)

			// ---- Cell B: ingress recovery. The same session serves the datagram
			// and creates the association it could not create before.
			dropWrites.Store(false)
			writeRawClientFrame(t, stream, []byte("ingress-recovered"), second)
			reader.expectPayload(t, "ingress-recovered", 10*time.Second)
			stats = waitForUDPStats(t, server.service, "the recovered datagram", func(stats UDPStats) bool {
				return stats.DatagramsReceived == 2 && stats.Associations == 2 && stats.DatagramsSent == 2
			})
			t.Logf("%s ingress loss: received=%d associations=%d dropped-frames=%d",
				protocol, stats.DatagramsReceived, stats.Associations, droppedWrites.Load())

			// ---- Cell C: egress loss. The service receives and answers; the answer
			// is consumed and dropped before it reaches the caller, so the reply
			// segment says "sent" while the caller sees nothing.
			dropReads.Store(true)
			writeRawClientFrame(t, stream, []byte("egress-lost"), first)
			reader.expectNothing(t, 500*time.Millisecond, "egress loss")
			stats = waitForUDPStats(t, server.service, "the answered but undelivered datagram", func(stats UDPStats) bool {
				return stats.DatagramsSent == 3
			})
			require.GreaterOrEqual(t, reader.dropped.Load(), int64(1),
				"the reply frame was not dropped at the transport")
			t.Logf("%s egress loss: received=%d sent=%d dropped-replies=%d (the answer was written and lost)",
				protocol, stats.DatagramsReceived, stats.DatagramsSent, reader.dropped.Load())

			// ---- Cell D: egress recovery.
			dropReads.Store(false)
			writeRawClientFrame(t, stream, []byte("egress-recovered"), first)
			reader.expectPayload(t, "egress-recovered", 10*time.Second)
			waitForUDPStats(t, server.service, "the recovered reply", func(stats UDPStats) bool {
				return stats.DatagramsSent == 4
			})

			// ---- Cell E: reclaim while the path is lossy, then recovery through a
			// NEW association. The client stays connected and silent, which is what
			// the idle sweep exists for.
			dropReads.Store(true)
			stats = waitForUDPStats(t, server.service, "the idle reclaim", func(stats UDPStats) bool {
				return stats.Associations == 0 && stats.QueuedBytes == 0
			})
			require.GreaterOrEqual(t, stats.AssociationsExpired, uint64(2),
				"both associations must be reclaimed while the client stays connected")
			expired := stats.AssociationsExpired
			dropReads.Store(false)
			writeRawClientFrame(t, stream, []byte("after-reclaim"), second)
			reader.expectPayload(t, "after-reclaim", 10*time.Second)
			stats = waitForUDPStats(t, server.service, "the new association", func(stats UDPStats) bool {
				return stats.Associations == 1 && stats.DatagramsSent == 5
			})
			require.Equal(t, expired, stats.AssociationsExpired,
				"recovery must not need a second reclaim; the association was created on the datagram")

			// ---- At rest the segments conserve, and every delivered datagram was
			// answered (this handler echoes), so the reply segment has no failures.
			stats = waitForUDPStats(t, server.service, "the final idle reclaim", func(stats UDPStats) bool {
				return stats.Associations == 0 && stats.QueuedBytes == 0
			})
			require.Zero(t, stats.RepliesFailed, "no reply was written into a closed stream in this cell")
			require.Equal(t, stats.DatagramsDelivered, stats.DatagramsSent+stats.RepliesFailed,
				"the reply segment does not conserve: %+v", stats)
			requireConserved(t, stats, protocol+" transport loss matrix")
			t.Logf("%s transport loss matrix at rest: %+v", protocol, stats)
		})
	}
}
