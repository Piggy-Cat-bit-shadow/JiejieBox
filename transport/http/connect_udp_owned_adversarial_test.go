package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Adversarial cases for the L4 CONNECT-UDP owned path.
//
// The happy-path tests live in connect_udp_owned_test.go; these are the shapes that break an
// ownership change: no headroom at all (which makes PrependContextID replace and release the
// buffer), every batch size around the transport's queue, a refusal in the middle of a batch, and
// one case per error class, because "any error means try a capsule" is exactly the bug this must
// not have.

// scriptedStream is the adversarial double: it can refuse by error class, by payload, or after N
// sends, and it records what it actually consumed.
type scriptedStream struct {
	access sync.Mutex

	datagramsEnabled bool
	ownedAvailable   bool

	// refuseWith is returned by an owned send when refuseAtPayload matches or refuseAfter is hit.
	refuseWith       error
	refuseAtPayload  []byte
	refuseAfter      int
	refuseFromCopy   error
	datagrams        [][]byte
	capsules         [][]byte
	ownedCalls       int
	copyCalls        int
	ownedReleases    int
	requireOwnedOnly bool
}

func (s *scriptedStream) DatagramsEnabled() bool { return s.datagramsEnabled }

func (s *scriptedStream) Read([]byte) (int, error) { return 0, io.EOF }

func (s *scriptedStream) Write(p []byte) (int, error) {
	s.access.Lock()
	s.capsules = append(s.capsules, append([]byte(nil), p...))
	s.access.Unlock()
	return len(p), nil
}

func (s *scriptedStream) Close() error { return nil }

func (s *scriptedStream) SendDatagram(payload []byte) error {
	s.access.Lock()
	s.copyCalls++
	err := s.refuseFromCopy
	if err == nil {
		s.datagrams = append(s.datagrams, append([]byte(nil), payload...))
	}
	s.access.Unlock()
	return err
}

func (s *scriptedStream) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *scriptedStream) SendDatagramOwned(buffer *buf.Buffer) error {
	s.access.Lock()
	s.ownedCalls++
	call := s.ownedCalls
	refuse := s.refuseWith
	if refuse != nil && s.refuseAfter > 0 && call <= s.refuseAfter {
		refuse = nil
	}
	if refuse != nil && len(s.refuseAtPayload) > 0 {
		if string(buffer.Bytes()) != string(append([]byte{0}, s.refuseAtPayload...)) {
			refuse = nil
		}
	}
	if refuse != nil {
		s.access.Unlock()
		return refuse
	}
	s.datagrams = append(s.datagrams, append([]byte(nil), buffer.Bytes()...))
	s.ownedReleases++
	s.access.Unlock()
	buffer.Release()
	return nil
}

func newScriptedConn(t *testing.T, stream DatagramStream) *http3PacketConn {
	t.Helper()
	return &http3PacketConn{
		stream:         stream,
		ownedDatagrams: AsOwnedDatagramSender(stream),
		destination:    M.ParseSocksaddr("192.0.2.1:443"),
		packets:        make(chan *buf.Buffer, 4),
		ctx:            context.Background(),
	}
}

