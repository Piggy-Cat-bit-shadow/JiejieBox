package provider

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"

	"github.com/sagernet/sing-box/adapter/provider/nodestore"
)

// These tests are in package provider (not provider_test) so they can observe the record
// generation directly. They use the real outbound manager, so a replacement is the same
// prepare-park-publish path production uses.

type recordNodeOptions struct {
	option.DialerOptions
	Revision int
	Fail     bool
}

type recordNode struct {
	outbound.Adapter
	options recordNodeOptions
}

func (n *recordNode) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage == adapter.StartStateInitialize {
		scope.Add(func() error { return nil })
	}
	if stage == adapter.StartStateStart && n.options.Fail {
		return errors.New("failed candidate")
	}
	return nil
}

func (n *recordNode) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("not used by these tests")
}

func (*recordNode) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("unsupported")
}

type recordFixture struct {
	provider *Adapter
	manager  *outbound.Manager
	builds   *atomic.Int32
}

func newRecordFixture(t *testing.T) *recordFixture {
	t.Helper()
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	factory := log.NewNOPFactory()
	logger := factory.Logger()
	registry := outbound.NewRegistry()
	var builds atomic.Int32
	outbound.Register[recordNodeOptions](registry, "record", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options recordNodeOptions) (adapter.Outbound, error) {
		builds.Add(1)
		return &recordNode{Adapter: outbound.NewAdapter("record", tag, []string{"tcp"}, nil), options: options}, nil
	})
	endpointManager := endpoint.NewManager(endpoint.NewRegistry())
	manager := outbound.NewManager(registry, endpointManager, "")
	require.NoError(t, manager.Create(ctx, nil, logger, "static", "record", &recordNodeOptions{}))
	scope := adapter.NewScope(ctx, logger)
	t.Cleanup(func() { require.NoError(t, scope.Close()) })
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, scope.Start("endpoint", endpointManager, stage))
		require.NoError(t, scope.Start("outbound", manager, stage))
	}
	adapterInstance := NewAdapter(ctx, nil, manager, factory, logger, "subscription", "inline", option.ProviderHealthCheckOptions{})
	require.NoError(t, adapterInstance.Start())
	t.Cleanup(func() { require.NoError(t, adapterInstance.Close()) })
	return &recordFixture{provider: &adapterInstance, manager: manager, builds: &builds}
}

func recordNodes(revisions map[string]int, failing map[string]bool, detours map[string]string) []option.Outbound {
	nodes := make([]option.Outbound, 0, len(revisions))
	// Stable order so the resolved tags match the map keys.
	for _, tag := range []string{"alpha", "beta", "front"} {
		revision, present := revisions[tag]
		if !present {
			continue
		}
		nodes = append(nodes, option.Outbound{
			Type: "record",
			Tag:  tag,
			Options: &recordNodeOptions{
				DialerOptions: option.DialerOptions{Detour: detours[tag]},
				Revision:      revision,
				Fail:          failing[tag],
			},
		})
	}
	return nodes
}

func mustEncodeNode(t *testing.T, node option.Outbound) nodestore.Payload {
	t.Helper()
	payload, err := nodestore.Encode(node)
	require.NoError(t, err)
	return payload
}

