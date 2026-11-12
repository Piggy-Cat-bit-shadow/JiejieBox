package route

// Q13: the reference graph is an INPUT of the eligibility pass, and it changes at runtime.
//
// The pass decides which owner may keep idle connections, and it derives that from three things:
// the static configuration, each object's References()/Dependencies(), and each group's published
// membership. A provider replacing a node under the same tag changes the OBJECT behind the tag
// without moving any selection; a group publishing a membership changes the reference edge set
// without necessarily moving its selection either. The URLTest history hook fires only on a
// selection change, so before adapter.OutboundGraphPublisher those two changes reached this pass
// only by accident.
//
// These tests drive the REAL outbound manager and the REAL reference manager through the real
// publication path - ReplaceOwned and UpdateGroupMembership - and assert the verdict lands on the
// instance that is actually published.

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// publishKeeper is an outbound that records every eligibility verdict it is told.
//
// The record is the whole assertion: the defect this covers is not "the flag had the wrong value",
// it is "the instance that is published was never told anything at all", so a fake that only
// exposes a bool could not tell "told true" from "never asked".
type publishKeeper struct {
	adapter.Outbound

	tag string
	// gate, when non-nil, parks the eligibility call. It exists so a publication can be made to
	// arrive while an older instance is still being told the older verdict, deterministically rather
	// than by scheduler luck.
	gate chan struct{}
	// entered, when non-nil, is closed as the eligibility call begins, so a test can wait for the
	// pass to be INSIDE this instance before publishing its replacement.
	entered     chan struct{}
	enteredOnce sync.Once

	access sync.Mutex
	// verdicts is every value this instance was told, in order.
	verdicts []bool
}

func (k *publishKeeper) Type() string           { return "publish-test" }
func (k *publishKeeper) Tag() string            { return k.tag }
func (k *publishKeeper) Network() []string      { return nil }
func (k *publishKeeper) Dependencies() []string { return nil }

func (k *publishKeeper) SetKeepIdleConnections(keep bool) {
	if k.entered != nil {
		k.enteredOnce.Do(func() { close(k.entered) })
	}
	if k.gate != nil {
		<-k.gate
	}
	k.access.Lock()
	k.verdicts = append(k.verdicts, keep)
	k.access.Unlock()
}

func (k *publishKeeper) CloseIdleConnections() {}

// verdictCount reports every verdict this instance was told.
func (k *publishKeeper) verdictCount() []bool {
	k.access.Lock()
	defer k.access.Unlock()
	return append([]bool(nil), k.verdicts...)
}

// publishKeeperOptions carries the instance a replacement should publish, so a same-tag replacement
// can hand the manager a brand-new object - which is exactly what a provider does.
type publishKeeperOptions struct {
	keeper *publishKeeper
}

// publishReferrerInbound makes an outbound tag REFERENCED, which is what turns keep=false into
// keep=true. It reports References() the way a real inbound does: the tags it dials.
type publishReferrerInbound struct {
	access     sync.Mutex
	references []string
}

func (i *publishReferrerInbound) Type() string { return "publish-test-inbound" }
func (i *publishReferrerInbound) Tag() string  { return "publish-test-inbound" }
func (i *publishReferrerInbound) Start(adapter.StartStage, *adapter.Scope) error {
	return nil
}
func (i *publishReferrerInbound) References() []string {
	i.access.Lock()
	defer i.access.Unlock()
	return append([]string(nil), i.references...)
}
func (i *publishReferrerInbound) setReferences(references ...string) {
	i.access.Lock()
	i.references = references
	i.access.Unlock()
}

type publishInboundManager struct {
	adapter.InboundManager
	inbound *publishReferrerInbound
}

func (m *publishInboundManager) Inbounds() []adapter.Inbound {
	return []adapter.Inbound{m.inbound}
}

// publishFixture is the production shape: a real outbound manager as the service, and the reference
// manager started over it.
type publishFixture struct {
	t         *testing.T
	manager   *outbound.Manager
	reference *ReferenceManager
	inbound   *publishReferrerInbound
	// waitForVerdicts lets a test block until the reference loop has caught up, instead of sleeping.
	waitForVerdicts func(keeper *publishKeeper, count int)
}

