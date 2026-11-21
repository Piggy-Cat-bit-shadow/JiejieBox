package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// The traffic fixture exists so that a memory number cannot be bought by breaking the relay.
//
// A relay that has been optimised until it holds nothing is also a relay that loses bytes, and an
// idle-connection budget cannot see the difference: nothing is flowing, so nothing can be lost. This
// fixture pushes a deterministic payload through the real SOCKS inbound and the real connection
// manager, half-closes the client side, requires the destination to answer AFTER it saw EOF, and
// compares every byte of both directions.
//
// # Why the reply is derived from the request
//
// The destination echoes a hash of exactly what it received, plus a body whose length and content
// come from that hash. A client that gets the expected reply therefore knows three things at once:
// the request arrived intact, the destination observed the half-close (it only replies after
// ReadAll returns), and the response direction survived the client's write half closing.

const replyMagic = "MSREPLY1"

// maxTrafficBytes bounds the fixture's own allocations. It is a harness limit, not a product limit:
// the ledger is about what a flow costs while it is held, and a multi-megabyte request would measure
// the fixture's buffer growth instead.
const maxTrafficBytes = 8 << 20

// probeServer is the local HTTP endpoint a URL-test group measures against.
//
// It is started before the configuration is parsed so that a probe round has somewhere to go, and
// its own listener is charged to the fixture_servers stage rather than to the product.
type probeServer struct {
	listener net.Listener
	server   *http.Server

	mu       sync.Mutex
	requests int
}

func startProbeServer() (*probeServer, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("probe server: %w", err)
	}
	probe := &probeServer{listener: listener}
	mux := http.NewServeMux()
	mux.HandleFunc("/generate_204", func(writer http.ResponseWriter, request *http.Request) {
		probe.mu.Lock()
		probe.requests++
		probe.mu.Unlock()
		writer.WriteHeader(http.StatusNoContent)
	})
	probe.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = probe.server.Serve(listener) }()
	return probe, nil
}

func (p *probeServer) URL() string {
	return "http://" + p.listener.Addr().String() + "/generate_204"
}

func (p *probeServer) Requests() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests
}

func (p *probeServer) Close() {
	_ = p.server.Close()
}

// destinationServer is the peer at the far end of the relay. It reads until EOF and only then
// answers, which is what turns half-close handling into an observable.
type destinationServer struct {
	listener net.Listener
	address  *net.TCPAddr

	mu        sync.Mutex
	accepted  int
	completed int
	failures  []string
	closed    bool
	wait      sync.WaitGroup
}

func startDestinationServer() (*destinationServer, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("destination: %w", err)
	}
	server := &destinationServer{listener: listener, address: listener.Addr().(*net.TCPAddr)}
	go server.acceptLoop()
	return server, nil
}

func (d *destinationServer) acceptLoop() {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			return
		}
		d.wait.Add(1)
		go d.serve(conn)
	}
}

func (d *destinationServer) serve(conn net.Conn) {
	defer d.wait.Done()
	defer func() { _ = conn.Close() }()
	d.mu.Lock()
	d.accepted++
	d.mu.Unlock()

	request, err := io.ReadAll(io.LimitReader(conn, maxTrafficBytes+1))
	if err != nil {
		d.fail("read request: " + err.Error())
		return
	}
	if len(request) > maxTrafficBytes {
		d.fail("request exceeded the fixture limit")
		return
	}
	// The reply cannot be written until ReadAll returns, and ReadAll returns on EOF. A relay that
	// turned CloseWrite into Close leaves this write going nowhere, which the client sees as a
	// missing reply rather than as a wrong byte.
	if _, err = conn.Write(buildReply(request)); err != nil {
		d.fail("write reply: " + err.Error())
		return
	}
	d.mu.Lock()
	d.completed++
	d.mu.Unlock()
}

func (d *destinationServer) fail(reason string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.failures) < 8 {
		d.failures = append(d.failures, reason)
	}
}

func (d *destinationServer) Address() net.Addr { return d.address }

func (d *destinationServer) Counts() (accepted int, completed int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.accepted, d.completed
}

func (d *destinationServer) Failures() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.failures...)
}

func (d *destinationServer) Close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	d.mu.Unlock()
	_ = d.listener.Close()
}

