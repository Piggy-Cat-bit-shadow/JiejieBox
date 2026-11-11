package endpoint

// The endpoint manager's duplicate-tag path builds an object and then discards it. Before this
// test the discard was `common.Close(endpoint)`, which only works if the endpoint's Close releases
// what its constructor acquired; nothing asserted that the loser is closed, so a leaked HTTP client
// or transport would have been invisible.

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

// countingEndpoint is an endpoint whose Close is observable and whose lifecycle methods are inert.
// The embedded Adapter supplies Type/Tag/Network/Dependencies, which is the same base every real
// endpoint uses.
type countingEndpoint struct {
	Adapter
	closes    atomic.Int32
	startedAt atomic.Int32
}

func newCountingEndpoint(tag string) *countingEndpoint {
	return &countingEndpoint{Adapter: NewAdapter("test-endpoint", tag, []string{N.NetworkTCP}, nil)}
}

func (e *countingEndpoint) Start(stage adapter.StartStage, _ *adapter.Scope) error {
	e.startedAt.Add(1)
	return nil
}

func (e *countingEndpoint) Close() error {
	e.closes.Add(1)
	return nil
}

func (e *countingEndpoint) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, E.New("counting endpoint: not dialable")
}

func (e *countingEndpoint) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, E.New("counting endpoint: not dialable")
}

// createCountingEndpoint is the constructor the fake registry hands to the manager.
func createCountingEndpoint(tag string) adapter.Endpoint {
	return newCountingEndpoint(tag)
}

// countingRegistry returns a fresh endpoint per Create call, and can be made slow so two Create
// calls really interleave.
type countingRegistry struct {
	access  sync.Mutex
	created []*countingEndpoint
	// barrier, when set, holds both constructors until both have entered, so the duplicate check
	// really races instead of the second call being rejected by the pre-check.
	barrier *sync.WaitGroup
}

func (r *countingRegistry) Create(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ string, _ any) (adapter.Endpoint, error) {
	if r.barrier != nil {
		r.barrier.Done()
		r.barrier.Wait()
	}
	endpoint := newCountingEndpoint(tag)
	r.access.Lock()
	r.created = append(r.created, endpoint)
	r.access.Unlock()
	return endpoint, nil
}

func (r *countingRegistry) CreateOptions(string) (any, bool) { return nil, false }
func (r *countingRegistry) OptionTypes() []string           { return nil }

// TestDuplicateTagCreateClosesTheLoserExactlyOnce: two Create calls race for one tag, exactly one
// endpoint is installed, and the object the loser built is closed rather than leaked.
func TestDuplicateTagCreateClosesTheLoserExactlyOnce(t *testing.T) {
	registry := &countingRegistry{barrier: &sync.WaitGroup{}}
	registry.barrier.Add(2)
	manager := NewManager(registry)

	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	results := make([]error, 2)
	for index := 0; index < 2; index++ {
		go func(slot int) {
			defer waitGroup.Done()
			results[slot] = manager.Create(context.Background(), nil, logger.NOP(), "shared", "test-endpoint", nil)
		}(index)
	}
	waitGroup.Wait()

	installed, found := manager.Get("shared")
	require.True(t, found)
	require.Equal(t, "shared", installed.Tag())
	require.Equal(t, 1, len(manager.Endpoints()), "only one endpoint may be installed")

	registry.access.Lock()
	created := append([]*countingEndpoint(nil), registry.created...)
	registry.access.Unlock()
	require.Len(t, created, 2, "both Create calls ran their constructor")

	var closed, open int
	for _, candidate := range created {
		if candidate.closes.Load() > 0 {
			closed++
		} else {
			open++
		}
	}
	require.Equal(t, 1, closed, "the discarded endpoint must be closed")
	require.Equal(t, 1, open, "the installed endpoint must stay open")
	for _, candidate := range created {
		if candidate.closes.Load() == 0 {
			require.Equal(t, installed.Tag(), candidate.Tag())
		}
	}
	failures := 0
	for _, createErr := range results {
		if createErr != nil {
			failures++
		}
	}
	require.Equal(t, 1, failures, "exactly one of the two creates must report the duplicate")
}

// TestRepeatedCreateAfterInstallRefusesWithoutBuilding: the pre-check must reject a duplicate before
// the constructor runs, so a second Create with a known tag leaks nothing at all.
func TestRepeatedCreateAfterInstallRefusesWithoutBuilding(t *testing.T) {
	registry := &countingRegistry{}
	manager := NewManager(registry)

	require.NoError(t, manager.Create(context.Background(), nil, logger.NOP(), "solo", "test-endpoint", nil))
	require.Error(t, manager.Create(context.Background(), nil, logger.NOP(), "solo", "test-endpoint", nil))

	registry.access.Lock()
	defer registry.access.Unlock()
	require.Len(t, registry.created, 1, "the duplicate was rejected before constructing anything")
	require.Equal(t, int32(0), registry.created[0].closes.Load())
}

// TestCloseOfANeverStartedEndpointIsSafe is the manager's own contract with a loser: the object it
// discards has never been started, and closing it must not hang, panic or need a scope.
func TestCloseOfANeverStartedEndpointIsSafe(t *testing.T) {
	endpoint := createCountingEndpoint("never-started")
	require.NoError(t, common.Close(endpoint))
	require.Equal(t, int32(1), endpoint.(*countingEndpoint).closes.Load())
	require.Equal(t, int32(0), endpoint.(*countingEndpoint).startedAt.Load())
}
