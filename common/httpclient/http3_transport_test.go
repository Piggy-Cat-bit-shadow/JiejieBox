//go:build with_quic

package httpclient

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func newVerdictTransport() *http3FallbackTransport {
	return &http3FallbackTransport{broken: make(map[string]http3BrokenEntry)}
}

func TestHTTP3BrokenAuthorityIsolation(t *testing.T) {
	transport := newVerdictTransport()

	transport.markH3Broken("a.example:443", transport.beginH3Attempt())
	if !transport.h3Broken("a.example:443") {
		t.Fatal("a.example:443 should be broken after mark")
	}
	if transport.h3Broken("b.example:443") {
		t.Fatal("b.example:443 must not be affected by marking a.example")
	}
}

// TestHTTP3BrokenBackoffPerAuthority pins the ladder: one episode is one rung.
//
// A burst of parallel requests on a dead path joins ONE dial inside the HTTP/3 transport, so their
// failures are observations of the same episode. Charging each of them multiplied the window by
// 2^N in one instant; this test requires the second failure of an open window to leave the rung
// alone, and the first failure after the window expires to advance it.
func TestHTTP3BrokenBackoffPerAuthority(t *testing.T) {
	transport := newVerdictTransport()

	transport.markH3Broken("a.example:443", transport.beginH3Attempt())
	if transport.broken["a.example:443"].backoff != 5*time.Minute {
		t.Fatalf("first episode should set backoff to 5m, got %v", transport.broken["a.example:443"].backoff)
	}
	armedUntil := transport.broken["a.example:443"].until

	// Same episode: the window is still open, so another failure must neither move the window
	// nor advance the ladder.
	transport.markH3Broken("a.example:443", transport.beginH3Attempt())
	if transport.broken["a.example:443"].backoff != 5*time.Minute {
		t.Fatalf("a failure inside the open window must not advance the ladder, got %v", transport.broken["a.example:443"].backoff)
	}
	if !transport.broken["a.example:443"].until.Equal(armedUntil) {
		t.Fatal("a failure inside the open window must not move its end")
	}

	// A new episode: the window is over, so the next failure doubles the rung.
	expireH3Window(transport, "a.example:443")
	transport.markH3Broken("a.example:443", transport.beginH3Attempt())
	if transport.broken["a.example:443"].backoff != 10*time.Minute {
		t.Fatalf("the first failure of a new episode should double to 10m, got %v", transport.broken["a.example:443"].backoff)
	}
	expireH3Window(transport, "a.example:443")
	transport.markH3Broken("a.example:443", transport.beginH3Attempt())
	if transport.broken["a.example:443"].backoff != 20*time.Minute {
		t.Fatalf("the next episode should double to 20m, got %v", transport.broken["a.example:443"].backoff)
	}

	if _, found := transport.broken["b.example:443"]; found {
		t.Fatal("marking a.example must not leak into b.example backoff state")
	}

	transport.markH3Broken("b.example:443", transport.beginH3Attempt())
	if transport.broken["b.example:443"].backoff != 5*time.Minute {
		t.Fatalf("b.example first mark should start at 5m independent of a.example, got %v", transport.broken["b.example:443"].backoff)
	}
}

func TestHTTP3BrokenBackoffCap(t *testing.T) {
	transport := newVerdictTransport()

	transport.broken["a.example:443"] = http3BrokenEntry{backoff: 48 * time.Hour, until: time.Now().Add(-time.Second)}
	transport.markH3Broken("a.example:443", transport.beginH3Attempt())
	if transport.broken["a.example:443"].backoff != 48*time.Hour {
		t.Fatalf("backoff must cap at 48h, got %v", transport.broken["a.example:443"].backoff)
	}
}

func TestHTTP3BrokenClearDisarmsWithoutForgettingTheOutcome(t *testing.T) {
	transport := newVerdictTransport()

	transport.markH3Broken("a.example:443", transport.beginH3Attempt())
	transport.markH3Broken("b.example:443", transport.beginH3Attempt())
	stale := transport.beginH3Attempt()
	cleared := transport.beginH3Attempt()
	transport.clearH3Broken("a.example:443", cleared)

	if transport.h3Broken("a.example:443") {
		t.Fatal("a success must disarm the authority's window")
	}
	if entry, found := transport.broken["a.example:443"]; !found || entry.backoff != 0 || !entry.until.IsZero() {
		t.Fatalf("the cleared entry must keep the success's outcome but no window/backoff, got %+v", entry)
	}
	if !transport.h3Broken("b.example:443") {
		t.Fatal("clearing a.example must not affect b.example")
	}

	// A failure that started BEFORE the success must not re-arm the authority, even though the
	// entry that records the success is still there.
	transport.markH3Broken("a.example:443", stale)
	if transport.h3Broken("a.example:443") {
		t.Fatal("a failure older than the success must not re-arm the window")
	}
}

// TestHTTP3BrokenExpiredEntryKeepsLadderMemory pins the other half of the ladder fix.
//
// The expired entry used to be deleted the first time anything asked whether the authority was
// broken. That made the ladder unreachable except through concurrent failures - every probe found
// no entry, so every episode restarted at five minutes. The entry stays now, carrying the rung;
// only staleness (h3BrokenPruneAge) forgets it.
func TestHTTP3BrokenExpiredEntryKeepsLadderMemory(t *testing.T) {
	transport := newVerdictTransport()

	transport.broken["a.example:443"] = http3BrokenEntry{
		backoff: 10 * time.Minute,
		until:   time.Now().Add(-time.Second),
	}
	if transport.h3Broken("a.example:443") {
		t.Fatal("an expired entry must report not broken")
	}
	if _, found := transport.broken["a.example:443"]; !found {
		t.Fatal("an expired entry must keep its rung: deleting it restarts the ladder at 5m")
	}
	assertNextEpisodeEscalates(t, transport)
}

