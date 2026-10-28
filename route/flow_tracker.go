package route

import (
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common"
)

var (
	_ tun.FlowTracker = (*flowInterrupter)(nil)
	_ tun.FlowTracker = (*multiFlowTracker)(nil)
)

// multiFlowTracker fans a flow's callbacks out to every tracker attached to it.
//
// # Why it is a struct with a lock rather than a slice
//
// The flow's lifecycle callbacks arrive from different goroutines: AttachFlow when the handover is
// established, CloseFlow when the flow dies, and the counters from the forwarding path. A tracker
// behind this fan-out therefore saw AttachFlow and CloseFlow concurrently, and one that assumed the
// two are ordered (the traffic-control recorder does) could close a flow before attaching it. The
// lock orders those two callbacks; the per-packet counters stay lock-free, exactly as upstream keeps
// them, because serialising every byte would cost more than the race it removes.
//
// The fix is upstream 92fe54be05f ("Fix flow close racing with tracker attach").
type multiFlowTracker struct {
	access   sync.Mutex
	trackers []tun.FlowTracker
}

func (t *multiFlowTracker) AttachFlow(handle tun.FlowHandle) {
	t.access.Lock()
	defer t.access.Unlock()
	for _, tracker := range t.trackers {
		tracker.AttachFlow(handle)
	}
}

func (t *multiFlowTracker) CountForward(n int) {
	for _, tracker := range t.trackers {
		tracker.CountForward(n)
	}
}

func (t *multiFlowTracker) CountReverse(n int) {
	for _, tracker := range t.trackers {
		tracker.CountReverse(n)
	}
}

func (t *multiFlowTracker) FlowEstablished() {
	for _, tracker := range t.trackers {
		tracker.FlowEstablished()
	}
}

func (t *multiFlowTracker) CloseFlow(reason tun.FlowCloseReason) {
	t.access.Lock()
	defer t.access.Unlock()
	for _, tracker := range t.trackers {
		tracker.CloseFlow(reason)
	}
}

type flowInterrupter struct {
	groups   []adapter.OutboundGroup
	removers []func()
}

func newFlowInterrupter(chain []adapter.Outbound) *flowInterrupter {
	groups := common.FilterIsInstance(chain, func(it adapter.Outbound) (adapter.OutboundGroup, bool) {
		group, isGroup := it.(adapter.OutboundGroup)
		return group, isGroup
	})
	if len(groups) == 0 {
		return nil
	}
	return &flowInterrupter{groups: groups}
}

type flowCloser struct {
	tun.FlowHandle
}

func (c flowCloser) Close() error {
	c.CloseFlow()
	return nil
}

func (t *flowInterrupter) AttachFlow(handle tun.FlowHandle) {
	closer := flowCloser{handle}
	for _, group := range t.groups {
		t.removers = append(t.removers, group.AttachConnection(closer))
	}
}

func (t *flowInterrupter) CountForward(n int) {
}

func (t *flowInterrupter) CountReverse(n int) {
}

func (t *flowInterrupter) FlowEstablished() {
}

func (t *flowInterrupter) CloseFlow(reason tun.FlowCloseReason) {
	for _, remove := range t.removers {
		remove()
	}
}
