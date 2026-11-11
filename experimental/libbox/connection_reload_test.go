//go:build with_clash_api

package libbox

// A Box reload replaces the daemon's instance while a subscription is open. The old stream ends (its
// manager closes), the client reports the disconnect, and the next subscription's snapshot carries the
// NEW instance's id - the consumer rebuilds from it rather than applying the old instance's state.

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestAReloadReplacesTheInstanceAndTheSnapshot(t *testing.T) {
	service, _ := startSilentTailDaemon(t)
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, service)
	})

	connections := NewConnections()
	handler := &countingHandler{connections: connections}
	clientOptions := &CommandClientOptions{}
	clientOptions.AddCommand(CommandConnections)
	client := NewCommandClient(handler, clientOptions)
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	var firstInstance string
	require.Eventually(t, func() bool {
		batches := handler.snapshot()
		if len(batches) == 0 || batches[0].InstanceId == "" {
			return false
		}
		firstInstance = batches[0].InstanceId
		return true
	}, 10*time.Second, 5*time.Millisecond, "the first subscription must deliver the instance id")

	// Reload: a fresh Box (and traffic manager) replaces the running one; the old manager closes, so
	// the old subscription's done channel fires and the stream ends.
	require.NoError(t, service.StartOrReloadService(context.Background(),
		`{"log":{"level":"debug"},"experimental":{"clash_api":{}}}`, nil))
	require.Eventually(t, func() bool {
		return handler.disconnected.Load() >= 1
	}, 10*time.Second, 5*time.Millisecond, "the old stream must end when its manager closes")

	// The platform reconnects; the new snapshot carries the new instance id.
	require.NoError(t, client.connect(dialTestServer(address)))
	require.Eventually(t, func() bool {
		for _, batch := range handler.snapshot() {
			if batch.Reset && batch.InstanceId != "" && batch.InstanceId != firstInstance {
				return true
			}
		}
		return false
	}, 10*time.Second, 5*time.Millisecond,
		"the new subscription's snapshot must carry the new instance id")
}
