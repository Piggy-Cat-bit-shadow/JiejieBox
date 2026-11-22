package clashapi

// Fixtures shared by the Clash API list-endpoint tests and the A/N2 measurement probe.
//
// They are deliberately plain: a node is a tag, a type, a network set and (for a group) a member
// list. Nothing here dials, so a fixture with ten thousand nodes is only ten thousand small structs
// and the tests measure the HANDLER's encoding cost rather than a fake node's behaviour.

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// fixtureOutbound is a leaf node.
type fixtureOutbound struct {
	tag      string
	typ      string
	networks []string
}

func (o *fixtureOutbound) Type() string           { return o.typ }
func (o *fixtureOutbound) Tag() string            { return o.tag }
func (o *fixtureOutbound) Network() []string      { return o.networks }
func (o *fixtureOutbound) Dependencies() []string { return nil }

func (o *fixtureOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, E.New("fixture outbound is not dialable")
}

func (o *fixtureOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, E.New("fixture outbound is not dialable")
}

// fixtureGroup is an OutboundGroup with a fixed member list and a fixed selection.
type fixtureGroup struct {
	*fixtureOutbound
	members  []adapter.Outbound
	selected adapter.Outbound
}

func (g *fixtureGroup) All() []string {
	tags := make([]string, 0, len(g.members))
	for _, member := range g.members {
		tags = append(tags, member.Tag())
	}
	return tags
}

func (g *fixtureGroup) Selected(network string) adapter.Outbound { return g.selected }
func (g *fixtureGroup) AttachConnection(io.Closer) func()        { return func() {} }
func (g *fixtureGroup) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, E.New("fixture group is not dialable")
}

func (g *fixtureGroup) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, E.New("fixture group is not dialable")
}

// fixtureFlowAwareGroup is a group whose member is a property of the flow, so the node list must
// omit `now` for it.
type fixtureFlowAwareGroup struct {
	*fixtureGroup
}

func (g *fixtureFlowAwareGroup) SelectForFlow(metadata *adapter.InboundContext, network string, commit bool) adapter.Outbound {
	return g.selected
}

// fixtureHiddenGroup is a group that must not be presented as a group of its own by a group LISTING
// (the daemon's group screen). The Clash API's node list is not such a listing: it has always shown
// hidden groups, so the fixture pins that behaviour instead of "fixing" it.
type fixtureHiddenGroup struct {
	*fixtureGroup
}

func (g *fixtureHiddenGroup) Hidden() bool { return true }

// fixtureEndpoint is an adapter.Endpoint wrapping a leaf outbound.
type fixtureEndpoint struct {
	*fixtureOutbound
}

func (e *fixtureEndpoint) Start(stage adapter.StartStage, scope *adapter.Scope) error { return nil }

type fixtureOutboundManager struct {
	adapter.OutboundManager
	list  []adapter.Outbound
	byTag map[string]adapter.Outbound
	def   adapter.Outbound
}

func (m *fixtureOutboundManager) Outbounds() []adapter.Outbound { return m.list }
func (m *fixtureOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	outbound, loaded := m.byTag[tag]
	return outbound, loaded
}
func (m *fixtureOutboundManager) Default() adapter.Outbound { return m.def }

type fixtureEndpointManager struct {
	adapter.EndpointManager
	list []adapter.Endpoint
}

func (m *fixtureEndpointManager) Endpoints() []adapter.Endpoint { return m.list }

type fixtureRouter struct {
	adapter.Router
	rules []adapter.Rule
}

func (r *fixtureRouter) Rules() []adapter.Rule { return r.rules }

type fixtureRuleAction struct {
	typ  string
	text string
}

func (a fixtureRuleAction) Type() string   { return a.typ }
func (a fixtureRuleAction) String() string { return a.text }

type fixtureRule struct {
	typ     string
	payload string
	action  fixtureRuleAction
}

