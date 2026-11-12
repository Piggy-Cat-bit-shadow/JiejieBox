package route

// Q17/Q35: the packet sniff must not read a transport it cannot bound.
//
// # The failure this pins
//
// When the staged first datagram is not enough to identify the protocol (a
// fragmented QUIC ClientHello is the real case), the packet sniff reads further
// datagrams from the inbound's connection while a read deadline bounds the wait.
// The deadline is set with SetReadDeadline, and its error is deliberately not
// fatal here because a timeout is a normal sniff outcome.
//
// Some transports cannot take a read deadline at all and say so: they implement
// deadline.WithoutReadDeadline and report true ("the caller must supply its own
// bound"), with SetReadDeadline returning os.ErrInvalid. Trojan's and naive's
// inbound packet conns are that shape today. On such a conn the read has NO
// bound, and the first datagram - already staged in `packetBuffers` and only
// replayed when this function returns - is held for as long as the flow lives
// (the association's idle window, two minutes by default). The flow does not just
// lose the sniff, it stops delivering.
//
// The fix is the same rule the rest of the tree applies to this declaration:
// honour it. The sniff replays what was staged and reports that it stopped.
//
// # Why this test is deterministic
//
// The fake conn's ReadPacket blocks until the test releases it, and the context
// deadline is long (2s). Without the guard the call can only return by canceling
// the context at that deadline, so a one-second bound separates the two
// behaviours without a sleep-based race.

import (
	"context"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/sniff"
	"github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// unboundedPacketConn declares that it needs an external read deadline and then
// blocks forever in ReadPacket: a transport whose SetReadDeadline is a no-op
// error. The declaration is what the production transports (trojan, naive) make.
type unboundedPacketConn struct {
	reads     atomic.Int32
	unblock   chan struct{}
	readStart chan struct{}
}

func newUnboundedPacketConn() *unboundedPacketConn {
	return &unboundedPacketConn{
		unblock:   make(chan struct{}),
		readStart: make(chan struct{}, 1),
	}
}

func (c *unboundedPacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	c.reads.Add(1)
	select {
	case c.readStart <- struct{}{}:
	default:
	}
	<-c.unblock
	return M.Socksaddr{}, io.EOF
}

func (c *unboundedPacketConn) WritePacket(*buf.Buffer, M.Socksaddr) error { return nil }
func (c *unboundedPacketConn) Close() error                               { return nil }
func (c *unboundedPacketConn) LocalAddr() net.Addr                        { return &net.UDPAddr{} }
func (c *unboundedPacketConn) SetDeadline(time.Time) error                { return os.ErrInvalid }
func (c *unboundedPacketConn) SetReadDeadline(time.Time) error            { return os.ErrInvalid }
func (c *unboundedPacketConn) SetWriteDeadline(time.Time) error           { return os.ErrInvalid }
func (c *unboundedPacketConn) NeedAdditionalReadDeadline() bool           { return true }

// TestFragmentedPacketSniffDoesNotWaitOnATransportWithoutAReadDeadline is the
// regression: the staged datagram is replayed, no unbounded read is started, and
// the call returns without waiting for the flow to die.
func TestFragmentedPacketSniffDoesNotWaitOnATransportWithoutAReadDeadline(t *testing.T) {
	previousDefaults := defaultPacketSniffers
	defaultPacketSniffers = []sniff.PacketSniffer{
		func(context.Context, *adapter.InboundContext, []byte) error {
			return sniff.ErrNeedMoreData
		},
	}
	defer func() { defaultPacketSniffers = previousDefaults }()

	recorder := newSniffLogRecorder()
	router := &Router{logger: recorder}
	firstDatagram := []byte("a-fragment-that-needs-the-rest")
	stagedBuffer := buf.NewPacket()
	_, err := stagedBuffer.Write(firstDatagram)
	require.NoError(t, err)
	// The buffer ownership belongs to the caller in this shape: the sniff returns
	// no buffers, so the staged one is released here exactly as the route caller
	// does after the replay.
	defer stagedBuffer.Release()
	staged := N.NewPacketBuffer()
	defer N.PutPacketBuffer(staged)
	*staged = N.PacketBuffer{Buffer: stagedBuffer, Destination: M.ParseSocksaddrHostPort("203.0.113.9", 443)}

	conn := newUnboundedPacketConn()
	defer close(conn.unblock)
	// No FakeIP domain and no sniffed domain: this is the plain unknown flow, so
	// the sniff really wants more datagrams and reaches the read loop.
	metadata := adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("203.0.113.9", 443)}
	action := &rule.RuleActionSniff{}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	type result struct {
		packetBuffers []*N.PacketBuffer
		err           error
	}
	done := make(chan result, 1)
	started := time.Now()
	go func() {
		_, packetBuffers, sniffErr := router.actionSniff(ctx, &metadata, action, nil, conn, nil, []*N.PacketBuffer{staged})
		done <- result{packetBuffers: packetBuffers, err: sniffErr}
	}()
	var outcome result
	select {
	case outcome = <-done:
	case <-time.After(time.Second):
		t.Fatal("the sniff waited on a transport that declared it cannot take a read deadline; the staged first datagram is parked with it")
	}
	elapsed := time.Since(started)
	require.Less(t, elapsed, time.Second)
	require.Equal(t, int32(0), conn.reads.Load(),
		"an unbounded read was started on a conn that reports it needs an external deadline")
	require.Empty(t, outcome.packetBuffers, "the sniff must not return buffers it never read")
	// A bounded sniff that could not identify the protocol is not fatal: the flow
	// must still be routed with the evidence it has.
	require.NoError(t, outcome.err)
	stopped := false
	for _, line := range recorder.recorded() {
		if strings.Contains(line, "no usable read deadline") {
			stopped = true
		}
	}
	require.True(t, stopped, "the stop must be observable in the log, not silent: %v", recorder.recorded())
}

