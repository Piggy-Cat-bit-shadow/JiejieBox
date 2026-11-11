package daemon

// A `hidden` group is a DISPLAY decision, not a lifecycle one. This file pins that from the outside,
// through a real started service and the RPCs a client calls:
//
//	the group list (the stream AND the snapshot) omits it
//	the outbound list still contains it, because that list is not a group display
//	the kernel still holds it: it resolves by tag, it is a member of its parent, the parent selects
//	it, the hidden group selects its own members, and a flow through either reaches the member that
//	was selected
//	a profile whose groups are ALL hidden still starts and still serves its members
//	reloading the profile updates the list, because the flag belongs to the configuration
//	a group type that does not implement adapter.HiddenOutboundGroup is still listed, and an empty
//	group that did not ask to be hidden is still listed - which is what proves no size threshold
//	came back with the filter
//
// The member type, the loopback listeners and the two dial helpers are the ones the selector-switch
// tests already use, so "which member the flow left through" is observed rather than asserted.

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxprovider "github.com/sagernet/sing-box/adapter/provider"
	boxservice "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/local"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/protocol/direct"
	"github.com/sagernet/sing-box/protocol/group"
	"github.com/sagernet/sing-box/protocol/socks"
	providerlocal "github.com/sagernet/sing-box/provider/local"
	providerremote "github.com/sagernet/sing-box/provider/remote"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

// groupDisplayContext is `selectorSwitchContext` plus the two other group types and the
// capability-free group below, because this file has to start all of them in one Box.
func groupDisplayContext(t *testing.T) context.Context {
	t.Helper()
	workingPath, err := os.MkdirTemp("", "sing-box-group-hidden-")
	if err != nil {
		t.Fatalf("create working directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workingPath) })
	outboundRegistry := outbound.NewRegistry()
	direct.RegisterOutbound(outboundRegistry)
	group.RegisterSelector(outboundRegistry)
	group.RegisterURLTest(outboundRegistry)
	group.RegisterLoadBalance(outboundRegistry)
	// The SOCKS outbound is here for the detour attack: `direct` refuses `detour`, so a proxy
	// outbound is the only way to point a real `detour` at a hidden group.
	socks.RegisterOutbound(outboundRegistry)
	outbound.Register[probeMemberOptions](outboundRegistry, "probe-member", newProbeMember)
	outbound.Register[capabilityFreeGroupOptions](outboundRegistry, "capability-free-group", newCapabilityFreeGroup)
	// The inline provider is here for the provider-backed attack: it publishes nodes during
	// box.New, so a hidden group fed by it has membership before the group starts. The REMOTE
	// provider is here for the republication attack, which needs a publication the test can trigger
	// through the daemon's own refresh RPC.
	providerRegistry := boxprovider.NewRegistry()
	providerlocal.RegisterProviderInline(providerRegistry)
	providerremote.RegisterProvider(providerRegistry)
	dnsRegistry := dns.NewTransportRegistry()
	local.RegisterTransport(dnsRegistry)
	ctx := box.Context(context.Background(),
		inbound.NewRegistry(),
		outboundRegistry,
		providerRegistry,
		endpoint.NewRegistry(),
		dnsRegistry,
		boxservice.NewRegistry(),
		certificate.NewRegistry(),
	)
	return filemanager.WithDefault(ctx, workingPath, workingPath, -1, -1)
}

// capabilityFreeGroupOptions is the configuration of a group type that deliberately does NOT
// implement adapter.HiddenOutboundGroup.
type capabilityFreeGroupOptions struct {
	Outbounds []string `json:"outbounds,omitempty"`
}

// capabilityFreeGroup is a group-shaped outbound with no display metadata of its own.
//
// It exists because the default is the interesting half of a capability interface: a group type that
// has never heard of `hidden` must keep appearing in the list, and only a type that really does not
// implement the interface can show that.
type capabilityFreeGroup struct {
	outbound.Adapter
	tags []string
}

var (
	_ adapter.OutboundGroup = (*capabilityFreeGroup)(nil)
	_ adapter.Referrer      = (*capabilityFreeGroup)(nil)
)

func newCapabilityFreeGroup(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options capabilityFreeGroupOptions) (adapter.Outbound, error) {
	return &capabilityFreeGroup{
		Adapter: outbound.NewAdapter("capability-free-group", tag, []string{N.NetworkTCP, N.NetworkUDP}, options.Outbounds),
		tags:    options.Outbounds,
	}, nil
}

func (g *capabilityFreeGroup) All() []string        { return g.tags }
func (g *capabilityFreeGroup) References() []string { return g.tags }

func (g *capabilityFreeGroup) Selected(network string) adapter.Outbound { return nil }

func (g *capabilityFreeGroup) AttachConnection(closer io.Closer) (detach func()) { return func() {} }

// It is a LISTING fixture: it exists to be counted in the group list, and nothing in the profile
// selects it, references it or dials it.
func (g *capabilityFreeGroup) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, E.New("the capability-free group is a group-listing fixture: it carries no flows")
}

func (g *capabilityFreeGroup) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, E.New("the capability-free group is a group-listing fixture: it carries no flows")
}

