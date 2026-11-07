package provider_test

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/adapter/provider"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/group"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// gatedMember is a leaf that lets the test stop a dial between "the group already handed out this
// object" and "the object has finished establishing the connection".
type gatedMember struct {
	outbound.Adapter
	entered  chan struct{}
	release  chan struct{}
	closed   atomic.Int32
	active   atomic.Int32
	enterOne sync.Once
}

type gatedOptions struct{ Revision int }

func (m *gatedMember) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	m.active.Add(1)
	m.enterOne.Do(func() { close(m.entered) })
	<-m.release
	if m.closed.Load() != 0 {
		return nil, net.ErrClosed
	}
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		_, _ = io.Copy(server, server)
	}()
	return client, nil
}

func (m *gatedMember) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	m.active.Add(1)
	m.enterOne.Do(func() { close(m.entered) })
	<-m.release
	if m.closed.Load() != 0 {
		return nil, net.ErrClosed
	}
	return &gatedPacketConn{}, nil
}

func (m *gatedMember) Close() error {
	m.closed.Add(1)
	return nil
}

type gatedPacketConn struct{}

func (c *gatedPacketConn) Read([]byte) (int, error) { return 0, io.EOF }
func (c *gatedPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, io.EOF
}
func (c *gatedPacketConn) WriteTo(b []byte, _ net.Addr) (int, error) { return len(b), nil }
func (c *gatedPacketConn) Write(b []byte) (int, error)               { return len(b), nil }
func (c *gatedPacketConn) Close() error                              { return nil }
func (c *gatedPacketConn) LocalAddr() net.Addr                       { return &net.TCPAddr{} }
func (c *gatedPacketConn) SetDeadline(time.Time) error               { return nil }
func (c *gatedPacketConn) SetReadDeadline(time.Time) error           { return nil }
func (c *gatedPacketConn) SetWriteDeadline(time.Time) error          { return nil }

type gatedWorld struct {
	manager  *outbound.Manager
	selector *group.Selector
	provider *provider.Adapter
	scope    *adapter.Scope
}

func newGatedWorld(t *testing.T) *gatedWorld {
	t.Helper()
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	factory := log.NewNOPFactory()
	logger := factory.Logger()
	registry := outbound.NewRegistry()
	var created *gatedMember
	outbound.Register[gatedOptions](registry, "gated", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ gatedOptions) (adapter.Outbound, error) {
		created = &gatedMember{
			Adapter: outbound.NewAdapter("gated", tag, []string{"tcp", "udp"}, nil),
			entered: make(chan struct{}),
			release: make(chan struct{}),
		}
		return created, nil
	})
	endpointManager := endpoint.NewManager(endpoint.NewRegistry())
	manager := outbound.NewManager(registry, endpointManager, "")
	manager.Initialize(func() (adapter.Outbound, error) {
		return &gatedMember{Adapter: outbound.NewAdapter("gated", "direct", []string{"tcp", "udp"}, nil)}, nil
	})
	scope := adapter.NewScope(ctx, logger)
	t.Cleanup(func() { _ = scope.Close() })
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, scope.Start("endpoint", endpointManager, stage))
		require.NoError(t, scope.Start("outbound", manager, stage))
	}
	inline := provider.NewAdapter(ctx, nil, manager, factory, logger, "p", "inline", option.ProviderHealthCheckOptions{})
	require.NoError(t, inline.Start())
	t.Cleanup(func() { _ = inline.Close() })
	ctx = service.ContextWith[adapter.OutboundManager](ctx, manager)
	ctx = service.ContextWith[adapter.ProviderManager](ctx, &replacementProviderManager{providers: map[string]adapter.Provider{
		"p": &replacementProvider{Adapter: &inline},
	}})
	createdSelector, err := group.NewSelector(ctx, nil, logger, "g", option.SelectorOutboundOptions{
		Providers: []string{"p"},
	})
	require.NoError(t, err)
	selector := createdSelector.(*group.Selector)
	require.NoError(t, selector.Start(adapter.StartStateStart, scope))
	return &gatedWorld{manager: manager, selector: selector, provider: &inline, scope: scope}
}

