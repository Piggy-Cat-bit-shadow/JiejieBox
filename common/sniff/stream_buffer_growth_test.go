package sniff_test

// On-demand growth of the TCP sniff buffer, driven through the real peek loop.
//
// The router-side hand-off is covered in route/sniff_buffer_growth_test.go; this file owns the
// properties the loop must hold on its own: no growth when the payload fits, one growth when it does
// not, the ceiling unchanged, every byte kept and in order, and the error classification untouched.
//
// Byte equivalence is asserted on the RELAY-VISIBLE stream - the returned buffer followed by what is
// left in the connection - not on the buffer alone. That distinction is the whole ownership argument:
// the sniffer may stop earlier than it used to (it now stops after the payload fits the small
// buffer), and the bytes it did not read stay in the socket for the connection to read next. A test
// that compared buffer lengths would call that a truncation; a test that compares the stream proves
// it is not.

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/sniff"
	"github.com/sagernet/sing/common/buf"

	"github.com/stretchr/testify/require"
)

// streamConn is a net.Conn over an io.Reader, i.e. a socket: a read takes what fits and leaves the
// rest for the next one. scriptedConn (sniff_helpers_test.go) deliberately drops the remainder of a
// chunk, which is fine for its read-count assertions and wrong for a byte-equivalence assertion.
type streamConn struct {
	reader    io.Reader
	deadlines []time.Time
	closeErr  error
}

func (c *streamConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c *streamConn) Write(p []byte) (int, error) {
	return len(p), nil
}
func (c *streamConn) Close() error { return c.closeErr }
func (c *streamConn) LocalAddr() net.Addr {
	return scriptedAddr{}
}
func (c *streamConn) RemoteAddr() net.Addr {
	return scriptedAddr{}
}
func (c *streamConn) SetDeadline(time.Time) error      { return nil }
func (c *streamConn) SetWriteDeadline(time.Time) error { return nil }

func (c *streamConn) SetReadDeadline(deadline time.Time) error {
	c.deadlines = append(c.deadlines, deadline)
	return nil
}

// oneByteReader delivers one byte per Read, the worst case for a read loop that also grows.
type oneByteReader struct {
	reader io.Reader
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return r.reader.Read(p[:1])
}

// terminalErrorReader reproduces the failure shapes a socket produces after data: EOF, a timeout,
// a cancellation or a reset.
type terminalErrorReader struct {
	err error
}

func (r terminalErrorReader) Read([]byte) (int, error) { return 0, r.err }

// waitingSniffer asks for more data unconditionally, which is how the loop is driven to the buffer
// ceiling without depending on a parser's internal thresholds.
func waitingSniffer(_ context.Context, _ *adapter.InboundContext, _ io.Reader) error {
	return sniff.ErrNeedMoreData
}

// largeHTTPRequest is a real HTTP/1.1 request whose first payload is larger than the initial sniff
// buffer and small enough that the completed header block still arrives within one growth.
func largeHTTPRequest() []byte {
	return []byte("GET /download HTTP/1.1\r\nHost: big.example\r\nCookie: " +
		strings.Repeat("k=0123456789abcdef; ", 128) + "\r\n\r\n")
}

type growthRun struct {
	metadata *adapter.InboundContext
	buffer   *buf.Buffer
	err      error
	conn     *streamConn
	growths  uint64
}

// runGrowth drives PeekStreamGrow the way route/route.go does: a small initial buffer, the ceiling
// passed in, and the returned buffer treated as the owner.
func runGrowth(t *testing.T, reader io.Reader, cached []*buf.Buffer, initialSize, limit int, sniffers ...sniff.StreamSniffer) growthRun {
	t.Helper()
	conn := &streamConn{reader: reader}
	metadata := &adapter.InboundContext{}
	initial := buf.NewSize(initialSize)
	before := sniff.StreamSniffGrowthCount()
	buffer, err := sniff.PeekStreamGrow(context.Background(), metadata, conn, cached, initial, limit, 0, sniffers...)
	t.Cleanup(func() {
		if buffer != nil {
			buffer.Release()
		}
	})
	return growthRun{
		metadata: metadata,
		buffer:   buffer,
		err:      err,
		conn:     conn,
		growths:  sniff.StreamSniffGrowthCount() - before,
	}
}

