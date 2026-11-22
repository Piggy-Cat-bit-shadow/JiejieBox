package route

// On-demand growth of the TCP sniff buffer, exercised through the router's own sniff hand-off.
//
// common/sniff/stream_buffer_growth_test.go owns the loop's properties. What is pinned HERE is that
// the router actually reaches them and hands the result on: actionSniff (route/route.go:1659, the only
// production caller), matchRule's append of the returned buffer into the connection's cached prefix,
// the server-first skip that must not allocate or read at all, and bufio.NewCachedConn - the wrapper
// routeConnection installs - delivering the complete stream in order afterwards.
//
// Every case that has a payload therefore reads it back through the same wrapper the connection
// manager uses, so "the buffer is correct" and "the connection sees the right bytes" are one
// assertion rather than two claims.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/sniff"
	C "github.com/sagernet/sing-box/constant"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// sniffGrowthConn is a net.Conn over an io.Reader, i.e. a socket: a read takes what fits and leaves
// the rest for the next one. The route tests need that property because the hand-off assertion is
// about the stream, not about one read.
type sniffGrowthConn struct {
	reader io.Reader
	reads  int
}

func (c *sniffGrowthConn) Read(p []byte) (int, error) {
	c.reads++
	return c.reader.Read(p)
}
func (c *sniffGrowthConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *sniffGrowthConn) Close() error                     { return nil }
func (c *sniffGrowthConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *sniffGrowthConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *sniffGrowthConn) SetDeadline(time.Time) error      { return nil }
func (c *sniffGrowthConn) SetReadDeadline(time.Time) error  { return nil }
func (c *sniffGrowthConn) SetWriteDeadline(time.Time) error { return nil }

// sniffGrowthHTTPRequest is a valid HTTP/1.1 request whose first payload is larger than the initial
// sniff buffer, so the router's path has to grow before the HTTP sniffer can see the whole header
// block. The Host header is at the front, which keeps the fixture independent of parser thresholds.
func sniffGrowthHTTPRequest() []byte {
	return []byte("GET /download HTTP/1.1\r\nHost: big.example\r\nCookie: " +
		strings.Repeat("k=0123456789abcdef; ", 128) + "\r\n\r\n")
}

// sniffGrowthDrain installs the wrapper routeConnection installs for the sniffed prefix and reads the
// whole stream back. The buffer is released by the wrapper once it drains, exactly as in production.
func sniffGrowthDrain(t *testing.T, buffer *buf.Buffer, conn net.Conn) []byte {
	t.Helper()
	cached := bufio.NewCachedConn(conn, buffer)
	var visible bytes.Buffer
	_, err := io.Copy(&visible, cached)
	require.NoError(t, err)
	require.NoError(t, cached.Close())
	return visible.Bytes()
}

// TestRouteSniffStartsWithTheSmallBufferAndHandsOffTheStream is the ordinary small-payload case: the
// capacity the connection holds afterwards is the initial one, not the packet buffer, and the stream
// is unchanged.
func TestRouteSniffStartsWithTheSmallBufferAndHandsOffTheStream(t *testing.T) {
	router := sniffTestRouter(nil)
	metadata := &adapter.InboundContext{
		Network:     N.NetworkTCP,
		Destination: M.ParseSocksaddrHostPort("192.0.2.10", 80),
	}
	payload := []byte("GET / HTTP/1.1\r\nHost: small.example\r\n\r\n")
	conn := &sniffGrowthConn{reader: bytes.NewReader(payload)}

	before := sniff.StreamSniffGrowthCount()
	buffer, packetBuffers, err := router.actionSniff(context.Background(), metadata, &R.RuleActionSniff{}, conn, nil, nil, nil)

	require.NoError(t, err)
	require.Empty(t, packetBuffers)
	require.NotNil(t, buffer)
	require.Equal(t, "http", metadata.Protocol)
	require.Equal(t, "small.example", metadata.Domain)
	require.Equal(t, sniff.StreamSniffInitialBufferSize, buffer.Cap(),
		"a small first payload must stay on the initial buffer")
	require.Zero(t, sniff.StreamSniffGrowthCount()-before)
	require.Equal(t, payload, sniffGrowthDrain(t, buffer, conn))
}

