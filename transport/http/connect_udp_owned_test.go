package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"sync"
	"testing"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The CONNECT-UDP owned path moves buffer ownership across three layers: this package prepends the
// MASQUE Context ID, the HTTP/3 layer prepends its quarter stream ID into the same headroom, and
// quic-go takes the bytes and releases the buffer once. The layer where this can go wrong is the
// composition: after a successful owned send the buffer belongs to the transport, so any release
// here is a double release that corrupts unrelated traffic later.
//
// The doubles below keep the REAL contract rather than counting calls:
//
//   - ownedStream releases on success and reads the payload at release time, so a release that
//     happened too early is visible in the recorded bytes;
//   - retainOwnership makes a successful owned send keep the buffer, which lets a test assert that
//     this package did not touch a buffer it had already handed over. That is the direct,
//     deterministic check for the double-release mistake.

type ownedStream struct {
	access     sync.Mutex
	serialized [][]byte
	capsules   [][]byte
	ownedCalls int
	copyCalls  int
	releases   int
	// failWith makes owned sends fail with it. failAfter is how many owned sends succeed first;
	// 0 means fail immediately.
	failWith  error
	failAfter int
	// datagramsEnabled is what DatagramsEnabled reports.
	datagramsEnabled bool
	// retainOwnership makes a successful owned send keep the buffer instead of releasing it,
	// modelling a transport that settles ownership after the call returns.
	retainOwnership bool
	// releaseHook runs while the payload is still valid, just before releasing.
	releaseHook func(buffer *buf.Buffer)
}

func (s *ownedStream) DatagramsEnabled() bool { return s.datagramsEnabled }

func (s *ownedStream) Read([]byte) (int, error) { return 0, io.EOF }

func (s *ownedStream) Write(p []byte) (int, error) {
	s.access.Lock()
	s.capsules = append(s.capsules, append([]byte(nil), p...))
	s.access.Unlock()
	return len(p), nil
}

func (s *ownedStream) Close() error { return nil }

func (s *ownedStream) SendDatagram(payload []byte) error {
	s.access.Lock()
	s.copyCalls++
	s.serialized = append(s.serialized, append([]byte(nil), payload...))
	s.access.Unlock()
	return nil
}

func (s *ownedStream) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *ownedStream) SendDatagramOwned(buffer *buf.Buffer) error {
	s.access.Lock()
	s.ownedCalls++
	failWith := s.failWith
	shouldFail := failWith != nil && s.ownedCalls > s.failAfter
	s.access.Unlock()
	if shouldFail {
		// The contract: on error the caller still owns the buffer, untouched.
		return failWith
	}
	s.access.Lock()
	// Read the payload while it is still ours: this is what the transport does when it
	// serializes the frame, and it is the last moment the bytes are guaranteed valid.
	s.serialized = append(s.serialized, append([]byte(nil), buffer.Bytes()...))
	hook := s.releaseHook
	retain := s.retainOwnership
	if !retain {
		s.releases++
	}
	s.access.Unlock()
	if hook != nil {
		hook(buffer)
	}
	if retain {
		// Do NOT release: the test asserts the caller left the buffer alone.
		return nil
	}
	buffer.Release()
	return nil
}

// plainStream is a datagram stream WITHOUT the owned capability, i.e. the H2 fallback and every
// build without the capability.
type plainStream struct {
	access    sync.Mutex
	copied    [][]byte
	capsules  [][]byte
	copyCalls int
}

func (s *plainStream) DatagramsEnabled() bool { return true }

func (s *plainStream) Read([]byte) (int, error) { return 0, io.EOF }

func (s *plainStream) Write(p []byte) (int, error) {
	s.access.Lock()
	s.capsules = append(s.capsules, append([]byte(nil), p...))
	s.access.Unlock()
	return len(p), nil
}

func (s *plainStream) Close() error { return nil }

func (s *plainStream) SendDatagram(payload []byte) error {
	s.access.Lock()
	s.copyCalls++
	s.copied = append(s.copied, append([]byte(nil), payload...))
	s.access.Unlock()
	return nil
}

func (s *plainStream) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// newTestPacketConn builds the connection directly instead of through the constructor, so the
// receive loops never start: these tests are about the send path's ownership only.
func newTestPacketConn(t testing.TB, stream DatagramStream) *http3PacketConn {
	t.Helper()
	return &http3PacketConn{
		stream:         stream,
		ownedDatagrams: AsOwnedDatagramSender(stream),
		destination:    M.ParseSocksaddr("192.0.2.1:443"),
		packets:        make(chan *buf.Buffer, 4),
		ctx:            context.Background(),
	}
}

