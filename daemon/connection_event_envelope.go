package daemon

// connectionEventEnvelope stamps the connection-event batches one subscription sends.
//
// # Why these three values live on the envelope and not on each event
//
// instanceId says WHICH Box instance produced the batch: a reload replaces the instance while a
// subscription can still be open, and a client that cannot tell the two apart applies an old
// snapshot as if it were current. sequence advances per batch, so a client sees a gap rather than
// assuming the stream is complete. dropped is the number of source events this stream missed: the
// observable bus drops silently when a subscriber's buffer is full, so the only honest place to
// count a loss is where the sequence gap appears - here, against the source event's Sequence.
//
// The envelope is a separate type so that accounting is testable without a gRPC stream, and so the
// RPC body reads as "observe, then wrap".

import (
	"github.com/sagernet/sing-box/common/trafficcontrol"
)

type connectionEventEnvelope struct {
	instanceID string

	// sequence counts the batches this subscription has sent; it starts at 1.
	sequence uint64
	// lastEventSequence is the last source sequence delivered, for gap accounting. Zero means no
	// event has been delivered yet.
	lastEventSequence uint64
	// dropped is the cumulative number of source events this stream did not deliver.
	dropped uint64
}

// observe records one source event for gap accounting.
//
// A sequence at or below the last one is ignored rather than subtracted: it cannot happen with the
// current source (the counter only advances), and underflowing `dropped` would be worse than
// missing one count.
func (e *connectionEventEnvelope) observe(event trafficcontrol.ConnectionEvent) {
	if event.Sequence == 0 {
		return
	}
	if e.lastEventSequence != 0 && event.Sequence > e.lastEventSequence+1 {
		e.dropped += event.Sequence - e.lastEventSequence - 1
	}
	if event.Sequence > e.lastEventSequence {
		e.lastEventSequence = event.Sequence
	}
}

// wrap builds the batch to send.
func (e *connectionEventEnvelope) wrap(events []*ConnectionEvent, reset bool) *ConnectionEvents {
	e.sequence++
	return &ConnectionEvents{
		Events:     events,
		Reset_:     reset,
		InstanceId: e.instanceID,
		Sequence:   e.sequence,
		Dropped:    e.dropped,
	}
}