func newPublishFixture(t *testing.T) *publishFixture {
	t.Helper()
	registry := outbound.NewRegistry()
	outbound.Register[publishKeeperOptions](registry, "publish-test",
		func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options publishKeeperOptions) (adapter.Outbound, error) {
			options.keeper.tag = tag
			return options.keeper, nil
		})
	outbound.Register[publishGroupOptions](registry, "publish-group",
		func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options publishGroupOptions) (adapter.Outbound, error) {
			options.group.tag = tag
			return options.group, nil
		})
	manager := outbound.NewManager(registry, nil, "")

	inbound := &publishReferrerInbound{}
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), manager)
	ctx = service.ContextWith[adapter.EndpointManager](ctx, &reuseTestEndpointManager{})
	ctx = service.ContextWith[adapter.InboundManager](ctx, &publishInboundManager{inbound: inbound})
	ctx = service.ContextWith[adapter.ServiceManager](ctx, &reuseTestServiceManager{})
	ctx = service.ContextWith[adapter.DNSTransportManager](ctx, &reuseTestDNSTransportManager{})
	ctx = service.ContextWith[adapter.NetworkManager](ctx, &reuseTestNetworkManager{})

	reference := NewReferenceManager(ctx, log.NewNOPFactory().NewLogger("reference"), option.Options{})
	scope := adapter.NewScope(ctx, log.NewNOPFactory().Logger())
	// One static node is created FIRST and becomes the default outbound. Without it, the first
	// provider node becomes the default and is therefore referenced by construction, which would
	// make "unreferenced" untestable for every node published afterwards.
	require.NoError(t, manager.Create(ctx, nil, log.NewNOPFactory().NewLogger("test"), "direct", "publish-test",
		&publishKeeperOptions{keeper: &publishKeeper{}}))
	require.NoError(t, reference.Start(adapter.StartStateStarted, scope))
	t.Cleanup(func() { require.NoError(t, scope.Close()) })

	return &publishFixture{
		t:         t,
		manager:   manager,
		reference: reference,
		inbound:   inbound,
		waitForVerdicts: func(keeper *publishKeeper, count int) {
			t.Helper()
			require.Eventually(t, func() bool { return len(keeper.verdictCount()) >= count },
				5*time.Second, time.Millisecond, "the reference loop never told this instance")
		},
	}
}

// publish replaces tag through the owner-aware path a provider uses.
func (f *publishFixture) publish(tag string, keeper *publishKeeper) {
	f.t.Helper()
	require.NoError(f.t, f.manager.ReplaceOwned(context.Background(), nil,
		log.NewNOPFactory().NewLogger("test"), tag, "publish-test",
		&publishKeeperOptions{keeper: keeper}, "p"))
}

// TestASameTagReplacementTellsTheNewInstanceInsteadOfTheOldOne is the case the audit named: a
// provider replaces a node, the tag does not move, and the eligibility verdict has to land on the
// object that will actually serve the next flow.
func TestASameTagReplacementTellsTheNewInstanceInsteadOfTheOldOne(t *testing.T) {
	fixture := newPublishFixture(t)

	// First publication: nothing references `p/x`, so the verdict is "do not keep idle connections".
	first := &publishKeeper{}
	fixture.publish("p/x", first)
	fixture.waitForVerdicts(first, 1)
	require.Equal(t, []bool{false}, first.verdictCount(),
		"an unreferenced node must be told to drop idle connections")

	// The tag becomes referenced. This is a real graph change - References() of a live inbound -
	// and it is published as one, by replacing the node through the same owner path.
	fixture.inbound.setReferences("p/x")
	second := &publishKeeper{}
	fixture.publish("p/x", second)

	fixture.waitForVerdicts(second, 1)
	require.Equal(t, []bool{true}, second.verdictCount(),
		"the replacement must be told the CURRENT verdict, not start from an empty opinion")
	require.Equal(t, []bool{false}, first.verdictCount(),
		"the retired instance must not be told the new verdict: it is no longer the object a flow can obtain")
}

