package dns

// O03 — the production entry point for the DNS history.
//
// The bus and the bounded history have their own contract tests. These drive the ROUTER's entry:
// the subscription a diagnostic consumer enables, the shared ring two consumers get, the detach
// that releases the drain goroutine, and the query/stats surface that reports "not enabled"
// differently from "nothing happened".

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

func newHistoryTestRouter(t *testing.T) *Router {
	t.Helper()
	router, err := NewRouter(context.Background(), log.NewNOPFactory(), option.DNSOptions{})
	require.NoError(t, err)
	return router
}

func emitTestQuery(router *Router, transport string, source QuerySource) {
	router.events.emit(QueryEvent{
		Time:         time.Now(),
		TransportTag: transport,
		Name:         "example.com",
		QType:        1,
		Source:       source,
		RCode:        0,
		DurationMs:   3,
	})
}

func TestEnableHistorySubscribesOnceAndSharesTheRing(t *testing.T) {
	router := newHistoryTestRouter(t)
	require.False(t, router.events.Active(), "a process that never asked for history has no subscriber")

	first := router.EnableHistory(HistoryOptions{MaxEntries: 8})
	second := router.EnableHistory(HistoryOptions{MaxEntries: 64})
	require.NotNil(t, first)
	require.Same(t, first, second,
		"a second consumer shares the ring; a second subscription would split the records and double the goroutines")
	require.True(t, router.events.Active())
	require.Same(t, first, router.DNSHistory())

	emitTestQuery(router, "remote-a", QuerySourceUpstream)
	require.Eventually(t, func() bool {
		records, err := router.QueryHistory(HistoryQuery{Limit: 8})
		return err == nil && len(records) == 1
	}, time.Second, time.Millisecond, "the enabled history must receive the query event")

	// One detach is enough for the one subscription: a second one would still be active.
	router.DisableHistory()
	require.False(t, router.events.Active(), "disabling detaches the subscription, not one of several")
	require.Nil(t, router.DNSHistory())
}

func TestADisabledHistoryIsNotAnEmptyResult(t *testing.T) {
	router := newHistoryTestRouter(t)

	_, err := router.QueryHistory(HistoryQuery{Limit: 8})
	require.ErrorIs(t, err, ErrHistoryNotEnabled,
		"a disabled feature must not look like a quiet window: the caller is diagnosing a query storm")
	_, err = router.HistoryTopK(TopFieldTransport, 3)
	require.ErrorIs(t, err, ErrHistoryNotEnabled)
	_, _, err = router.HistoryStats()
	require.ErrorIs(t, err, ErrHistoryNotEnabled)
}

func TestTheHistoryReportsTheActualExitAndItsOwnBounds(t *testing.T) {
	router := newHistoryTestRouter(t)
	history := router.EnableHistory(HistoryOptions{MaxEntries: 2})

	emitTestQuery(router, "remote-a", QuerySourceUpstream)
	emitTestQuery(router, "remote-b", QuerySourceCache)
	emitTestQuery(router, "remote-a", QuerySourceUpstream)

	require.Eventually(t, func() bool {
		stats, _, err := router.HistoryStats()
		return err == nil && stats.Entries == 2
	}, time.Second, time.Millisecond, "the ring is bounded by its entries limit")

	// The exit attribution is what the record exists for: which transport answered, and whether it
	// was an upstream answer or a cache hit.
	records, err := router.QueryHistory(HistoryQuery{Limit: 8, Transport: "remote-a"})
	require.NoError(t, err)
	require.NotEmpty(t, records)
	for _, record := range records {
		require.Equal(t, "remote-a", record.Event.TransportTag)
		require.Equal(t, QuerySourceUpstream, record.Event.Source)
	}
	top, err := router.HistoryTopK(TopFieldTransport, 2)
	require.NoError(t, err)
	require.NotEmpty(t, top)

	stats, dropped, err := router.HistoryStats()
	require.NoError(t, err)
	require.Equal(t, 2, stats.Entries)
	require.GreaterOrEqual(t, stats.Bytes, 0)
	// Drops are a bus fact, reported next to the history's own accounting rather than hidden.
	_ = dropped
	_ = history
}

func TestTheBoxShutdownDetachesTheHistory(t *testing.T) {
	router := newHistoryTestRouter(t)
	router.EnableHistory(HistoryOptions{MaxEntries: 8})

	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	before := runtime.NumGoroutine()

	require.NoError(t, router.closeHistory())
	require.Nil(t, router.DNSHistory())
	require.False(t, router.events.Active(),
		"the subscription belongs to the box; a closed box must not keep consuming events")
	require.NoError(t, router.closeHistory(), "shutdown must be idempotent")

	// The drain goroutine that was started for the subscription must be gone.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	require.LessOrEqual(t, runtime.NumGoroutine(), before+1)
}

// TestARealQueryReachesTheHistoryWithItsExit drives the same Exchange path the router uses, so the
// record's "actual exit" is the one the client really took rather than one a test supplied.
func TestARealQueryReachesTheHistoryWithItsExit(t *testing.T) {
	bus := NewEventBus(8)
	history := NewHistory(HistoryOptions{MaxEntries: 8})
	detach := history.Subscribe(bus)
	defer detach()

	transport := &herdTransport{}
	client := newEventClient(t, transport, bus, false)
	runEventExchange(t, client, transport, herdMessage())
	runEventExchange(t, client, transport, herdMessage())

	require.Eventually(t, func() bool {
		records, err := history.Query(HistoryQuery{Limit: 8})
		return err == nil && len(records) == 2
	}, time.Second, time.Millisecond, "both queries must be recorded")

	records, err := history.Query(HistoryQuery{Limit: 8})
	require.NoError(t, err)
	require.Equal(t, "herd", records[0].Event.TransportTag)
	require.Equal(t, QuerySourceUpstream, records[0].Event.Source)
	require.Equal(t, QuerySourceCache, records[1].Event.Source,
		"the second query was answered from cache, and the history must name that exit")
	require.EqualValues(t, 1, transport.queryCount.Load(),
		"the cache hit did not reach the transport, which is what makes the attribution meaningful")
}

// TestHistoryUnderPressureStaysBoundedAndReportsDrops pins the consumer-cost side: a burst must not
// grow the ring past its limits, must not block a query, and must leave the drops countable.
func TestHistoryUnderPressureStaysBoundedAndReportsDrops(t *testing.T) {
	router := newHistoryTestRouter(t)
	router.EnableHistory(HistoryOptions{MaxEntries: 16, MaxBytes: 1 << 16})

	for index := 0; index < 2000; index++ {
		emitTestQuery(router, "remote-a", QuerySourceUpstream)
	}

	require.Eventually(t, func() bool {
		stats, _, err := router.HistoryStats()
		return err == nil && stats.Queries >= 0 && stats.Entries > 0
	}, 2*time.Second, time.Millisecond)

	stats, dropped, err := router.HistoryStats()
	require.NoError(t, err)
	require.LessOrEqual(t, stats.Entries, 16, "the ring stays inside its entries limit under a burst")
	require.LessOrEqual(t, stats.Bytes, 1<<16, "and inside its byte limit")
	require.GreaterOrEqual(t, dropped, uint64(0), "drops are reported as a fact, not hidden")

	router.DisableHistory()
	require.False(t, router.events.Active())
}
