package naive

import (
	"slices"
	"testing"
)

// TestQuicTLSConfigScopesALPNToH3 pins the QUIC listener's TLS configuration.
//
// The inbound shares one ServerConfig between its TCP and QUIC listeners. Go's
// ALPN negotiates by the SERVER's list order, so handing the shared list to the
// QUIC listener lets a client that offers ["h3","h2"] negotiate "h2" over QUIC.
// The QUIC listener therefore gets a transport-scoped view whose list is exactly
// ["h3"], whatever the operator configured.
//
// The contract the last two rows settle: `network` is the switch that decides
// whether H3 is served. An operator list without h3 stays a TCP-side list; it
// does not turn the QUIC listener off (which is what it used to do, with only a
// warning at Start and a passing `sing-box check`).
func TestQuicTLSConfigScopesALPNToH3(t *testing.T) {
	cases := []struct {
		name       string
		configured []string
	}{
		{name: "h2 before h3 is the defect", configured: []string{"h2", "http/1.1", "h3"}},
		{name: "h3 first still gets a scoped list", configured: []string{"h3", "h2"}},
		{name: "operator list without h3 still serves h3", configured: []string{"h2", "http/1.1"}},
		{name: "unset still serves h3", configured: nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			shared := &stubServerConfig{nextProtos: append([]string(nil), testCase.configured...)}
			inbound := &Inbound{tlsConfig: shared}

			got := inbound.quicTLSConfig()
			if got == nil {
				t.Fatal("quicTLSConfig returned nil")
			}
			if !slices.Equal(got.NextProtos(), []string{"h3"}) {
				t.Fatalf("QUIC ALPN = %v, want [h3]", got.NextProtos())
			}
			if got == shared {
				t.Fatal("the QUIC listener must use a transport-scoped view, not the shared config")
			}
			// A view must not write through to the shared config: that is the
			// whole point of not mutating the object TCP is using.
			got.SetNextProtos([]string{"mutated"})
			if !slices.Equal(shared.NextProtos(), testCase.configured) {
				t.Fatalf("the QUIC view mutated the shared ALPN list: %v", shared.NextProtos())
			}
		})
	}
}

// TestQuicTLSConfigWithoutTLS confirms the helper preserves the nil config that
// builds without TLS produce; the HTTP/3 initialiser reports its own error.
func TestQuicTLSConfigWithoutTLS(t *testing.T) {
	inbound := &Inbound{}
	if got := inbound.quicTLSConfig(); got != nil {
		t.Fatalf("quicTLSConfig = %v, want nil when the inbound has no TLS config", got)
	}
}
