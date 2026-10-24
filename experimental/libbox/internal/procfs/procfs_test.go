//go:build linux || android

package procfs

import (
	"net/netip"
	"testing"
)

// The legacy Android path compares /proc/net/tcp{,6} rows byte-for-byte against this form:
// native-endian 32-bit words of the address, then a big-endian port. Android is little-endian on
// every supported ABI, so these are the exact strings the parser will see.
func TestFormatSocketAddress(t *testing.T) {
	testCases := []struct {
		name    string
		address netip.AddrPort
		want    string
	}{
		{"ipv4 loopback", netip.MustParseAddrPort("127.0.0.1:80"), "0100007f:0050"},
		{"ipv4 any", netip.MustParseAddrPort("0.0.0.0:443"), "00000000:01bb"},
		{"ipv4 public", netip.MustParseAddrPort("192.0.2.10:12345"), "0a0200c0:3039"},
		{"ipv6 loopback", netip.MustParseAddrPort("[::1]:53"), "00000000000000000000000001000000:0035"},
		{"ipv6 unspecified", netip.MustParseAddrPort("[::]:5353"), "00000000000000000000000000000000:14e9"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := formatSocketAddress(testCase.address); got != testCase.want {
				t.Fatalf("formatSocketAddress(%s) = %q, want %q", testCase.address, got, testCase.want)
			}
		})
	}
}

// A port is always encoded big-endian, independent of the address words.
func TestFormatSocketAddressPortIsBigEndian(t *testing.T) {
	got := formatSocketAddress(netip.MustParseAddrPort("127.0.0.1:1"))
	if got[len(got)-4:] != "0001" {
		t.Fatalf("port encoding is not big-endian: %q", got)
	}
}