func (d *destinationServer) WaitAccepted(flows int, timeout time.Duration) error {
	return waitUntil(timeout, func() bool {
		accepted, _ := d.Counts()
		return accepted >= flows
	}, func() string {
		accepted, _ := d.Counts()
		return fmt.Sprintf("destination accepted %d of %d flows", accepted, flows)
	})
}

func (d *destinationServer) WaitCompleted(flows int, timeout time.Duration) error {
	return waitUntil(timeout, func() bool {
		_, completed := d.Counts()
		return completed >= flows
	}, func() string {
		_, completed := d.Counts()
		return fmt.Sprintf("destination completed %d of %d flows", completed, flows)
	})
}

// waitUntil polls a predicate to a deadline.
//
// A poll is not a substitute for a readiness signal; it is how a signal that arrives from another
// process's goroutine is consumed. Every wait in this fixture is on an observable event (a
// connection accepted, a reply completed, a counter reaching a value), and the deadline turns "it
// never happened" into a failure instead of a hang.
func waitUntil(timeout time.Duration, condition func() bool, describe func() string) error {
	deadline := time.Now().Add(timeout)
	for {
		if condition() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s within %s", describe(), timeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// buildReply derives the response from what was actually received.
func buildReply(request []byte) []byte {
	digest := sha256.Sum256(request)
	body := replyBody(len(request))
	reply := make([]byte, 0, len(replyMagic)+8+len(digest)+len(body))
	reply = append(reply, replyMagic...)
	reply = binary.BigEndian.AppendUint64(reply, uint64(len(request)))
	reply = append(reply, digest[:]...)
	reply = append(reply, body...)
	return reply
}

// replyBody is a deterministic function of the request length, independent of the request content,
// so a corrupted request and a corrupted response cannot cancel each other out.
func replyBody(requestBytes int) []byte {
	body := make([]byte, requestBytes)
	for index := range body {
		body[index] = byte((index*13 + requestBytes*7) % 239)
	}
	return body
}

// requestPayload is a deterministic function of the flow index, so two flows cannot be confused
// with each other and a retry produces exactly the same bytes.
func requestPayload(flow int, size int) []byte {
	payload := make([]byte, size)
	for index := range payload {
		payload[index] = byte((index*31 + flow*97 + 11) % 251)
	}
	return payload
}

// trafficHarness drives flows through the relay and records what the business path did.
type trafficHarness struct {
	socksAddress string
	destination  *destinationServer
	held         []net.Conn
	report       trafficReport
}

func newTrafficHarness(socksAddress string, destination *destinationServer, flows int) *trafficHarness {
	return &trafficHarness{
		socksAddress: socksAddress,
		destination:  destination,
		report:       trafficReport{Flows: flows},
	}
}

// Open establishes `flows` connections through the relay and waits until the destination has
// accepted all of them. The connections are then held and quiet, which is the state the held-flow
// reading is taken in.
func (t *trafficHarness) Open(flows int) error {
	for index := 0; index < flows; index++ {
		conn, err := dialThroughSOCKS(t.socksAddress, t.destination.Address())
		if err != nil {
			t.Close()
			return fmt.Errorf("flow %d: %w", index, err)
		}
		t.held = append(t.held, conn)
	}
	if err := t.destination.WaitAccepted(flows, 20*time.Second); err != nil {
		return err
	}
	return nil
}

// Held exposes the established connections so the caller can keep them alive across a reading.
func (t *trafficHarness) Held() []net.Conn { return t.held }

// Verify pushes the payload through every held flow, half-closes the client side, and compares both
// directions. The connections are NOT closed here: the verified-but-still-held state is its own
// ledger stage, and closing is a separate, synchronous step.
func (t *trafficHarness) Verify(requestBytes int) error {
	for index, conn := range t.held {
		request := requestPayload(index, requestBytes)
		if _, err := conn.Write(request); err != nil {
			t.report.Failures = append(t.report.Failures, fmt.Sprintf("flow %d write: %v", index, err))
			continue
		}
		tcpConn, ok := conn.(*net.TCPConn)
		if !ok {
			t.report.Failures = append(t.report.Failures, fmt.Sprintf("flow %d is %T, not *net.TCPConn", index, conn))
			continue
		}
		// CloseWrite, not Close: the read half must stay usable for the reply. This is the whole
		// point of the fixture - a relay that propagates the close as a full close passes an idle
		// budget and fails here.
		if err := tcpConn.CloseWrite(); err != nil {
			t.report.Failures = append(t.report.Failures, fmt.Sprintf("flow %d close-write: %v", index, err))
			continue
		}
		if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			t.report.Failures = append(t.report.Failures, fmt.Sprintf("flow %d deadline: %v", index, err))
			continue
		}
		reply, err := io.ReadAll(io.LimitReader(conn, int64(len(buildReply(request)))+1))
		if err != nil {
			t.report.Failures = append(t.report.Failures, fmt.Sprintf("flow %d read reply: %v", index, err))
			continue
		}
		if len(reply) == 0 {
			t.report.Failures = append(t.report.Failures,
				fmt.Sprintf("flow %d got no reply: the response direction was torn down by the half-close", index))
			continue
		}
		t.report.ResponseAfterHalfClose = true
		expected := buildReply(request)
		if !bytes.Equal(reply, expected) {
			t.report.Failures = append(t.report.Failures,
				fmt.Sprintf("flow %d reply mismatch: got %d bytes, want %d", index, len(reply), len(expected)))
			continue
		}
		t.report.RequestIntact = true
		t.report.ResponseIntact = true
		t.report.HalfClosePropagated = true
		t.report.RequestBytes += int64(len(request))
		t.report.ResponseBytes += int64(len(reply))
	}
	t.report.Verified = t.report.RequestIntact && t.report.ResponseIntact &&
		t.report.HalfClosePropagated && t.report.ResponseAfterHalfClose &&
		len(t.report.Failures) == 0
	return nil
}

func (t *trafficHarness) Report() trafficReport { return t.report }

// Close closes the client side of every flow and waits for the destination to finish its side, so
// the next reading is taken with the relay truly drained rather than mid-teardown.
func (t *trafficHarness) Close() {
	for _, conn := range t.held {
		_ = conn.Close()
	}
	t.held = nil
}

func (t *trafficHarness) WaitDrained(flows int, timeout time.Duration) error {
	return t.destination.WaitCompleted(flows, timeout)
}

// dialThroughSOCKS performs the smallest SOCKS5 handshake that yields an established stream: no
// authentication, CONNECT to the destination address. After it returns the connection is a plain
// byte stream and the box is holding it.
func dialThroughSOCKS(proxyAddress string, destination net.Addr) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp4", proxyAddress, 10*time.Second)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (net.Conn, error) {
		_ = conn.Close()
		return nil, err
	}
	if _, err = conn.Write([]byte{5, 1, 0}); err != nil {
		return fail(err)
	}
	greeting := make([]byte, 2)
	if _, err = io.ReadFull(conn, greeting); err != nil {
		return fail(err)
	}
	if greeting[0] != 5 || greeting[1] != 0 {
		return fail(fmt.Errorf("proxy answered %v", greeting))
	}
	tcpAddress, ok := destination.(*net.TCPAddr)
	if !ok {
		return fail(fmt.Errorf("destination is %T", destination))
	}
	ipv4 := tcpAddress.IP.To4()
	if ipv4 == nil {
		return fail(fmt.Errorf("destination %v is not IPv4", tcpAddress))
	}
	request := []byte{5, 1, 0, 1}
	request = append(request, ipv4...)
	request = binary.BigEndian.AppendUint16(request, uint16(tcpAddress.Port))
	if _, err = conn.Write(request); err != nil {
		return fail(err)
	}
	reply := make([]byte, 10)
	if _, err = io.ReadFull(conn, reply); err != nil {
		return fail(err)
	}
	if reply[1] != 0 {
		return fail(fmt.Errorf("proxy refused the connect: code %d", reply[1]))
	}
	return conn, nil
}

// reservePort asks the operating system for a free port and gives it back.
//
// There is a race between the release and the box binding it. It is accepted here because the
// alternative - binding a listener and handing the socket over - is not something the product's own
// configuration path supports, and the window is a few milliseconds on a loopback interface. A run
// that loses the race fails loudly at Start (bind: address already in use) rather than measuring a
// box that is not listening.
func reservePort() (uint16, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		return 0, err
	}
	return uint16(port), nil
}
