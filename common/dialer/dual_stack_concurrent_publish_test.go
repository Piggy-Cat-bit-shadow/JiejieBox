package dialer

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Regression tests for the concurrent-publisher crash in literal recovery.
//
// # The defect
//
// adapter.DNSDualStackRouter requires publish to be safe from different goroutines, and the
// production router honours that requirement: LookupFamilies issues A and AAAA on two goroutines
// and publishes each family's answer from that family's goroutine. The recovery consumer, however,
// assumed "the lookup goroutine" was one goroutine and let both callbacks run deliver() against an
// unsynchronized seen map. When both families answered together, the writes collided and the Go
// runtime killed the Network Extension with "concurrent map writes".
//
// These tests drive that exact shape - two publishers released at the same instant, each delivering
// a whole family answer - and pin the three properties the fix must preserve: both families are
// still published, a canonical address is published only when it carries new information, and
// cancellation still unblocks a publisher that is waiting on the stream.
//
// The unsynchronized version is detected by `go test -race` on the map accesses themselves. Without
// the race detector the same run may instead die with the runtime's concurrent-map-write fatal
// error, which is the production failure; both are failures of the same test.

// concurrentFamilyPublisher is a DNSDualStackRouter whose two family answers are produced at the
// same instant, from two goroutines, exactly as dns.Router.LookupFamilies does.
//
// It is deliberately single-use: the start barrier and the completion signal are closed once, so a
// test that reused it would be asserting on a lookup the barrier never covered.
type concurrentFamilyPublisher struct {
	adapter.DNSRouter

	resultsA    []adapter.DNSFamilyResult
	resultsAAAA []adapter.DNSFamilyResult

	// release is closed by the test once both family goroutines have arrived. Both publishers
	// then enter publish together, which is the interleaving this file exists to exercise.
	release chan struct{}
	// started closes when both family goroutines are parked at the barrier. It makes the test
	// wait for the concurrency instead of for the wall clock.
	started chan struct{}
	// done closes when both family goroutines have returned from every publish call, so a test
	// can prove cancellation unblocked them rather than merely unblocking the stream.
	done chan struct{}

	arrived sync.WaitGroup
}

func newConcurrentFamilyPublisher(resultsA, resultsAAAA []adapter.DNSFamilyResult) *concurrentFamilyPublisher {
	return &concurrentFamilyPublisher{
		resultsA:    resultsA,
		resultsAAAA: resultsAAAA,
		release:     make(chan struct{}),
		started:     make(chan struct{}),
		done:        make(chan struct{}),
	}
}

func (r *concurrentFamilyPublisher) LookupFamilies(ctx context.Context, domain string, options adapter.DNSQueryOptions, publish func(adapter.DNSFamilyResult)) error {
	var publishers sync.WaitGroup
	r.arrived.Add(2)
	publishers.Add(2)
	publishFamily := func(results []adapter.DNSFamilyResult) {
		defer publishers.Done()
		r.arrived.Done()
		<-r.release
		for _, result := range results {
			if ctx.Err() != nil {
				return
			}
			publish(result)
		}
	}
	go publishFamily(r.resultsA)
	go publishFamily(r.resultsAAAA)
	r.arrived.Wait()
	close(r.started)
	publishers.Wait()
	close(r.done)
	return nil
}

// startConcurrentRecovery starts the production recovery stream over the concurrent publisher and
// releases both family goroutines. The caller owns stream.stop().
func startConcurrentRecovery(t *testing.T, router *concurrentFamilyPublisher, original netip.Addr) recoveryStream {
	t.Helper()
	destination := M.SocksaddrFrom(original, 443)
	ctx := adapter.WithContext(context.Background(), &adapter.InboundContext{
		Destination: destination,
		Domain:      "concurrent-recovery.example.test",
	})
	dialer := &resolveDialer{router: router}
	stream := dialer.streamRecoveredCandidates(ctx, destination, dialer.captureAdmissionPolicy())
	require.True(t, stream.ready(), "the dual-stack router must select the streaming recovery path")
	select {
	case <-router.started:
	case <-time.After(5 * time.Second):
		stream.stop()
		t.Fatal("both family publishers did not reach the start barrier")
	}
	close(router.release)
	return stream
}

