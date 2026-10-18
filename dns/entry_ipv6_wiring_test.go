package dns

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// entryPresenterManagerStub is a network manager that implements only the optional entry presenter,
// which is the shape every platform manager that does not implement it must degrade from.
type entryPresenterManagerStub struct {
	adapter.NetworkManager
	presentment netcap.IPv6Presentment
}

func (s *entryPresenterManagerStub) IPv6EntryPresentment() netcap.IPv6Presentment {
	return s.presentment
}

// TestTheRouterResolvesTheEntryPresenterAtStart is the wiring test for the one line that connects
// the network manager's decision to the DNS entry.
//
// It is resolved at Start rather than at construction because the manager is built AFTER this router
// in box.go - so a test that only built the router with a managed context would pass while
// production resolved nothing, and the gate would be silently inert for every query.
func TestTheRouterResolvesTheEntryPresenterAtStart(t *testing.T) {
	manager := &entryPresenterManagerStub{presentment: netcap.SuppressIPv6}
	ctx := service.ContextWith[adapter.NetworkManager](context.Background(), manager)
	router, err := NewRouter(ctx, log.NewNOPFactory(), option.DNSOptions{})
	require.NoError(t, err)

	require.NoError(t, router.Start(adapter.StartStateStart, &adapter.Scope{}))
	require.NotNil(t, router.entryIPv6Presentment,
		"a manager in the context must be found and used, or the gate never fires in production")
	require.Equal(t, netcap.SuppressIPv6, router.entryIPv6Presentment.IPv6EntryPresentment())
}

// TestTheRouterWithoutAPresenterKeepsThePreviousBehaviour covers the optional-capability degradation.
func TestTheRouterWithoutAPresenterKeepsThePreviousBehaviour(t *testing.T) {
	var manager adapter.NetworkManager // in the context, but it cannot report the decision
	ctx := service.ContextWith[adapter.NetworkManager](context.Background(), manager)
	router, err := NewRouter(ctx, log.NewNOPFactory(), option.DNSOptions{})
	require.NoError(t, err)

	require.NoError(t, router.Start(adapter.StartStateStart, &adapter.Scope{}))
	require.Nil(t, router.entryIPv6Presentment)
}
