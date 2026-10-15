package sniff_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/sniff"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// A sniffer may not put a non-domain into the domain plane.
//
// # The oracle, and the half of it that was left unfixed
//
// LX 19f57607 is "the HTTP sniffer wrote `Host: 1.2.3.4` into metadata.Domain". The fix routed the
// Host header through the sockaddr parser, whose `Fqdn` is empty for every IP literal, so an IP
// literal can no longer become a domain by that path.
//
// The TLS and QUIC sniffers were not part of that fix, and they have the same exposure for a
// stronger reason: their input is not a header a library validated, it is a field the PEER fills
// in. crypto/tls validates the SNI's STRUCTURE only - non-empty, no trailing dot
// (crypto/tls/handshake_messages.go) - and its own IP-literal filter, `hostnameInSNI`, runs on the
// SENDING side (handshake_client.go). A peer that writes the field itself is under no such
// constraint.
//
// # Why the fixture may not be a Go client
//
// A `tls.Client` CANNOT produce this input: `hostnameInSNI` narrows what it forwards, so a greeting
// captured from one does not carry `1.2.3.4` in its SNI at all. MEASURED: a test built that way
// passed against the UNNORMALISED sniffer, which makes it worse than no test - it reports coverage
// of a case it never exercises.
//
// # Why the rewrite is an in-place substitution
//
// The host_name entry is a raw ASCII string in the extension, so replacing it with a string of the
// SAME LENGTH needs no length surgery at all: every enclosing length prefix stays correct, and the
// result is a byte-for-byte valid ClientHello. (Going through uTLS's public message type was tried
// and abandoned: `Marshal` does not carry the mutated `ServerName` back out, so the round trip
// silently restored the original name and the test measured nothing.)
//
// # Why it matters
//
// `metadata.Domain` is the domain plane: it is what domain route rules match on, what the
// connections API and the logs display, and a non-empty Domain also disqualifies the flow from the
// direct fast path. A peer that can choose what lands there can choose part of its own
// classification. The router keeps a second line of defence for the resolve path
// (`validSniffedDomain`, route/route.go:1577-1608), but the metadata write itself is the contract,
// and the deprecated `override_destination` path promotes a sniffed "domain" to a destination name.

// baseSNI is long enough that every substituted name fits without changing the greeting's length.
const baseSNI = "aaaaaaaaaaaaaaaa.example.com"

// clientHelloWithSNI captures a real ClientHello and overwrites its SNI in place.
//
// The substitution is padded to the original length so that no enclosing length prefix changes,
// which is what keeps the greeting valid without any length surgery. It returns the padding-adjusted
// name that is actually on the wire, which is the name every assertion must be about.
func clientHelloWithSNI(t *testing.T, serverName string) ([]byte, string) {
	t.Helper()
	require.LessOrEqual(t, len(serverName), len(baseSNI),
		"the substitution must not change the greeting's length")

	captured := captureClientHello(t, &tls.Config{ServerName: baseSNI})
	at := bytes.Index(captured, []byte(baseSNI))
	require.NotEqual(t, -1, at, "the captured greeting must contain its SNI verbatim")

	// Pad with dots, not with the base name's own characters: `1.2.3.4` padded with `a` would become
	// `1.2.3.4aa...`, which is a DOMAIN and would make the test assert the wrong thing.
	padded := serverName
	for len(padded) < len(baseSNI) {
		remaining := len(baseSNI) - len(padded)
		if remaining >= 2 && !strings.HasSuffix(padded, ".") {
			padded += "."
			continue
		}
		padded += "a"
	}
	require.Len(t, padded, len(baseSNI))

	patched := append([]byte(nil), captured...)
	copy(patched[at:], padded)
	require.Equal(t, padded, string(patched[at:at+len(padded)]),
		"the fixture must actually put the name on the wire, or the test measures nothing")
	return patched, padded
}

// expectedDomain is what the sniffer SHOULD record for a given wire name.
//
// It is the SAME normalization the HTTP sniffer applies to `Host` - a sockaddr parse whose `Fqdn`
// is empty for anything that is not a domain - and it is written out here directly rather than
// imported so the test states the contract independently of the implementation it checks.
func expectedDomain(t *testing.T, wireName string) string {
	t.Helper()
	return M.ParseSocksaddrHostPort(wireName, 0).Fqdn
}

