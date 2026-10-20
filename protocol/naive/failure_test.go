//go:build with_naive_outbound

package naive

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/sagernet/cronet-go"
	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// P1: the Naive outbound must turn the REAL CONNECT handshake result into
// adapter.FailureFacts, so that the group/urltest policies can tell a proxy
// rejection from a first-hop path failure. These tests pin the mapping and the
// two-stage dial.

// fakeNaiveConn is a net.Conn whose handshake outcome the test chooses. It
// exists because the failure classification must be testable without a native
// Cronet library.
type fakeNaiveConn struct {
	handshakeErr error
	closed       bool
}

func (c *fakeNaiveConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *fakeNaiveConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *fakeNaiveConn) Close() error                     { c.closed = true; return nil }
func (c *fakeNaiveConn) LocalAddr() net.Addr              { return nil }
func (c *fakeNaiveConn) RemoteAddr() net.Addr             { return nil }
func (c *fakeNaiveConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeNaiveConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeNaiveConn) SetWriteDeadline(time.Time) error { return nil }

func (c *fakeNaiveConn) Handshake() error { return c.handshakeErr }
func (c *fakeNaiveConn) HandshakeContext(context.Context) error {
	return c.handshakeErr
}

// fakeDialClient is the narrow client surface dialNaiveTunnel uses.
type fakeDialClient struct {
	conn      *fakeNaiveConn
	dialErr   error
	calls     int
	forceQUIC []bool
}

func (c *fakeDialClient) DialEarly(context.Context, M.Socksaddr) (cronet.NaiveConn, error) {
	c.calls++
	if c.dialErr != nil {
		return nil, c.dialErr
	}
	return c.conn, nil
}

// DialEarlyWithQUIC exists so the fake satisfies naiveDialerClient after the
// per-dial selector was added. The P1 tests never ask for a specific transport,
// so it records the choice and behaves like DialEarly.
func (c *fakeDialClient) DialEarlyWithQUIC(_ context.Context, _ M.Socksaddr, forceQUIC bool) (cronet.NaiveConn, error) {
	c.calls++
	c.forceQUIC = append(c.forceQUIC, forceQUIC)
	if c.dialErr != nil {
		return nil, c.dialErr
	}
	return c.conn, nil
}

func factOf(t *testing.T, err error) (adapter.FailureFacts, bool) {
	t.Helper()
	return adapter.FailureFactsOf(err)
}

// TestNaiveHandshakeFacts is the classification matrix. Every row states an
// outcome the handshake can really return, and the fact policy deliberately
// refuses to guess on the ambiguous ones.
func TestNaiveHandshakeFacts(t *testing.T) {
	testCases := []struct {
		name       string
		err        error
		wantFacts  adapter.FailureFacts
		wantKnown  bool
		wantIsPath bool
	}{
		{
			name:      "407 is a proxy authentication failure",
			err:       E.New("naive CONNECT handshake: unexpected response status: 407"),
			wantFacts: adapter.FailureFacts{Stage: adapter.FailureStageHandshake, Origin: adapter.FailureOriginProxy},
			wantKnown: true,
		},
		{
			// A non-407 status answered, but whether the target or the proxy is
			name:      "any other status stays unknown",
			err:       E.New("naive CONNECT handshake: unexpected response status: 403"),
			wantKnown: false,
		},
		{
			name:      "connection refused is a path failure",
			err:       cronet.NetErrorConnectionRefused,
			wantFacts: adapter.PathFailureFacts(adapter.FailureStageHandshake),
			wantKnown: true,
		},
		{
			name:      "address unreachable is a path failure",
			err:       cronet.NetErrorAddressUnreachable,
			wantFacts: adapter.PathFailureFacts(adapter.FailureStageHandshake),
			wantKnown: true,
		},
		{
			name:      "name resolution is local, never a member penalty",
			err:       cronet.NetErrorNameNotResolved,
			wantFacts: adapter.FailureFacts{Stage: adapter.FailureStageHandshake, Origin: adapter.FailureOriginLocal},
			wantKnown: true,
		},
		{
			name:      "certificate failure belongs to the endpoint",
			err:       cronet.NetErrorCertDateInvalid,
			wantFacts: adapter.FailureFacts{Stage: adapter.FailureStageHandshake, Origin: adapter.FailureOriginProxy},
			wantKnown: true,
		},
		{
			// The whole reason this row exists: a QUIC failure proves UDP is
			// blocked, not that the member is broken over TCP. A Path fact here
			// would demote a working node globally.
			name:      "a QUIC failure is not proof against the member",
			err:       cronet.NetErrorQUICProtocolError,
			wantKnown: false,
		},
		{
			name:      "a QUIC handshake failure is not proof either",
			err:       cronet.NetErrorQUICHandshakeFailed,
			wantKnown: false,
		},
		{
			name:      "a timeout is not proof the path is gone",
			err:       cronet.NetErrorTimedOut,
			wantKnown: false,
		},
		{
			name:      "reset means something answered",
			err:       cronet.NetErrorConnectionReset,
			wantKnown: false,
		},
		{
			name:      "EOF means something answered",
			err:       io.EOF,
			wantKnown: false,
		},
		{
			name:      "a caller cancellation is the caller's",
			err:       context.Canceled,
			wantKnown: false,
		},
		{
			name:      "this process closing the client is not a member failure",
			err:       net.ErrClosed,
			wantKnown: false,
		},
		{
			name:      "an unknown error stays unknown",
			err:       errors.New("something else"),
			wantKnown: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			facts := naiveHandshakeFailureFacts(context.Background(), testCase.err)
			require.Equal(t, testCase.wantKnown, facts.Known(),
				"known=%v for %v", facts.Known(), testCase.err)
			if testCase.wantKnown {
				require.Equal(t, testCase.wantFacts, facts)
			}
		})
	}
}

