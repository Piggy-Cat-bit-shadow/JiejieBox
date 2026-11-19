package hysteria2

import (
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-quic/hysteria2"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

// errNetworkChangedForTest is the shape InterfaceUpdated passes to CloseWithError. Anything that is
// not a teardown must leave the carrier reusable.
var errNetworkChangedForTest = errors.New("network changed")

// TestAShutdownCarrierNeverCreatesAClientAgain is the Box-teardown x pool pairing, pinned at the one
// place where a client can come into existence.
//
// # The defect it pins
//
// CloseWithError serves two callers: InterfaceUpdated (a network change, after which the carrier
// must stay usable) and the Box Scope (shutdown, after which it must not). The generation counter
// only protects a race that was ALREADY RUNNING — race() records the generation before creating and
// discards a result whose generation moved. A dial that starts AFTER the last CloseWithError reads
// the current generation, finds no client, creates one and installs it, and nothing bumps the
// generation again: that client's socket and keepalive goroutines outlive every owner the process
// still has.
//
// The Scope teardown reaches this shape because a relay accepted just before its listener closed can
// still call DialContext after the outbound cleanup ran.
//
// # What is asserted
//
// The counter is the independent result: the creation callback is the exact seam where a client is
// built, so "it was never called" is the invariant, not the error value. The error is asserted too,
// because "refused for the right reason" is what keeps a group policy from reading a shutdown as a
// node failure.
func TestAShutdownCarrierNeverCreatesAClientAgain(t *testing.T) {
	var created atomic.Int64
	carrier := newFamilyCarrier(func(int) (*hysteria2.Client, error) {
		created.Add(1)
		return nil, errNoFamilyClient
	})
	require.NoError(t, carrier.CloseWithError(os.ErrClosed))

	destination := M.ParseSocksaddrHostPort("example.com", 443)
	_, err := carrier.DialConn(t.Context(), destination)
	require.ErrorIs(t, err, os.ErrClosed,
		"a dial that arrives after shutdown must be refused as a teardown, not attempt a new session")
	require.Zero(t, created.Load(),
		"the closed carrier created a client: its socket and keepalive goroutines have no owner left "+
			"to close them")

	_, err = carrier.ListenPacket(t.Context())
	require.ErrorIs(t, err, os.ErrClosed)
	require.Zero(t, created.Load())
}

// TestANetworkChangeLeavesTheCarrierReusable is the other half of the same decision: the fix above
// must not turn the network-change reset into a permanent shutdown.
//
// InterfaceUpdated passes a descriptive error and the next demand must build fresh clients and race
// again — that is the whole point of the two-family reset. If the closed flag were set on every
// CloseWithError, every network change would silently disable the outbound for the rest of the
// process, which is a far worse failure than the one the flag fixes.
func TestANetworkChangeLeavesTheCarrierReusable(t *testing.T) {
	var created atomic.Int64
	carrier := newFamilyCarrier(func(int) (*hysteria2.Client, error) {
		created.Add(1)
		return nil, errNoFamilyClient
	})
	require.NoError(t, carrier.CloseWithError(errNetworkChangedForTest))
	_, err := carrier.DialConn(t.Context(), M.ParseSocksaddrHostPort("example.com", 443))
	require.ErrorIs(t, err, errNoFamilyClient,
		"a network change must leave the carrier able to create a client again")
	require.EqualValues(t, 1, created.Load())
}
