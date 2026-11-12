package adapter

// The dial-path record exists so an event can say where a connection actually went without reading
// current state afterwards. These cases pin the record's own contract: what it counts, what a new
// attempt does to the previous one, the bound, and the vocabulary that explains an incomplete
// projection. The projection onto a DNS event is pinned in dns/events_dialpath_test.go.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// WithDialPath installs a record; a context without one yields nil and every method is a no-op.
func TestDialPathIsAbsentUnlessInstalled(t *testing.T) {
	t.Parallel()

	require.Nil(t, DialPathFromContext(context.Background()))

	dialPath := NewDialPath()
	ctx := WithDialPath(context.Background(), dialPath)
	require.Same(t, dialPath, DialPathFromContext(ctx))

	// The nil receiver is the unobserved path: it must not panic.
	var absent *DialPath
	absent.BeginDial()
	absent.Record("proxy", 1, true)
	absent.Replay([]DialPathStep{{Tag: "proxy"}})
	absent.RecordTransportMember("dns-group", "hk")
	absent.MarkTransportFanOut()
	absent.MarkDirect()
	require.Nil(t, absent.Snapshot())
	require.Equal(t, DialPathResult{}, absent.Result())

	// WithDialPath(ctx, nil) is the same context, so the wrapper can be applied unconditionally.
	require.Equal(t, context.Background(), WithDialPath(context.Background(), nil))
}

// A new attempt discards the previous attempt's chain: the last dial is the one that carries the
// exchange, and an earlier failed attempt is not part of the path that answered.
func TestDialPathBeginDialStartsAFreshChain(t *testing.T) {
	t.Parallel()

	dialPath := NewDialPath()
	dialPath.BeginDial()
	dialPath.Record("proxy", 0, false)
	dialPath.Record("node-a", 3, true)

	dialPath.BeginDial()
	dialPath.Record("proxy", 0, false)
	dialPath.Record("node-b", 4, true)

	result := dialPath.Result()
	require.Equal(t, 2, result.Dials)
	require.Equal(t, []DialPathStep{
		{Tag: "proxy"},
		{Tag: "node-b", Revision: 4, HasRevision: true},
	}, result.Steps)

	leaf, ok := result.Leaf()
	require.True(t, ok)
	require.Equal(t, "node-b", leaf.Tag, "the leaf is the last object the dial was handed to")
	require.Equal(t, uint64(4), leaf.Revision)
	require.True(t, leaf.HasRevision)
	require.Equal(t, DialPathReasonNone, result.Reason())
}

// A replayed chain belongs to a connection that already exists, so it is not a dial attempt, and it
// is superseded by a later attempt the same way a dial is.
func TestDialPathReplayIsNotADial(t *testing.T) {
	t.Parallel()

	dialPath := NewDialPath()
	dialPath.Replay([]DialPathStep{{Tag: "proxy"}, {Tag: "node-a", Revision: 1, HasRevision: true}})

	result := dialPath.Result()
	require.Equal(t, 0, result.Dials, "a replayed connection was not established by this exchange")
	require.True(t, result.Replayed)
	require.Equal(t, "node-a", result.Steps[len(result.Steps)-1].Tag)

	dialPath.BeginDial()
	dialPath.Record("proxy", 0, false)
	dialPath.Record("node-b", 2, true)
	require.Equal(t, "node-b", dialPath.Result().Steps[len(dialPath.Result().Steps)-1].Tag,
		"a dial made after a replay describes the connection this exchange established")
}

// The chain is bounded and the bound is reported rather than silently applied.
func TestDialPathChainIsBoundedAndReportsTruncation(t *testing.T) {
	t.Parallel()

	dialPath := NewDialPath()
	dialPath.BeginDial()
	for index := 0; index < maxDialPathSteps+3; index++ {
		dialPath.Record("hop", 0, false)
	}

	result := dialPath.Result()
	require.Len(t, result.Steps, maxDialPathSteps)
	require.True(t, result.Truncated, "reaching the cap must be observable, not silent")
}

// Every incomplete projection carries a reason, and the reason depends on what actually happened.
func TestDialPathReasonVocabulary(t *testing.T) {
	t.Parallel()

	noConnection := NewDialPath().Result()
	require.Equal(t, DialPathReasonNoConnection, noConnection.Reason(),
		"an exchange that never reached a connection must say so")

	direct := NewDialPath()
	direct.BeginDial()
	direct.MarkDirect()
	require.Equal(t, DialPathReasonDirect, direct.Result().Reason(),
		"a dial with no configured outbound is direct, not unobserved")

	reusedUntracked := NewDialPath()
	reusedUntracked.Replay(nil)
	require.Equal(t, DialPathReasonReplayUnknown, reusedUntracked.Result().Reason(),
		"a pooled connection whose chain was not recorded is a distinct fact")

	unobserved := NewDialPath()
	unobserved.BeginDial()
	require.Equal(t, DialPathReasonUnobserved, unobserved.Result().Reason(),
		"a dial that recorded nothing must not present an empty chain as a complete one")
}

// RecordDialStep is what a group calls: the step carries the group's revision when it reports one,
// and an empty member tag records nothing.
func TestRecordDialStepCarriesTheChoosingGroupsRevision(t *testing.T) {
	t.Parallel()

	chooser := &revisionOutbound{tag: "proxy", revision: 7}
	member := &revisionOutbound{tag: "node-a"}
	dialPath := NewDialPath()
	ctx := WithDialPath(context.Background(), dialPath)

	RecordDialStep(ctx, chooser, member)
	RecordDialStep(ctx, chooser, nil)

	result := dialPath.Result()
	require.Len(t, result.Steps, 1)
	require.Equal(t, "node-a", result.Steps[0].Tag)
	require.Equal(t, uint64(7), result.Steps[0].Revision)
	require.True(t, result.Steps[0].HasRevision)

	// A group with no revision reports none rather than a fabricated zero.
	plainChooser := &plainOutbound{tag: "plain"}
	RecordDialStep(ctx, plainChooser, member)
	second := dialPath.Result().Steps[1]
	require.Equal(t, uint64(0), second.Revision)
	require.False(t, second.HasRevision, "zero with HasRevision false is unknown, and must stay distinct")

	// Recording into a context with no record is a no-op.
	RecordDialStep(context.Background(), chooser, member)
}

// revisionOutbound is an outbound that reports a selection revision.
type revisionOutbound struct {
	Outbound
	tag      string
	revision uint64
	reports  bool
}

func (o *revisionOutbound) Tag() string { return o.tag }

func (o *revisionOutbound) DialPathRevision() uint64 {
	o.reports = true
	return o.revision
}

// plainOutbound is an outbound with no selection revision at all.
type plainOutbound struct {
	Outbound
	tag string
}

func (o *plainOutbound) Tag() string { return o.tag }
