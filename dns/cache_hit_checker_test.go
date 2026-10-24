package dns

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"

	mDNS "github.com/miekg/dns"

	"github.com/stretchr/testify/require"
)

// A cache hit must still pass the address-filter checker. The response was accepted when it
// was cached, but the rule set can change while the entry is alive; returning it without the
// checker makes that policy update invisible for the rest of the TTL.
func TestCacheHitRunsTheResponseChecker(t *testing.T) {
	transport := &herdTransport{}
	client := NewClient(ClientOptions{
		Context: context.Background(),
		Logger:  log.NewNOPFactory().Logger(),
	})
	message := herdMessage()
	checker := func(*mDNS.Msg) bool { return true }

	first, err := client.Exchange(context.Background(), transport, message, adapter.DNSQueryOptions{}, checker)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.EqualValues(t, 1, transport.queryCount.Load(), "the first lookup must query upstream")

	// The policy changes before the entry expires. The cached answer must be rejected rather
	// than served, and it must not turn into an upstream query either.
	rejected, err := client.Exchange(context.Background(), transport, message, adapter.DNSQueryOptions{}, func(*mDNS.Msg) bool { return false })
	require.ErrorIs(t, err, ErrResponseRejectedCached)
	require.Nil(t, rejected)
	require.EqualValues(t, 1, transport.queryCount.Load(), "a rejected cache hit must not query upstream")

	// A checker that accepts again sees the same cached entry: the rejection did not evict it.
	accepted, err := client.Exchange(context.Background(), transport, message, adapter.DNSQueryOptions{}, checker)
	require.NoError(t, err)
	require.NotNil(t, accepted)
	require.EqualValues(t, 1, transport.queryCount.Load(), "the accepted cache hit must not query upstream")
}
