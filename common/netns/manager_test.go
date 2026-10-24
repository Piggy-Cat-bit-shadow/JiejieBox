//go:build linux

package netns

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/logger"
)

func TestUnshareNamespace(t *testing.T) {
	if os.Getenv("NETNS_TEST_HOLDER") == "1" {
		Hold()
	}
	pipeReader, pipeWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipeReader.Close()
	defer pipeWriter.Close()
	os.Setenv("NETNS_TEST_HOLDER", "1")
	defer os.Unsetenv("NETNS_TEST_HOLDER")
	manager, err := NewManager(logger.NOP(), []option.NetworkNamespace{{
		Type: C.NetNsTypeUnshare,
		Tag:  "test",
		UnshareOptions: option.UnshareNetworkNamespaceOptions{
			PidFile: "/proc/self/fd/" + F.ToString(pipeWriter.Fd()),
		},
	}}, []string{"/proc/self/exe", "-test.run=^TestUnshareNamespace$"})
	if err != nil {
		t.Fatal(err)
	}
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	err = manager.Start(adapter.StartStateInitialize, scope)
	if err != nil {
		t.Fatal(err)
	}

	pipeReader.SetReadDeadline(time.Now().Add(10 * time.Second))
	pidLine, err := bufio.NewReader(pipeReader).ReadString('\n')
	if err != nil {
		t.Fatal("read pid from pipe: ", err)
	}
	pid, err := strconv.Atoi(strings.TrimSuffix(pidLine, "\n"))
	if err != nil {
		t.Fatal("parse pid: ", err)
	}

	resolvedPath := manager.ResolvePath("test")
	if resolvedPath != netnsPath(pid) {
		t.Fatal("resolved path ", resolvedPath, " does not match pid ", pid)
	}
	currentNs, err := os.Readlink("/proc/thread-self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	holderNs, err := os.Readlink(resolvedPath)
	if err != nil {
		t.Fatal("holder netns not accessible: ", err)
	}
	if currentNs == holderNs {
		t.Fatal("holder is in the current netns")
	}

	err = scope.Close()
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		_, err = os.Stat("/proc/" + strconv.Itoa(pid))
		if err != nil {
			return
		}
	}
	t.Fatal("holder process did not exit after close")
}

func TestHolderIgnoresTerminationSignals(t *testing.T) {
	if os.Getenv("NETNS_HOLD_TEST") == "1" {
		Hold()
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestHolderIgnoresTerminationSignals$")
	command.Env = append(os.Environ(), "NETNS_HOLD_TEST=1")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	var waitOnce sync.Once
	wait := func() error {
		var waitErr error
		waitOnce.Do(func() {
			waitErr = command.Wait()
		})
		return waitErr
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = command.Process.Kill()
		_ = wait()
	})

	// Ignore is installed before the read loop; give the child a moment to reach it, then send the
	// signals a terminal or a service manager would.
	time.Sleep(100 * time.Millisecond)
	for _, signal := range []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP} {
		if err = command.Process.Signal(signal); err != nil {
			t.Fatalf("signal %v: %v", signal, err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if err = command.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("the holder exited on a termination signal: %v", err)
	}

	// The parent's pipe close is the exit condition.
	if err = stdin.Close(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- wait() }()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("the holder did not exit after stdin was closed")
	}
}

func TestOwnsUserNamespaceFalseForRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root is the only uid that can be asserted without creating a user namespace")
	}
	if ownsUserNamespace() {
		t.Fatal("root in the initial user namespace must not be reported as owning one")
	}
}
