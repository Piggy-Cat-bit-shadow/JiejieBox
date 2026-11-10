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
	"sync"
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

// TestTheStreamBindingReadsTheInstanceAndIdentityTogether pins the single read boundary: a stream
// must never pair one instance's manager with another instance's identity. The test swaps the pair
// while readers capture it and checks every captured pair against the two valid combinations.
func TestTheStreamBindingReadsTheInstanceAndIdentityTogether(t *testing.T) {
	service := &StartedService{}
	first := &Instance{}
	second := &Instance{}
	const (
		firstID  = "instance-first"
		secondID = "instance-second"
	)
	// Write the pair exactly the way production does: both fields under serviceAccess.
	setPair := func(instance *Instance, identity string) {
		service.serviceAccess.Lock()
		service.instance = instance
		service.instanceID = identity
		service.serviceAccess.Unlock()
	}

	setPair(first, firstID)
	firstBinding, firstReadID, err := service.connectionStreamBinding()
	require.NoError(t, err)
	require.Same(t, first, firstBinding)
	require.Equal(t, firstID, firstReadID)

	setPair(second, secondID)
	secondBinding, secondReadID, err := service.connectionStreamBinding()
	require.NoError(t, err)
	require.Same(t, second, secondBinding)
	require.Equal(t, secondID, secondReadID)

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				boxService, instanceID, bindingErr := service.connectionStreamBinding()
				if bindingErr != nil {
					continue
				}
				switch boxService {
				case first:
					if instanceID != firstID {
						t.Errorf("stream binding paired the first instance with %q", instanceID)
						return
					}
				case second:
					if instanceID != secondID {
						t.Errorf("stream binding paired the second instance with %q", instanceID)
						return
					}
				default:
					t.Errorf("stream binding returned an unknown instance")
					return
				}
			}
		}()
	}
	for swap := 0; swap < 200; swap++ {
		if swap%2 == 0 {
			setPair(first, firstID)
		} else {
			setPair(second, secondID)
		}
	}
	close(stop)
	readers.Wait()

	service.serviceAccess.Lock()
	service.setInstanceLocked(nil)
	service.serviceAccess.Unlock()
	_, _, err = service.connectionStreamBinding()
	require.Error(t, err, "the STOPPING nil window is reported, not snapshotted")
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
