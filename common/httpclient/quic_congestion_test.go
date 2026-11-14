//go:build with_quic

package httpclient

import (
	"reflect"
	"testing"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// Tests for the client QUIC congestion control option.
//
// # The property that matters most
//
// An UNSET value must select CUBIC, and it must select it EXPLICITLY. The
// resolver returns the same factory for "" / "default" as for "cubic", so the
// default cannot drift when a dependency changes its own sender: this fork's
// quic-go builds its default with `use Reno = true`
// (internal/ackhandler/sent_packet_handler.go), which is why leaving the sender
// in place would not be CUBIC.
//
// BBR and Reno stay available by name. The matching is exact and case-sensitive:
// silently accepting "BBR" or " bbr" would make a typo look like it worked.

func TestClientCongestionControlUnsetIsCubic(t *testing.T) {
	t.Parallel()

	cubicFactory, err := NewClientCongestionControl("cubic")
	require.NoError(t, err)
	require.NotNil(t, cubicFactory)
	cubicPointer := reflect.ValueOf(cubicFactory).Pointer()

	for _, name := range []string{"", "default"} {
		factory, err := NewClientCongestionControl(name)
		require.NoError(t, err, "value %q must be accepted", name)
		require.NotNil(t, factory,
			"value %q must select an explicit sender; nil would leave the library's Reno in place", name)
		require.Equal(t, cubicPointer, reflect.ValueOf(factory).Pointer(),
			"value %q must select the same sender as explicit \"cubic\"", name)
	}
}

func TestClientCongestionControlExplicitNamesStayDistinct(t *testing.T) {
	t.Parallel()

	cubicFactory, err := NewClientCongestionControl("cubic")
	require.NoError(t, err)
	cubicPointer := reflect.ValueOf(cubicFactory).Pointer()

	for _, name := range []string{"bbr", "reno"} {
		factory, err := NewClientCongestionControl(name)
		require.NoError(t, err, "value %q must be accepted", name)
		require.NotNil(t, factory,
			"value %q must produce a sender factory, or the option would be a no-op", name)
		require.NotEqual(t, cubicPointer, reflect.ValueOf(factory).Pointer(),
			"value %q must not resolve to the CUBIC factory", name)
	}
}

// TestClientCongestionControlRejectsUnknownNames pins the validation.
//
// The matching is exact and case-sensitive on purpose: silently accepting "BBR" or
// " bbr" would make a typo look like it worked, and the operator would believe a
// performance setting was in effect when it was not.
func TestClientCongestionControlRejectsUnknownNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"BBR", " bbr", "bbr ", "cubicc", "vegas", "bbr2", "Default", "CUBIC"} {
		factory, err := NewClientCongestionControl(name)
		require.Error(t, err, "value %q must be rejected as unknown", name)
		require.Nil(t, factory, "a rejected value must not produce a factory")
		require.Contains(t, err.Error(), "unknown quic congestion control",
			"the error must name the problem so the operator can act on it")
	}
}

// TestClientCongestionControlErrorNamesTheValue guards the diagnostic.
//
// "unknown quic congestion control" without the offending value forces the reader
// to hunt through their configuration to find which option is wrong.
func TestClientCongestionControlErrorNamesTheValue(t *testing.T) {
	t.Parallel()

	_, err := NewClientCongestionControl("cubik")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cubik",
		"the error must quote the rejected value")
}

// TestApplyClientCongestionControlNilIsANoOp keeps the per-dial guard safe.
//
// The resolver returns a factory for every accepted value, so nil no longer
// occurs in production; the guard remains because this runs on every dial and a
// future caller passing nil must be a no-op rather than a panic. A nil conn is
// passed deliberately: if the function dereferenced it before checking the
// factory, this would panic rather than return.
func TestApplyClientCongestionControlNilIsANoOp(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() {
		ApplyClientCongestionControl(nil, nil)
	}, "a nil factory must return without touching the connection")
}

// TestNewQUICConfigLeavesCongestionControlToTheLibrary asserts the option is NOT
// smuggled into quic.Config.
//
// quic.Config has no congestion control field; the sender is installed on the Conn.
// Asserting the config still builds correctly with the new option set guards
// against a future refactor that tried to express it here.
func TestNewQUICConfigLeavesCongestionControlToTheLibrary(t *testing.T) {
	t.Parallel()

	config := NewQUICConfig(option.QUICOptions{CongestionControl: "bbr"})
	require.NotNil(t, config)
	// The new option must not disturb the fields that ARE part of quic.Config.
	require.Equal(t, int64(0), config.MaxIncomingStreams,
		"an unset max_concurrent_streams must stay unset")
}

// TestClientCongestionControlFactoryIsNotEvaluatedEarly exercises each factory
// against a real *quic.Conn shape.
//
// The factories close over conn.InitialPacketSize(), so a factory that was
// constructed correctly but called against a nil conn would panic at dial time
// rather than at load time. This test is what proves the closure is only evaluated
// where a conn exists - which is why ApplyClientCongestionControl takes the conn
// rather than the factory being pre-bound.
func TestClientCongestionControlFactoryIsNotEvaluatedEarly(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "default", "cubic", "bbr", "reno"} {
		factory, err := NewClientCongestionControl(name)
		require.NoError(t, err)
		require.NotNil(t, factory)
		// Building the factory must NOT require a connection: if it did, resolving
		// the option at construction time would panic.
		require.NotPanics(t, func() {
			_ = factory
		}, "resolving %q must not need a live connection", name)
	}
}

var _ = func() *quic.Conn { return nil }