// relayVisible is the byte stream the connection sees after the hand-off: the buffer's payload first,
// then whatever the sniff did not read. route/route.go installs exactly this order with
// bufio.NewCachedConn.
func relayVisible(t *testing.T, run growthRun) []byte {
	t.Helper()
	var visible bytes.Buffer
	if run.buffer != nil {
		visible.Write(run.buffer.Bytes())
	}
	rest, err := io.ReadAll(run.conn)
	require.NoError(t, err)
	visible.Write(rest)
	return visible.Bytes()
}

// TestStreamSniffBufferIsSmallAndCoversARealClientHello pins the two numbers the design rests on: the
// initial capacity is below the ceiling, and a real modern ClientHello still fits without growing.
func TestStreamSniffBufferIsSmallAndCoversARealClientHello(t *testing.T) {
	require.Equal(t, 2*1024, sniff.StreamSniffInitialBufferSize,
		"the report argues for 2 KiB from the measured payload sizes; changing it must move the report")
	require.Less(t, sniff.StreamSniffInitialBufferSize, buf.UDPBufferSize,
		"the initial buffer must be smaller than the ceiling, or there is nothing to save")

	hello := captureClientHello(t, &tls.Config{ServerName: "www.example.com"})
	require.LessOrEqual(t, len(hello), sniff.StreamSniffInitialBufferSize,
		"a real Go 1.27 ClientHello was measured at 1513 B; a toolchain change that pushes it past the "+
			"initial buffer makes the chosen value wrong and the measurement must be repeated")

	initial := sniff.NewStreamSniffBuffer()
	defer initial.Release()
	require.Equal(t, sniff.StreamSniffInitialBufferSize, initial.Cap())

	run := runGrowth(t, bytes.NewReader(hello), nil, initial.Cap(), buf.UDPBufferSize, sniff.DefaultStreamSniffers...)
	require.NoError(t, run.err)
	require.Equal(t, "www.example.com", run.metadata.Domain, "the fixture must actually be sniffed")
	require.Zero(t, run.growths, "a real ClientHello must not grow the buffer")
	require.Equal(t, sniff.StreamSniffInitialBufferSize, run.buffer.Cap())
	require.Equal(t, len(hello), run.buffer.Len())
	require.Equal(t, hello, run.buffer.Bytes())
	require.Equal(t, hello, relayVisible(t, run))
}

// TestStreamSniffBufferGrowsOnlyWhenThePayloadDoesNotFit is the positive case: one growth, the
// ceiling as the new capacity, and every byte still in order.
func TestStreamSniffBufferGrowsOnlyWhenThePayloadDoesNotFit(t *testing.T) {
	payload := largeHTTPRequest()
	require.Greater(t, len(payload), sniff.StreamSniffInitialBufferSize)

	run := runGrowth(t, bytes.NewReader(payload), nil, sniff.StreamSniffInitialBufferSize, buf.UDPBufferSize, sniff.DefaultStreamSniffers...)
	require.NoError(t, run.err)
	require.Equal(t, "http", run.metadata.Protocol)
	require.Equal(t, "big.example", run.metadata.Domain)
	require.EqualValues(t, 1, run.growths, "exactly one growth, not one per round")
	require.Equal(t, buf.UDPBufferSize, run.buffer.Cap(), "a growth lands on the ceiling the old code allocated")
	require.Equal(t, len(payload), run.buffer.Len())
	require.Equal(t, payload, run.buffer.Bytes())
	require.Equal(t, payload, relayVisible(t, run))
}

// largeClientHello captures a real ClientHello pushed past the initial buffer by a long ALPN list, so
// a delivery that crosses the growth point mid-record is still a record a real client can send.
//
// It is used instead of the HTTP request for the one-byte case because a read boundary inside an HTTP
// request line is a pre-existing weakness of the HTTP plan (http_method_boundary_test.go pins it: the
// request is lost at such a boundary), and a test of growth must not turn that separate fact into a
// growth failure.
func largeClientHello(t *testing.T) []byte {
	t.Helper()
	protocols := make([]string, 0, 24)
	for i := 0; i < 24; i++ {
		protocols = append(protocols, fmt.Sprintf("proto-%02d-%s", i, strings.Repeat("x", 30)))
	}
	hello := captureClientHello(t, &tls.Config{ServerName: "www.example.com", NextProtos: protocols})
	require.Greater(t, len(hello), sniff.StreamSniffInitialBufferSize)
	return hello
}

