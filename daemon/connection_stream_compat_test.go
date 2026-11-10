package daemon

// Forward compatibility of the two contracts this window added.
//
// The claims are "an old producer's bytes still decode" and "an old service's answer is
// recognisable as unconfirmed". Both are stated in the construction notes; these tests pin them at
// the WIRE level, with bytes built by hand instead of structs marshalled from the same generated
// type - marshalling the new type would only test that proto3 omits zero fields, not that an
// older producer's payload is accepted.

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/stretchr/testify/require"
)

// legacyConnectionEventsBytes is what a daemon built BEFORE this window sends: `events` (field 1)
// whose elements carry type (1) and id (2) but no sequence (7), and `reset` (field 2) but no
// instance (3) or dropped (4).
func legacyConnectionEventsBytes(t *testing.T) []byte {
	t.Helper()
	var event []byte
	event = protowire.AppendTag(event, 1, protowire.VarintType) // type = NEW
	event = protowire.AppendVarint(event, 0)
	event = protowire.AppendTag(event, 2, protowire.BytesType) // id
	event = protowire.AppendString(event, "legacy-connection")

	var message []byte
	message = protowire.AppendTag(message, 1, protowire.BytesType) // events[0]
	message = protowire.AppendBytes(message, event)
	message = protowire.AppendTag(message, 2, protowire.VarintType) // reset = true
	message = protowire.AppendVarint(message, 1)
	return message
}

func TestAnOlderDaemonsConnectionEventsStillDecode(t *testing.T) {
	var decoded ConnectionEvents
	require.NoError(t, proto.Unmarshal(legacyConnectionEventsBytes(t), &decoded))

	require.Len(t, decoded.Events, 1)
	require.Equal(t, "legacy-connection", decoded.Events[0].Id)
	require.True(t, decoded.Reset_)
	// The new fields default to zero, which is what "this producer does not report identity or
	// loss" has to mean: a client must not read 0 as a real instance or a real drop count.
	require.Zero(t, decoded.Instance)
	require.Zero(t, decoded.Dropped)
	require.Zero(t, decoded.Events[0].Sequence)
}

func TestAnOlderDaemonsSelectionConfirmationIsUnconfirmed(t *testing.T) {
	// `SelectOutbound` used to return google.protobuf.Empty. Its wire form is zero bytes, so a new
	// client decoding that answer gets a zero response - and a zero requestId is exactly the
	// "no confirmation for this attempt" the caller has to treat it as.
	legacyAnswer, err := proto.Marshal(&emptypb.Empty{})
	require.NoError(t, err)
	require.Empty(t, legacyAnswer)

	var response SelectOutboundResponse
	require.NoError(t, proto.Unmarshal(legacyAnswer, &response))
	require.Zero(t, response.RequestId, "an old daemon cannot confirm a request it never saw")
	require.Empty(t, response.AppliedOutboundTag)
	require.False(t, response.Stale)
}

func TestANewMessageIsReadableByAnOlderDecoder(t *testing.T) {
	// The other direction: unknown fields are retained by protobuf, and an old decoder that only
	// knows `events`/`reset` must still see those. This is the property that lets an old client keep
	// working against the new daemon; it is a proto guarantee, asserted here so a future hand-rolled
	// encoder cannot silently drop field 1 or 2.
	encoded, err := proto.Marshal(&ConnectionEvents{
		Events:   []*ConnectionEvent{{Type: ConnectionEventType_CONNECTION_EVENT_NEW, Id: "new"}},
		Reset_:   true,
		Instance: 42,
		Dropped:  7,
	})
	require.NoError(t, err)

	// Decode only the fields an older reader knows, using the same hand-rolled reader.
	fields := 0
	for len(encoded) > 0 {
		number, wireType, n := protowire.ConsumeTag(encoded)
		require.GreaterOrEqual(t, n, 0, "the message must be well-formed")
		encoded = encoded[n:]
		switch number {
		case 1:
			require.Equal(t, protowire.BytesType, wireType)
			value, m := protowire.ConsumeBytes(encoded)
			require.GreaterOrEqual(t, m, 0)
			encoded = encoded[m:]
			require.NotEmpty(t, value)
			fields++
		case 2:
			require.Equal(t, protowire.VarintType, wireType)
			_, m := protowire.ConsumeVarint(encoded)
			require.GreaterOrEqual(t, m, 0)
			encoded = encoded[m:]
			fields++
		default:
			m := protowire.ConsumeFieldValue(number, wireType, encoded)
			require.GreaterOrEqual(t, m, 0)
			encoded = encoded[m:]
		}
	}
	require.Equal(t, 2, fields, "both fields an older reader knows survive the round trip")
}
