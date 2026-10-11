package sniff_test

// The HTTP cases the task's coverage list names and the existing suite does not assert end to end:
// a POST and a custom token method have to be *recognised* (Protocol and Domain), not merely
// classified the same way the parser classifies them; the CR/LF boundary has to be survivable when
// the two bytes land in different segments; and the six payloads that are not HTTP have to be
// refused with a verdict rather than a request for more bytes.
//
// The gate-level equivalence over the accept corpus (http_gate_internal_test.go) pins that the gate
// does not change what the parser decides, but it never asserts what this sniffer *reports* for a
// POST: it compares the sniffer against the same parser call, so a POST that reached the wrong
// domain would agree with itself. That is the gap these cases close.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/sniff"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/buf"

	"github.com/stretchr/testify/require"
)

// httpSplitReader hands back one preset piece per Read, so a test can place a read boundary at an
// exact byte offset - which is what "CR at the end of one read" means.
type httpSplitReader struct {
	pieces [][]byte
	index  int
	reads  int
}

func (r *httpSplitReader) Read(p []byte) (int, error) {
	r.reads++
	if r.index >= len(r.pieces) {
		return 0, io.EOF
	}
	n := copy(p, r.pieces[r.index])
	r.index++
	return n, nil
}

// TestHTTPHostRecognisesEveryMethodShape is the direct statement of the method list: the method is a
// token, not a member of a whitelist, so plain GET, POST and an extension method have to reach the
// same verdict with the same domain. The domains differ per case so that a sniffer which reused the
// previous case's answer would fail rather than pass.
func TestHTTPHostRecognisesEveryMethodShape(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name    string
		request string
		domain  string
	}{
		{"get", "GET / HTTP/1.1\r\nHost: get.example.com\r\nAccept: */*\r\n\r\n", "get.example.com"},
		{"post", "POST /submit HTTP/1.1\r\nHost: post.example.com\r\nContent-Length: 0\r\n\r\n", "post.example.com"},
		{"propfind", "PROPFIND /dav/ HTTP/1.1\r\nHost: propfind.example.com\r\nDepth: 1\r\n\r\n", "propfind.example.com"},
		{"mkcol", "MKCOL /a HTTP/1.1\r\nHost: mkcol.example.com\r\n\r\n", "mkcol.example.com"},
		{"patch", "PATCH /r HTTP/1.1\r\nHost: patch.example.com\r\n\r\n", "patch.example.com"},
		{"frobnicate", "FROBNICATE /thing HTTP/1.1\r\nHost: frobnicate.example.com\r\n\r\n", "frobnicate.example.com"},
		{"lowercase-get", "get / HTTP/1.1\r\nHost: lower.example.com\r\n\r\n", "lower.example.com"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			// Directly, in one read.
			var metadata adapter.InboundContext
			require.NoError(t, sniff.HTTPHost(context.Background(), &metadata, strings.NewReader(testCase.request)))
			require.Equal(t, C.ProtocolHTTP, metadata.Protocol, "protocol for %q", testCase.request)
			require.Equal(t, testCase.domain, metadata.Domain, "domain for %q", testCase.request)

			// And through the production stream plan, which is what the router runs.
			planMetadata, planErr, _ := peekStreamChunks(t, [][]byte{[]byte(testCase.request)}, nil, nil)
			require.NoError(t, planErr, "plan for %q", testCase.request)
			require.Equal(t, C.ProtocolHTTP, planMetadata.Protocol)
			require.Equal(t, testCase.domain, planMetadata.Domain)
		})
	}
}

