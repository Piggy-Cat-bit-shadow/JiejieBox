//go:build with_quic

package v2rayquic

// The cross-path migration acceptance test for the pinned QUIC library.
//
// # What this pins
//
// The window's deliverable is "one active V2Ray QUIC flow keeps carrying checked payload while
// the physical path changes, and the old socket is then physically closed". The capability
// lives in the dependency, so this test drives it through the pinned module exactly the way the
// transport does:
//
//   - the connection is dialed on a connected socket through the library (quic.DialConn), the same
//     shape the transport uses, and the test keeps both the socket and the transport;
//   - a second real socket becomes a candidate path on the SAME connection (Conn.AddPath) through
//     the same `connectedPacketConn` adapter production uses, it is validated (Path.Probe) and
//     switched to (Path.Switch);
//   - one already-open stream keeps carrying sequenced, CRC-checked records across the switch,
//     every record is ACKED BY THE ENDPOINT on the same stream, and the endpoint still sees ONE
//     connection and ONE stream - a reconnect cannot pass this;
//   - the transport that carried the handshake is retired (Conn.RetireTransport) and its socket
//     physically closed, and the stream still carries a record whose acknowledgement proves the
//     ENDPOINT received bytes sent after that close.
//
// The acknowledgement is the part the earlier version of this test lacked: it wrote records and
// read one ack frame without checking which record it belonged to, so a peer that acknowledged
// only the pre-switch record satisfied it. The negative control at the bottom of this file
// reproduces exactly that false green.
//
// Everything here is real: real QUIC handshake, real UDP sockets, real streams, sequence
// numbers and an application-layer digest.

import (
	"context"
	stdTLS "crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	boxTLS "github.com/sagernet/sing-box/common/tls"
	"github.com/stretchr/testify/require"
)

const migrationSettle = 10 * time.Second

var (
	migrationCertificateOnce sync.Once
	migrationCertificatePair stdTLS.Certificate
	migrationCertificateErr  error
)

func migrationCertificate(t *testing.T) stdTLS.Certificate {
	t.Helper()
	migrationCertificateOnce.Do(func() {
		keyPEM, certPEM, err := boxTLS.GenerateCertificate(nil, nil, time.Now, "localhost", time.Now().Add(24*time.Hour))
		if err != nil {
			migrationCertificateErr = err
			return
		}
		migrationCertificatePair, migrationCertificateErr = stdTLS.X509KeyPair(certPEM, keyPEM)
	})
	require.NoError(t, migrationCertificateErr)
	return migrationCertificatePair
}

func migrationTLSConfig(t *testing.T) *stdTLS.Config {
	return &stdTLS.Config{
		Certificates:       []stdTLS.Certificate{migrationCertificate(t)},
		NextProtos:         []string{http3.NextProtoH3},
		InsecureSkipVerify: true,
	}
}

// migrationRecord is one application-layer record. Its framing and digest exist so that
// "the old stream continued" cannot be confused with "a new dial started at sequence 0":
// the receiver checks the sequence gap and the digest of every record it accepts, and the
// acknowledgement carries the same sequence and payload back.
type migrationRecord struct {
	sequence uint64
	payload  []byte
}