func (w *gatedWorld) publish(t *testing.T, revision int) {
	t.Helper()
	require.NoError(t, w.provider.UpdateNodes([]option.Outbound{{Type: "gated", Tag: "X", Options: &gatedOptions{Revision: revision}}}))
	require.NoError(t, w.provider.UpdateGroups())
}

// TestDialHoldingTheOldSelectionSurvivesConcurrentPublication is the deterministic interleaving:
//
//  1. the dial goroutine has already obtained the selected object and is inside its DialContext;
//  2. the main goroutine replaces p/X, notifies the group and completes the publication;
//  3. the dial goroutine resumes.
//
// The old object must still be alive at step 2: closing it here hands an in-flight dial a closed
// object even though a valid replacement is published. After the dial hands back its connection the
// retired object may close, exactly once.
func TestDialHoldingTheOldSelectionSurvivesConcurrentPublication(t *testing.T) {
	world := newGatedWorld(t)
	world.publish(t, 1)
	first, loaded := world.manager.Outbound("p/X")
	require.True(t, loaded)
	member := first.(*gatedMember)

	dialErr := make(chan error, 1)
	go func() {
		conn, err := world.selector.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:1"))
		if conn != nil {
			_ = conn.Close()
		}
		dialErr <- err
	}()
	<-member.entered

	// Replace and publish while the dial is between "obtained the member" and "handed back a conn".
	require.NoError(t, world.provider.UpdateNodes([]option.Outbound{{Type: "gated", Tag: "X", Options: &gatedOptions{Revision: 2}}}))
	require.NoError(t, world.provider.UpdateGroups())
	require.EqualValues(t, 0, member.closed.Load(),
		"an in-flight dial still owns the previous object; the publication barrier must not close it")

	close(member.release)
	require.NoError(t, <-dialErr, "the in-flight dial must complete against the still-open object")

	deadline := time.Now().Add(2 * time.Second)
	for member.closed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	require.EqualValues(t, 1, member.closed.Load(),
		"after the last user leaves, the retired object is closed exactly once")
}

func waitForActive(t *testing.T, member *gatedMember, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for member.active.Load() != want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	require.Equal(t, want, member.active.Load())
}

func waitForClose(t *testing.T, member *gatedMember, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for member.closed.Load() != want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	require.Equal(t, want, member.closed.Load())
}

// TestMultipleDialUsersKeepTheOldObjectAlive proves the lease is a count, not a boolean: several
// hand-offs that obtained the same retired object keep it open until the last one finishes.
func TestMultipleDialUsersKeepTheOldObjectAlive(t *testing.T) {
	world := newGatedWorld(t)
	world.publish(t, 1)
	first, loaded := world.manager.Outbound("p/X")
	require.True(t, loaded)
	member := first.(*gatedMember)

	const users = 3
	results := make(chan error, users)
	for index := 0; index < users; index++ {
		go func() {
			conn, err := world.selector.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:1"))
			if conn != nil {
				_ = conn.Close()
			}
			results <- err
		}()
	}
	waitForActive(t, member, users)

	require.NoError(t, world.provider.UpdateNodes([]option.Outbound{{Type: "gated", Tag: "X", Options: &gatedOptions{Revision: 2}}}))
	require.NoError(t, world.provider.UpdateGroups())
	require.EqualValues(t, 0, member.closed.Load(), "three in-flight dials still own the object")

	close(member.release)
	for index := 0; index < users; index++ {
		require.NoError(t, <-results)
	}
	waitForClose(t, member, 1)
}