// TestProviderRecordGenerationIsolation is the Store x same-tag replacement rule: an old reader's
// generation keeps returning the old configuration after the provider published a replacement, the
// new reader sees the new generation, and the old arena is released only when the old reader lets go.
func TestProviderRecordGenerationIsolation(t *testing.T) {
	fixture := newRecordFixture(t)
	first := recordNodes(map[string]int{"alpha": 1, "beta": 1}, nil, nil)
	require.NoError(t, fixture.provider.UpdateNodes(first))
	oldLease := fixture.provider.acquireSnapshotLease()
	require.NotNil(t, oldLease)
	oldGeneration := oldLease.Generation()
	oldAlpha, ok := oldLease.Payload("subscription/alpha")
	require.True(t, ok)
	require.Equal(t, mustEncodeNode(t, first[0]).Bytes, oldAlpha)
	oldBeta, ok := oldLease.Payload("subscription/beta")
	require.True(t, ok)

	second := recordNodes(map[string]int{"alpha": 2, "beta": 1}, nil, nil)
	require.NoError(t, fixture.provider.UpdateNodes(second))
	newGeneration := fixture.provider.storeGeneration()
	require.Greater(t, newGeneration, oldGeneration)

	newLease := fixture.provider.acquireSnapshotLease()
	require.NotNil(t, newLease)
	require.Equal(t, newGeneration, newLease.Generation())
	newAlpha, ok := newLease.Payload("subscription/alpha")
	require.True(t, ok)
	require.NotEqual(t, oldAlpha, newAlpha)
	require.Equal(t, mustEncodeNode(t, second[0]).Bytes, newAlpha)
	// The unchanged sibling is a distinct generation but the same configuration: its record bytes
	// must be identical, which is why the refresh can keep the live object.
	newBeta, ok := newLease.Payload("subscription/beta")
	require.True(t, ok)
	require.Equal(t, oldBeta, newBeta)

	// The old generation is retired but the lease keeps it readable, and it still holds the OLD
	// alpha - not the node that now sits at the same index in the new store.
	oldAlphaAfter, ok := oldLease.Payload("subscription/alpha")
	require.True(t, ok)
	require.Equal(t, oldAlpha, oldAlphaAfter)
	require.NotEqual(t, newAlpha, oldAlphaAfter)
	oldRef, ok := oldLease.Ref("subscription/alpha")
	require.True(t, ok)
	oldStore := oldRef.Store()
	require.False(t, oldStore.Freed())

	newLease.Close()
	oldLease.Close()
	require.True(t, oldStore.Freed(), "the retired generation must be released once its last lease is gone")
}

