package libbox

// A snapshot larger than one gRPC message arrives as a group of fragments (part 1..parts). The client
// must apply it only when the LAST part has arrived: a partial group is not a baseline, and treating
// it as one would rebuild the table from an incomplete view and call recovery successful.
//
// The assembly is deliberately separate from the batch classifier: the classifier only ever sees one
// complete logical batch, so the ordering rules (stale/resync/accept) do not need a fragment case.

const connectionSnapshotMaxParts = 1024

type connectionSnapshotAssembly struct {
	instanceID   string
	subscription uint64
	parts        uint32
	nextPart     uint32
	events       []*ConnectionEvent
	dropped      uint64
	lastSequence uint64
}

// startSnapshot begins a group from its first fragment. Invalid groups are refused and the stream
// resynchronises; a group from a different instance can never be completed.
func startSnapshot(batch *ConnectionEvents, subscription uint64) (*connectionSnapshotAssembly, bool) {
	if batch.snapshotPart != 1 || batch.snapshotParts <= 1 || batch.snapshotParts > connectionSnapshotMaxParts {
		return nil, false
	}
	return &connectionSnapshotAssembly{
		instanceID:   batch.InstanceId,
		subscription: subscription,
		parts:        batch.snapshotParts,
		nextPart:     2,
		events:       append([]*ConnectionEvent(nil), batch.events...),
		dropped:      batch.dropped,
		lastSequence: batch.sequence,
	}, true
}

// add appends one continuation fragment. It refuses a gap, a different group, or a different instance.
func (a *connectionSnapshotAssembly) add(batch *ConnectionEvents, subscription uint64) bool {
	if a == nil {
		return false
	}
	if batch.snapshotParts != a.parts || batch.snapshotPart != a.nextPart {
		return false
	}
	if batch.InstanceId != a.instanceID || subscription != a.subscription {
		return false
	}
	a.events = append(a.events, batch.events...)
	a.lastSequence = batch.sequence
	a.nextPart++
	return true
}

// complete reports whether every part has arrived.
func (a *connectionSnapshotAssembly) complete() bool {
	return a != nil && a.nextPart == a.parts+1 && len(a.events) > 0
}

// finish builds the single logical baseline the consumer sees. Its sequence is the LAST fragment's,
// so the next increment must be last+1, and its dropped is the first fragment's (a baseline carries
// zero; a later rise is reported by a subsequent batch and triggers another repair if needed).
func (a *connectionSnapshotAssembly) finish() *ConnectionEvents {
	return &ConnectionEvents{
		Reset:        true,
		InstanceId:   a.instanceID,
		sequence:     a.lastSequence,
		dropped:      a.dropped,
		events:       a.events,
		subscription: a.subscription,
	}
}
