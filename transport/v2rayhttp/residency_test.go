package v2rayhttp

// Q15: the transport's ledger counts POOL GENERATIONS, not streams and not conns.
//
// The audit's finding was that a stream, a session and a device connection were being added into one
// "physical connection" number. This transport's resident object is a generation, so the test pins
// the generation lifecycle: a current generation is Active while it carries a request and Idle when
// it does not, a retired generation is Draining (NOT released, NOT subtracted) until its last request
// returns, and only then is it counted in Terminated.

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/stretchr/testify/require"

	"golang.org/x/net/http2"
)

func TestHTTPGenerationResidencyLedger(t *testing.T) {
	t.Parallel()
	// No pool: the HTTP/1 shape. It answers with its own zero instead of an unknown.
	http1 := (&Client{}).ResidencyLedger()
	require.True(t, http1.Answered)
	require.True(t, http1.Exact)
	require.Zero(t, http1.Live())
	require.Contains(t, http1.Note, "HTTP/1")

	pool := NewGenerationPool(&http2.Transport{})
	client := &Client{pool: pool}
	idle := client.ResidencyLedger()
	require.True(t, idle.Answered)
	require.True(t, idle.Exact)
	require.Zero(t, idle.Active)
	require.EqualValues(t, 1, idle.Idle, "a fresh pool holds exactly one idle generation")
	require.EqualValues(t, 1, idle.Generation)
	require.Zero(t, idle.Draining)
	require.Zero(t, idle.RetiredGenerations)
	require.Zero(t, idle.Creating, "a generation is never observable half-built")

	active := pool.Begin()
	require.EqualValues(t, 1, client.ResidencyLedger().Active)
	require.Zero(t, client.ResidencyLedger().Idle)

	// The network moved while the request is running: the generation is RETIRED and becomes
	// Draining. It must not be subtracted before its last request returns.
	pool.Retire(&http2.Transport{})
	draining := client.ResidencyLedger()
	require.EqualValues(t, 2, draining.Generation)
	require.EqualValues(t, 1, draining.Draining, "the retired generation is draining, not gone")
	require.EqualValues(t, 1, draining.RetiredGenerations)
	require.Zero(t, draining.Terminated, "retirement is not a release")
	require.EqualValues(t, 1, draining.Idle, "the new generation is current and idle")

	pool.Finish(active)
	released := client.ResidencyLedger()
	require.Zero(t, released.Draining)
	require.Zero(t, released.RetiredGenerations)
	require.EqualValues(t, 1, released.Terminated, "the release is counted when it really happened")
}

// TestTheLedgerNeverCountsAStreamOrAConnAsAGeneration keeps the unit statement true: the ledger's Live
// is one per generation, however many requests are in flight on it.
func TestTheLedgerNeverCountsAStreamOrAConnAsAGeneration(t *testing.T) {
	t.Parallel()
	pool := NewGenerationPool(&http2.Transport{})
	client := &Client{pool: pool}
	first := pool.Begin()
	second := pool.Begin()
	ledger := client.ResidencyLedger()
	require.EqualValues(t, 1, ledger.Live(), "two requests on one generation are still one object")
	require.Equal(t, adapter.ResidencyUnitGeneration, ledger.Unit)
	pool.Finish(first)
	pool.Finish(second)
}
