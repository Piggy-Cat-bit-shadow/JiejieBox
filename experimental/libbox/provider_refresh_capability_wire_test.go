package libbox

// F — the capability gap on the real wire.
//
// The unit test pins the DOCUMENT that is produced for a missing RPC; this one produces it the way
// a real deployment does: a gRPC service that does not implement `RefreshRemoteProviders` answers
// UNIMPLEMENTED, and the client must turn that into a successful `{"supported":false}` result.
//
// Why it matters: a gomobile call that returns an error loses its value in the generated bridge, so
// the app's old `NSError.code == 12` test never matched anything and the capability gap reached the
// user as "the refresh failed". The translation lives in Go, where the gRPC code still exists.

import (
	"encoding/json"
	"testing"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestAnOldDaemonAnswersAProviderRefreshWithACapabilityDocument(t *testing.T) {
	// Nothing registers the started service: every method answers UNIMPLEMENTED, exactly like a
	// kernel that predates the RPC.
	address := bridgeTestServer(t, func(*grpc.Server) {})
	client := bridgeTestClient(t, address)

	document, err := client.RefreshRemoteProviders("", 1000)
	require.NoError(t, err, "a missing capability is a fact, not a failed call")
	require.NotNil(t, document)

	var decoded struct {
		Supported bool   `json:"supported"`
		Reason    string `json:"reason"`
		Results   []any  `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(document.Value), &decoded), document.Value)
	require.False(t, decoded.Supported)
	require.Contains(t, decoded.Reason, "predates")
	require.Empty(t, decoded.Results, "the capability answer must not look like a report with no providers")
}

// TestTheCapabilityDocumentIsAlsoProducedByTheRealServer checks the other half of the same wire:
// against a live `StartedService` with no running Box, the call is a refused precondition and NOT a
// capability gap - the two must not be confused by the client.
func TestTheCapabilityDocumentIsAlsoProducedByTheRealServer(t *testing.T) {
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, daemon.NewStartedService(daemon.ServiceOptions{Context: t.Context()}))
	})
	client := bridgeTestClient(t, address)

	document, err := client.RefreshRemoteProviders("", 1000)
	require.Error(t, err, "a daemon that has the RPC but no running service must say so")
	require.Nil(t, document)
	require.Contains(t, err.Error(), "not started")
}
