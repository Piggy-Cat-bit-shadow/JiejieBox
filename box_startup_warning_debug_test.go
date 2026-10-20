package box_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Startup-validation debug: the three properties the two-axis split can silently break.
//
// The construction round turned capability findings into warnings. These tests attack the places
// where such a change usually leaks: a warning that still refuses the start (or a fatal that gets
// swallowed), a warned start that leaves the Box half-initialised, and a message path that cannot
// carry the tag names the report used.

// TestAFatalFindingWinsOverAWarningInTheSameConfiguration is the severity-isolation attack.
//
// One selector holds a member that cannot carry the routed network (capability) and a member whose
// tag does not exist (structural). The start must fail, the error must name the structural defect,
// and the capability finding must not be promoted into the reason.
func TestAFatalFindingWinsOverAWarningInTheSameConfiguration(t *testing.T) {
	const config = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"},
    {"type": "selector", "tag": "sel", "outbounds": ["tcp-only", "ghost"], "default": "tcp-only"},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rules": [{"network": "udp", "outbound": "sel"}],
    "final": "direct"
  }
}`
	startErr, warnings := startBoxCapturingWarnings(t, config)
	require.Error(t, startErr,
		"a declared member that does not exist is structural and must refuse the start")
	require.Contains(t, startErr.Error(), "ghost",
		"the error must name the missing member; got: "+startErr.Error())
	require.NotContains(t, startErr.Error(), "cannot serve the udp flow",
		"the structural verdict must not be replaced by the capability finding")
	require.NotContains(t, warnings, "cannot serve the udp flow",
		"a refused start reports the fatal finding, not a warning that suggests the rest came up")
}

// TestAWarnedStartClosesCleanly is the lifecycle half of E1.
//
// The warning path returns before startOutbounds only when a fatal finding exists. A warned start
// must have started the outbounds, registered the scope cleanups and left a usable status surface -
// not a Box that reports a half-start state.
func TestAWarnedStartClosesCleanly(t *testing.T) {
	const config = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"},
    {"type": "selector", "tag": "sel", "outbounds": ["tcp-only"], "default": "tcp-only"},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rules": [{"network": "udp", "outbound": "sel"}],
    "final": "direct"
  }
}`
	startErr, warnings := startBoxCapturingWarnings(t, config)
	require.NoError(t, startErr)
	require.Contains(t, warnings, "cannot serve the udp flow")

	// The helper closes the Box in Cleanup; the status surface must not report the disconnected
	// state that a refused start leaves, because this start succeeded.
	instance, err := newBoxFromConfig(t, config)
	require.NoError(t, err)
	t.Cleanup(func() { _ = instance.Close() })
	require.NoError(t, instance.Start())
	status := instance.Status("sel", "tcp")
	require.NotEmpty(t, status.Hops,
		"a warned start has started the outbounds, so the status graph is populated")
	for _, unknown := range status.Unknowns {
		require.NotContains(t, unknown.Reason, "disconnected",
			"a warned start is a started Box, not a disconnected one; got: %s", unknown.Reason)
	}
	require.NoError(t, instance.Close(), "the scope cleanups registered by a warned start must run")
	require.NoError(t, instance.Close(), "and Close stays idempotent")
}

// TestTheWarningCarriesTheReportedTagNames intact is the message-path attack.
//
// The reported configuration used emoji, a pipe and CJK in its tags. A report that split on the pipe,
// truncated the tag or crashed on the encoding would be useless for exactly the operator it is for.
func TestTheWarningCarriesTheReportedTagNames(t *testing.T) {
	const config = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "美国1|N|9929|1x倍率", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"},
    {"type": "selector", "tag": "🎯 全球直连", "outbounds": ["美国1|N|9929|1x倍率"], "default": "美国1|N|9929|1x倍率"},
    {"type": "selector", "tag": "🐟 漏网之鱼", "outbounds": ["🎯 全球直连"], "default": "🎯 全球直连"},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rules": [{"network": "udp", "outbound": "🐟 漏网之鱼"}],
    "final": "direct"
  }
}`
	startErr, warnings := startBoxCapturingWarnings(t, config)
	require.NoError(t, startErr,
		"the reported shape is a capability risk, not an unbuildable configuration")
	for _, tag := range []string{"🐟 漏网之鱼", "🎯 全球直连", "美国1|N|9929|1x倍率"} {
		require.Contains(t, warnings, tag,
			"the warning must carry %q intact; a split, truncated or re-encoded tag cannot be acted "+
				"on. Got:\n%s", tag, warnings)
	}
	require.Contains(t, warnings, "cannot serve the udp flow")
	require.Equal(t, 1, strings.Count(warnings, "the following outbound paths cannot serve some flows"),
		"one aggregated warning; got:\n"+warnings)
}

// TestAWarningWithLoggingDisabledNeitherPanicsNorRefuses is the E3 attack.
//
// With `log.disabled` the manager is handed a NOP logger. A warning that a nil or disabled sink
// turned into an error would refuse a configuration that has nothing structurally wrong - and a
// panic here would take the whole start down for a diagnostic.
func TestAWarningWithLoggingDisabledNeitherPanicsNorRefuses(t *testing.T) {
	const config = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"},
    {"type": "selector", "tag": "sel", "outbounds": ["tcp-only"], "default": "tcp-only"},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rules": [{"network": "udp", "outbound": "sel"}],
    "final": "direct"
  }
}`
	instance, err := newBoxFromConfig(t, config)
	require.NoError(t, err)
	t.Cleanup(func() { _ = instance.Close() })
	require.NoError(t, instance.Start(),
		"a suppressed warning must not change the verdict; no panic on the NOP logger")
}