// TestStreamSniffBufferGrowsAcrossOneByteSegments is the fragmentation case: one byte per read until
// the initial buffer is full, a growth mid-record, and the same verdict and bytes afterwards.
func TestStreamSniffBufferGrowsAcrossOneByteSegments(t *testing.T) {
	hello := largeClientHello(t)
	run := runGrowth(t, &oneByteReader{reader: bytes.NewReader(hello)}, nil,
		sniff.StreamSniffInitialBufferSize, buf.UDPBufferSize, sniff.DefaultStreamSniffers...)

	require.NoError(t, run.err)
	require.Equal(t, "www.example.com", run.metadata.Domain)
	require.EqualValues(t, 1, run.growths)
	require.Equal(t, buf.UDPBufferSize, run.buffer.Cap())
	require.Equal(t, hello, run.buffer.Bytes())
	require.Equal(t, hello, relayVisible(t, run))
}

// TestStreamSniffBufferCachedFragmentsKeepTheirOwnership: the cached buffers a previous round left
// behind are borrowed views. Growth must copy only the NEW bytes, leave the cached buffers untouched
// and still let the sniffers see the cached payload in front of the new one.
func TestStreamSniffBufferCachedFragmentsKeepTheirOwnership(t *testing.T) {
	cachedPrefix := []byte("GET /cached HTTP/1.1\r\nHo")
	cachedHost := []byte("st: cached.example\r\n")
	cached := []*buf.Buffer{buf.As(cachedPrefix), buf.As(cachedHost)}
	payload := []byte("Cookie: " + strings.Repeat("v", 3000) + "\r\n\r\n")

	run := runGrowth(t, bytes.NewReader(payload), cached,
		sniff.StreamSniffInitialBufferSize, buf.UDPBufferSize, sniff.DefaultStreamSniffers...)

	require.NoError(t, run.err)
	require.Equal(t, "cached.example", run.metadata.Domain,
		"the Host only exists in the cached prefix, so the sniffers must have re-parsed it")
	require.EqualValues(t, 1, run.growths)
	require.Equal(t, len(payload), run.buffer.Len(), "the new buffer holds only the bytes it read")
	require.Equal(t, payload, run.buffer.Bytes())
	require.Equal(t, cachedPrefix, cached[0].Bytes(), "the borrowed view is never consumed")
	require.Equal(t, cachedHost, cached[1].Bytes())
	require.Equal(t, 0, cached[0].Start())
	require.Equal(t, 0, cached[1].Start())

	var full []byte
	for _, cachedBuffer := range cached {
		full = append(full, cachedBuffer.Bytes()...)
	}
	full = append(full, payload...)
	require.Equal(t, full, append(mustConcat(cached), relayVisible(t, run)...))
}

func mustConcat(buffers []*buf.Buffer) []byte {
	var out []byte
	for _, buffer := range buffers {
		out = append(out, buffer.Bytes()...)
	}
	return out
}

// TestStreamSniffBufferStopsAtTheCeiling is the ceiling contract: growth stops where the old
// allocation stopped, the sniff ends with the bytes it has, and the unread bytes stay in the socket.
func TestStreamSniffBufferStopsAtTheCeiling(t *testing.T) {
	const limit = 4 * 1024

	t.Run("past-the-ceiling", func(t *testing.T) {
		payload := bytes.Repeat([]byte{0x16}, 3*limit)

		run := runGrowth(t, bytes.NewReader(payload), nil, sniff.StreamSniffInitialBufferSize, limit, waitingSniffer)

		require.ErrorIs(t, run.err, sniff.ErrNeedMoreData,
			"a payload that fills the ceiling ends the sniff exactly as before")
		require.EqualValues(t, 1, run.growths, "one growth, then the ceiling refuses")
		require.Equal(t, limit, run.buffer.Cap())
		require.Equal(t, limit, run.buffer.Len())
		require.Equal(t, payload[:limit], run.buffer.Bytes())
		require.Equal(t, payload, relayVisible(t, run), "the bytes the sniff never read are still in the stream")
	})

	t.Run("exactly-at-the-ceiling", func(t *testing.T) {
		payload := bytes.Repeat([]byte{0x16}, limit)

		run := runGrowth(t, bytes.NewReader(payload), nil, sniff.StreamSniffInitialBufferSize, limit, waitingSniffer)

		require.ErrorIs(t, run.err, sniff.ErrNeedMoreData,
			"an undecided sniff that runs out of room reports need-more-data, not a verdict")
		require.EqualValues(t, 1, run.growths)
		require.Equal(t, limit, run.buffer.Cap(), "the payload fits the ceiling exactly, so it stops there")
		require.Equal(t, payload, run.buffer.Bytes())
	})
}

