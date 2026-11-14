package trusttunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

// recordingContextLogger captures the two levels these tests assert on. It embeds
// the NOP context logger so every other method keeps working.
type recordingContextLogger struct {
	logger.ContextLogger
	access sync.Mutex
	warns  []string
	debugs []string
}

func newRecordingContextLogger() *recordingContextLogger {
	return &recordingContextLogger{ContextLogger: log.NewNOPFactory().Logger()}
}

func (r *recordingContextLogger) WarnContext(_ context.Context, args ...any) {
	r.access.Lock()
	defer r.access.Unlock()
	r.warns = append(r.warns, fmt.Sprint(args...))
}

func (r *recordingContextLogger) DebugContext(_ context.Context, args ...any) {
	r.access.Lock()
	defer r.access.Unlock()
	r.debugs = append(r.debugs, fmt.Sprint(args...))
}

func (r *recordingContextLogger) warnMessages() []string {
	r.access.Lock()
	defer r.access.Unlock()
	return append([]string(nil), r.warns...)
}

func (r *recordingContextLogger) debugMessages() []string {
	r.access.Lock()
	defer r.access.Unlock()
	return append([]string(nil), r.debugs...)
}

// TestFallbackLogsTheRealTransportAndWarnsOnce covers the two log defects of the
// fallback path:
//
//   - the session that was opened is HTTP/2, but the line used to print the
//     configured protocol ("h3 session opened"), which is precisely what made the
//     original incident look like HTTP/3 was working;
//   - the first failure of a cooldown window is a statement about the node and is
//     now a warn, while repeats inside the same window stay at debug.
func TestFallbackLogsTheRealTransportAndWarnsOnce(t *testing.T) {
	h3Failure := errors.New("server negotiated ALPN \"h2\", expected h3")
	client, clock := newFallbackTestClient(t,
		func(context.Context) (sessionConn, error) { return nil, h3Failure },
		func(context.Context) (sessionConn, error) { return newFakeSession(), nil },
	)
	recorder := newRecordingContextLogger()
	client.logger = recorder

	require.NoError(t, dialFallback(t, context.Background(), client))

	warns := recorder.warnMessages()
	require.Len(t, warns, 1, "the first failure of a window must be a warn")
	require.Contains(t, warns[0], "HTTP/3 attempt")
	require.Contains(t, warns[0], "expected h3")

	debugs := strings.Join(recorder.debugMessages(), "\n")
	require.Contains(t, debugs, "trusttunnel: h2 session to",
		"the opened-session line must name the transport that was actually opened")
	require.NotContains(t, debugs, "trusttunnel: h3 session to",
		"a fallback session must never be reported as an h3 session")

	// A second demand inside the cooldown reuses the HTTP/2 session and must not
	// repeat the warn: one event is one decision.
	require.NoError(t, dialFallback(t, context.Background(), client))
	require.Len(t, recorder.warnMessages(), 1, "a repeat inside the window must stay at debug")

	// Once the window expires, a new failure is new evidence and warns again.
	clock.Add(int64(2 * time.Second))
	require.NoError(t, dialFallback(t, context.Background(), client))
	require.Len(t, recorder.warnMessages(), 2, "a new window must warn again")
}

// TestServeTCPWarnsWhenALPNIsNotH2 covers the server half of the same asymmetry:
// the QUIC side already warned on a non-h3 ALPN, while the TCP side logged the
// same permanent configuration failure at debug.
func TestServeTCPWarnsWhenALPNIsNotH2(t *testing.T) {
	recorder := newRecordingContextLogger()
	service, err := NewService(ServiceOptions{
		Logger:  recorder,
		Handler: &testHandler{},
		Users:   []auth.User{{Username: testUsername, Password: testPassword}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Close() })

	serverTLS, clientTLS := newTestTLS(t)
	// ServeTCP prepends h2, so the server list becomes ["h2","http/1.1"]. A client
	// that offers only http/1.1 negotiates it and reaches the ALPN check.
	serverTLS.config.NextProtos = []string{"http/1.1"}
	clientTLS.config.NextProtos = []string{"http/1.1"}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = service.ServeTCP(listener, serverTLS) }()

	raw, err := net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
	require.NoError(t, err)
	defer raw.Close()
	tlsConn := tls.Client(raw, clientTLS.config)
	require.NoError(t, tlsConn.Handshake())
	require.Equal(t, "http/1.1", tlsConn.ConnectionState().NegotiatedProtocol)

	require.Eventually(t, func() bool {
		for _, message := range recorder.warnMessages() {
			if strings.Contains(message, "is not h2") {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond,
		"the server must warn when a TCP connection negotiates a non-h2 ALPN")
}
