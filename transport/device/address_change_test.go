package device

import (
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

// recordingConn is a net.Conn that records whether it was closed.
type recordingConn struct {
	closed atomic.Bool
}

func (c *recordingConn) Read([]byte) (int, error)         { return 0, net.ErrClosed }
func (c *recordingConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *recordingConn) Close() error                     { c.closed.Store(true); return nil }
func (c *recordingConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *recordingConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *recordingConn) SetDeadline(time.Time) error      { return nil }
func (c *recordingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *recordingConn) SetWriteDeadline(time.Time) error { return nil }

// TestDeviceAddressChangeInterruptsUDPSockets is the regression for upstream 9f1cd1ef75b
// "Close UDP sockets when device addresses change".
//
// A device (VPN endpoint) can be re-addressed while UDP sockets bound to its old address are
// still open. Those sockets keep sending from an address the peer no longer routes back to and
// fail silently - there is no handshake to notice it. The fix is to interrupt the sockets that
// belong to the family whose address changed, and to do it BEFORE the new address is published.
func TestDeviceAddressChangeInterruptsUDPSockets(t *testing.T) {
	device := &stackDevice{}
	device.inet4Address = netip.MustParseAddr("10.0.0.1")
	device.inet6Address = netip.MustParseAddr("2001:db8::1")

	inet4Conn := &recordingConn{}
	inet6Conn := &recordingConn{}
	_ = device.inet4UDP.NewConn(inet4Conn, false)
	_ = device.inet6UDP.NewConn(inet6Conn, false)

	// A change of the IPv4 address only: the IPv6 sockets must survive.
	err := device.UpdateConfiguration(Configuration{
		Address: []netip.Prefix{netip.MustParsePrefix("10.0.0.2/24"), netip.MustParsePrefix("2001:db8::1/64")},
	})
	require.NoError(t, err)
	require.True(t, inet4Conn.closed.Load(), "the IPv4 socket must be interrupted")
	require.False(t, inet6Conn.closed.Load(), "an unchanged family must keep its sockets")

	// The bind address must be the new one, and it must hand back the group that owns it.
	device.stateAccess.RLock()
	bind, group, err := device.bindAddressLocked(M.Socksaddr{Addr: netip.MustParseAddr("192.0.2.1")})
	device.stateAccess.RUnlock()
	require.NoError(t, err)
	require.Equal(t, netip.MustParseAddr("10.0.0.2"), bind)
	require.NotNil(t, group)

	// A configuration that does not move the address must not interrupt anything.
	idleConn := &recordingConn{}
	_ = device.inet4UDP.NewConn(idleConn, false)
	err = device.UpdateConfiguration(Configuration{
		Address: []netip.Prefix{netip.MustParsePrefix("10.0.0.2/24"), netip.MustParsePrefix("2001:db8::1/64")},
	})
	require.NoError(t, err)
	require.False(t, idleConn.closed.Load(), "an unchanged address must not interrupt a healthy socket")
}
