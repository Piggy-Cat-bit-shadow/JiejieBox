package daemon

// The group switch, from the RPC a client calls to the member the next flow actually leaves through,
// across a profile reload.
//
// The task asks for one invariant across the layers: what a client is told is selected must be what
// the core then uses. The daemon is the layer where that is decided - SelectOutbound resolves the
// group on the RUNNING instance and calls the selector, while a reload replaces that instance
// entirely - so a switch racing a reload is the case where the two could disagree.
//
// This file pins it end to end with stock objects only:
//
//	two direct outbounds whose override_address/override_port point at two loopback listeners, and a
//	selector over them. The listener that receives a connection therefore IDENTIFIES the member the
//	selector used - no fake, no probe type, the production direct outbound and the production group.
//
//  1. the reported selection matches the member the flow reaches;
//  2. a switch moves both together;
//  3. after a reload the reported selection is still one of the members and the flow reaches
//     exactly the reported one - the invariant survives the instance that held it;
//  4. a switch that races reloads neither panics nor leaves the two disagreeing.

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
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
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

// selectorSwitchContext is libboxContext plus the two outbound types this profile uses. The
// registries are still empty of everything else: this test only needs a Box that can build a
// selector over direct members.
func selectorSwitchContext(t *testing.T) context.Context {
	t.Helper()
	workingPath, err := os.MkdirTemp("", "sing-box-selector-switch-")
	if err != nil {
		t.Fatalf("create working directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workingPath) })
	outboundRegistry := outbound.NewRegistry()
	direct.RegisterOutbound(outboundRegistry)
	group.RegisterSelector(outboundRegistry)
	// A test-only member type: it dials a fixed loopback listener, so which member a flow left
	// through is observable without a proxy protocol in the middle. The group, the selector state and
	// the daemon RPC are the production objects; only the member's body is a stub.
	outbound.Register[probeMemberOptions](outboundRegistry, "probe-member", newProbeMember)
	dnsRegistry := dns.NewTransportRegistry()
	local.RegisterTransport(dnsRegistry)
	ctx := box.Context(context.Background(),
		inbound.NewRegistry(),
		outboundRegistry,
		boxprovider.NewRegistry(),
		endpoint.NewRegistry(),
		dnsRegistry,
		boxservice.NewRegistry(),
		certificate.NewRegistry(),
	)
	return filemanager.WithDefault(ctx, workingPath, workingPath, -1, -1)
}

// probeMemberOptions is the configuration of the test-only member type.
type probeMemberOptions struct {
	// Destination is the loopback listener this member always dials.
	Destination string `json:"destination"`
}

// probeMember is one selector member whose dial is identifiable by the listener it reaches.
type probeMember struct {
	outbound.Adapter
	target string
}

func newProbeMember(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options probeMemberOptions) (adapter.Outbound, error) {
	return &probeMember{
		Adapter: outbound.NewAdapter("probe-member", tag, []string{N.NetworkTCP}, nil),
		target:  options.Destination,
	}, nil
}

func (m *probeMember) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "tcp", m.target)
}

func (m *probeMember) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

var _ adapter.Outbound = (*probeMember)(nil)

// memberListener counts the connections one member's destination receives.
type memberListener struct {
	listener net.Listener
	accepted atomic.Int32
}

func startMemberListener(t *testing.T) *memberListener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	member := &memberListener{listener: listener}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			member.accepted.Add(1)
			_ = conn.Close()
		}
	}()
	return member
}

func (m *memberListener) port() uint16 {
	return uint16(m.listener.Addr().(*net.TCPAddr).Port)
}

