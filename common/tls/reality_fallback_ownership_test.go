//go:build with_utls

package tls

// Stage-2 coupling test: a REALITY verification failure hands the connection to the
// camouflage fallback, and the shared TLS dialer must not close it behind the
// fallback's back.
//
// Before the handoff marker the dialer closed the socket immediately after the
// handshake error, so the fallback's HTTP/2 request raced a closed connection. The
// prober saw an abrupt close - the exact signature the camouflage exists to avoid.

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type handoffFakeConn struct {
	closed int
}

func (c *handoffFakeConn) Read([]byte) (int, error)         { return 0, net.ErrClosed }
func (c *handoffFakeConn) Write(buffer []byte) (int, error) { return len(buffer), nil }
func (c *handoffFakeConn) Close() error                     { c.closed++; return nil }
func (c *handoffFakeConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *handoffFakeConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *handoffFakeConn) SetDeadline(time.Time) error      { return nil }
func (c *handoffFakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *handoffFakeConn) SetWriteDeadline(time.Time) error { return nil }

type handoffFakeDialer struct {
	conn *handoffFakeConn
}

func (d *handoffFakeDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return d.conn, nil
}

func (d *handoffFakeDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// handoffFakeConfig reports a handshake result without any I/O.
type handoffFakeConfig struct {
	err error
}

func (c *handoffFakeConfig) ServerName() string                { return "example.com" }
func (c *handoffFakeConfig) SetServerName(string)              {}
func (c *handoffFakeConfig) NextProtos() []string              { return nil }
func (c *handoffFakeConfig) SetNextProtos([]string)            {}
func (c *handoffFakeConfig) HandshakeTimeout() time.Duration   { return 0 }
func (c *handoffFakeConfig) SetHandshakeTimeout(time.Duration) {}
func (c *handoffFakeConfig) STDConfig() (*tls.Config, error)   { return &tls.Config{}, nil }
func (c *handoffFakeConfig) Client(net.Conn) (Conn, error)     { return nil, c.err }
func (c *handoffFakeConfig) Clone() Config                     { return c }
func (c *handoffFakeConfig) ClientHandshake(context.Context, net.Conn) (Conn, error) {
	return nil, c.err
}

func TestDialerLeavesAHandedOffConnectionOpen(t *testing.T) {
	t.Parallel()
	conn := &handoffFakeConn{}
	dialer := NewDialer(&handoffFakeDialer{conn: conn}, &handoffFakeConfig{
		err: &realityFallbackError{cause: errors.New("reality verification failed")},
	})
	_, err := dialer.DialTLSContext(context.Background(), M.ParseSocksaddr("example.com:443"))
	if err == nil {
		t.Fatal("the handshake was expected to fail")
	}
	if conn.closed != 0 {
		t.Fatalf("the dialer closed a connection handed to the REALITY fallback (%d close(s))", conn.closed)
	}
}

func TestDialerClosesAnOrdinaryHandshakeFailure(t *testing.T) {
	t.Parallel()
	conn := &handoffFakeConn{}
	dialer := NewDialer(&handoffFakeDialer{conn: conn}, &handoffFakeConfig{
		err: errors.New("certificate rejected"),
	})
	_, err := dialer.DialTLSContext(context.Background(), M.ParseSocksaddr("example.com:443"))
	if err == nil {
		t.Fatal("the handshake was expected to fail")
	}
	if conn.closed != 1 {
		t.Fatalf("ordinary handshake failure closed the connection %d time(s), want exactly 1", conn.closed)
	}
}

// The handoff marker must survive wrapping: callers wrap errors with context.
func TestHandoffMarkerSurvivesWrapping(t *testing.T) {
	t.Parallel()
	err := &realityFallbackError{cause: errors.New("reality verification failed")}
	wrapped := errors.Join(errors.New("outbound: "), err)
	if !isHandedOff(wrapped) {
		t.Fatal("the handoff marker was lost when the error was wrapped")
	}
	if isHandedOff(errors.New("unrelated")) {
		t.Fatal("an unrelated error was treated as a handoff")
	}
	var _ N.Dialer = (*handoffFakeDialer)(nil)
}
