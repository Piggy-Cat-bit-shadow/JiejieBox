package quic

import (
	"testing"

	M "github.com/sagernet/sing/common/metadata"
)

// HTTP/3 uses the same URL host rules as HTTP/2; this pins that the IPv6 brackets survive the
// separate transport and that a domain is not bracketed.
func TestHTTP3DestinationHost(t *testing.T) {
	testCases := []struct {
		name    string
		address M.Socksaddr
		port    uint16
		want    string
	}{
		{"domain default port", M.ParseSocksaddrHostPort("dns.example", 443), 443, "dns.example"},
		{"domain explicit port", M.ParseSocksaddrHostPort("dns.example", 8443), 8443, "dns.example:8443"},
		{"ipv4 default port", M.ParseSocksaddrHostPort("192.0.2.1", 443), 443, "192.0.2.1"},
		{"ipv6 default port", M.ParseSocksaddrHostPort("2001:db8::1", 443), 443, "[2001:db8::1]"},
		{"ipv6 zero port", M.ParseSocksaddrHostPort("2001:db8::1", 0), 0, "[2001:db8::1]"},
		{"ipv6 explicit port", M.ParseSocksaddrHostPort("2001:db8::1", 8443), 8443, "[2001:db8::1]:8443"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := http3DestinationHost(testCase.address, testCase.port); got != testCase.want {
				t.Fatalf("http3DestinationHost(%s, %d) = %q, want %q", testCase.address, testCase.port, got, testCase.want)
			}
		})
	}
}