func encodeMigrationRecord(sequence uint64, payload []byte) []byte {
	framed := make([]byte, 4+8+4+len(payload))
	binary.BigEndian.PutUint32(framed[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint64(framed[4:12], sequence)
	binary.BigEndian.PutUint32(framed[12:16], crc32.ChecksumIEEE(payload))
	copy(framed[16:], payload)
	return framed
}

func decodeMigrationRecord(header []byte, payload []byte) (migrationRecord, bool) {
	length := binary.BigEndian.Uint32(header[0:4])
	sequence := binary.BigEndian.Uint64(header[4:12])
	digest := binary.BigEndian.Uint32(header[12:16])
	if int(length) != len(payload) || crc32.ChecksumIEEE(payload) != digest {
		return migrationRecord{}, false
	}
	return migrationRecord{sequence: sequence, payload: payload}, true
}

// migrationServer is one real QUIC endpoint that records, per stream, the records it accepted
// and acknowledges each one it is configured to acknowledge.
type migrationServer struct {
	listener    *quic.Listener
	addr        net.Addr
	handshakes  atomic.Int32
	streamCount atomic.Int32
	// acknowledge decides whether a received record is acknowledged. nil means every record.
	acknowledge func(sequence uint64) bool

	access  sync.Mutex
	streams []*migrationStreamState
	failure error
}

type migrationStreamState struct {
	nextSequence uint64
	seen         []migrationRecord
}

func startMigrationServer(t *testing.T, acknowledge func(sequence uint64) bool) *migrationServer {
	return startMigrationServerAt(t, "127.0.0.1:0", acknowledge)
}

// startMigrationServerAt is the same endpoint bound to a given address: the namespace test needs
// every address of its namespace, the loopback tests need the default one.
func startMigrationServerAt(t *testing.T, address string, acknowledge func(sequence uint64) bool) *migrationServer {
	t.Helper()
	listener, err := quic.ListenAddr(address, migrationTLSConfig(t), &quic.Config{})
	require.NoError(t, err)
	server := &migrationServer{listener: listener, addr: listener.Addr(), acknowledge: acknowledge}
	t.Cleanup(func() { _ = listener.Close() })
	go server.acceptLoop()
	return server
}

func (s *migrationServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept(context.Background())
		if err != nil {
			return
		}
		s.handshakes.Add(1)
		go s.serveConnection(conn)
	}
}

func (s *migrationServer) serveConnection(conn *quic.Conn) {
	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			return
		}
		s.access.Lock()
		index := len(s.streams)
		s.streams = append(s.streams, &migrationStreamState{})
		s.access.Unlock()
		s.streamCount.Add(1)
		go s.readStream(stream, index)
	}
}

func (s *migrationServer) readStream(stream *quic.Stream, index int) {
	header := make([]byte, 16)
	for {
		if _, err := io.ReadFull(stream, header); err != nil {
			return
		}
		length := int(binary.BigEndian.Uint32(header[0:4]))
		payload := make([]byte, length)
		if _, err := io.ReadFull(stream, payload); err != nil {
			return
		}
		record, ok := decodeMigrationRecord(header, payload)

		s.access.Lock()
		state := s.streams[index]
		switch {
		case !ok:
			s.failure = errors.New("record digest or framing mismatch")
		case record.sequence != state.nextSequence:
			s.failure = fmt.Errorf("sequence gap: want %d, got %d", state.nextSequence, record.sequence)
		default:
			state.nextSequence = record.sequence + 1
			state.seen = append(state.seen, record)
			// The acknowledgement travels back on the SAME stream and carries the same sequence
			// and payload, so a migration that only preserved the send direction cannot pass and
			// the caller can check WHICH record was acknowledged.
			if s.acknowledge == nil || s.acknowledge(record.sequence) {
				_, _ = stream.Write(encodeMigrationRecord(record.sequence, record.payload))
			}
		}
		s.access.Unlock()
	}
}

func (s *migrationServer) waitRecords(t *testing.T, index int, want int) []migrationRecord {
	t.Helper()
	var seen []migrationRecord
	require.Eventually(t, func() bool {
		s.access.Lock()
		defer s.access.Unlock()
		if len(s.streams) <= index {
			return false
		}
		seen = append([]migrationRecord(nil), s.streams[index].seen...)
		return len(seen) >= want
	}, migrationSettle, 2*time.Millisecond, "the endpoint did not accept %d records", want)
	s.access.Lock()
	defer s.access.Unlock()
	require.NoError(t, s.failure)
	return seen
}

// migrationRecordStream is the slice of a QUIC stream the record protocol needs. It is a method
// set, not *quic.Stream, because the production client hands out a net.Conn wrapper around one.
type migrationRecordStream interface {
	io.Reader
	io.Writer
	SetReadDeadline(time.Time) error
}

// sendRecord writes one record and requires the endpoint's acknowledgement of THAT record: same
// sequence, same payload, valid digest.
func sendRecord(t *testing.T, stream migrationRecordStream, sequence uint64, payload string) {
	t.Helper()
	_, err := stream.Write(encodeMigrationRecord(sequence, []byte(payload)))
	require.NoError(t, err, "record %d must be accepted by the migrated stream", sequence)
	require.NoError(t, stream.SetReadDeadline(time.Now().Add(migrationSettle)))
	header := make([]byte, 16)
	_, err = io.ReadFull(stream, header)
	require.NoError(t, err, "the endpoint never acknowledged record %d", sequence)
	ackPayload := make([]byte, binary.BigEndian.Uint32(header[0:4]))
	_, err = io.ReadFull(stream, ackPayload)
	require.NoError(t, err)
	acknowledged, ok := decodeMigrationRecord(header, ackPayload)
	require.True(t, ok, "the acknowledgement of record %d must be a well-formed record", sequence)
	require.Equal(t, sequence, acknowledged.sequence, "the acknowledgement must be for the record just sent")
	require.Equal(t, payload, string(acknowledged.payload), "the acknowledgement must carry that record's payload")
}