// TestBoundedPacketSniffStillReadsWhenTheTransportSupportsADeadline is the
// contrast: a conn that does NOT declare the need is read, because a bound
// exists (its own deadline, or the caller's). The root router must not stop
// sniffing every transport as a side effect of the guard.
func TestBoundedPacketSniffStillReadsWhenTheTransportSupportsADeadline(t *testing.T) {
	previousDefaults := defaultPacketSniffers
	defaultPacketSniffers = []sniff.PacketSniffer{
		func(context.Context, *adapter.InboundContext, []byte) error {
			return sniff.ErrNeedMoreData
		},
	}
	defer func() { defaultPacketSniffers = previousDefaults }()

	router := &Router{logger: newSniffLogRecorder()}
	firstDatagram := buf.NewPacket()
	_, err := firstDatagram.Write([]byte("first"))
	require.NoError(t, err)
	defer firstDatagram.Release()
	staged := N.NewPacketBuffer()
	defer N.PutPacketBuffer(staged)
	*staged = N.PacketBuffer{Buffer: firstDatagram, Destination: M.ParseSocksaddrHostPort("203.0.113.9", 443)}

	// oneDatagramPacketConn (fragmented_quic_known_domain_test.go) does not
	// declare the need and returns EOF on the second read: exactly one extra read
	// is attempted, which is what a bounded sniff is allowed to do.
	conn := &oneDatagramPacketConn{packet: []byte("second")}
	metadata := adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("203.0.113.9", 443)}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, packetBuffers, err := router.actionSniff(ctx, &metadata, &rule.RuleActionSniff{}, nil, conn, nil, []*N.PacketBuffer{staged})
	require.NoError(t, err)
	require.Equal(t, 1, conn.reads, "a transport with a deadline must still be read")
	require.Len(t, packetBuffers, 1, "the extra datagram must be retained for replay")
	N.ReleaseMultiPacketBuffer(packetBuffers)
}
