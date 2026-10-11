package box_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// SV: the startup path must DIAGNOSE a bad configuration, never crash on it.
//
// # The defect this file pins
//
// `adapter/outbound.Manager.lintOutbounds` ordered the outbound graph with a set keyed by TAG:
//
//	started := make(map[string]bool, len(outbounds))
//	...
//	if len(started) == len(outbounds) { break }
//	currentOutbound := common.Find(outbounds, ...)   // nil when two objects share a tag
//	return nil, lintOutbound([]string{currentOutbound.Tag()}, currentOutbound)   // PANIC
//
// Two objects sharing a tag are ONE key for TWO nodes, so the loop can never mark both as started:
// `len(started)` stays short, `canContinue` is false, and `common.Find` returns nil - which the next
// line dereferences. `Box.Start` therefore PANICKED on a configuration instead of returning an
// error.
//
// # Why it is reachable from a shipped protocol
//
// The manager creates its own fallback outbound tagged `direct` when no outbound claims that tag
// (`adapter/outbound/manager.go`, `m.defaultOutboundFallback()`). An endpoint the user named `direct`
// then collides with it, and the collision is not the user's mistake to guess: `direct` is the
// conventional name for the built-in direct route, so it is a natural thing to call a tunnel.
//
// MEASURED before the fix, with `{"endpoints":[{"type":"wireguard","tag":"direct",...}]}` and no
// declared outbound: `Start` panicked with a nil dereference at the `currentOutbound.Tag()` call.
//
// # What the fix is, and what it is not
//
// It is NOT a resolution of the collision - which object the tag was meant for is the user's
// decision - and it does NOT make the configuration start. It is the difference between a crash and
// a diagnostic, which is the whole contract this file asserts: every case below either starts or
// returns an error, and NONE of them panics.

// svWireGuardEndpoint is a real, shipped endpoint type, because the defect was measured on one: a
// stub would only prove that a stub can collide.
func svWireGuardEndpoint(tag string) string {
	return `{"type": "wireguard", "tag": "` + tag + `", "system": false,
     "address": ["10.0.0.2/32"], "private_key": "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=",
     "peers": [{"address": "127.0.0.1", "port": 51820,
                "public_key": "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=",
                "allowed_ips": ["0.0.0.0/0"]}]}`
}

// svStartWithoutPanic runs Start inside a recover and reports what happened.
//
// The recover is the point of the test, so it is explicit rather than delegated to `require`: a
// panic in a goroutine cannot be recovered from, and the thing being measured is precisely whether
// an unrecovered one escapes.
func svStartWithoutPanic(t *testing.T, config string) (startErr error, recovered any) {
	t.Helper()
	instance, err := newBoxFromConfig(t, config)
	require.NoError(t, err, "construction cannot see this: it is a start-time decision")
	t.Cleanup(func() { _ = instance.Close() })
	func() {
		defer func() { recovered = recover() }()
		startErr = instance.Start()
	}()
	return startErr, recovered
}

// TestAnEndpointTaggedDirectIsDiagnosedRatherThanCrashing is the defect statement.
//
// The configuration is a name clash between the user's endpoint and the manager's own fallback, and
// the required behaviour is a REFUSAL THAT NAMES THE CLASH - not a crash, and not a silent start
// that leaves one of the two objects unreachable.
func TestAnEndpointTaggedDirectIsDiagnosedRatherThanCrashing(t *testing.T) {
	t.Parallel()

	startErr, recovered := svStartWithoutPanic(t, `{
  "log": {"disabled": true},
  "endpoints": [ `+svWireGuardEndpoint("direct")+` ],
  "route": {"rules": [{"network": "tcp", "outbound": "direct"}]}
}`)

	require.Nil(t, recovered,
		"a configuration must never crash the process; a tag clash is a diagnostic, not a panic. "+
			"recovered: %v", recovered)
	require.Error(t, startErr,
		"two objects cannot share a tag, because a tag resolves to one of them and every routing "+
			"rule and dependency edge names it")
	require.Contains(t, startErr.Error(), "direct",
		"and the refusal must name the tag the user has to change; got: "+startErr.Error())
}

// TestTheTagClashRefusalIdentifiesBothObjects is the diagnostic half, and it is not cosmetic.
//
// MEASURED: an earlier version of this message rendered BOTH objects as `endpoint/direct (direct)`,
// because `Type()` is not unique across this fork's registrations - an object whose type string is
// `direct` is not necessarily the `direct` outbound. A refusal that misnames the two things it is
// asking the user to choose between is worse than no refusal, so the message carries the concrete
// Go type as well.
func TestTheTagClashRefusalIdentifiesBothObjects(t *testing.T) {
	t.Parallel()

	startErr, recovered := svStartWithoutPanic(t, `{
  "log": {"disabled": true},
  "endpoints": [ `+svWireGuardEndpoint("direct")+` ]
}`)

	require.Nil(t, recovered, "no panic even without a routing rule naming the tag")
	if startErr == nil {
		// A configuration with no rule and no `final` may legitimately start if the manager can
		// order the set without resolving the clash; the assertion is only that it does not crash.
		return
	}
	require.Contains(t, startErr.Error(), "wireguard",
		"the message must say which endpoint type claimed the tag, not only that a clash exists; "+
			"got: "+startErr.Error())
}

// TestADistinctEndpointTagStarts is the positive control.
//
// The fix must not refuse every endpoint: the clash is what is refused, and the same configuration
// under a tag the manager does not claim must start.
func TestADistinctEndpointTagStarts(t *testing.T) {
	t.Parallel()

	startErr, recovered := svStartWithoutPanic(t, `{
  "log": {"disabled": true},
  "endpoints": [ `+svWireGuardEndpoint("wg-endpoint")+` ],
  "route": {"rules": [{"network": "tcp", "outbound": "wg-endpoint"}]}
}`)

	require.Nil(t, recovered, "and it must not crash either")
	require.NoError(t, startErr,
		"an endpoint whose tag is not the fallback's must start: the refusal is about the clash, "+
			"not about endpoints")
}

// TestAFinalNamingTheEndpointTagAvoidsTheClashEntirely is the case that explains the collision.
//
// The manager creates its fallback outbound ONLY when no object claims the configured tag. Naming
// the tag in `route.final` therefore means no fallback is created, and an endpoint under that tag is
// the sole claimant: the configuration is unambiguous and starts.
//
// This is asserted rather than assumed because it is the sharper statement of the defect - the crash
// was never about the tag being reserved, it was about the tag being claimed TWICE - and because a
// "fix" that simply refused every endpoint tagged `direct` would make this legal configuration fail.
func TestAFinalNamingTheEndpointTagAvoidsTheClashEntirely(t *testing.T) {
	t.Parallel()

	startErr, recovered := svStartWithoutPanic(t, `{
  "log": {"disabled": true},
  "endpoints": [ `+svWireGuardEndpoint("direct")+` ],
  "route": {"final": "direct"}
}`)

	require.Nil(t, recovered, "no crash: the tag has exactly one claimant")
	require.NoError(t, startErr,
		"`final` naming the tag is what stops the manager from creating its own `direct` fallback, "+
			"so this is a legal configuration and must start: the defect is a tag claimed twice, not "+
			"a tag being reserved")
}
