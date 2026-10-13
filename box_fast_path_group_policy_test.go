package box_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// fast_path is a LOGICAL GROUP policy. These tests pin the configuration-layer half of that claim:
// the key is refused everywhere it would be read as a node property, and accepted on exactly the
// groups that can carry it.
//
// The refusal is deliberate rather than a silent ignore. An operator who writes fast_path on a node
// believes the node has been authorised; nothing reads it there, so accepting it would leave the
// configuration looking effective while the data path never consults it.

// TestFastPathIsRefusedOnALeafOutbound is Case F of §14.1. `direct` is the sharpest example: it is
// the outbound the fast path actually bypasses, and even there the key must not be accepted, because
// authorisation belongs to the group a flow ENTERS and not to the node that serves it.
func TestFastPathIsRefusedOnALeafOutbound(t *testing.T) {
	for name, config := range map[string]string{
		"direct": `{
  "log": {"disabled": true},
  "outbounds": [{"type": "direct", "tag": "美国-01", "fast_path": true}],
  "route": {"final": "美国-01"}
}`,
		"a proxy leaf": `{
  "log": {"disabled": true},
  "outbounds": [{"type": "socks", "tag": "美国-01", "server": "127.0.0.1", "server_port": 1080, "fast_path": true}],
  "route": {"final": "美国-01"}
}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newBoxFromConfig(t, config)
			require.Error(t, err, "fast_path on a leaf must be refused")
			require.Contains(t, err.Error(), "fast_path",
				"and the error must name the field; got: "+err.Error())
			require.Contains(t, err.Error(), "美国-01",
				"and the outbound it was set on; got: "+err.Error())
		})
	}
}

// TestFastPathIsRefusedOnLoadBalance states the one exclusion that is a product decision rather than
// a structural one. LoadBalance may own the dial of the flow it selected and can retry, so a chain
// through it is not transparent and bypassing it would silently discard the retry its configuration
// asked for. It is a group, so the "not a group" check would not catch it; the transparency claim
// does.
func TestFastPathIsRefusedOnLoadBalance(t *testing.T) {
	_, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "direct", "tag": "美国-01"},
    {"type": "direct", "tag": "美国-02"},
    {"type": "loadbalance", "tag": "🏠 LB", "fast_path": true, "outbounds": ["美国-01", "美国-02"]}
  ],
  "route": {"final": "🏠 LB"}
}`)
	require.Error(t, err, "loadbalance must not be able to carry fast_path")
	require.Contains(t, err.Error(), "fast_path")
	require.Contains(t, err.Error(), "🏠 LB")
}

// TestFastPathIsAcceptedOnTheSupportedGroups is the positive half: the two pure selection groups
// accept the key, on every one of the three states.
func TestFastPathIsAcceptedOnTheSupportedGroups(t *testing.T) {
	for _, groupType := range []string{"selector", "urltest"} {
		for _, value := range []string{"true", "false"} {
			t.Run(groupType+"/fast_path="+value, func(t *testing.T) {
				instance, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "direct", "tag": "美国-01"},
    {"type": "direct", "tag": "美国-02"},
    {"type": "`+groupType+`", "tag": "🤖 AI", "fast_path": `+value+`, "outbounds": ["美国-01", "美国-02"]}
  ],
  "route": {"final": "🤖 AI"}
}`)
				require.NoError(t, err, "a supported group must accept fast_path")
				require.NotNil(t, instance)
				require.NoError(t, instance.Start())
				t.Cleanup(func() { _ = instance.Close() })
			})
		}

		t.Run(groupType+"/unset", func(t *testing.T) {
			instance, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "direct", "tag": "美国-01"},
    {"type": "`+groupType+`", "tag": "🤖 AI", "outbounds": ["美国-01"]}
  ],
  "route": {"final": "🤖 AI"}
}`)
			require.NoError(t, err, "omitting the field must stay valid")
			require.NotNil(t, instance)
			require.NoError(t, instance.Start())
			t.Cleanup(func() { _ = instance.Close() })
		})
	}
}

// TestFastPathOnAGroupDoesNotChangeOrdinaryRouting is the §14.6 compatibility claim at the
// configuration level: a configuration using the field must start and route exactly as one that does
// not. The behavioural half is covered by the route-package tests; this pins that enabling the
// policy introduces no start-time refusal of its own.
func TestFastPathOnAGroupDoesNotChangeOrdinaryRouting(t *testing.T) {
	base := `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "direct", "tag": "美国-01"},
    {"type": "direct", "tag": "普通-01"},
    {"type": "selector", "tag": "🤖 AI"%s, "outbounds": ["美国-01"]},
    {"type": "selector", "tag": "🌐 普通代理", "outbounds": ["普通-01"]}
  ],
  "route": {"final": "🌐 普通代理"}
}`

	without, err := newBoxFromConfig(t, fmt.Sprintf(base, ""))
	require.NoError(t, err)
	require.NoError(t, without.Start())
	t.Cleanup(func() { _ = without.Close() })

	withPolicy, err := newBoxFromConfig(t, fmt.Sprintf(base, `, "fast_path": true`))
	require.NoError(t, err, "adding the field must not make a valid configuration invalid")
	require.NoError(t, withPolicy.Start())
	t.Cleanup(func() { _ = withPolicy.Close() })
}
