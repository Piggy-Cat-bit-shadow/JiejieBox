package mieru

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	mieruconstant "github.com/enfein/mieru/v3/apis/constant"
	mierumodel "github.com/enfein/mieru/v3/apis/model"

	"github.com/stretchr/testify/require"
)

// TestTrackedConnRemovesItselfFromRegistry pins the bookkeeping that keeps the
// inbound's live-connection registry bounded. Without it, every finished
// session would stay in the map until the inbound closed.
func TestTrackedConnRemovesItselfFromRegistry(t *testing.T) {
	registry := connRegistry{conns: make(map[net.Conn]struct{})}
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	tracked := &trackedConn{Conn: serverConn, registry: &registry}
	require.True(t, registry.add(tracked))
	require.Len(t, registry.conns, 1)
	require.NoError(t, tracked.Close())
	require.Empty(t, registry.conns)

	// CloseAll closes every remaining connection and refuses new ones.
	secondClient, secondServer := net.Pipe()
	defer secondClient.Close()
	second := &trackedConn{Conn: secondServer, registry: &registry}
	require.True(t, registry.add(second))
	registry.closeAll()
	require.Empty(t, registry.conns)
	require.False(t, registry.add(&trackedConn{Conn: secondClient, registry: &registry}))
}

type namedConn struct {
	net.Conn
	name string
}

func (c *namedConn) UserName() string { return c.name }

func TestTrackedConnPreservesUserName(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	tracked := &trackedConn{Conn: &namedConn{Conn: serverConn, name: "alice"}}
	require.Equal(t, "alice", tracked.UserName())
	var userContext interface{ UserName() string } = tracked
	require.Equal(t, "alice", userContext.UserName())
}

// TestHandleConnectionRejectsBeforeSuccess pins the SOCKS5 reply for requests
// this server cannot route. An unsupported command or an unparseable
// destination must not receive the synthetic success reply.
func TestHandleConnectionRejectsBeforeSuccess(t *testing.T) {
	testCases := []struct {
		name    string
		request *mierumodel.Request
		reply   byte
	}{
		{
			name: "unsupported command",
			request: &mierumodel.Request{
				Command: 0x7f,
				DstAddr: mierumodel.AddrSpec{IP: net.IPv4(192, 0, 2, 1), Port: 80},
			},
			reply: mieruconstant.Socks5ReplyCommandNotSupported,
		},
		{
			name: "invalid destination port",
			request: &mierumodel.Request{
				Command: mieruconstant.Socks5ConnectCmd,
				DstAddr: mierumodel.AddrSpec{IP: net.IPv4(192, 0, 2, 1), Port: 70000},
			},
			reply: mieruconstant.Socks5ReplyServerFailure,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			created, err := NewInbound(context.Background(), newEchoRouter(), nopLogger(), "mieru-in", validInboundOptions())
			require.NoError(t, err)
			inboundInstance := created.(*Inbound)

			clientConn, serverConn := net.Pipe()
			defer clientConn.Close()
			done := make(chan struct{})
			go func() {
				inboundInstance.handleConnection(serverConn, testCase.request)
				close(done)
			}()
			require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(5*time.Second)))
			response := make([]byte, 10)
			_, err = io.ReadFull(clientConn, response)
			require.NoError(t, err)
			require.Equal(t, byte(0x05), response[0], "SOCKS5 version")
			require.Equal(t, testCase.reply, response[1], "SOCKS5 reply")
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("handleConnection did not return after the failure reply")
			}
		})
	}
}

// running is closed rather than tracked into a registry nobody will walk.
func TestConnRegistryClosedBeforeAdd(t *testing.T) {
	registry := connRegistry{conns: make(map[net.Conn]struct{})}
	registry.closeAll()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	require.False(t, registry.add(&trackedConn{Conn: serverConn, registry: &registry}))
}
