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

type relayABObservation struct {
	threshold int64
	granted   bool
	copied    int64
	err       error
}

type relayABArm struct {
	name    string
	flows   int
	wall    time.Duration
	streams []relayABObservation
	granted uint64
	refused uint64
}

func runRelayABA(t *testing.T, addr string) string {
	t.Helper()
	arms := []relayABArm{
		{name: "granted", flows: 64},
		{name: "refused", flows: 0},
	}
	if os.Getenv("RELAY_AB_ORDER") == "refused-first" {
		// Alternate the arm order between runs: a fixed order can hide a warm-up bias.
		arms[0], arms[1] = arms[1], arms[0]
	}
	for index := range arms {
		runRelayABArm(t, addr, &arms[index])
	}
	var grantedArm, refusedArm *relayABArm
	for index := range arms {
		if arms[index].flows == 0 {
			refusedArm = &arms[index]
		} else {
			grantedArm = &arms[index]
		}
	}
	if grantedArm == nil || refusedArm == nil {
		t.Fatal("the A/B arms are incomplete")
	}
	if grantedArm.streams[0].threshold <= 0 {
		t.Fatalf("the granted arm got threshold %d, want the engine's growth threshold", grantedArm.streams[0].threshold)
	}
	if refusedArm.streams[0].threshold != 0 {
		t.Fatalf("the refused arm got threshold %d, want 0 (never grow)", refusedArm.streams[0].threshold)
	}
	if len(grantedArm.streams) != relayABStreams || len(refusedArm.streams) != relayABStreams {
		t.Fatalf("arm stream counts: granted=%d refused=%d", len(grantedArm.streams), len(refusedArm.streams))
	}
	if grantedArm.granted != uint64(relayABStreams) || grantedArm.refused != 0 {
		t.Fatalf("granted arm permits: granted=%d refused=%d", grantedArm.granted, grantedArm.refused)
	}
	if refusedArm.granted != 0 || refusedArm.refused != uint64(relayABStreams) {
		t.Fatalf("refused arm permits: granted=%d refused=%d", refusedArm.granted, refusedArm.refused)
	}
	grantedBytes, refusedBytes := int64(0), int64(0)
	for _, observation := range grantedArm.streams {
		grantedBytes += observation.copied
	}
	for _, observation := range refusedArm.streams {
		refusedBytes += observation.copied
	}
	if grantedBytes != refusedBytes || grantedBytes != int64(relayABStreams*relayABStreamBytes) {
		t.Fatalf("copied bytes: granted=%d refused=%d want %d", grantedBytes, refusedBytes, int64(relayABStreams*relayABStreamBytes))
	}
	return fmt.Sprintf("order=%s granted_bytes=%d granted_wall_ms=%d granted_threshold=%d refused_bytes=%d refused_wall_ms=%d refused_threshold=%d\n",
		os.Getenv("RELAY_AB_ORDER"), grantedBytes, grantedArm.wall.Milliseconds(), grantedArm.streams[0].threshold,
		refusedBytes, refusedArm.wall.Milliseconds(), refusedArm.streams[0].threshold)
}

// runRelayABArm runs one arm's streams, keeping EVERY stream's observation separate until
// the join: the per-stream threshold and permit are validated here, so an inconsistent
// threshold fails immediately instead of being written into a shared field by a goroutine
// (the former fixture's data race).
func runRelayABArm(t *testing.T, addr string, arm *relayABArm) {
	t.Helper()
	budget := NewGrowthBudget(GrowthConfig{Flows: arm.flows}, func() Level { return LevelNormal })
	observations := make(chan relayABObservation, relayABStreams)
	started := time.Now()
	var waitGroup sync.WaitGroup
	for stream := 0; stream < relayABStreams; stream++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			observations <- runRelayABStream(addr, budget)
		}()
	}
	waitGroup.Wait()
	close(observations)
	arm.wall = time.Since(started)
	stats := budget.Stats()
	arm.granted = stats.Granted
	arm.refused = stats.Refused
	for observation := range observations {
		if observation.err != nil {
			t.Fatalf("%s arm: %v", arm.name, observation.err)
		}
		if observation.copied != int64(relayABStreamBytes) {
			t.Fatalf("%s arm stream copied %d bytes, want %d", arm.name, observation.copied, int64(relayABStreamBytes))
		}
		if arm.flows == 0 {
			if observation.threshold != 0 || observation.granted {
				t.Fatalf("%s arm stream got threshold=%d granted=%v, want 0/false", arm.name, observation.threshold, observation.granted)
			}
		} else if observation.threshold <= 0 || !observation.granted {
			t.Fatalf("%s arm stream got threshold=%d granted=%v, want a growth threshold", arm.name, observation.threshold, observation.granted)
		}
		if len(arm.streams) > 0 && observation.threshold != arm.streams[0].threshold {
			t.Fatalf("%s arm thresholds differ: %d vs %d", arm.name, arm.streams[0].threshold, observation.threshold)
		}
		arm.streams = append(arm.streams, observation)
	}
	if len(arm.streams) != relayABStreams {
		t.Fatalf("%s arm collected %d stream observations, want %d", arm.name, len(arm.streams), relayABStreams)
	}
}

// runRelayABStream is one direction through the real copy engine: it uploads the payload
// and copies the echo back with the arm's threshold.
func runRelayABStream(addr string, budget *GrowthBudget) relayABObservation {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return relayABObservation{err: err}
	}
	defer conn.Close()
	threshold, permit := budget.Admit(trafficclass.ClassBulk, false)
	if os.Getenv("RELAY_AB_INVERT") != "" {
		// Deliberate inversion: the assertion must catch a growth decision that does not
		// match the budget's permit (used by the Debug pass to prove the checks execute).
		threshold = 0
	}
	observation := relayABObservation{threshold: threshold, granted: permit != nil}
	if permit != nil {
		defer permit.Release()
	}
	go func() {
		payload := make([]byte, relayABStreamBytes)
		_, _ = conn.Write(payload)
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	copied, copyErr := bufio.CopyWithIncreateBuffer(io.Discard, conn, threshold, bufio.DefaultBatchSize)
	observation.copied = copied
	observation.err = copyErr
	return observation
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