// hiddenGroupMembers are the loopback endpoints the profile's members dial, keyed by member tag.
func hiddenGroupProfile(members map[string]*memberListener) string {
	return fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "us-1", "destination": "%s"},
    {"type": "probe-member", "tag": "us-2", "destination": "%s"},
    {"type": "probe-member", "tag": "hk-1", "destination": "%s"},
    {"type": "probe-member", "tag": "jp-1", "destination": "%s"},

    {"type": "selector", "tag": "us-pool", "outbounds": ["us-1", "us-2"], "default": "us-1", "hidden": true},
    {"type": "urltest", "tag": "hk-pool", "outbounds": ["hk-1"], "hidden": true},
    {"type": "loadbalance", "tag": "jp-pool", "outbounds": ["jp-1"], "hidden": true},

    {"type": "selector", "tag": "main", "outbounds": ["us-pool", "hk-pool", "jp-pool"], "default": "us-pool"},
    {"type": "selector", "tag": "visible-single", "outbounds": ["jp-1"], "hidden": false},
    {"type": "selector", "tag": "visible-default", "outbounds": ["jp-1"]},
    {"type": "capability-free-group", "tag": "plain-empty-group", "outbounds": []}
  ],
  "route": {"final": "us-pool"}
}`,
		members["us-1"].target(), members["us-2"].target(),
		members["hk-1"].target(), members["jp-1"].target())
}

// reportedGroupTags is what a client is told the group list contains.
func reportedGroupTags(t *testing.T, started *StartedService) []string {
	t.Helper()
	groups := started.readGroups()
	tags := make([]string, 0, len(groups.Group))
	for _, entry := range groups.Group {
		tags = append(tags, entry.Tag)
	}
	return tags
}

// reportedGroupItems is the member list of one reported group.
func reportedGroupItems(t *testing.T, started *StartedService, groupTag string) []string {
	t.Helper()
	for _, entry := range started.readGroups().Group {
		if entry.Tag != groupTag {
			continue
		}
		tags := make([]string, 0, len(entry.Items))
		for _, item := range entry.Items {
			tags = append(tags, item.Tag)
		}
		return tags
	}
	t.Fatalf("the running instance reports no group named %s", groupTag)
	return nil
}

// reportedOutboundTags is the COMPLETE outbound list, which this feature must not touch.
func reportedOutboundTags(t *testing.T, started *StartedService) []string {
	t.Helper()
	outbounds := started.readOutbounds()
	tags := make([]string, 0, len(outbounds.Outbounds))
	for _, item := range outbounds.Outbounds {
		tags = append(tags, item.Tag)
	}
	return tags
}

// newHiddenGroupService starts one service on the group-display profile.
func newHiddenGroupService(t *testing.T, profile string) *StartedService {
	t.Helper()
	started := NewStartedService(ServiceOptions{Context: groupDisplayContext(t)})
	t.Cleanup(started.Close)
	require.NoError(t, started.StartOrReloadService(context.Background(), profile, nil))
	require.NotNil(t, started.Instance())
	return started
}

func TestAHiddenGroupIsOmittedFromTheGroupListAndKeptEverywhereElse(t *testing.T) {
	members := map[string]*memberListener{
		"us-1": startMemberListener(t),
		"us-2": startMemberListener(t),
		"hk-1": startMemberListener(t),
		"jp-1": startMemberListener(t),
	}
	started := newHiddenGroupService(t, hiddenGroupProfile(members))

	// 1. The group list omits the three hidden groups and keeps everything else, including the
	//    single-node and the explicit-false ones and the group type that knows nothing about hidden.
	require.Equal(t, []string{"main", "visible-single", "visible-default", "plain-empty-group"},
		reportedGroupTags(t, started),
		"the group list must contain exactly the groups that did not ask to be hidden")

	// 2. The complete outbound list is NOT a group display and keeps every object.
	require.ElementsMatch(t,
		[]string{"us-1", "us-2", "hk-1", "jp-1", "us-pool", "hk-pool", "jp-pool", "main", "visible-single", "visible-default", "plain-empty-group"},
		reportedOutboundTags(t, started),
		"the outbound list must keep the hidden groups")

	// 4. The kernel still holds them, and the parent still lists them as members.
	for _, hiddenTag := range []string{"us-pool", "hk-pool", "jp-pool"} {
		_, isLoaded := started.Instance().outboundManager.Outbound(hiddenTag)
		require.True(t, isLoaded, "the outbound manager must still hold %s", hiddenTag)
	}
	require.Equal(t, []string{"us-pool", "hk-pool", "jp-pool"}, reportedGroupItems(t, started, "main"),
		"a parent's member list must not be filtered")

	// 5. The parent selects a hidden group through the RPC a client calls, and the flow follows.
	_, err := started.SelectOutbound(context.Background(), &SelectOutboundRequest{
		GroupTag:    "main",
		OutboundTag: "us-pool",
	})
	require.NoError(t, err, "a hidden group must be selectable as a member of its parent")
	require.Equal(t, "us-pool", reportedGroupSelection(t, started, "main"))
	require.Equal(t, "us-1", dialThroughNamedGroup(t, started, "main", members),
		"the flow through the parent must leave through the hidden group's selected member")

	// 6. The hidden group's OWN selection still works - the RPC resolves it by tag exactly as it
	//    resolves a visible group - and the new member is what the flow then uses.
	_, err = started.SelectOutbound(context.Background(), &SelectOutboundRequest{
		GroupTag:    "us-pool",
		OutboundTag: "us-2",
	})
	require.NoError(t, err, "a hidden group must remain selectable in its own right")
	hiddenSelector, isLoaded := started.Instance().outboundManager.Outbound("us-pool")
	require.True(t, isLoaded)
	selected := hiddenSelector.(adapter.OutboundGroup).Selected(N.NetworkTCP)
	require.NotNil(t, selected)
	require.Equal(t, "us-2", selected.Tag(), "the hidden group's selection must be recorded and readable")
	require.Equal(t, "us-2", dialThroughNamedGroup(t, started, "main", members),
		"the flow through the parent must follow the HIDDEN group's new selection")

	// 7. A hidden group is still a legal route target: the profile's `route.final` names one, and
	//    this service started with it. That reference is validated, not decorative - the same profile
	//    with `"final": "does-not-exist"` is refused at start with "default outbound not found"
	//    (measured). Dialling the group directly is the lookup a detour performs.
	require.Equal(t, "us-2", dialThroughNamedGroup(t, started, "us-pool", members),
		"a hidden group must be dialable as its own object, which is what a detour resolves to")
}

// TestEveryGroupHiddenStillStarts pins the degenerate configuration: nothing to show, everything to
// run. A filter that reached the lifecycle would break here first.
func TestEveryGroupHiddenStillStarts(t *testing.T) {
	members := map[string]*memberListener{
		"us-1": startMemberListener(t),
		"us-2": startMemberListener(t),
	}
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "us-1", "destination": "%s"},
    {"type": "probe-member", "tag": "us-2", "destination": "%s"},
    {"type": "selector", "tag": "us-pool", "outbounds": ["us-1", "us-2"], "default": "us-1", "hidden": true},
    {"type": "urltest", "tag": "us-urltest", "outbounds": ["us-1", "us-2"], "hidden": true},
    {"type": "loadbalance", "tag": "us-loadbalance", "outbounds": ["us-1", "us-2"], "hidden": true}
  ]
}`, members["us-1"].target(), members["us-2"].target())
	started := newHiddenGroupService(t, profile)

	require.Empty(t, reportedGroupTags(t, started),
		"a profile whose groups are all hidden must report an empty group list, not fail")
	require.ElementsMatch(t,
		[]string{"us-1", "us-2", "us-pool", "us-urltest", "us-loadbalance"},
		reportedOutboundTags(t, started),
		"the outbound list is unaffected by every group being hidden")

	// Every hidden group is still a working object, including the two whose selection is not a
	// single committed member.
	for _, hiddenTag := range []string{"us-pool", "us-urltest", "us-loadbalance"} {
		_, isLoaded := started.Instance().outboundManager.Outbound(hiddenTag)
		require.True(t, isLoaded, "the outbound manager must still hold %s", hiddenTag)
	}
	require.Contains(t, []string{"us-1", "us-2"}, dialThroughNamedGroup(t, started, "us-pool", members))
}

