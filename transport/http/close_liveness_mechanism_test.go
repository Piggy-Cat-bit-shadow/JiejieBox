//go:build with_quic

package http

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go"

	"github.com/stretchr/testify/require"
)

// The close-liveness mechanism, pinned.
//
// # The question this answers
//
// A TUN stage holds its lock across the port write, and the port's write can block in the QUIC
// DATAGRAM queue. The earlier round measured that a SILENT path ends the block at the idle timeout
// (5.80 s at a 6 s setting). That left one counterexample open: the idle timer is refreshed by
// received packets, so a peer that keeps sending valid QUIC traffic could in principle keep the
// connection alive while our queue stays full -- and then nothing would end the block.
//
// # What actually happens, measured here
//
// A QUIC peer that receives no acknowledgements cannot keep sending: its congestion window never
// opens. In this test the server uses quic-go's DEFAULT idle timeout (30 s: a zero value selects the
// default, it does not disable the timeout) with a 200 ms keepalive, and the client uses 3 s, so the
// effective minimum is the client side. The relay
// drops only our direction. The peer still stops after ~19 packets, and the client's connection then
// dies exactly one idle timeout after the LAST inbound packet -- which is what bounds the wait.
//
// The two subtests below are the contrast the analysis needs: the silent path, and the one-way
// broken path where inbound traffic exists but cannot be sustained.

// livenessRelay forwards client->server and server->client, and can break either direction.
type livenessRelay struct {
	front      *net.UDPConn
	upstream   *net.UDPConn
	dropOut    atomic.Bool
	dropIn     atomic.Bool
	lastClient atomic.Pointer[net.UDPAddr]
	inbound    atomic.Int64
}

func newLivenessRelay(serverAddr *net.UDPAddr) (*livenessRelay, error) {
	front, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	upstream, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		front.Close()
		return nil, err
	}
	relay := &livenessRelay{front: front, upstream: upstream}
	go func() {
		buffer := make([]byte, 2048)
		for {
			n, address, readErr := front.ReadFromUDP(buffer)
			if readErr != nil {
				return
			}
			relay.lastClient.Store(address)
			if relay.dropOut.Load() {
				continue
			}
			_, _ = upstream.Write(buffer[:n])
		}
	}()
	go func() {
		buffer := make([]byte, 2048)
		for {
			n, readErr := upstream.Read(buffer)
			if readErr != nil {
				return
			}
			relay.inbound.Add(1)
			if relay.dropIn.Load() {
				continue
			}
			if client := relay.lastClient.Load(); client != nil {
				_, _ = front.WriteToUDP(buffer[:n], client)
			}
		}
	}()
	return relay, nil
}

func (r *livenessRelay) address() string { return r.front.LocalAddr().String() }

func (r *livenessRelay) Close() {
	r.front.Close()
	r.upstream.Close()
}

func livenessTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	return &tls.Config{
		Certificates:       []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		NextProtos:         []string{"liveness"},
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
	}
}

type livenessStand struct {
	client *quic.Conn
	relay  *livenessRelay
	stop   func()
}

// startLivenessStand brings up a real connection through the relay.
//
// serverIdle of 0 selects quic-go's DEFAULT idle timeout (30 s); it does not disable it. The client
// value is what the experiment varies, and the effective timeout is the minimum of the two.
func startLivenessStand(t *testing.T, clientIdle, serverIdle time.Duration, serverKeepAlive time.Duration) *livenessStand {
	t.Helper()
	tlsConfig := livenessTLSConfig(t)
	serverConfig := &quic.Config{
		EnableDatagrams:         true,
		MaxDatagramFrameSize:    1200,
		InitialPacketSize:       1200,
		HandshakeIdleTimeout:    5 * time.Second,
		MaxIdleTimeout:          serverIdle,
		KeepAlivePeriod:         serverKeepAlive,
		DisablePathMTUDiscovery: true,
	}
	listener, err := quic.ListenAddr("127.0.0.1:0", tlsConfig, serverConfig)
	require.NoError(t, err)
	relay, err := newLivenessRelay(listener.Addr().(*net.UDPAddr))
	require.NoError(t, err)
	go func() {
		conn, acceptErr := listener.Accept(context.Background())
		if acceptErr != nil {
			return
		}
		<-conn.Context().Done()
	}()
	clientConfig := &quic.Config{
		EnableDatagrams:         true,
		MaxDatagramFrameSize:    1200,
		InitialPacketSize:       1200,
		HandshakeIdleTimeout:    5 * time.Second,
		MaxIdleTimeout:          clientIdle,
		DisablePathMTUDiscovery: true,
	}
	client, err := quic.DialAddr(context.Background(), relay.address(), tlsConfig, clientConfig)
	require.NoError(t, err)
	stand := &livenessStand{client: client, relay: relay, stop: func() {
		client.CloseWithError(0, "")
		listener.Close()
		relay.Close()
	}}
	t.Cleanup(stand.stop)
	time.Sleep(300 * time.Millisecond)
	return stand
}

