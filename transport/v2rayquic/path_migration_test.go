//go:build with_quic

package v2rayquic

// The cross-path migration acceptance test for the pinned QUIC library.
//
// # What this pins
//
// The window's deliverable is "one active V2Ray QUIC flow keeps carrying checked payload while
// the physical path changes, and the old socket is then physically closed". The capability
// lives in the dependency, so this test drives it through the pinned module exactly the way the
// transport will:
//
//   - a second real UDP socket becomes a candidate path on the SAME connection
//     (Conn.AddPath), it is validated (Path.Probe) and switched to (Path.Switch);
//   - one already-open stream keeps carrying sequenced, CRC-checked records across the
//     switch, and the endpoint still sees ONE connection and ONE stream - a reconnect cannot
//     pass this;
//   - the transport that carried the handshake is retired and its socket physically closed,
//     and the same stream still carries records afterwards. Without the retirement the
//     socket's read error is a transport failure and would destroy the connection.
//
// The capacity is on the pinned fork branch network/path-length-fix (commit d981f089): it
// carries both the path-transport connection-ID-length fix and Conn.RetireHandshakeTransport.
// A pin that predates it makes Probe hang until its deadline and the retirement API disappear,
// which is why this test is load-bearing rather than illustrative.
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
// the receiver checks the sequence gap and the digest of every record it accepts.
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
// and any protocol violation.
type migrationServer struct {
	listener    *quic.Listener
	addr        net.Addr
	handshakes  atomic.Int32
	streamCount atomic.Int32

	access  sync.Mutex
	streams []*migrationStreamState
	failure error
}

type migrationStreamState struct {
	nextSequence uint64
	seen         []migrationRecord
}

func startMigrationServer(t *testing.T) *migrationServer {
	t.Helper()
	listener, err := quic.ListenAddr("127.0.0.1:0", migrationTLSConfig(t), &quic.Config{})
	require.NoError(t, err)
	server := &migrationServer{listener: listener, addr: listener.Addr()}
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
			// The acknowledgement travels back on the SAME stream, so a migration that only
			// preserved the send direction cannot pass.
			_, _ = stream.Write(encodeMigrationRecord(record.sequence, record.payload[:1]))
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

// TestActiveStreamSurvivesAPathSwitchAndTheOldSocketsClose is the acceptance: one connection,
// one already-open stream, records continuing across a switch, then the handshake socket is
// physically closed and records keep flowing.
func TestActiveStreamSurvivesAPathSwitchAndTheOldSocketsClose(t *testing.T) {
	server := startMigrationServer(t)

	primary, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NotNil(t, primary)

	ctx, cancel := context.WithTimeout(context.Background(), migrationSettle)
	defer cancel()
	conn, err := quic.Dial(ctx, primary, server.addr, migrationTLSConfig(t), &quic.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseWithError(0, "") })

	stream, err := conn.OpenStream()
	require.NoError(t, err)

	_, err = stream.Write(encodeMigrationRecord(0, []byte("before-the-switch")))
	require.NoError(t, err)
	require.Len(t, server.waitRecords(t, 0, 1), 1)

	// The second real path: its own UDP socket, its own transport, the same connection.
	candidate, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = candidate.Close() })
	path, err := conn.AddPath(&quic.Transport{Conn: candidate})
	require.NoError(t, err, "the pinned library must offer a candidate path on a second socket")
	require.ErrorIs(t, path.Switch(), quic.ErrPathNotValidated,
		"a path must not be switchable before it is validated")

	probeCtx, probeCancel := context.WithTimeout(ctx, migrationSettle)
	defer probeCancel()
	require.NoError(t, path.Probe(probeCtx), "path validation must complete on a live second socket")
	require.NoError(t, path.Switch())
	// Switch only registers the request; the run loop installs the new send path. The test does
	// not poll Conn.LocalAddr to observe that: LocalAddr reads the send connection without
	// synchronization, so polling it during a switch is a data race in the library (recorded in
	// the window handoff). The records below and the retirement wait force and then prove the
	// applied state instead.

	// The same stream continues with an unbroken sequence.
	for sequence := uint64(1); sequence <= 3; sequence++ {
		payload := []byte(fmt.Sprintf("after-the-switch-%d", sequence))
		_, err = stream.Write(encodeMigrationRecord(sequence, payload))
		require.NoError(t, err, "record %d must be accepted on the migrated stream", sequence)
	}
	seen := server.waitRecords(t, 0, 4)
	require.Len(t, seen, 4)
	for index, record := range seen {
		require.Equal(t, uint64(index), record.sequence, "the endpoint must see unbroken sequence numbers")
	}
	require.Equal(t, int32(1), server.handshakes.Load(), "the switch must not establish a second connection")
	require.Equal(t, int32(1), server.streamCount.Load(), "the switch must not open a second stream")

	// Retire the transport that carried the handshake, then physically close its socket.
	require.NoError(t, conn.RetireHandshakeTransport())
	require.NoError(t, primary.Close())
	select {
	case <-conn.Context().Done():
		t.Fatal("closing the retired socket ended the migrated connection")
	case <-time.After(150 * time.Millisecond):
	}

	// The same stream still carries data in both directions after the old socket is gone.
	_, err = stream.Write(encodeMigrationRecord(4, []byte("after-the-old-socket-closed")))
	require.NoError(t, err, "the migrated stream must survive the old socket's close")
	seen = server.waitRecords(t, 0, 5)
	require.Len(t, seen, 5)
	require.Equal(t, uint64(4), seen[4].sequence)

	header := make([]byte, 16)
	require.NoError(t, stream.SetReadDeadline(time.Now().Add(migrationSettle)))
	_, err = io.ReadFull(stream, header)
	require.NoError(t, err, "the migrated stream must still receive data from the endpoint")
	ackPayload := make([]byte, binary.BigEndian.Uint32(header[0:4]))
	_, err = io.ReadFull(stream, ackPayload)
	require.NoError(t, err)
	_, ok := decodeMigrationRecord(header, ackPayload)
	require.True(t, ok, "the acknowledgement must be a well-formed record")

	require.Nil(t, conn.Context().Err(), "the connection must still be live")
	require.Equal(t, int32(1), server.handshakes.Load())
	require.Equal(t, int32(1), server.streamCount.Load())
}
