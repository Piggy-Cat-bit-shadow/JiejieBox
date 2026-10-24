package dhcp

import (
	"reflect"
	"testing"
)

func TestNameList(t *testing.T) {
	testCases := []struct {
		name   string
		search []string
		input  string
		want   []string
	}{
		{"single label", []string{"example.com."}, "host.", []string{"host.example.com.", "host."}},
		{"multi label untouched", []string{"example.com."}, "host.local.", []string{"host.local."}},
		{"fully qualified with trailing dot", []string{"example.com."}, "host.example.com.", []string{"host.example.com."}},
		{"empty search", nil, "host.", []string{"host."}},
		{"duplicate suffixes keep both probes", []string{"example.com.", "example.com."}, "host.", []string{"host.example.com.", "host.example.com.", "host."}},
		{"reverse ipv6 is multi-label", []string{"example.com."}, "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa.", []string{"1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa."}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := nameList(testCase.search, testCase.input)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("nameList(%q, %q) = %q, want %q", testCase.search, testCase.input, got, testCase.want)
			}
		})
	}
}