// migrationDial dials a real endpoint through a connected socket, the way the transport does, and
// returns the connection, the transport that carried the handshake and the socket.
func migrationDial(t *testing.T, ctx context.Context, serverAddr net.Addr) (*quic.Conn, *quic.Transport, net.Conn) {
	t.Helper()
	socket, err := net.Dial("udp", serverAddr.String())
	require.NoError(t, err)
	conn, err := quic.DialConn(ctx, socket, migrationTLSConfig(t), &quic.Config{})
	require.NoError(t, err)
	transport := conn.HandshakeTransport()
	require.NotNil(t, transport, "the pinned library must report the transport the connection was dialed on")
	return conn, transport, socket
}

// addMigrationPath attaches one real UNCONNECTED packet socket as a candidate path and validates it.
//
// The socket shape is the production shape after Q30: quic-go writes every candidate-path packet
// with an explicit destination sockaddr, which a connected socket rejects on Darwin. An
// unconnected ListenPacket socket is what that write is designed for, and it is also the only shape
// that exercises the library's OOB transport on Windows, so this test now covers the branch the
// macOS job failed on instead of silently downgrading to the basic transport.
func addMigrationPath(t *testing.T, ctx context.Context, conn *quic.Conn, serverAddr net.Addr) (*quic.Path, net.PacketConn) {
	t.Helper()
	socket, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	path, err := conn.AddPath(&quic.Transport{Conn: socket})
	require.NoError(t, err, "the pinned library must offer a candidate path on a second socket")
	probeCtx, probeCancel := context.WithTimeout(ctx, migrationSettle)
	defer probeCancel()
	require.NoError(t, path.Probe(probeCtx), "path validation must complete on a live second socket")
	return path, socket
}

// TestActiveStreamSurvivesAPathSwitchAndTheOldSocketsClose is the acceptance: one connection,
// one already-open stream, acknowledged records continuing across a switch, then the handshake
// socket is physically closed and the endpoint still receives and acknowledges the next record.
func TestActiveStreamSurvivesAPathSwitchAndTheOldSocketsClose(t *testing.T) {
	server := startMigrationServer(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), migrationSettle)
	defer cancel()
	conn, handshakeTransport, handshakeSocket := migrationDial(t, ctx, server.addr)
	t.Cleanup(func() { _ = conn.CloseWithError(0, "") })

	stream, err := conn.OpenStream()
	require.NoError(t, err)

	sendRecord(t, stream, 0, "before-the-switch")
	require.Len(t, server.waitRecords(t, 0, 1), 1)

	// The second real path: its own socket, its own transport, the same connection.
	path, _ := addMigrationPath(t, ctx, conn, server.addr)
	require.ErrorIs(t, path.Switch(), nil)
	// Switch only registers the request; the run loop installs the new send path. The test does
	// not poll Conn.LocalAddr to observe that: LocalAddr reads the send connection without
	// synchronization, so polling it during a switch is a data race in the library (recorded in
	// the window handoff). The acknowledged records below and the retirement wait force and then
	// prove the applied state instead.

	// The same stream continues with an unbroken sequence, and every record is acknowledged by the
	// endpoint through the same stream.
	for sequence := uint64(1); sequence <= 3; sequence++ {
		sendRecord(t, stream, sequence, fmt.Sprintf("after-the-switch-%d", sequence))
	}
	require.Len(t, server.waitRecords(t, 0, 4), 4)
	require.Equal(t, int32(1), server.handshakes.Load(), "the switch must not establish a second connection")
	require.Equal(t, int32(1), server.streamCount.Load(), "the switch must not open a second stream")

	// Retire the transport that carried the handshake, then physically close its socket.
	retireCtx, retireCancel := context.WithTimeout(ctx, migrationSettle)
	defer retireCancel()
	require.NoError(t, conn.RetireTransport(retireCtx, handshakeTransport))
	require.NoError(t, handshakeSocket.Close())
	select {
	case <-conn.Context().Done():
		t.Fatal("closing the retired socket ended the migrated connection")
	case <-time.After(150 * time.Millisecond):
	}

	// Record 4 is written AFTER the old socket is physically closed, and its acknowledgement is
	// the evidence that the endpoint received bytes over the new path: sequence, payload and CRC
	// all checked against the record that was just sent.
	sendRecord(t, stream, 4, "after-the-old-socket-closed")
	seen := server.waitRecords(t, 0, 5)
	require.Len(t, seen, 5)
	require.Equal(t, uint64(4), seen[4].sequence)
	require.Equal(t, "after-the-old-socket-closed", string(seen[4].payload))

	require.Nil(t, conn.Context().Err(), "the connection must still be live")
	require.Equal(t, int32(1), server.handshakes.Load())
	require.Equal(t, int32(1), server.streamCount.Load())
}

