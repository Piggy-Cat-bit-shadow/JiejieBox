package hysteria2

// Q05: the first family is UDP-writable but silent, the second family answers.
//
// The blackhole is a real UDP socket bound to 127.0.0.1:<port> that reads and discards everything,
// while the Hysteria2 service listens only on [::1]:<port>. The two family clients are the real
// pinned sing-quic clients; their dialers differ only in the socket family they use, which is the
// same restriction the production dual-stack carrier gets from its two domain strategies.
//
// This is the audit's counterexample, on loopback and without a device: the first family is
// writable, the second is usable, and the carrier must produce exactly one winner with working
// bidirectional traffic inside one total budget.

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	boxTLS "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-quic/hysteria2"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// fixedFamilyDialer returns UDP sockets of one family to a fixed endpoint, ignoring the destination
// the client asked for. It is the test's stand-in for the two family-restricted dialers: the client
// code under test is unchanged, only the socket family is pinned.
type fixedFamilyDialer struct {
	network string
	address string
}

func (d fixedFamilyDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, d.network, d.address)
}

func (d fixedFamilyDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	var listenConfig net.ListenConfig
	return listenConfig.ListenPacket(ctx, d.network, d.address)
}

// startHysteriaCarrierServerIPv6 starts the same pinned service as the carrier test, but bound to
// the IPv6 loopback only, so the blackhole can own the IPv4 port number.
func startHysteriaCarrierServerIPv6(t *testing.T, handler hysteria2.ServerHandler) uint16 {
	t.Helper()
	directory := t.TempDir()
	keyPEM, certPEM, err := boxTLS.GenerateCertificate(nil, nil, time.Now, "localhost", time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	certificatePath := filepath.Join(directory, "cert.pem")
	keyPath := filepath.Join(directory, "key.pem")
	require.NoError(t, os.WriteFile(certificatePath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	logger := log.NewNOPFactory().NewLogger("carrier-hysteria2-v6")
	serverTLS, err := boxTLS.NewServer(context.Background(), logger, option.InboundTLSOptions{
		Enabled:         true,
		ServerName:      "localhost",
		CertificatePath: certificatePath,
		KeyPath:         keyPath,
	})
	require.NoError(t, err)
	require.NoError(t, serverTLS.Start())
	t.Cleanup(func() { _ = serverTLS.Close() })

	service, err := hysteria2.NewService[int](hysteria2.ServiceOptions{
		Context:               context.Background(),
		Logger:                logger,
		TLSConfig:             serverTLS,
		Handler:               handler,
		MasqueradeHandler:     http404(),
		IgnoreClientBandwidth: true,
	})
	require.NoError(t, err)
	service.UpdateUsers([]int{0}, []string{hysteriaCarrierPassword})
	packetConn, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	require.NoError(t, err)
	require.NoError(t, service.Start(packetConn))
	t.Cleanup(func() { _ = service.Close() })
	return uint16(packetConn.LocalAddr().(*net.UDPAddr).Port)
}

// TestTheSilentFirstFamilyFailsOverToTheSecond is the Q05 acceptance: one winner, the losing
// family's client closed, and bidirectional business on the winning family.
func TestTheSilentFirstFamilyFailsOverToTheSecond(t *testing.T) {
	handler := &hysteriaCarrierHandler{target: startHysteriaCarrierEcho(t)}
	port := startHysteriaCarrierServerIPv6(t, handler)

	// The IPv4 blackhole: a real socket, so the client's v4 Initial really is written; it never
	// answers, which is the "UDP writable but QUIC silent" shape.
	blackhole, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = blackhole.Close() })
	var blackholed atomic.Int64
	go func() {
		buffer := make([]byte, 2048)
		for {
			if _, _, readErr := blackhole.ReadFromUDP(buffer); readErr != nil {
				return
			}
			blackholed.Add(1)
		}
	}()

	logger := log.NewNOPFactory().NewLogger("family-race")
	clientTLS, err := boxTLS.NewClient(context.Background(), logger, "localhost", option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: "localhost",
		Insecure:   true,
	})
	require.NoError(t, err)

	newClient := func(family int) (*hysteria2.Client, error) {
		dialer := fixedFamilyDialer{network: "udp4", address: fmt.Sprintf("127.0.0.1:%d", port)}
		if family == 2 {
			dialer = fixedFamilyDialer{network: "udp6", address: fmt.Sprintf("[::1]:%d", port)}
		}
		return hysteria2.NewClient(hysteria2.ClientOptions{
			Context:       context.Background(),
			Dialer:        dialer,
			Logger:        logger,
			ServerAddress: M.ParseSocksaddr(fmt.Sprintf("server.test:%d", port)),
			Password:      hysteriaCarrierPassword,
			TLSConfig:     clientTLS,
		})
	}
	primary, err := newClient(1)
	require.NoError(t, err)
	secondary, err := newClient(2)
	require.NoError(t, err)
	carrier := newFamilyCarrier(newClient)
	carrier.clients[0] = primary
	carrier.clients[1] = secondary
	carrier.families = 2
	t.Cleanup(func() { _ = carrier.CloseWithError(net.ErrClosed) })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := carrier.DialConn(ctx, M.ParseSocksaddr("echo.test:443"))
	require.NoError(t, err, "the healthy family must win within the caller's single budget")
	require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))
	payload := make([]byte, 4096)
	for index := range payload {
		payload[index] = byte(index)
	}
	_, err = conn.Write(payload)
	require.NoError(t, err)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err)
	require.Equal(t, payload, echo, "business bytes must flow in both directions on the winning family")
	_ = conn.Close()

	require.Greater(t, blackholed.Load(), int64(0),
		"the IPv4 family must have been attempted and stayed silent")
	require.Eventually(t, func() bool {
		carrier.access.Lock()
		defer carrier.access.Unlock()
		return carrier.winner == 2 && carrier.clients[0] == nil
	}, 5*time.Second, 20*time.Millisecond,
		"the IPv6 family must be the single winner and the losing family's client must be closed")
}

// TestADualStackDomainBuildsTheFamilyCarrier pins the production wiring: a domain server without a
// realm builds two family clients; a literal or realm server keeps the single-client behaviour.
func TestADualStackDomainBuildsTheFamilyCarrier(t *testing.T) {
	created, err := NewOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("family-wiring"), "family-wiring",
		option.Hysteria2OutboundOptions{
			ServerOptions: option.ServerOptions{Server: "localhost", ServerPort: 443},
			Password:      "password",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{Enabled: true, ServerName: "localhost", Insecure: true},
			},
		})
	require.NoError(t, err)
	outbound := created.(*Outbound)
	require.Equal(t, 2, outbound.client.families)
	require.NoError(t, outbound.client.CloseWithError(net.ErrClosed))
}
