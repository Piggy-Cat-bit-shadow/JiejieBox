package parser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Fuzz target for the share-link reader (PS), the fallback the format-detection chain uses for
// every document the structured readers refuse.
//
// The invariants are the ones the rest of the program relies on: a successful parse is a USABLE
// outbound (a type and a non-nil option object, serialisable for the cache), and the provider's real
// entry point accepts the same single link and returns the same node. A panic would take down the
// daemon; an outbound with nil options would panic later, during construction.
//
// Run it with:
//
//	go test ./provider/parser -run '^$' -fuzz '^FuzzParseSubscriptionLinkKeepsTheOutboundUsable$' -fuzztime=30s
func FuzzParseSubscriptionLinkKeepsTheOutboundUsable(f *testing.F) {
	seeds := []string{
		``,
		`not-a-link`,
		`://`,
		`vless://11111111-2222-3333-4444-555555555555@example.com:443?security=tls&sni=example.com#node`,
		`trojan://password@example.com:443?security=tls&type=ws&path=%2Fws#node`,
		`ss://YWVzLTEyOC1nY206eA@example.com:443#node`,
		`ss://YWVzLTEyOC1nY206eA==@example.com:443#node`,
		`vmess://eyJ2IjoiMiIsInBzIjoibm9kZSIsImFkZCI6ImV4YW1wbGUuY29tIiwicG9ydCI6NDQzLCJpZCI6IjExMTExMTExLTIyMjItMzMzMy00NDQ0LTU1NTU1NTU1NTU1NSIsIm5ldCI6IndzIiwiaG9zdCI6ImV4YW1wbGUuY29tIiwicGF0aCI6Ii93cyJ9`,
		`vmess://!!!notbase64!!!`,
		`hysteria2://password@example.com:8443?sni=example.com&insecure=1#node`,
		`hy2://password@example.com?obfs=salamander&obfs-password=x#node`,
		`anytls://password@example.com:443#node`,
		`tuic://uuid:password@example.com:443?congestion_control=bbr&alpn=h3,h2#node`,
		`hysteria://example.com:443?auth=x&up=100&down=100#node`,
		`vless://@:`,
		`vless://%00@example.com:443`,
		`vless://x@[::1]:443?type=grpc&serviceName=x#node`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, link string) {
		if len(link) > 1<<16 {
			t.Skip("bound one fuzz case to a share-link-sized input")
		}
		outbound, err := ParseSubscriptionLink(link)
		if err != nil {
			return
		}
		if outbound.Type == "" {
			t.Fatalf("successful link parse returned no type: %q", link)
		}
		if outbound.Options == nil {
			t.Fatalf("successful link parse returned nil options: %q", link)
		}
		if _, marshalErr := json.Marshal(outbound); marshalErr != nil {
			t.Fatalf("successful link parse returned a non-serialisable outbound for %q: %v", link, marshalErr)
		}
		// A single link is also a raw-list subscription, and the provider uses that entry point.
		if strings.ContainsAny(link, "\r\n") {
			return
		}
		document, parseErr := ParseSubscription(context.Background(), link, nil, "p")
		if parseErr != nil {
			t.Fatalf("the format-detection chain refused a link its own reader accepted: %q: %v", link, parseErr)
		}
		if len(document) == 0 {
			t.Fatalf("the format-detection chain accepted %q but returned no nodes", link)
		}
	})
}