func (r *fixtureRule) Type() string                                { return r.typ }
func (r *fixtureRule) String() string                              { return r.payload }
func (r *fixtureRule) Action() adapter.RuleAction                  { return r.action }
func (r *fixtureRule) Match(metadata *adapter.InboundContext) bool { return false }
func (r *fixtureRule) Start() error                                { return nil }
func (r *fixtureRule) Close() error                                { return nil }

// proxiesFixture is a Server wired to deterministic fake managers.
type proxiesFixture struct {
	server    *Server
	manager   *fixtureOutboundManager
	endpoints *fixtureEndpointManager
}

// newProxiesFixture builds nodeCount outbounds:
//
//	DIRECT, REJECT, DNS          - not part of GLOBAL.all, still present in `proxies`
//	node-00003 .. node-N         - leaves; every fourth carries a delay history
//	group-selector               - 8 members, selected = member 0
//	group-urltest                - 4 members, selected = member 2
//	group-balance                - flow-aware, selected = member 3
//	group-hidden                 - a hidden but fully working selector
//
// nodeCount is the total number of outbounds; it is clamped to at least 16 so the group fixtures
// always have members.
func newProxiesFixture(t testing.TB, nodeCount int) *proxiesFixture {
	t.Helper()
	if nodeCount < 16 {
		nodeCount = 16
	}

	direct := &fixtureOutbound{tag: "DIRECT", typ: C.TypeDirect, networks: []string{N.NetworkTCP, N.NetworkUDP}}
	block := &fixtureOutbound{tag: "REJECT", typ: C.TypeBlock, networks: []string{N.NetworkTCP}}
	dns := &fixtureOutbound{tag: "DNS", typ: C.TypeDNS, networks: []string{N.NetworkTCP, N.NetworkUDP}}

	leaves := make([]adapter.Outbound, 0, nodeCount)
	all := make([]adapter.Outbound, 0, nodeCount+4)
	all = append(all, direct, block, dns)
	for i := 3; i < nodeCount-4; i++ {
		leaf := &fixtureOutbound{
			tag:      fmt.Sprintf("node-%05d", i),
			typ:      "shadowsocks",
			networks: []string{N.NetworkTCP, N.NetworkUDP},
		}
		leaves = append(leaves, leaf)
		all = append(all, leaf)
	}
	if len(leaves) < 8 {
		t.Fatalf("fixture needs at least 8 leaves, got %d", len(leaves))
	}

	selector := &fixtureGroup{
		fixtureOutbound: &fixtureOutbound{tag: "group-selector", typ: "selector", networks: []string{N.NetworkTCP, N.NetworkUDP}},
		members:         leaves[:8],
		selected:        leaves[0],
	}
	urlTest := &fixtureGroup{
		fixtureOutbound: &fixtureOutbound{tag: "group-urltest", typ: "urltest", networks: []string{N.NetworkTCP, N.NetworkUDP}},
		members:         leaves[:4],
		selected:        leaves[2],
	}
	balance := &fixtureFlowAwareGroup{&fixtureGroup{
		fixtureOutbound: &fixtureOutbound{tag: "group-balance", typ: "loadbalance", networks: []string{N.NetworkTCP, N.NetworkUDP}},
		members:         leaves[:8],
		selected:        leaves[3],
	}}
	hidden := &fixtureHiddenGroup{&fixtureGroup{
		fixtureOutbound: &fixtureOutbound{tag: "group-hidden", typ: "selector", networks: []string{N.NetworkTCP, N.NetworkUDP}},
		members:         leaves[:2],
		selected:        leaves[1],
	}}

	all = append(all, selector, urlTest, balance, hidden)

	byTag := make(map[string]adapter.Outbound, len(all))
	for _, outbound := range all {
		byTag[outbound.Tag()] = outbound
	}

	endpoints := &fixtureEndpointManager{list: []adapter.Endpoint{
		&fixtureEndpoint{&fixtureOutbound{tag: "endpoint-00001", typ: "wireguard", networks: []string{N.NetworkTCP, N.NetworkUDP}}},
	}}

	history := urltest.NewHistoryStorage()
	stamp := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	for index, leaf := range leaves {
		if index%4 != 0 {
			continue
		}
		history.StoreDisplayHistory(leaf.Tag(), urltest.MeasurementScope{}, &adapter.URLTestHistory{
			Time:  stamp.Add(time.Duration(index) * time.Second),
			Delay: uint16(10 + index),
		})
	}

	manager := &fixtureOutboundManager{
		list:  all,
		byTag: byTag,
		def:   leaves[0],
	}
	server := &Server{
		outbound:       manager,
		endpoint:       endpoints,
		urlTestHistory: history,
	}
	return &proxiesFixture{server: server, manager: manager, endpoints: endpoints}
}