// TestStreamSniffBufferMultiRecordTLSKeepsTheWholeStream is the multi-record shape: the TLS sniffer
// claims the flow on the FIRST record, so the loop stops inside the second one and must still leave
// the whole stream readable.
func TestStreamSniffBufferMultiRecordTLSKeepsTheWholeStream(t *testing.T) {
	hello := captureClientHello(t, &tls.Config{ServerName: "www.example.com"})
	payload := append(append([]byte(nil), hello...), hello...)
	require.Greater(t, len(payload), sniff.StreamSniffInitialBufferSize)

	run := runGrowth(t, bytes.NewReader(payload), nil, sniff.StreamSniffInitialBufferSize, buf.UDPBufferSize, sniff.DefaultStreamSniffers...)

	require.NoError(t, run.err)
	require.Equal(t, "www.example.com", run.metadata.Domain)
	require.Zero(t, run.growths,
		"the verdict arrives on the first record, which fits: no sniffer asked for more")
	require.Equal(t, sniff.StreamSniffInitialBufferSize, run.buffer.Cap())
	require.Equal(t, payload, relayVisible(t, run))
}

// TestStreamSniffBufferGrowthThenEOFKeepsTheExactPayload is the "growth, then nothing more" case: the
// next read reports EOF, and that must end as a sniff error rather than as a success, with the whole
// payload handed over.
func TestStreamSniffBufferGrowthThenEOFKeepsTheExactPayload(t *testing.T) {
	payload := bytes.Repeat([]byte{0x16}, sniff.StreamSniffInitialBufferSize)

	run := runGrowth(t, bytes.NewReader(payload), nil, sniff.StreamSniffInitialBufferSize, buf.UDPBufferSize, waitingSniffer)

	require.ErrorIs(t, run.err, sniff.ErrNeedMoreData)
	require.EqualValues(t, 1, run.growths)
	require.Equal(t, len(payload), run.buffer.Len())
	require.Equal(t, payload, run.buffer.Bytes())
}

// TestStreamSniffBufferGrowthDoesNotFakeSuccessOnReadFailure drives each terminal read error after a
// growth. Every case must hand over every byte READ and report an error: a grown buffer that swallowed
// a read failure would let the router relay a stream it believes was sniffed.
func TestStreamSniffBufferGrowthDoesNotFakeSuccessOnReadFailure(t *testing.T) {
	payload := bytes.Repeat([]byte{0x16}, 5000)
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{"eof", io.EOF},
		{"timeout", timeoutError{}},
		{"cancel", context.Canceled},
		{"reset", net.ErrClosed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			reader := io.MultiReader(bytes.NewReader(payload), terminalErrorReader{err: testCase.err})
			run := runGrowth(t, reader, nil, sniff.StreamSniffInitialBufferSize, buf.UDPBufferSize, waitingSniffer)

			require.Error(t, run.err, "a read failure is never reported as a sniff verdict")
			require.EqualValues(t, 1, run.growths)
			require.Equal(t, len(payload), run.buffer.Len(), "growth must not give up bytes already read")
			require.Equal(t, payload, run.buffer.Bytes())
		})
	}
}