// TestConnectUDPOwnedHeadroomMatrix walks the headroom values a producer can hand over.
//
// At 0 the Context ID cannot be prepended in place, so PrependContextID allocates a replacement and
// releases the original. The invariant the caller depends on: after the call the ORIGINAL buffer is
// empty (released by that replacement), the transport saw context-ID + payload, and nothing is
// released twice.
func TestConnectUDPOwnedHeadroomMatrix(t *testing.T) {
	t.Parallel()

	payload := []byte("payload that a tunnel would carry")
	for _, headroom := range []int{0, 1, 5, 6, 8, 9, 16} {
		t.Run(fmt.Sprintf("headroom=%d", headroom), func(t *testing.T) {
			t.Parallel()
			stream := &scriptedStream{datagramsEnabled: true, ownedAvailable: true}
			conn := newScriptedConn(t, stream)

			buffer := newTestPayload(payload, headroom)
			require.NoError(t, conn.WritePacket(buffer, M.ParseSocksaddr("192.0.2.1:443")))

			require.Equal(t, 1, stream.ownedCalls, "the owned send must be used")
			require.Equal(t, 0, stream.copyCalls)
			require.Len(t, stream.datagrams, 1)
			require.Equal(t, byte(0), stream.datagrams[0][0], "Context ID must lead the datagram")
			require.Equal(t, payload, stream.datagrams[0][1:], "payload must survive every geometry")
			require.Equal(t, 1, stream.ownedReleases, "the transport releases exactly once")
			require.Zero(t, buffer.Len(),
				"the caller's handle must be empty after the call, whatever the geometry")
		})
	}
}

// TestConnectUDPOwnedBatchSizes walks the sizes around the transport's 32-frame queue.
func TestConnectUDPOwnedBatchSizes(t *testing.T) {
	t.Parallel()

	for _, size := range []int{1, 2, 4, 8, 16, 31, 32, 33, 64, 65} {
		t.Run(fmt.Sprintf("batch=%d", size), func(t *testing.T) {
			t.Parallel()
			stream := &scriptedStream{datagramsEnabled: true, ownedAvailable: true}
			conn := newScriptedConn(t, stream)
			writer := &connectedBatchWriter{conn: conn}

			batch := make([]*buf.Buffer, size)
			for i := range batch {
				batch[i] = newTestPayload([]byte(fmt.Sprintf("packet-%03d", i)), connectUDPOwnedHeadroom)
			}
			require.NoError(t, writer.WriteConnectedPacketBatch(batch))

			require.Len(t, stream.datagrams, size, "every packet is its own datagram")
			require.Equal(t, size, stream.ownedReleases)
			require.Empty(t, stream.capsules, "no capsule on a datagram-capable peer")
			for i, datagram := range stream.datagrams {
				require.Equal(t, byte(0), datagram[0])
				require.Equal(t, fmt.Sprintf("packet-%03d", i), string(datagram[1:]),
					"packet %d must keep its position", i)
			}
			for i, buffer := range batch {
				require.Zero(t, buffer.Len(), "buffer %d must be consumed exactly once", i)
			}
		})
	}
}

// TestConnectUDPOwnedRefusalInTheMiddleKeepsOrderAndCount proves a refusal for one packet does not
// replay or reorder the batch: the refused packet becomes a capsule IN ITS POSITION, and the
// datagrams around it stay in order.
func TestConnectUDPOwnedRefusalInTheMiddleKeepsOrderAndCount(t *testing.T) {
	t.Parallel()

	stream := &scriptedStream{
		datagramsEnabled: true,
		ownedAvailable:   true,
		refuseWith:       &DatagramTooLargeError{MaxPayloadSize: 100},
		refuseAtPayload:  []byte("too-big"),
	}
	conn := newScriptedConn(t, stream)
	writer := &connectedBatchWriter{conn: conn}

	batch := []*buf.Buffer{
		newTestPayload([]byte("first"), connectUDPOwnedHeadroom),
		newTestPayload([]byte("too-big"), connectUDPOwnedHeadroom),
		newTestPayload([]byte("third"), connectUDPOwnedHeadroom),
	}
	require.NoError(t, writer.WriteConnectedPacketBatch(batch))

	require.Len(t, stream.datagrams, 2, "the two fitting packets are datagrams")
	require.Equal(t, "first", string(stream.datagrams[0][1:]))
	require.Equal(t, "third", string(stream.datagrams[1][1:]))
	require.Len(t, stream.capsules, 1, "the oversized one is a capsule, not a drop")
	require.Contains(t, string(stream.capsules[0]), "too-big")
	for i, buffer := range batch {
		require.Zero(t, buffer.Len(), "buffer %d consumed once", i)
	}
}