// TestTheGroupStreamOmitsHiddenGroups is the stream half of the display: the command clients build
// their group screen from `SubscribeGroups`, so that is the payload that has to be filtered.
func TestTheGroupStreamOmitsHiddenGroups(t *testing.T) {
	members := map[string]*memberListener{
		"us-1": startMemberListener(t),
		"us-2": startMemberListener(t),
		"hk-1": startMemberListener(t),
		"jp-1": startMemberListener(t),
	}
	started := newHiddenGroupService(t, hiddenGroupProfile(members))

	streamContext, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	stream := &groupSubscriptionStream{ctx: streamContext, sent: make(chan *Groups, 4)}
	finished := make(chan error, 1)
	go func() {
		finished <- started.SubscribeGroups(&emptypb.Empty{}, stream)
	}()

	var streamed *Groups
	select {
	case streamed = <-stream.sent:
	case <-time.After(10 * time.Second):
		t.Fatal("the group stream sent nothing")
	}
	cancelStream()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the group stream did not return after its context was cancelled")
	}

	streamedTags := make([]string, 0, len(streamed.Group))
	for _, entry := range streamed.Group {
		streamedTags = append(streamedTags, entry.Tag)
	}
	require.Equal(t, reportedGroupTags(t, started), streamedTags,
		"the group stream and the snapshot must report the same groups")
	for _, hiddenTag := range []string{"us-pool", "hk-pool", "jp-pool"} {
		require.NotContains(t, streamedTags, hiddenTag, "the group stream must omit %s", hiddenTag)
	}
}

