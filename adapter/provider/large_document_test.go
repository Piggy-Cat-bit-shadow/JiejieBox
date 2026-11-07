package provider_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/stretchr/testify/require"
)

// TestProviderLargeDocumentUpdate bounds the apply path: a real airport subscription is hundreds to
// a few thousand nodes, and the diff must stay linear enough that a refresh does not stall the
// outbound manager (every replacement takes its mutation lock) for seconds.
func TestProviderLargeDocumentUpdate(t *testing.T) {
	t.Parallel()
	const nodeCount = 1500
	fixture := newProviderFixture(t)
	options := make([]option.Outbound, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		options = append(options, option.Outbound{
			Type:    "test",
			Tag:     fmt.Sprintf("node-%04d", i),
			Options: &nodeOptions{Revision: 1},
		})
	}
	started := time.Now()
	require.NoError(t, fixture.provider.UpdateNodes(options))
	elapsed := time.Since(started)
	require.Len(t, fixture.provider.Outbounds(), nodeCount)
	require.Less(t, elapsed, 30*time.Second, "a %d node apply took %s", nodeCount, elapsed)
	t.Logf("applied %d nodes in %s", nodeCount, elapsed)

	// An unchanged re-apply must not rebuild anything: it is the common refresh (ETag 200 with the
	// same document) and must be cheap.
	before := fixture.provider.Outbounds()
	started = time.Now()
	require.NoError(t, fixture.provider.UpdateNodes(options))
	require.Same(t, before[0], fixture.provider.Outbounds()[0])
	t.Logf("re-applied %d unchanged nodes in %s", nodeCount, time.Since(started))

	// And dropping most of them must not leave stragglers.
	require.NoError(t, fixture.provider.UpdateNodes(options[:10]))
	require.Len(t, fixture.provider.Outbounds(), 10)
	require.Len(t, fixture.outbound.Outbounds(), 11) // 10 provider nodes plus the fixture's default
	_ = adapter.StartStateInitialize
}