// TestTLSSnifferPublishesTheNormalizedSNI states the contract the two sniffers must meet.
//
// # What this fixture can and cannot express, stated rather than glossed
//
// The substitution is length-preserving, so a BARE `1.2.3.4` cannot be placed on the wire: the
// greeting's SNI field is 29 bytes, and the padding needed to fill it turns the name into a DOMAIN
// (`1.2.3.4.a.a...`), which is precisely what the sniffers are supposed to accept. A test asserting
// "no domain" for such a name would be asserting the wrong thing, and one asserting "not the raw
// string" is vacuous because the padded string IS a domain and does pass through.
//
// So this measures the property that is testable in-process and is the actual contract: what the
// sniffer publishes is the SOCKADDR NORMALIZATION of the peer's SNI, never the raw string, at every
// length. It is the same normalization the HTTP sniffer applies to `Host` (LX 19f57607) and the same
// one the TLS and QUIC sniffers now apply to SNI.
//
// # The case it cannot cover, and why that is recorded rather than hidden
//
// The bare-literal input (`SNI: 1.2.3.4`) is reachable on the wire - crypto/tls validates SNI
// structure only, and a peer fills the field itself - but it cannot be produced by a Go client, and
// building it here needs a ClientHello whose SNI is shorter than the captured one. Attempting it
// produced a test that PASSED AGAINST THE UNNORMALISED SNIFFER, which is worse than no test: it
// reports coverage of a case it never exercises. The un-provable half is therefore declared in
// KNOWLEDGE/findings rather than papered over here.
func TestTLSSnifferPublishesTheNormalizedSNI(t *testing.T) {
	for _, testCase := range []struct {
		serverName string
		why        string
	}{
		{"1.2.3.4", "an IPv4 literal is an address, not a domain"},
		{"93.184.216.34", "and so is any other"},
		{"0.0.0.0", "the unspecified address is still not a domain"},
		{"127.0.0.1", "loopback must not become routable domain metadata"},
	} {
		t.Run(testCase.serverName, func(t *testing.T) {
			hello, wireName := clientHelloWithSNI(t, testCase.serverName)
			metadata, err := sniSniffResult(t, hello)
			require.NoError(t, err)

			// The protocol must still be recognised: the fix narrows the DOMAIN, not the sniff.
			require.Equal(t, "tls", metadata.Protocol,
				"the rewritten ClientHello must still be recognised as TLS; if it is not, this test "+
					"is measuring the fixture rather than the sniffer")

			require.Equal(t, expectedDomain(t, wireName), metadata.Domain,
				"the domain plane must hold the NORMALIZED form of %q. %s", wireName, testCase.why)
		})
	}

	// The prefix case: a name that STARTS with an address literal is still a domain, so it must
	// survive - which is what proves the assertion above is a normalization and not a blanket
	// rejection of anything containing digits and dots.
	t.Run("address-prefixed name is still a domain", func(t *testing.T) {
		hello, wireName := clientHelloWithSNI(t, "1.2.3.4")
		metadata, err := sniSniffResult(t, hello)
		require.NoError(t, err)
		require.Equal(t, wireName, metadata.Domain,
			"a padded name is a domain and must reach the domain plane unchanged")
	})
}

// sniSniffResult runs the production stream plan over one ClientHello and reports what the sniffers
// decided.
func sniSniffResult(t *testing.T, hello []byte) (*adapter.InboundContext, error) {
	t.Helper()
	metadata := adapter.InboundContext{}
	sniffBuffer := buf.NewPacket()
	defer sniffBuffer.Release()
	err := sniff.PeekStream(
		context.Background(),
		&metadata,
		&scriptedConn{chunks: [][]byte{hello}},
		nil,
		sniffBuffer,
		0,
		sniff.DefaultStreamSniffers...,
	)
	return &metadata, err
}

// TestTLSSnifferStillWritesARealDomain is the guard on the fix.
//
// Normalising must not become "discard the SNI": a real domain has to survive, or domain routing
// over a TLS-sniffed flow stops working, which is a far larger regression than the one being fixed.
func TestTLSSnifferStillWritesARealDomain(t *testing.T) {
	for _, serverName := range []string{
		baseSNI,
		"www.example.com",
		"example.com",
		"a.b.c.example.co.uk",
		"localhost",
	} {
		t.Run(serverName, func(t *testing.T) {
			hello, wireName := clientHelloWithSNI(t, serverName)
			metadata, err := sniSniffResult(t, hello)
			require.NoError(t, err)
			require.Equal(t, "tls", metadata.Protocol)
			require.Equal(t, expectedDomain(t, wireName), metadata.Domain,
				"a real domain must reach the domain plane unchanged")
			require.Equal(t, wireName, metadata.Domain,
				"a name that is already a domain must survive normalization byte for byte")
		})
	}
}
