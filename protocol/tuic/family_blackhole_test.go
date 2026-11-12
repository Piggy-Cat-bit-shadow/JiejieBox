package tuic

// Q05 for TUIC: the first family is UDP-writable but silent, the second answers.
//
// Same shape as the Hysteria2 acceptance: a real v4 socket reads and discards, the TUIC service
// listens on [::1] only, and two real pinned clients (one per family) race one creation. The winner
// must carry a bidirectional stream, the loser must be closed, and both attempts must share the
// caller's one context.

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
	"github.com/sagernet/sing-quic/tuic"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/gofrs/uuid/v5"
	"github.com/stretchr/testify/require"
)

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

// startCarrierServerIPv6 is the pinned TUIC service bound to the IPv6 loopback only, so the
// blackhole can own the IPv4 port number.
func startCarrierServerIPv6(t *testing.T, handler tuic.ServiceHandler) uint16 {
	t.Helper()
	directory := t.TempDir()
	keyPEM, certPEM, err := boxTLS.GenerateCertificate(nil, nil, time.Now, "localhost", time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	certificatePath := filepath.Join(directory, "cert.pem")
	keyPath := filepath.Join(directory, "key.pem")
	require.NoError(t, os.WriteFile(certificatePath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	logger := log.NewNOPFactory().NewLogger("carrier-tuic-v6")
	serverTLS, err := boxTLS.NewServer(context.Background(), logger, option.InboundTLSOptions{
		Enabled:         true,
		ServerName:      "localhost",
		CertificatePath: certificatePath,
		KeyPath:         keyPath,
	})
	require.NoError(t, err)
	require.NoError(t, serverTLS.Start())
	t.Cleanup(func() { _ = serverTLS.Close() })

	service, err := tuic.NewService[int](tuic.ServiceOptions{
		Context:   context.Background(),
		Logger:    logger,
		TLSConfig: serverTLS,
		Handler:   handler,
	})
	require.NoError(t, err)
	packetConn, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback})
	require.NoError(t, err)
	require.NoError(t, service.Start(packetConn))
	t.Cleanup(func() { _ = service.Close() })

	userUUID, err := uuid.FromString(carrierTestUUID)
	require.NoError(t, err)
	service.UpdateUsers([]int{0}, [][16]byte{userUUID}, []string{carrierTestPassword})
	return uint16(packetConn.LocalAddr().(*net.UDPAddr).Port)
}

func TestTheSilentFirstFamilyFailsOverToTheSecond(t *testing.T) {
	handler := &carrierHandler{target: startCarrierEcho(t)}
	port := startCarrierServerIPv6(t, handler)

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

	logger := log.NewNOPFactory().NewLogger("tuic-family-race")
	clientTLS, err := boxTLS.NewClient(context.Background(), logger, "localhost", option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: "localhost",
		Insecure:   true,
	})
	require.NoError(t, err)
	userUUID, err := uuid.FromString(carrierTestUUID)
	require.NoError(t, err)

	newClient := func(family int) (*tuic.Client, error) {
		dialer := fixedFamilyDialer{network: "udp4", address: fmt.Sprintf("127.0.0.1:%d", port)}
		if family == 2 {
			dialer = fixedFamilyDialer{network: "udp6", address: fmt.Sprintf("[::1]:%d", port)}
		}
		return tuic.NewClient(tuic.ClientOptions{
			Context:       context.Background(),
			Dialer:        dialer,
			ServerAddress: M.ParseSocksaddr(fmt.Sprintf("server.test:%d", port)),
			TLSConfig:     clientTLS,
			UUID:          userUUID,
			Password:      carrierTestPassword,
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
	payload := []byte("tuic-family-failover-payload")
	_, err = conn.Write(payload)
	require.NoError(t, err)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err)
	require.Equal(t, payload, echo)
	ledger := carrier.ResidencyLedger()
	require.True(t, ledger.Answered)
	require.EqualValues(t, 1, ledger.Active, "the established session with a live stream is Active")
	_ = conn.Close()
	require.EqualValues(t, 1, carrier.ResidencyLedger().Idle, "a session with no live stream is Idle")

	require.Greater(t, blackholed.Load(), int64(0), "the IPv4 family must have been attempted and stayed silent")
	require.Eventually(t, func() bool {
		carrier.access.Lock()
		defer carrier.access.Unlock()
		return carrier.winner == 2 && carrier.clients[0] == nil
	}, 5*time.Second, 20*time.Millisecond,
		"the IPv6 family must be the single winner and the losing client must be closed")
	_ = carrier.CloseWithError(net.ErrClosed)
	require.EqualValues(t, 1, carrier.ResidencyLedger().Terminated, "the ended session is counted once")
}

// TestADualStackDomainBuildsTheFamilyCarrier pins the production wiring for TUIC.
func TestADualStackDomainBuildsTheFamilyCarrier(t *testing.T) {
	created, err := NewOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("tuic-family-wiring"), "tuic-family-wiring",
		option.TUICOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "localhost", ServerPort: 443},
			UUID:          carrierTestUUID,
			Password:      carrierTestPassword,
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{Enabled: true, ServerName: "localhost", Insecure: true},
			},
		})
	require.NoError(t, err)
	outbound := created.(*Outbound)
	require.Equal(t, 2, outbound.client.families)
	require.NoError(t, outbound.client.CloseWithError(net.ErrClosed))
}