// emptyProxiesFixture is the degenerate case: no outbounds, no endpoint manager entries.
func emptyProxiesFixture() *proxiesFixture {
	manager := &fixtureOutboundManager{byTag: map[string]adapter.Outbound{}, def: &fixtureOutbound{tag: ""}}
	return &proxiesFixture{
		server: &Server{
			outbound:       manager,
			endpoint:       &fixtureEndpointManager{},
			urlTestHistory: urltest.NewHistoryStorage(),
		},
		manager:   manager,
		endpoints: &fixtureEndpointManager{},
	}
}

// newRulesFixture builds ruleCount rules whose payloads exercise escaping, length and empty fields.
func newRulesFixture(ruleCount int) *fixtureRouter {
	rules := make([]adapter.Rule, 0, ruleCount)
	for i := 0; i < ruleCount; i++ {
		payload := fmt.Sprintf("domain_suffix,node-%05d.example", i)
		switch i % 7 {
		case 3:
			payload = "domain,<html>&\"quoted\"\\slash"
		case 5:
			payload = ""
		case 6:
			payload = fmt.Sprintf("domain_keyword,%s", longFixtureName(i))
		}
		proxy := fmt.Sprintf("proxy-%05d", i%97)
		switch i % 11 {
		case 4:
			proxy = "REJECT"
		case 9:
			proxy = ""
		}
		rules = append(rules, &fixtureRule{
			typ:     "default",
			payload: payload,
			action:  fixtureRuleAction{typ: "route", text: proxy},
		})
	}
	return &fixtureRouter{rules: rules}
}

// longFixtureName returns a name longer than the JSON encoder's small-buffer path.
func longFixtureName(seed int) string {
	name := make([]byte, 0, 300)
	name = append(name, "long"...)
	for len(name) < 260 {
		name = append(name, byte('a'+seed%26))
	}
	return string(name)
}

// discardResponseWriter is a ResponseWriter that keeps only the status and the byte count.
//
// A httptest.ResponseRecorder stores the whole body, and that storage belongs to the CLIENT side of
// the measurement: counting it would attribute the response size to the server's encoder. The probe
// therefore discards bytes and records the length only, and the body itself is validated separately
// over a real socket.
type discardResponseWriter struct {
	header http.Header
	status int
	bytes  int64
}

func newDiscardResponseWriter() *discardResponseWriter {
	return &discardResponseWriter{header: make(http.Header)}
}

func (w *discardResponseWriter) Header() http.Header { return w.header }

func (w *discardResponseWriter) Write(p []byte) (int, error) {
	w.bytes += int64(len(p))
	return len(p), nil
}

func (w *discardResponseWriter) WriteHeader(status int) { w.status = status }

// Flush makes the writer satisfy http.Flusher as a real ResponseWriter does.
func (w *discardResponseWriter) Flush() {}

// serveReset runs one handler invocation and returns only its status and body size.
func serveReset(handler http.HandlerFunc, request *http.Request) *discardResponseWriter {
	writer := newDiscardResponseWriter()
	handler(writer, request)
	return writer
}

var _ http.Flusher = (*discardResponseWriter)(nil)
var _ = httptest.NewRequest