// fillDatagramQueue sends until the queue stops accepting immediately. It returns the payload used.
func fillDatagramQueue(t *testing.T, client *quic.Conn) []byte {
	t.Helper()
	payload := make([]byte, 1000)
	for i := 0; i < 64; i++ {
		require.NoError(t, client.SendDatagram(payload))
	}
	return payload
}

// TestTheIdleTimeoutBoundsASilentPath is the control: nothing comes back, so the block ends at the
// idle timeout.
func TestTheIdleTimeoutBoundsASilentPath(t *testing.T) {
	const idleTimeout = 3 * time.Second
	stand := startLivenessStand(t, idleTimeout, idleTimeout, 0)
	fillDatagramQueue(t, stand.client)

	stand.relay.dropOut.Store(true)
	stand.relay.dropIn.Store(true)

	start := time.Now()
	var sendErr error
	for time.Since(start) < 20*time.Second {
		if sendErr = stand.client.SendDatagram(make([]byte, 1000)); sendErr != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.Error(t, sendErr, "the silent path must end the block")
	t.Logf("MEASURED silent path: send failed after %v with %v", time.Since(start).Round(time.Millisecond), sendErr)
	// The invariant is "bounded by the idle timeout", not a tight wall-clock budget. A loaded CI
	// box can delay the timer by seconds, and asserting a narrow multiple turned that into a flake:
	// this test once took 12.3 s under a concurrent build while running at about 8 s alone. Five
	// idle timeouts is still three times shorter than an unbounded wait would be, which is what the
	// assertion exists to catch.
	require.Less(t, time.Since(start), 5*idleTimeout, "the silent path must be bounded by the idle timeout")
}

// TestActiveInboundStopsBecauseThePeerIsStarved is the counterexample attempt: the peer has its idle
// default idle timeout (30 s) with a keepalive, the client at 3 s, and only our direction is broken,
// so valid inbound packets do
// arrive and DO refresh the idle timer. They stop anyway, and the connection dies one idle timeout
// after the last one.
func TestActiveInboundStopsBecauseThePeerIsStarved(t *testing.T) {
	const idleTimeout = 3 * time.Second
	stand := startLivenessStand(t, idleTimeout, 0, 200*time.Millisecond)
	fillDatagramQueue(t, stand.client)

	stand.relay.dropOut.Store(true)
	start := time.Now()
	lastInboundAt := time.Duration(0)
	lastInboundCount := int64(0)
	var firstErrAt time.Duration
	var firstErr error
	for time.Since(start) < 20*time.Second {
		if firstErr = stand.client.SendDatagram(make([]byte, 1000)); firstErr != nil {
			firstErrAt = time.Since(start)
			break
		}
		if count := stand.relay.inbound.Load(); count != lastInboundCount {
			lastInboundCount = count
			lastInboundAt = time.Since(start)
		}
		time.Sleep(20 * time.Millisecond)
	}

	require.Error(t, firstErr, "this configuration must still close; an unbounded wait would hang here")
	t.Logf("MEASURED active path: inbound stopped at %v after %d packets, connection died at %v (%v)",
		lastInboundAt.Round(time.Millisecond), lastInboundCount, firstErrAt.Round(time.Millisecond), firstErr)

	// The idle timer was refreshed: the connection lived past the last inbound packet by about one
	// idle timeout, not from the start of the experiment.
	gap := firstErrAt - lastInboundAt
	require.Greater(t, lastInboundAt, idleTimeout, "inbound traffic must have refreshed the idle timer")
	require.Greater(t, gap, idleTimeout/2, "the close must follow the last inbound packet, not the start")
	// Same reasoning as the silent path: the bound is what matters, and scheduler delay under load
	// must not read as a protocol failure. Six idle timeouts still distinguishes "bounded" from
	// "hung", which is the property under test.
	require.Less(t, gap, 6*idleTimeout, "the close must still happen within a bounded time")

	// The peer stopped because it was starved of acknowledgements. If it could send forever, the
	// gap would not exist at all.
	require.Less(t, lastInboundCount, int64(128),
		"a peer that receives no ACKs must not be able to keep sending indefinitely")
}
