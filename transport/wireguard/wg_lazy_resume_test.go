package wireguard

// The release protocol keeps the stack and its flows while it destroys the wireguard-go Device. That
// split is only correct if a flow that STAYED in the stack can bring the chain back - otherwise the
// release turns every open connection into a permanent stall the moment the endpoint wakes.
//
// Independent review found exactly that: after SetIdle(true)/SetIdle(false), an established TCP flow
// timed out (20 s) unless the test opened ANOTHER connection first, because only a new Dial went
// through the rebuild path. These tests pin the repaired contract:
//
//	no traffic after the wake        -> the heavy chain stays released (the saving survives)
//	a retained flow's next packet    -> real demand, rebuilds the chain, the flow recovers
//	suspend/resume, repeatedly       -> the same flow keeps working, with no second connection
//
// The control case (lazyEligible=false) stays in the suite so a future change cannot "fix" this by
// quietly disabling the release.

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// awgObfuscationProfile is a full AWG profile: AWG shares this endpoint's device lifecycle, so the
// release/rebuild contract has to be proved on the obfuscated path too, not only on plain WG.
func awgObfuscationProfile() option.AmneziaWGOptions {
	return option.AmneziaWGOptions{
		Jc:                     4,
		Jmin:                   40,
		Jmax:                   70,
		S1:                     55,
		S2:                     42,
		S3:                     40,
		S4:                     12,
		H1:                     "1",
		H2:                     "2",
		H3:                     "3",
		H4:                     "4",
		HeaderProtectionKey:    testPrivateKey,
		ContentPaddingAddition: "10-100",
		RandomTrailers:         true,
		DisableCookies:         true,
	}
}

// lazyResumeCases is the pair of transports this contract is proved on.
func lazyResumeCases() []struct {
	name string
	awg  option.AmneziaWGOptions
} {
	return []struct {
		name string
		awg  option.AmneziaWGOptions
	}{
		{name: "wireguard"},
		{name: "amneziawg", awg: awgObfuscationProfile()},
	}
}

// TestARetainedFlowRebuildsAReleasedChainWithoutANewDial is the regression the review reproduced.
func TestARetainedFlowRebuildsAReleasedChainWithoutANewDial(t *testing.T) {
	for _, testCase := range lazyResumeCases() {
		t.Run(testCase.name, func(t *testing.T) {
			testRetainedFlowRebuildsChain(t, testCase.awg)
		})
	}
}

func testRetainedFlowRebuildsChain(t *testing.T, awg option.AmneziaWGOptions) {
	pair := lazyStartPair(t, awg)
	first := pair.dial(t)
	defer first.Close()
	pair.exchange(t, first, "before-idle", 10*time.Second)

	pair.client.SetIdle(true)
	published, released := pair.client.lazyState()
	require.False(t, published, "the idle path must release the heavy chain for this shape")
	require.True(t, released)

	pair.client.SetIdle(false)
	published, released = pair.client.lazyState()
	require.False(t, published,
		"waking must not spend the Device before any traffic: that would defeat the release")
	require.True(t, released)

	// The flow that stayed in the stack writes again. No second connection is created: this is the
	// demand that must rebuild the chain.
	pair.exchange(t, first, "after-wake", 20*time.Second)

	published, released = pair.client.lazyState()
	require.True(t, published, "the retained flow's traffic must have rebuilt the heavy chain")
	require.False(t, released)
}

// TestAWakeWithoutTrafficDoesNotRebuildTheChain is the saving half, stated as its own test so a fix
// for the case above cannot pass by rebuilding eagerly on every wake.
func TestAWakeWithoutTrafficDoesNotRebuildTheChain(t *testing.T) {
	for _, testCase := range lazyResumeCases() {
		t.Run(testCase.name, func(t *testing.T) {
			testWakeWithoutTrafficKeepsTheChainReleased(t, testCase.awg)
		})
	}
}

func testWakeWithoutTrafficKeepsTheChainReleased(t *testing.T, awg option.AmneziaWGOptions) {
	pair := lazyStartPair(t, awg)
	first := pair.dial(t)
	defer first.Close()
	pair.exchange(t, first, "before-idle", 10*time.Second)

	for round := 0; round < 3; round++ {
		pair.client.SetIdle(true)
		require.True(t, func() bool { _, isReleased := pair.client.lazyState(); return isReleased }(),
			"round %d: the chain must be released while idle", round)
		pair.client.SetIdle(false)
		published, released := pair.client.lazyState()
		require.False(t, published, "round %d: a wake with no traffic must not rebuild", round)
		require.True(t, released, "round %d: the chain must still be released", round)
	}
}

// TestAReleasedChainSurvivesRepeatedSuspendResume keeps the retained flow usable across several
// pause/wake cycles, so the rebuild is not a one-shot recovery.
func TestAReleasedChainSurvivesRepeatedSuspendResume(t *testing.T) {
	for _, testCase := range lazyResumeCases() {
		t.Run(testCase.name, func(t *testing.T) {
			testRepeatedSuspendResume(t, testCase.awg)
		})
	}
}

func testRepeatedSuspendResume(t *testing.T, awg option.AmneziaWGOptions) {
	pair := lazyStartPair(t, awg)
	first := pair.dial(t)
	defer first.Close()
	pair.exchange(t, first, "round-0", 10*time.Second)

	for round := 1; round <= 3; round++ {
		pair.client.SetIdle(true)
		pair.client.SetIdle(false)
		pair.exchange(t, first, "round-"+string(rune('0'+round)), 20*time.Second)
	}
}

// TestTheLazyReleaseControlStillResumesTheRetainedFlow is the reviewer's control: with the release
// switched off, the pre-existing Down/Up behaviour resumes the flow. It must keep passing, so the
// comparison stays available and the release cannot be "fixed" by removing it.
func TestTheLazyReleaseControlStillResumesTheRetainedFlow(t *testing.T) {
	pair := lazyStartPair(t, option.AmneziaWGOptions{})
	pair.client.lazyEligible = false
	first := pair.dial(t)
	defer first.Close()
	pair.exchange(t, first, "before-idle", 10*time.Second)

	pair.client.SetIdle(true)
	published, _ := pair.client.lazyState()
	require.True(t, published, "the control keeps the Device across the pause")

	pair.client.SetIdle(false)
	pair.exchange(t, first, "after-wake", 10*time.Second)
}