func assertNextEpisodeEscalates(t *testing.T, transport *http3FallbackTransport) {
	t.Helper()
	transport.markH3Broken("a.example:443", transport.beginH3Attempt())
	if transport.broken["a.example:443"].backoff != 20*time.Minute {
		t.Fatalf("the episode after an expired window must advance the retained ladder, got %v", transport.broken["a.example:443"].backoff)
	}
}

func TestHTTP3BrokenEmptyAuthorityNoOp(t *testing.T) {
	transport := newVerdictTransport()

	transport.markH3Broken("", transport.beginH3Attempt())
	if len(transport.broken) != 0 {
		t.Fatalf("markH3Broken must ignore empty authority, got %d entries", len(transport.broken))
	}
	if transport.h3Broken("") {
		t.Fatal("h3Broken must return false for empty authority")
	}
	transport.clearH3Broken("", transport.beginH3Attempt())
}

// TestHTTP3BrokenConcurrentFailuresChargeOneStep is the reported defect: eleven concurrent results
// pushed one authority from five minutes to the 48-hour ceiling.
func TestHTTP3BrokenConcurrentFailuresChargeOneStep(t *testing.T) {
	transport := newVerdictTransport()
	attempts := make([]uint64, 16)
	for i := range attempts {
		attempts[i] = transport.beginH3Attempt()
	}
	start := make(chan struct{})
	var wait sync.WaitGroup
	for _, attempt := range attempts {
		wait.Add(1)
		go func(attempt uint64) {
			defer wait.Done()
			<-start
			transport.markH3Broken("a.example:443", attempt)
		}(attempt)
	}
	close(start)
	wait.Wait()

	if got := transport.broken["a.example:443"].backoff; got != 5*time.Minute {
		t.Fatalf("sixteen observations of one episode must charge one rung (5m), got %v", got)
	}
	if !transport.h3Broken("a.example:443") {
		t.Fatal("the episode must still arm the window")
	}
}

// TestHTTP3BrokenStaleFailureCannotArmAfterNewerSuccess pins the ordering rule: an attempt that
// started BEFORE a success describes a condition that success disproved.
func TestHTTP3BrokenStaleFailureCannotArmAfterNewerSuccess(t *testing.T) {
	transport := newVerdictTransport()

	old := transport.beginH3Attempt()
	newer := transport.beginH3Attempt()
	transport.clearH3Broken("a.example:443", newer)
	transport.markH3Broken("a.example:443", old)

	if transport.h3Broken("a.example:443") {
		t.Fatal("a failure from an older attempt must not arm the window after a newer success")
	}
	if entry := transport.broken["a.example:443"]; entry.backoff != 0 || !entry.until.IsZero() {
		t.Fatalf("and it must not move the cleared entry's window/backoff, got %+v", entry)
	}

	// The mirror: a stale success must not clear a newer failure.
	old = transport.beginH3Attempt()
	newer = transport.beginH3Attempt()
	transport.markH3Broken("a.example:443", newer)
	transport.clearH3Broken("a.example:443", old)
	if !transport.h3Broken("a.example:443") {
		t.Fatal("a success from an older attempt must not clear a newer failure")
	}
}

// TestHTTP3BrokenNewerSuccessResetsLadder proves the success really is a new episode boundary.
func TestHTTP3BrokenNewerSuccessResetsLadder(t *testing.T) {
	transport := newVerdictTransport()

	transport.markH3Broken("a.example:443", transport.beginH3Attempt())
	expireH3Window(transport, "a.example:443")
	transport.markH3Broken("a.example:443", transport.beginH3Attempt())
	if transport.broken["a.example:443"].backoff != 10*time.Minute {
		t.Fatalf("precondition: ladder should be at 10m, got %v", transport.broken["a.example:443"].backoff)
	}

	transport.clearH3Broken("a.example:443", transport.beginH3Attempt())
	transport.markH3Broken("a.example:443", transport.beginH3Attempt())
	if transport.broken["a.example:443"].backoff != 5*time.Minute {
		t.Fatalf("a success must reset the ladder to its first rung, got %v", transport.broken["a.example:443"].backoff)
	}
}

func TestHTTP3BrokenPrunesLongStaleEntries(t *testing.T) {
	transport := newVerdictTransport()
	transport.broken["old.example:443"] = http3BrokenEntry{
		backoff: 5 * time.Minute,
		until:   time.Now().Add(-h3BrokenPruneAge - time.Hour),
	}
	for i := 0; i < 80; i++ {
		transport.broken[fmt.Sprintf("filler%d.example:443", i)] = http3BrokenEntry{
			backoff: 5 * time.Minute,
			until:   time.Now().Add(time.Hour),
		}
	}
	transport.markH3Broken("a.example:443", transport.beginH3Attempt())
	if _, found := transport.broken["old.example:443"]; found {
		t.Fatal("an entry whose window expired more than a day ago must be forgotten")
	}
}

// expireH3Window rewinds an entry's window so the next failure is a new episode.
func expireH3Window(transport *http3FallbackTransport, authority string) {
	transport.verdictAccess.Lock()
	entry := transport.broken[authority]
	entry.until = time.Now().Add(-time.Millisecond)
	transport.broken[authority] = entry
	transport.verdictAccess.Unlock()
}
