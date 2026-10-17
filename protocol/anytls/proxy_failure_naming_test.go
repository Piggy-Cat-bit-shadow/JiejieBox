package anytls

import (
	"errors"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// A failure the PROXY reported must name the proxy, because that is the only actionable fact about it.
//
// # The production evidence
//
// Four connections in one log window ended with:
//
//	connection: connection download closed: anytls: remote: lookup analytics.immersivetranslate.com: NXDOMAIN
//
// The prefix `remote:` places the lookup on the proxy's side, and the name is demonstrably valid - this
// machine resolves it to Cloudflare addresses through both 1.1.1.1 and 8.8.8.8 - so the failure is a
// property of one proxy's resolver configuration. Nothing in the message identified WHICH of the configured
// AI/overseas outbounds that was. This outbound is the only place that knows, so it now says so.
//
// # What this test proves, and what it does not
//
// It proves the wrapping: a `remote:` failure surfaces with the outbound's tag and server attached, and a
// nil error is not turned into a failure. It does NOT prove the NXDOMAIN is resolved, because it cannot be
// from here - the lookup happens on the other side of the wire.
func TestProxyFailuresNameTheProxy(t *testing.T) {
	outbound := &Outbound{
		Adapter: outbound.NewAdapter(C.TypeAnyTLS, "🇺🇸 美国｜住宅 AnyTLS", []string{N.NetworkTCP, N.NetworkUDP}, nil),
		server:  M.ParseSocksaddrHostPort("95.169.1.53", 443),
	}

	remoteFailure := errors.New("anytls: remote: lookup analytics.immersivetranslate.com: NXDOMAIN")
	named := outbound.nameTheProxy(remoteFailure)

	if named == nil {
		t.Fatal("a non-nil failure must stay non-nil")
	}
	for _, want := range []string{
		// The outbound, by the operator's own label.
		"🇺🇸 美国｜住宅 AnyTLS",
		// And the far end, which is the fact the label does not carry.
		"95.169.1.53",
		// The original cause survives, so the KIND of failure is still visible.
		"NXDOMAIN",
		"lookup analytics.immersivetranslate.com",
	} {
		if !strings.Contains(named.Error(), want) {
			t.Fatalf("%q does not contain %q", named.Error(), want)
		}
	}

	// The success path must not be disturbed: wrapping a nil error would manufacture a failure.
	if outbound.nameTheProxy(nil) != nil {
		t.Fatal("a nil error must stay nil")
	}
}

// TestProxyNamingDoesNotDoubleWrap keeps the prefix off a cause that already carries it.
//
// The message is one line in a connection report. Repeating the proxy on every hop of a retry chain would
// turn it into noise, and the second identical prefix says nothing the first did not.
func TestProxyNamingDoesNotDoubleWrap(t *testing.T) {
	outbound := &Outbound{
		Adapter: outbound.NewAdapter(C.TypeAnyTLS, "AI", []string{N.NetworkTCP}, nil),
		server:  M.ParseSocksaddrHostPort("203.0.113.9", 443),
	}

	once := outbound.nameTheProxy(errors.New("anytls: remote: lookup example.com: NXDOMAIN"))
	twice := outbound.nameTheProxy(once)
	if twice.Error() != once.Error() {
		t.Fatalf("naming an already-named failure changed it:\n once: %s\n twice: %s", once, twice)
	}
}
