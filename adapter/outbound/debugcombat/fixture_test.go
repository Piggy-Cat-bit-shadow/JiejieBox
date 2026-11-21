// Package debugcombat holds the cross-system fixtures X owns.
//
// The fixtures here are deliberately OUTSIDE the packages they attack: they reach the production
// code through its exported surface (Manager, Replace/ReplaceOwned, Remove, UpdateGroupMembership)
// and implement the same interfaces the real objects do, so an attack cannot be satisfied by a
// helper that the production path does not use.
package debugcombat

import (
	"context"
	"io"
	"net"
	"os"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// stubOutbound is the smallest object the manager can publish: it declares edges and reports
// whether it was closed. Every graph attack needs exactly those two facts.
type stubOutbound struct {
	tag          string
	deps         []string
	references   []string
	closedAccess sync.Mutex
	closed       int
}

func newStubOutbound(tag string, deps ...string) *stubOutbound {
	return &stubOutbound{tag: tag, deps: deps}
}

func (o *stubOutbound) Type() string           { return "stub" }
func (o *stubOutbound) Tag() string            { return o.tag }
func (o *stubOutbound) Network() []string      { return []string{N.NetworkTCP} }
func (o *stubOutbound) Dependencies() []string { return o.deps }
func (o *stubOutbound) References() []string   { return o.references }
func (o *stubOutbound) CloseCount() int {
	o.closedAccess.Lock()
	defer o.closedAccess.Unlock()
	return o.closed
}
func (o *stubOutbound) Close() error {
	o.closedAccess.Lock()
	o.closed++
	o.closedAccess.Unlock()
	return nil
}
func (o *stubOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, os.ErrInvalid
}
func (o *stubOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

// stubGroup is a group whose published membership the test controls. It is the attacker's handle
// on the UNION edge set: membership is what turns a published node into an edge.
type stubGroup struct {
	*stubOutbound
	access  sync.Mutex
	members []string
}

func newStubGroup(tag string, members ...string) *stubGroup {
	return &stubGroup{stubOutbound: newStubOutbound(tag), members: members}
}

func (g *stubGroup) All() []string {
	g.access.Lock()
	defer g.access.Unlock()
	return append([]string(nil), g.members...)
}

func (g *stubGroup) Selected(network string) adapter.Outbound { return nil }

// publishMembers is the group half of a membership update: a real group stores the list inside the
// commit callback, and the manager validates that prospective list BEFORE calling it. A fixture
// whose commit did nothing would validate a graph the live objects never enter.
func (g *stubGroup) publishMembers(members []string) {
	g.access.Lock()
	g.members = append([]string(nil), members...)
	g.access.Unlock()
}

func (g *stubGroup) AttachConnection(closer io.Closer) (detach func()) { return func() {} }

// stubOptions carries the object a stub constructor should return. The real registry is used, so
// the manager's construction path - options type, registry lookup, constructor - is the production
// one rather than a test shortcut.
type stubOptions struct {
	object adapter.Outbound
}

const stubType = "debugcombat-stub"

func newStubRegistry() *outbound.Registry {
	registry := outbound.NewRegistry()
	outbound.Register[stubOptions](registry, stubType, func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options stubOptions) (adapter.Outbound, error) {
		if options.object == nil {
			return nil, os.ErrInvalid
		}
		return options.object, nil
	})
	return registry
}

func newStubManager(t *testing.T) *outbound.Manager {
	t.Helper()
	return outbound.NewManager(newStubRegistry(), nil, "")
}

var stubLogger = log.NewNOPFactory().Logger()

// publish installs an object at startup through the manager's Create verb, which is the unvalidated
// publication path box.New uses for a static configuration: the start-order sort validated the
// declared edges before it, so Create itself does not re-walk the graph.
func publish(t *testing.T, manager *outbound.Manager, object adapter.Outbound) {
	t.Helper()
	err := manager.Create(context.Background(), nil, stubLogger, object.Tag(), stubType, &stubOptions{object: object})
	if err != nil {
		t.Fatalf("publish %s: %v", object.Tag(), err)
	}
}

func replaceWith(t *testing.T, manager *outbound.Manager, object adapter.Outbound) error {
	t.Helper()
	return manager.Replace(context.Background(), nil, stubLogger, object.Tag(), stubType, &stubOptions{object: object})
}

// referencedBy reports whether tag resolves to want, which is how a test proves the manager still
// routes to the object it had before a refused mutation.
func referencedBy(t *testing.T, manager *outbound.Manager, tag string, want adapter.Outbound) bool {
	t.Helper()
	got, loaded := manager.Outbound(tag)
	return loaded && got == want
}