// TestReloadingTheProfileUpdatesTheGroupList pins that the flag is configuration state, not a
// property of the running object that outlives it: a reload rebuilds every group, and the list
// follows the new configuration in both directions.
func TestReloadingTheProfileUpdatesTheGroupList(t *testing.T) {
	members := map[string]*memberListener{
		"us-1": startMemberListener(t),
		"us-2": startMemberListener(t),
	}
	hiddenProfile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "us-1", "destination": "%s"},
    {"type": "probe-member", "tag": "us-2", "destination": "%s"},
    {"type": "selector", "tag": "us-pool", "outbounds": ["us-1", "us-2"], "default": "us-1", "hidden": true}
  ]
}`, members["us-1"].target(), members["us-2"].target())
	visibleProfile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "us-1", "destination": "%s"},
    {"type": "probe-member", "tag": "us-2", "destination": "%s"},
    {"type": "selector", "tag": "us-pool", "outbounds": ["us-1", "us-2"], "default": "us-1"}
  ]
}`, members["us-1"].target(), members["us-2"].target())

	started := newHiddenGroupService(t, hiddenProfile)
	require.Empty(t, reportedGroupTags(t, started), "the hidden group must not be listed before the reload")

	require.NoError(t, started.StartOrReloadService(context.Background(), visibleProfile, nil))
	require.NoError(t, started.waitForStarted(context.Background()))
	require.Equal(t, []string{"us-pool"}, reportedGroupTags(t, started),
		"a reload that stops hiding the group must list it")

	require.NoError(t, started.StartOrReloadService(context.Background(), hiddenProfile, nil))
	require.NoError(t, started.waitForStarted(context.Background()))
	require.Empty(t, reportedGroupTags(t, started),
		"a reload that hides the group again must drop it from the list")

	// And the group it dropped is still a working object after both rebuilds.
	require.Contains(t, []string{"us-1", "us-2"}, dialThroughNamedGroup(t, started, "us-pool", members))
}