// TestProviderUnchangedRefreshDoesNotRebuild is the "unchanged configuration is not rebuilt" rule,
// now keyed on records: the same document must reuse every live object and still produce a fresh
// generation whose record bytes equal the previous ones.
func TestProviderUnchangedRefreshDoesNotRebuild(t *testing.T) {
	fixture := newRecordFixture(t)
	document := recordNodes(map[string]int{"alpha": 1, "beta": 1}, nil, nil)
	require.NoError(t, fixture.provider.UpdateNodes(document))
	buildsAfterFirst := fixture.builds.Load()
	require.EqualValues(t, 3, buildsAfterFirst, "static node plus two provider nodes")
	firstLease := fixture.provider.acquireSnapshotLease()
	require.NotNil(t, firstLease)
	firstAlpha, ok := firstLease.Payload("subscription/alpha")
	require.True(t, ok)
	firstLease.Close()
	alphaBefore, _ := fixture.provider.Outbound("subscription/alpha")

	require.NoError(t, fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": 1, "beta": 1}, nil, nil)))
	require.Equal(t, buildsAfterFirst, fixture.builds.Load(), "an unchanged document must not construct any node")
	alphaAfter, _ := fixture.provider.Outbound("subscription/alpha")
	require.Same(t, alphaBefore, alphaAfter)
	secondLease := fixture.provider.acquireSnapshotLease()
	require.NotNil(t, secondLease)
	secondAlpha, ok := secondLease.Payload("subscription/alpha")
	require.True(t, ok)
	require.Equal(t, firstAlpha, secondAlpha)
	secondLease.Close()
}

// TestProviderFailedReplacementKeepsOldRecord is the failed-publication rule: the node keeps its
// previous object and the snapshot must keep the configuration that produced that object, not the
// rejected document.
func TestProviderFailedReplacementKeepsOldRecord(t *testing.T) {
	fixture := newRecordFixture(t)
	first := recordNodes(map[string]int{"alpha": 1}, nil, nil)
	require.NoError(t, fixture.provider.UpdateNodes(first))
	alphaBefore, _ := fixture.provider.Outbound("subscription/alpha")
	oldPayload := mustEncodeNode(t, first[0]).Bytes

	// The second document asks for a different revision but its Start fails.
	err := fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": 2}, map[string]bool{"alpha": true}, nil))
	require.Error(t, err)
	alphaAfter, _ := fixture.provider.Outbound("subscription/alpha")
	require.Same(t, alphaBefore, alphaAfter, "a failed replacement must leave the previous object published")
	lease := fixture.provider.acquireSnapshotLease()
	require.NotNil(t, lease)
	payload, ok := lease.Payload("subscription/alpha")
	require.True(t, ok)
	require.Equal(t, oldPayload, payload, "the record must describe the object that is actually live")
	lease.Close()
}

// TestProviderDependentRebuildKeepsRecordsConsistent is the dependency-change rule: the dependent is
// rebuilt even though its own options are unchanged, and its record still describes its own
// (unchanged) configuration.
func TestProviderDependentRebuildKeepsRecordsConsistent(t *testing.T) {
	fixture := newRecordFixture(t)
	detours := map[string]string{"front": "subscription/alpha"}
	require.NoError(t, fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": 1, "front": 1}, nil, detours)))
	frontBefore, _ := fixture.provider.Outbound("subscription/front")
	lease := fixture.provider.acquireSnapshotLease()
	require.NotNil(t, lease)
	frontPayload, ok := lease.Payload("subscription/front")
	require.True(t, ok)
	lease.Close()

	require.NoError(t, fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": 2, "front": 1}, nil, detours)))
	frontAfter, _ := fixture.provider.Outbound("subscription/front")
	require.NotSame(t, frontBefore, frontAfter, "a dependent must be rebuilt when its member is replaced")
	lease = fixture.provider.acquireSnapshotLease()
	require.NotNil(t, lease)
	frontPayloadAfter, ok := lease.Payload("subscription/front")
	require.True(t, ok)
	require.Equal(t, frontPayload, frontPayloadAfter)
	lease.Close()
}

// TestProviderReleasesDecodedConfigurationTree is the ownership proof for step 8: after the apply,
// the decoded options value the provider was handed must be collectable. Before the record store,
// the appliedOutbounds map kept a shallow copy that pinned exactly this tree for the lifetime of the
// published generation; a finalizer that never runs would prove the old tree is still retained.
func TestProviderReleasesDecodedConfigurationTree(t *testing.T) {
	fixture := newRecordFixture(t)
	released := make(chan struct{}, 1)
	options := &recordNodeOptions{Revision: 1}
	runtime.SetFinalizer(options, func(*recordNodeOptions) {
		select {
		case released <- struct{}{}:
		default:
		}
	})
	document := []option.Outbound{{Type: "record", Tag: "alpha", Options: options}}
	require.NoError(t, fixture.provider.UpdateNodes(document))
	document = nil
	options = nil
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case <-released:
			return
		default:
		}
		runtime.GC()
		if time.Now().After(deadline) {
			t.Fatal("the decoded option tree is still referenced after the apply: the provider retained the configuration")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestProviderSnapshotConcurrentWithApply exercises lease/close and publication through the
// provider's real refresh path.
//
// # Why this test cannot pass by luck
//
// The independent review was right that the earlier version still depended on the scheduler: it
// only asserted that *some* read succeeded. This version has two independent obligations:
//
//  1. A deterministic lease is taken before an apply and held ACROSS it. Its generation must not
//     change, its payload read after the apply must not miss, and it must return the same bytes -
//     this is the "old generation keeps serving" rule with no scheduling involved.
//  2. Readers run concurrently with a series of applies. A non-nil lease whose Payload misses the
//     published tag is counted as a failure (acquire only returns nil after the provider closes,
//     which this test does at cleanup), every observed generation is recorded, and the test waits
//     (bounded, with Gosched, no sleeps) until at least two distinct generations were observed.
//     The start barrier and the iteration counter remain: a reader that never ran fails by itself.
func TestProviderSnapshotConcurrentWithApply(t *testing.T) {
	fixture := newRecordFixture(t)
	require.NoError(t, fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": 1, "beta": 1}, nil, nil)))
	firstGeneration := fixture.provider.storeGeneration()

	// Phase 1: a lease taken before the apply keeps its generation and its bytes across it.
	holder := fixture.provider.acquireSnapshotLease()
	require.NotNil(t, holder)
	require.Equal(t, firstGeneration, holder.Generation())
	heldPayload, heldOK := holder.Payload("subscription/alpha")
	require.True(t, heldOK, "the held lease must read its own generation")
	require.NoError(t, fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": 2, "beta": 1}, nil, nil)))
	secondGeneration := fixture.provider.storeGeneration()
	require.Greater(t, secondGeneration, firstGeneration, "the apply must publish a new generation")
	require.Equal(t, firstGeneration, holder.Generation(), "a held lease keeps the generation it observed")
	payloadAfterApply, payloadAfterOK := holder.Payload("subscription/alpha")
	require.True(t, payloadAfterOK, "a non-nil lease must never miss its payload after an apply")
	require.Equal(t, heldPayload, payloadAfterApply, "the held lease must still read the old configuration")
	holder.Close()

	// Phase 2: readers concurrent with applies.
	var (
		observedAccess sync.Mutex
		observed       = make(map[uint64]struct{})
	)
	recordGeneration := func(generation uint64) {
		observedAccess.Lock()
		observed[generation] = struct{}{}
		observedAccess.Unlock()
	}
	observedCount := func() int {
		observedAccess.Lock()
		defer observedAccess.Unlock()
		return len(observed)
	}

	stop := make(chan struct{})
	started := make(chan struct{}, 3)
	var readers sync.WaitGroup
	var reads atomic.Int64
	var misses atomic.Int64
	var iterations [3]atomic.Int64
	for reader := 0; reader < 3; reader++ {
		readers.Add(1)
		go func(reader int) {
			defer readers.Done()
			started <- struct{}{}
			for {
				select {
				case <-stop:
					return
				default:
				}
				iterations[reader].Add(1)
				lease := fixture.provider.acquireSnapshotLease()
				if lease == nil {
					// The provider is not closed during this phase, so a nil lease would mean the
					// published store disappeared. That is a failure, not a retry.
					misses.Add(1)
					continue
				}
				recordGeneration(lease.Generation())
				if _, ok := lease.Payload("subscription/alpha"); !ok {
					misses.Add(1)
				} else {
					reads.Add(1)
				}
				lease.Close()
			}
		}(reader)
	}
	// The start barrier: every reader announced it entered its loop. Wait for one completed
	// iteration per reader too, so at least one generation is observed by each before the applies
	// below and the "two generations" assertion cannot depend on one lucky goroutine.
	for reader := 0; reader < 3; reader++ {
		<-started
	}
	waitForCondition(t, func() bool {
		for reader := 0; reader < 3; reader++ {
			if iterations[reader].Load() == 0 {
				return false
			}
		}
		return true
	}, "the reader goroutines never completed an iteration before the applies")
	for revision := 3; revision < 30; revision++ {
		require.NoError(t, fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": revision, "beta": 1}, nil, nil)))
	}
	// Bounded wait, not a sleep: every reader already ran once, so the scheduler will run them
	// again; the assertion is that publication is observable, which a second generation proves.
	waitForCondition(t, func() bool {
		return observedCount() >= 2
	}, "the concurrent readers never observed two distinct generations")
	close(stop)
	readers.Wait()
	require.Zero(t, misses.Load(), "a non-nil lease must never miss a published payload")
	for reader := 0; reader < 3; reader++ {
		require.Greater(t, iterations[reader].Load(), int64(0), "reader %d never ran: the test would prove nothing", reader)
	}
	require.Greater(t, reads.Load(), int64(0), "no reader observed a published payload")
	require.GreaterOrEqual(t, observedCount(), 2, "at least two generations must have been observed")
}

// waitForCondition spins with Gosched (never sleeps) until condition holds or the deadline passes.
// It keeps the concurrency tests bounded without turning them into scheduler races.
func waitForCondition(t *testing.T, condition func() bool, failure string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(failure)
		}
		runtime.Gosched()
	}
}

// TestProviderCloseAfterLastReader is the shutdown ordering rule: closing the provider retires the
// snapshot, but a lease taken before the close keeps its generation readable until it is released.
func TestProviderCloseAfterLastReader(t *testing.T) {
	fixture := newRecordFixture(t)
	require.NoError(t, fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": 1}, nil, nil)))
	lease := fixture.provider.acquireSnapshotLease()
	require.NotNil(t, lease)
	ref, ok := lease.Ref("subscription/alpha")
	require.True(t, ok)
	store := ref.Store()
	require.NoError(t, fixture.provider.Close())
	require.Nil(t, fixture.provider.acquireSnapshotLease(), "a closed provider publishes no new reader")
	payload, ok := lease.Payload("subscription/alpha")
	require.True(t, ok, "the last legitimate reader keeps reading its generation after the provider closed")
	require.NotEmpty(t, payload)
	lease.Close()
	require.True(t, store.Freed())
}
