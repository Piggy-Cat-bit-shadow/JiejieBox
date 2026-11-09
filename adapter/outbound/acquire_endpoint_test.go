package outbound

import (
	"context"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// leaseEndpoint is a leaf that is published through the endpoint namespace instead of the
// outbound one. Endpoints have no retirement lifecycle: the endpoint manager rejects a second
// object for a tag, so a published endpoint is stable for the life of the manager.
type leaseEndpoint struct {
	*graphNode
}

func (e *leaseEndpoint) Start(adapter.StartStage, *adapter.Scope) error { return nil }

// leaseEndpointManager is the endpoint half of the namespace Manager.Outbound falls back to.
type leaseEndpointManager struct {
	access sync.Mutex
	byTag  map[string]adapter.Endpoint
}

func (m *leaseEndpointManager) Start(adapter.StartStage, *adapter.Scope) error { return nil }
func (m *leaseEndpointManager) StartEndpoint(adapter.Endpoint) error           { return nil }
func (m *leaseEndpointManager) Endpoints() []adapter.Endpoint {
	m.access.Lock()
	defer m.access.Unlock()
	endpoints := make([]adapter.Endpoint, 0, len(m.byTag))
	for _, endpoint := range m.byTag {
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}
func (m *leaseEndpointManager) Get(tag string) (adapter.Endpoint, bool) {
	m.access.Lock()
	defer m.access.Unlock()
	endpoint, found := m.byTag[tag]
	return endpoint, found
}
func (m *leaseEndpointManager) Create(context.Context, adapter.Router, log.ContextLogger, string, string, any) error {
	return nil
}

func newEndpointManagerWith(tag string, endpoint adapter.Endpoint) *leaseEndpointManager {
	return &leaseEndpointManager{byTag: map[string]adapter.Endpoint{tag: endpoint}}
}

// TestAcquireOutboundLeasesAPublishedEndpoint pins the defect that made an AWG endpoint route
// unusable: the lease manager only knew the outbound namespace, so a leaf published through the
// endpoint namespace was reported as "replaced" on every hand-off - including the retry that
// re-read the same, live endpoint by tag - and the flow was refused with
// "outbound wg-cli was replaced while the connection was being set up".
func TestAcquireOutboundLeasesAPublishedEndpoint(t *testing.T) {
	endpoint := &leaseEndpoint{graphNode: &graphNode{tag: "wg-cli"}}
	manager := NewManager(&graphRegistry{}, newEndpointManagerWith("wg-cli", endpoint), "")
	manager.Initialize(func() (adapter.Outbound, error) {
		return &graphNode{tag: "direct"}, nil
	})

	// This is what the router's retry path resolves the tag to.
	resolved, found := manager.Outbound("wg-cli")
	require.True(t, found)
	require.Same(t, endpoint, resolved)

	release, ok := manager.AcquireOutbound(endpoint)
	require.True(t, ok, "a published endpoint must be leasable for the hand-off")
	require.NotNil(t, release)
	release()

	require.EqualValues(t, 0, endpoint.closed.Load(), "leasing an endpoint must not close it")
}

// TestAcquireOutboundRejectsAnUnpublishedEndpoint keeps the lease from degenerating into "any
// object whose tag exists in the endpoint namespace is dialable": identity still decides.
func TestAcquireOutboundRejectsAnUnpublishedEndpoint(t *testing.T) {
	published := &leaseEndpoint{graphNode: &graphNode{tag: "wg-cli"}}
	other := &leaseEndpoint{graphNode: &graphNode{tag: "wg-cli"}}
	manager := NewManager(&graphRegistry{}, newEndpointManagerWith("wg-cli", published), "")
	manager.Initialize(func() (adapter.Outbound, error) {
		return &graphNode{tag: "direct"}, nil
	})

	release, ok := manager.AcquireOutbound(other)
	if release != nil {
		release()
	}
	require.False(t, ok, "an object that is not the published endpoint must not be leased")
}
