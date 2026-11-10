package daemon

// The envelope is the wire-side half of the connection-event contract: which instance produced a
// batch, which batch it is, and how many source events this stream missed.
//
// The reviewed failure needed all three at once: a reload replaced the instance while a
// subscription was open, the consumer applied the old instance's snapshot over the new state, and
// a slow subscriber lost events with no way to notice. The subscription identity in libbox already
// rejects a superseded STREAM; these fields let a consumer make the same decision about a batch and
// measure a loss instead of assuming completeness.

import (
	"testing"

	"github.com/sagernet/sing-box/common/trafficcontrol"

	"github.com/stretchr/testify/require"
)

func TestTheEnvelopeCountsGapsInTheSourceSequence(t *testing.T) {
	envelope := &connectionEventEnvelope{instanceID: "instance-1"}

	envelope.observe(trafficcontrol.ConnectionEvent{Sequence: 1})
	envelope.observe(trafficcontrol.ConnectionEvent{Sequence: 2})
	envelope.observe(trafficcontrol.ConnectionEvent{Sequence: 5})
	envelope.observe(trafficcontrol.ConnectionEvent{Sequence: 6})

	require.EqualValues(t, 2, envelope.dropped,
		"sequences 3 and 4 were never delivered, and the gap is the only evidence of it")
	require.EqualValues(t, 6, envelope.lastEventSequence)
}

func TestTheEnvelopeStartsItsBatchSequenceAtOne(t *testing.T) {
	envelope := &connectionEventEnvelope{instanceID: "instance-1"}

	first := envelope.wrap(nil, true)
	second := envelope.wrap([]*ConnectionEvent{{Id: "x"}}, false)
	third := envelope.wrap(nil, false)

	require.EqualValues(t, 1, first.Sequence)
	require.EqualValues(t, 2, second.Sequence)
	require.EqualValues(t, 3, third.Sequence)
	require.Equal(t, "instance-1", first.InstanceId)
	require.True(t, first.Reset_)
	require.False(t, second.Reset_)
	require.EqualValues(t, 0, first.Dropped)
}

func TestTheEnvelopeCarriesTheCumulativeDroppedCount(t *testing.T) {
	envelope := &connectionEventEnvelope{instanceID: "instance-1"}
	envelope.observe(trafficcontrol.ConnectionEvent{Sequence: 1})
	envelope.observe(trafficcontrol.ConnectionEvent{Sequence: 4})

	batch := envelope.wrap(nil, false)
	require.EqualValues(t, 2, batch.Dropped)
	require.Equal(t, "instance-1", batch.InstanceId)
}

// TestTheEnvelopeIgnoresOutOfOrderSequences keeps the counter from underflowing: a sequence that
// moves backwards cannot happen with the current source, but subtracting it would wrap `dropped`
// to an enormous number, which is worse than a missed count.
func TestTheEnvelopeIgnoresOutOfOrderSequences(t *testing.T) {
	envelope := &connectionEventEnvelope{instanceID: "instance-1"}
	envelope.observe(trafficcontrol.ConnectionEvent{Sequence: 10})
	envelope.observe(trafficcontrol.ConnectionEvent{Sequence: 4})

	require.EqualValues(t, 0, envelope.dropped)
	require.EqualValues(t, 10, envelope.lastEventSequence)
}

// TestTheInstanceIdentityChangesWithTheInstance pins the service-side half: a new instance gets a
// new identity, and clearing it clears the identity too.
func TestTheInstanceIdentityChangesWithTheInstance(t *testing.T) {
	service := &StartedService{}

	service.serviceAccess.Lock()
	service.setInstanceLocked(&Instance{})
	service.serviceAccess.Unlock()
	first := service.currentInstanceID()
	require.NotEmpty(t, first)

	service.serviceAccess.Lock()
	service.setInstanceLocked(nil)
	service.serviceAccess.Unlock()
	require.Empty(t, service.currentInstanceID())

	service.serviceAccess.Lock()
	service.setInstanceLocked(&Instance{})
	service.serviceAccess.Unlock()
	second := service.currentInstanceID()
	require.NotEmpty(t, second)
	require.NotEqual(t, first, second, "a replacement instance must not reuse the identity")
}
