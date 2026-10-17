package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/power"
	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// The dashboard updater must obey the power lifecycle.
//
// # The gap this closes
//
// `loopUpdate` fetches a zip on a timer that fires whether or not anybody is looking, and until this
// gate existed it consulted no power state: a device that had gone to sleep still woke the radio to
// download files that would not be read until it woke anyway. Of the two network workers in the core
// that answered to no power state at all, this was the second - the optimistic DNS refresh was the
// first, and it is gated the same way.
//
// # What must not change
//
// The FIRST update. On a fresh profile the dashboard directory is empty, so the panel is blank until
// that fetch lands; that is what the operator is waiting for, and deferring it to a wake would be a
// regression dressed as a saving. `loopUpdate` tracks that with its own `initial` flag, and
// TestTheFirstDashboardUpdateIsNotDeferred pins it.

// dashboardProbeTransport answers every request without touching the network and counts them.
//
// A 304 is the status that exercises the most of `fetch` for the least fixture: the real code path
// runs, the etag bookkeeping is written, and nothing is unzipped.
type dashboardProbeTransport struct {
	requests atomic.Int32
}

func (t *dashboardProbeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.requests.Add(1)
	return &http.Response{
		StatusCode: http.StatusNotModified,
		Header:     http.Header{"Etag": []string{"probe-etag"}},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    request,
	}, nil
}

func newDashboardProbe(t *testing.T, governor *power.Governor, interval time.Duration) (*dashboard, *dashboardProbeTransport) {
	t.Helper()
	transport := &dashboardProbeTransport{}
	instance := &dashboard{
		ctx:            context.Background(),
		logger:         log.NewNOPFactory().NewLogger("dashboard-probe"),
		path:           t.TempDir(),
		url:            "http://dashboard.test/archive.zip",
		updateInterval: interval,
		httpClient:     &http.Client{Transport: transport},
		powerGovernor:  governor,
	}
	return instance, transport
}

// dashboardGatePolicy is DefaultPolicy with a fast wake stagger: the stagger values are the only
// thing changed, and they have to be - DefaultPolicy spreads a wake over fifteen seconds, which is
// right for a phone and wrong for a test.
func dashboardGatePolicy() power.Policy {
	policy := power.DefaultPolicy()
	policy.WakeStagger = power.WakeStagger{ProviderRefresh: 20 * time.Millisecond}
	return policy
}

// runDashboardLoop drives the real loop for the duration of the test.
func runDashboardLoop(t *testing.T, instance *dashboard) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		instance.loopUpdate(ctx, dashboardManaged)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the dashboard update loop did not exit after its context was cancelled")
		}
	})
}

// TestTheDashboardUpdateLoopRunsWhileTheDeviceIsActive is the CONTROL.
//
// Without it the suppression test below proves nothing: "the fetch count stopped growing" is also
// what a loop that died, or a fixture whose interval was long enough to hide the fetch, reports.
func TestTheDashboardUpdateLoopRunsWhileTheDeviceIsActive(t *testing.T) {
	instance, transport := newDashboardProbe(t, nil, 20*time.Millisecond)
	require.Nil(t, instance.powerGovernor, "no governor was installed, so none may be reported")

	runDashboardLoop(t, instance)

	require.Eventually(t, func() bool { return transport.requests.Load() >= 3 },
		10*time.Second, 5*time.Millisecond,
		"with no governor the loop must keep updating at its interval. This is the control: if it "+
			"fails, the suppression test measures a loop that never ran")
}

// TestTheFirstDashboardUpdateIsNotDeferred pins the half that must NOT be gated.
func TestTheFirstDashboardUpdateIsNotDeferred(t *testing.T) {
	governor := power.NewGovernor(dashboardGatePolicy())
	t.Cleanup(governor.Close)
	governor.DevicePaused()

	instance, transport := newDashboardProbe(t, governor, time.Hour)
	runDashboardLoop(t, instance)

	require.Eventually(t, func() bool { return transport.requests.Load() >= 1 },
		10*time.Second, 5*time.Millisecond,
		"the FIRST update was deferred. On a fresh profile the dashboard directory is empty and "+
			"this fetch is what fills the panel the operator is looking at, so gating it would "+
			"leave a blank page until the device happens to wake")
}

// TestTheDashboardUpdateIsSuppressedWhileTheDeviceIsPaused is the property.
func TestTheDashboardUpdateIsSuppressedWhileTheDeviceIsPaused(t *testing.T) {
	governor := power.NewGovernor(dashboardGatePolicy())
	t.Cleanup(governor.Close)

	instance, transport := newDashboardProbe(t, governor, 20*time.Millisecond)
	require.Same(t, governor, instance.powerGovernor)

	runDashboardLoop(t, instance)

	// The first update is not deferred, so it lands before the device is told to sleep.
	require.Eventually(t, func() bool { return transport.requests.Load() >= 2 },
		10*time.Second, 5*time.Millisecond,
		"the loop must be updating before the device is paused, or the window below is measured "+
			"against a loop that never started")

	governor.DevicePaused()
	require.False(t, governor.Active())

	// The window: several intervals long, so a loop that kept updating would land several fetches
	// in it. The control above proves this same fixture fetches at this cadence, which is what
	// makes "the count did not move" a measurement rather than an absence of evidence.
	requestsWhilePaused := transport.requests.Load()
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, requestsWhilePaused, transport.requests.Load(),
		"the dashboard updater fetched while the device was asleep. The panel keeps serving the "+
			"copy on disk, so this woke the radio for files that would not be read until the device "+
			"woke anyway")

	// The gate must reopen, or it is an outage rather than a saving.
	governor.DeviceWake()
	require.Eventually(t, func() bool { return transport.requests.Load() > requestsWhilePaused },
		10*time.Second, 5*time.Millisecond,
		"the dashboard updater never resumed after the wake. Suppressing a speculative download "+
			"during sleep is a saving only if it comes back when the device does")
}
