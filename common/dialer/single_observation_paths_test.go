package dialer

import (
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
)

// One operation, one observation of the network - on the two paths that broke the rule without a test
// noticing.
//
// # The rule
//
// A dial or a packet socket creates something on a specific network generation. The decision to allow it
// (the family gate) and the check that it may still be handed over (ownership) must come from ONE
// observation: `captureLocalFamilyPolicy` exists to produce exactly that, and reads epoch, settled state
// and capability from a single call.
//
// Two places did not follow it, and neither was visible to the existing suite because every manager it
// builds reports ALL of the optional interfaces at once. A source that is silently ignored looks exactly
// like a source that agrees.

// TestSnapshotOnlyManagerIsReadThroughTheSnapshot guards the resolution's precedence.
//
// It used to guard `captureEpoch`'s early return, which keyed on `d.networkEpoch == nil` - ONE of the
// three optional sources the ownership fields could be built from. A manager that reported the
// transition snapshot and NOT the reset counter took that early return, the snapshot it DID report was
// never consulted, and the predicate answered "owned" unconditionally. That is the same defect
// `captureLocalFamilyPolicy` was fixed for, in the same shape, in the same round.
//
// With one resolution the shape cannot recur: the observer takes the strongest interface the manager
// implements, and both the capture and the predicate read it. What is asserted here is therefore the
// RESOLUTION (the dialer is reading the pair interface, and its predicate honours it) and the three
// orderings a dial can be in - because the resolution only matters if the answer is right.
func TestSnapshotOnlyManagerIsReadThroughTheSnapshot(t *testing.T) {
	// DURING: the publisher's unstable window. Nothing may be handed over.
	during := &pairSnapshotOnlyManager{}
	during.epoch.Store(5)
	during.stable.Store(false)
	duringOwner := &DefaultDialer{network: newNetworkObserver(during)}
	require.Equal(t, sourceTransitionPair, duringOwner.network.source,
		"a manager that reports the transition snapshot must be read through it")
	require.True(t, duringOwner.network.reportsStability,
		"and the settled flag it reported must be usable, or the DURING case below cannot be rejected")
	predicate := duringOwner.captureEpoch()
	require.False(t, predicate(),
		"a dial that began while the network was unsettled must not be handed over, and the ONLY source "+
			"of that fact this manager reports is the snapshot it was given")

	// STALE: settled at epoch 5, then the network moves to 6.
	stale := &pairSnapshotOnlyManager{}
	stale.epoch.Store(5)
	stale.stable.Store(true)
	staleOwner := &DefaultDialer{network: newNetworkObserver(stale)}
	stalePredicate := staleOwner.captureEpoch()
	require.True(t, stalePredicate(), "a settled, unmoved generation may be handed over")
	stale.epoch.Store(6)
	require.False(t, stalePredicate(), "once the epoch moves, the captured one is superseded")
}

// pairSnapshotOnlyManager reports the transition snapshot and NOTHING narrower.
//
// It is the shape the defect needed: no reset counter, no settled getter, so a consumer that resolves
// its source from those two interfaces alone sees nothing at all. The resolution has to notice that the
// snapshotter is present.
type pairSnapshotOnlyManager struct {
	adapter.NetworkManager
	epoch  atomic.Uint64
	stable atomic.Bool
}

func (m *pairSnapshotOnlyManager) NetworkTransitionSnapshot() (uint64, bool) {
	return m.epoch.Load(), m.stable.Load()
}

func (m *pairSnapshotOnlyManager) InterfaceFinder() control.InterfaceFinder {
	return control.NewDefaultInterfaceFinder()
}

var (
	_ adapter.NetworkTransitionSnapshotter = (*pairSnapshotOnlyManager)(nil)
	_                                      = func() {
		var manager any = &pairSnapshotOnlyManager{}
		if _, isCounter := manager.(adapter.NetworkResetCounter); isCounter {
			panic("the double must not implement NetworkResetCounter")
		}
		if _, isState := manager.(adapter.NetworkTransitionState); isState {
			panic("the double must not implement NetworkTransitionState")
		}
	}
)

