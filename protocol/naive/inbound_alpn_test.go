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
// The fix is a transport-scoped view whose list is exactly ["h3"].
//
// The two non-h3 rows are deliberate: this commit is an ordering fix, not a
// change to what "tls.alpn omits h3" means. Those paths must keep their current
// behaviour (empty list is filled by the HTTP/3 initialiser; a list without h3
// is refused there), and the contract change that makes `network` authoritative
// is a separate decision with its own tests.
func TestQuicTLSConfigScopesALPNToH3(t *testing.T) {
	cases := []struct {
		name       string
		configured []string
		want       []string
		scoped     bool
	}{
		{
			name:       "h2 before h3 is the defect",
			configured: []string{"h2", "http/1.1", "h3"},
			want:       []string{"h3"},
			scoped:     true,
		},
		{
			name:       "h3 first still gets a scoped list",
			configured: []string{"h3", "h2"},
			want:       []string{"h3"},
			scoped:     true,
		},
		{
			name:       "operator list without h3 keeps the existing path",
			configured: []string{"h2", "http/1.1"},
			want:       []string{"h2", "http/1.1"},
			scoped:     false,
		},
		{
			name:       "unset keeps the existing path",
			configured: nil,
			want:       nil,
			scoped:     false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			shared := &stubServerConfig{nextProtos: append([]string(nil), testCase.configured...)}
			inbound := &Inbound{tlsConfig: shared}

			got := inbound.quicTLSConfig()
			if got == nil {
				t.Fatal("quicTLSConfig returned nil")
			}
			if !slices.Equal(got.NextProtos(), testCase.want) {
				t.Fatalf("QUIC ALPN = %v, want %v", got.NextProtos(), testCase.want)
			}
			isScoped := got != shared
			if isScoped != testCase.scoped {
				t.Fatalf("scoped = %v, want %v (QUIC ALPN = %v)",
					isScoped, testCase.scoped, got.NextProtos())
			}
			if isScoped {
				// A view must not write through to the shared config: that is
				// the whole point of not mutating the object TCP is using.
				got.SetNextProtos([]string{"mutated"})
				if !slices.Equal(shared.NextProtos(), testCase.configured) {
					t.Fatalf("the QUIC view mutated the shared ALPN list: %v",
						shared.NextProtos())
				}
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
