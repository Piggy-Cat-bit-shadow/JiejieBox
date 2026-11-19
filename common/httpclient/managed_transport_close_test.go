package httpclient

// P9 on the shared HTTP client: a marker that outlives its owner.
//
// ManagedTransport.close() swaps the epoch to nil and closes the inner transport. Nil is
// indistinguishable - at the next RoundTrip - from "nothing has been built yet", so getEpoch runs the
// factory and installs a NEW inner transport inside a transport that has already been closed. The
// manager's Close has run by then, so nothing will ever close the replacement: on a daemon reload the
// old Box's transport is closed while a provider refresh or a rule-set fetch may still be finishing,
// and that late request builds a whole new transport (base TLS config, socket pool) in a Box that is
// gone.
//
// The same shape as triple-A's T1 (a generation installed into a closed pool) and the same fix: the
// owner's state has to make "closed" a distinct answer from "not built yet", and the check has to be
// in the same critical section as the build.

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestARequestAfterCloseDoesNotResurrectTheTransport is the request half.
func TestARequestAfterCloseDoesNotResurrectTheTransport(t *testing.T) {
	t.Parallel()
	var built atomic.Int64
	transport := &ManagedTransport{
		cheapRebuild: true,
		factory: func() (innerTransport, error) {
			built.Add(1)
			return &fakeInnerTransport{}, nil
		},
	}
	response, err := transport.RoundTrip(newTestRequest())
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.EqualValues(t, 1, built.Load())

	require.NoError(t, transport.close())

	_, err = transport.RoundTrip(newTestRequest())
	require.Error(t, err, "a request that arrives after Close must be refused, not served on a "+
		"transport built after the close")
	require.EqualValues(t, 1, built.Load(),
		"the closed transport rebuilt an inner transport; the manager's Close has already run, so "+
			"nothing will ever close this one")
}

// TestAResetAfterCloseDoesNotResurrectTheTransport is the network-change half: Reset is reached from
// the DNS environment reset and from the reference manager, both of which can land while the box is
// being torn down. A cheap rebuild makes the resurrection immediate rather than lazy.
func TestAResetAfterCloseDoesNotResurrectTheTransport(t *testing.T) {
	t.Parallel()
	var built atomic.Int64
	var closedCount atomic.Int64
	transport := &ManagedTransport{
		cheapRebuild: true,
		factory: func() (innerTransport, error) {
			built.Add(1)
			return &fakeInnerTransport{closed: &closedCount}, nil
		},
	}
	response, err := transport.RoundTrip(newTestRequest())
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.EqualValues(t, 1, built.Load())

	require.NoError(t, transport.close())
	require.EqualValues(t, 1, closedCount.Load(), "Close releases the inner transport")

	transport.Reset()
	require.EqualValues(t, 1, built.Load(),
		"a reset after Close rebuilt a transport that nothing will close")
	require.EqualValues(t, 1, closedCount.Load())
	require.Nil(t, transport.epoch.Load(), "no epoch may exist after Close")
}

// TestCloseIsIdempotentAndStillReportsOnce pins that the closed state does not change the existing
// lifecycle contract (the manager calls Close once, but a double close must not panic or double-close).
func TestCloseIsIdempotentAndStillReportsOnce(t *testing.T) {
	t.Parallel()
	var built atomic.Int64
	var closedCount atomic.Int64
	transport := &ManagedTransport{
		factory: func() (innerTransport, error) {
			built.Add(1)
			return &fakeInnerTransport{closed: &closedCount}, nil
		},
	}
	response, err := transport.RoundTrip(newTestRequest())
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())

	require.NoError(t, transport.close())
	require.NoError(t, transport.close())
	require.EqualValues(t, 1, built.Load())
	require.EqualValues(t, 1, closedCount.Load(), "the inner transport is closed exactly once")
}
