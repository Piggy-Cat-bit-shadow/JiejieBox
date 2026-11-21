package quicpolicy

// The four windows are two independent pairs, and the boundary between them is where a policy edit
// goes wrong: a grant written into a ceiling (or the reverse) would look like a smaller window while
// actually advertising or refusing a different one. These cases enumerate the pairs instead of
// sampling them, and pin the one boundary sing-quic owns: a ceiling below the grant cannot lower an
// advertised window, so it is raised to it.

import (
	"testing"

	"github.com/sagernet/quic-go"
	qtls "github.com/sagernet/sing-quic"

	"github.com/stretchr/testify/require"
)

// TestApplyNamesExactlyTheWindowsThePolicyNames walks every combination of named and unnamed windows
// and asserts each one is written by exactly one of the two calls, and only to its own field.
func TestApplyNamesExactlyTheWindowsThePolicyNames(t *testing.T) {
	names := []string{"initial stream", "initial connection", "maximum stream", "maximum connection"}
	for mask := 0; mask < 1<<len(names); mask++ {
		policy := ReceiveWindows{}
		if mask&1 != 0 {
			policy.InitialStream = 1 << 20
		}
		if mask&2 != 0 {
			policy.InitialConnection = 2 << 20
		}
		if mask&4 != 0 {
			policy.MaxStream = 4 << 20
		}
		if mask&8 != 0 {
			policy.MaxConnection = 8 << 20
		}

		options := &qtls.QUICOptions{}
		policy.ApplyCeilings(options)
		policy.ApplyGrants(options)

		require.Equal(t, policy.InitialStream, options.InitialStreamReceiveWindow, "mask %04b", mask)
		require.Equal(t, policy.InitialConnection, options.InitialConnectionReceiveWindow, "mask %04b", mask)
		require.Equal(t, policy.MaxStream, options.MaxStreamReceiveWindow, "mask %04b", mask)
		require.Equal(t, policy.MaxConnection, options.MaxConnectionReceiveWindow, "mask %04b", mask)
	}
}

// TestCeilingsNeverTouchASectionWindow pins the precedence the hysteria2 caller relies on: the
// operator's legacy single-value window is not a field this package writes, so it must survive both
// halves of the policy. The final decision belongs to qtls.ApplyQUICOptions, where a non-zero
// StreamReceiveWindow replaces BOTH values of its pair; this only proves the policy does not claim
// it first.
func TestCeilingsNeverTouchASectionWindow(t *testing.T) {
	options := &qtls.QUICOptions{StreamReceiveWindow: 16 << 20, ConnectionReceiveWindow: 32 << 20}
	DefaultNetworkExtensionReceiveWindows().ApplyCeilings(options)
	DefaultNetworkExtensionReceiveWindows().ApplyGrants(options)

	require.Equal(t, uint64(16<<20), options.StreamReceiveWindow)
	require.Equal(t, uint64(32<<20), options.ConnectionReceiveWindow)
}

// TestACeilingBelowTheGrantIsRaisedNotTheGrantLowered pins the direction of the only inconsistent
// policy a caller can hand in: an initial window larger than its own ceiling. The initial window is
// advertised, so lowering it after the fact is impossible; raising the ceiling keeps what the
// connection promised and keeps growth monotone. The point of the test is that the policy never
// silently produces Max < Initial, which would be a connection whose window can never grow.
func TestACeilingBelowTheGrantIsRaisedNotTheGrantLowered(t *testing.T) {
	policy := ReceiveWindows{
		InitialStream:     32 << 20,
		InitialConnection: 64 << 20,
		MaxStream:         8 << 20,
		MaxConnection:     20 << 20,
	}
	var options qtls.QUICOptions
	policy.ApplyCeilings(&options)
	policy.ApplyGrants(&options)

	config := &quic.Config{}
	qtls.ApplyQUICOptions(config, options)
	require.Equal(t, uint64(32<<20), config.InitialStreamReceiveWindow, "the advertised grant is not lowered")
	require.Equal(t, uint64(32<<20), config.MaxStreamReceiveWindow, "the ceiling is raised to the grant")
	require.Equal(t, uint64(64<<20), config.InitialConnectionReceiveWindow)
	require.Equal(t, uint64(64<<20), config.MaxConnectionReceiveWindow)
	require.GreaterOrEqual(t, config.MaxStreamReceiveWindow, config.InitialStreamReceiveWindow)
	require.GreaterOrEqual(t, config.MaxConnectionReceiveWindow, config.InitialConnectionReceiveWindow)
}
