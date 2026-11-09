//go:build with_awg

package wireguard

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service/pause"

	"github.com/stretchr/testify/require"
)

// TT07: the AWG endpoint owns a device, its handshake workers and the dials waiting on them.
// Close landing while dialers are blocked in a handshake must release every one of them, and a
// second Close must stay a no-op. The options carry the full AWG 3.1 profile on purpose: the
// obfuscation path adds header protection and padding work to the same lifecycle, so a close
// race there is the one that matters.
func TestAWGEndpointCloseReleasesBlockedDialers(t *testing.T) {
	ctx := pause.WithDefaultManager(context.Background())

	endpoint, err := NewEndpoint(EndpointOptions{
		Context:    ctx,
		Logger:     log.NewNOPFactory().Logger(),
		Dialer:     &plainDialer{},
		MTU:        1420,
		Address:    []netip.Prefix{netip.MustParsePrefix("10.10.0.1/24")},
		PrivateKey: testPrivateKey,
		AmneziaWG: option.AmneziaWGOptions{
			Jc:                     4,
			Jmin:                   40,
			Jmax:                   70,
			S1:                     55,
			S2:                     42,
			S3:                     40,
			S4:                     12,
			H1:                     "1",
			H2:                     "2",
			H3:                     "3",
			H4:                     "4",
			HeaderProtectionKey:    testPrivateKey,
			ContentPaddingAddition: "10-100",
			RandomTrailers:         true,
			DisableCookies:         true,
		},
		Peers: []PeerOptions{{
			Endpoint:   M.ParseSocksaddrHostPort("127.0.0.1", 51820),
			PublicKey:  testListenPeerPublicKey,
			AllowedIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, endpoint.Initialize(nil))
	require.NoError(t, endpoint.Start(false))

	const dialers = 8
	destination := M.ParseSocksaddrHostPort("10.10.0.2", 80)
	var returned atomic.Int32
	var waitGroup sync.WaitGroup
	waitGroup.Add(dialers)
	for index := 0; index < dialers; index++ {
		go func() {
			defer waitGroup.Done()
			dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			conn, dialErr := endpoint.DialContext(dialCtx, "tcp", destination)
			if dialErr == nil {
				_ = conn.Close()
			}
			returned.Add(1)
		}()
	}
	// Let the dials get past the peer lookup and into the handshake wait.
	time.Sleep(50 * time.Millisecond)

	require.NoError(t, endpoint.Close())

	finished := make(chan struct{})
	go func() {
		waitGroup.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(15 * time.Second):
		t.Fatalf("only %d of %d dialers returned after endpoint.Close()", returned.Load(), dialers)
	}
	require.EqualValues(t, dialers, returned.Load())
	require.NoError(t, endpoint.Close(), "Close must stay idempotent")
}

// The same endpoint must refuse new dials after Close rather than panic or block on a dead device.
func TestAWGEndpointDialAfterCloseFailsClosed(t *testing.T) {
	ctx := pause.WithDefaultManager(context.Background())

	endpoint, err := NewEndpoint(EndpointOptions{
		Context:    ctx,
		Logger:     log.NewNOPFactory().Logger(),
		Dialer:     &plainDialer{},
		MTU:        1420,
		Address:    []netip.Prefix{netip.MustParsePrefix("10.10.0.1/24")},
		PrivateKey: testPrivateKey,
		AmneziaWG: option.AmneziaWGOptions{
			Jc: 4, Jmin: 40, Jmax: 70, S1: 55, S2: 42, S3: 40, S4: 12,
			H1: "1", H2: "2", H3: "3", H4: "4",
			HeaderProtectionKey: testPrivateKey,
			DisableCookies:      true,
		},
		Peers: []PeerOptions{{
			Endpoint:   M.ParseSocksaddrHostPort("127.0.0.1", 51820),
			PublicKey:  testListenPeerPublicKey,
			AllowedIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, endpoint.Initialize(nil))
	require.NoError(t, endpoint.Start(false))
	require.NoError(t, endpoint.Close())

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := endpoint.DialContext(dialCtx, "tcp", M.ParseSocksaddrHostPort("10.10.0.2", 80))
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err, "a closed endpoint must not hand back a connection")
}
