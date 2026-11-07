package group

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// scriptedLeaseManager is the selector's outbound manager with a scripted AcquireOutbound answer.
// It exists to drive the retry/refusal branches without a real provider, whose timing cannot be
// pinned to "reject exactly the first attempt".
type scriptedLeaseManager struct {
	adapter.OutboundManager
	results []bool
	calls   atomic.Int32
}

func (m *scriptedLeaseManager) AcquireOutbound(adapter.Outbound) (func(), bool) {
	index := int(m.calls.Add(1)) - 1
	if index < len(m.results) {
		return func() {}, m.results[index]
	}
	return func() {}, false
}

func TestSelectorRetriesAfterALeaseRejection(t *testing.T) {
	selector, leaf := newSelectorFixture(t, false)
	script := &scriptedLeaseManager{
		OutboundManager: selector.outbound,
		results:         []bool{false, true},
	}
	selector.outbound = script

	conn, err := selector.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
	require.NoError(t, err)
	require.NotNil(t, conn)
	_ = conn.Close()
	require.EqualValues(t, 2, script.calls.Load(), "the selector must re-read the selection after a rejection")
	require.NotNil(t, leaf.lastConn(), "the accepted attempt must reach the member")
}

func TestSelectorRefusesAfterRepeatedLeaseRejections(t *testing.T) {
	selector, leaf := newSelectorFixture(t, false)
	script := &scriptedLeaseManager{
		OutboundManager: selector.outbound,
		results:         []bool{false, false, false, false, false},
	}
	selector.outbound = script

	_, err := selector.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
	require.ErrorContains(t, err, "replaced during connection setup")
	require.EqualValues(t, 4, script.calls.Load(), "the retry loop is bounded")
	require.Nil(t, leaf.lastConn(), "a rejected selection must never be dialled")
}