// newTestPayload builds the shape a producer hands to WritePacket: a payload with headroom in
// front of it, exactly like bufio.CopyPacket allocates one.
func newTestPayload(payload []byte, headroom int) *buf.Buffer {
	buffer := buf.NewSize(headroom + len(payload))
	buffer.Resize(headroom, 0)
	buffer.Write(payload)
	return buffer
}

// TestConnectUDPOwnedIsTakenAndTransfersOwnership proves the owned path is actually selected and
// that the bytes it transfers are the Context-ID framing, not the bare payload.
func TestConnectUDPOwnedIsTakenAndTransfersOwnership(t *testing.T) {
	t.Parallel()

	payload := []byte("a connect-udp datagram payload")
	stream := &ownedStream{datagramsEnabled: true}
	conn := newTestPacketConn(t, stream)

	// Capture the payload as seen at release time, which is the last point it must still be valid.
	var atRelease []byte
	stream.releaseHook = func(buffer *buf.Buffer) {
		atRelease = append([]byte(nil), buffer.Bytes()...)
	}

	buffer := newTestPayload(payload, connectUDPOwnedHeadroom)
	require.NoError(t, conn.WritePacket(buffer, M.ParseSocksaddr("192.0.2.1:443")))

	require.Equal(t, 1, stream.ownedCalls, "the owned capability must be used")
	require.Equal(t, 0, stream.copyCalls, "the copying path must not also run")
	require.Equal(t, 1, stream.releases, "the transport releases exactly once")
	require.Len(t, stream.serialized, 1)
	require.Equal(t, byte(0), stream.serialized[0][0], "the MASQUE Context ID leads the payload")
	require.Equal(t, payload, stream.serialized[0][1:], "and the payload is verbatim after it")
	require.Equal(t, stream.serialized[0], atRelease,
		"the bytes at release time must equal the bytes at serialization time: no early release")
	require.Zero(t, buffer.Len(), "the transferred buffer is gone from the caller")
}

// TestConnectUDPOwnedDoesNotReleaseWhatItTransferred is the double-release test, and it is
// deterministic: the transport accepts ownership and holds it, so the buffer must still be alive
// after WritePacket returns. A release here would zero it.
func TestConnectUDPOwnedDoesNotReleaseWhatItTransferred(t *testing.T) {
	t.Parallel()

	payload := []byte("owned but not yet serialized")
	stream := &ownedStream{datagramsEnabled: true, retainOwnership: true}
	conn := newTestPacketConn(t, stream)

	buffer := newTestPayload(payload, connectUDPOwnedHeadroom)
	require.NoError(t, conn.WritePacket(buffer, M.ParseSocksaddr("192.0.2.1:443")))

	require.Equal(t, 1, stream.ownedCalls)
	require.Equal(t, 0, stream.releases, "the double is still holding ownership")
	require.NotZero(t, buffer.Len(),
		"WritePacket must NOT release a buffer whose ownership it transferred")
	require.Equal(t, append([]byte{0}, payload...), buffer.Bytes(),
		"and the buffer is untouched, still carrying the Context ID it was sent with")
	buffer.Release()
}

// TestConnectUDPOwnedFailureKeepsCallerOwnership proves a refused owned send does not lose the
// datagram silently and does not downgrade a transport fault into a capsule.
func TestConnectUDPOwnedFailureKeepsCallerOwnership(t *testing.T) {
	t.Parallel()

	transportErr := errors.New("quic connection is gone")
	payload := []byte("payload")
	stream := &ownedStream{datagramsEnabled: true, failWith: transportErr}
	conn := newTestPacketConn(t, stream)

	buffer := newTestPayload(payload, connectUDPOwnedHeadroom)
	err := conn.WritePacket(buffer, M.ParseSocksaddr("192.0.2.1:443"))
	require.ErrorIs(t, err, transportErr, "the transport error is returned unchanged")
	require.Equal(t, 1, stream.ownedCalls)
	require.Equal(t, 0, stream.releases, "the transport never took ownership")
	require.Empty(t, stream.capsules, "a transport fault must not become a capsule write")
	require.Zero(t, buffer.Len(), "WritePacket consumes its input, so it releases it here")
}

