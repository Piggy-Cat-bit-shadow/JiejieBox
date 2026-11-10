package daemon

// A retained closed-connection window can legitimately exceed one gRPC message: 1,000 records with
// ~5 KiB rule descriptions encode to ~5.4 MiB, while the default receive limit is 4 MiB. The Reset
// view is therefore split into bounded fragments; the client assembles them and applies one baseline.
//
// The contract lives in the proto comments: part 1 carries reset=true, the last part completes the
// group, and an interrupted group is never a baseline. A snapshot that fits in one message keeps the
// historical shape (0/0), so an older peer sees no change.

import (
	"google.golang.org/protobuf/proto"
)

// snapshotFragmentBudget leaves room for the envelope and for gRPC/HTTP2 framing under the default
// 4 MiB receive limit. A single event larger than the budget is sent alone rather than dropped; if it
// also exceeds the receiver's transport limit the stream fails explicitly (ResourceExhausted), which
// is an observable failure, not a silent loss.
const snapshotFragmentBudget = 1 << 20

// chunkConnectionEvents splits the snapshot into fragments whose encoded batch stays within budget.
// The check is made on the real encoded batch size, not an estimate of the event sizes.
func chunkConnectionEvents(events []*ConnectionEvent) [][]*ConnectionEvent {
	if len(events) == 0 {
		return nil
	}
	var chunks [][]*ConnectionEvent
	var current []*ConnectionEvent
	for _, event := range events {
		if len(current) > 0 && proto.Size(&ConnectionEvents{Events: append(current, event)}) > snapshotFragmentBudget {
			chunks = append(chunks, current)
			current = nil
		}
		current = append(current, event)
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}

// sendConnectionSnapshot sends the initial Reset view, splitting it only when it cannot fit one
// message. It returns the drop count the baseline carried: zero for a baseline (the snapshot cannot
// prove losses outside its captured view), and the per-subscription counter otherwise.
func sendConnectionSnapshot(envelope *connectionEventEnvelope, events []*ConnectionEvent, send func(*ConnectionEvents) error) (uint64, error) {
	chunks := chunkConnectionEvents(events)
	if len(chunks) <= 1 {
		batch, dropped := envelope.wrap(events, true)
		return dropped, send(batch)
	}
	parts := uint32(len(chunks))
	for index, chunk := range chunks {
		batch := envelope.wrapSnapshot(chunk, uint32(index+1), parts, index == 0)
		if err := send(batch); err != nil {
			return 0, err
		}
	}
	return 0, nil
}
