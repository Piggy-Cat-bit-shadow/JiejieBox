//go:build with_quic

package http

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The real-connection L4 case.
//
// The unit matrix proves the ownership rules against a scripted boundary; this proves the same
// production functions over a live HTTP/3 connection, which is the one thing a scripted transport
// cannot answer: whether the bytes actually arrive, in order, once, after the owned path handed
// them to quic-go.
//
// It is deliberately separate from the CONNECT-IP end-to-end test: passing there says nothing about
// CONNECT-UDP, because the two use different request shapes and different packet connections.

// connectUDPTestClient builds the same production Client the CONNECT-IP e2e helper builds, so the
// only difference between the two tests is the request the client makes.
func connectUDPTestClient(t *testing.T, address string) *Client {
	t.Helper()
	clientTLS, err := tls.NewSTDClient(t.Context(), logger.NOP(), "example.test",
		option.OutboundTLSOptions{
			Enabled:    true,
			ServerName: "example.test",
			Insecure:   true,
			ALPN:       []string{"h3"},
		})
	require.NoError(t, err)

	impl := &http3ClientImpl{
		dialer:    &connectedUDPDialer{},
		server:    M.ParseSocksaddr(address),
		authority: address,
		tlsConfig: clientTLS,
		quicConfig: &quic.Config{
			HandshakeIdleTimeout: 5 * time.Second,
			MaxIdleTimeout:       60 * time.Second,
			EnableDatagrams:      true,
		},
		transport: &http3.Transport{EnableDatagrams: true, DisableCompression: true},
	}
	impl.connectCandidate = impl.connectCandidateAt

	return &Client{
		dialer:            &connectedUDPDialer{},
		http1Dialer:       &connectedUDPDialer{},
		authorityOverride: address,
		version:           3,
		server:            M.ParseSocksaddr(address),
		http3:             impl,
		http3Authority:    address,
	}
}

// connectUDPTunnel opens a real CONNECT-UDP tunnel and returns the production packet connection,
// unwrapped, so the test can inspect the capability the layer below resolved.
func connectUDPTunnel(t *testing.T, address string) *http3PacketConn {
	t.Helper()
	client := connectUDPTestClient(t, address)
	packetConn, err := client.listenPacket(context.Background(), M.ParseSocksaddr("echo.test:443"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = packetConn.Close() })
	concrete, ok := packetConn.(*http3PacketConn)
	require.True(t, ok, "an HTTP/3 CONNECT-UDP tunnel must produce the HTTP/3 packet connection")
	return concrete
}

func newOwnedPayload(payload []byte) *buf.Buffer {
	buffer := buf.NewSize(connectUDPOwnedHeadroom + len(payload))
	buffer.Resize(connectUDPOwnedHeadroom, 0)
	buffer.Write(payload)
	return buffer
}

// TestConnectUDPOwnedOverRealHTTP3 is the end-to-end proof: the owned capability exists on a real
// connection, the geometry is right, and every byte arrives exactly once and in order.
func TestConnectUDPOwnedOverRealHTTP3(t *testing.T) {
	server := startOwnedPathServer(t)
	conn := connectUDPTunnel(t, server.address)

	require.NotNil(t, conn.ownedDatagrams,
		"a real HTTP/3 datagram stream must expose the owned path; nil means the zero-copy path is dead code")
	require.Equal(t, connectUDPOwnedHeadroom, conn.FrontHeadroom(),
		"the producer must be told to reserve the Context ID plus the widest quarter-stream-ID varint")

	const packets = 32
	payloads := make([][]byte, packets)
	for i := range payloads {
		payloads[i] = bytes.Repeat([]byte{byte(i + 1)}, 64+i)
	}
	for i, payload := range payloads {
		require.NoError(t, conn.WritePacket(newOwnedPayload(payload), conn.destination),
			"packet %d must be accepted", i)
	}

	require.Eventually(t, func() bool {
		return len(server.payloads()) >= packets
	}, 10*time.Second, 10*time.Millisecond, "the server did not receive every packet")

	received := server.payloads()
	require.Len(t, received, packets, "exactly one datagram per packet, no duplicates")
	for i, datagram := range received {
		require.Equal(t, byte(0), datagram[0], "datagram %d carries Context ID 0", i)
		require.Equal(t, payloads[i], datagram[1:], "datagram %d carries its own payload, in order", i)
	}
}

// TestConnectUDPOwnedBatchOverRealHTTP3 drives the production batch writer over a live connection,
// which is the L4 call chain the CONNECT-IP end-to-end test does not cover.
func TestConnectUDPOwnedBatchOverRealHTTP3(t *testing.T) {
	server := startOwnedPathServer(t)
	conn := connectUDPTunnel(t, server.address)

	writer, ok := bufio.CreateConnectedPacketBatchWriter(conn)
	require.True(t, ok, "the connection advertises the connected batch writer")
	require.NotNil(t, conn.ownedDatagrams)

	const packets = 16
	batch := make([]*buf.Buffer, packets)
	payloads := make([][]byte, packets)
	for i := range batch {
		payloads[i] = []byte(fmt.Sprintf("batch-packet-%02d", i))
		batch[i] = newOwnedPayload(payloads[i])
	}
	require.NoError(t, writer.WriteConnectedPacketBatch(batch))

	require.Eventually(t, func() bool {
		return len(server.payloads()) >= packets
	}, 10*time.Second, 10*time.Millisecond, "the batch never reached the server")

	received := server.payloads()
	require.Len(t, received, packets, "a batch must not merge or duplicate packets")
	for i, datagram := range received {
		require.Equal(t, byte(0), datagram[0])
		require.Equal(t, payloads[i], datagram[1:], "batch packet %d is out of order", i)
	}
}
