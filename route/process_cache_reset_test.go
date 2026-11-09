package route

// Section 5 — the process cache's reset interleaving.
//
// A reset (network change) purges the cache because every tuple's answer belongs to the previous
// network generation. A lookup that was ALREADY running when the purge happened used to add its
// pre-reset answer afterwards, restoring exactly the fact the purge removed. The cache now carries a
// generation: an answer produced across a reset is returned to its caller but not cached.
//
// The second case is the field distinction known to the cache key: an owner-only answer (no path)
// must not satisfy a lookup that needs paths.

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/process"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/contrab/freelru"
	"github.com/sagernet/sing/contrab/maphash"

	"github.com/stretchr/testify/require"
)

// gatedProcessSearcher blocks the first walk until release, counting walks.
type gatedProcessSearcher struct {
	entered   chan struct{}
	release   chan struct{}
	once      sync.Once
	walks     atomic.Int32
	resetSeen atomic.Int32
}

func newGatedProcessSearcher() *gatedProcessSearcher {
	return &gatedProcessSearcher{entered: make(chan struct{}), release: make(chan struct{})}
}

func (s *gatedProcessSearcher) FindProcessInfo(ctx context.Context, network string, source netip.AddrPort, destination netip.AddrPort) (*adapter.ConnectionOwner, error) {
	walk := s.walks.Add(1)
	if walk == 1 {
		s.once.Do(func() { close(s.entered) })
		<-s.release
	}
	return &adapter.ConnectionOwner{ProcessPaths: []string{"/usr/bin/curl"}}, nil
}

func (s *gatedProcessSearcher) ResetCache()  { s.resetSeen.Add(1) }
func (s *gatedProcessSearcher) Close() error { return nil }

func newProcessCacheTestRouter(t *testing.T, searcher process.Searcher) *Router {
	t.Helper()
	cache := common.Must1(freelru.New[processCacheKey, processCacheEntry](256, maphash.NewHasher[processCacheKey]().Hash32, true))
	cache.SetLifetime(200 * time.Millisecond)
	t.Cleanup(cache.Purge)
	return &Router{processCache: cache, processSearcher: searcher}
}

// TestAnAnswerThatCrossedAResetIsNotCached is the interleaving.
func TestAnAnswerThatCrossedAResetIsNotCached(t *testing.T) {
	searcher := newGatedProcessSearcher()
	router := newProcessCacheTestRouter(t, searcher)
	source := netip.MustParseAddrPort("127.0.0.1:12345")
	destination := netip.MustParseAddrPort("127.0.0.1:443")

	type outcome struct {
		owner *adapter.ConnectionOwner
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		owner, err := router.findProcessInfoCached(context.Background(), "tcp", source, destination, true)
		done <- outcome{owner: owner, err: err}
	}()

	select {
	case <-searcher.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the lookup never reached the searcher")
	}

	// The network generation changes WHILE the lookup runs.
	router.resetProcessCaches()
	require.EqualValues(t, 1, searcher.resetSeen.Load(), "the reset must reach the searcher too")

	close(searcher.release)
	result := <-done
	require.NoError(t, result.err)
	require.NotNil(t, result.owner)
	require.Zero(t, router.processCache.Len(),
		"an answer produced across a reset must not be cached: it belongs to the previous generation")

	// The next lookup asks the searcher again instead of being served the stale fact.
	_, err := router.findProcessInfoCached(context.Background(), "tcp", source, destination, true)
	require.NoError(t, err)
	require.EqualValues(t, 2, searcher.walks.Load())
}

// TestTheCacheKeyKeepsThePathFieldDistinct: an owner-only answer cannot satisfy a path lookup.
func TestTheCacheKeyKeepsThePathFieldDistinct(t *testing.T) {
	searcher := &countingProcessSearcher{}
	router := newProcessCacheTestRouter(t, searcher)
	source := netip.MustParseAddrPort("127.0.0.1:23456")
	destination := netip.MustParseAddrPort("127.0.0.1:443")

	owner, err := router.findProcessInfoCached(context.Background(), "tcp", source, destination, false)
	require.NoError(t, err)
	require.NotNil(t, owner)
	require.EqualValues(t, 1, searcher.calls.Load())

	withPaths, err := router.findProcessInfoCached(context.Background(), "tcp", source, destination, true)
	require.NoError(t, err)
	require.NotNil(t, withPaths)
	require.EqualValues(t, 2, searcher.calls.Load(),
		"a path lookup must not be answered by the owner-only entry")

	// And the same lookup again IS served from the cache.
	_, err = router.findProcessInfoCached(context.Background(), "tcp", source, destination, true)
	require.NoError(t, err)
	require.EqualValues(t, 2, searcher.calls.Load())
}

type countingProcessSearcher struct {
	calls atomic.Int32
}

func (s *countingProcessSearcher) FindProcessInfo(context.Context, string, netip.AddrPort, netip.AddrPort) (*adapter.ConnectionOwner, error) {
	s.calls.Add(1)
	return &adapter.ConnectionOwner{ProcessPaths: []string{"/usr/bin/curl"}}, nil
}

func (s *countingProcessSearcher) ResetCache()  {}
func (s *countingProcessSearcher) Close() error { return nil }
