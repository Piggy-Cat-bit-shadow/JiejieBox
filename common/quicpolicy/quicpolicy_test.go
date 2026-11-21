package quicpolicy

import (
	"context"
	"testing"

	qtls "github.com/sagernet/sing-quic"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// TestDefaultNetworkExtensionReceiveWindows pins the numbers, because they are the whole of the
// policy and a silent edit to any of them is a silent change to every iOS extension.
func TestDefaultNetworkExtensionReceiveWindows(t *testing.T) {
	windows := DefaultNetworkExtensionReceiveWindows()
	require.Equal(t, uint64(1<<20), windows.InitialStream)
	require.Equal(t, uint64(12<<20), windows.InitialConnection)
	require.Equal(t, uint64(8<<20), windows.MaxStream)
	require.Equal(t, uint64(20<<20), windows.MaxConnection)
}

// TestFromContextDistinguishesAbsence pins the second return value: "no policy" and "a policy that
// names nothing" are both empty values, and a caller that wants to say which policy a connection
// will use must be able to tell them apart.
func TestFromContextDistinguishesAbsence(t *testing.T) {
	base := service.ContextWithDefaultRegistry(context.Background())
	_, ok := FromContext(base)
	require.False(t, ok, "a box with no policy reports absence")

	installed, ok := FromContext(NewContext(base, ReceiveWindows{}))
	require.True(t, ok, "a policy that names nothing is still a policy")
	require.Equal(t, ReceiveWindows{}, installed)
}

// TestNewContextStoresACopy keeps the policy immutable from the outside: a caller that later
// mutates its own struct must not change what a Box was constructed with.
func TestNewContextStoresACopy(t *testing.T) {
	windows := ReceiveWindows{InitialStream: 1 << 20}
	ctx := NewContext(service.ContextWithDefaultRegistry(context.Background()), windows)
	windows.InitialStream = 9 << 20

	installed, ok := FromContext(ctx)
	require.True(t, ok)
	require.Equal(t, uint64(1<<20), installed.InitialStream, "the box keeps the value it was given")
}

// TestApplyCeilingsAndGrantsNameTheirOwnFields is the property the hysteria2 caller depends on: each
// half writes only its own windows, so a protocol can take the local ceilings without touching the
// advertised grants.
func TestApplyCeilingsAndGrantsNameTheirOwnFields(t *testing.T) {
	windows := DefaultNetworkExtensionReceiveWindows()

	ceilingsOnly := &qtls.QUICOptions{}
	windows.ApplyCeilings(ceilingsOnly)
	require.Equal(t, uint64(8<<20), ceilingsOnly.MaxStreamReceiveWindow)
	require.Equal(t, uint64(20<<20), ceilingsOnly.MaxConnectionReceiveWindow)
	require.Zero(t, ceilingsOnly.InitialStreamReceiveWindow)
	require.Zero(t, ceilingsOnly.InitialConnectionReceiveWindow)

	grantsOnly := &qtls.QUICOptions{}
	windows.ApplyGrants(grantsOnly)
	require.Equal(t, uint64(1<<20), grantsOnly.InitialStreamReceiveWindow)
	require.Equal(t, uint64(12<<20), grantsOnly.InitialConnectionReceiveWindow)
	require.Zero(t, grantsOnly.MaxStreamReceiveWindow)
	require.Zero(t, grantsOnly.MaxConnectionReceiveWindow)
}

// TestApplySkipsWindowsThePolicyDoesNotName covers a partially filled policy: a zero field is
// silence, not a request to remove the caller's window.
func TestApplySkipsWindowsThePolicyDoesNotName(t *testing.T) {
	options := &qtls.QUICOptions{
		InitialStreamReceiveWindow:     8 << 20,
		MaxStreamReceiveWindow:         8 << 20,
		InitialConnectionReceiveWindow: 20 << 20,
		MaxConnectionReceiveWindow:     20 << 20,
	}
	ReceiveWindows{InitialStream: 1 << 20}.ApplyGrants(options)
	ReceiveWindows{MaxConnection: 4 << 20}.ApplyCeilings(options)
	require.Equal(t, uint64(1<<20), options.InitialStreamReceiveWindow)
	require.Equal(t, uint64(20<<20), options.InitialConnectionReceiveWindow, "not named, not touched")
	require.Equal(t, uint64(8<<20), options.MaxStreamReceiveWindow, "not named, not touched")
	require.Equal(t, uint64(4<<20), options.MaxConnectionReceiveWindow)
}

// TestStringNamesEveryWindow is the reporting contract: the outbound's log line is the only place a
// reader learns which windows were chosen, so it must name all four.
func TestStringNamesEveryWindow(t *testing.T) {
	rendered := DefaultNetworkExtensionReceiveWindows().String()
	for _, expected := range []string{"initial stream", "initial connection", "maximum stream", "maximum connection"} {
		require.Contains(t, rendered, expected)
	}
}
