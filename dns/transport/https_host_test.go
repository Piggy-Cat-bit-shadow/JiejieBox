package transport

import (
	"testing"

	M "github.com/sagernet/sing/common/metadata"
)

// Coverage for the URL host spelling: a domain must not be bracketed, an IPv6 literal must be,
// and the port is only appended when it is not the default 443.
func TestHTTPSDestinationHost(t *testing.T) {
	testCases := []struct {
		name    string
		address M.Socksaddr
		port    uint16
		want    string
	}{
		{"domain default port", M.ParseSocksaddrHostPort("example.com", 443), 443, "example.com"},
		{"domain explicit port", M.ParseSocksaddrHostPort("example.com", 8443), 8443, "example.com:8443"},
		{"domain zero port", M.ParseSocksaddrHostPort("example.com", 0), 0, "example.com"},
		{"ipv4 default port", M.ParseSocksaddrHostPort("192.0.2.1", 443), 443, "192.0.2.1"},
		{"ipv4 explicit port", M.ParseSocksaddrHostPort("192.0.2.1", 8443), 8443, "192.0.2.1:8443"},
		{"ipv6 default port", M.ParseSocksaddrHostPort("2001:db8::1", 443), 443, "[2001:db8::1]"},
		{"ipv6 zero port", M.ParseSocksaddrHostPort("2001:db8::1", 0), 0, "[2001:db8::1]"},
		{"ipv6 explicit port", M.ParseSocksaddrHostPort("2001:db8::1", 8443), 8443, "[2001:db8::1]:8443"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := httpsDestinationHost(testCase.address, testCase.port); got != testCase.want {
				t.Fatalf("httpsDestinationHost(%s, %d) = %q, want %q", testCase.address, testCase.port, got, testCase.want)
			}
		})
	}
}
