//go:build with_quic && linux

package v2rayquic

// Isolated two-path migration evidence inside network namespaces.
//
// The loopback tests prove the state machine; this one proves the PATH. Two namespaces are joined
// by two independent veth links, the server holds ONE address reachable over both, and the client
// dials it from a different source address per link. The test then physically takes the first link
// down and requires the already-open flow to keep carrying acknowledged bytes after Reset moved
// the connection to the second link.
//
// It is driven by a shell harness (see the window's evidence directory) because a test binary can
// only live in one namespace at a time: the same binary runs in the server namespace with
// role=server and in the client namespace with role=client. Nothing here runs in a normal test
// run; without V2RAYQUIC_NS_DUALPATH=1 the test skips.

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

const (
	dualPathEnableEnv = "V2RAYQUIC_NS_DUALPATH"
	dualPathRoleEnv   = "V2RAYQUIC_NS_ROLE"
	dualPathStateEnv  = "V2RAYQUIC_NS_STATE"
	dualPathAddrEnv   = "V2RAYQUIC_NS_ADDR"
	dualPathFirstLink = "va1"
)

// dualPathCounters is what the server role publishes for the client role to check: a migration
// that secretly reconnected would show more than one handshake or more than one stream.
type dualPathCounters struct {
	Handshakes int32 `json:"handshakes"`
	Streams    int32 `json:"streams"`
}

// dualPathDialer binds every socket it creates to the next source address: the first dial (the
// handshake) leaves on the first link, every later one (a migration candidate) on the second.
type dualPathDialer struct {
	access sync.Mutex
	locals []string
	calls  int
}

func (d *dualPathDialer) next() string {
	d.access.Lock()
	defer d.access.Unlock()
	local := d.locals[len(d.locals)-1]
	if d.calls < len(d.locals) {
		local = d.locals[d.calls]
	}
	d.calls++
	return local
}

func (d *dualPathDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	dialer := &net.Dialer{
		LocalAddr: &net.UDPAddr{IP: net.ParseIP(d.next())},
		Timeout:   10 * time.Second,
	}
	return dialer.DialContext(ctx, network, destination.String())
}

func (d *dualPathDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(d.next())})
}

func (d *dualPathDialer) count() int {
	d.access.Lock()
	defer d.access.Unlock()
	return d.calls
}

// TestIsolatedDualPathMigration runs one half of the namespace scenario.
func TestIsolatedDualPathMigration(t *testing.T) {
	if os.Getenv(dualPathEnableEnv) != "1" {
		t.Skip("set " + dualPathEnableEnv + "=1 and run the namespace harness instead")
	}
	state := os.Getenv(dualPathStateEnv)
	require.NotEmpty(t, state)
	serverAddr := os.Getenv(dualPathAddrEnv)
	require.NotEmpty(t, serverAddr)

	if os.Getenv(dualPathRoleEnv) == "server" {
		runDualPathServer(t, state)
		return
	}
	runDualPathClient(t, state, serverAddr)
}

func runDualPathServer(t *testing.T, state string) {
	server := startMigrationServerAt(t, "0.0.0.0:0", nil)
	_, port, splitErr := net.SplitHostPort(server.addr.String())
	require.NoError(t, splitErr)
	require.NoError(t, os.WriteFile(filepath.Join(state, "port"), []byte(port), 0o600))
	t.Logf("server listening on %s", server.addr)

	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		counters := dualPathCounters{
			Handshakes: server.handshakes.Load(),
			Streams:    server.streamCount.Load(),
		}
		if encoded, marshalErr := json.Marshal(counters); marshalErr == nil {
			_ = os.WriteFile(filepath.Join(state, "counters"), encoded, 0o600)
		}
		if _, statErr := os.Stat(filepath.Join(state, "done")); statErr == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func runDualPathClient(t *testing.T, state, serverAddr string) {
	migrationTrace = t.Logf
	t.Cleanup(func() { migrationTrace = nil })
	port := waitForDualPathFile(t, filepath.Join(state, "port"), 30*time.Second)
	localA := os.Getenv("V2RAYQUIC_NS_LOCAL_A")
	localB := os.Getenv("V2RAYQUIC_NS_LOCAL_B")
	require.NotEmpty(t, localA)
	require.NotEmpty(t, localB)
	dialer := &dualPathDialer{locals: []string{localA, localB}}

	resolved, err := net.ResolveUDPAddr("udp", net.JoinHostPort(serverAddr, port))
	require.NoError(t, err)
	server := &migrationServer{addr: resolved}
	client := newMigratingClient(t, server, dialer)
	t.Cleanup(func() { _ = client.Close() })

	stream := openStream(t, client)
	sendRecord(t, stream, 0, "before-the-link-went-down")

	// Physically take the first link down, the way losing Wi-Fi does: the socket's route is gone
	// and the peer is reachable over the second link only.
	require.NoError(t, exec.Command("ip", "link", "set", dualPathFirstLink, "down").Run())
	t.Logf("link %s is down", dualPathFirstLink)

	started := time.Now()
	require.NoError(t, client.Reset(), "the network change must be handled without an error")
	elapsed := time.Since(started)
	require.Less(t, elapsed, migrationProbeBudget+migrationRetireBudget+2*time.Second)
	require.Equal(t, 2, dialer.count(), "one candidate socket, bound to the second link")
	t.Logf("migration after the link went down took %s", elapsed)
	if conn := client.conn.Load(); conn == nil {
		t.Log("after Reset: no published connection")
	} else if path := conn.currentPath(); path != nil {
		t.Logf("after Reset: send socket %v, connection error %v", path.socket.LocalAddr(), conn.Context().Err())
	}

	// The flow opened before the link went down keeps carrying acknowledged bytes on the same
	// stream, now over the second link.
	sendRecord(t, stream, 1, "after-the-link-went-down")

	// The endpoint must still see exactly one handshake and one stream: the connection moved, it
	// did not reconnect.
	require.NoError(t, os.WriteFile(filepath.Join(state, "done"), []byte("done"), 0o600))
	var counters dualPathCounters
	require.Eventually(t, func() bool {
		raw, readErr := os.ReadFile(filepath.Join(state, "counters"))
		if readErr != nil {
			return false
		}
		if unmarshalErr := json.Unmarshal(raw, &counters); unmarshalErr != nil {
			return false
		}
		return counters.Handshakes == 1
	}, 10*time.Second, 50*time.Millisecond, "the endpoint saw more than one handshake")
	require.Equal(t, int32(1), counters.Streams, "the endpoint saw more than one stream")
}

func waitForDualPathFile(t *testing.T, path string, timeout time.Duration) string {
	t.Helper()
	var content string
	require.Eventually(t, func() bool {
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) == 0 {
			return false
		}
		content = string(raw)
		return true
	}, timeout, 20*time.Millisecond, "the server never published %s", path)
	return content
}

var _ N.Dialer = (*dualPathDialer)(nil)
