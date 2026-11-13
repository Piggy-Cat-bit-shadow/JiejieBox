package urltest

import (
	"sync"
	"testing"
)

// TestFailoverStateConcurrentSnapshot pins the state machine's reader/writer boundary: a health
// round mutates the policy while a diagnostics sampler may call the read-only snapshot methods.
// Without the state lock, `go test -race` reports the stable/widened/field accesses.
func TestFailoverStateConcurrentSnapshot(t *testing.T) {
	state := NewFailoverState(FailoverPolicy{
		WidenAfter:   1,
		SettleAfter:  1,
		RecoverAfter: 1,
	}, "a")
	members := []string{"a", "b"}
	observations := map[string]FailoverObservation{
		"a": ok(10),
		"b": ok(20),
	}

	const iterations = 1000
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	go func() {
		defer waitGroup.Done()
		for index := 0; index < iterations; index++ {
			state.Decide(members, observations)
		}
	}()
	go func() {
		defer waitGroup.Done()
		for index := 0; index < iterations; index++ {
			_ = state.Stable()
			_ = state.Widened()
			_ = state.Probes(members)
		}
	}()
	waitGroup.Wait()
}