// TestStreamSniffBufferNoGrowthForAFirstReadFailure is the empty-flow shape the small buffer exists
// for: nothing arrives, nothing is allocated beyond the initial buffer, and the caller releases it.
func TestStreamSniffBufferNoGrowthForAFirstReadFailure(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{"timeout", timeoutError{}},
		{"eof", io.EOF},
		{"cancel", context.Canceled},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			run := runGrowth(t, terminalErrorReader{err: testCase.err}, nil,
				sniff.StreamSniffInitialBufferSize, buf.UDPBufferSize, sniff.DefaultStreamSniffers...)

			require.Error(t, run.err)
			require.Zero(t, run.growths)
			require.NotNil(t, run.buffer, "the caller still owns the initial buffer and releases it")
			require.Equal(t, sniff.StreamSniffInitialBufferSize, run.buffer.Cap())
			require.Zero(t, run.buffer.Len())
			require.True(t, run.buffer.IsEmpty())
		})
	}
}

// TestStreamSniffBufferGrowthOnlyWhenAskedForMore is the negative control for the growth trigger: a
// payload that fills the small buffer but gives every sniffer a DEFINITIVE verdict must not pay for a
// bigger buffer, because no round ever asked for one.
func TestStreamSniffBufferGrowthOnlyWhenAskedForMore(t *testing.T) {
	// 0x00 as the first byte is rejected definitively by every default stream sniffer.
	payload := bytes.Repeat([]byte{0x00}, 5000)

	run := runGrowth(t, bytes.NewReader(payload), nil, sniff.StreamSniffInitialBufferSize, buf.UDPBufferSize, sniff.DefaultStreamSniffers...)

	require.Error(t, run.err)
	require.False(t, errors.Is(run.err, sniff.ErrNeedMoreData),
		"the fixture must be a definitive verdict, not a needs-more-data one")
	require.Zero(t, run.growths, "no sniffer wanted more bytes, so there was no reason to grow")
	require.Equal(t, sniff.StreamSniffInitialBufferSize, run.buffer.Cap())
	require.Equal(t, payload[:sniff.StreamSniffInitialBufferSize], run.buffer.Bytes())
	require.Equal(t, payload, relayVisible(t, run),
		"and the 3 KiB the sniff did not read is still the connection's to read")
}

// TestFixedCeilingPeekStreamStillHasItsOwnCeiling pins the compatibility wrapper: PeekStream keeps the
// capacity it is handed (there is no growth limit to reach), and PeekStreamGrow with a limit equal to
// the initial capacity behaves exactly like the old fixed-ceiling path.
func TestFixedCeilingPeekStreamStillHasItsOwnCeiling(t *testing.T) {
	payload := largeHTTPRequest()

	before := sniff.StreamSniffGrowthCount()
	fixed := buf.NewPacket()
	defer fixed.Release()
	conn := &streamConn{reader: bytes.NewReader(payload)}
	var metadata adapter.InboundContext
	require.NoError(t, sniff.PeekStream(context.Background(), &metadata, conn, nil, fixed, 0, sniff.DefaultStreamSniffers...))
	require.Equal(t, buf.UDPBufferSize, fixed.Cap(), "PeekStream never changes the capacity it is given")
	require.Equal(t, len(payload), fixed.Len())
	require.Zero(t, sniff.StreamSniffGrowthCount()-before, "and never grows")

	// The same payload with a growth limit equal to the initial capacity: the ceiling is 2 KiB, so the
	// sniff ends where the old code would have ended had it allocated 2 KiB.
	fixedCeiling := runGrowth(t, bytes.NewReader(payload), nil,
		sniff.StreamSniffInitialBufferSize, sniff.StreamSniffInitialBufferSize, sniff.DefaultStreamSniffers...)
	require.ErrorIs(t, fixedCeiling.err, sniff.ErrNeedMoreData)
	require.Zero(t, fixedCeiling.growths)
	require.Equal(t, sniff.StreamSniffInitialBufferSize, fixedCeiling.buffer.Cap())
	require.Equal(t, payload[:sniff.StreamSniffInitialBufferSize], fixedCeiling.buffer.Bytes())
	require.Equal(t, payload, relayVisible(t, fixedCeiling))
}

