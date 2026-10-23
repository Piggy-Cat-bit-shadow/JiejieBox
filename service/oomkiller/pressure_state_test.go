package oomkiller

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The Triggered state must be left at the ARMED line, not at resume. At 33 MiB (above resume 30,
// below armed 40) the old transition stayed Triggered forever, which is the upsteam
// 9c2aa1bed32a defect: one reset pinned the killer in the crisis state.
func TestPressureStateExitsTriggeredAtArmedNotResume(t *testing.T) {
	triggered := pressureStateTriggered
	require.Equal(t, triggered, nextPressureState(triggered, false, true),
		"an armed footprint stays in the crisis state")
	require.Equal(t, pressureStateNormal, nextPressureState(triggered, false, false),
		"a footprint below armed must leave Triggered, even when it is above resume")
}

func TestPressureStateEntersAndArms(t *testing.T) {
	require.Equal(t, pressureStateTriggered, nextPressureState(pressureStateNormal, true, true))
	require.Equal(t, pressureStateArmed, nextPressureState(pressureStateNormal, false, true))
	require.Equal(t, pressureStateNormal, nextPressureState(pressureStateArmed, false, false))
}
