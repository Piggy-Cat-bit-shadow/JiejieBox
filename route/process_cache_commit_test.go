package route

// P1.4 — the process cache's generation check and its write are ONE commit step.
//
// The cache had a generation: a lookup read it before the search and compared it again before
// adding, and a reset advanced it and purged. That closes most of the race but not the window
// between the second comparison and the write - a reset landing there advanced the generation,
// purged, and the very next instruction re-added the pre-reset answer the purge had just removed.
// The previous test only blocked INSIDE the searcher, which lands before that window.
//
// The fix holds a commit lock across the comparison and the write, and the reset takes the same
// lock around (inner reset, epoch advance, purge). The search stays outside the lock: a blocked
// /proc walk must not hold the boundary a network transition needs, and the tests below pin both
// halves - the window closed, and no lock held across the search.

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

// commitRecordingSearcher counts walks and can return an injected error.
type commitRecordingSearcher struct {
	calls atomic.Int32
	err   error
}

func (s *commitRecordingSearcher) FindProcessInfo(context.Context, string, netip.AddrPort, netip.AddrPort) (*adapter.ConnectionOwner, error) {
	s.calls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	return &adapter.ConnectionOwner{ProcessPaths: []string{"/usr/bin/curl"}}, nil
}

func (s *commitRecordingSearcher) ResetCache()  {}
func (s *commitRecordingSearcher) Close() error { return nil }

// TestAResetThatCompletesBeforeTheWriteCannotBeOutrunByTheWrite is the reviewed interleaving: the
// reset happens after the search has finished and before the entry is written.
func TestAResetThatCompletesBeforeTheWriteCannotBeOutrunByTheWrite(t *testing.T) {
	searcher := &commitRecordingSearcher{}
	router := newProcessCacheTestRouter(t, searcher)
	source := netip.MustParseAddrPort("127.0.0.1:31001")
	destination := netip.MustParseAddrPort("127.0.0.1:443")

	resetFinished := make(chan struct{})
	router.processCacheAboutToPublish = func() {
		// Only the first publication is intercepted; the follow-up lookup must be normal.
		router.processCacheAboutToPublish = nil
		go func() {
			router.resetProcessCaches()
			close(resetFinished)
		}()
		// Wait for the reset to COMPLETE, which is exactly the order the review reproduced: the
		// generation has advanced and the purge has run before the lookup reaches its write.
		select {
		case <-resetFinished:
		case <-time.After(5 * time.Second):
			t.Error("the reset never completed")
		}
	}

	owner, err := router.findProcessInfoCached(context.Background(), "tcp", source, destination, true)
	require.NoError(t, err)
	require.NotNil(t, owner,
		"the answer is still returned to the caller that asked for it - the cross-generation return is allowed")
	require.Zero(t, router.processCache.Len(),
		"a write that lost the generation race must not restore the entry the reset removed")

	// The next lookup asks the searcher again instead of being served the stale fact.
	_, err = router.findProcessInfoCached(context.Background(), "tcp", source, destination, true)
	require.NoError(t, err)
	require.EqualValues(t, 2, searcher.calls.Load())
}

// TestANewGenerationsLookupOfTheSameTupleIsCached is the other half: the guard must not disable the
// cache. A lookup that starts in the new generation is cached normally.
func TestANewGenerationsLookupOfTheSameTupleIsCached(t *testing.T) {
	searcher := &commitRecordingSearcher{}
	router := newProcessCacheTestRouter(t, searcher)
	source := netip.MustParseAddrPort("127.0.0.1:31002")
	destination := netip.MustParseAddrPort("127.0.0.1:443")

	_, err := router.findProcessInfoCached(context.Background(), "tcp", source, destination, true)
	require.NoError(t, err)
	require.EqualValues(t, 1, searcher.calls.Load())
	require.NotZero(t, router.processCache.Len())

	router.resetProcessCaches()
	require.Zero(t, router.processCache.Len())

	// Same tuple, new generation: measured again, then cached again.
	_, err = router.findProcessInfoCached(context.Background(), "tcp", source, destination, true)
	require.NoError(t, err)
	require.EqualValues(t, 2, searcher.calls.Load())
	_, err = router.findProcessInfoCached(context.Background(), "tcp", source, destination, true)
	require.NoError(t, err)
	require.EqualValues(t, 2, searcher.calls.Load(),
		"a lookup from the current generation is served from the cache")
}