// TestGrowthKeepsProtocolDomainAndRelayStream is the byte-equivalence matrix: the fixed-ceiling path
// and the growing path must agree on protocol, domain, error CLASS and the stream the relay sees,
// for every payload shape the sniffers actually meet.
func TestGrowthKeepsProtocolDomainAndRelayStream(t *testing.T) {
	hello := captureClientHello(t, &tls.Config{ServerName: "www.example.com"})
	fixtures := []struct {
		name    string
		payload []byte
	}{
		{"tls-client-hello", hello},
		{"tls-client-hello-one-byte-reads", hello},
		{"tls-two-records", append(append([]byte(nil), hello...), hello...)},
		{"http-minimal", []byte("GET / HTTP/1.1\r\nHost: small.example\r\n\r\n")},
		{"http-large-cookies", largeHTTPRequest()},
		{"ssh-banner-then-payload", append([]byte("SSH-2.0-OpenSSH_9.6\r\n"), bytes.Repeat([]byte{0x41}, 3000)...)},
		{"unknown-binary", bytes.Repeat([]byte{0x00}, 3000)},
		{"dns-over-tcp", append([]byte{0x00, 0x1e}, mustHex("740701000001000000000000012a06676f6f676c6503636f6d0000010001")...)},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			reader := func() io.Reader {
				if strings.HasSuffix(fixture.name, "one-byte-reads") {
					return &oneByteReader{reader: bytes.NewReader(fixture.payload)}
				}
				return bytes.NewReader(fixture.payload)
			}

			// The path before this change: the whole packet buffer, no growth.
			oldConn := &streamConn{reader: reader()}
			oldMetadata := &adapter.InboundContext{}
			oldBuffer := buf.NewPacket()
			oldErr := sniff.PeekStream(context.Background(), oldMetadata, oldConn, nil, oldBuffer, 0, sniff.DefaultStreamSniffers...)
			oldVisible := append(append([]byte(nil), oldBuffer.Bytes()...), mustReadAll(t, oldConn)...)
			oldBuffer.Release()

			// The path after it.
			run := runGrowth(t, reader(), nil, sniff.StreamSniffInitialBufferSize, buf.UDPBufferSize, sniff.DefaultStreamSniffers...)
			newVisible := relayVisible(t, run)

			require.Equal(t, oldMetadata.Protocol, run.metadata.Protocol)
			require.Equal(t, oldMetadata.Domain, run.metadata.Domain)
			require.Equal(t, oldMetadata.Client, run.metadata.Client)
			require.Equal(t, errors.Is(oldErr, sniff.ErrNeedMoreData), errors.Is(run.err, sniff.ErrNeedMoreData),
				"the observable error class must not change")
			require.Equal(t, fixture.payload, oldVisible, "the old path must see the whole stream")
			require.Equal(t, fixture.payload, newVisible, "and the new path must see exactly the same stream")
			require.Equal(t, oldVisible, newVisible)
		})
	}
}

// TestGrowthMayStopReadingEarlierWithoutLosingBytes is the ownership statement in one assertion pair:
// the buffer CONTENTS may differ from the fixed path (the sniff stops as soon as it has a verdict),
// while the stream the connection reads must not.
func TestGrowthMayStopReadingEarlierWithoutLosingBytes(t *testing.T) {
	payload := append([]byte("SSH-2.0-OpenSSH_9.6\r\n"), bytes.Repeat([]byte{0x41}, 3000)...)

	oldConn := &streamConn{reader: bytes.NewReader(payload)}
	oldMetadata := &adapter.InboundContext{}
	oldBuffer := buf.NewPacket()
	require.NoError(t, sniff.PeekStream(context.Background(), oldMetadata, oldConn, nil, oldBuffer, 0, sniff.DefaultStreamSniffers...))
	defer oldBuffer.Release()

	run := runGrowth(t, bytes.NewReader(payload), nil, sniff.StreamSniffInitialBufferSize, buf.UDPBufferSize, sniff.DefaultStreamSniffers...)
	require.NoError(t, run.err)

	require.Greater(t, oldBuffer.Len(), run.buffer.Len(),
		"the fixed path read the whole 3 KiB because it had a 16 KiB buffer; the growing path stopped at "+
			"the banner it recognised. That difference is real and is why comparing buffers alone would "+
			"misread this change as a truncation")
	require.Equal(t, payload, relayVisible(t, run))
}

func mustReadAll(t *testing.T, conn *streamConn) []byte {
	t.Helper()
	rest, err := io.ReadAll(conn)
	require.NoError(t, err)
	return rest
}
