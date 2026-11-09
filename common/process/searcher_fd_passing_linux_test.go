//go:build linux

package process

// A real file descriptor handed to a DIFFERENT program is the case the "every owner path" contract
// exists for: two processes of the same uid hold one socket, and a lookup must report both. The
// synthetic version of this test fakes the /proc map; this one uses the kernel, so it also proves
// the walk itself (the /proc/fd scan and the exe readlink) rather than the map arithmetic.

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func TestARealPassedFileDescriptorReportsEveryOwner(t *testing.T) {
	t.Parallel()

	// The receiver has to be a different program for its exe path to be distinguishable from the
	// test binary's; sleep is present on every Linux image this suite runs on.
	const receiver = "/usr/bin/sleep"
	if _, err := os.Stat(receiver); err != nil {
		t.Skipf("%s is not installed: %v", receiver, err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	conn, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	tcpConn, isTCP := conn.(*net.TCPConn)
	require.True(t, isTCP)

	// A dup of the socket, handed to the child at exec. The parent keeps its own descriptor, so the
	// socket has two owners from this point on.
	passed, err := tcpConn.File()
	require.NoError(t, err)
	defer passed.Close()

	child := exec.Command(receiver, "30")
	child.ExtraFiles = []*os.File{passed}
	require.NoError(t, child.Start())
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	})

	// The child's exe is read through /proc so the assertion names the same path the walk sees
	// (a symlink may resolve somewhere else on a merged-/usr image).
	var childExe string
	deadline := time.Now().Add(5 * time.Second)
	for {
		childExe, err = os.Readlink(fmt.Sprintf("/proc/%d/exe", child.Process.Pid))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the child's exe could not be resolved: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	searcher := newScanTestSearcher(t, buildProcessPaths)
	info, err := FindProcessInfo(
		searcher,
		context.Background(),
		N.NetworkTCP,
		M.AddrPortFromNet(conn.LocalAddr()),
		M.AddrPortFromNet(listener.Addr()),
	)
	require.NoError(t, err, "the real /proc walk must resolve this socket")
	require.EqualValues(t, os.Getuid(), info.UserId)

	// The child's kernel-side exe may differ from the string we executed (a busybox or a wrapper),
	// so the required path is the one read back from /proc/<child>/exe.
	require.Contains(t, info.ProcessPaths, childExe,
		"the passed descriptor belongs to the child, and the lookup must report its path")
	require.GreaterOrEqual(t, len(info.ProcessPaths), 2,
		"the parent also holds the socket, so the owner set has more than one path: %v", info.ProcessPaths)
}
