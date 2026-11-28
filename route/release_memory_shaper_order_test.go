package route

// E01B-ORDER, second path: the shaper release can close flows too.
//
// The closeout audit found that opening the reconnect window before reclaimAll was still not
// enough. Reclaim releases the traffic scheduler BEFORE it reclaims connections, and a parked flow
// whose write is released with ErrClosed closes both ends of its own copy - so with upload shaping
// configured, the first reconnects can arrive between the shaper release and the window that was
// supposed to cover them. The window now opens before both.
//
// The hook samples the governor at exactly that seam: after the scheduler release, before any
// connection is touched. Opening the window later - anywhere after this point - fails here even
// though every earlier test still passes.

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/trafficsched"
	"github.com/stretchr/testify/require"
)

func TestTheMemoryCrisisOpensTheWindowBeforeTheShaperRelease(t *testing.T) {
	connections := NewConnectionManager(nil)
	t.Cleanup(func() { _ = connections.Close() })

	// A real scheduler, so the production branch (policy.releaseFlows && m.scheduler != nil) runs.
	connections.scheduler = trafficsched.NewScheduler(trafficsched.Options{})

	require.False(t, connections.DialGovernorOpen())

	var sampled bool
	var openAtRelease bool
	previous := testHookAfterSchedulerRelease
	t.Cleanup(func() { testHookAfterSchedulerRelease = previous })
	testHookAfterSchedulerRelease = func() {
		openAtRelease = connections.DialGovernorOpen()
		sampled = true
	}

	connections.Reclaim(ReclaimMemoryPressure)
	_ = context.Background()

	require.True(t, sampled, "the fixture never reached the shaper release")
	require.True(t, openAtRelease,
		"the reconnect window was still shut after the shaper release: a parked flow released with "+
			"ErrClosed closes its own copy there, so the reconnects it provokes arrive ungoverned")

	// The drain keeps its own behaviour: it opens the window at the end of the pass, and it is not
	// this test's subject.
	require.Eventually(t, connections.DialGovernorOpen, time.Second, 10*time.Millisecond)
}
