package resourceguard

// The relay's copy-growth budget on a REAL socket: the same completed bytes through sing's
// copy engine with the budget granting growth and with the budget refusing it. The scripted
// relay test (growth_relay_test.go) pins the permit lifecycle; this one answers the other
// question the fault matrix asks: with a real TCP peer across a shaped link, what does the
// permission actually buy?
//
// Modes (RELAY_AB):
//
//	1      in-process: a loopback echo server, both arms.
//	server listen on RELAY_AB_ADDR, echo every connection, publish the address and the
//	       running totals, serve until killed (bounded by RELAY_AB_DURATION).
//	client read the address, run both arms against it, assert equal completed bytes and
//	       the permit accounting, write the totals to RELAY_AB_RESULT_FILE.
//
// The server/client split lets the run cross a veth with netem (see run_relay_shaped_ab.sh).

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing/common/bufio"
)

const (
	relayABStreams     = 4
	relayABStreamBytes = 2 << 20
)

func TestRelayGrowthShapedAB(t *testing.T) {
	switch os.Getenv("RELAY_AB") {
	case "":
		t.Skip("set RELAY_AB=1 (in-process), =server or =client")
	case "server":
		runRelayABServer(t)
		return
	case "client":
		runRelayABClient(t)
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go relayABEcho(listener)
	runRelayABA(t, listener.Addr().String())
}

func relayABEcho(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}()
	}
}

func runRelayABServer(t *testing.T) {
	addr := os.Getenv("RELAY_AB_ADDR")
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go relayABEcho(listener)
	if addrFile := os.Getenv("RELAY_AB_ADDR_FILE"); addrFile != "" {
		_ = os.WriteFile(addrFile, []byte(listener.Addr().String()), 0o644)
	}
	t.Logf("serving on %s", listener.Addr())
	duration := 180 * time.Second
	if raw := os.Getenv("RELAY_AB_DURATION"); raw != "" {
		if parsed, parseErr := time.ParseDuration(raw); parseErr == nil {
			duration = parsed
		}
	}
	select {
	case <-time.After(duration):
	case <-t.Context().Done():
	}
}

func runRelayABClient(t *testing.T) {
	addrFile := os.Getenv("RELAY_AB_ADDR_FILE")
	if addrFile == "" {
		t.Fatal("RELAY_AB_ADDR_FILE is required in client mode")
	}
	addr := waitRelayABAddr(t, addrFile)
	t.Logf("client of %s", addr)
	summary := runRelayABA(t, addr)
	if resultFile := os.Getenv("RELAY_AB_RESULT_FILE"); resultFile != "" {
		_ = os.WriteFile(resultFile, []byte(summary), 0o644)
	}
	t.Logf("result: %s", summary)
}

type relayABArm struct {
	name      string
	flows     int
	threshold int64 // observed on the first permit; arms differ
	wall      time.Duration
	streams   int
	bytes     int64
	granted   uint64
	refused   uint64
}

func runRelayABA(t *testing.T, addr string) string {
	t.Helper()
	arms := []relayABArm{
		{name: "granted", flows: 64},
		{name: "refused", flows: 0},
	}
	for index := range arms {
		arm := &arms[index]
		budget := NewGrowthBudget(GrowthConfig{Flows: arm.flows}, func() Level { return LevelNormal })
		started := time.Now()
		var waitGroup sync.WaitGroup
		var failures atomic.Int64
		var written atomic.Int64
		for stream := 0; stream < relayABStreams; stream++ {
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				conn, err := net.Dial("tcp", addr)
				if err != nil {
					failures.Add(1)
					return
				}
				defer conn.Close()
				threshold, permit := budget.Admit(trafficclass.ClassBulk, false)
				if permit != nil {
					defer permit.Release()
				}
				if arm.threshold == 0 {
					arm.threshold = threshold
				} else if arm.threshold != threshold {
					t.Logf("%s: threshold varies %d vs %d", arm.name, arm.threshold, threshold)
				}
				go func() {
					payload := make([]byte, relayABStreamBytes)
					if _, writeErr := conn.Write(payload); writeErr != nil {
						failures.Add(1)
					}
					if tcp, ok := conn.(*net.TCPConn); ok {
						_ = tcp.CloseWrite()
					}
				}()
				copied, copyErr := bufio.CopyWithIncreateBuffer(io.Discard, conn, threshold, bufio.DefaultBatchSize)
				if copyErr != nil {
					failures.Add(1)
					return
				}
				written.Add(copied)
			}()
		}
		waitGroup.Wait()
		arm.wall = time.Since(started)
		arm.streams = relayABStreams
		arm.bytes = written.Load()
		stats := budget.Stats()
		arm.granted = stats.Granted
		arm.refused = stats.Refused
		if arm.bytes != int64(relayABStreams*relayABStreamBytes) {
			t.Fatalf("%s arm copied %d bytes, want %d", arm.name, arm.bytes, int64(relayABStreams*relayABStreamBytes))
		}
		if failures.Load() != 0 {
			t.Fatalf("%s arm had %d failures", arm.name, failures.Load())
		}
		if arm.flows == 0 {
			if arm.granted != 0 || arm.refused != uint64(relayABStreams) {
				t.Fatalf("%s arm permits: granted=%d refused=%d, want 0/%d", arm.name, arm.granted, arm.refused, relayABStreams)
			}
		} else if arm.granted != uint64(relayABStreams) || arm.refused != 0 {
			t.Fatalf("%s arm permits: granted=%d refused=%d, want %d/0", arm.name, arm.granted, arm.refused, relayABStreams)
		}
	}
	if arms[0].threshold <= 0 {
		t.Fatalf("the granted arm got threshold %d, want the engine's growth threshold", arms[0].threshold)
	}
	if arms[1].threshold != 0 {
		t.Fatalf("the refused arm got threshold %d, want 0 (never grow)", arms[1].threshold)
	}
	if arms[0].bytes != arms[1].bytes {
		t.Fatalf("arms copied different bytes: %d vs %d", arms[0].bytes, arms[1].bytes)
	}
	return fmt.Sprintf("granted_bytes=%d granted_wall_ms=%d granted_threshold=%d refused_bytes=%d refused_wall_ms=%d refused_threshold=%d\n",
		arms[0].bytes, arms[0].wall.Milliseconds(), arms[0].threshold,
		arms[1].bytes, arms[1].wall.Milliseconds(), arms[1].threshold)
}

func waitRelayABAddr(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) > 0 {
			if _, _, splitErr := net.SplitHostPort(string(raw)); splitErr == nil {
				return string(raw)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server address never appeared in %s", path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
