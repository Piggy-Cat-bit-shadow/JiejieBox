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
	// loss returns this subscription's exact drop count. It is what makes a SILENT tail loss
	// recoverable: the daemon can publish "you lost N events" on a tick even when no source event
	// follows to reveal a sequence gap. A queued event does not move it.
	loss func() uint64

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

// shouldSendConnectionBatch reports whether the subscription has something to send: events to
// deliver, or a loss that has not been announced yet. It is the ticker's condition, extracted so the
// silent-tail contract can be tested without a stream.
func shouldSendConnectionBatch(eventCount int, currentLoss uint64, reportedLoss uint64) bool {
	return eventCount > 0 || currentLoss > reportedLoss
}

// wrap builds the batch to send and returns the drop count that batch carries.
//
// A BASELINE (reset) carries zero. The snapshot only proves what its captured view covers: drops that
// happen while that view is being built are not in it, so adopting the counter there told the consumer
// "136 lost events are already accounted for" when they were not, and the next batch carried the same
// 136 - no rise, no recovery, silence. The per-subscription counter stays live; the first later batch,
// or the idle tick, reports the rise and the existing recovery path runs.
//
// The returned value is the ONLY number the sender may record as notified. Reading the counter again
// after Send would mark drops that landed while the Send was blocked as delivered, although nobody
// has seen them yet.
func (e *connectionEventEnvelope) wrap(events []*ConnectionEvent, reset bool) (*ConnectionEvents, uint64) {
	e.sequence++
	var dropped uint64
	if !reset {
		// Dropped is the exact loss count when one is available, and the gap count otherwise. They
		// are the same number once an event reveals the gap; the larger avoids double counting while
		// letting the counter speak during silence.
		dropped = e.dropped
		if e.loss != nil {
			if counter := e.loss(); counter > dropped {
				dropped = counter
			}
		}
	}
	return &ConnectionEvents{
		Events:     events,
		Reset_:     reset,
		InstanceId: e.instanceID,
		Sequence:   e.sequence,
		Dropped:    dropped,
	}, dropped
}

// wrapSnapshot builds one fragment of a split snapshot. Every fragment carries zero: the group is a
// single baseline, and the per-subscription counter is announced after the group completes (a drop
// during the split is outside the captured view too).
func (e *connectionEventEnvelope) wrapSnapshot(events []*ConnectionEvent, part uint32, parts uint32, reset bool) *ConnectionEvents {
	e.sequence++
	return &ConnectionEvents{
		Events:        events,
		Reset_:        reset,
		InstanceId:    e.instanceID,
		Sequence:      e.sequence,
		Dropped:       0,
		SnapshotPart:  part,
		SnapshotParts: parts,
	}
}
