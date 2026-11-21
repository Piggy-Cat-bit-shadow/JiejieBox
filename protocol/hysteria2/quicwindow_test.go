package hysteria2

import (
	"testing"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/sing-box/common/quicpolicy"
	qtls "github.com/sagernet/sing-quic"

	"github.com/stretchr/testify/require"
)

// hysteria2DefaultQUICConfig is the quic.Config the sing-quic hysteria2 client starts from, before
// it applies this outbound's options: the hysteria defaults, 8 MiB per stream and 20 MiB per
// connection, used as BOTH the initial window and the ceiling. Emulating it here is what makes the
// assertions below statements about the connection that is dialled rather than about an
// intermediate struct.
func hysteria2DefaultQUICConfig() *quic.Config {
	return &quic.Config{
		InitialStreamReceiveWindow:     8 << 20,
		MaxStreamReceiveWindow:         8 << 20,
		InitialConnectionReceiveWindow: 20 << 20,
		MaxConnectionReceiveWindow:     20 << 20,
	}
}

// appliedQUICConfig runs the policy through the same two steps the client does and returns the
// resulting configuration.
func appliedQUICConfig(t *testing.T, options qtls.QUICOptions) *quic.Config {
	t.Helper()
	config := hysteria2DefaultQUICConfig()
	qtls.ApplyQUICOptions(config, options)
	return config
}

// TestApplyReceiveWindowPolicyWithoutAFingerprint is the path the policy exists for: the
// outbound's handshake transport parameters are its own, so both halves of the policy reach the
// connection - the grant drops from 8 MiB to 1 MiB per stream and from 20 MiB to 12 MiB per
// connection, and the ceilings keep the hysteria defaults.
func TestApplyReceiveWindowPolicyWithoutAFingerprint(t *testing.T) {
	var options qtls.QUICOptions
	applied := applyReceiveWindowPolicy(&options, quicpolicy.DefaultNetworkExtensionReceiveWindows(), true)
	require.True(t, applied, "the grants must be reported as applied when the handshake is ours")

	config := appliedQUICConfig(t, options)
	require.Equal(t, uint64(1<<20), config.InitialStreamReceiveWindow)
	require.Equal(t, uint64(12<<20), config.InitialConnectionReceiveWindow)
	require.Equal(t, uint64(8<<20), config.MaxStreamReceiveWindow)
	require.Equal(t, uint64(20<<20), config.MaxConnectionReceiveWindow)
}

// TestApplyReceiveWindowPolicyKeepsTheFingerprint pins the finding that decides whether part A can
// lower the advertised windows at all: with ChromeParrot on, quic-go replaces both initial windows
// with the prototype's values, so the policy must write the ceilings and must NOT write a grant it
// cannot honour - "applied and silently replaced" is the failure mode this branch prevents.
func TestApplyReceiveWindowPolicyKeepsTheFingerprint(t *testing.T) {
	var options qtls.QUICOptions
	applied := applyReceiveWindowPolicy(&options, quicpolicy.DefaultNetworkExtensionReceiveWindows(), false)
	require.False(t, applied, "the fingerprint owns the advertised initial windows")
	require.Zero(t, options.InitialStreamReceiveWindow,
		"a grant quic-go would overwrite must not be written here")
	require.Zero(t, options.InitialConnectionReceiveWindow)
	require.Equal(t, uint64(8<<20), options.MaxStreamReceiveWindow,
		"the ceilings are local, so they still apply")
	require.Equal(t, uint64(20<<20), options.MaxConnectionReceiveWindow)

	config := appliedQUICConfig(t, options)
	require.Equal(t, uint64(8<<20), config.InitialStreamReceiveWindow,
		"the connection keeps exactly the windows it had before the policy existed")
	require.Equal(t, uint64(20<<20), config.InitialConnectionReceiveWindow)
}

// TestApplyReceiveWindowPolicyLeavesAnOperatorsWindowAlone checks the other half of the contract:
// the policy never overrules an explicit stream_receive_window / connection_receive_window. The
// precedence is asserted where it is decided - qtls.ApplyQUICOptions - not only where it is
// written, which is why the options go through the real application here.
func TestApplyReceiveWindowPolicyLeavesAnOperatorsWindowAlone(t *testing.T) {
	options := qtls.QUICOptions{
		StreamReceiveWindow:     16 << 20,
		ConnectionReceiveWindow: 32 << 20,
	}
	applied := applyReceiveWindowPolicy(&options, quicpolicy.DefaultNetworkExtensionReceiveWindows(), true)
	require.True(t, applied)

	config := appliedQUICConfig(t, options)
	require.Equal(t, uint64(16<<20), config.InitialStreamReceiveWindow, "the operator asked for 16 MiB")
	require.Equal(t, uint64(16<<20), config.MaxStreamReceiveWindow)
	require.Equal(t, uint64(32<<20), config.InitialConnectionReceiveWindow, "and 32 MiB here")
	require.Equal(t, uint64(32<<20), config.MaxConnectionReceiveWindow)
}

// TestApplyReceiveWindowPolicyIsAbsentWithoutAPolicy states the default: an outbound built by a
// box that installed no policy asks for exactly what it asked for before this mechanism existed.
func TestApplyReceiveWindowPolicyIsAbsentWithoutAPolicy(t *testing.T) {
	options := qtls.QUICOptions{}
	config := appliedQUICConfig(t, options)
	require.Equal(t, uint64(8<<20), config.InitialStreamReceiveWindow)
	require.Equal(t, uint64(20<<20), config.InitialConnectionReceiveWindow)
}