// TestNaiveHandshakeFactsCallerContextWins proves the caller's own context is
// checked first: a transport that reports something else after the caller left
// must not become evidence about the path.
func TestNaiveHandshakeFactsCallerContextWins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	facts := naiveHandshakeFailureFacts(ctx, cronet.NetErrorConnectionRefused)
	require.False(t, facts.Known(),
		"a cancelled caller must not attach a path fact, even if the transport reported a refusal")
}

// TestNaiveResponseStatusParsing pins the dependency's message shape, including
// the outer wrapper this package adds.
func TestNaiveResponseStatusParsing(t *testing.T) {
	status, ok := naiveResponseStatus(E.New("naive CONNECT handshake: unexpected response status: 407"))
	require.True(t, ok)
	require.Equal(t, 407, status)

	_, ok = naiveResponseStatus(errors.New("unexpected response status: not-a-number"))
	require.False(t, ok, "a non-numeric tail must not be guessed")

	_, ok = naiveResponseStatus(errors.New("some other failure"))
	require.False(t, ok)
}

// TestDialNaiveTunnelWaitsForTheHandshake is the heart of P1: the handshake is
// performed before the connection is returned, and its failure is facted.
func TestDialNaiveTunnelWaitsForTheHandshake(t *testing.T) {
	conn := &fakeNaiveConn{handshakeErr: E.New("unexpected response status: 407")}
	client := &fakeDialClient{conn: conn}

	got, err := dialNaiveTunnel(context.Background(), client, M.ParseSocksaddr("example.com:443"))
	require.Error(t, err)
	require.Nil(t, got)
	require.True(t, conn.closed, "a tunnel whose CONNECT was rejected must be closed")

	facts, known := factOf(t, err)
	require.True(t, known, "the 407 must reach the caller as a fact")
	require.Equal(t, adapter.FailureFacts{
		Stage:  adapter.FailureStageHandshake,
		Origin: adapter.FailureOriginProxy,
	}, facts)
	require.Contains(t, err.Error(), "407", "the status must survive in the message")
}

// TestDialNaiveTunnelReturnsAHandshakedConnection proves the success path returns
// the connection only after the handshake succeeded.
func TestDialNaiveTunnelReturnsAHandshakedConnection(t *testing.T) {
	conn := &fakeNaiveConn{}
	client := &fakeDialClient{conn: conn}

	got, err := dialNaiveTunnel(context.Background(), client, M.ParseSocksaddr("example.com:443"))
	require.NoError(t, err)
	require.Equal(t, conn, got)
	require.False(t, conn.closed)
	require.Equal(t, 1, client.calls)
}

// TestDialNaiveTunnelDialErrorIsNotFacted proves the dial stage keeps its own
// classification: no protocol happened yet, so no protocol fact may be attached.
func TestDialNaiveTunnelDialErrorIsNotFacted(t *testing.T) {
	refused := syscall.ECONNREFUSED
	client := &fakeDialClient{dialErr: refused}

	_, err := dialNaiveTunnel(context.Background(), client, M.ParseSocksaddr("example.com:443"))
	require.Error(t, err)
	require.ErrorIs(t, err, refused, "the errno chain must be preserved for the existing policy")
	_, known := factOf(t, err)
	require.False(t, known, "DialEarly failures carry no protocol fact")
}

// TestNaiveHandshakeFactKeepsTheErrorChain proves attaching facts does not
// replace errors.Is/As: the existing consumers and logs still see the cause.
func TestNaiveHandshakeFactKeepsTheErrorChain(t *testing.T) {
	conn := &fakeNaiveConn{handshakeErr: cronet.NetErrorConnectionRefused}
	client := &fakeDialClient{conn: conn}

	_, err := dialNaiveTunnel(context.Background(), client, M.ParseSocksaddr("example.com:443"))
	require.Error(t, err)
	require.ErrorIs(t, err, cronet.NetErrorConnectionRefused,
		"the fact wrapper must preserve the original error chain")
	facts, known := factOf(t, err)
	require.True(t, known)
	require.Equal(t, adapter.FailureOriginPath, facts.Origin)
	require.Equal(t, adapter.FailureStageHandshake, facts.Stage)
}
