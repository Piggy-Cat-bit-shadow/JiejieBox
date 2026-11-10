package libbox

// One decision for the connection-event stream, shared by the stream client and the connection table.
//
// # Why a shared classifier
//
// The client and the table used to each decide whether a batch could be applied, with different
// rules, and the reviewed failures came from the difference:
//
//   - the client advanced its watermark on a DUPLICATE batch, so `1R,2,1,3` made 3 look like a gap and
//     triggered a second subscription for a stream that had lost nothing;
//   - the table accepted every Reset unconditionally, so an OLD Reset from the same subscription
//     rolled the rows back to a state that had already been superseded.
//
// The classifier is the single owner of "may this batch change state, and what does it mean". It is
// pure: it reads the wire fields and the current state, and returns accept / stale / resync. Only an
// accepted batch may advance the instance, sequence and dropped baseline; a stale batch changes
// nothing; a resync verdict means the consumer must wait for a baseline.
//
// # Reset authority
//
// A Reset is a baseline when it comes from a NEW subscription, when there is no baseline yet, or when
// it carries no wire ordering (an older peer). A Reset from the SAME subscription whose sequence is
// not newer than what was already applied is a stale duplicate and has no authority: that is what
// stops an old snapshot from clearing a newer table. A legitimate recovery baseline arrives on a new
// subscription (the client resubscribes), so it is accepted by the first rule.

type connectionBatchVerdict int

const (
	// batchAccept means the batch may be applied and may advance the baseline.
	batchAccept connectionBatchVerdict = iota
	// batchStale means the batch describes a state that is already superseded: do not apply it, do
	// not deliver it, and do not move any baseline. It is NOT a loss.
	batchStale
	// batchResync means an increment cannot be placed in order (gap, rising drop count, instance
	// change): the consumer must wait for a baseline.
	batchResync
)

// connectionBatchState is the ordering state of one subscription/consumer.
type connectionBatchState struct {
	hasBaseline  bool
	needsResync  bool
	instanceID   string
	sequence     uint64
	dropped      uint64
	subscription uint64
}

// classifyConnectionBatch decides what one batch means for this state.
func classifyConnectionBatch(state *connectionBatchState, batch *ConnectionEvents) connectionBatchVerdict {
	// A batch from an OLDER subscription can never write state, whatever it carries.
	if batch.subscription != 0 && state.subscription != 0 && batch.subscription < state.subscription {
		return batchStale
	}
	if batch.Reset {
		if state.hasBaseline && batch.sequence != 0 && state.sequence != 0 &&
			batch.subscription != 0 && batch.subscription == state.subscription &&
			batch.sequence <= state.sequence {
			return batchStale
		}
		return batchAccept
	}
	if state.needsResync {
		return batchResync
	}
	if batch.InstanceId != "" && state.instanceID != "" && batch.InstanceId != state.instanceID {
		return batchResync
	}
	if batch.sequence == 0 {
		// An older daemon sends no ordering fields; the Reset flag and subscription identity are the
		// only facts that exist, and the previous behaviour must be preserved.
		return batchAccept
	}
	if state.hasBaseline && state.sequence != 0 {
		if batch.sequence <= state.sequence {
			return batchStale
		}
		if batch.sequence != state.sequence+1 {
			return batchResync
		}
	}
	if state.hasBaseline && batch.dropped > state.dropped {
		// The source reports that events were lost: the batch may be missing a change, so it cannot
		// be applied as a complete increment.
		return batchResync
	}
	return batchAccept
}

// apply records an accepted batch in the state.
func (s *connectionBatchState) apply(batch *ConnectionEvents) {
	s.hasBaseline = true
	s.needsResync = false
	s.instanceID = batch.InstanceId
	s.sequence = batch.sequence
	s.dropped = batch.dropped
	if batch.subscription != 0 {
		s.subscription = batch.subscription
	}
}
