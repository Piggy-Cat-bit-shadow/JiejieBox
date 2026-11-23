package mieru

// The wrapper the relay sees for a Mieru INBOUND connection must let the copy layer reach the
// session's CloseWrite.
//
// trackedConn forwards Upstream() so the copy layer "can still find the real writer for capability
// checks", but sing unwraps a writer only when the wrapper ALSO declares WriterReplaceable(). Without
// that method N.UnwrapWriter stopped at this wrapper, route/conn.go's
// `N.UnwrapWriter(destinationWriter).(N.WriteCloser)` failed, and a relay whose destination finished
// writing first closed the whole session instead of ending only the write direction - so the peer's
// still-running upload was cut. These tests pin the wire between the two halves: the unwrap reaches a
// CloseWrite that is called exactly once, while Close keeps going through the registry wrapper.

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// writeEndSession stands in for the SDK session: it is a net.Conn whose CloseWrite is observable.
// Using a double here is deliberate - the point of the test is the WRAPPER contract, and the real
// session's CloseWrite is covered by the fork's own end-to-end tests.
type writeEndSession struct {
	closed      atomic.Int32
	closeWrites atomic.Int32
}

func (s *writeEndSession) Read(_ []byte) (int, error)  { return 0, nil }
func (s *writeEndSession) Write(b []byte) (int, error) { return len(b), nil }
func (s *writeEndSession) Close() error                { s.closed.Add(1); return nil }
func (s *writeEndSession) LocalAddr() net.Addr         { return &net.TCPAddr{} }
func (s *writeEndSession) RemoteAddr() net.Addr        { return &net.TCPAddr{} }
func (s *writeEndSession) SetDeadline(time.Time) error      { return nil }
func (s *writeEndSession) SetReadDeadline(time.Time) error  { return nil }
func (s *writeEndSession) SetWriteDeadline(time.Time) error { return nil }
func (s *writeEndSession) CloseWrite() error                { s.closeWrites.Add(1); return nil }

// trackedConnFor builds the wrapper exactly as the inbound does, with a live registry.
func trackedConnFor(session net.Conn) (*trackedConn, *connRegistry) {
	registry := &connRegistry{conns: make(map[net.Conn]struct{})}
	wrapped := &trackedConn{Conn: session, registry: registry}
	require.True(nil, registry.add(wrapped))
	return wrapped, registry
}

// TestTheInboundWrapperUnwrapsToTheSessionsCloseWrite is the regression: the relay's exact expression
// must find a WriteCloser through the wrapper.
func TestTheInboundWrapperUnwrapsToTheSessionsCloseWrite(t *testing.T) {
	session := &writeEndSession{}
	wrapped, _ := trackedConnFor(session)

	writer, _ := N.UnwrapCountWriter(wrapped, nil)
	writeCloser, isWriteCloser := N.UnwrapWriter(writer).(N.WriteCloser)
	require.True(t, isWriteCloser,
		"without WriterReplaceable the relay cannot see the session's CloseWrite and full-closes instead")

	require.NoError(t, writeCloser.CloseWrite())
	require.EqualValues(t, 1, session.closeWrites.Load(), "the session's own write-direction end is used")
	require.Zero(t, session.closed.Load(), "ending the write direction must not close the session")

	// The second half of the same contract: round the wrapper, the same call reaches the session once,
	// and Close still goes through the wrapper so the registry stops tracking it.
	require.NoError(t, N.CloseWrite(wrapped))
	require.EqualValues(t, 2, session.closeWrites.Load())
	require.NoError(t, wrapped.Close())
	require.EqualValues(t, 1, session.closed.Load(), "Close still closes the underlying connection")
	require.Empty(t, wrapped.registry.conns, "Close must still remove the flow from the registry")
}

// TestTheInboundWrapperKeepsCloseBookkeepingWithoutAWriteCloser keeps the other shape honest: a
// connection that offers no write-direction end must not be turned into one by the wrapper.
func TestTheInboundWrapperKeepsCloseBookkeepingWithoutAWriteCloser(t *testing.T) {
	plain, peer := net.Pipe()
	defer peer.Close()
	wrapped, _ := trackedConnFor(plain)

	_, isWriteCloser := N.UnwrapWriter(wrapped).(N.WriteCloser)
	require.False(t, isWriteCloser, "the wrapper must not invent a capability the connection does not have")
	require.NoError(t, N.CloseWrite(wrapped), "a missing capability is not an error for the generic helper")

	require.NoError(t, wrapped.Close())
	require.Empty(t, wrapped.registry.conns)
}