// TestHTTPHostIsNotClaimedByNonHTTPPayloads is the false-positive half, stated on the exported
// sniffer with the header that makes each payload look the way it does on the wire: a DNS-over-TCP
// length prefix, an SSH banner, an RDP TPKT header, a BitTorrent handshake, a TLS record and an
// all-zero segment. Every one of them has to come back with a definite verdict - not ErrNeedMoreData,
// which would keep PeekStream reading forever, and not a Protocol, which would attribute the flow to
// HTTP.
func TestHTTPHostIsNotClaimedByNonHTTPPayloads(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name    string
		payload []byte
	}{
		{"dns-over-tcp", mustHex("001e740701000001000000000000012a06676f6f676c6503636f6d0000010001")},
		{"ssh", []byte("SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13\r\n")},
		{"rdp", []byte{0x03, 0x00, 0x00, 0x13, 0x0e, 0xe0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x08, 0x00, 0x03, 0x00, 0x00, 0x00}},
		{"bittorrent", append([]byte{19}, []byte("BitTorrent protocol")...)},
		{"tls-record", mustHex("160301002e0100002a")},
		{"zero-bytes", make([]byte, 64)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var metadata adapter.InboundContext
			err := sniff.HTTPHost(context.Background(), &metadata, bytes.NewReader(testCase.payload))
			require.Error(t, err, "payload %x was accepted as HTTP", testCase.payload)
			require.False(t, errors.Is(err, sniff.ErrNeedMoreData),
				"payload %x asked for more data instead of deciding", testCase.payload)
			require.Empty(t, metadata.Protocol)
			require.Empty(t, metadata.Domain)

			// The stream plan has to agree: the protocol reported for each of these is the one its
			// own parser owns, never HTTP.
			planMetadata, planErr, _ := peekStreamChunks(t, [][]byte{testCase.payload}, nil, nil)
			require.NotEqual(t, C.ProtocolHTTP, planMetadata.Protocol,
				"the plan attributed %s to HTTP (err %v)", testCase.name, planErr)
		})
	}
}

// TestHTTPHostSurvivesAReadBoundaryInsideCRLF places the read boundary exactly between the CR and
// the LF of the request line, which is the one split the gate has to be careful about: a CR as the
// last byte in hand may end the line or sit inside it, and only the byte after it tells the two
// apart. The request below is a real one, so a gate that decided on the CR would turn a valid
// request into a verdict and the domain would be lost.
func TestHTTPHostSurvivesAReadBoundaryInsideCRLF(t *testing.T) {
	t.Parallel()
	reader := &httpSplitReader{pieces: [][]byte{
		[]byte("GET / HTTP/1.1\r"),
		[]byte("\nHost: crlf.example.com\r"),
		[]byte("\nAccept: */*\r\n\r\n"),
	}}
	var metadata adapter.InboundContext
	require.NoError(t, sniff.HTTPHost(context.Background(), &metadata, reader))
	require.Equal(t, C.ProtocolHTTP, metadata.Protocol)
	require.Equal(t, "crlf.example.com", metadata.Domain)
}

// TestPeekStreamSurvivesASegmentBoundaryInsideCRLF is the same boundary one layer out, where it is a
// *segment* boundary rather than a read boundary: the CR ends a buffer an earlier sniff action
// cached and the LF starts the bytes this call reads. The payload reader hands the gate one segment
// per Read, so the gate really does see a head whose last byte is that CR.
func TestPeekStreamSurvivesASegmentBoundaryInsideCRLF(t *testing.T) {
	t.Parallel()
	cached := []*buf.Buffer{buf.As([]byte("POST /dav/ HTTP/1.1\r"))}
	metadata, err, readCount := peekStreamChunks(t,
		[][]byte{[]byte("\nHost: crlf-cached.example.com\r\nContent-Length: 0\r\n\r\n")},
		cached, nil)
	require.NoError(t, err)
	require.Equal(t, C.ProtocolHTTP, metadata.Protocol)
	require.Equal(t, "crlf-cached.example.com", metadata.Domain)
	require.Equal(t, 1, readCount, "the whole request was already in hand, so one read is the whole conversation")
}

