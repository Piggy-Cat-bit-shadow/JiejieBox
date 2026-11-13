//go:build with_quic

package trusttunnel

// The accepts that need HTTP/3 live in a with_quic file, following the repository's pattern for
// tag-dependent tests: without the tag, NewOutbound refuses `quic: true` with ErrQUICNotIncluded
// before any fallback question is asked, so the untagged suite must keep passing in that build.

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

// TestOutboundAcceptsQUICFallbackOptions covers both alphabets the fallback allows: the derived pair
// when the node leaves ALPN unset, and an explicit pair. What this file can prove is that the outbound
// accepts them and builds a client from them; the client-side wiring (the HTTP/3 leg offering [h3, h2]
// and the HTTP/2 leg getting its own [h2]) is pinned in transport/trusttunnel, where those fields live.
func TestOutboundAcceptsQUICFallbackOptions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		modify func(*option.TrustTunnelOutboundOptions)
	}{
		{
			name: "derived ALPN pair",
			modify: func(options *option.TrustTunnelOutboundOptions) {
				options.QUIC = true
				options.QUICFallbackToH2 = true
			},
		},
		{
			name: "explicit ALPN pair",
			modify: func(options *option.TrustTunnelOutboundOptions) {
				options.QUIC = true
				options.QUICFallbackToH2 = true
				options.TLS.ALPN = badoption.Listable[string]{"h3", "h2", "http/1.1"}
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			options := validOutboundOptions()
			testCase.modify(&options)
			created, err := NewOutbound(context.Background(), nil, log.NewNOPFactory().Logger(), "tt", options)
			require.NoError(t, err)
			outbound, isOutbound := created.(*Outbound)
			require.True(t, isOutbound)
			t.Cleanup(func() { outbound.Close() })
			require.NotNil(t, outbound.client, "the accepted options must produce a client, not only pass validation")
		})
	}
}
