package loadgen

import (
	"context"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/scripts/perf/internal/target"
)

// fakeSocks is a SOCKS5 UDP-ASSOCIATE server plus a relay that forwards each
// payload to a real target udp-echo and can duplicate, reorder, delay, corrupt or
// drop the reply. It exists so the loader's accounting can be tested end to end
// without a proxy binary.
type fakeSocks struct {
	t        *testing.T
	listener net.Listener
	udp      *net.UDPConn
	upstream *net.UDPConn
	target   *net.UDPAddr
	scenario relayScenario
	mu       sync.Mutex
	pending  []byte
	closed   chan struct{}
	wg       sync.WaitGroup
}

type relayScenario struct {
	duplicate    bool
	reorder      bool
	delay        time.Duration
	staleEvery   int
	foreignEvery int
	dropEvery    int
}

func startFakeSocks(t *testing.T, targetAddr string, scenario relayScenario) *fakeSocks {
	t.Helper()
	resolved, err := net.ResolveUDPAddr("udp", targetAddr)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	upstream, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		listener.Close()
		udp.Close()
		t.Fatal(err)
	}
	fake := &fakeSocks{t: t, listener: listener, udp: udp, upstream: upstream, target: resolved, scenario: scenario, closed: make(chan struct{})}
	fake.wg.Add(2)
	go fake.serveTCP()
	go fake.serveUDP()
	t.Cleanup(func() {
		close(fake.closed)
		listener.Close()
		udp.Close()
		upstream.Close()
		fake.wg.Wait()
	})
	return fake
}

func (f *fakeSocks) addr() string { return f.listener.Addr().String() }

func (f *fakeSocks) serveTCP() {
	defer f.wg.Done()
	conn, err := f.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	greeting := make([]byte, 3)
	if _, err = conn.Read(greeting); err != nil {
		return
	}
	if _, err = conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}
	request := make([]byte, 10)
	if _, err = conn.Read(request); err != nil {
		return
	}
	if request[1] != 0x03 {
		f.t.Errorf("command = %d, want UDP ASSOCIATE", request[1])
		return
	}
	port := f.udp.LocalAddr().(*net.UDPAddr).Port
	reply := []byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, byte(port >> 8), byte(port)}
	if _, err = conn.Write(reply); err != nil {
		return
	}
	<-f.closed
}

func (f *fakeSocks) serveUDP() {
	defer f.wg.Done()
	buffer := make([]byte, 64*1024)
	echoBuffer := make([]byte, 64*1024)
	client := (*net.UDPAddr)(nil)
	count := 0
	for {
		_ = f.udp.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, from, err := f.udp.ReadFromUDP(buffer)
		if err != nil {
			select {
			case <-f.closed:
				return
			default:
				continue
			}
		}
		client = from
		count++
		payload := socksUDPPayload(buffer[:n])
		if len(payload) < udpHeaderSize {
			continue
		}
		if f.scenario.dropEvery > 0 && count%f.scenario.dropEvery == 0 {
			continue
		}
		response := append([]byte(nil), payload...)
		if f.scenario.staleEvery > 0 && count%f.scenario.staleEvery == 0 {
			binary.BigEndian.PutUint32(response[4:8], binary.BigEndian.Uint32(response[4:8])+7)
		}
		if f.scenario.foreignEvery > 0 && count%f.scenario.foreignEvery == 0 {
			binary.BigEndian.PutUint32(response[0:4], 0xdeadbeef)
		}
		if _, err = f.upstream.WriteToUDP(response, f.target); err != nil {
			continue
		}
		_ = f.upstream.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		echoed, _, err := f.upstream.ReadFromUDP(echoBuffer)
		if err != nil {
			continue
		}
		if f.scenario.delay > 0 {
			time.Sleep(f.scenario.delay)
		}
		f.reply(client, append([]byte(nil), echoBuffer[:echoed]...))
		if f.scenario.duplicate {
			f.reply(client, append([]byte(nil), echoBuffer[:echoed]...))
		}
	}
}

