//go:build with_quic

package trusttunnel

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCongestionControlUnsetIsCubic pins the default for both halves of the
// tunnel: the client and the service resolve congestion control through the same
// congestionControlFunc, so one assertion covers both.
//
// The default is CUBIC, and it is selected explicitly: "" and "cubic" must
// resolve to the same factory, so "unset" cannot drift from "cubic" if a
// dependency changes its own default. This fork's quic-go builds its default
// sender with `use Reno = true`, so "leave the library alone" would be Reno.
func TestCongestionControlUnsetIsCubic(t *testing.T) {
	ctx := context.Background()

	cubicFactory, err := congestionControlFunc(ctx, "cubic")
	require.NoError(t, err)
	require.NotNil(t, cubicFactory)
	cubicPointer := reflect.ValueOf(cubicFactory).Pointer()

	unsetFactory, err := congestionControlFunc(ctx, "")
	require.NoError(t, err)
	require.NotNil(t, unsetFactory, "an unset value must select an explicit sender")
	require.Equal(t, cubicPointer, reflect.ValueOf(unsetFactory).Pointer(),
		"an unset value must select the same sender as explicit \"cubic\"")

	bbrFactory, err := congestionControlFunc(ctx, "bbr")
	require.NoError(t, err)
	require.NotEqual(t, cubicPointer, reflect.ValueOf(bbrFactory).Pointer(),
		"explicit bbr must not resolve to the CUBIC factory")

	renoFactory, err := congestionControlFunc(ctx, "reno")
	require.NoError(t, err)
	require.NotEqual(t, cubicPointer, reflect.ValueOf(renoFactory).Pointer(),
		"explicit reno must not resolve to the CUBIC factory")

	newRenoFactory, err := congestionControlFunc(ctx, "new_reno")
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(renoFactory).Pointer(), reflect.ValueOf(newRenoFactory).Pointer(),
		"new_reno and reno are the same algorithm and must resolve to the same factory")
}

// TestCongestionControlAcceptedAndRejectedNames pins the value set at
// construction, which is what `sing-box check` sees.
func TestCongestionControlAcceptedAndRejectedNames(t *testing.T) {
	for _, name := range []string{"", "cubic", "bbr", "reno", "new_reno"} {
		require.NoError(t, validateCongestionControl(name), "value %q must be accepted", name)
	}
	for _, name := range []string{"default", "BBR", "Cubic", " bbr", "bbr ", "vegas", "bbr2"} {
		require.Error(t, validateCongestionControl(name), "value %q must be rejected", name)
	}
}
