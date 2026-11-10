package daemon

// The connection stream's identity and its loss signal.
//
// A connection event stream is lossy by design: a subscriber whose buffer is full drops an event
// rather than blocking a connection. That is the right trade, but it leaves the client unable to
// tell "this connection never existed" from "this connection's event was dropped" - and the deltas
// it applies are relative, so a single lost event corrupts every later total for that id.
//
// Two fields close that hole: `instance` (which Box generation the stream belongs to) and
// `sequence`/`dropped` (how much of the producer's emission order the consumer never saw). This file
// pins the arithmetic and the identity, not the gRPC plumbing.

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/gofrs/uuid/v5"
	"github.com/stretchr/testify/require"
)

func TestTheSequenceGapIsTheDropCount(t *testing.T) {
	var sequence connectionStreamSequence

	require.Zero(t, sequence.Observe(1), "the first event is not a gap")
	require.Zero(t, sequence.Observe(2), "consecutive events are not a gap")
	require.EqualValues(t, 0, sequence.Dropped())

	require.EqualValues(t, 1, sequence.Observe(4), "one missing emission is one drop")
	require.EqualValues(t, 1, sequence.Dropped())
	require.EqualValues(t, 4, sequence.Observe(8), "three more missing emissions add three")
	require.EqualValues(t, 4, sequence.Dropped())

	// A stale or duplicate sequence must not count as loss: the client already has that event.
	require.EqualValues(t, 4, sequence.Observe(3))
	require.EqualValues(t, 4, sequence.Observe(8))
	require.EqualValues(t, 4, sequence.Dropped())

	// Zero means "this message carries no emission" (the reconcile events are synthesised from the
	// live table). It must not be mistaken for a gap.
	require.EqualValues(t, 4, sequence.Observe(0))
	require.EqualValues(t, 4, sequence.Dropped())
}

func TestTheFirstSequenceMayStartAnywhere(t *testing.T) {
	// A stream whose subscription started late sees a high first sequence; that is not a gap,
	// because there is no earlier observation to compare with.
	var sequence connectionStreamSequence
	require.Zero(t, sequence.Observe(1000))
	require.EqualValues(t, 1, sequence.Observe(1002))
}

func TestTheAppliedEventCarriesItsEmissionSequence(t *testing.T) {
	service := NewStartedService(ServiceOptions{Context: context.Background()})
	t.Cleanup(service.Close)
	id := uuid.Must(uuid.NewV4())
	metadata := &trafficcontrol.TrackerMetadata{
		ID:       id,
		Metadata: adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("example.test", 443)},
		Upload:   &atomic.Int64{},
		Download: &atomic.Int64{},
	}
	snapshots := make(map[uuid.UUID]connectionSnapshot)

	event := service.applyConnectionEvent(trafficcontrol.ConnectionEvent{
		Type:     trafficcontrol.ConnectionEventNew,
		ID:       id,
		Metadata: metadata,
		Sequence: 7,
	}, snapshots)
	require.NotNil(t, event)
	require.EqualValues(t, 7, event.Sequence, "the consumer needs the producer's order, not a local one")
}

func TestEachServiceHasItsOwnStreamInstance(t *testing.T) {
	first := NewStartedService(ServiceOptions{Context: context.Background()})
	t.Cleanup(first.Close)
	second := NewStartedService(ServiceOptions{Context: context.Background()})
	t.Cleanup(second.Close)

	require.NotZero(t, first.connectionStreamInstance)
	require.NotEqual(t, first.connectionStreamInstance, second.connectionStreamInstance,
		"two services are two Box generations; a client must be able to tell their streams apart")
}
