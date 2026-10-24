package wireguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
	"github.com/sagernet/wireguard-go/conn"

	"github.com/stretchr/testify/require"
)

// A paused bind used to wait on the pause manager alone. Close cancelled the bind context, but the
// goroutine stayed in WaitActive until the network came back, so a suspend/close sequence could park
// a wireguard-go goroutine indefinitely (upstream cc022098fdec). The wait is now on the bind context;
// this is the negative control: with the old wait, the Close below would never release the sender.

type pauseRefusingDialer struct {
	attempted chan struct{}
	once      sync.Once
}

func newPauseRefusingDialer() *pauseRefusingDialer {
	return &pauseRefusingDialer{attempted: make(chan struct{})}
}

func (d *pauseRefusingDialer) markAttempted() {
	d.once.Do(func() { close(d.attempted) })
}

func (d *pauseRefusingDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.markAttempted()
	return nil, errors.New("pause test: dial refused")
}

func (d *pauseRefusingDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	d.markAttempted()
	return nil, errors.New("pause test: listen refused")
}

func TestClientBindCloseWhilePausedReleasesTheSender(t *testing.T) {
	managerContext := pause.WithDefaultManager(t.Context())
	manager := service.FromContext[pause.Manager](managerContext)
	require.NotNil(t, manager)

	dialer := newPauseRefusingDialer()
	bind := NewClientBind(managerContext, log.NewNOPFactory().Logger(), dialer, true, netip.MustParseAddrPort("192.0.2.1:51820"), [3]uint8{})

	manager.NetworkPause()
	if !manager.IsPaused() {
		t.Fatal("the network pause did not take effect")
	}

	sent := make(chan error, 1)
	go func() {
		sent <- bind.Send(
			[][]byte{make([]byte, 64)},
			remoteEndpoint(netip.MustParseAddrPort("192.0.2.2:51820")),
			0,
		)
	}()

	select {
	case <-dialer.attempted:
	case <-time.After(5 * time.Second):
		t.Fatal("the send path never attempted a dial")
	}

	// The dial failed and the send is now waiting for the pause to be lifted. It must stay parked:
	// releasing on its own would make the close assertion below pass for the wrong reason.
	select {
	case err := <-sent:
		t.Fatalf("Send returned %v while the network was paused", err)
	case <-time.After(150 * time.Millisecond):
	}

	closed := make(chan error, 1)
	go func() { closed <- bind.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked behind a sender parked in the pause wait")
	}

	select {
	case err := <-sent:
		require.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("the paused sender was not released by Close")
	}

	// The manager is still paused after the bind closed; that state belongs to the platform, not to
	// the bind, and nothing may have woken it on the bind's behalf.
	require.True(t, manager.IsPaused())
	manager.NetworkWake()
	require.False(t, manager.IsPaused())
}

var (
	_ N.Dialer  = (*pauseRefusingDialer)(nil)
	_ conn.Bind = (*ClientBind)(nil)
)