// TestConnectUDPOwnedRefusalDoesNotLeakOrDuplicate proves the refusal path releases the packet
// exactly once and never sends it twice.
func TestConnectUDPOwnedRefusalDoesNotLeakOrDuplicate(t *testing.T) {
	t.Parallel()

	transportErr := errors.New("stream reset")
	stream := &scriptedStream{datagramsEnabled: true, ownedAvailable: true, refuseWith: transportErr}
	conn := newScriptedConn(t, stream)

	buffer := newTestPayload([]byte("x"), connectUDPOwnedHeadroom)
	err := conn.WritePacket(buffer, M.ParseSocksaddr("192.0.2.1:443"))
	require.ErrorIs(t, err, transportErr)
	require.Empty(t, stream.datagrams, "a refused send must not appear as sent")
	require.Empty(t, stream.capsules, "and must not become a capsule")
	require.Zero(t, buffer.Len(), "the caller releases its own buffer on the error path")
}

// TestConnectUDPErrorClassMatrix pins what every error class does. The dangerous generalisation is
// "any error means try a capsule": it turns a broken connection into a silent protocol change.
func TestConnectUDPErrorClassMatrix(t *testing.T) {
	t.Parallel()

	tooLarge := &DatagramTooLargeError{MaxPayloadSize: 1200}
	for _, testCase := range []struct {
		name         string
		err          error
		expectCapsul bool
		expectError  bool
	}{
		{"capability refused", ErrDatagramUnsupported, true, false},
		// DatagramTooLargeError unwraps to ErrDatagramUnsupported, so the single-packet path
		// carries it as a capsule too -- the same decision the batch path makes. Measured here
		// rather than assumed: this is why the two paths agree.
		{"too large", tooLarge, true, false},
		{"net closed", net.ErrClosed, false, true},
		{"context canceled", context.Canceled, false, true},
		{"stream reset", errors.New("stream reset"), false, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			stream := &scriptedStream{datagramsEnabled: true, ownedAvailable: true, refuseWith: testCase.err}
			conn := newScriptedConn(t, stream)
			err := conn.WritePacket(newTestPayload([]byte("p"), connectUDPOwnedHeadroom), M.ParseSocksaddr("192.0.2.1:443"))
			if testCase.expectError {
				require.Error(t, err, "this class must reach the caller")
			} else {
				require.NoError(t, err)
			}
			if testCase.expectCapsul {
				require.Len(t, stream.capsules, 1)
			} else {
				require.Empty(t, stream.capsules)
			}
		})
	}
}

// TestConnectUDPOwnedBatchTooLargeIsACapsule proves the batch path keeps its size decision: a
// too-large packet is carried by a capsule, because that is a size problem rather than a broken
// connection.
func TestConnectUDPOwnedBatchTooLargeIsACapsule(t *testing.T) {
	t.Parallel()

	tooLarge := &DatagramTooLargeError{MaxPayloadSize: 1200}
	stream := &scriptedStream{datagramsEnabled: true, ownedAvailable: true, refuseWith: tooLarge}
	conn := newScriptedConn(t, stream)
	writer := &connectedBatchWriter{conn: conn}

	require.NoError(t, writer.WriteConnectedPacketBatch([]*buf.Buffer{
		newTestPayload([]byte("big"), connectUDPOwnedHeadroom),
	}))
	require.Empty(t, stream.datagrams)
	require.Len(t, stream.capsules, 1)
}

