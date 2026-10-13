package wireguard

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing/service/pause"
)

// Diagnostic for the macOS release gate: what does the receive census actually see?
//
// The macOS runner reports `Should not be: -1` from measuredSocketsPerStandardBind, which means
// `socketCensus` returned 0 for a STARTED, SETTLED endpoint - the one reading that establishes
// `socketsPerStandardBind`. On Linux and Windows the same call returns 2. So before changing any
// assertion, this records what the census is looking at on whatever host runs it: the marker's
// presence in a live dump, the receiver address it derives, the device address it compares
// against, and the census it computes at each point.
//
// It is a report, not a gate: it never fails on a census value. It can only fail if the endpoint
// itself could not be brought up, which would make the report meaningless.

// dumpIncomingFrames prints every frame line mentioning the receiver, plus how many goroutines
// contain the marker at all - which separates "no such goroutine" from "found but not counted".
func dumpIncomingFrames(t *testing.T, label string) {
	t.Helper()
	buffer := make([]byte, 1<<22)
	buffer = buffer[:runtime.Stack(buffer, true)]
	all := string(buffer)

	withMarker := strings.Count(all, markerReceiveIncoming)
	withRoutine := strings.Count(all, "RoutineReceiveIncoming")
	t.Logf("%s: goroutines containing %q = %d; containing \"RoutineReceiveIncoming\" = %d",
		label, markerReceiveIncoming, withMarker, withRoutine)

	shown := 0
	for _, goroutine := range strings.Split(all, "\n\n") {
		if !strings.Contains(goroutine, "RoutineReceiveIncoming") {
			continue
		}
		shown++
		if shown > 3 {
			continue
		}
		lines := strings.Split(goroutine, "\n")
		for index, line := range lines {
			if index > 6 {
				break
			}
			t.Logf("    %s", line)
		}
		t.Logf("    ---")
	}
	if shown == 0 {
		t.Logf("    (no goroutine mentions RoutineReceiveIncoming at all)")
	}
}

// TestCensusDiagnostic_WhatTheReceiveCensusSees reports the census on a live endpoint, at the
// pause boundary, and after a wake, and dumps the frames behind each reading.
func TestCensusDiagnostic_WhatTheReceiveCensusSees(t *testing.T) {
	fixture := newRebindFixture(t, 0)
	fixture.proveStandardBindBranch(t)
	endpoint := fixture.endpoint

	// Established and settled. This is the reading that establishes socketsPerStandardBind.
	settled := socketCensus(t, endpoint)
	device := endpoint.device.Load()
	t.Logf("SETTLED census            = %d   (this is what measuredSocketsPerStandardBind must see)", settled)
	t.Logf("device pointer frame text = %q", endpointFrameAddress(device))
	t.Logf("deviceReceiveCensus       = %d", deviceReceiveCensus(device))
	t.Logf("goroutineCensus(recovery) = %d", goroutineCensus(t, endpoint))
	dumpIncomingFrames(t, "SETTLED")

	// The port is held, so the endpoint really is up: this separates a broken fixture from a
	// census that cannot see.
	held := fixture.livePort(t)
	t.Logf("port held by the endpoint = %d, free = %v", held, udpPortIsFree(t, held))

	// At the pause boundary.
	endpoint.onPauseUpdated(pause.EventNetworkPause)
	immediate := socketCensus(t, endpoint)
	t.Logf("PAUSE boundary census     = %d   (the immediate assertion reads this)", immediate)
	dumpIncomingFrames(t, "PAUSE")

	// Does it ever reach zero, and how long does it take? Bounded, and reported either way.
	start := time.Now()
	reached := time.Duration(-1)
	deadline := start.Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if socketCensus(t, endpoint) == 0 {
			reached = time.Since(start)
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Logf("PAUSE census reached zero = %v (negative means it never did within 30s)", reached)
	if reached < 0 {
		dumpIncomingFrames(t, "PAUSE-NEVER-ZERO")
	}

	// And after a wake.
	endpoint.onPauseUpdated(pause.EventNetworkWake)
	time.Sleep(500 * time.Millisecond)
	woken := socketCensus(t, endpoint)
	t.Logf("WAKE census               = %d   (socketsPerStandardBind is %d)", woken, socketsPerStandardBind)
	dumpIncomingFrames(t, "WAKE")

	// The report is the point; this only guards that the endpoint was genuinely up.
	if settled == 0 && immediate == 0 && woken == 0 {
		t.Logf("CENSUS IS BLIND ON THIS HOST: every reading was 0. The marker or the address "+
			"comparison does not match this platform's stack format, and socketsPerStandardBind "+
			"can never be established here. The frames above say which.")
	}
	fmt.Fprintf(discardWriter{}, "")
}

// discardWriter exists only so fmt stays referenced if the log lines above are edited away.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
