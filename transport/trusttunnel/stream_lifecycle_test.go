package trusttunnel

// P11 at the product entry: the stream's lifetime is the pool's Active/Idle evidence.
//
// A CONNECT stream can end in three ways that the caller does not control: the server finishes the
// response, the server drops the stream mid-transfer, or the caller closes a tunnel that is silent
// and never read. Each of them must end in the pool's books - the stream's slot must come back, the
// session must stay usable, and a healthy stream sharing the carrier must not be disturbed. Those
// are the properties the previous audit left as "the response body half of the pool is untested",
// and they are checked here against the real service, not a fake.
//
// The slot accounting itself (`streams`) is asserted directly: it is the same counter the trim, the
// reuse boundary and the residency ledger read, so a slot that is not returned makes the session
// unkillable and is a leak even when every byte arrived.

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// startOneShotServer answers every accepted connection with payload and then closes its side, which
// is the half-close shape: the tunnel's response ends while the caller's write half is still open.
func startOneShotServer(t *testing.T, payload string) M.Socksaddr {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = conn.Write([]byte(payload))
			}()
		}
	}()
	return M.SocksaddrFromNet(listener.Addr())
}

// requireEcho writes payload and reads exactly the echo back. It deliberately does not set a
// deadline: the H2 stream reports os.ErrInvalid for one (the tunnel's lifetime is the stream's, not a
// deadline's), and every endpoint here is loopback.
func requireEcho(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	_, err := conn.Write([]byte(payload))
	require.NoError(t, err)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err)
	require.Equal(t, payload, string(echo))
}

// TestClosingAStalledStreamKeepsTheSessionAndItsSibling is the first shape: a tunnel the caller never
// reads is closed. Close must return, the server must release its side, the carrier must survive, and
// the slot must come back.
func TestClosingAStalledStreamKeepsTheSessionAndItsSibling(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			server := startTestServer(t, protocol)
			echo := startEchoServer(t)
			dialer := &testDialer{}
			client := server.newClient(t, protocol, testPassword, dialer, nil)
			ctx := testContext(t)

			stalled, err := client.DialContext(ctx, holdDestination())
			require.NoError(t, err)
			// hold.test is accepted and drained by the server: a silent tunnel that is open.
			_, err = stalled.Write([]byte("into-the-void"))
			require.NoError(t, err)

			healthy, err := client.DialContext(ctx, echo)
			require.NoError(t, err)
			requireEcho(t, healthy, "sibling-before")

			closed := make(chan struct{})
			go func() {
				_ = stalled.Close()
				close(closed)
			}()
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Fatal("Close of a stalled CONNECT did not return")
			}

			// The server saw its side of that stream end.
			require.Eventually(t, func() bool { return server.handler.holding.Load() == 0 },
				5*time.Second, time.Millisecond,
				"the server never released the CONNECT that was closed on the client")

			// The carrier is the same one and still carries: closing a stalled stream must not make
			// the pool dial again, and must not disturb the sibling.
			requireEcho(t, healthy, "sibling-after")
			require.EqualValues(t, 1, dialer.dials.Load(),
				"closing a stalled stream made the pool open a second session")
			third, err := client.DialContext(ctx, echo)
			require.NoError(t, err)
			requireEcho(t, third, "third-flow")

			require.NoError(t, healthy.Close())
			require.NoError(t, third.Close())

			client.access.Lock()
			session := client.session
			client.access.Unlock()
			require.NotNil(t, session)
			require.Eventually(t, func() bool { return session.streams.Load() == 0 },
				5*time.Second, time.Millisecond,
				"a closed stream left its slot reserved, so the session can never be trimmed")
			client.CloseIdleConnections()
			require.Eventually(t, func() bool { return !session.conn.alive() },
				5*time.Second, time.Millisecond,
				"the idle session was not released after its last stream closed")
		})
	}
}

// TestServerEndedStreamLeavesTheSessionUsable is the half-close shape: the target answers and closes,
// so the response body reaches EOF while the caller's write half is still open. A write after that
// EOF must not block the caller, and the session must still serve a new CONNECT.
func TestServerEndedStreamLeavesTheSessionUsable(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			server := startTestServer(t, protocol)
			echo := startEchoServer(t)
			target := startOneShotServer(t, "bye")
			dialer := &testDialer{}
			client := server.newClient(t, protocol, testPassword, dialer, nil)
			ctx := testContext(t)

			ending, err := client.DialContext(ctx, target)
			require.NoError(t, err)
			received := make([]byte, len("bye"))
			_, err = io.ReadFull(ending, received)
			require.NoError(t, err)
			require.Equal(t, "bye", string(received))
			_, err = ending.Read(received)
			require.True(t, errors.Is(err, io.EOF), "the ended response did not report EOF: %v", err)

			// The write half is still open on this side; the write must resolve (accept it or report
			// the reset) instead of blocking the caller forever.
			writeDone := make(chan error, 1)
			go func() {
				_, writeErr := ending.Write([]byte("after-eof"))
				writeDone <- writeErr
			}()
			select {
			case <-writeDone:
			case <-time.After(5 * time.Second):
				t.Fatal("a write after the server ended the response blocked forever")
			}
			require.NoError(t, ending.Close())

			// The carrier survives and still carries a new flow.
			next, err := client.DialContext(ctx, echo)
			require.NoError(t, err)
			requireEcho(t, next, "after-the-ended-stream")
			require.EqualValues(t, 1, dialer.dials.Load(),
				"a stream the server ended made the pool open a second session")
			require.NoError(t, next.Close())

			client.access.Lock()
			session := client.session
			client.access.Unlock()
			require.NotNil(t, session)
			require.Eventually(t, func() bool { return session.streams.Load() == 0 },
				5*time.Second, time.Millisecond,
				"the ended stream left its slot reserved")
		})
	}
}

// TestTrimCannotTouchALiveStream is the trim contract at the body level: CloseIdleConnections must
// leave a session that carries a stream alone, the stream must keep working across the trim, and the
// same trim must be able to close the session once that stream is gone.
func TestTrimCannotTouchALiveStream(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			server := startTestServer(t, protocol)
			echo := startEchoServer(t)
			dialer := &testDialer{}
			client := server.newClient(t, protocol, testPassword, dialer, nil)
			ctx := testContext(t)

			live, err := client.DialContext(ctx, echo)
			require.NoError(t, err)
			requireEcho(t, live, "before-the-trim")

			client.access.Lock()
			session := client.session
			client.access.Unlock()
			require.NotNil(t, session)

			client.CloseIdleConnections()
			require.True(t, session.conn.alive(),
				"the trim closed a session that was carrying a stream")
			requireEcho(t, live, "after-the-trim")
			require.EqualValues(t, 1, dialer.dials.Load(), "the trim made the pool dial again")

			require.NoError(t, live.Close())
			require.Eventually(t, func() bool { return session.streams.Load() == 0 },
				5*time.Second, time.Millisecond, "the closed stream left its slot reserved")
			client.CloseIdleConnections()
			require.Eventually(t, func() bool { return !session.conn.alive() },
				5*time.Second, time.Millisecond,
				"the trim could not close the session after its last stream ended")
		})
	}
}
