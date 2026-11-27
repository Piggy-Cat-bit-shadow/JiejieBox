package route

import (
	"io"
	"net"
	"os"
	"testing"

	B "github.com/sagernet/sing/common/bufio"
	N "github.com/sagernet/sing/common/network"

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

// The production seam must be built from the copy operands, not from the raw route
// arguments: the engine strips counting/replaceable wrappers and decides after that
// (MD-005). The structural assertion pins the call site; the behavioural one below
// proves the probe really unwraps.
func TestGrowthThresholdAsksForTheDirectionWeight(t *testing.T) {
	content, err := os.ReadFile("conn.go")
	require.NoError(t, err)
	source := string(content)
	require.Contains(t, source, "GrowthBoundaryFor(plan.class, plan.geometry, func() int {",
		"the weight must be resolved from the operands the copy engine uses, at the boundary")
	require.Contains(t, source, "growthBuffersForDirection(source, copyWriter)")
}

// countedConn is a counting wrapper shaped like the ones the production path puts in
// front of the copy: it hides the vectorised capability of the inner connection, but it
// is unwrappable, and sing strips exactly these layers before it decides.
type countedConn struct {
	net.Conn
	read  int64
	write int64
}

func (c *countedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.read += int64(n)
	return n, err
}

func (c *countedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.write += int64(n)
	return n, err
}

func (c *countedConn) UnwrapReader() (io.Reader, []N.CountFunc) {
	return c.Conn, []N.CountFunc{func(n int64) {}}
}

func (c *countedConn) UnwrapWriter() (io.Writer, []N.CountFunc) {
	return c.Conn, []N.CountFunc{func(n int64) {}}
}

// TestGrowthBufferGeometryUnwrapsTheCountingLayer is MD-005: the tracker's counting
// connection (and the sniffing cached connection) sits in front of the real socket, so a
// probe of the wrapper says "not vectorisable" while the engine, after unwrapping,
// vectorises and holds eight buffers. The weight must be asked of the unwrapped layer.
func TestGrowthBufferGeometryUnwrapsTheCountingLayer(t *testing.T) {
	client, server := tcpPair(t)
	defer client.Close()
	defer server.Close()

	countedSource := &countedConn{Conn: server}
	countedDestination := &countedConn{Conn: client}

	// The wrapper itself is not vectorisable - this is the layer that used to be probed.
	_, wrapperVectorised := B.CreateVectorisedWriter(countedDestination)
	require.False(t, wrapperVectorised, "the counting wrapper must hide the vectorised writer")

	// The engine unwraps to the socket, so the direction really does hold the batch.
	require.Equal(t, B.DefaultBatchSize, growthBuffersForDirection(countedSource, countedDestination),
		"the weight must be resolved after the counting layer is stripped")
}