// TestCaptureEpochWithNoSourceAtAllStillAbsolves is the control that keeps the fix from becoming "always
// refuse".
//
// A manager that reports no snapshot and no getters constrains nothing, which is the degradation every
// other optional capability here takes: the dialer keeps its previous behaviour rather than losing a
// connection because a struct was left empty.
func TestCaptureEpochWithNoSourceAtAllStillAbsolves(t *testing.T) {
	bare := &DefaultDialer{}
	require.True(t, bare.captureEpoch()(),
		"a manager that can report nothing must not turn into a manager that refuses everything")
}

// TestPacketEntryPointGatesOnItsOwnCapture is the single-observation claim on the packet path.
//
// # The defect
//
// `ListenSerialInterfacePacket` used to take TWO captures: `captureEpoch` (the transition snapshot) at its
// entry for the hand-over check, and `captureLocalFamilyPolicy` (the family snapshot) inside for its gate.
// Two calls, two optional interfaces, a window in between - so the gate could decide on an observation the
// operation never claimed.
//
// The harm was bounded: the hand-over check still validated the first capture, so no connection produced
// under a divergent gate could be handed over. But the gate could HARD-DENY an operation whose own
// ownership capture said "not owned", and the direction the design requires there is fail open.
//
// # How this is asserted without a socket
//
// The gate runs before any socket work, so "refused" proves the gate fired and "not refused" proves it did
// not. The policy under test is built by hand, which is the only way to make the two captures disagree on
// demand - and it is the shape the fix guarantees can no longer occur, because there is now one capture.
func TestOnePolicyServesThePacketGateAndThePacketHandOver(t *testing.T) {
	// An UNOWNED policy: what a capture whose ownership has been superseded evaluates to, and the capture
	// the operation would be holding when the two-capture gate decided on a different observation.
	//
	// `newLocalFamilyPolicy` is owned by construction (the right default for a policy with no network
	// lifecycle to bind to), so the superseded shape is built explicitly rather than via that helper.
	unowned := newLocalFamilyPolicy(netcap.FamilyCapability{
		IPv4:             netcap.StateProvisioned,
		IPv6:             netcap.StateProvisioned,
		GlobalIPv6Egress: netcap.StateUnavailable,
	})
	unowned.ownership.predicate = func() bool { return false }
	globalIPv6 := netip.MustParseAddr("2001:db8::2c")

	// The capability alone would refuse it...
	require.True(t, unowned.capability.HardUndialable(globalIPv6),
		"the fixture must describe a machine that cannot reach this destination, or the test proves nothing")
	// ...and the unowned policy must not, which is the answer the gate is now guaranteed to give.
	require.False(t, unowned.ownership.owns(), "the fixture must be superseded")
	require.False(t, unowned.hardUndialable(globalIPv6),
		"an unowned capture refuses nothing, so a gate holding it lets the operation through")
	require.True(t, unowned.restrictsNothing(),
		"and the fast path must agree, or it would skip a filter the slow path would have applied")

	// A policy captured while owned refuses it, and the SAME value answers the hand-over question, so the
	// two cannot disagree.
	owned := newLocalFamilyPolicy(netcap.FamilyCapability{
		IPv4:             netcap.StateProvisioned,
		IPv6:             netcap.StateProvisioned,
		GlobalIPv6Egress: netcap.StateUnavailable,
	})
	owned.ownership.startedStable = true
	owned.ownership.predicate = func() bool { return true }
	require.NotNil(t, owned.ownership.predicate,
		"stillCurrentEpochPacketConn accepts the policy's predicate; a nil one falls back to permissive")
	require.True(t, owned.hardUndialable(globalIPv6))
	require.ErrorIs(t, owned.hardUndialableError(globalIPv6), errLocalIPv6EgressUnavailable,
		"and the reason is the machine's real fact, not the general family one")
}

func (m *pairSnapshotOnlyManager) NetworkEnvironment() uint64 { return 0 }