// collectBatches drains the recovery stream and fails rather than hanging if it never closes.
func collectBatches(t *testing.T, stream recoveryStream) [][]dualStackCandidate {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	var batches [][]dualStackCandidate
	for {
		select {
		case batch, open := <-stream.batches:
			if !open {
				return batches
			}
			batches = append(batches, batch)
		case <-timer.C:
			t.Fatalf("the recovery stream did not close after both families published; collected %d batches", len(batches))
		}
	}
}

// familyFillers returns count distinct addresses of the requested family, enough for each family's
// callback to do real work inside its critical section.
func familyFillers(ipv6 bool, count int) []netip.Addr {
	addresses := make([]netip.Addr, 0, count)
	for index := 1; index <= count; index++ {
		if ipv6 {
			addresses = append(addresses, netip.AddrFrom16([16]byte{
				0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(index),
			}))
		} else {
			addresses = append(addresses, netip.AddrFrom4([4]byte{192, 0, 2, byte(index)}))
		}
	}
	return addresses
}

// TestRecoverySurvivesConcurrentFamilyPublish is the direct regression for the crash.
//
// Both families are released together and each publishes a large answer, so the two deliver() calls
// are inside the identity set at the same time. The address shared by the two answers is supplied
// as an IPv4 literal in the A answer and as the same address in IPv4-mapped IPv6 form in the AAAA
// answer: unmapAddress canonicalizes both to one identity, so this also proves that deduplication
// still works across concurrently published families.
func TestRecoverySurvivesConcurrentFamilyPublish(t *testing.T) {
	const perFamily = 128
	const generation = uint64(4)

	original := netip.MustParseAddr("2001:db8:ffff::2c")
	shared := netip.MustParseAddr("198.51.100.7")
	sharedMapped := netip.MustParseAddr("::ffff:198.51.100.7")

	addressesA := append(familyFillers(false, perFamily), shared)
	addressesAAAA := append(familyFillers(true, perFamily), sharedMapped)

	router := newConcurrentFamilyPublisher(
		[]adapter.DNSFamilyResult{{
			IPv6:                false,
			Addresses:           addressesA,
			SourceGeneration:    generation,
			HasSourceGeneration: true,
		}},
		[]adapter.DNSFamilyResult{{
			IPv6:                true,
			Addresses:           addressesAAAA,
			SourceGeneration:    generation,
			HasSourceGeneration: true,
		}},
	)
	stream := startConcurrentRecovery(t, router, original)
	defer stream.stop()

	batches := collectBatches(t, stream)
	require.Len(t, batches, 2, "one batch per family, and each answer carries new addresses")

	published := make(map[netip.Addr]int)
	families := make(map[addressFamily]int)
	for _, batch := range batches {
		require.NotEmpty(t, batch)
		for _, candidate := range batch {
			published[candidate.address]++
			families[candidate.family]++
			require.True(t, candidate.sourceKnown, "the generation supplied by the router must be kept")
			require.Equal(t, generation, candidate.sourceGeneration)
		}
	}

	require.Len(t, published, perFamily*2+1,
		"both families' distinct addresses arrive, and the canonical duplicate counts once")
	for address, count := range published {
		require.Equal(t, 1, count, "address %v was published %d times across concurrent families", address, count)
	}
	require.Equal(t, 1, published[shared],
		"the IPv4 literal and its IPv4-mapped form are one identity and must be published once")
	require.NotContains(t, published, original, "the caller's own literal must never be re-fed")
	require.Equal(t, perFamily+1, families[familyIPv4],
		"the IPv4 answer plus the canonical shared address")
	require.Equal(t, perFamily, families[familyIPv6],
		"the IPv6 answer contributes its own addresses only")
}

