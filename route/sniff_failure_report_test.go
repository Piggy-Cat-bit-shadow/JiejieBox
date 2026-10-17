package route

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/route/rule"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// A sniffer that inspected a flow and found nothing must say so.
//
// # The production evidence
//
// Fourteen WeChat flows in the log connect to a global IPv6 literal and end with
//
//	ERROR connection: open connection to [2409:...]:80 using outbound/direct[direct]:
//	      local network has no usable ipv6 path ...
//
// Every one of them shows `match[0] => sniff` first, and then either a bare `sniffed protocol: http`
// (parsed, no Host) or - for the TLS ones - NO sniff line at all. Nothing distinguishes "the sniffer
// looked and found nothing" from "the sniffer never ran", because the failure branch logged nothing.
//
// That distinction is the whole point of this round: with no domain evidence, recovery has nothing to
// work with and the literal's family refusal is the only possible outcome. An operator reading the log
// could not tell whether that was the machine's fault or a missed sniff.

// sniffLogRecorder captures DebugContext lines; everything else delegates to a real no-op logger.
type sniffLogRecorder struct {
	log.ContextLogger
	access sync.Mutex
	lines  []string
}

func newSniffLogRecorder() *sniffLogRecorder {
	return &sniffLogRecorder{ContextLogger: log.NewNOPFactory().Logger()}
}

func (l *sniffLogRecorder) DebugContext(ctx context.Context, args ...any) {
	l.access.Lock()
	defer l.access.Unlock()
	l.lines = append(l.lines, fmt.Sprint(args...))
}

func (l *sniffLogRecorder) recorded() []string {
	l.access.Lock()
	defer l.access.Unlock()
	return append([]string(nil), l.lines...)
}

// oneShotConn serves a payload once and then reports EOF, which is what makes PeekStream end with a
// verdict instead of reading forever.
//
// The payload is chosen so that no default stream sniffer claims it: not a TLS ClientHello, not an HTTP
// request line, not a DNS query, not a BitTorrent handshake. PeekStream therefore returns the aggregate of
// the sniffers' own refusals, which is exactly the "inspected, found nothing" outcome.
type oneShotConn struct {
	payload  []byte
	consumed bool
}

func (c *oneShotConn) Read(p []byte) (int, error) {
	if c.consumed {
		return 0, io.EOF
	}
	c.consumed = true
	n := copy(p, c.payload)
	return n, nil
}

func (c *oneShotConn) Write(p []byte) (int, error)        { return len(p), nil }
func (c *oneShotConn) Close() error                       { return nil }
func (c *oneShotConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c *oneShotConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (c *oneShotConn) SetDeadline(t time.Time) error      { return nil }
func (c *oneShotConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *oneShotConn) SetWriteDeadline(t time.Time) error { return nil }

// TestAFailedSniffIsReported is the regression.
func TestAFailedSniffIsReported(t *testing.T) {
	recorder := newSniffLogRecorder()
	router := &Router{logger: recorder}

	metadata := adapter.InboundContext{
		Destination: M.ParseSocksaddrHostPort("2409:8c54:1050:44::3", 80),
	}
	action := &rule.RuleActionSniff{}

	// A payload no stream sniffer claims.
	conn := &oneShotConn{payload: []byte("PROPFIND /unsniffable HTTP/1.1\r\n")}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, _ = router.actionSniff(ctx, &metadata, action, conn, nil, nil, nil)

	var failureLine string
	for _, line := range recorder.recorded() {
		if strings.Contains(line, "sniff found no protocol") {
			failureLine = line
			break
		}
	}
	require.NotEmpty(t, failureLine,
		"a flow the sniffers inspected and did not recognise must be reported: without this line the "+
			"operator cannot tell a missed sniff from a flow that had no domain evidence to begin with")

	// The cause must travel with it, so the reader knows why.
	require.Greater(t, len(failureLine), len("sniff found no protocol for this flow: "),
		"the report must carry the sniffers' own reason, not just the fact of failure")
	require.Empty(t, metadata.Domain, "the fixture must not have produced a domain")
}

// TestASuccessfulSniffIsStillReportedOnce is the control: the success branch keeps its own message and does
// not start emitting the failure one, so the two outcomes stay distinguishable.
func TestASuccessfulSniffIsStillReportedOnce(t *testing.T) {
	recorder := newSniffLogRecorder()
	router := &Router{logger: recorder}

	metadata := adapter.InboundContext{
		Destination: M.ParseSocksaddrHostPort("2409:8c54:1050:44::3", 80),
	}
	action := &rule.RuleActionSniff{}

	// A minimal HTTP/1.1 request with a Host header: the HTTP sniffer claims it and sets Domain.
	conn := &oneShotConn{payload: []byte("GET / HTTP/1.1\r\nHost: szminorshort.weixin.qq.com\r\n\r\n")}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, _ = router.actionSniff(ctx, &metadata, action, conn, nil, nil, nil)

	require.Equal(t, "szminorshort.weixin.qq.com", metadata.Domain,
		"the fixture must actually be sniffed, or the control proves nothing")

	recorded := recorder.recorded()
	require.Contains(t, strings.Join(recorded, "\n"), "sniffed protocol: http",
		"a successful sniff keeps its own report")
	for _, line := range recorded {
		require.NotContains(t, line, "sniff found no protocol",
			"a successful sniff must not also report a failure")
	}
}