// TestAPublicationDuringAnOlderVerdictDoesNotLoseTheNewOne is the interleaving case.
//
// The eligibility pass is slow by nature - it walks every owner and calls into each keeper - so a
// publication WILL land while it is running. The pass must finish against the graph it read, and
// the publication's event must then produce a second pass that sees the new instance. Both halves
// are asserted: the old instance keeps the one verdict it was being told, and the new instance gets
// the current one.
func TestAPublicationDuringAnOlderVerdictDoesNotLoseTheNewOne(t *testing.T) {
	fixture := newPublishFixture(t)

	// The first instance parks the eligibility pass inside its own call, and reports that it has
	// arrived, so the publication below lands in a KNOWN position rather than a guessed one.
	gate := make(chan struct{})
	entered := make(chan struct{})
	first := &publishKeeper{gate: gate, entered: entered}
	fixture.publish("p/x", first)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the eligibility pass never reached the first instance")
	}

	// The pass is now blocked inside `first`. Publish the replacement while it is parked: this is a
	// real publication, committed by the real manager, with the reference loop still inside the old
	// instance.
	second := &publishKeeper{}
	fixture.inbound.setReferences("p/x")
	fixture.publish("p/x", second)

	// Let the parked pass finish, then require the publication's own event to be acted on.
	close(gate)
	fixture.waitForVerdicts(second, 1)
	require.Equal(t, []bool{true}, second.verdictCount(),
		"the event that arrived while the pass was blocked was lost")
	require.Equal(t, []bool{false}, first.verdictCount(),
		"the old instance was told something after it stopped being published")
}

// TestACommittedGroupMembershipReachesThePass proves the second event source: a group publishing a
// member list changes the reference edge set even when its selection is unchanged, and the pass is
// told to recompute.
func TestACommittedGroupMembershipReachesThePass(t *testing.T) {
	fixture := newPublishFixture(t)

	group := &publishGroup{tag: "g"}
	require.NoError(t, fixture.manager.Replace(context.Background(), nil,
		log.NewNOPFactory().NewLogger("test"), "g", "publish-group", &publishGroupOptions{group: group}))
	// The group is reachable from a live inbound; the node is not reachable at all until the group
	// publishes it as a member. That is the whole shape: reachability is what the pass derives.
	fixture.inbound.setReferences("g")

	member := &publishKeeper{}
	fixture.publish("p/x", member)
	fixture.waitForVerdicts(member, 1)
	require.Equal(t, []bool{false}, member.verdictCount())

	// The group publishes `p/x` as a member. Nobody selects it yet, and the selection does not move -
	// which is exactly why a selection-shaped notification cannot carry this change.
	require.NoError(t, fixture.manager.UpdateGroupMembership("g", []string{"p/x"}, func() {
		group.setMembers([]string{"p/x"})
	}))

	fixture.waitForVerdicts(member, 2)
	require.Equal(t, []bool{false, true}, member.verdictCount(),
		"a committed membership naming the node must make it referenced")
}

// publishGroup is a group whose membership is what it publishes, shaped like the load-balancing
// group: every member is a reference (traffic may use any of them) and there is no single
// selection whose movement a history hook could report.
type publishGroup struct {
	adapter.Outbound

	tag     string
	access  sync.Mutex
	members []string
}

func (g *publishGroup) Type() string           { return "publish-group" }
func (g *publishGroup) Tag() string            { return g.tag }
func (g *publishGroup) Network() []string      { return nil }
func (g *publishGroup) Dependencies() []string { return nil }
func (g *publishGroup) All() []string {
	g.access.Lock()
	defer g.access.Unlock()
	return append([]string(nil), g.members...)
}
func (g *publishGroup) References() []string { return g.All() }
func (g *publishGroup) Selected(string) adapter.Outbound {
	return nil
}
func (g *publishGroup) AttachConnection(io.Closer) (detach func()) {
	return func() {}
}
func (g *publishGroup) setMembers(members []string) {
	g.access.Lock()
	g.members = members
	g.access.Unlock()
}

type publishGroupOptions struct {
	group *publishGroup
}
