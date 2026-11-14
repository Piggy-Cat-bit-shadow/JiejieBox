package oomkiller

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"

	"github.com/stretchr/testify/require"
)

// An unmeasurable footprint is NOT an all-clear.
//
// # The failure these tests were written against
//
// `memory.Total()` is `phys_footprint` through `task_info(TASK_VM_INFO)` on Darwin, and the platform
// wrapper returns 0 when the call fails. `readMemorySample` then handed that 0 to the state machine as
// a real measurement. Zero is below every threshold, so the timer published MemoryPressureNone, the
// OOMKiller's reclaim path had nothing to act on, and common/resourceguard - which reads the same
// atomic - admitted expensive creations at the full LevelNormal limit.
//
// The result was the most dangerous shape a safety mechanism can have: the less the process knew
// about its own footprint, the more work it admitted. `policy.go` states the assumption that made it
// invisible ("phys_footprint ... is always available"); this file is the proof that it is not.
//
// # What "conservative and bounded" means here
//
// The timer must not claim the process is safe, and it must not manufacture a crisis either: a reset
// tears down live paths and needs evidence. So an unavailable sample holds the state at, or raises it
// to, the ARMED line - the level at which admission tightens and pools shrink but nothing is
// destroyed - and a crisis already in progress is not cleared by an unmeasurable sample.
//
// The tests drive the timer's decision surface directly, so they run on every platform and without a
// device.

// newUnmeasurableFootprintTimer builds the smallest timer that reaches poll's decision and then
// answers every sample with "the platform could not read the footprint".
func newUnmeasurableFootprintTimer(pressure *atomic.Uint32) *adaptiveTimer {
	config := timerConfig{
		policyMode:      policyModeNetworkExtension,
		minInterval:     time.Second,
		maxInterval:     10 * time.Second,
		memoryLimit:     50 * 1024 * 1024,
		safetyMargin:    5 * 1024 * 1024,
		hasSafetyMargin: true,
	}
	timer := &adaptiveTimer{
		timerConfig:     config,
		logger:          log.NewNOPFactory().Logger(),
		limitThresholds: computeLimitThresholds(config.memoryLimit, config.safetyMargin),
		timer:           time.NewTimer(time.Hour),
		pressure:        pressure,
	}
	timer.sampleFunc = func() memorySample { return memorySample{footprintUnavailable: true} }
	return timer
}

// TestUnmeasurableFootprintDoesNotPublishAllClear is the regression.
//
// RED before the fix: the zero footprint read as a real sample, nextState returned Normal, and the
// published pressure was MemoryPressureNone - which is exactly the "safe" claim the brief forbids a
// failed sample from making.
func TestUnmeasurableFootprintDoesNotPublishAllClear(t *testing.T) {
	pressure := &atomic.Uint32{}
	timer := newUnmeasurableFootprintTimer(pressure)
	t.Cleanup(func() { timer.timer.Stop() })

	timer.poll()

	require.NotEqual(t, uint32(tun.MemoryPressureNone), pressure.Load(),
		"an unmeasurable footprint was published as no pressure: admission would then run at the full "+
			"LevelNormal limit precisely because the process could not see its own memory")
	require.Equal(t, tun.MemoryPressureWarning, tun.MemoryPressure(pressure.Load()),
		"the unavailable sample belongs on the ARMED line: tighter admission and a smaller pool, but no "+
			"destructive reset that the measurement does not justify")
	require.Equal(t, pressureStateArmed, timer.state)
}

// TestUnmeasurableFootprintKeepsCrisis pins the other half: an unavailable sample must not clear a
// crisis the process already measured. A lost measurement is not evidence of recovery.
func TestUnmeasurableFootprintKeepsCrisis(t *testing.T) {
	pressure := &atomic.Uint32{}
	timer := newUnmeasurableFootprintTimer(pressure)
	t.Cleanup(func() { timer.timer.Stop() })
	timer.state = pressureStateTriggered

	require.Equal(t, pressureStateTriggered, timer.nextState(memorySample{footprintUnavailable: true}),
		"an unmeasurable sample cleared a measured crisis")
}

// TestUnmeasurableFootprintHoldsArmed keeps the state from oscillating between an absence of data and
// a warning: once armed for this reason, it stays armed until a real sample says otherwise.
func TestUnmeasurableFootprintHoldsArmed(t *testing.T) {
	timer := newUnmeasurableFootprintTimer(&atomic.Uint32{})
	t.Cleanup(func() { timer.timer.Stop() })
	timer.state = pressureStateArmed

	require.Equal(t, pressureStateArmed, timer.nextState(memorySample{footprintUnavailable: true}))
}

// TestFootprintZeroIsUnavailable pins the platform boundary: a live process cannot occupy zero bytes,
// so a zero read is a failed read. A real non-zero read stays a measurement.
func TestFootprintZeroIsUnavailable(t *testing.T) {
	unavailable := readMemorySampleFor(policyModeNetworkExtension, 0)
	require.True(t, unavailable.footprintUnavailable,
		"a zero footprint was accepted as a real measurement; the process would look safe")

	measured := readMemorySampleFor(policyModeNetworkExtension, 15*1024*1024)
	require.False(t, measured.footprintUnavailable)
	require.EqualValues(t, 15*1024*1024, measured.usage)
}

// TestTimelineReportsUnavailableFootprint is the report contract: a reader must be able to tell
// "0 bytes because the process was empty" (impossible) from "0 bytes because the read failed'.
func TestTimelineReportsUnavailableFootprint(t *testing.T) {
	unknown, err := json.Marshal(timelineRow{At: "t", State: "armed", FootprintUnavailable: true})
	require.NoError(t, err)
	require.Contains(t, string(unknown), `"footprintUnavailable":true`)

	known, err := json.Marshal(timelineRow{At: "t", State: "normal", MemoryBytes: 1024})
	require.NoError(t, err)
	require.NotContains(t, string(known), "footprintUnavailable",
		"a known footprint must not carry the flag: the report would then be ambiguous in the other "+
			"direction, which is what the availableKnown precedent exists to avoid")
}
