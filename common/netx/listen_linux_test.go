//go:build linux

package netx

import (
	"net"
	"testing"

	"golang.org/x/sys/unix"
)

// TestListenTCPDoesNotUseMPTCP pins the security default on the endpoints that
// do not go through common/listener. Go 1.24+ would otherwise create an MPTCP
// listener on Linux; getsockopt(SOL_MPTCP, MPTCP_INFO) succeeds only on an
// MPTCP socket.
func TestListenTCPDoesNotUseMPTCP(t *testing.T) {
	listener, err := ListenTCP("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	tcpListener, ok := listener.(*net.TCPListener)
	if !ok {
		t.Fatalf("listener is %T, want *net.TCPListener", listener)
	}
	raw, err := tcpListener.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn: %v", err)
	}
	var usesMPTCP bool
	if err = raw.Control(func(fd uintptr) {
		_, err := unix.GetsockoptInt(int(fd), 0x11c /* SOL_MPTCP */, 0x1 /* MPTCP_INFO */)
		usesMPTCP = err == nil
	}); err != nil {
		t.Fatalf("raw control: %v", err)
	}
	if usesMPTCP {
		t.Fatal("netx.ListenTCP created an MPTCP listener; the default must be plain TCP")
	}
}