// TestAPeerThatOnlyAckedBeforeTheSwitchWouldSatisfyTheOldAssertion is the negative control for
// the acknowledgements above. It replays the same sequence against an endpoint that acknowledges
// only the pre-switch record, and shows what the earlier version of this test observed:
//
//   - the record written after the old socket closed is accepted locally and the endpoint's
//     sequence accounting is fine;
//   - reading ONE ack frame and decoding it still succeeds - because the pre-switch ack was never
//     read. That frame is the acknowledgement of record 0, so the old assertion proved nothing
//     about the post-close record.
//
// The test then requires the check this file now performs to FAIL against that endpoint: no
// acknowledgement of record 4 arrives within a bounded window. Together the two observations are
// what makes the acceptance test's ack check load-bearing instead of decorative.
func TestAPeerThatOnlyAckedBeforeTheSwitchWouldSatisfyTheOldAssertion(t *testing.T) {
	server := startMigrationServer(t, func(sequence uint64) bool { return sequence == 0 })

	ctx, cancel := context.WithTimeout(context.Background(), migrationSettle)
	defer cancel()
	conn, handshakeTransport, handshakeSocket := migrationDial(t, ctx, server.addr)
	t.Cleanup(func() { _ = conn.CloseWithError(0, "") })
	stream, err := conn.OpenStream()
	require.NoError(t, err)

	// Record 0 is written but its acknowledgement is deliberately left unread.
	_, err = stream.Write(encodeMigrationRecord(0, []byte("before-the-switch")))
	require.NoError(t, err)
	require.Len(t, server.waitRecords(t, 0, 1), 1)

	path, _ := addMigrationPath(t, ctx, conn, server.addr)
	require.NoError(t, path.Switch())
	for sequence := uint64(1); sequence <= 3; sequence++ {
		_, err = stream.Write(encodeMigrationRecord(sequence, []byte(fmt.Sprintf("after-the-switch-%d", sequence))))
		require.NoError(t, err)
	}
	retireCtx, retireCancel := context.WithTimeout(ctx, migrationSettle)
	defer retireCancel()
	require.NoError(t, conn.RetireTransport(retireCtx, handshakeTransport))
	require.NoError(t, handshakeSocket.Close())
	_, err = stream.Write(encodeMigrationRecord(4, []byte("after-the-old-socket-closed")))
	require.NoError(t, err, "the local write succeeds even though nothing will acknowledge it")

	// The old assertion: one decodable ack frame. It passes, and it is the pre-switch ack.
	require.NoError(t, stream.SetReadDeadline(time.Now().Add(5*time.Second)))
	header := make([]byte, 16)
	_, err = io.ReadFull(stream, header)
	require.NoError(t, err, "the stale pre-switch acknowledgement is still readable")
	stalePayload := make([]byte, binary.BigEndian.Uint32(header[0:4]))
	_, err = io.ReadFull(stream, stalePayload)
	require.NoError(t, err)
	stale, ok := decodeMigrationRecord(header, stalePayload)
	require.True(t, ok)
	require.Equal(t, uint64(0), stale.sequence, "the frame the old assertion accepted acknowledges record 0")
	require.Equal(t, "before-the-switch", string(stale.payload))

	// The assertion this file adds: an acknowledgement OF record 4 never arrives.
	require.NoError(t, stream.SetReadDeadline(time.Now().Add(300*time.Millisecond)))
	_, err = io.ReadFull(stream, header)
	require.Error(t, err, "the endpoint acknowledged a record it never received")
	require.True(t, errors.Is(err, context.DeadlineExceeded) || isTimeout(err), "want a read timeout, got %v", err)
}

// isTimeout reports whether err is a net timeout.
func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}