// TestACancelledLookupThatCrossedAResetIsNotCached keeps the cancellation contract: the caller gets
// its cancellation, and the negative result does not become the next generation's cached fact.
func TestACancelledLookupThatCrossedAResetIsNotCached(t *testing.T) {
	searcher := &commitRecordingSearcher{err: context.Canceled}
	router := newProcessCacheTestRouter(t, searcher)
	source := netip.MustParseAddrPort("127.0.0.1:31003")
	destination := netip.MustParseAddrPort("127.0.0.1:443")

	resetFinished := make(chan struct{})
	router.processCacheAboutToPublish = func() {
		router.processCacheAboutToPublish = nil
		go func() {
			router.resetProcessCaches()
			close(resetFinished)
		}()
		<-resetFinished
	}

	owner, err := router.findProcessInfoCached(context.Background(), "tcp", source, destination, true)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, owner)
	require.Zero(t, router.processCache.Len(),
		"a cancellation that crossed a reset must not be cached as the new generation's answer")
}

// gatedCommitSearcher blocks inside the search until released.
type gatedCommitSearcher struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func newGatedCommitSearcher() *gatedCommitSearcher {
	return &gatedCommitSearcher{entered: make(chan struct{}), release: make(chan struct{})}
}

func (s *gatedCommitSearcher) FindProcessInfo(context.Context, string, netip.AddrPort, netip.AddrPort) (*adapter.ConnectionOwner, error) {
	s.calls.Add(1)
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return &adapter.ConnectionOwner{ProcessPaths: []string{"/usr/bin/curl"}}, nil
}

func (s *gatedCommitSearcher) ResetCache()  {}
func (s *gatedCommitSearcher) Close() error { return nil }

// TestAResetDoesNotWaitForASearchToFinish proves the lock's extent: the commit lock is not held
// across the search, so a network reset cannot be blocked behind a wedged process walk.
func TestAResetDoesNotWaitForASearchToFinish(t *testing.T) {
	searcher := newGatedCommitSearcher()
	router := newProcessCacheTestRouter(t, searcher)
	source := netip.MustParseAddrPort("127.0.0.1:31004")
	destination := netip.MustParseAddrPort("127.0.0.1:443")

	lookupDone := make(chan struct{})
	go func() {
		defer close(lookupDone)
		_, _ = router.findProcessInfoCached(context.Background(), "tcp", source, destination, true)
	}()
	select {
	case <-searcher.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the lookup never reached the searcher")
	}

	resetDone := make(chan struct{})
	go func() {
		router.resetProcessCaches()
		close(resetDone)
	}()
	select {
	case <-resetDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the reset waited for a search that was still blocked in the searcher: the commit lock must not span the search")
	}

	close(searcher.release)
	<-lookupDone
	require.Zero(t, router.processCache.Len(),
		"the pre-reset answer from that lookup must not be cached")
}

// TestAPermanentFailureIsNotRetriedByTheSameGeneration keeps the cheap-negative behaviour the cache
// exists for: within one generation a failure is remembered (and the reset drops it).
func TestAPermanentFailureIsNotRetriedByTheSameGeneration(t *testing.T) {
	searcher := &commitRecordingSearcher{err: errors.New("process lookup unavailable")}
	router := newProcessCacheTestRouter(t, searcher)
	source := netip.MustParseAddrPort("127.0.0.1:31005")
	destination := netip.MustParseAddrPort("127.0.0.1:443")

	_, err := router.findProcessInfoCached(context.Background(), "tcp", source, destination, true)
	require.Error(t, err)
	_, err = router.findProcessInfoCached(context.Background(), "tcp", source, destination, true)
	require.Error(t, err)
	require.EqualValues(t, 1, searcher.calls.Load(),
		"a failure within one generation is a cached negative result, as before")
}
