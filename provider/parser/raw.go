package parser

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
)

func ParseRawSubscription(ctx context.Context, content string) ([]option.Outbound, []option.Endpoint, error) {
	if base64Content, err := DecodeBase64URLSafe(content); err == nil {
		servers, _ := parseRawSubscription(base64Content)
		if len(servers) > 0 {
			return servers, nil, err
		}
	}
	outbounds, err := parseRawSubscription(content)
	return outbounds, nil, err
}

func parseRawSubscription(content string) ([]option.Outbound, error) {
	var servers []option.Outbound
	content = strings.ReplaceAll(content, "\r\n", "\n")
	linkList := strings.SplitSeq(content, "\n")
	for linkLine := range linkList {
		server, err := ParseSubscriptionLink(linkLine)
		if err != nil {
			continue
		}
		servers = append(servers, server)
	}
	if len(servers) == 0 {
		return nil, E.New("no servers found")
	}
	return servers, nil
}

func DecodeBase64URLSafe(content string) (string, error) {
	// Native JSON/YAML and share-link lists normally reveal themselves in the
	// prefix. Reject them before the normalizations and DecodeString's
	// document-sized allocation. Bound the scan so encoded subscriptions keep
	// the decoder's existing throughput and permissive normalization semantics.
	for i := 0; i < len(content) && i < 128; i++ {
		c := content[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		switch c {
		case '-', '_', '/', '+', '=', ' ', '\r', '\n':
			// The legacy decoder accepts CR/LF, maps spaces to '-', and
			// removes padding even in the middle of the input.
		default:
			return content, nil
		}
	}
	s := strings.ReplaceAll(content, " ", "-")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "+", "-")
	s = strings.ReplaceAll(s, "=", "")
	result, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return content, nil
	}
	return string(result), nil
}