// TestConnectUDPOwnedUnsupportedFallsBackToCapsule proves capability refusal keeps the existing
// capsule behaviour, byte for byte.
func TestConnectUDPOwnedUnsupportedFallsBackToCapsule(t *testing.T) {
	t.Parallel()

	payload := []byte("payload that has to ride a capsule")
	stream := &ownedStream{datagramsEnabled: true, failWith: ErrDatagramUnsupported}
	conn := newTestPacketConn(t, stream)

	buffer := newTestPayload(payload, connectUDPOwnedHeadroom)
	require.NoError(t, conn.WritePacket(buffer, M.ParseSocksaddr("192.0.2.1:443")))

	require.Equal(t, 1, stream.ownedCalls)
	require.Equal(t, 0, stream.copyCalls, "the copying datagram path is not retried")
	require.Equal(t, 0, stream.releases)
	require.Len(t, stream.capsules, 1, "exactly one capsule")
	capsule := stream.capsules[0]
	require.Equal(t, byte(CapsuleTypeDatagram), capsule[0])
	require.Contains(t, string(capsule), string(append([]byte{0}, payload...)),
		"the capsule carries the Context ID and the payload")
	require.Zero(t, buffer.Len(), "the capsule writer consumed the datagram")
}

// TestConnectUDPWithoutOwnedKeepsCopyingPath pins the fallback for a stream that cannot take
// ownership: nothing about it may change.
func TestConnectUDPWithoutOwnedKeepsCopyingPath(t *testing.T) {
	t.Parallel()

	payload := []byte("copied datagram")
	stream := &plainStream{}
	conn := newTestPacketConn(t, stream)
	require.Nil(t, conn.ownedDatagrams, "a plain stream has no owned capability")

	buffer := newTestPayload(payload, connectUDPOwnedHeadroom)
	require.NoError(t, conn.WritePacket(buffer, M.ParseSocksaddr("192.0.2.1:443")))

	require.Equal(t, 1, stream.copyCalls)
	require.Len(t, stream.copied, 1)
	require.Equal(t, byte(0), stream.copied[0][0])
	require.Equal(t, payload, stream.copied[0][1:])
	require.Zero(t, buffer.Len(), "the copying path releases after the transport copied")
}

// TestConnectUDPOwnedBatchConsumesEveryBuffer proves the batch writer hands every buffer over and
// releases none of them itself.
func TestConnectUDPOwnedBatchConsumesEveryBuffer(t *testing.T) {
	t.Parallel()

	payloads := [][]byte{[]byte("first"), []byte("second"), []byte("third")}
	stream := &ownedStream{datagramsEnabled: true}
	conn := newTestPacketConn(t, stream)
	writer := &connectedBatchWriter{conn: conn}

	buffers := make([]*buf.Buffer, len(payloads))
	for i, payload := range payloads {
		buffers[i] = newTestPayload(payload, connectUDPOwnedHeadroom)
	}
	require.NoError(t, writer.WriteConnectedPacketBatch(buffers))

	require.Equal(t, 3, stream.ownedCalls)
	require.Equal(t, 0, stream.copyCalls)
	require.Equal(t, 3, stream.releases)
	require.Empty(t, stream.capsules)
	for i, buffer := range buffers {
		require.Zero(t, buffer.Len(), "buffer %d must be consumed exactly once", i)
	}
	require.Len(t, stream.serialized, 3)
	for i, payload := range payloads {
		require.Equal(t, append([]byte{0}, payload...), stream.serialized[i],
			"packet %d keeps its position and its framing", i)
	}
}

// TestConnectUDPOwnedBatchCapsuleFallbackConsumesEveryBuffer is the same batch where every send is
// refused by capability, so every packet must end up as a capsule and none may leak.
func TestConnectUDPOwnedBatchCapsuleFallbackConsumesEveryBuffer(t *testing.T) {
	t.Parallel()

	stream := &ownedStream{datagramsEnabled: true, failWith: ErrDatagramUnsupported}
	conn := newTestPacketConn(t, stream)
	writer := &connectedBatchWriter{conn: conn}

	buffers := []*buf.Buffer{
		newTestPayload([]byte("one"), connectUDPOwnedHeadroom),
		newTestPayload([]byte("two"), connectUDPOwnedHeadroom),
	}
	require.NoError(t, writer.WriteConnectedPacketBatch(buffers))

	require.Equal(t, 2, stream.ownedCalls)
	require.Len(t, stream.capsules, 2, "one capsule per packet, in order")
	require.Contains(t, string(stream.capsules[0]), "one")
	require.Contains(t, string(stream.capsules[1]), "two")
	for i, buffer := range buffers {
		require.Zero(t, buffer.Len(), "capsule buffer %d must be consumed", i)
	}
}