// TestPeekStreamReadBoundaryInsideTheRequestLine records what the plan does when a read ends inside
// the request line, which is where "a CR at the end of one read and the LF at the start of the next"
// lands - and also where every other cut inside that line lands.
//
// MEASURED, and the reason this test asserts a loss rather than a recognition: the sniffer is not
// what fails here. The gate waits for the byte after the CR exactly as it should
// (TestHTTPHostSurvivesAReadBoundaryInsideCRLF pins that, and it passes), and the request is
// recovered when the CR ends a *cached segment* instead of a read
// (TestPeekStreamSurvivesASegmentBoundaryInsideCRLF). What is lost is the flow, and the cause is one
// layer down: net/http's parseRequestLine answers a request line it has not finished reading with a
// definite `malformed HTTP version` verdict rather than io.ErrUnexpectedEOF, so no sniffer in the
// plan is still asking for bytes, so PeekStream ends the attempt after the first read.
//
// The three cases are what separates the CR from the general case: a cut at the CR, a cut mid-line
// away from any CR, and a cut after the request line, which is the only shape that works. The two
// failing shapes are the same limitation TestStreamSniffingAcrossReadsMatchesPreOptimizationBaseline
// already pins from its side ("http-cut-inside-version", "http-cut-inside-path"), reached here from
// the CR side. It is reported rather than repaired: the repair changes what HTTPHost answers for an
// incomplete request, which this round is not allowed to touch.
func TestPeekStreamReadBoundaryInsideTheRequestLine(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name       string
		chunks     [][]byte
		recognised bool
	}{
		{
			name: "cut between the CR and the LF",
			chunks: [][]byte{
				[]byte("GET /crlf HTTP/1.1\r"),
				[]byte("\nHost: crlf.example.com\r\nAccept: */*\r\n\r\n"),
			},
		},
		{
			name: "cut mid-line, away from any CR",
			chunks: [][]byte{
				[]byte("GET /crlf HTTP/1."),
				[]byte("1\r\nHost: crlf.example.com\r\nAccept: */*\r\n\r\n"),
			},
		},
		{
			name: "cut after the request line",
			chunks: [][]byte{
				[]byte("GET /crlf HTTP/1.1\r\n"),
				[]byte("Host: crlf.example.com\r\nAccept: */*\r\n\r\n"),
			},
			recognised: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			metadata, err, readCount := peekStreamChunks(t, testCase.chunks, nil, nil)
			if testCase.recognised {
				require.NoError(t, err)
				require.Equal(t, C.ProtocolHTTP, metadata.Protocol)
				require.Equal(t, "crlf.example.com", metadata.Domain)
				require.Equal(t, 2, readCount)
				return
			}
			require.Error(t, err)
			require.False(t, errors.Is(err, sniff.ErrNeedMoreData),
				"the sweep stopped with a request for more data, so this case is no longer the limitation it pins")
			require.Empty(t, metadata.Protocol)
			require.Empty(t, metadata.Domain)
			// The verdict is the parser's own, which is the evidence for where the loss happens.
			require.ErrorContains(t, err, "malformed HTTP version")
			require.Equal(t, 1, readCount, "nothing was still asking for bytes, so one read ended the attempt")
		})
	}
}

// TestPeekStreamHostAfterTheRequestLine is the "Host arrives later" case stated as an assertion on
// the reported domain rather than on the read count: the request line is one read, the Host header
// is the next, and the domain has to be the one from the later read.
func TestPeekStreamHostAfterTheRequestLine(t *testing.T) {
	t.Parallel()
	metadata, err, readCount := peekStreamChunks(t, [][]byte{
		[]byte("GET /late-host HTTP/1.1\r\n"),
		[]byte("User-Agent: sniff\r\n"),
		[]byte("Host: late-host.example.com\r\n\r\n"),
	}, nil, nil)
	require.NoError(t, err)
	require.Equal(t, C.ProtocolHTTP, metadata.Protocol)
	require.Equal(t, "late-host.example.com", metadata.Domain)
	require.Equal(t, 3, readCount)
}