// TestRouteSniffGrowsForAPayloadPastTheInitialBuffer is the non-waiting half of the change: a payload
// that does not fit still gets sniffed in full, at the ceiling the old code allocated up front.
func TestRouteSniffGrowsForAPayloadPastTheInitialBuffer(t *testing.T) {
	router := sniffTestRouter(nil)
	metadata := &adapter.InboundContext{
		Network:     N.NetworkTCP,
		Destination: M.ParseSocksaddrHostPort("192.0.2.10", 80),
	}
	payload := sniffGrowthHTTPRequest()
	require.Greater(t, len(payload), sniff.StreamSniffInitialBufferSize)
	conn := &sniffGrowthConn{reader: bytes.NewReader(payload)}

	before := sniff.StreamSniffGrowthCount()
	buffer, _, err := router.actionSniff(context.Background(), metadata, &R.RuleActionSniff{}, conn, nil, nil, nil)

	require.NoError(t, err)
	require.NotNil(t, buffer)
	require.Equal(t, "big.example", metadata.Domain)
	require.EqualValues(t, 1, sniff.StreamSniffGrowthCount()-before)
	require.Equal(t, buf.UDPBufferSize, buffer.Cap())
	require.Equal(t, len(payload), buffer.Len())
	require.Equal(t, payload, buffer.Bytes())
	require.Equal(t, payload, sniffGrowthDrain(t, buffer, conn))
}

// TestRouteSniffStopsAtTheCeilingAndKeepsTheStream is the over-limit shape through the router. The
// action carries one sniffer that keeps asking for more data, which is how a real plan behaves while a
// record is still incomplete: the growth must stop at the ceiling the old code allocated, the sniff
// must end with ErrNeedMoreData as it always did, and the bytes the sniff never read must still reach
// the connection.
func TestRouteSniffStopsAtTheCeilingAndKeepsTheStream(t *testing.T) {
	router := sniffTestRouter(nil)
	metadata := &adapter.InboundContext{
		Network:     N.NetworkTCP,
		Destination: M.ParseSocksaddrHostPort("192.0.2.10", 443),
	}
	payload := bytes.Repeat([]byte{0x16}, 3*buf.UDPBufferSize)
	conn := &sniffGrowthConn{reader: bytes.NewReader(payload)}
	action := &R.RuleActionSniff{
		StreamSniffers: []sniff.StreamSniffer{
			func(_ context.Context, _ *adapter.InboundContext, _ io.Reader) error {
				return sniff.ErrNeedMoreData
			},
		},
	}

	before := sniff.StreamSniffGrowthCount()
	buffer, _, fatalErr := router.actionSniff(context.Background(), metadata, action, conn, nil, nil, nil)

	require.NoError(t, fatalErr, "a sniff that runs out of room is never a fatal connection error")
	require.ErrorIs(t, metadata.SniffError, sniff.ErrNeedMoreData,
		"the ceiling must end the sniff the way it always did, not report a verdict")
	require.EqualValues(t, 1, sniff.StreamSniffGrowthCount()-before, "one growth, then the ceiling")
	require.NotNil(t, buffer)
	require.Equal(t, buf.UDPBufferSize, buffer.Cap())
	require.Equal(t, buf.UDPBufferSize, buffer.Len(), "the sniff stopped exactly at the ceiling")
	require.Equal(t, payload[:buf.UDPBufferSize], buffer.Bytes())
	require.Equal(t, payload, sniffGrowthDrain(t, buffer, conn),
		"what the sniff never read is still the connection's to read")
}

// TestRouteMatchRuleCarriesTheGrownSniffBuffer runs the rule walk, not only the action: the buffer
// matchRule appends to the connection's cached list must be the grown one, or the bytes read after a
// growth would be missing from the stream the outbound receives.
func TestRouteMatchRuleCarriesTheGrownSniffBuffer(t *testing.T) {
	router := sniffTestRouter(&reverseMappingDNS{})
	captured := &stubRule{action: &R.RuleActionReject{Method: C.RuleActionRejectMethodDefault}}
	router.rules = []adapter.Rule{
		&stubRule{action: &R.RuleActionSniff{}},
		captured,
	}
	metadata := adapter.InboundContext{
		Network:     N.NetworkTCP,
		Destination: M.Socksaddr{Fqdn: "origin.example", Port: 80},
	}
	payload := sniffGrowthHTTPRequest()
	conn := &sniffGrowthConn{reader: bytes.NewReader(payload)}

	_, _, buffers, _, err := router.matchRule(context.Background(), &metadata, conn, nil)

	require.NoError(t, err)
	require.NotNil(t, captured.seen, "the walk must reach the rule after the sniff")
	require.Equal(t, "big.example", metadata.Domain)
	require.Len(t, buffers, 1)
	require.Equal(t, buf.UDPBufferSize, buffers[0].Cap())
	require.Equal(t, len(payload), buffers[0].Len())
	require.Equal(t, payload, buffers[0].Bytes())

	// routeConnection wraps the buffers in reverse order; there is one here, so the order is trivial
	// and the assertion is about the hand-off existing at all.
	var stream net.Conn = conn
	for _, buffer := range buffers {
		stream = bufio.NewCachedConn(stream, buffer)
	}
	var visible bytes.Buffer
	_, err = io.Copy(&visible, stream)
	require.NoError(t, err)
	require.Equal(t, payload, visible.Bytes())
	require.NoError(t, stream.Close())
}

