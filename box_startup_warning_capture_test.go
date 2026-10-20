package box_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

// startBoxCapturingWarnings starts a configuration with its log written to a temporary file at warn
// level, and returns the start error together with what was logged.
//
// # Why a real file rather than a fake logger
//
// The point of the warnings this suite now asserts is that they are produced by the PRODUCTION start
// path through the normal log interface, with the configured level applying to them. A test that
// injected a recording logger into the manager would prove the message exists but not that a user's
// `"log": {"level": "warn", "output": "..."}` receives it. `log.output` selects the file writer in
// the real factory, so this is the same mechanism an operator uses.
func startBoxCapturingWarnings(t *testing.T, configContent string) (startErr error, logText string) {
	t.Helper()
	ctx := include.Context(context.Background())
	options, err := json.UnmarshalExtendedContext[option.Options](ctx, []byte(configContent))
	require.NoError(t, err, "the configuration must decode; a parse failure would make the start assertion meaningless")
	logPath := filepath.Join(t.TempDir(), "box.log")
	options.Log.Disabled = false
	options.Log.Level = "warn"
	options.Log.Output = logPath
	options.Log.Timestamp = false
	options.Log.DisableColor = true
	instance, err := box.New(box.Options{Context: ctx, Options: options})
	require.NoError(t, err, "construction cannot see this: it is a start-time decision")
	t.Cleanup(func() { _ = instance.Close() })
	startErr = instance.Start()
	data, readErr := os.ReadFile(logPath)
	if readErr == nil {
		logText = string(data)
	}
	return startErr, logText
}

// TestTheReportedUDPCapabilityShapeStartsWithAWarning is the incident this round exists for.
//
// The chain is the reported one - a top-level selector, a second selection step, a third, and a
// terminal node that carries TCP only - with a rule delivering UDP to the top. Before the change,
// that configuration refused to start at all: no TCP flow could run because a UDP flow MIGHT have
// failed later. Now it starts, and the warning names the root, the route and the missing network.
//
// ASCII tags are used deliberately: the shape is what matters, and a tag with emoji or a pipe would
// make this test's failure output depend on console encoding rather than on the behaviour.
func TestTheReportedUDPCapabilityShapeStartsWithAWarning(t *testing.T) {
	const config = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "us1-tcp-only", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"},
    {"type": "selector", "tag": "global-direct", "outbounds": ["us1-tcp-only"], "default": "us1-tcp-only"},
    {"type": "selector", "tag": "node-select", "outbounds": ["global-direct"], "default": "global-direct"},
    {"type": "selector", "tag": "leak-fish", "outbounds": ["node-select"], "default": "node-select"},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rules": [
      {"network": "udp", "outbound": "leak-fish"}
    ],
    "final": "direct"
  }
}`
	startErr, warnings := startBoxCapturingWarnings(t, config)
	require.NoError(t, startErr,
		"the TCP flows this configuration serves must not be refused because a UDP flow cannot be")
	require.Contains(t, warnings, "outbound/leak-fish",
		"the warning must name the entry point the rule selects")
	require.Contains(t, warnings, "us1-tcp-only",
		"and the terminal node that cannot carry the flow")
	require.Contains(t, warnings, "cannot serve the udp flow",
		"and the network that cannot be carried, rather than only that something is unsupported")
	require.Contains(t, warnings, "WARN",
		"and it must be emitted at warn level through the production logger")
}
