package route

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// nxdomainLogRecorder records the two levels this test distinguishes. The rest of the context
// logger is the no-op production factory, so only the lines the connection manager actually emits
// for a failed setup can appear.
type nxdomainLogRecorder struct {
	log.ContextLogger
	access sync.Mutex
	errors []string
	debugs []string
}

func newNXDomainLogRecorder() *nxdomainLogRecorder {
	return &nxdomainLogRecorder{ContextLogger: log.NewNOPFactory().Logger()}
}

func (l *nxdomainLogRecorder) ErrorContext(ctx context.Context, args ...any) {
	l.access.Lock()
	l.errors = append(l.errors, fmt.Sprint(args...))
	l.access.Unlock()
}

func (l *nxdomainLogRecorder) DebugContext(ctx context.Context, args ...any) {
	l.access.Lock()
	l.debugs = append(l.debugs, fmt.Sprint(args...))
	l.access.Unlock()
}

func (l *nxdomainLogRecorder) snapshot() (errors []string, debugs []string) {
	l.access.Lock()
	defer l.access.Unlock()
	return append([]string(nil), l.errors...), append([]string(nil), l.debugs...)
}

type nxdomainFailingDialer struct {
	err error
}

func (d nxdomainFailingDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, d.err
}

func (d nxdomainFailingDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, d.err
}

// TestConnectionNXDomainLoggingIsDebug pins the connection-layer half of the fix. The DNS lookup
// already returns the NXDOMAIN error unchanged; this layer used to log every failed setup at ERROR,
// which is where the third `open connection to ...: resolve ...: NXDOMAIN` line came from.
func TestConnectionNXDomainLoggingIsDebug(t *testing.T) {
	nxdomain := E.Cause(dns.RcodeError(dns.RcodeNameError), "resolve nx.example")
	for _, testCase := range []struct {
		name      string
		err       error
		wantError bool
	}{
		{name: "wrapped NXDOMAIN", err: nxdomain},
		{name: "SERVFAIL", err: dns.RcodeError(dns.RcodeServerFailure), wantError: true},
		{name: "timeout", err: context.DeadlineExceeded, wantError: true},
		{name: "network unreachable", err: errors.New("network unreachable"), wantError: true},
		{
			name:      "NXDOMAIN mixed with a real failure",
			err:       E.Errors(nxdomain, context.DeadlineExceeded),
			wantError: true,
		},
		{name: "plain text is not a DNS verdict", err: errors.New("NXDOMAIN"), wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := newNXDomainLogRecorder()
			manager := NewConnectionManager(recorder)
			client, peer := net.Pipe()
			t.Cleanup(func() {
				_ = client.Close()
				_ = peer.Close()
			})

			manager.NewConnection(context.Background(), nxdomainFailingDialer{err: testCase.err}, peer,
				adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("nx.example", 443)}, nil)

			errors, debugs := recorder.snapshot()
			if testCase.wantError {
				require.Len(t, errors, 1, "a real failure must keep its ERROR line; got %v", errors)
				require.Contains(t, strings.Join(errors, "\n"), "open connection to nx.example:443")
				return
			}
			require.Empty(t, errors, "a normal NXDOMAIN must not be logged at ERROR; got %v", errors)
			require.Contains(t, strings.Join(debugs, "\n"), "open connection to nx.example:443",
				"the failed setup must still be visible at DEBUG; got %v", debugs)
			require.Contains(t, strings.Join(debugs, "\n"), "NXDOMAIN")
		})
	}
}
