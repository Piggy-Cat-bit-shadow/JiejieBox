package provider

// Close meeting a BATCH apply: the manager publishes the whole segment, then the provider's terminal
// phase gate refuses it. The reclaim path must remove and close every candidate of that segment
// exactly once, and must release the previous generation the batch retired.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProviderBatchRefusedByCloseReclaimsEveryCandidate(t *testing.T) {
	fixture := newRecordFixture(t)
	require.NoError(t, fixture.provider.UpdateNodes(recordNodes(
		map[string]int{"alpha": 1, "beta": 1, "front": 1}, nil, nil)))

	installed := make(chan string, 1)
	release := make(chan struct{})
	fixture.provider.testAfterNodeInstall = func(tag string) {
		select {
		case installed <- tag:
		default:
		}
		<-release
	}
	updateDone := make(chan error, 1)
	go func() {
		updateDone <- fixture.provider.UpdateNodes(recordNodes(
			map[string]int{"alpha": 2, "beta": 2, "front": 2}, nil, nil))
	}()
	select {
	case tag := <-installed:
		require.NotEmpty(t, tag)
	case <-time.After(10 * time.Second):
		t.Fatal("the batch apply never reached its per-node install barrier")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- fixture.provider.Close() }()
	waitForCondition(t, func() bool {
		return fixture.provider.phaseState() != providerPhaseOpen
	}, "Close did not publish its closing phase while the batch apply was parked")
	close(release)

	require.Error(t, waitForUpdate(t, updateDone),
		"a batch refused at the publish point reports the refusal")
	require.NoError(t, waitForClose(t, closeDone))
	require.Nil(t, fixture.provider.currentSnapshot())
	for _, tag := range []string{"subscription/alpha", "subscription/beta", "subscription/front"} {
		_, found := fixture.manager.Outbound(tag)
		require.False(t, found, "a refused batch must not leave %s published", tag)
	}
	// Three previous objects (rev1, parked as retired by the batch) and three provisional
	// candidates (rev2, installed by the manager and reclaimed by the refusal): each closed once.
	// A double release would make this 7+, a leak would make it less.
	require.EqualValues(t, 6, fixture.closes.Load())
}
