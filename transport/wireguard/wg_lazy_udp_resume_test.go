package wireguard

// UDP has no retransmission: one datagram IS the request. The release protocol keeps the stack and
// its flows but destroys the Device, so a wake that ends in "the trigger was dropped" is data loss,
// not a delay.
//
// The independent review measured exactly that with the previous shape: a single post-wake UDP
// request timed out deterministically while the Device DID rebuild, and only the second datagram (a
// new application retry) arrived. TCP hid the same hole because its transport retransmits.
//
// The fix is an ownership boundary rather than a packet cache: while a UDP association is open, the
// heavy chain is NOT released (suspend only takes the Device down), because that association is the
// thing that still needs it. With no UDP association open - the case the release exists for - the
// chain is still reclaimed, which is what TestAWakeWithoutTrafficDoesNotRebuildTheChain and the
// TCP fixtures cover. These tests pin the UDP half.

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// startUDPEcho runs an echo socket inside the pair's server stack, on the pair's destination.
func startUDPEcho(t *testing.T, pair *lazyPair) {
	t.Helper()
	echo, err := pair.server.tunDevice.(*stackDevice).stack.ListenUDP(pair.destination.AddrPort())
	require.NoError(t, err)
	t.Cleanup(func() { _ = echo.Close() })
	go func() {
		packet := make([]byte, 2048)
		for {
			read, addr, readErr := echo.ReadFromUDPAddrPort(packet)
			if readErr != nil {
				return
			}
			if _, writeErr := echo.WriteToUDPAddrPort(packet[:read], addr); writeErr != nil {
				return
			}
		}
	}()
}

// exchangeUDP sends ONE datagram and waits for its echo. No loop, no retry: that is the point.
func exchangeUDP(t *testing.T, conn interface {
	SetDeadline(time.Time) error
	Write([]byte) (int, error)
	Read([]byte) (int, error)
}, payload string, budget time.Duration,
) error {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(budget)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte(payload)); err != nil {
		return err
	}
	reply := make([]byte, 2048)
	read, err := conn.Read(reply)
	if err != nil {
		return err
	}
	require.Equal(t, payload, string(reply[:read]))
	return nil
}

// TestAnOpenUDPAssociationKeepsTheHeavyChainAlive is the P2 regression the review reproduced.
func TestAnOpenUDPAssociationKeepsTheHeavyChainAlive(t *testing.T) {
	for _, testCase := range resumeCases() {
		t.Run(testCase.name, func(t *testing.T) {
			pair := lazyStartPair(t, testCase.awg)
			startUDPEcho(t, pair)

			conn, err := pair.client.DialContext(context.Background(), "udp", pair.destination)
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, exchangeUDP(t, conn, "before-idle", 10*time.Second))
			require.EqualValues(t, 1, pair.client.udpAssociations.Load(),
				"the open association must be counted; the release boundary is what it buys")

			pair.client.SetIdle(true)
			published, released := pair.client.lazyState()
			require.True(t, published,
				"an open UDP association must keep the Device: releasing it would drop the next datagram")
			require.False(t, released)

			pair.client.SetIdle(false)
			// ONE datagram, sent once, after the wake. No application retry is allowed to be assumed.
			require.NoError(t, exchangeUDP(t, conn, "single-datagram-after-wake", 12*time.Second),
				"a single post-wake UDP datagram must be delivered")
		})
	}
}

// TestTheReleaseStillHappensOnceTheUDPAssociationIsClosed keeps the saving: the boundary is the OPEN
// association, not UDP support in general.
func TestTheReleaseStillHappensOnceTheUDPAssociationIsClosed(t *testing.T) {
	for _, testCase := range resumeCases() {
		t.Run(testCase.name, func(t *testing.T) {
			pair := lazyStartPair(t, testCase.awg)
			startUDPEcho(t, pair)

			conn, err := pair.client.DialContext(context.Background(), "udp", pair.destination)
			require.NoError(t, err)
			require.NoError(t, exchangeUDP(t, conn, "before-idle", 10*time.Second))
			require.NoError(t, conn.Close())
			require.EqualValues(t, 0, pair.client.udpAssociations.Load(),
				"closing the association must release the boundary")

			pair.client.SetIdle(true)
			_, released := pair.client.lazyState()
			require.True(t, released,
				"with no UDP association open the chain must still be reclaimed")

			// A NEW flow after the wake goes through the ordinary rebuild path.
			pair.client.SetIdle(false)
			fresh, dialErr := pair.client.DialContext(context.Background(), "udp", pair.destination)
			require.NoError(t, dialErr)
			defer fresh.Close()
			require.NoError(t, exchangeUDP(t, fresh, "new-association-after-wake", 20*time.Second))
		})
	}
}

// TestAPacketConnAlsoHoldsTheBoundary covers the listen-side UDP path: a caller that owns a
// PacketConn (rather than a connected association) needs the same protection.
func TestAPacketConnAlsoHoldsTheBoundary(t *testing.T) {
	pair := lazyStartPair(t, option.AmneziaWGOptions{})
	startUDPEcho(t, pair)

	packetConn, err := pair.client.ListenPacket(context.Background(), pair.destination)
	require.NoError(t, err)
	defer packetConn.Close()
	require.EqualValues(t, 1, pair.client.udpAssociations.Load())

	pair.client.SetIdle(true)
	published, released := pair.client.lazyState()
	require.True(t, published, "an open PacketConn must keep the Device")
	require.False(t, released)
}