// TestConnectUDPOwnedBatchReleasesRemainderOnFault proves an error in the middle of a batch
// releases the packets the loop never reached, and does not touch the ones already transferred.
func TestConnectUDPOwnedBatchReleasesRemainderOnFault(t *testing.T) {
	t.Parallel()

	transportErr := errors.New("link down")
	stream := &ownedStream{datagramsEnabled: true, failWith: transportErr, failAfter: 1}
	conn := newTestPacketConn(t, stream)
	writer := &connectedBatchWriter{conn: conn}

	first := newTestPayload([]byte("accepted"), connectUDPOwnedHeadroom)
	second := newTestPayload([]byte("faulted"), connectUDPOwnedHeadroom)
	third := newTestPayload([]byte("never reached"), connectUDPOwnedHeadroom)

	err := writer.WriteConnectedPacketBatch([]*buf.Buffer{first, second, third})
	require.ErrorIs(t, err, transportErr)
	require.Zero(t, first.Len(), "the accepted packet was transferred")
	require.Zero(t, second.Len(), "the faulted packet is consumed by the error path")
	require.Zero(t, third.Len(), "the unreached packet is released by the batch writer")
	require.Equal(t, 1, stream.releases, "only the accepted packet was transferred")
}

// TestConnectUDPFrontHeadroomDependsOnCapability pins the geometry: the owned path needs room for
// the Context ID plus the widest quarter-stream-ID varint, the capsule fallback for its framing.
func TestConnectUDPFrontHeadroomDependsOnCapability(t *testing.T) {
	t.Parallel()

	owned := newTestPacketConn(t, &ownedStream{datagramsEnabled: true})
	require.Equal(t, connectUDPOwnedHeadroom, owned.FrontHeadroom())
	require.Equal(t, 9, owned.FrontHeadroom(), "1 byte Context ID + 8 byte quarter stream ID")

	plain := newTestPacketConn(t, &plainStream{})
	require.Equal(t, CapsuleHeadroom, plain.FrontHeadroom())
}

// The benchmark models the two implementations at the boundary this change touches.
//
// A copying send builds a frame buffer with the quarter-stream-ID prefix and copies the payload
// into it (what the HTTP/3 layer does today); an owned send writes the prefix into headroom that
// already exists and hands the buffer over. The producer cost (allocating the payload buffer) is
// identical in both arms and stays in the measurement, so the delta is the send path.

var benchSink []byte

type benchCopyingStream struct{}

func (benchCopyingStream) DatagramsEnabled() bool { return true }

func (benchCopyingStream) Read([]byte) (int, error) { return 0, io.EOF }

func (benchCopyingStream) Write(p []byte) (int, error) { return len(p), nil }

func (benchCopyingStream) Close() error { return nil }

func (benchCopyingStream) SendDatagram(payload []byte) error {
	frame := make([]byte, 0, len(payload)+1)
	frame = append(frame, 0)
	frame = append(frame, payload...)
	benchSink = frame
	return nil
}

func (benchCopyingStream) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type benchOwnedStream struct{}

func (benchOwnedStream) DatagramsEnabled() bool { return true }

func (benchOwnedStream) Read([]byte) (int, error) { return 0, io.EOF }

func (benchOwnedStream) Write(p []byte) (int, error) { return len(p), nil }

func (benchOwnedStream) Close() error { return nil }

func (benchOwnedStream) SendDatagram(payload []byte) error { return nil }

func (benchOwnedStream) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (benchOwnedStream) SendDatagramOwned(buffer *buf.Buffer) error {
	header := buffer.ExtendHeader(1)
	header[0] = 0
	buffer.Release()
	return nil
}

func benchmarkWritePacket(b *testing.B, stream DatagramStream, size int) {
	payload := bytes.Repeat([]byte{0x5a}, size)
	conn := newTestPacketConn(b, stream)
	destination := conn.destination
	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = conn.WritePacket(newTestPayload(payload, connectUDPOwnedHeadroom), destination)
	}
}

func BenchmarkConnectUDPWritePacket(b *testing.B) {
	for _, size := range []int{64, 1200, 1400, 4096} {
		b.Run("owned/"+strconv.Itoa(size), func(b *testing.B) {
			benchmarkWritePacket(b, benchOwnedStream{}, size)
		})
		b.Run("copying/"+strconv.Itoa(size), func(b *testing.B) {
			benchmarkWritePacket(b, benchCopyingStream{}, size)
		})
	}
}
