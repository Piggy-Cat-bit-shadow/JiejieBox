//go:build with_quic

package v2rayquic

// Window B's PMTU-black-hole group, runnable here after all: the path can be simulated in-process.
//
// `NewClient` enables path-MTU discovery on Linux and Windows (`DisablePathMTUDiscovery:
// !C.IsLinux && !C.IsWindows`) and disables it on Darwin. On a path whose real MTU is smaller than the
// probe size and which returns NO ICMP (the classic black hole), the connection must still work: the
// discovery probes are dropped, DPLPMTUD treats that as a probe failure, and the connection stays at a
// datagram size the path carries.
//
// The simulation is a net.Conn wrapper around the client's UDP socket that accepts datagrams up to the
// simulated MTU and silently drops everything larger, reporting a successful write as a black hole
// would (the sender learns nothing). No netem, no namespace, no privilege.

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// simulatedPathMTU is the largest datagram the simulated path carries: the IPv6 minimum, which is the
// size every path is required to support.
const simulatedPathMTU = 1280

type blackHoleDialer struct {
	N.Dialer
	dropped atomic.Int64
}

func (d *blackHoleDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	conn, err := d.Dialer.DialContext(ctx, network, destination)
	if err != nil {
		return nil, err
	}
	if network != N.NetworkUDP {
		return conn, nil
	}
	return &blackHoleConn{Conn: conn, dropped: &d.dropped}, nil
}

// blackHoleConn drops oversized datagrams without telling the sender, which is what a path with a
// smaller MTU and no ICMP looks like from the process.
type blackHoleConn struct {
	net.Conn
	dropped *atomic.Int64
}

func (c *blackHoleConn) Write(payload []byte) (int, error) {
	if len(payload) > simulatedPathMTU {
		c.dropped.Add(1)
		return len(payload), nil
	}
	return c.Conn.Write(payload)
}

func TestAPathThatDropsOversizedDatagramsStillCarriesTraffic(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &blackHoleDialer{Dialer: &N.DefaultDialer{}}
	client := newGenerationClient(t, server, dialer, nil)
	t.Cleanup(func() { _ = client.Close() })

	first := openStream(t, client)
	// Enough traffic that the discovery actually raises the datagram size; a tiny exchange never
	// probes, and a test that never probes cannot claim a black hole was survived.
	payload := make([]byte, 1024)
	for index := 0; index < 256; index++ {
		sendRecord(t, first, uint64(index), string(payload))
	}
	require.EqualValues(t, 1, server.streamCount.Load(),
		"the records did not reach the server through a path whose MTU is 1280 with no ICMP")

	// A second stream, to prove the connection is usable rather than merely established.
	second := openStream(t, client)
	sendRecord(t, second, 0, "and-a-second-stream")
	require.EqualValues(t, 2, server.streamCount.Load())

	require.Positive(t, dialer.dropped.Load(),
		"the simulated path dropped nothing: the workload never grew a datagram past 1280, so this "+
			"test did not exercise a black hole at all")
	t.Logf("datagrams dropped by the simulated path: %d", dialer.dropped.Load())
}

// TestAPathThatDropsEverythingFailsForTheCaller is the control for the test above: the wrapper really
// does swallow the datagrams, so the success above is caused by the traffic fitting the path and not by
// the wrapper being ignored.
//
// The caller's context is the only bound here. The transport deliberately does not impose one - an
// earlier version of this control used context.Background() and waited past ten seconds, which is not
// a defect but the documented ownership: `offerNew` dials under a context derived from the client's
// lifetime, so a caller that never gives up is a caller that waits.
func TestAPathThatDropsEverythingFailsForTheCaller(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &dropAllDialer{Dialer: &N.DefaultDialer{}}
	client := newGenerationClient(t, server, dialer, nil)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	stream, err := client.DialContext(ctx)
	elapsed := time.Since(started)
	require.Error(t, err, "a path that drops every datagram cannot establish a connection")
	require.Nil(t, stream)
	require.Less(t, elapsed, 5*time.Second,
		"the caller's deadline must end the attempt: the transport adds no timeout of its own")
}

type dropAllDialer struct {
	N.Dialer
}

func (d *dropAllDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	conn, err := d.Dialer.DialContext(ctx, network, destination)
	if err != nil {
		return nil, err
	}
	if network != N.NetworkUDP {
		return conn, nil
	}
	return &dropAllConn{Conn: conn}, nil
}

type dropAllConn struct {
	net.Conn
}

func (c *dropAllConn) Write(payload []byte) (int, error) {
	return len(payload), nil
}
