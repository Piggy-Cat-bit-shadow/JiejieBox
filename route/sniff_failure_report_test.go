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

	// A payload NO stream sniffer claims, with none of them asking for more bytes.
	//
	// This fixture used to be "PROPFIND /unsniffable HTTP/1.1\r\n", chosen because it read as an
	// unsniffable request. Measured: that payload drives the HTTP gate to `ErrNeedMoreData`, so the error
	// PeekStream returns is the aggregate "(tls: ... | need more data | unexpected EOF | invalid argument)".
	// The test therefore pinned the "needs more data" state while its own comment claimed the "inspected and
	// found nothing" state - it passed, and it proved the wrong thing.
	//
	// A short binary prefix makes every default stream sniffer reject definitively
	// ("first record does not look like a TLS handshake", "'\x00' at offset 0 is not a token octet"),
	// which is the state this test is named for. The needs-more-data state has its own case below.
	conn := &oneShotConn{payload: []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}}

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
	require.NotContains(t, failureLine, "need more data",
		"this case is the DEFINITIVE outcome; a needs-more-data payload is a different state and has its "+
			"own message")

	// The cause must travel with it, so the reader knows why.
	require.Greater(t, len(failureLine), len("sniff found no protocol for this flow: "),
		"the report must carry the sniffers' own reason, not just the fact of failure")
	require.Empty(t, metadata.Domain, "the fixture must not have produced a domain")
}

// TestASniffThatRanOutOfDataIsReportedAsSuch is the state the previous fixture was accidentally in.
//
// It is a distinct state and now has a distinct message, because the two mean different things to whoever
// reads the log: a payload too short to decide is a normal protocol shape (a long preface, a fragmented
// ClientHello), while an unrecognised payload means the sniffer set does not cover this traffic. One
// message for both made the log unable to answer the question it exists for.
func TestASniffThatRanOutOfDataIsReportedAsSuch(t *testing.T) {
	recorder := newSniffLogRecorder()
	router := &Router{logger: recorder}

	metadata := adapter.InboundContext{
		Destination: M.ParseSocksaddrHostPort("2409:8c54:1050:44::3", 8080),
	}
	action := &rule.RuleActionSniff{}

	// Measured to leave the HTTP gate on ErrNeedMoreData: a syntactically valid request line whose headers
	// never arrive.
	conn := &oneShotConn{payload: []byte("PROPFIND /truncated HTTP/1.1\r\n")}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, _ = router.actionSniff(ctx, &metadata, action, conn, nil, nil, nil)

	var reported string
	for _, line := range recorder.recorded() {
		if strings.Contains(line, "sniff") {
			reported += line + "\n"
		}
	}
	require.Contains(t, reported, "sniff needed more data than the flow provided",
		"a sniff that ran out of bytes must say so, distinctly from one that found nothing")
	require.NotContains(t, reported, "sniff found no protocol",
		"and the two states must not share a message")
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

// TestAStreamSniffReportsTheClientString is the parity fix for the fourth state.
//
// A sniffer can identify a protocol, name no domain, and still know something worth logging: an SSH banner
// sets `Client` and never `Domain`. The packet arm printed the client string; the stream arm had no branch
// for "no domain but a client", so it logged only the protocol and dropped it. Measured: the payload
// `SSH-2.0-OpenSSH_8.9` sets Protocol="ssh", Client="OpenSSH_8.9", Domain="", and the log said only
// `sniffed protocol: ssh` - the same sniff outcome reported two ways depending on the transport.
func TestAStreamSniffReportsTheClientString(t *testing.T) {
	recorder := newSniffLogRecorder()
	router := &Router{logger: recorder}

	metadata := adapter.InboundContext{
		Destination: M.ParseSocksaddrHostPort("2409:8c54:1050:44::3", 22),
	}
	action := &rule.RuleActionSniff{}

	conn := &oneShotConn{payload: []byte("SSH-2.0-OpenSSH_8.9\r\n")}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, _ = router.actionSniff(ctx, &metadata, action, conn, nil, nil, nil)

	require.Equal(t, "ssh", metadata.Protocol, "the fixture must actually be sniffed")
	require.Equal(t, "OpenSSH_8.9", metadata.Client, "and it must carry a client string")
	require.Empty(t, metadata.Domain, "with no domain, which is what makes this the fourth state")

	require.Contains(t, strings.Join(recorder.recorded(), "\n"), "client: OpenSSH_8.9",
		"the client string is part of what the sniffer learned, so the stream arm must log it exactly as "+
			"the packet arm does")
}
