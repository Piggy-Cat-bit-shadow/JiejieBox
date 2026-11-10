package daemon

// The selection acknowledgement.
//
// `SelectOutbound` used to return an empty message: the caller learned that the RPC succeeded and
// nothing else. A client that keeps a pending selection (the phone's group buttons do) could not
// tell a confirmation for ITS request from one for an older attempt, and could not read back what
// the group actually holds.
//
// The response now carries the caller's request id, the group's revision after the selection, the
// member the group really selects, and whether the caller's view was stale. These tests drive the
// real service with a real Box.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

const selectorOutboundConfig = `{"log":{"level":"error"},"outbounds":[
	{"type":"direct","tag":"a"},
	{"type":"direct","tag":"b"},
	{"type":"selector","tag":"sel","outbounds":["a","b"]}
],"route":{"final":"sel"}}`

// selectorService starts a real Box with the direct/selector outbound set this test needs.
func selectorService(t *testing.T, profile string) *StartedService {
	t.Helper()
	service := NewStartedService(ServiceOptions{Context: recoveryBusinessContext(t)})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	require.NoError(t, service.StartOrReloadService(context.Background(), profile, nil))
	return service
}

func TestSelectOutboundEchoesTheRequestAndReadsBackTheGroup(t *testing.T) {
	service := selectorService(t, selectorOutboundConfig)

	first, err := service.SelectOutbound(context.Background(), &SelectOutboundRequest{
		GroupTag:    "sel",
		OutboundTag: "b",
		RequestId:   41,
	})
	require.NoError(t, err)
	require.EqualValues(t, 41, first.RequestId, "the caller needs its own identity back")
	require.Equal(t, "b", first.AppliedOutboundTag, "the response reports the member the group holds")
	require.NotZero(t, first.AppliedRevision, "a selection that changed the member advances the revision")
	require.False(t, first.Stale, "no expectation was expressed")

	// Selecting the member that is already selected changes nothing, so the revision does not move:
	// a client must not treat "applied" as "changed".
	second, err := service.SelectOutbound(context.Background(), &SelectOutboundRequest{
		GroupTag:         "sel",
		OutboundTag:      "b",
		RequestId:        42,
		ExpectedRevision: first.AppliedRevision,
	})
	require.NoError(t, err)
	require.EqualValues(t, 42, second.RequestId)
	require.Equal(t, first.AppliedRevision, second.AppliedRevision)
	require.False(t, second.Stale)

	// A caller whose expected revision is not the state the decision was taken against gets
	// stale=true; the selection still applies, because refusing it would make a race permanent.
	third, err := service.SelectOutbound(context.Background(), &SelectOutboundRequest{
		GroupTag:         "sel",
		OutboundTag:      "a",
		RequestId:        43,
		ExpectedRevision: 999,
	})
	require.NoError(t, err)
	require.True(t, third.Stale, "the caller's view did not match the group")
	require.Equal(t, "a", third.AppliedOutboundTag)
	require.Greater(t, third.AppliedRevision, second.AppliedRevision,
		"moving to a different member advances the revision")
}

func TestSelectOutboundReportsAnUnknownMember(t *testing.T) {
	service := selectorService(t, selectorOutboundConfig)
	_, err := service.SelectOutbound(context.Background(), &SelectOutboundRequest{
		GroupTag:    "sel",
		OutboundTag: "does-not-exist",
		RequestId:   7,
	})
	require.Error(t, err, "an unknown member is an error, not a silent no-op")
}

// TestThreeSelectionsCarryThreeIdentities is the A->B->C shape: three attempts, three request ids,
// and each answer names the member the group held after ITS request. A confirmation that arrives
// late carries an older request id, so a caller matching on the id cannot settle the wrong attempt.
func TestThreeSelectionsCarryThreeIdentities(t *testing.T) {
	service := selectorService(t, selectorOutboundConfig)
	tags := []string{"b", "a", "b"}
	responses := make([]*SelectOutboundResponse, 0, len(tags))
	for index, tag := range tags {
		response, err := service.SelectOutbound(context.Background(), &SelectOutboundRequest{
			GroupTag:    "sel",
			OutboundTag: tag,
			RequestId:   uint64(index + 1),
		})
		require.NoError(t, err)
		require.EqualValues(t, index+1, response.RequestId)
		require.Equal(t, tag, response.AppliedOutboundTag)
		responses = append(responses, response)
	}
	require.Greater(t, responses[1].AppliedRevision, responses[0].AppliedRevision)
	require.Greater(t, responses[2].AppliedRevision, responses[1].AppliedRevision)

	// A duplicate request (the caller retried because the answer was slow) must not look like a
	// second change: the revision is unchanged, and the answer still names the same member.
	duplicate, err := service.SelectOutbound(context.Background(), &SelectOutboundRequest{
		GroupTag:    "sel",
		OutboundTag: "b",
		RequestId:   3,
	})
	require.NoError(t, err)
	require.EqualValues(t, 3, duplicate.RequestId)
	require.Equal(t, responses[2].AppliedRevision, duplicate.AppliedRevision)
	require.Equal(t, "b", duplicate.AppliedOutboundTag)
}
