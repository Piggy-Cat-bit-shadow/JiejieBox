//go:build with_tailscale

package derp

import "testing"

// The mesh peer URL is built by string concatenation, so an IPv6 literal without brackets turns
// the port into the host. Domains and IPv4 literals must remain unchanged.
func TestDERPMeshHost(t *testing.T) {
	testCases := []struct {
		name     string
		hostname string
		port     uint16
		want     string
	}{
		{"domain default", "derp.example", 443, "derp.example"},
		{"domain explicit", "derp.example", 8443, "derp.example:8443"},
		{"ipv4 default", "192.0.2.1", 443, "192.0.2.1"},
		{"ipv4 explicit", "192.0.2.1", 8443, "192.0.2.1:8443"},
		{"ipv6 default", "2001:db8::1", 443, "[2001:db8::1]"},
		{"ipv6 zero", "2001:db8::1", 0, "[2001:db8::1]"},
		{"ipv6 explicit", "2001:db8::1", 8443, "[2001:db8::1]:8443"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := derpMeshHost(testCase.hostname, testCase.port); got != testCase.want {
				t.Fatalf("derpMeshHost(%q, %d) = %q, want %q", testCase.hostname, testCase.port, got, testCase.want)
			}
		})
	}
}