// TestRouteSniffCachedFragmentsSurviveGrowth: inputBuffers are borrowed views of earlier rounds'
// payload. Growth must not consume, release or rewrite them, and the sniffers must still see them in
// front of the bytes read now.
func TestRouteSniffCachedFragmentsSurviveGrowth(t *testing.T) {
	router := sniffTestRouter(nil)
	metadata := &adapter.InboundContext{
		Network:     N.NetworkTCP,
		Destination: M.ParseSocksaddrHostPort("192.0.2.10", 80),
	}
	cachedPrefix := []byte("GET /cached HTTP/1.1\r\nHo")
	cachedHost := []byte("st: cached.example\r\n")
	cached := []*buf.Buffer{buf.As(cachedPrefix), buf.As(cachedHost)}
	payload := []byte("Cookie: " + strings.Repeat("v", 3000) + "\r\n\r\n")
	conn := &sniffGrowthConn{reader: bytes.NewReader(payload)}

	buffer, _, err := router.actionSniff(context.Background(), metadata, &R.RuleActionSniff{}, conn, nil, cached, nil)

	require.NoError(t, err)
	require.Equal(t, "cached.example", metadata.Domain,
		"the Host exists only in the borrowed prefix, so it must still be re-parsed")
	require.NotNil(t, buffer)
	require.Equal(t, len(payload), buffer.Len())
	require.Equal(t, payload, buffer.Bytes())
	require.Equal(t, cachedPrefix, cached[0].Bytes())
	require.Equal(t, cachedHost, cached[1].Bytes())
	require.Equal(t, 0, cached[0].Start(), "the borrowed view is not advanced")
	require.Equal(t, 0, cached[1].Start())
	require.Equal(t, payload, sniffGrowthDrain(t, buffer, conn))
}

// TestRouteSniffTimeoutReleasesTheSmallBuffer: a flow that never sends a byte must not hold the
// ceiling while it waits, and actionSniff must release what it did hold.
func TestRouteSniffTimeoutReleasesTheSmallBuffer(t *testing.T) {
	router := sniffTestRouter(nil)
	metadata := &adapter.InboundContext{
		Network:     N.NetworkTCP,
		Destination: M.ParseSocksaddrHostPort("192.0.2.10", 8080),
	}
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	before := sniff.StreamSniffGrowthCount()
	buffer, _, fatalErr := router.actionSniff(context.Background(), metadata,
		&R.RuleActionSniff{Timeout: 30 * time.Millisecond}, serverConn, nil, nil, nil)

	require.NoError(t, fatalErr, "an empty flow is not a fatal connection error")
	require.Error(t, metadata.SniffError, "the flow ended with the read timeout, not with a verdict")
	require.Nil(t, buffer, "an empty sniff buffer is released by actionSniff, never handed over")
	require.Zero(t, sniff.StreamSniffGrowthCount()-before)
}

// TestRouteSniffServerFirstSkipAllocatesNothing is the negative control: the skip happens before any
// buffer exists, so a server-first port must not read, grow or hand off.
func TestRouteSniffServerFirstSkipAllocatesNothing(t *testing.T) {
	router := sniffTestRouter(nil)
	metadata := &adapter.InboundContext{
		Network:     N.NetworkTCP,
		Destination: M.ParseSocksaddrHostPort("192.0.2.10", 25), // SMTP: server-first
	}
	conn := &sniffGrowthConn{reader: bytes.NewReader([]byte("220 mail.example ESMTP\r\n"))}

	before := sniff.StreamSniffGrowthCount()
	buffer, _, err := router.actionSniff(context.Background(), metadata, &R.RuleActionSniff{}, conn, nil, nil, nil)

	require.NoError(t, err)
	require.Nil(t, buffer)
	require.Zero(t, conn.reads, "the server-first skip must not read a byte")
	require.Zero(t, sniff.StreamSniffGrowthCount()-before)
}

// TestRouteSniffGrowthKeepsOverrideDestination: growth changes where the bytes are, never what the
// sniff decides, so the override branch must still rewrite the target.
func TestRouteSniffGrowthKeepsOverrideDestination(t *testing.T) {
	router := sniffTestRouter(nil)
	metadata := &adapter.InboundContext{
		Network:     N.NetworkTCP,
		Destination: M.Socksaddr{Fqdn: "old.example", Port: 80},
	}
	payload := sniffGrowthHTTPRequest()
	conn := &sniffGrowthConn{reader: bytes.NewReader(payload)}

	buffer, _, err := router.actionSniff(context.Background(), metadata,
		&R.RuleActionSniff{OverrideDestination: true}, conn, nil, nil, nil)

	require.NoError(t, err)
	require.NotNil(t, buffer)
	require.Equal(t, buf.UDPBufferSize, buffer.Cap(), "the fixture must actually have grown")
	require.Equal(t, "big.example", metadata.Destination.Fqdn)
	require.Equal(t, uint16(80), metadata.Destination.Port)
	buffer.Release()
}