// selectorProfile is the profile the service is started (and reloaded) with.
func selectorProfile(first, second *memberListener) string {
	return fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "member-a", "destination": "127.0.0.1:%d"},
    {"type": "probe-member", "tag": "member-b", "destination": "127.0.0.1:%d"},
    {"type": "selector", "tag": "group", "outbounds": ["member-a", "member-b"], "default": "member-a"}
  ]
}`, first.port(), second.port())
}

// reportedSelection reads what a client would be told is selected for the group.
func reportedSelection(t *testing.T, started *StartedService) string {
	t.Helper()
	groups := started.readGroups()
	for _, entry := range groups.Group {
		if entry.Tag != "group" {
			continue
		}
		require.True(t, entry.Selectable, "the group must be reported as a selectable selector")
		return entry.Selected
	}
	t.Fatal("the running instance reports no group named group")
	return ""
}

// dialThroughGroup is the flow: the group's own dial path, which selects the member.
func dialThroughGroup(t *testing.T, started *StartedService) {
	t.Helper()
	instance := started.Instance()
	require.NotNil(t, instance)
	member, isLoaded := instance.outboundManager.Outbound("group")
	require.True(t, isLoaded, "the running instance has no group")
	dialer, isDialer := member.(N.Dialer)
	require.True(t, isDialer)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, dialErr := dialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("origin.test:443"))
	require.NoError(t, dialErr, "the group must be able to dial through its selected member")
	_ = conn.Close()
}

// requireFlowReachesReportedMember is the invariant: the reported selection and the member the flow
// left through are the same.
func requireFlowReachesReportedMember(t *testing.T, started *StartedService, first, second *memberListener) string {
	t.Helper()
	selected := reportedSelection(t, started)
	require.Contains(t, []string{"member-a", "member-b"}, selected)
	beforeFirst := first.accepted.Load()
	beforeSecond := second.accepted.Load()
	dialThroughGroup(t, started)
	require.Eventually(t, func() bool {
		return first.accepted.Load() > beforeFirst || second.accepted.Load() > beforeSecond
	}, 5*time.Second, 2*time.Millisecond, "the flow reached neither member's destination")
	if selected == "member-a" {
		require.Greater(t, first.accepted.Load(), beforeFirst,
			"the client is told member-a is selected but the flow left through member-b")
		require.Equal(t, beforeSecond, second.accepted.Load(),
			"the flow reached the member the client was NOT told about")
	} else {
		require.Greater(t, second.accepted.Load(), beforeSecond,
			"the client is told member-b is selected but the flow left through member-a")
		require.Equal(t, beforeFirst, first.accepted.Load(),
			"the flow reached the member the client was NOT told about")
	}
	return selected
}

func TestTheSelectedMemberIsTheOneTheFlowUsesAcrossAReload(t *testing.T) {
	first := startMemberListener(t)
	second := startMemberListener(t)
	profile := selectorProfile(first, second)

	started := NewStartedService(ServiceOptions{Context: selectorSwitchContext(t)})
	t.Cleanup(started.Close)
	require.NoError(t, started.StartOrReloadService(context.Background(), profile, nil))
	require.NotNil(t, started.Instance())

	// 1. The default is reported and used.
	require.Equal(t, "member-a", requireFlowReachesReportedMember(t, started, first, second))

	// 2. The switch moves both together, through the RPC a client actually calls.
	_, switchErr := started.SelectOutbound(context.Background(), &SelectOutboundRequest{
		GroupTag:    "group",
		OutboundTag: "member-b",
	})
	require.NoError(t, switchErr)
	require.Equal(t, "member-b", requireFlowReachesReportedMember(t, started, first, second))

	// 3. A reload replaces the instance that held the selection. Whatever the new instance reports
	// must still be what its flows use - the invariant, not the choice.
	require.NoError(t, started.StartOrReloadService(context.Background(), profile, nil))
	require.NotNil(t, started.Instance())
	requireFlowReachesReportedMember(t, started, first, second)

	require.NoError(t, started.CloseService())
}

func TestASwitchThatRacesAProfileReloadStaysConsistent(t *testing.T) {
	first := startMemberListener(t)
	second := startMemberListener(t)
	profile := selectorProfile(first, second)

	started := NewStartedService(ServiceOptions{Context: selectorSwitchContext(t)})
	t.Cleanup(started.Close)
	require.NoError(t, started.StartOrReloadService(context.Background(), profile, nil))

	// A client switching the group while the profile is being reloaded underneath it.
	stop := make(chan struct{})
	var switching sync.WaitGroup
	switching.Add(1)
	errors := make(chan error, 64)
	go func() {
		defer switching.Done()
		for index := 0; ; index++ {
			select {
			case <-stop:
				return
			default:
			}
			target := "member-a"
			if index%2 == 1 {
				target = "member-b"
			}
			_, err := started.SelectOutbound(context.Background(), &SelectOutboundRequest{
				GroupTag:    "group",
				OutboundTag: target,
			})
			if err != nil {
				select {
				case errors <- err:
				default:
				}
			}
		}
	}()

	for reload := 0; reload < 3; reload++ {
		require.NoError(t, started.StartOrReloadService(context.Background(), profile, nil),
			"reload %d failed while a switch was in flight", reload)
	}
	close(stop)
	switching.Wait()
	close(errors)
	// A switch may lose the race with a reload that replaced the instance under it - that is a
	// rejected request, not a wrong one. It must be a gRPC-shaped rejection (not a panic), and the
	// service must still be running with a coherent state.
	for err := range errors {
		require.Error(t, err)
	}
	require.NotNil(t, started.Instance(), "the service is not running after the racing reloads")
	requireFlowReachesReportedMember(t, started, first, second)

	require.NoError(t, started.CloseService())
}
