package shadowtls

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

// These tests cover the handshake-target connection sing-shadowtls abandons when a
// handshake fails after the dial. See handshake_target_cleanup.go for the upstream
// lines and for why the existing lifecycle cannot reach it.
//
// The leak is only observable while the target is still alive: a leaked socket is
// ours and open, so the target sees no FIN. That is exactly what
// startClosureReportingTarget watches for, and it is why the test drives the
// service into the "write server handshake" error return - the one of the three
// leak paths that leaves the target connection live.

// Spanning the v3 wire shape the service validates. They are duplicated from
// sing-shadowtls (v3_constrat.go, all unexported) because a test must not reach
// into the SDK, and the arithmetic is part of what is being asserted.
const (
	testTLSHeaderSize          = 5
	testTLSRandomSize          = 32
	testTLSSessionIDSize       = 32
	testHMACSize               = 4
	testSessionIDLengthIndex   = testTLSHeaderSize + 1 + 3 + 2 + testTLSRandomSize
	testClientHelloHMACIndex   = testSessionIDLengthIndex + 1 + testTLSSessionIDSize - testHMACSize
	testHandshakeRecordType    = 22
	testClientHelloMessageType = 1
	testServerHelloMessageType = 2
)

// captureClientHello captures a real TLS 1.3 ClientHello from crypto/tls.
//
// A hand-built frame cannot be used: the service parses the record with
// extractServerName before it verifies the credential, so the fixture has to be a
// genuine ClientHello. The SDK does the same thing to real traffic.
func captureClientHello(t *testing.T) []byte {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	captured := make(chan []byte, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		header := make([]byte, testTLSHeaderSize)
		if _, readErr := io.ReadFull(conn, header); readErr != nil {
			return
		}
		body := make([]byte, binary.BigEndian.Uint16(header[3:]))
		if _, readErr := io.ReadFull(conn, body); readErr != nil {
			return
		}
		captured <- append(header, body...)
	}()

	clientConn, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	defer clientConn.Close()
	client := tls.Client(clientConn, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "decoy.test",
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
	})
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	go func() { _ = client.Handshake() }()

	select {
	case frame := <-captured:
		return frame
	case <-time.After(10 * time.Second):
		t.Fatal("could not capture a ClientHello for the fixture")
		return nil
	}
}

// signClientHello puts the v3 credential into a captured ClientHello, exactly as a
// real ShadowTLS client does: HMAC-SHA1 over the ClientHello with the session ID's
// last 4 bytes zeroed, truncated to those 4 bytes.
func signClientHello(t *testing.T, frame []byte, password string) {
	t.Helper()
	require.Greater(t, len(frame), testClientHelloHMACIndex+testHMACSize,
		"the captured ClientHello is too short to carry the v3 credential")
	require.Equal(t, byte(testHandshakeRecordType), frame[0])
	require.Equal(t, byte(testClientHelloMessageType), frame[testTLSHeaderSize])
	require.Equal(t, byte(testTLSSessionIDSize), frame[testSessionIDLengthIndex],
		"the captured ClientHello must carry a %d-byte legacy session id, which is "+
			"where the v3 credential lives; a client that did not send one cannot be "+
			"used as this fixture", testTLSSessionIDSize)

	mac := hmac.New(sha1.New, []byte(password))
	mac.Write(frame[testTLSHeaderSize:testClientHelloHMACIndex])
	mac.Write([]byte{0, 0, 0, 0})
	mac.Write(frame[testClientHelloHMACIndex+testHMACSize:])
	copy(frame[testClientHelloHMACIndex:testClientHelloHMACIndex+testHMACSize], mac.Sum(nil)[:testHMACSize])
}