func (f *fakeSocks) reply(client *net.UDPAddr, payload []byte) {
	header := f.responseHeader()
	packet := append(header, payload...)
	f.mu.Lock()
	if f.scenario.reorder {
		if f.pending == nil {
			f.pending = packet
			f.mu.Unlock()
			return
		}
		previous := f.pending
		f.pending = nil
		f.mu.Unlock()
		_, _ = f.udp.WriteToUDP(packet, client)
		_, _ = f.udp.WriteToUDP(previous, client)
		return
	}
	f.mu.Unlock()
	_, _ = f.udp.WriteToUDP(packet, client)
}

func (f *fakeSocks) responseHeader() []byte {
	port := f.target.Port
	return []byte{0x00, 0x00, 0x00, 0x01, 127, 0, 0, 1, byte(port >> 8), byte(port)}
}

func startEcho(t *testing.T) *target.Server {
	t.Helper()
	server, err := target.Start(target.UDPEcho, "127.0.0.1:0", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func runUDPTest(t *testing.T, proxyAddr, targetAddr string, cfg Config) (Result, error) {
	t.Helper()
	if cfg.PacketSize == 0 {
		cfg.PacketSize = 200
	}
	if cfg.Streams == 0 {
		cfg.Streams = 1
	}
	return RunUDPForTest(context.Background(), cfg, proxyAddr, targetAddr)
}

// RunUDPForTest is the test seam: Run() normally derives the proxy and target
// addresses from a scenario, which a unit test does not have.
func RunUDPForTest(ctx context.Context, cfg Config, proxyAddr, targetAddr string) (Result, error) {
	cfg.ProxyAddr = proxyAddr
	cfg.TargetAddr = targetAddr
	cfg.Pattern = UDP
	cfg.normalize()
	return runUDP(ctx, cfg)
}

func TestPacedUDPRoundTrip(t *testing.T) {
	echo := startEcho(t)
	relay := startFakeSocks(t, echo.Addr(), relayScenario{})
	cfg := Config{Duration: 400 * time.Millisecond, UDPRate: 300, UDPDrain: 300 * time.Millisecond, PacketSize: 200}
	result, err := runUDPTest(t, relay.addr(), echo.Addr(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Paced || result.DatagramWrites == 0 {
		t.Fatalf("paced=%v writes=%d", result.Paced, result.DatagramWrites)
	}
	if result.DatagramUnique != result.DatagramWrites {
		t.Fatalf("unique=%d writes=%d", result.DatagramUnique, result.DatagramWrites)
	}
	if result.DatagramDuplicate != 0 || result.DatagramForeign != 0 || result.DatagramStale != 0 {
		t.Fatalf("unexpected attribution: %+v", result)
	}
	if !result.DrainComplete || !result.AllReturned {
		t.Fatalf("drain complete=%v all returned=%v", result.DrainComplete, result.AllReturned)
	}
	stats := echo.Snapshot()
	if stats.Datagrams != result.DatagramWrites || stats.Echoed != result.DatagramWrites {
		t.Fatalf("target datagrams=%d echoed=%d, writes=%d", stats.Datagrams, stats.Echoed, result.DatagramWrites)
	}
}

func TestPacedUDPDuplicateInjection(t *testing.T) {
	echo := startEcho(t)
	relay := startFakeSocks(t, echo.Addr(), relayScenario{duplicate: true})
	result, err := runUDPTest(t, relay.addr(), echo.Addr(), Config{Duration: 300 * time.Millisecond, UDPRate: 200, UDPDrain: 300 * time.Millisecond, PacketSize: 200})
	if err != nil {
		t.Fatal(err)
	}
	if result.DatagramDuplicate == 0 {
		t.Fatal("duplicates were not attributed")
	}
	if result.DatagramUnique != result.DatagramWrites {
		t.Fatalf("unique=%d writes=%d", result.DatagramUnique, result.DatagramWrites)
	}
	if result.DatagramReturned <= result.DatagramUnique {
		t.Fatalf("returned=%d unique=%d", result.DatagramReturned, result.DatagramUnique)
	}
}

func TestPacedUDPReorderInjection(t *testing.T) {
	echo := startEcho(t)
	relay := startFakeSocks(t, echo.Addr(), relayScenario{reorder: true})
	result, err := runUDPTest(t, relay.addr(), echo.Addr(), Config{Duration: 300 * time.Millisecond, UDPRate: 200, UDPDrain: 300 * time.Millisecond, PacketSize: 200})
	if err != nil {
		t.Fatal(err)
	}
	if result.DatagramOutOfOrder == 0 {
		t.Fatalf("reordering was not attributed: %+v", result)
	}
	if result.DatagramUnique != result.DatagramWrites {
		t.Fatalf("unique=%d writes=%d", result.DatagramUnique, result.DatagramWrites)
	}
}

func TestPacedUDPStaleEpochIgnored(t *testing.T) {
	echo := startEcho(t)
	relay := startFakeSocks(t, echo.Addr(), relayScenario{staleEvery: 4})
	result, err := runUDPTest(t, relay.addr(), echo.Addr(), Config{Duration: 300 * time.Millisecond, UDPRate: 200, UDPDrain: 300 * time.Millisecond, PacketSize: 200})
	if err != nil {
		t.Fatal(err)
	}
	if result.DatagramStale == 0 {
		t.Fatal("stale datagrams were not counted")
	}
	if result.DatagramUnique == result.DatagramWrites {
		t.Fatal("stale datagrams must not count as unique")
	}
}

func TestPacedUDPForeignDatagramIgnored(t *testing.T) {
	echo := startEcho(t)
	relay := startFakeSocks(t, echo.Addr(), relayScenario{foreignEvery: 4})
	result, err := runUDPTest(t, relay.addr(), echo.Addr(), Config{Duration: 300 * time.Millisecond, UDPRate: 200, UDPDrain: 300 * time.Millisecond, PacketSize: 200})
	if err != nil {
		t.Fatal(err)
	}
	if result.DatagramForeign == 0 {
		t.Fatal("foreign datagrams were not counted")
	}
	if result.DatagramUnique == result.DatagramWrites {
		t.Fatal("foreign datagrams must not count as unique")
	}
}

func TestPacedUDPDrainCapturesDelayedReplies(t *testing.T) {
	echo := startEcho(t)
	relay := startFakeSocks(t, echo.Addr(), relayScenario{delay: 120 * time.Millisecond})
	result, err := runUDPTest(t, relay.addr(), echo.Addr(), Config{Duration: 150 * time.Millisecond, UDPRate: 20, UDPDrain: 600 * time.Millisecond, PacketSize: 200})
	if err != nil {
		t.Fatal(err)
	}
	if result.DatagramUnique != result.DatagramWrites {
		t.Fatalf("drain did not capture delayed replies: unique=%d writes=%d", result.DatagramUnique, result.DatagramWrites)
	}
	if result.DrainLastReply < 50*time.Millisecond {
		t.Fatalf("last reply only %v after send end", result.DrainLastReply)
	}
}

func TestPacedUDPContextCancel(t *testing.T) {
	echo := startEcho(t)
	relay := startFakeSocks(t, echo.Addr(), relayScenario{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	result, err := RunUDPForTest(ctx, Config{Duration: 2 * time.Second, UDPRate: 200, UDPDrain: 500 * time.Millisecond, PacketSize: 200}, relay.addr(), echo.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 1200*time.Millisecond {
		t.Fatalf("cancel took %v", elapsed)
	}
	if !result.Aborted {
		t.Fatal("aborted flag not set")
	}
	if result.DatagramWrites == 0 {
		t.Fatal("no packets before cancellation")
	}
}

func TestUDPAssociateFailureIsAnError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	if _, err = RunUDPForTest(context.Background(), Config{Duration: 100 * time.Millisecond, UDPRate: 100}, addr, "127.0.0.1:9"); err == nil {
		t.Fatal("associate failure was not returned as an error")
	}
}
