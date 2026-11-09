//go:build with_quic

package v2rayquic

// The measured capability boundary of the pinned QUIC library for this window.
//
// # What the window needs
//
// One active V2Ray QUIC stream must keep carrying checked payload while the physical path
// changes, and the old socket must then be physically closed. The pinned fork exports
// `Conn.AddPath` / `Path.Probe` / `Path.Switch` for client-initiated migration, so the first
// question is whether that API can complete a probe at all before any production code is
// written around it.
//
// It cannot, on this pin, and this file records the exact failure rather than a belief:
//
//   - `AddPath` on a second real UDP socket succeeds and refuses `Switch` until validated;
//   - `Probe` never validates: the probe packet reaches the server (the server answers with
//     its own PATH_CHALLENGE for the new path), but no PATH_RESPONSE is ever processed by the
//     client, so the path stays unvalidated until the deadline;
//   - the connection is NOT damaged by the attempt: the already-open stream keeps carrying
//     records on the original path, the endpoint sees one connection and one stream, and the
//     abandoned candidate closes cleanly.
//
// The consequence for this window is the fallback the task already names: migration is not
// available on this pin, so the supported route is the bounded reconnect that
// protocol/{vless,vmess,trojan}.Outbound.InterfaceUpdated already drives through
// `Reset` -> new dial (pinned end-to-end by
// protocol/vless/outbound_quic_transition_test.go:TestNetworkTransitionKeepsTheOutboundDialable).
// Enabling real migration needs the dependency, not this package; see the window handoff for
// the exact fork requirements.
//
// # Why a failing behaviour is asserted
//
// This is a characterization test: it pins what the dependency does today so the boundary
// cannot move silently. When a fork pin (or an upstream change) makes the probe complete,
// this test fails by design - that failure is the signal to replace it with the positive form
// (same connection, same stream, records continuing across the switch). The assertions are
// all observable outcomes, never internals.

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

// migrationServer is one real QUIC endpoint that records, per stream, the records it
// accepted and any protocol violation.
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

func (s *migrationServer) failureError() error {
	s.access.Lock()
	defer s.access.Unlock()
	return s.failure
}

// TestCandidatePathProbeDoesNotCompleteOnThisPin records the boundary described in the file
// header: the candidate path can be created, is correctly unusable before validation, never
// validates, and leaves the connection untouched when abandoned.
func TestCandidatePathProbeDoesNotCompleteOnThisPin(t *testing.T) {
	server := startMigrationServer(t)

	primary, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = primary.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), migrationSettle)
	defer cancel()
	conn, err := quic.Dial(ctx, primary, server.addr, migrationTLSConfig(t), &quic.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseWithError(0, "") })

	stream, err := conn.OpenStream()
	require.NoError(t, err)
	_, err = stream.Write(encodeMigrationRecord(0, []byte("before-the-attempt")))
	require.NoError(t, err)
	require.Len(t, server.waitRecords(t, 0, 1), 1)

	// A second, real path: its own UDP socket and its own transport, same connection.
	candidate, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = candidate.Close() })
	path, err := conn.AddPath(&quic.Transport{Conn: candidate})
	require.NoError(t, err, "the pinned library must accept a candidate path on a second socket")
	require.ErrorIs(t, path.Switch(), quic.ErrPathNotValidated,
		"a candidate must not be switchable before validation")

	// The measurement: validation does not complete inside a bounded window.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer probeCancel()
	probeErr := path.Probe(probeCtx)
	require.Error(t, probeErr, "path validation completed: the boundary moved - replace this test with the "+
		"positive form (same connection and stream carrying records across the switch)")
	require.ErrorIs(t, probeErr, context.DeadlineExceeded)

	// Abandoning the candidate is safe and bounded: the path closes, and the connection and
	// its already-open stream are exactly where they were.
	require.NoError(t, path.Close())
	_, err = stream.Write(encodeMigrationRecord(1, []byte("after-the-attempt")))
	require.NoError(t, err, "a failed candidate must not damage the connection's current path")

	seen := server.waitRecords(t, 0, 2)
	require.Len(t, seen, 2)
	require.Equal(t, uint64(1), seen[1].sequence, "the same stream must continue its sequence")
	require.Equal(t, int32(1), server.handshakes.Load(),
		"the attempt must not establish a second QUIC connection")
	require.Equal(t, int32(1), server.streamCount.Load(),
		"the attempt must not open a second stream")
	require.NoError(t, server.failureError())

	// The local path is still the original socket: nothing switched.
	require.Equal(t, primary.LocalAddr().String(), conn.LocalAddr().String())

	// Abandoning is idempotent: a second close is a no-op, not a double-teardown.
	require.NoError(t, path.Close())
}
