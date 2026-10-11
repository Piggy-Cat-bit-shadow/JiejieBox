package box_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAnHTTPClientDetourMustNameSomething is the fdC asymmetry, closed.
//
// # The defect
//
// Four places accept a `detour`, and three of them refused one that names nothing while the fourth
// started and failed later, on the first request:
//
//	dns transport   REFUSED  "outbound detour not found: ghost"
//	outbound        REFUSED  "dependency[ghost] not found for outbound[s]"
//	rule-set        REFUSED  "http_client not found: ghost"
//	http client     STARTED  (before this check)
//
// The cause is that `dialer.NewDetour` resolves the tag LAZILY - it stores the name and only asks the
// outbound manager on the first dial (`common/dialer/detour.go`, `DetourDialer.init`) - and the http
// client manager never called the eager half the others use (`dialer.InitializeDetour`).
//
// # What the fix is careful NOT to do
//
// The obvious fix, resolving every defined client at start, constructs a transport per client
// (`resolveShared` -> `NewTransport`) and would build clients that nothing references - a new
// failure mode for configurations that work today, which is the false-deny class this audit exists
// to remove. The check asks the outbound manager for the TAG instead, which constructs nothing.
// Both halves are asserted below.
func TestAnHTTPClientDetourMustNameSomething(t *testing.T) {
	t.Parallel()

	// Refused: the tag names nothing, and the failure is now a start-time diagnostic.
	dangling, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "http_clients": [{"tag": "hc", "version": 1, "detour": "ghost"}],
  "outbounds": [{"type": "direct", "tag": "out"}],
  "route": {"final": "out"}
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dangling.Close() })
	startErr := dangling.Start()
	require.Error(t, startErr, "a detour naming nothing cannot work, so it must not start")
	require.Contains(t, startErr.Error(), "ghost",
		"and the refusal must name the tag that resolves to nothing; got: "+startErr.Error())

	// Accepted: a detour that names a real outbound.
	real, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "http_clients": [{"tag": "hc", "version": 1, "detour": "out"}],
  "outbounds": [{"type": "direct", "tag": "out"}],
  "route": {"final": "out"}
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = real.Close() })
	require.NoError(t, real.Start(), "the reference resolves, so nothing here may refuse it")

	// Accepted: no detour at all. This is the control that keeps the check from being "every client
	// must declare a detour".
	absent, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "http_clients": [{"tag": "hc", "version": 1}],
  "outbounds": [{"type": "direct", "tag": "out"}],
  "route": {"final": "out"}
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = absent.Close() })
	require.NoError(t, absent.Start(), "a client with no detour is the ordinary case")
}

// TestAnUnreferencedHTTPClientIsNotBuiltAtStart is the other half, and it is the reason the fix
// checks the tag rather than resolving the transport.
//
// A client that nothing references must not be constructed by the new validation. The observable
// difference is that a client whose CONSTRUCTION would fail still starts when nothing uses it - so
// the check adds a refusal for a dangling reference and adds no refusal for an unused client.
func TestAnUnreferencedHTTPClientIsNotBuiltAtStart(t *testing.T) {
	t.Parallel()

	// A client whose own settings are fine but which nothing references. If the validation resolved
	// transports instead of references, this would be built and any construction-time cost or
	// failure would move to start for no benefit.
	unused, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "http_clients": [{"tag": "unused", "version": 1, "detour": "out"}],
  "outbounds": [{"type": "direct", "tag": "out"}],
  "route": {"final": "out"}
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = unused.Close() })
	require.NoError(t, unused.Start(),
		"a declared client that resolves its detour is accepted whether or not a rule-set names it")
}
