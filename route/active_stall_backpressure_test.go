package route

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
)

// §7: does a HEALTHY but backpressured flow look like a stalled one?
//
// The discriminator in active_stall_discriminator_test.go separated HEALTHY_IDLE from STALLED and
// nothing else. That is not enough to justify a fix, because a receiver that is merely slow produces
// the same blocked writer for a while:
//
//	HEALTHY_BACKPRESSURED   writer blocked, reader parked, same socket - then the receiver resumes
//	                        reading and the writer unblocks on its own
//	DEAD_NO_PROGRESS        the same state, except nothing downstream ever reads again
//
// If those two are indistinguishable at every observation point, the only lever is the length of the
// grace period, and that period has to sit between the longest legitimate backpressure and whatever
// ends the flow unaided. This file measures both ends of that window rather than guessing at it.

// backpressureServer is a peer that talks normally and then stops reading for a while.
//
// It echoes, because a one-way peer would park the DOWNLOAD copy as well and make "recovery"
// unobservable: the application side would still be blocked on its upload write and could never
// demonstrate that the flow came back. Echoing keeps the download direction demonstrably alive, so
// the only thing under test is the upload write's block.
type backpressureServer struct {
	listener net.Listener
	// gate, once closed, lets the reader start consuming. Before that the peer is a slow consumer.
	gate     chan struct{}
	consumed chan int64
}

func newBackpressureServer(t *testing.T) *backpressureServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &backpressureServer{
		listener: listener,
		gate:     make(chan struct{}),
		consumed: make(chan int64, 1),
	}
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()

		// A short greeting, so the download copy has real data to carry and is provably working.
		if _, err := conn.Write([]byte("greeting")); err != nil {
			return
		}

		// Then stop reading until the test says otherwise. This is the slow receiver.
		<-server.gate

		buffer := make([]byte, 32*1024)
		var total int64
		for {
			n, readErr := conn.Read(buffer)
			total += int64(n)
			if readErr != nil {
				select {
				case server.consumed <- total:
				default:
				}
				return
			}
			// Drain without echoing further: the point is consumption, not conversation.
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

// TestBackpressure_HealthyBlockedWriterIsIdenticalToStalled measures the lower bound: a genuinely
// healthy flow whose writer is blocked by a slow receiver, observed at the points a detector could
// plausibly sample.
//
// It is written to be informative whichever way it goes, and the log line is the result.
func TestBackpressure_HealthyBlockedWriterIsIdenticalToStalled(t *testing.T) {
	server := newBackpressureServer(t)
	captured := &stallLog{}
	manager := NewConnectionManager(captured.logger())

	client, proxySide := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = proxySide.Close(); _ = manager.Close() })

	closed := make(chan struct{})
	var once sync.Once
	manager.NewConnection(context.Background(),
		newDialerTo(t, manager, M.ParseSocksaddr(server.listener.Addr().String())), proxySide,
		adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("ws.chatgpt.com", 443)},
		func(error) { once.Do(func() { close(closed) }) })

	// Prove the flow is live in BOTH directions before the receiver goes slow: read the greeting,
	// then exchange one round trip.
	greeting := make([]byte, len("greeting"))
	if _, err := client.Read(greeting); err != nil {
		t.Fatalf("the greeting never arrived, so the fixture is not live: %v\n%s", err, captured.dump())
	}
	t.Logf("greeting received: %q", greeting)

	writeDone := make(chan error, 1)
	go func() {
		_, err := client.Write([]byte("hello"))
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("the first application write failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the first application write never completed, so the fixture is not live\n%s",
			captured.dump())
	}

	// Now fill the chain. The peer has stopped reading, so this must block.
	if !fillTheChain(t, client, 6*time.Second) {
		t.Fatal("the writer never blocked, so this is not a backpressured flow")
	}

	for _, pause := range []time.Duration{0, 2 * time.Second, 5 * time.Second, 10 * time.Second} {
		if pause > 0 {
			time.Sleep(pause)
		}
		observation := observeCopyGoroutines()
		t.Logf("paused %-4s: copies=%d upload=%q download=%q uploadWriting=%v sameSocket=%v",
			pause, observation.copies, observation.uploadFrame, observation.downloadFrame,
			observation.uploadWriting, observation.sameSocket)
	}

	// Still healthy: nothing closed it and the manager still counts it.
	select {
	case <-closed:
		t.Fatalf("the healthy backpressured flow was closed\n%s", captured.dump())
	default:
	}
	if count := manager.Count(); count != 1 {
		t.Fatalf("the manager holds %d connections, want 1\n%s", count, captured.dump())
	}

	// Let the slow receiver catch up. A healthy flow must resume.
	released := time.Now()
	close(server.gate)

	recovered := make(chan struct{})
	go func() {
		defer close(recovered)
		payload := make([]byte, 32*1024)
		for index := 0; index < 8; index++ {
			if _, err := client.Write(payload); err != nil {
				return
			}
		}
	}()
	select {
	case <-recovered:
		t.Logf("HEALTHY RESUMPTION: the slow receiver caught up and the writer proceeded")
	case <-time.After(30 * time.Second):
		t.Fatalf("the flow did NOT resume after the receiver caught up, so it was not healthy "+
			"backpressure\n%s", captured.dump())
	}
	_ = released
}

// TestBackpressure_KernelGiveUpWindow measures the upper bound: how long an idle-but-established
// connection whose peer has stopped reading survives before the kernel itself declares it dead.
//
// This bounds the entire bug. If the kernel closes a stalled socket after T, the stall can only ever
// last T unaided, and a grace period longer than T is pointless - the read loop would have failed on
// its own. The measurement is a bound, not a constant.
func TestBackpressure_KernelGiveUpWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("measures a kernel timeout; takes minutes")
	}
	peer, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := peer.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()

	conn, err := net.Dial("tcp", peer.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	held := <-accepted

	start := time.Now()
	const observation = 6 * time.Minute

	report := make(chan error, 1)
	go func() {
		buffer := make([]byte, 1)
		_, readErr := held.Read(buffer)
		report <- readErr
	}()

	select {
	case readErr := <-report:
		t.Logf("KERNEL GAVE UP after %s: read returned %v",
			time.Since(start).Round(time.Second), readErr)
	case <-time.After(observation):
		t.Logf("NO KERNEL GIVE-UP within %s, with both stacks alive and no traffic.", observation)
		t.Logf("That is the expected and important result: TCP keepalive answers for a live stack, " +
			"so an idle-but-healthy connection never fails on its own, and a stall over a healthy " +
			"path is unbounded in time rather than merely slow.")
	}
}
