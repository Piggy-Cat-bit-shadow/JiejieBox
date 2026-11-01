//go:build linux

package listener

import (
	"context"
	"net"
	"testing"

	"github.com/sagernet/sing-box/option"
	"golang.org/x/sys/unix"
)

// TestListenTCPMultiPathIsOptIn pins the security default: an unset
// tcp_multi_path must not create an MPTCP listener, because Go 1.24+ enables
// MPTCP for Linux listeners by default and the kernel MPTCP listener is the
// remote-root attack surface Xray disabled in v26.10.31. Explicit
// tcp_multi_path must still opt in.
func TestListenTCPMultiPathIsOptIn(t *testing.T) {
	probe, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0x106 /* IPPROTO_MPTCP */)
	if err != nil {
		t.Skipf("kernel does not support MPTCP: %v", err)
	}
	unix.Close(probe)

	defaultListener, err := ListenTCPWithOptions(context.Background(), option.ListenOptions{}, 0, false)
	if err != nil {
		t.Fatalf("listen with tcp_multi_path unset: %v", err)
	}
	defer defaultListener.Close()
	if listenerUsesMPTCP(t, defaultListener) {
		t.Fatal("a listener with tcp_multi_path unset uses MPTCP; the default must be plain TCP")
	}

	enabledListener, err := ListenTCPWithOptions(context.Background(), option.ListenOptions{TCPMultiPath: true}, 0, false)
	if err != nil {
		t.Skipf("MPTCP listener unavailable: %v", err)
	}
	defer enabledListener.Close()
	if !listenerUsesMPTCP(t, enabledListener) {
		t.Fatal("tcp_multi_path=true no longer enables MPTCP")
	}
}

// listenerUsesMPTCP asks the kernel whether the listener is an MPTCP socket.
// getsockopt(SOL_MPTCP, MPTCP_INFO) fails on a plain TCP socket.
func listenerUsesMPTCP(t *testing.T, listener net.Listener) bool {
	t.Helper()
	tcpListener, ok := listener.(*net.TCPListener)
	if !ok {
		t.Fatalf("listener is %T, want *net.TCPListener", listener)
	}
	raw, err := tcpListener.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn: %v", err)
	}
	var uses bool
	if err = raw.Control(func(fd uintptr) {
		_, err := unix.GetsockoptInt(int(fd), 0x11c /* SOL_MPTCP */, 0x1 /* MPTCP_INFO */)
		uses = err == nil
	}); err != nil {
		t.Fatalf("raw control: %v", err)
	}
	return uses
}
