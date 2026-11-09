package ssh

// One production SSH outbound across a network change, end to end.
//
// The other tests here cover the locked retirement of the session; none of them runs a real SSH
// session, so what a network change does to an ACTIVE tunnelled flow was open. This is the row:
// the real outbound, a real SSH server that accepts the direct-tcpip channel the outbound opens,
// and the answer - the old session is retired and the next dial opens a new one that carries bytes.
//
// The server is a minimal x/crypto/ssh server: password auth (the outbound's configured method) and
// the one channel type that matters. Its only job is to make the tunnel real; the stream's routing
// is a loopback echo, and the destination the outbound is given IS that echo, so the channel's
// direct-tcpip payload points at it exactly as it would in production.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"golang.org/x/crypto/ssh"

	"github.com/stretchr/testify/require"
)

const (
	carrierUser     = "carrier"
	carrierPassword = "carrier-password"
	carrierSettle   = 15 * time.Second
)

// carrierSSHServer accepts password auth and serves direct-tcpip channels by dialing the target the
// channel names.
type carrierSSHServer struct {
	listener net.Listener
	config   *ssh.ServerConfig
	streams  atomic.Int32
	closed   atomic.Int32
}

func startCarrierSSHServer(t *testing.T) *carrierSSHServer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(privateKey)
	require.NoError(t, err)
	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(metadata ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if metadata.User() == carrierUser && string(password) == carrierPassword {
				return nil, nil
			}
			return nil, fmt.Errorf("ssh: denied")
		},
	}
	serverConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &carrierSSHServer{listener: listener, config: serverConfig}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	return server
}

func (s *carrierSSHServer) serve(conn net.Conn) {
	sshConn, channels, requests, err := ssh.NewServerConn(conn, s.config)
	if err != nil {
		_ = conn.Close()
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
		if newChannel.ChannelType() != "direct-tcpip" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only direct-tcpip is served")
			continue
		}
		var payload struct {
			Host       string
			Port       uint32
			OriginHost string
			OriginPort uint32
		}
		if unmarshalErr := ssh.Unmarshal(newChannel.ExtraData(), &payload); unmarshalErr != nil {
			_ = newChannel.Reject(ssh.ConnectionFailed, "bad direct-tcpip payload")
			continue
		}
		channel, channelRequests, acceptErr := newChannel.Accept()
		if acceptErr != nil {
			continue
		}
		go ssh.DiscardRequests(channelRequests)
		s.streams.Add(1)
		go func() {
			defer s.closed.Add(1)
			defer channel.Close()
			peer, dialErr := net.Dial("tcp", net.JoinHostPort(payload.Host, strconv.Itoa(int(payload.Port))))
			if dialErr != nil {
				return
			}
			defer peer.Close()
			// The route path closes the whole flow when EITHER direction fails; this server must do the same,
			// or a client that dies leaves the handler blocked on a peer that is still waiting for bytes.
			uploadDone := make(chan struct{})
			go func() {
				defer close(uploadDone)
				_, _ = io.Copy(peer, channel)
				if tcpPeer, isTCP := peer.(*net.TCPConn); isTCP {
					_ = tcpPeer.CloseWrite()
				}
			}()
			_, _ = io.Copy(channel, peer)
			<-uploadDone
		}()
	}
}

func (s *carrierSSHServer) port() uint16 {
	return uint16(s.listener.Addr().(*net.TCPAddr).Port)
}

func startCarrierEcho(t *testing.T) net.Addr {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				buffer := make([]byte, 4096)
				for {
					read, readErr := conn.Read(buffer)
					if read > 0 {
						_, _ = conn.Write(buffer[:read])
					}
					if readErr != nil {
						return
					}
				}
			}()
		}
	}()
	return listener.Addr()
}

func newCarrierOutbound(t *testing.T, port uint16) *Outbound {
	t.Helper()
	created, err := NewOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("carrier-ssh"), "ssh-carrier",
		option.SSHOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
			User:          carrierUser,
			Password:      carrierPassword,
		})
	require.NoError(t, err)
	outbound, isOutbound := created.(*Outbound)
	require.True(t, isOutbound)
	t.Cleanup(func() {
		outbound.clientAccess.Lock()
		if outbound.client != nil {
			_ = outbound.client.Close()
		}
		outbound.clientAccess.Unlock()
	})
	return outbound
}

func sshRoundTrip(t *testing.T, outbound *Outbound, destination net.Addr, payload string) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), carrierSettle)
	defer cancel()
	conn, err := outbound.DialContext(ctx, N.NetworkTCP, M.SocksaddrFromNet(destination))
	require.NoError(t, err)
	require.NotNil(t, conn)
	_ = conn.SetDeadline(time.Now().Add(carrierSettle))
	_, err = conn.Write([]byte(payload))
	require.NoError(t, err)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err)
	require.Equal(t, payload, string(echo))
	return conn
}

func TestARealSSHFlowSurvivesANetworkChangeByReconnecting(t *testing.T) {
	echo := startCarrierEcho(t)
	server := startCarrierSSHServer(t)
	outbound := newCarrierOutbound(t, server.port())

	before := sshRoundTrip(t, outbound, echo, "before-the-change")
	require.EqualValues(t, 1, server.streams.Load(), "the first flow reached the SSH server")

	// The production entry: what NetworkManager calls on every physical network change.
	outbound.InterfaceUpdated(context.Background())

	_ = before.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := before.Write([]byte("stale")); err == nil {
		t.Fatal("the pre-change SSH flow still accepted a write: the old session was not retired")
	}

	// The retirement is also visible at the server: the channel that carried the old path finishes,
	// within a bound.
	retiredAt := time.Now()
	require.Eventually(t, func() bool { return server.closed.Load() >= 1 },
		5*time.Second, 10*time.Millisecond,
		"the SSH server never saw the retired channel end: the old session's release has no bound")
	t.Logf("MEASURED old SSH channel released %s after the network change", time.Since(retiredAt).Round(time.Millisecond))

	after := sshRoundTrip(t, outbound, echo, "after-the-change")
	_ = after.Close()
	require.EqualValues(t, 2, server.streams.Load(), "the flow after the change opened a new channel")
}
