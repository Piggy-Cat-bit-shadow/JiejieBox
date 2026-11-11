package v2rayxhttp

// Stage-2 coupling test: a dial that races (or follows) Close must not create a
// pooled connection that no later Close can reach.
//
// Close emptied the pool and closed the transport it owned. get() then saw an
// empty pool and opened a replacement, which nothing owned afterwards: the
// lifecycle had already run. The invariant is not "no error" - failing is the
// correct outcome - it is "no connection was created after the pool closed".

import (
	"context"
	"sync"
	"testing"
)

func TestXmuxDoesNotOpenAConnectionAfterClose(t *testing.T) {
	manager, opened := poolOf(t, xmuxConfig{maxConcurrency: intRange{4, 4}})
	manager.Close()

	client, wait := manager.get()
	if client != nil || wait != 0 {
		t.Fatalf("get after Close returned client=%v wait=%v, want the closed-pool signal", client, wait)
	}
	if _, err := manager.getContext(context.Background()); err == nil {
		t.Fatal("getContext after Close returned no error")
	}
	if conns := opened(); len(conns) != 0 {
		t.Fatalf("%d connection(s) were opened after Close", len(conns))
	}
}

func TestXmuxCloseRacingDialsLeavesNoConnectionUnclosed(t *testing.T) {
	manager, opened := poolOf(t, xmuxConfig{maxConcurrency: intRange{4, 4}})
	var wg sync.WaitGroup
	for index := 0; index < 8; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 32; attempt++ {
				manager.get()
			}
		}()
	}
	manager.Close()
	wg.Wait()
	// A get that won the race may have appended its connection before Close took the lock; Close
	// snapshots the pool under that lock, so every connection opened is closed exactly once.
	for index, conn := range opened() {
		if closes := conn.closes(); closes != 1 {
			t.Fatalf("connection %d closed %d time(s), want exactly 1", index, closes)
		}
	}
	// And after everything settled, the pool stays closed.
	if client, _ := manager.get(); client != nil {
		t.Fatal("the pool re-opened after Close settled")
	}
}