// TestRouteSniffGrowthConcurrency drives the real action concurrently at the sizes the task requires.
// It checks three things at once: the verdict and the stream are per-connection correct, the growth
// count matches exactly the flows whose payload cannot fit the initial buffer, and no flow pays for a
// bigger buffer without needing one.
func TestRouteSniffGrowthConcurrency(t *testing.T) {
	smallHTTP := []byte("GET / HTTP/1.1\r\nHost: small.example\r\n\r\n")
	tlsHello := tlsClientHelloRecord(t, "hello.example")
	largeHTTP := sniffGrowthHTTPRequest()
	unknown := bytes.Repeat([]byte{0x00}, 3000)
	require.LessOrEqual(t, len(tlsHello), sniff.StreamSniffInitialBufferSize,
		"the TLS fixture is the no-growth control: a real ClientHello must fit the initial buffer")

	type sniffGrowthFixture struct {
		name       string
		payload    []byte
		wantDomain string
		wantGrowth bool
		// wantError marks the fixtures no sniffer claims: actionSniff still hands the bytes over and
		// still returns no FATAL error, but the sniff outcome is recorded as an error.
		wantError bool
	}
	fixtures := []sniffGrowthFixture{
		{name: "http-small", payload: smallHTTP, wantDomain: "small.example"},
		{name: "tls-hello", payload: tlsHello, wantDomain: "hello.example"},
		{name: "http-large", payload: largeHTTP, wantDomain: "big.example", wantGrowth: true},
		{name: "unknown-binary", payload: unknown, wantError: true},
	}

	router := sniffTestRouter(nil)
	for _, concurrency := range []int{1, 100, 1000} {
		t.Run("concurrency="+strconv.Itoa(concurrency), func(t *testing.T) {
			before := sniff.StreamSniffGrowthCount()
			var (
				waitGroup sync.WaitGroup
				access    sync.Mutex
				failures  []string
			)
			fail := func(format string, args ...any) {
				access.Lock()
				failures = append(failures, fmt.Sprintf(format, args...))
				access.Unlock()
			}
			expectedGrowths := uint64(0)
			for i := 0; i < concurrency; i++ {
				fixture := fixtures[i%len(fixtures)]
				if fixture.wantGrowth {
					expectedGrowths++
				}
				waitGroup.Add(1)
				go func() {
					defer waitGroup.Done()
					metadata := &adapter.InboundContext{
						Network:     N.NetworkTCP,
						Destination: M.ParseSocksaddrHostPort("192.0.2.10", 80),
					}
					conn := &sniffGrowthConn{reader: bytes.NewReader(fixture.payload)}
					buffer, _, fatalErr := router.actionSniff(context.Background(), metadata, &R.RuleActionSniff{}, conn, nil, nil, nil)
					if fatalErr != nil {
						fail("%s: a sniff outcome must not become a fatal error: %v", fixture.name, fatalErr)
						return
					}
					if fixture.wantError != (metadata.SniffError != nil) {
						fail("%s: sniff error %v, wantError=%v", fixture.name, metadata.SniffError, fixture.wantError)
					}
					if buffer == nil {
						fail("%s: no buffer handed off", fixture.name)
						return
					}
					wantCapacity := sniff.StreamSniffInitialBufferSize
					if fixture.wantGrowth {
						wantCapacity = buf.UDPBufferSize
					}
					if buffer.Cap() != wantCapacity {
						fail("%s: buffer capacity %d, want %d", fixture.name, buffer.Cap(), wantCapacity)
					}
					if metadata.Domain != fixture.wantDomain {
						fail("%s: domain %q, want %q", fixture.name, metadata.Domain, fixture.wantDomain)
					}
					cached := bufio.NewCachedConn(conn, buffer)
					var visible bytes.Buffer
					if _, copyErr := io.Copy(&visible, cached); copyErr != nil {
						fail("%s: draining the hand-off: %v", fixture.name, copyErr)
						return
					}
					if !bytes.Equal(visible.Bytes(), fixture.payload) {
						fail("%s: stream changed: got %d bytes, want %d", fixture.name, visible.Len(), len(fixture.payload))
					}
					if closeErr := cached.Close(); closeErr != nil {
						fail("%s: close: %v", fixture.name, closeErr)
					}
				}()
			}
			waitGroup.Wait()
			require.Empty(t, failures, "every concurrent flow must hold the same contract")
			require.Equal(t, expectedGrowths, sniff.StreamSniffGrowthCount()-before,
				"exactly the flows that cannot fit the initial buffer may grow, once each")
		})
	}
}
