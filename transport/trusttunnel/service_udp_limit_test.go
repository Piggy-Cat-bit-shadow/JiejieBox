package trusttunnel

import (
	"context"
	"io"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// udpDestinations returns count distinct client 4-tuples that do not need a
// listening socket: the test handler echoes, it never dials.
func udpDestinations(count int) []M.Socksaddr {
	address := netip.MustParseAddr("127.0.0.1")
	destinations := make([]M.Socksaddr, count)
	for i := range destinations {
		destinations[i] = M.SocksaddrFrom(address, uint16(20000+i))
	}
	return destinations
}

func waitForUDPStats(t *testing.T, service *Service, what string, condition func(UDPStats) bool) UDPStats {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		stats := service.UDPStats()
		if condition(stats) {
			return stats
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s, stats %+v", what, stats)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type receivedDatagram struct {
	payload []byte
	source  M.Socksaddr
	err     error
}

// readDatagram reads one reply. PacketConn has no read deadline on HTTP/2, so
// the timeout is the test's, not the conn's.
func readDatagram(t *testing.T, conn *PacketConn, timeout time.Duration) receivedDatagram {
	t.Helper()
	results := make(chan receivedDatagram, 1)
	go func() {
		buffer := buf.NewSize(MaxPayloadSize)
		defer buffer.Release()
		source, err := conn.ReadPacket(buffer)
		results <- receivedDatagram{payload: append([]byte(nil), buffer.Bytes()...), source: source, err: err}
	}()
	select {
	case result := <-results:
		return result
	case <-time.After(timeout):
		t.Fatal("timed out reading a UDP reply")
		return receivedDatagram{}
	}
}

func writeDatagram(t *testing.T, conn *PacketConn, payload []byte, destination M.Socksaddr) {
	t.Helper()
	_, err := conn.WriteTo(payload, destination.UDPAddr())
	if err != nil {
		t.Fatalf("write to %s: %s", destination, err)
	}
}

func openRawUDPStream(t *testing.T, client *Client) *rawStream {
	t.Helper()
	_, stream, err := client.openStream(testContext(t), nil, AuthorityUDP, UserAgent("test"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stream.close)
	return stream
}

// writeRawClientFrame writes one well-formed _udp2 client frame.
func writeRawClientFrame(t *testing.T, stream *rawStream, payload []byte, destination M.Socksaddr) {
	t.Helper()
	const appName = "test"
	buffer := buf.NewSize(ClientFrameHeadroom(appName) + len(payload))
	buffer.Advance(ClientFrameHeadroom(appName))
	_, err := buffer.Write(payload)
	if err == nil {
		err = EncodeClientFrame(buffer, M.Socksaddr{}, destination, appName)
	}
	if err == nil {
		_, err = stream.writer.Write(buffer.Bytes())
	}
	buffer.Release()
	if err != nil {
		t.Fatal(err)
	}
}

// holdingHandler records associations but does not read them until release is
// closed, so the service queues have to hold the datagrams.
type holdingHandler struct {
	access  sync.Mutex
	conns   []N.PacketConn
	release chan struct{}
	closed  atomic.Int32
}

func (h *holdingHandler) NewConnection(ctx context.Context, conn *ServerConn, metadata Metadata) {
	conn.Close()
}

func (h *holdingHandler) NewPacketConnection(ctx context.Context, conn N.PacketConn, metadata Metadata) {
	h.access.Lock()
	h.conns = append(h.conns, conn)
	h.access.Unlock()
	go func() {
		<-h.release
		for {
			buffer := buf.NewSize(MaxPayloadSize)
			_, err := conn.ReadPacket(buffer)
			buffer.Release()
			if err != nil {
				conn.Close()
				h.closed.Add(1)
				return
			}
		}
	}()
}

func (h *holdingHandler) associations() int {
	h.access.Lock()
	defer h.access.Unlock()
	return len(h.conns)
}

// rejectHandler refuses every association the way a routing rejection does.
type rejectHandler struct {
	accepted atomic.Int32
}

func (h *rejectHandler) NewConnection(ctx context.Context, conn *ServerConn, metadata Metadata) {
	conn.Close()
}

func (h *rejectHandler) NewPacketConnection(ctx context.Context, conn N.PacketConn, metadata Metadata) {
	h.accepted.Add(1)
	conn.Close()
}

// TestUDPSessionAssociationLimit floods one stream with distinct 4-tuples: the
// association count stops at the stream limit, the refused datagrams cost
// nothing, an admitted association keeps working and closing the stream returns
// every slot.
func TestUDPSessionAssociationLimit(t *testing.T) {
	limits := UDPLimits{
		SessionAssociations: 4,
		ServiceAssociations: 64,
		SessionQueuedBytes:  1 << 20,
		ServiceQueuedBytes:  1 << 20,
		IdleTimeout:         time.Minute,
	}
	handler := &testHandler{}
	server := startTestService(t, ProtocolHTTP2, handler, limits)
	client := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	packetConn, err := client.ListenPacket(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	destinations := udpDestinations(12)
	for _, destination := range destinations {
		writeDatagram(t, packetConn, []byte("probe"), destination)
	}
	stats := waitForUDPStats(t, server.service, "the session association limit", func(stats UDPStats) bool {
		return stats.Associations == limits.SessionAssociations && stats.RefusedAssociations >= 8
	})
	if stats.QueuedBytes > limits.SessionQueuedBytes {
		t.Fatalf("%d queued bytes over the %d limit", stats.QueuedBytes, limits.SessionQueuedBytes)
	}
	waitFor(t, "the handler associations", func() bool {
		return len(handler.udpMetadata()) == limits.SessionAssociations
	})
	admitted := make(map[M.Socksaddr]bool)
	for _, flow := range handler.udpMetadata() {
		admitted[flow.Destination] = true
	}
	if len(admitted) != limits.SessionAssociations {
		t.Fatalf("%d associations reached the handler, want %d", len(admitted), limits.SessionAssociations)
	}
	for _, destination := range destinations[:limits.SessionAssociations] {
		if !admitted[destination] {
			t.Fatalf("association to %s was refused before the limit was reached", destination)
		}
	}
	// Every admitted association answers, even after the flood was refused.
	replied := make(map[M.Socksaddr]bool)
	for i := 0; i < limits.SessionAssociations; i++ {
		reply := readDatagram(t, packetConn, 5*time.Second)
		if reply.err != nil {
			t.Fatal(reply.err)
		}
		replied[reply.source] = true
	}
	for destination := range admitted {
		if !replied[destination] {
			t.Fatalf("association to %s stopped answering", destination)
		}
	}
	writeDatagram(t, packetConn, []byte("again"), destinations[0])
	if reply := readDatagram(t, packetConn, 5*time.Second); reply.err != nil || !reply.source.IsValid() {
		t.Fatalf("healthy association after the flood: %v %s", reply.err, reply.source)
	}
	if err = packetConn.Close(); err != nil {
		t.Fatal(err)
	}
	waitForUDPStats(t, server.service, "the release after close", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
}

// TestUDPServiceAssociationLimit fills the service-wide association budget from
// one stream, refuses a second stream, and admits it again once the first one
// returns its slots.
func TestUDPServiceAssociationLimit(t *testing.T) {
	limits := UDPLimits{
		SessionAssociations: 8,
		ServiceAssociations: 3,
		SessionQueuedBytes:  1 << 20,
		ServiceQueuedBytes:  1 << 20,
		IdleTimeout:         time.Minute,
	}
	handler := &testHandler{}
	server := startTestService(t, ProtocolHTTP2, handler, limits)
	first := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	second := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	firstConn, err := first.ListenPacket(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	secondConn, err := second.ListenPacket(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	firstDestinations := udpDestinations(3)
	for index, destination := range firstDestinations {
		writeDatagram(t, firstConn, []byte{byte(index + 1)}, destination)
	}
	waitForUDPStats(t, server.service, "the service budget", func(stats UDPStats) bool {
		return stats.Associations == limits.ServiceAssociations
	})
	secondDestinations := []M.Socksaddr{
		M.SocksaddrFrom(netip.MustParseAddr("127.0.0.2"), 20000),
		M.SocksaddrFrom(netip.MustParseAddr("127.0.0.2"), 20001),
	}
	for _, destination := range secondDestinations {
		writeDatagram(t, secondConn, []byte("second"), destination)
	}
	stats := waitForUDPStats(t, server.service, "the service-wide refusal", func(stats UDPStats) bool {
		return stats.Associations == limits.ServiceAssociations && stats.RefusedAssociations >= 2
	})
	if stats.QueuedBytes > limits.ServiceQueuedBytes {
		t.Fatalf("%d queued bytes over the %d limit", stats.QueuedBytes, limits.ServiceQueuedBytes)
	}
	waitFor(t, "the first stream's associations", func() bool {
		return len(handler.udpMetadata()) == len(firstDestinations)
	})
	// The first stream still answers.
	for range firstDestinations {
		if reply := readDatagram(t, firstConn, 5*time.Second); reply.err != nil {
			t.Fatal(reply.err)
		}
	}
	// Closing the first stream returns the slots to the second one.
	if err = firstConn.Close(); err != nil {
		t.Fatal(err)
	}
	waitForUDPStats(t, server.service, "the release of the first stream", func(stats UDPStats) bool {
		return stats.Associations == 0
	})
	writeDatagram(t, secondConn, []byte("second"), secondDestinations[0])
	reply := readDatagram(t, secondConn, 5*time.Second)
	if reply.err != nil {
		t.Fatal(reply.err)
	}
	if reply.source != secondDestinations[0] {
		t.Fatalf("reply from %s, want %s", reply.source, secondDestinations[0])
	}
	secondConn.Close()
	waitForUDPStats(t, server.service, "the final release", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
}

// TestUDPSessionQueuedBytes fills one association's queues while the handler is
// not reading: the stream byte limit caps the queue, datagrams over it are
// dropped, and the buffered datagrams are returned once the handler drains.
func TestUDPSessionQueuedBytes(t *testing.T) {
	limits := UDPLimits{
		SessionAssociations: 8,
		ServiceAssociations: 64,
		SessionQueuedBytes:  200_000,
		ServiceQueuedBytes:  400_000,
		IdleTimeout:         time.Minute,
	}
	handler := &holdingHandler{release: make(chan struct{})}
	server := startTestService(t, ProtocolHTTP2, handler, limits)
	client := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	packetConn, err := client.ListenPacket(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	destination := udpDestinations(1)[0]
	payload := make([]byte, MaxPayloadSize)
	for i := 0; i < 8; i++ {
		writeDatagram(t, packetConn, payload, destination)
	}
	// Three datagrams fit the 200000-byte stream limit; the fourth would cross
	// it and is dropped.
	wantQueued := 3 * MaxPayloadSize
	stats := waitForUDPStats(t, server.service, "the stream byte limit", func(stats UDPStats) bool {
		return stats.QueuedBytes == wantQueued && stats.DroppedDatagrams > 0
	})
	if stats.Associations != 1 {
		t.Fatalf("%d associations, want 1", stats.Associations)
	}
	close(handler.release)
	waitForUDPStats(t, server.service, "the drain of the queues", func(stats UDPStats) bool {
		return stats.QueuedBytes == 0
	})
	// The association itself lives until the stream closes.
	if stats = server.service.UDPStats(); stats.Associations != 1 {
		t.Fatalf("%d associations after the drain, want 1", stats.Associations)
	}
	packetConn.Close()
	waitForUDPStats(t, server.service, "the release after close", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
}

// TestUDPServiceQueuedBytes fills the service-wide byte budget from two streams:
// the shared queue stays under the service limit and returns to zero when the
// held associations are drained and closed.
func TestUDPServiceQueuedBytes(t *testing.T) {
	limits := UDPLimits{
		SessionAssociations: 8,
		ServiceAssociations: 64,
		SessionQueuedBytes:  400_000,
		ServiceQueuedBytes:  300_000,
		IdleTimeout:         time.Minute,
	}
	handler := &holdingHandler{release: make(chan struct{})}
	server := startTestService(t, ProtocolHTTP2, handler, limits)
	first := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	second := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	firstConn, err := first.ListenPacket(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	secondConn, err := second.ListenPacket(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, MaxPayloadSize)
	for i := 0; i < 3; i++ {
		writeDatagram(t, firstConn, payload, udpDestinations(1)[0])
	}
	for i := 0; i < 2; i++ {
		writeDatagram(t, secondConn, payload, M.SocksaddrFrom(netip.MustParseAddr("127.0.0.2"), 20000))
	}
	// At most four 64 KiB datagrams fit the 300000-byte service budget.
	wantQueued := 4 * MaxPayloadSize
	waitForUDPStats(t, server.service, "the service byte limit", func(stats UDPStats) bool {
		return stats.QueuedBytes == wantQueued && stats.DroppedDatagrams > 0
	})
	close(handler.release)
	waitForUDPStats(t, server.service, "the service queue drain", func(stats UDPStats) bool {
		return stats.QueuedBytes == 0
	})
	firstConn.Close()
	secondConn.Close()
	waitForUDPStats(t, server.service, "the final release", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
}

// TestUDPBadFrameReleasesAssociations sends a frame whose length is shorter
// than its own header: the stream cannot be resynchronised, so the session ends
// and every association and queued byte returns.
func TestUDPBadFrameReleasesAssociations(t *testing.T) {
	handler := &testHandler{}
	server := startTestService(t, ProtocolHTTP2, handler, UDPLimits{IdleTimeout: time.Minute})
	client := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	stream := openRawUDPStream(t, client)
	writeRawClientFrame(t, stream, []byte("payload"), udpDestinations(1)[0])
	waitForUDPStats(t, server.service, "the association", func(stats UDPStats) bool {
		return stats.Associations == 1
	})
	// A zero length is shorter than the 37-byte client frame header.
	if _, err := stream.writer.Write([]byte{0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	waitForUDPStats(t, server.service, "the release after the bad frame", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
}

// TestUDPHalfCloseReleasesAssociations ends the request body (the client half of
// the stream) while associations are open: the session ends, the flows are
// closed and the accounting returns to zero.
func TestUDPHalfCloseReleasesAssociations(t *testing.T) {
	handler := &testHandler{}
	server := startTestService(t, ProtocolHTTP2, handler, UDPLimits{IdleTimeout: time.Minute})
	client := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	stream := openRawUDPStream(t, client)
	destinations := udpDestinations(3)
	for _, destination := range destinations {
		writeRawClientFrame(t, stream, []byte("payload"), destination)
	}
	waitForUDPStats(t, server.service, "the associations", func(stats UDPStats) bool {
		return stats.Associations == len(destinations)
	})
	closer, isCloser := stream.writer.(io.Closer)
	if !isCloser {
		t.Fatal("the stream writer cannot be half-closed")
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	waitForUDPStats(t, server.service, "the release after the half close", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
}

// TestUDPStreamsWithoutFramesHoldNoBudget opens _udp2 streams that never send a
// frame: the HTTP layer bounds them, and the UDP accounting stays empty.
func TestUDPStreamsWithoutFramesHoldNoBudget(t *testing.T) {
	handler := &testHandler{}
	server := startTestService(t, ProtocolHTTP2, handler, UDPLimits{IdleTimeout: time.Minute})
	client := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	streams := make([]*rawStream, 8)
	for index := range streams {
		streams[index] = openRawUDPStream(t, client)
	}
	if stats := server.service.UDPStats(); stats.Associations != 0 || stats.QueuedBytes != 0 {
		t.Fatalf("empty streams hold budget: %+v", stats)
	}
	for _, stream := range streams {
		stream.close()
	}
	waitForUDPStats(t, server.service, "the release of the idle streams", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
}

// TestUDPAuthenticationFailureHoldsNoBudget checks the rejected credentials
// before the UDP path: a 407 never creates a session, an association or a
// queued byte, and a valid client still works afterwards.
func TestUDPAuthenticationFailureHoldsNoBudget(t *testing.T) {
	handler := &testHandler{}
	server := startTestService(t, ProtocolHTTP2, handler, UDPLimits{IdleTimeout: time.Minute})
	rejected := server.newClient(t, ProtocolHTTP2, "wrong", &testDialer{}, nil)
	for i := 0; i < 4; i++ {
		_, err := rejected.ListenPacket(testContext(t))
		if !IsAuthenticationRequired(err) {
			t.Fatalf("attempt %d: got %v, want a rejected password", i, err)
		}
	}
	if stats := server.service.UDPStats(); stats.Associations != 0 || stats.QueuedBytes != 0 {
		t.Fatalf("a rejected password held budget: %+v", stats)
	}
	accepted := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	packetConn, err := accepted.ListenPacket(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	destination := udpDestinations(1)[0]
	writeDatagram(t, packetConn, []byte("payload"), destination)
	if reply := readDatagram(t, packetConn, 5*time.Second); reply.err != nil || reply.source != destination {
		t.Fatalf("valid client after the rejections: %v %s", reply.err, reply.source)
	}
	packetConn.Close()
	waitForUDPStats(t, server.service, "the final release", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
}

// TestUDPConcurrentShutdown starts streams and associations, keeps writing while
// the service closes, and checks that the accounting reaches rest.
func TestUDPConcurrentShutdown(t *testing.T) {
	handler := &testHandler{}
	server := startTestService(t, ProtocolHTTP2, handler, UDPLimits{IdleTimeout: time.Minute})
	destinations := udpDestinations(3)
	var conns []*PacketConn
	for i := 0; i < 3; i++ {
		client := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
		packetConn, err := client.ListenPacket(testContext(t))
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, packetConn)
		for _, destination := range destinations {
			writeDatagram(t, packetConn, []byte("payload"), destination)
		}
	}
	waitForUDPStats(t, server.service, "the associations", func(stats UDPStats) bool {
		return stats.Associations == len(conns)*len(destinations)
	})
	var group sync.WaitGroup
	for _, packetConn := range conns {
		group.Add(1)
		go func(packetConn *PacketConn) {
			defer group.Done()
			payload := make([]byte, 1024)
			for i := 0; i < 50; i++ {
				_, _ = packetConn.WriteTo(payload, destinations[i%len(destinations)].UDPAddr())
			}
		}(packetConn)
	}
	time.Sleep(10 * time.Millisecond)
	server.service.Close()
	group.Wait()
	waitForUDPStats(t, server.service, "the release after shutdown", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
	for _, packetConn := range conns {
		packetConn.Close()
	}
}

// TestUDPIdleAssociationExpiry keeps an association quiet past the idle window:
// the sweeper closes it, returns its slot and the next 4-tuple is admitted.
func TestUDPIdleAssociationExpiry(t *testing.T) {
	limits := UDPLimits{
		SessionAssociations: 1,
		ServiceAssociations: 8,
		SessionQueuedBytes:  1 << 20,
		ServiceQueuedBytes:  1 << 20,
		IdleTimeout:         60 * time.Millisecond,
	}
	handler := &testHandler{}
	server := startTestService(t, ProtocolHTTP2, handler, limits)
	client := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	packetConn, err := client.ListenPacket(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	destinations := udpDestinations(2)
	writeDatagram(t, packetConn, []byte("payload"), destinations[0])
	if reply := readDatagram(t, packetConn, 5*time.Second); reply.err != nil {
		t.Fatal(reply.err)
	}
	waitForUDPStats(t, server.service, "the association", func(stats UDPStats) bool {
		return stats.Associations == 1
	})
	// No traffic in either direction: the sweeper returns the slot.
	waitForUDPStats(t, server.service, "the idle expiry", func(stats UDPStats) bool {
		return stats.Associations == 0
	})
	// The slot is usable again; the stream itself is still open.
	writeDatagram(t, packetConn, []byte("payload"), destinations[1])
	reply := readDatagram(t, packetConn, 5*time.Second)
	if reply.err != nil || reply.source != destinations[1] {
		t.Fatalf("association after expiry: %v %s", reply.err, reply.source)
	}
	packetConn.Close()
	waitForUDPStats(t, server.service, "the final release", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
}

// TestUDPAssociationReturnedWhenHandlerCloses makes the handler close every
// association without reading it: the slot must come back each time, so the
// stream can present many 4-tuples one after another with a limit of one.
func TestUDPAssociationReturnedWhenHandlerCloses(t *testing.T) {
	limits := UDPLimits{
		SessionAssociations: 1,
		ServiceAssociations: 4,
		SessionQueuedBytes:  1 << 20,
		ServiceQueuedBytes:  1 << 20,
		IdleTimeout:         time.Minute,
	}
	handler := &rejectHandler{}
	server := startTestService(t, ProtocolHTTP2, handler, limits)
	client := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	packetConn, err := client.ListenPacket(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	destinations := udpDestinations(8)
	for index, destination := range destinations {
		writeDatagram(t, packetConn, []byte("payload"), destination)
		// The handler closes the association, which returns the slot before the
		// next 4-tuple arrives.
		waitFor(t, "the association to be returned", func() bool {
			stats := server.service.UDPStats()
			return stats.RefusedAssociations == 0 &&
				stats.Associations == 0 &&
				int(handler.accepted.Load()) == index+1
		})
	}
	if accepted := handler.accepted.Load(); accepted != int32(len(destinations)) {
		t.Fatalf("%d associations reached the handler, want %d", accepted, len(destinations))
	}
	packetConn.Close()
	waitForUDPStats(t, server.service, "the final release", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
}
