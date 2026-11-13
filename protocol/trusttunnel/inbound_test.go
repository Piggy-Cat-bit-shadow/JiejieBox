package trusttunnel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/json/badoption"

	"golang.org/x/net/http2"
)

func testCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "tt.test"},
		DNSNames:     []string{"tt.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

func validInboundOptions(t *testing.T) option.TrustTunnelInboundOptions {
	certificate, key := testCertificate(t)
	return option.TrustTunnelInboundOptions{
		Users: []auth.User{{Username: "user", Password: "secret"}},
		InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
			TLS: &option.InboundTLSOptions{
				Enabled:     true,
				ServerName:  "tt.test",
				Certificate: badoption.Listable[string]{certificate},
				Key:         badoption.Listable[string]{key},
			},
		},
	}
}

func TestResolveTransportALPNSeparatesQUICAndTCP(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		configured []string
		serveTCP   bool
		serveQUIC  bool
		wantTCP    []string
		wantQUIC   []string
	}{
		{
			name:     "dual adds h2 and h3",
			serveTCP: true, serveQUIC: true,
			wantTCP: []string{http2.NextProtoTLS}, wantQUIC: []string{nextProtoH3},
		},
		{
			name:       "dual removes configured h3 from TCP",
			configured: []string{nextProtoH3},
			serveTCP:   true, serveQUIC: true,
			wantTCP: []string{http2.NextProtoTLS}, wantQUIC: []string{nextProtoH3},
		},
		{
			name:       "dual keeps configured TCP protocols in order",
			configured: []string{"acme-tls/1", nextProtoH3},
			serveTCP:   true, serveQUIC: true,
			wantTCP: []string{http2.NextProtoTLS, "acme-tls/1"}, wantQUIC: []string{nextProtoH3},
		},
		{
			name:       "tcp only drops h3",
			configured: []string{nextProtoH3},
			serveTCP:   true, serveQUIC: false,
			wantTCP: []string{http2.NextProtoTLS}, wantQUIC: nil,
		},
		{
			name:       "quic only is h3 and never h2",
			configured: []string{http2.NextProtoTLS},
			serveTCP:   false, serveQUIC: true,
			wantTCP: nil, wantQUIC: []string{nextProtoH3},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			gotTCP, gotQUIC := resolveTransportALPN(testCase.configured, testCase.serveTCP, testCase.serveQUIC)
			if !slices.Equal(gotTCP, testCase.wantTCP) {
				t.Fatalf("tcp ALPN = %v, want %v", gotTCP, testCase.wantTCP)
			}
			if !slices.Equal(gotQUIC, testCase.wantQUIC) {
				t.Fatalf("quic ALPN = %v, want %v", gotQUIC, testCase.wantQUIC)
			}
		})
	}
}
func TestInboundValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		modify func(options *option.TrustTunnelInboundOptions)
		want   string
	}{
		{"tls missing", func(options *option.TrustTunnelInboundOptions) { options.TLS = nil }, C.ErrTLSRequired.Error()},
		{"tls disabled", func(options *option.TrustTunnelInboundOptions) { options.TLS.Enabled = false }, C.ErrTLSRequired.Error()},
		{"no users", func(options *option.TrustTunnelInboundOptions) { options.Users = nil }, "missing users"},
		{"empty username", func(options *option.TrustTunnelInboundOptions) {
			options.Users = append(options.Users, auth.User{Password: "secret"})
		}, "users[1]: missing username"},
		{"empty password", func(options *option.TrustTunnelInboundOptions) {
			options.Users = []auth.User{{Username: "user"}}
		}, "users[0]: missing password"},
		{"icmp", func(options *option.TrustTunnelInboundOptions) { options.Network = "icmp" }, "unsupported network: icmp"},
		{"bad congestion control", func(options *option.TrustTunnelInboundOptions) { options.QUICCongestionControl = "new_reno" }, "unknown quic_congestion_control: new_reno"},
	}
	for _, testCase := range cases {
		options := validInboundOptions(t)
		testCase.modify(&options)
		_, err := NewInbound(context.Background(), nil, log.NewNOPFactory().Logger(), "tt-in", options)
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Errorf("%s: expected %q, got %v", testCase.name, testCase.want, err)
		}
	}
}

func TestInboundAcceptsValidOptions(t *testing.T) {
	t.Parallel()
	for _, network := range []option.NetworkList{"", "tcp", "udp"} {
		options := validInboundOptions(t)
		options.Network = network
		options.QUICCongestionControl = "cubic"
		inbound, err := NewInbound(context.Background(), nil, log.NewNOPFactory().Logger(), "tt-in", options)
		if err != nil {
			t.Fatalf("network %q: %v", network, err)
		}
		if inbound.Type() != C.TypeTrustTunnel {
			t.Fatalf("type = %s", inbound.Type())
		}
	}
}
