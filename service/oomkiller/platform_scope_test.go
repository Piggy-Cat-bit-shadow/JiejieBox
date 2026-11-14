package oomkiller

import (
	"context"
	"strconv"
	"testing"

	"github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/byteformats"

	"github.com/stretchr/testify/require"
)

// The strict NetworkExtension budget must not leak to another platform.
//
// # What is being checked, precisely
//
// The admission guard is installed only when resolvePolicyMode returns policyModeNetworkExtension,
// and that mode requires BOTH the iOS build constant and a platform interface reporting an active
// Packet Tunnel. On a host build the first half is false by construction, so the executable check is
// that the resolver never returns the mode (and therefore never the 50 MiB budget) here - whatever
// the configuration asks for.
//
// This is not a substitute for a device: it proves the non-Apple path, and the Apple path is
// NEEDS_DEVICE.

func memoryLimitOption(t *testing.T, bytes int64) *byteformats.MemoryBytes {
	t.Helper()
	value := &byteformats.MemoryBytes{}
	require.NoError(t, value.UnmarshalJSON([]byte(strconv.FormatInt(bytes, 10))))
	return value
}

func TestResolvePolicyModeCannotSelectTheNetworkExtensionBudgetOffIOS(t *testing.T) {
	if constant.IsIos {
		t.Skip("this assertion is about host builds; an iOS test binary takes the other branch")
	}

	cases := []struct {
		name    string
		options option.OOMKillerServiceOptions
	}{
		{name: "default"},
		{name: "explicit-memory-limit", options: option.OOMKillerServiceOptions{MemoryLimit: memoryLimitOption(t, 64*1024*1024)}},
		{name: "override", options: option.OOMKillerServiceOptions{MemoryLimitOverride: 64 * 1024 * 1024}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			limit, mode := resolvePolicyMode(context.Background(), testCase.options)
			require.NotEqual(t, policyModeNetworkExtension, mode,
				"a host build selected the iOS NetworkExtension policy")
			require.NotEqual(t, uint64(DefaultAppleNetworkExtensionMemoryLimit), limit,
				"the 50 MiB observation budget reached a platform that does not enforce it")
		})
	}
}

// TestUnavailableFootprintRuleIsScopedToFootprintModes documents and pins the exact reach of the
// round-1 "unmeasurable is not safe" rule.
//
// It applies to the two modes that read the process footprint: the NetworkExtension policy and an
// explicit memory_limit. The available-memory mode is deliberately unchanged: when the platform
// cannot report free memory, the pre-existing behaviour (Normal) stays, because that mode's very
// selection already requires the API to exist and it does not police a process budget.
func TestUnavailableFootprintRuleIsScopedToFootprintModes(t *testing.T) {
	timer := &adaptiveTimer{}

	timer.policyMode = policyModeNetworkExtension
	timer.state = pressureStateNormal
	require.Equal(t, pressureStateArmed, timer.nextState(memorySample{footprintUnavailable: true}),
		"the NE policy must not report Normal on an unmeasurable footprint")

	timer.policyMode = policyModeMemoryLimit
	timer.state = pressureStateNormal
	require.Equal(t, pressureStateArmed, timer.nextState(memorySample{footprintUnavailable: true}),
		"an explicit memory_limit must not report Normal on an unmeasurable footprint")

	timer.policyMode = policyModeAvailable
	timer.state = pressureStateNormal
	require.Equal(t, pressureStateNormal,
		timer.nextState(memorySample{usage: 10 << 20, availableKnown: false}),
		"the available-memory mode's existing fail-open behaviour must not change in this round")
}

// TestAvailableMemoryModeStillUsesTheAvailableFigure keeps the mode's own contract honest after the
// footprint rule was added: a real available figure below the armed line still arms.
func TestAvailableMemoryModeStillUsesTheAvailableFigure(t *testing.T) {
	timer := &adaptiveTimer{
		timerConfig: timerConfig{policyMode: policyModeAvailable},
	}
	timer.state = pressureStateNormal

	// The thresholds derive from the sample's usage when no margin is configured.
	low := memorySample{usage: 100 << 20, available: 1 << 20, availableKnown: true}
	require.Equal(t, pressureStateTriggered, timer.nextState(low),
		"an exhausted available figure must still trigger in available mode")

	generous := memorySample{usage: 100 << 20, available: 1 << 30, availableKnown: true}
	timer.state = pressureStateNormal
	require.Equal(t, pressureStateNormal, timer.nextState(generous))
}
