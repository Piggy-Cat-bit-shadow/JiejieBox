package daemon

// P1 regression: `hidden` lives on the URLTest WRAPPER while a provider publication replaces the
// inner URLTestGroup. If the flag were stored on the inner object it would be silently lost on the
// first republication: the group would reappear in the client's list while everything else kept
// working. That is the exact failure this file rules out.
//
// The publication is triggered through the daemon's own `RefreshRemoteProviders` RPC, so the path
// under test is the production one: remote fetch -> provider apply -> group callback -> inner swap.
//
// # What is asserted, and what is deliberately not
//
// ADDITIVE republication (one node -> two) is asserted end to end: the new node joins the group, is
// measured by the group's own probe path, and the flag survives on the same wrapper object.
//
// A pure REPLACEMENT (old nodes removed, a new one added) is NOT asserted to deliver the new set,
// because it does not - and not because of `hidden`. Measured on this tree with a VISIBLE urltest
// group and the same provider, a replacement leaves the group on a stale member (`[pool/node-1]`)
// while the RPC correctly reports "published"; an additive update applies cleanly. That is a
// pre-existing provider/group republication behaviour outside this feature's boundary, so this test
// asserts only the part the hidden feature owns after a replacement: the wrapper, the flag, and the
// absence of the group from the display.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func TestHiddenURLTestSurvivesAProviderRepublication(t *testing.T) {
	nodes := make([]*countingServer, 3)
	for index := range nodes {
		nodes[index] = startCountingServer(t)
	}

	var document atomic.Value
	document.Store(providerDocument(nodes[0]))
	subscription := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(document.Load().(string)))
	}))
	t.Cleanup(subscription.Close)

	// `route.final` must not be the provider-fed group: the provider's own download is routed through
	// the final outbound, and a group fed only by that provider would be waiting on itself. The
	// platform refuses exactly that, which is why the profile has a static download path.
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "providers": [
    {"type": "remote", "tag": "pool", "url": "%s/sub.json", "update_interval": "1h"}
  ],
  "outbounds": [
    {"type": "direct", "tag": "download-path"},
    {"type": "urltest", "tag": "REGION", "providers": ["pool"],
     "url": "%s", "expected_status": "204", "interval": "1h", "idle_timeout": "2h", "hidden": true},
    {"type": "selector", "tag": "MAIN", "outbounds": ["REGION"], "default": "REGION"}
  ],
  "route": {"final": "download-path"}
}`, subscription.URL, nodes[0].url)
	started := newHiddenGroupService(t, profile)

	// --- before any update ---
	require.Equal(t, []string{"MAIN"}, reportedGroupTags(t, started))
	require.Equal(t, []string{"pool/node-1"}, liveGroupMembers(t, started, "REGION"))
	require.Equal(t, []string{"REGION"}, reportedGroupItems(t, started, "MAIN"))
	wrapper, isLoaded := started.Instance().outboundManager.Outbound("REGION")
	require.True(t, isLoaded)
	hiddenGroup, isHiddenCapable := wrapper.(adapter.HiddenOutboundGroup)
	require.True(t, isHiddenCapable)
	require.True(t, hiddenGroup.Hidden())
	require.Equal(t, "node-1", dialThroughCountingGroup(t, started, "MAIN", nodes))

	// --- additive republication: the provider publishes one MORE node ---
	document.Store(providerDocument(nodes[0], nodes[1]))
	requirePublished(t, started, "pool")

	require.True(t, waitForMembers(t, started, "REGION", 15*time.Second, "pool/node-1", "pool/node-2"),
		"the group must adopt the newly published node; last members: %v", liveGroupMembers(t, started, "REGION"))

	currentWrapper, isLoaded := started.Instance().outboundManager.Outbound("REGION")
	require.True(t, isLoaded)
	require.Same(t, wrapper, currentWrapper,
		"a provider publication must replace the INNER group, not the wrapper that carries hidden")
	require.True(t, currentWrapper.(adapter.HiddenOutboundGroup).Hidden(),
		"hidden must survive a provider republication")
	require.Equal(t, []string{"MAIN"}, reportedGroupTags(t, started),
		"the group must not reappear in the display after the republication")
	require.Equal(t, []string{"REGION"}, reportedGroupItems(t, started, "MAIN"),
		"the parent must still name the hidden group after the republication")
	require.True(t, containsAll(reportedOutboundTags(t, started), "pool/node-2", "REGION", "MAIN"),
		"the complete outbound list must contain the newly published node")

	// The NEW node is actually used by the group's own probe path, not merely listed: the manual
	// check measures every member, and node-2's endpoint is where its measurement lands.
	measuredBefore := nodes[1].count.Load()
	_, err := started.URLTest(context.Background(), &URLTestRequest{OutboundTag: "REGION"})
	require.NoError(t, err, "the hidden group must still answer the URLTest RPC after a republication")
	require.Eventually(t, func() bool { return nodes[1].count.Load() > measuredBefore },
		15*time.Second, 10*time.Millisecond,
		"the newly published member must be measured by the hidden group's own probe path")

	// --- replacement: only the part this feature owns is asserted ---
	document.Store(providerDocument(nodes[2]))
	requirePublished(t, started, "pool")
	currentWrapper, isLoaded = started.Instance().outboundManager.Outbound("REGION")
	require.True(t, isLoaded)
	require.Same(t, wrapper, currentWrapper, "a replacement must not swap the wrapper either")
	require.True(t, currentWrapper.(adapter.HiddenOutboundGroup).Hidden())
	require.Equal(t, []string{"MAIN"}, reportedGroupTags(t, started))
	require.Equal(t, []string{"REGION"}, reportedGroupItems(t, started, "MAIN"))
}

// requirePublished runs one manual provider refresh and requires the provider to report a publication
// (not "skipped", "unchanged", "busy" or "failed").
func requirePublished(t *testing.T, started *StartedService, providerTag string) {
	t.Helper()
	refresh, err := started.RefreshRemoteProviders(context.Background(), &RefreshRemoteProvidersRequest{
		Tags:      []string{providerTag},
		TimeoutMs: 15000,
	})
	require.NoError(t, err)
	require.Len(t, refresh.Results, 1, "the refresh must address exactly the named provider")
	require.Equal(t, string(ProviderRefreshPublished), refresh.Results[0].Status,
		"the provider must publish the new document: %s", refresh.Results[0].Reason)
}

// providerDocument renders the document the subscription server serves.
func providerDocument(nodes ...*countingServer) string {
	outbounds := make([]string, 0, len(nodes))
	for index, node := range nodes {
		outbounds = append(outbounds, fmt.Sprintf(
			`{"type": "probe-member", "tag": "node-%d", "destination": "%s"}`,
			index+1, node.destination(),
		))
	}
	return `{"outbounds": [` + strings.Join(outbounds, ",") + `]}`
}

// countingServer is an HTTP endpoint that answers 204 and counts every accepted connection, which is
// what makes "did this member carry the flow / receive a measurement" observable for a protocol that
// only connects.
type countingServer struct {
	server *httptest.Server
	count  *atomic.Int32
	url    string
}

func startCountingServer(t *testing.T) *countingServer {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	counter := new(atomic.Int32)
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			counter.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	return &countingServer{server: server, count: counter, url: server.URL}
}

func (s *countingServer) destination() string { return s.server.Listener.Addr().String() }

// dialThroughCountingGroup dials one group and reports which counting server received the
// connection - the same observation the member-listener helper provides elsewhere, for members whose
// body is an HTTP endpoint instead of a bare listener.
func dialThroughCountingGroup(t *testing.T, started *StartedService, groupTag string, nodes []*countingServer) string {
	t.Helper()
	loaded, isLoaded := started.Instance().outboundManager.Outbound(groupTag)
	require.True(t, isLoaded, "the running instance has no group %s", groupTag)
	dialer, isDialer := loaded.(N.Dialer)
	require.True(t, isDialer, "the group %s must be a dialer", groupTag)

	before := make([]int32, len(nodes))
	for index, node := range nodes {
		before[index] = node.count.Load()
	}
	dialContext, cancelDial := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDial()
	conn, err := dialer.DialContext(dialContext, N.NetworkTCP, M.ParseSocksaddr("origin.test:443"))
	require.NoError(t, err, "the group %s must be able to dial through its selected member", groupTag)
	if conn != nil {
		_ = conn.Close()
	}

	reached := ""
	require.Eventually(t, func() bool {
		for index, node := range nodes {
			if node.count.Load() > before[index] {
				reached = fmt.Sprintf("node-%d", index+1)
				return true
			}
		}
		return false
	}, 5*time.Second, 5*time.Millisecond, "the flow of %s reached none of its members", groupTag)
	return reached
}

// waitForMembers polls a group's live member list until it equals the wanted set exactly.
func waitForMembers(t *testing.T, started *StartedService, groupTag string, timeout time.Duration, wanted ...string) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		members := liveGroupMembers(t, started, groupTag)
		if len(members) == len(wanted) {
			matched := true
			for index := range wanted {
				if members[index] != wanted[index] {
					matched = false
					break
				}
			}
			if matched {
				return true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func containsAll(tags []string, wanted ...string) bool {
	for _, want := range wanted {
		if !slices.Contains(tags, want) {
			return false
		}
	}
	return true
}