// startClosureReportingTarget starts a handshake target that accepts one
// connection, answers the relayed ClientHello with a ServerHello-shaped record and
// then reports whether the service closes its side.
func startClosureReportingTarget(t *testing.T) (uint16, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	closed := make(chan struct{})
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(time.Minute))

		header := make([]byte, testTLSHeaderSize)
		if _, readErr := io.ReadFull(conn, header); readErr != nil {
			return
		}
		body := make([]byte, binary.BigEndian.Uint16(header[3:]))
		if _, readErr := io.ReadFull(conn, body); readErr != nil {
			return
		}

		// A ServerHello-shaped record: the service only has to read it as one
		// complete record and then try to write it to the client.
		const serverHelloBodySize = 64
		serverHello := make([]byte, testTLSHeaderSize+serverHelloBodySize)
		serverHello[0] = testHandshakeRecordType
		serverHello[1], serverHello[2] = 3, 3
		binary.BigEndian.PutUint16(serverHello[3:], serverHelloBodySize)
		serverHello[testTLSHeaderSize] = testServerHelloMessageType
		if _, writeErr := conn.Write(serverHello); writeErr != nil {
			return
		}

		// Block until the service closes this connection. A leaked socket produces
		// no EOF and no reset, so this read never returns.
		if _, readErr := conn.Read(make([]byte, 1)); readErr != nil {
			close(closed)
		}
	}()
	return uint16(listener.Addr().(*net.TCPAddr).Port), closed
}

// failingWriteConn is the accepted client socket with a broken write direction.
// Its reads are the real socket, so the service parses a real ClientHello and dials
// the target; only the write of the ServerHello back to the client fails, which is
// the deterministic way to reach that error return.
type failingWriteConn struct {
	net.Conn
}

func (c *failingWriteConn) Write(p []byte) (int, error) {
	return 0, errors.New("shadowtls test: the client socket is gone")
}

func (c *failingWriteConn) Upstream() any {
	return c.Conn
}

// TestShadowTLSHandshakeTargetIsClosedOnAnErrorReturn is the regression test for
// the abandoned handshake-target connection.
//
// Before the cleanup, the target below never sees its peer close: the service
// returns the error and simply drops its reference to the socket, so the descriptor
// is held until the process exits.
func TestShadowTLSHandshakeTargetIsClosedOnAnErrorReturn(t *testing.T) {
	const password = "alice-password"
	clientHello := captureClientHello(t)
	signClientHello(t, clientHello, password)

	targetPort, targetClosed := startClosureReportingTarget(t)
	// The inbound is not started: NewConnection is called directly, which is exactly
	// how the listener invokes it, and keeps this test free of accept-loop timing.
	inboundInstance := newPreAuthInbound(t, freeLoopbackPort(t), targetPort, nil)

	serverSide, clientSide := newTCPPair(t)
	defer clientSide.Close()
	_, err := clientSide.Write(clientHello)
	require.NoError(t, err)

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		// NewConnection reports nothing; it closes the client connection on failure,
		// which is how the caller observes the end of the handshake.
		inboundInstance.NewConnection(context.Background(), &failingWriteConn{Conn: serverSide}, adapter.InboundContext{}, nil)
	}()

	select {
	case <-targetClosed:
	case <-time.After(15 * time.Second):
		t.Fatal("the handshake-target connection was never closed: the service " +
			"abandoned it on the error return, so its file descriptor stays open " +
			"until the process exits")
	}

	select {
	case <-returned:
	case <-time.After(15 * time.Second):
		t.Fatal("NewConnection never returned")
	}

	// The handshake must also have failed on the client side: a failed write cannot
	// be reported as a completed handshake.
	require.NoError(t, clientSide.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = clientSide.Read(make([]byte, 1))
	require.Error(t, err, "the client connection must fail, never survive a failed handshake")
}

// TestHandshakeTargetsClosesOnlyWhatItRecorded is the unit half: the tracker closes
// what this handshake dialled and is safe to call again, so the success and fallback
// paths - where the service already closed the connection - cannot be harmed.
func TestHandshakeTargetsClosesOnlyWhatItRecorded(t *testing.T) {
	recorded, recordedPeer := newTCPPair(t)
	unrelated, unrelatedPeer := newTCPPair(t)
	defer unrelated.Close()
	defer unrelatedPeer.Close()

	targets := &handshakeTargets{}
	targets.add(recorded)
	targets.closeLeftovers()

	require.NoError(t, recordedPeer.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err := recordedPeer.Read(make([]byte, 1))
	require.Error(t, err, "the recorded connection must be closed")

	// The unrelated connection was never recorded, so it must be untouched.
	require.NoError(t, unrelatedPeer.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
	_, err = unrelatedPeer.Read(make([]byte, 1))
	var netErr net.Error
	require.ErrorAs(t, err, &netErr)
	require.True(t, netErr.Timeout(), "an unrecorded connection must not be closed by the tracker")

	require.NotPanics(t, targets.closeLeftovers, "a second cleanup must be harmless")
}
