//go:build with_quic

package http

// A network transition retires ONE outbound's HTTP/3 connection, not the transport under every
// outbound of the process.
//
// The existing wiring test proves that InterfaceUpdated really drops the connection that carried
// the old network (a second handshake at the egress, not a cleared field). What it cannot show is
// the SCOPE of that action: NetworkManager walks every outbound, so a reset that crossed outbound
// boundaries would take unrelated tunnels down with it on every change - the same class of defect
// as a group switch interrupting a group nobody switched.
//
// The three observations here are the pass condition's:
//
//   - directional reconnect: the reset outbound reaches the egress again with a new handshake and
//     carries payload, so the new connection is real;
//   - old resource released: a stream opened before the reset no longer accepts a write, so the
//     old session is gone rather than merely unreferenced;
//   - unrelated flow continues: a second outbound, with its own client and its own connection,
//     keeps carrying payload across the first outbound's reset and reuses its own connection
//     afterwards (no extra handshake).

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// newH3WiringOutbound builds one production HTTP/3 outbound for the loopback egress.
func newH3WiringOutbound(t *testing.T, tag, address string) *Outbound {
	t.Helper()
	host, portText, err := net.SplitHostPort(address)
	require.NoError(t, err)
	portValue, err := strconv.ParseUint(portText, 10, 16)
	require.NoError(t, err)

	instance, err := NewOutbound(context.Background(), nil, log.NewNOPFactory().Logger(), tag,
		option.HTTPOutboundOptions{
			ServerOptions: option.ServerOptions{Server: host, ServerPort: uint16(portValue)},
			Version:       3,
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{
					Enabled:    true,
					ServerName: "example.test",
					Insecure:   true,
					ALPN:       []string{"h3"},
				},
			},
		})
	require.NoError(t, err)
	outbound := instance.(*Outbound)
	t.Cleanup(func() { _ = outbound.client.Close() })
	return outbound
}

// h3RoundTrip opens one tunnel through the outbound and requires the egress to echo the payload.
func h3RoundTrip(t *testing.T, outbound *Outbound, payload string) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := outbound.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("origin.test:443"))
	require.NoError(t, err, "the tunnel must be established over HTTP/3")
	require.NotNil(t, conn)
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	_, err = conn.Write([]byte(payload))
	require.NoError(t, err)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err, "the tunnel must carry payload, or nothing was established")
	require.Equal(t, payload, string(echo))
	return conn
}

func TestInterfaceUpdateRetiresOnlyItsOwnHTTP3Outbound(t *testing.T) {
	egress := startH3WiringEgress(t)
	first := newH3WiringOutbound(t, "h3-out-a", egress.address)
	second := newH3WiringOutbound(t, "h3-out-b", egress.address)

	// Both outbounds have their own connection: two handshakes, one each.
	firstConn := h3RoundTrip(t, first, "first-before")
	secondConn := h3RoundTrip(t, second, "second-before")
	t.Cleanup(func() { _ = firstConn.Close() })
	t.Cleanup(func() { _ = secondConn.Close() })
	require.Equal(t, 2, egress.acceptedCount(), "each outbound establishes its own connection")

	// The production dispatch: NetworkManager calls this on the outbound that must follow the new
	// network.
	first.InterfaceUpdated(context.Background())

	// Old resource released: the stream opened before the reset no longer takes a write.
	_ = firstConn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := firstConn.Write([]byte("stale")); err == nil {
		t.Fatal("the pre-reset stream still accepted a write: the old session was not retired")
	}

	// Unrelated flow continues: the other outbound's stream still carries payload, on ITS
	// connection, without a new handshake.
	_ = secondConn.SetDeadline(time.Now().Add(10 * time.Second))
	_, err := secondConn.Write([]byte("second-after"))
	require.NoError(t, err, "another outbound's stream is not the reset outbound's to close")
	echo := make([]byte, len("second-after"))
	_, err = io.ReadFull(secondConn, echo)
	require.NoError(t, err)
	require.Equal(t, "second-after", string(echo))

	// Directional reconnect: the reset outbound reaches the egress again with a new handshake and
	// carries payload.
	reconnected := h3RoundTrip(t, first, "first-after")
	_ = reconnected.Close()
	require.Equal(t, 3, egress.acceptedCount(),
		"the reset outbound must perform a new handshake while the unrelated one reuses its own")

	// The unrelated outbound keeps reusing its connection: no handshake was spent on it.
	reused := h3RoundTrip(t, second, "second-reused")
	_ = reused.Close()
	require.Equal(t, 3, egress.acceptedCount(),
		"the unrelated outbound must reuse its connection, not redial")
}
