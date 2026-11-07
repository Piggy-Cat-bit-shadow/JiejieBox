package parser

import (
	"context"
	"testing"
)

// FuzzParseSubscription feeds arbitrary bytes to the format-detection chain. The invariant is
// narrow on purpose: whatever the input, the function must return, and an error must not be paired
// with nodes. Node-level correctness is covered by the table tests.
func FuzzParseSubscription(f *testing.F) {
	seeds := []string{
		``,
		`{"outbounds": []}`,
		`{"outbounds": [{"type": "direct", "tag": "a"}]}`,
		`{"outbounds": [{"type": "vless", "tag": "a", "server": "example.com", "server_port": 443, "uuid": "11111111-2222-3333-4444-555555555555"}]}`,
		`{"outbounds": [null]}`,
		`{"proxies": []}`,
		"proxies:\n  - {name: a, type: ss, server: example.com, port: 443, cipher: aes-128-gcm, password: x}\n",
		`{"version": 1, "servers": [{"server": "example.com", "server_port": 443, "method": "aes-128-gcm", "password": "x"}]}`,
		"ss://YWVzLTEyOC1nY206eA@example.com:443#node\n",
		"not a subscription",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, content string) {
		outbounds, err := ParseSubscription(context.Background(), content, nil, "p")
		if err == nil && len(outbounds) == 0 {
			t.Fatalf("a successful parse returned no outbounds for %q", content)
		}
		if err != nil && len(outbounds) != 0 {
			t.Fatalf("an error was paired with %d outbounds for %q", len(outbounds), content)
		}
	})
}