// TestConnectUDPOwnedPeerWithoutDatagrams proves a peer that did not negotiate HTTP Datagrams gets
// capsules even though the stream implements the owned API.
func TestConnectUDPOwnedPeerWithoutDatagrams(t *testing.T) {
	t.Parallel()

	stream := &scriptedStream{datagramsEnabled: false, ownedAvailable: true, refuseWith: ErrDatagramUnsupported}
	conn := newScriptedConn(t, stream)
	writer := &connectedBatchWriter{conn: conn}

	require.NoError(t, writer.WriteConnectedPacketBatch([]*buf.Buffer{
		newTestPayload([]byte("a"), connectUDPOwnedHeadroom),
		newTestPayload([]byte("b"), connectUDPOwnedHeadroom),
	}))
	require.Empty(t, stream.datagrams, "no datagram may reach a peer that cannot read one")
	require.Len(t, stream.capsules, 2)
}

// TestConnectUDPOwnedLostTailIsReleased proves the packets after a fault are released by the batch
// writer and never sent.
func TestConnectUDPOwnedLostTailIsReleased(t *testing.T) {
	t.Parallel()

	transportErr := errors.New("queue full")
	stream := &scriptedStream{datagramsEnabled: true, ownedAvailable: true, refuseWith: transportErr, refuseAfter: 2}
	conn := newScriptedConn(t, stream)
	writer := &connectedBatchWriter{conn: conn}

	batch := make([]*buf.Buffer, 8)
	for i := range batch {
		batch[i] = newTestPayload([]byte(fmt.Sprintf("p%d", i)), connectUDPOwnedHeadroom)
	}
	err := writer.WriteConnectedPacketBatch(batch)
	require.ErrorIs(t, err, transportErr)
	require.Len(t, stream.datagrams, 2, "exactly the two accepted packets were sent")
	for i, buffer := range batch {
		require.Zero(t, buffer.Len(), "buffer %d must be consumed once, sent or released", i)
	}
}

// TestConnectUDPOwnedCapsuleCarriesTheOriginalBytes proves the refusal path hands the capsule the
// Context ID plus the original payload, i.e. the HTTP/3 layer rolled its prefix back.
func TestConnectUDPOwnedCapsuleCarriesTheOriginalBytes(t *testing.T) {
	t.Parallel()

	payload := []byte("capsule payload")
	stream := &scriptedStream{datagramsEnabled: true, ownedAvailable: true, refuseWith: ErrDatagramUnsupported}
	conn := newScriptedConn(t, stream)

	require.NoError(t, conn.WritePacket(newTestPayload(payload, connectUDPOwnedHeadroom), M.ParseSocksaddr("192.0.2.1:443")))
	require.Len(t, stream.capsules, 1)
	capsule := stream.capsules[0]
	require.Equal(t, byte(CapsuleTypeDatagram), capsule[0])
	// type byte, varint length, then the datagram (context ID + payload)
	require.True(t, len(capsule) > 2)
	require.Equal(t, byte(0), capsule[2], "the Context ID must be intact inside the capsule")
	require.Equal(t, payload, capsule[3:], "and the payload must be byte-identical")
}

// TestConnectUDPConcurrentWritersStayAccounted proves two concurrent batch writers do not
// double-consume or lose a packet.
func TestConnectUDPConcurrentWritersStayAccounted(t *testing.T) {
	t.Parallel()

	stream := &scriptedStream{datagramsEnabled: true, ownedAvailable: true}
	conn := newScriptedConn(t, stream)
	writer := &connectedBatchWriter{conn: conn}

	const writers = 8
	const perWriter = 16
	var waitGroup sync.WaitGroup
	var failures atomic.Int64
	for w := 0; w < writers; w++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			batch := make([]*buf.Buffer, perWriter)
			for i := range batch {
				batch[i] = newTestPayload([]byte(fmt.Sprintf("w%d-%d", worker, i)), connectUDPOwnedHeadroom)
			}
			if err := writer.WriteConnectedPacketBatch(batch); err != nil {
				failures.Add(1)
			}
		}(w)
	}
	waitGroup.Wait()

	require.Zero(t, failures.Load(), "no batch may fail on a healthy stream")
	require.Len(t, stream.datagrams, writers*perWriter, "every packet is sent exactly once")
	require.Equal(t, writers*perWriter, stream.ownedReleases)
}