// TestAHiddenGroupsSelectionIsPersisted pins the last piece of "nothing about the group changed":
// the selector still WRITES its selection to the cache file and still RESTORES it when the profile is
// reloaded, so hiding a group does not cost it the memory a visible group keeps.
func TestAHiddenGroupsSelectionIsPersisted(t *testing.T) {
	members := map[string]*memberListener{
		"us-1": startMemberListener(t),
		"us-2": startMemberListener(t),
	}
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "experimental": {"cache_file": {"enabled": true}},
  "outbounds": [
    {"type": "probe-member", "tag": "us-1", "destination": "%s"},
    {"type": "probe-member", "tag": "us-2", "destination": "%s"},
    {"type": "selector", "tag": "us-pool", "outbounds": ["us-1", "us-2"], "default": "us-1", "hidden": true},
    {"type": "selector", "tag": "main", "outbounds": ["us-pool"]}
  ]
}`, members["us-1"].target(), members["us-2"].target())
	started := newHiddenGroupService(t, profile)
	require.Equal(t, []string{"main"}, reportedGroupTags(t, started),
		"the hidden group must not be listed, so the persistence below is observed on a hidden group")

	_, err := started.SelectOutbound(context.Background(), &SelectOutboundRequest{
		GroupTag:    "us-pool",
		OutboundTag: "us-2",
	})
	require.NoError(t, err)

	cacheFile := started.Instance().cacheFile
	require.NotNil(t, cacheFile, "the profile enables the cache file, so the selection must have somewhere to go")
	require.Equal(t, "us-2", cacheFile.LoadSelected("us-pool"),
		"a hidden group must still persist its selection")

	// The reload replaces the object; the rebuilt hidden group must come back on the stored member.
	require.NoError(t, started.StartOrReloadService(context.Background(), profile, nil))
	require.NoError(t, started.waitForStarted(context.Background()))
	require.Equal(t, []string{"main"}, reportedGroupTags(t, started))
	restored, isLoaded := started.Instance().outboundManager.Outbound("us-pool")
	require.True(t, isLoaded)
	restoredSelection := restored.(adapter.OutboundGroup).Selected(N.NetworkTCP)
	require.NotNil(t, restoredSelection)
	require.Equal(t, "us-2", restoredSelection.Tag(),
		"a hidden group must restore its persisted selection after a reload")
	require.Equal(t, "us-2", dialThroughNamedGroup(t, started, "main", members),
		"the flow must follow the restored selection")
}

// groupSubscriptionStream is the smallest grpc.ServerStreamingServer[Groups] that can observe what// SubscribeGroups emits. It delivers sends to a channel so the test can wait for the first one, and
// it holds no state the service could depend on.
type groupSubscriptionStream struct {
	ctx  context.Context
	sent chan *Groups
}

var _ grpc.ServerStreamingServer[Groups] = (*groupSubscriptionStream)(nil)

func (s *groupSubscriptionStream) Send(groups *Groups) error {
	select {
	case s.sent <- groups:
	case <-s.ctx.Done():
	}
	return nil
}

func (s *groupSubscriptionStream) SetHeader(metadata.MD) error  { return nil }
func (s *groupSubscriptionStream) SendHeader(metadata.MD) error { return nil }
func (s *groupSubscriptionStream) SetTrailer(metadata.MD)       {}
func (s *groupSubscriptionStream) Context() context.Context     { return s.ctx }
func (s *groupSubscriptionStream) SendMsg(any) error            { return nil }
func (s *groupSubscriptionStream) RecvMsg(any) error            { return nil }
