package route

import (
	"net"
	"os"
	"testing"

	B "github.com/sagernet/sing/common/bufio"

	"github.com/stretchr/testify/require"
)

// The growth budget's unit is one grown copy buffer, so the route wiring has to tell
// it how many buffers the direction it is about to start will hold. These tests pin
// that answer to the copy engine's own predicates: a direction enters the vectorised
// loop (DefaultBatchSize buffers per read) only when both a vectorised read waiter and
// a vectorised writer can be created for the two ends.

func TestGrowthBufferGeometryMatchesTheCopyEngine(t *testing.T) {
	client, server := tcpPair(t)
	defer client.Close()
	defer server.Close()

	_, readerVectorised := B.CreateVectorisedReadWaiter(server)
	_, writerVectorised := B.CreateVectorisedWriter(client)
	expected := 1
	if readerVectorised && writerVectorised {
		expected = B.DefaultBatchSize
	}
	require.Equal(t, expected, growthBuffersForDirection(client, server))

	if expected == 1 {
		t.Log("this platform/conn pair is not vectorisable; the single-buffer price is pinned")
	} else {
		t.Logf("vectorised pair: one direction is charged %d units", expected)
	}
}

// A writer that cannot take vectorised writes - the shape every TLS/mux outbound has -
// must keep the old single-buffer price, or the fix would restrict the paths that never
// had the 8x under-count.
func TestGrowthBufferGeometrySeesAPlainWriter(t *testing.T) {
	client, server := tcpPair(t)
	defer client.Close()
	defer server.Close()

	plainWriter := struct{ net.Conn }{Conn: client}
	require.Equal(t, 1, growthBuffersForDirection(plainWriter, server),
		"a writer that cannot be vectorised holds one grown buffer")
}

// The production seam must be built with the resolved weight rather than the old
// per-direction call, which is the defect P2-01 measured.
func TestGrowthThresholdAsksForTheDirectionWeight(t *testing.T) {
	content, err := os.ReadFile("conn.go")
	require.NoError(t, err)
	require.Contains(t, string(content),
		"GrowthBoundaryBuffers(class, base == earlyConnectionBufferIncreaseAfter, growthBuffersForDirection(destination, source))",
		"the seam must be sized by the direction geometry, not by a bare direction count")
}