// TestRepeatedReplacementClosesOnlyUnusedRevisions pins the resource rule: a revision that no dial
// ever obtained is closed at the publication barrier, while the revision still inside a dial stays
// open until that dial returns (each exactly once).
func TestRepeatedReplacementClosesOnlyUnusedRevisions(t *testing.T) {
	world := newGatedWorld(t)
	world.publish(t, 1)
	first, loaded := world.manager.Outbound("p/X")
	require.True(t, loaded)
	member := first.(*gatedMember)

	dialErr := make(chan error, 1)
	go func() {
		conn, err := world.selector.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:1"))
		if conn != nil {
			_ = conn.Close()
		}
		dialErr <- err
	}()
	<-member.entered

	require.NoError(t, world.provider.UpdateNodes([]option.Outbound{{Type: "gated", Tag: "X", Options: &gatedOptions{Revision: 2}}}))
	second, loaded := world.manager.Outbound("p/X")
	require.True(t, loaded)
	require.NotSame(t, first, second)
	require.NoError(t, world.provider.UpdateGroups())
	require.EqualValues(t, 0, member.closed.Load(), "the leased revision survives the first barrier")
	require.EqualValues(t, 0, second.(*gatedMember).active.Load(),
		"the unpublished revision never entered a dial")

	require.NoError(t, world.provider.UpdateNodes([]option.Outbound{{Type: "gated", Tag: "X", Options: &gatedOptions{Revision: 3}}}))
	third, loaded := world.manager.Outbound("p/X")
	require.True(t, loaded)
	require.NoError(t, world.provider.UpdateGroups())
	require.EqualValues(t, 0, member.closed.Load(), "the leased revision still survives")
	require.Same(t, third, world.selector.Selected("tcp"))
	waitForClose(t, second.(*gatedMember), 1)

	close(member.release)
	require.NoError(t, <-dialErr)
	waitForClose(t, member, 1)
}

// TestListenPacketHoldingTheOldSelectionSurvivesConcurrentPublication is the packet half of the
// deterministic interleaving: ListenPacket obtained the member and is inside it when the publication
// barrier runs.
func TestListenPacketHoldingTheOldSelectionSurvivesConcurrentPublication(t *testing.T) {
	world := newGatedWorld(t)
	world.publish(t, 1)
	first, loaded := world.manager.Outbound("p/X")
	require.True(t, loaded)
	member := first.(*gatedMember)

	packetErr := make(chan error, 1)
	go func() {
		conn, err := world.selector.ListenPacket(context.Background(), M.ParseSocksaddr("127.0.0.1:1"))
		if conn != nil {
			_ = conn.Close()
		}
		packetErr <- err
	}()
	<-member.entered

	require.NoError(t, world.provider.UpdateNodes([]option.Outbound{{Type: "gated", Tag: "X", Options: &gatedOptions{Revision: 2}}}))
	require.NoError(t, world.provider.UpdateGroups())
	require.EqualValues(t, 0, member.closed.Load())

	close(member.release)
	require.NoError(t, <-packetErr)
	waitForClose(t, member, 1)
}

// TestParallelDialHoldingTheOldSelectionSurvivesConcurrentPublication covers DialParallelNetwork:
// the selected member is obtained through the same leased path before the candidate list is dialled.
func TestParallelDialHoldingTheOldSelectionSurvivesConcurrentPublication(t *testing.T) {
	world := newGatedWorld(t)
	world.publish(t, 1)
	first, loaded := world.manager.Outbound("p/X")
	require.True(t, loaded)
	member := first.(*gatedMember)

	dialErr := make(chan error, 1)
	go func() {
		conn, err := world.selector.DialParallelNetwork(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:1"), nil, nil, nil, nil, 0)
		if conn != nil {
			_ = conn.Close()
		}
		dialErr <- err
	}()
	<-member.entered

	require.NoError(t, world.provider.UpdateNodes([]option.Outbound{{Type: "gated", Tag: "X", Options: &gatedOptions{Revision: 2}}}))
	require.NoError(t, world.provider.UpdateGroups())
	require.EqualValues(t, 0, member.closed.Load())

	close(member.release)
	require.NoError(t, <-dialErr)
	waitForClose(t, member, 1)
}
