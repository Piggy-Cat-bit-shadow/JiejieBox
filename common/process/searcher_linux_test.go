//go:build linux

package process

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// P04: one /proc walk per UID, bounded distinct walks, and no publication from
// a walk that was in flight when the cache was reset.
//
// The walk is replaced by a barrier-controlled function, so the tests assert
// the sharing and generation contracts directly instead of depending on a
// process table that changes underneath them.

func newScanTestSearcher(t *testing.T, scan func(uid uint32) (map[uint32][]string, error)) *linuxSearcher {
	t.Helper()
	created, err := NewSearcher(Config{Logger: log.NewNOPFactory().Logger()})
	require.NoError(t, err)
	searcher, isLinuxSearcher := created.(*linuxSearcher)
	require.True(t, isLinuxSearcher)
	searcher.scanPaths = scan
	t.Cleanup(func() { _ = searcher.Close() })
	return searcher
}

func TestSameUIDConcurrentLookupsShareOneScan(t *testing.T) {
	var scans atomic.Int32
	release := make(chan struct{})
	searcher := newScanTestSearcher(t, func(uid uint32) (map[uint32][]string, error) {
		scans.Add(1)
		<-release
		return map[uint32][]string{42: {"/usr/bin/one"}}, nil
	})

	const callers = 8
	results := make(chan []string, callers)
	for index := 0; index < callers; index++ {
		go func() {
			processPaths, err := searcher.scanProcessPaths(7)
			if err != nil {
				results <- nil
				return
			}
			results <- processPaths[42]
		}()
	}

	require.Eventually(t, func() bool { return scans.Load() == 1 }, 2*time.Second, time.Millisecond,
		"the first lookup did not start a scan")
	time.Sleep(50 * time.Millisecond)
	require.EqualValues(t, 1, scans.Load(),
		"concurrent lookups for one UID must share one /proc walk")

	close(release)
	for index := 0; index < callers; index++ {
		select {
		case paths := <-results:
			require.Equal(t, []string{"/usr/bin/one"}, paths)
		case <-time.After(5 * time.Second):
			t.Fatalf("caller %d stayed blocked after the shared scan finished", index)
		}
	}
}

func TestDistinctUIDScansAreBounded(t *testing.T) {
	var active, peak atomic.Int32
	release := make(chan struct{})
	searcher := newScanTestSearcher(t, func(uid uint32) (map[uint32][]string, error) {
		current := active.Add(1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		<-release
		active.Add(-1)
		return map[uint32][]string{}, nil
	})

	callers := processPathScanLimit + 3
	var waitGroup sync.WaitGroup
	waitGroup.Add(callers)
	for index := 0; index < callers; index++ {
		go func(uid uint32) {
			defer waitGroup.Done()
			_, _ = searcher.scanProcessPaths(uid)
		}(uint32(1000 + index))
	}

	require.Eventually(t, func() bool { return active.Load() == int32(processPathScanLimit) },
		2*time.Second, time.Millisecond, "the scan limit was never reached")
	time.Sleep(50 * time.Millisecond)
	require.LessOrEqual(t, peak.Load(), int32(processPathScanLimit),
		"distinct-UID /proc walks exceeded the in-flight limit")

	close(release)
	finished := make(chan struct{})
	go func() {
		waitGroup.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("a bounded scan stayed pending after the release")
	}
	require.EqualValues(t, 0, len(searcher.scanSlots),
		"every scan slot must be released")
}

func TestResetDeniesAnInFlightScanPublication(t *testing.T) {
	var scans atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	searcher := newScanTestSearcher(t, func(uid uint32) (map[uint32][]string, error) {
		scans.Add(1)
		entered <- struct{}{}
		<-release
		return map[uint32][]string{42: {"/usr/bin/one"}}, nil
	})

	result := make(chan error, 1)
	go func() {
		_, err := searcher.scanProcessPaths(7)
		result <- err
	}()
	<-entered

	searcher.ResetCache()
	close(release)
	require.NoError(t, <-result)

	_, cached := searcher.processPathCache.Get(7)
	require.False(t, cached,
		"a scan started before ResetCache must not republish its snapshot")

	// The next lookup must walk again, and that walk may publish.
	processPaths, err := searcher.scanProcessPaths(7)
	require.NoError(t, err)
	require.Equal(t, []string{"/usr/bin/one"}, processPaths[42])
	require.EqualValues(t, 2, scans.Load())
	require.Eventually(t, func() bool {
		_, found := searcher.processPathCache.Get(7)
		return found
	}, 2*time.Second, time.Millisecond, "a scan started after the reset must publish")
}

func TestMissIsCachedAndExpires(t *testing.T) {
	var scans atomic.Int32
	searcher := newScanTestSearcher(t, func(uid uint32) (map[uint32][]string, error) {
		scans.Add(1)
		return map[uint32][]string{}, nil
	})

	_, err := searcher.findProcessPaths(42, 7)
	require.Error(t, err)
	// One walk for the UID and one for all users.
	require.EqualValues(t, 2, scans.Load())

	_, err = searcher.findProcessPaths(42, 7)
	require.Error(t, err)
	require.EqualValues(t, 2, scans.Load(),
		"a tuple the last walk missed must not be walked again inside the miss window")

	time.Sleep(processMissLifetime + 100*time.Millisecond)
	_, err = searcher.findProcessPaths(42, 7)
	require.Error(t, err)
	require.EqualValues(t, 4, scans.Load(),
		"the miss must expire faster than the snapshot so a new socket is seen")
}

func TestResetPurgesTheMissCache(t *testing.T) {
	var scans atomic.Int32
	searcher := newScanTestSearcher(t, func(uid uint32) (map[uint32][]string, error) {
		scans.Add(1)
		return map[uint32][]string{}, nil
	})

	_, err := searcher.findProcessPaths(42, 7)
	require.Error(t, err)
	require.EqualValues(t, 2, scans.Load())

	searcher.ResetCache()
	_, err = searcher.findProcessPaths(42, 7)
	require.Error(t, err)
	require.EqualValues(t, 4, scans.Load(),
		"ResetCache must drop cached misses as well as snapshots")
}