// TestRecoveryConcurrentPublishKeepsTheNewestSourceGeneration pins the provenance rule under the
// same concurrency: whichever family publishes the shared address first, the identity set must end
// on the newest source, and a newer source may only upgrade an address - never be lost behind it.
func TestRecoveryConcurrentPublishKeepsTheNewestSourceGeneration(t *testing.T) {
	const perFamily = 64

	original := netip.MustParseAddr("2001:db8:ffff::99")
	shared := netip.MustParseAddr("198.51.100.9")
	sharedMapped := netip.MustParseAddr("::ffff:198.51.100.9")

	addressesA := append(familyFillers(false, perFamily), shared)
	addressesAAAA := append(familyFillers(true, perFamily), sharedMapped)

	router := newConcurrentFamilyPublisher(
		[]adapter.DNSFamilyResult{{
			IPv6:                false,
			Addresses:           addressesA,
			SourceGeneration:    5,
			HasSourceGeneration: true,
		}},
		[]adapter.DNSFamilyResult{{
			IPv6:                true,
			Addresses:           addressesAAAA,
			SourceGeneration:    9,
			HasSourceGeneration: true,
		}},
	)
	stream := startConcurrentRecovery(t, router, original)
	defer stream.stop()

	batches := collectBatches(t, stream)

	var sharedCandidates []dualStackCandidate
	for _, batch := range batches {
		for _, candidate := range batch {
			if candidate.address == shared {
				sharedCandidates = append(sharedCandidates, candidate)
			}
		}
	}
	require.NotEmpty(t, sharedCandidates, "the address both families named must reach the consumer")
	require.LessOrEqual(t, len(sharedCandidates), 2,
		"at most the older source and its upgrade may be published")

	last := sharedCandidates[len(sharedCandidates)-1]
	require.True(t, last.sourceKnown)
	require.Equal(t, uint64(9), last.sourceGeneration,
		"the identity set must be left on the newest source, not on whichever write ran last")
	if len(sharedCandidates) == 2 {
		require.Equal(t, uint64(5), sharedCandidates[0].sourceGeneration,
			"the only legal two-publication sequence is the older source followed by its upgrade")
	}
}

// TestRecoveryCancellationReleasesConcurrentPublishers proves the lock scope does not turn
// cancellation into a wait. The consumer takes one batch and stops reading; with both channel
// buffers full, family publishers are parked inside deliver() holding the identity-set lock. stop()
// must still cancel and return, and the parked callbacks must observe the cancellation and exit.
func TestRecoveryCancellationReleasesConcurrentPublishers(t *testing.T) {
	const batchesPerFamily = 8
	original := netip.MustParseAddr("2001:db8:ffff::77")

	resultsA := make([]adapter.DNSFamilyResult, 0, batchesPerFamily)
	resultsAAAA := make([]adapter.DNSFamilyResult, 0, batchesPerFamily)
	for index := 1; index <= batchesPerFamily; index++ {
		resultsA = append(resultsA, adapter.DNSFamilyResult{
			IPv6:                false,
			Addresses:           []netip.Addr{netip.AddrFrom4([4]byte{198, 18, 0, byte(index)})},
			SourceGeneration:    1,
			HasSourceGeneration: true,
		})
		resultsAAAA = append(resultsAAAA, adapter.DNSFamilyResult{
			IPv6: true,
			Addresses: []netip.Addr{netip.AddrFrom16([16]byte{
				0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, byte(index),
			})},
			SourceGeneration:    1,
			HasSourceGeneration: true,
		})
	}

	router := newConcurrentFamilyPublisher(resultsA, resultsAAAA)
	stream := startConcurrentRecovery(t, router, original)
	stopCalled := false
	defer func() {
		if !stopCalled {
			stream.stop()
		}
	}()

	select {
	case batch := <-stream.batches:
		require.NotEmpty(t, batch)
	case <-time.After(5 * time.Second):
		t.Fatal("no batch arrived from the concurrent publisher")
	}

	stopCalled = true
	stopReturned := make(chan struct{})
	go func() {
		stream.stop()
		close(stopReturned)
	}()
	select {
	case <-stopReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return: cancellation is blocked behind a callback holding the identity-set lock")
	}

	select {
	case <-router.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the family publishers remained parked after cancellation; the locked send was not released")
	}
}
