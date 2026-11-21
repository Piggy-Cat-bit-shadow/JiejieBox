package receivebudget

import (
	"context"
	"testing"

	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// TestDefaultNetworkExtension pins the number, because it is the whole policy and the protocol
// owner measured it: below it the reader holds only a couple of segments and loses throughput,
// above it the bound stops being interesting on a 50 MiB budget.
func TestDefaultNetworkExtension(t *testing.T) {
	require.Equal(t, int64(256<<10), DefaultNetworkExtension().UnreadPayloadBytes)
}

// TestFromContextDistinguishesAbsence pins the second return value: "no policy" and "a policy that
// disables the mechanism" are both zero values, and the outbound that logs which one it got must be
// able to tell them apart.
func TestFromContextDistinguishesAbsence(t *testing.T) {
	base := service.ContextWithDefaultRegistry(context.Background())
	_, ok := FromContext(base)
	require.False(t, ok, "a box with no policy reports absence")

	installed, ok := FromContext(NewContext(base, Policy{}))
	require.True(t, ok, "a policy that disables the mechanism is still a policy")
	require.Zero(t, installed.UnreadPayloadBytes)
}

// TestNewContextStoresACopy keeps the policy immutable from the outside.
func TestNewContextStoresACopy(t *testing.T) {
	policy := Policy{UnreadPayloadBytes: 1 << 20}
	ctx := NewContext(service.ContextWithDefaultRegistry(context.Background()), policy)
	policy.UnreadPayloadBytes = 9 << 20

	installed, ok := FromContext(ctx)
	require.True(t, ok)
	require.Equal(t, int64(1<<20), installed.UnreadPayloadBytes)
}

// TestStringReportsDisabledRatherThanZero keeps the log line honest: a zero budget is the mechanism
// being off, not a budget of zero bytes.
func TestStringReportsDisabledRatherThanZero(t *testing.T) {
	require.Equal(t, "disabled", Policy{}.String())
	require.Equal(t, "256 kB", DefaultNetworkExtension().String())
}
