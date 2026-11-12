package v2raygrpclite

// Q15: this transport's ledger counts POOL GENERATIONS, not streams and not conns - the same unit
// protocol/v2rayhttp reports, because it is the same pool mechanism.
//
// The test pins the generation lifecycle: the current generation is Active while it carries a stream
// and Idle when it does not, a retired generation is Draining (NOT released, NOT subtracted) until
// its last stream returns, and only then is it counted in Terminated.

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/transport/v2rayhttp"
	"github.com/stretchr/testify/require"

	"golang.org/x/net/http2"
)

func TestGRPCGenerationResidencyLedger(t *testing.T) {
	t.Parallel()
	unbuilt := (&Client{}).ResidencyLedger()
	require.False(t, unbuilt.Answered, "a transport with no pool must not report a precise zero")
	require.Zero(t, unbuilt.Live())

	pool := v2rayhttp.NewGenerationPool(&http2.Transport{})
	client := &Client{pool: pool}
	idle := client.ResidencyLedger()
	require.True(t, idle.Answered)
	require.True(t, idle.Exact)
	require.Equal(t, adapter.ResidencyUnitGeneration, idle.Unit)
	require.EqualValues(t, 1, idle.Idle)
	require.EqualValues(t, 1, idle.Generation)

	active := pool.Begin()
	require.EqualValues(t, 1, client.ResidencyLedger().Active)

	pool.Retire(&http2.Transport{})
	draining := client.ResidencyLedger()
	require.EqualValues(t, 1, draining.Draining, "a retired generation is draining, not gone")
	require.EqualValues(t, 1, draining.RetiredGenerations)
	require.EqualValues(t, 2, draining.Generation)
	require.Zero(t, draining.Terminated, "retirement is not a release")

	pool.Finish(active)
	released := client.ResidencyLedger()
	require.Zero(t, released.Draining)
	require.Zero(t, released.RetiredGenerations)
	require.EqualValues(t, 1, released.Terminated)
	require.EqualValues(t, 1, released.Live(), "one generation is still held")
}

func TestTheGRPCLedgerCountsOneObjectPerGeneration(t *testing.T) {
	t.Parallel()
	pool := v2rayhttp.NewGenerationPool(&http2.Transport{})
	client := &Client{pool: pool}
	first := pool.Begin()
	second := pool.Begin()
	require.EqualValues(t, 1, client.ResidencyLedger().Live(),
		"two streams on one generation are still one resident object")
	pool.Finish(first)
	pool.Finish(second)
}
