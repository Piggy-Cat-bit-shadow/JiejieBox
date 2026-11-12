package trusttunnel

// Q35 / Q17: the server-side _udp2 association honours a read deadline.
//
// # Why this is a product requirement and not test scaffolding
//
// The association is handed to the router as an N.PacketConn, and the router's
// fragmented-QUIC sniff reads further datagrams while bounded by
// SetReadDeadline. Before this deadline existed, SetReadDeadline returned
// os.ErrInvalid and the route layer deliberately does not treat that error as
// fatal, so the read had no bound at all: a fragmented ClientHello parked the
// FIRST datagram - already staged for replay - for the life of the association
// (two minutes by default). The DNS reader and the health probes take the same
// path.
//
// # What the declaration means
//
// deadline.NeedAdditionalReadDeadline(conn) is the contract consumers use to
// decide between "use the real deadline" and "run your own timer". This flow
// honours the deadline now, so it must report false; a stale true would tell
// every consumer to bypass the deadline that works.

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio/deadline"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// newDeadlineFlowFixture builds one session with one association directly, so
// the test drives the same methods the service drives without an HTTP round trip
// per cell.
func newDeadlineFlowFixture(t *testing.T, limits UDPLimits) *udpFlow {
	t.Helper()
	service, err := NewService(ServiceOptions{
		Handler:   &rejectHandler{},
		Users:     []auth.User{{Username: testUsername, Password: testPassword}},
		UDPLimits: limits,
	})
	require.NoError(t, err)
	t.Cleanup(func() { service.Close() })
	destination := udpDestinations(1)[0]
	session := &udpSession{
		service: service,
		ctx:     context.Background(),
		flows:   make(map[udpFlowKey]*udpFlow),
		done:    make(chan struct{}),
	}
	flow := &udpFlow{
		session:     session,
		key:         udpFlowKey{source: M.Socksaddr{}.AddrPort(), destination: destination.AddrPort()},
		source:      M.Socksaddr{},
		destination: destination,
		packets:     make(chan *buf.Buffer, udpFlowQueueSize),
		done:        make(chan struct{}),
	}
	require.True(t, service.udp.acquireAssociation())
	session.flows[flow.key] = flow
	t.Cleanup(func() { flow.Close() })
	return flow
}

// queueDatagram pushes one datagram through the same admission path a frame
// takes, so the queued bytes are accounted for like production traffic.
func queueDatagram(t *testing.T, flow *udpFlow, payload string) {
	t.Helper()
	require.True(t, flow.session.reserveBytesLocked(len(payload)), "the datagram was refused by the byte budget")
	data := buf.NewSize(len(payload))
	_, err := data.Write([]byte(payload))
	require.NoError(t, err)
	pushed, closed := flow.push(data)
	require.True(t, pushed, "the datagram was not queued (closed=%v)", closed)
}

// TestUDPFlowReadDeadlineIsHonouredAndDeclared is the deadline contract.
func TestUDPFlowReadDeadlineIsHonouredAndDeclared(t *testing.T) {
	flow := newDeadlineFlowFixture(t, UDPLimits{IdleTimeout: time.Minute})
	var _ N.PacketConn = flow
	var _ N.FrontHeadroom = flow

	// The declaration consumers act on. It is read through the same helper the
	// router uses, so a change to one without the other cannot pass.
	require.False(t, deadline.NeedAdditionalReadDeadline(flow),
		"the flow honours SetReadDeadline, so it must not ask callers for their own timer")
	require.NoError(t, flow.SetReadDeadline(time.Time{}))
	// SetDeadline reports that the write half is unsupported, but it must still
	// apply the read half: the tree's DNS reader ignores this error and relies on
	// the ensuing read being bounded.
	require.ErrorIs(t, flow.SetDeadline(time.Now().Add(-time.Second)), os.ErrInvalid,
		"SetDeadline must say the write half is unsupported instead of silently ignoring it")
	_, err := flow.ReadPacket(buf.NewSize(MaxPayloadSize))
	require.ErrorIs(t, err, os.ErrDeadlineExceeded,
		"SetDeadline's read half was not applied, so a caller ignoring its error reads unbounded")
	require.NoError(t, flow.SetReadDeadline(time.Time{}))
	require.ErrorIs(t, flow.SetWriteDeadline(time.Now()), os.ErrInvalid)

	// A deadline in the past returns at once, with the timeout error a caller
	// already has to handle.
	require.NoError(t, flow.SetReadDeadline(time.Now().Add(-time.Second)))
	started := time.Now()
	_, err = flow.ReadPacket(buf.NewSize(MaxPayloadSize))
	require.ErrorIs(t, err, os.ErrDeadlineExceeded)
	require.Less(t, time.Since(started), time.Second, "a past deadline must not wait")

	// A future deadline with nothing queued waits for the deadline, then times
	// out: this is the branch the router relies on.
	require.NoError(t, flow.SetReadDeadline(time.Now().Add(120*time.Millisecond)))
	started = time.Now()
	_, err = flow.ReadPacket(buf.NewSize(MaxPayloadSize))
	elapsed := time.Since(started)
	require.ErrorIs(t, err, os.ErrDeadlineExceeded)
	require.GreaterOrEqual(t, elapsed, 100*time.Millisecond, "the read returned before its deadline")
	require.Less(t, elapsed, 2*time.Second, "the read did not return at its deadline")

	// The deadline persists until it is changed, exactly as net.PacketConn
	// documents: a second read after the timeout still returns the timeout,
	// which is why the sniff loop resets it after every round.
	_, err = flow.ReadPacket(buf.NewSize(MaxPayloadSize))
	require.ErrorIs(t, err, os.ErrDeadlineExceeded)

	// The timeout must not damage the association: once the deadline is cleared
	// (what the sniff loop does after its read) the datagram that arrives
	// afterwards is delivered, and its bytes are returned to the budget.
	require.NoError(t, flow.SetReadDeadline(time.Time{}))
	queueDatagram(t, flow, "after-timeout")
	buffer := buf.NewSize(MaxPayloadSize)
	_, err = flow.ReadPacket(buffer)
	require.NoError(t, err)
	require.Equal(t, "after-timeout", string(buffer.Bytes()))
	buffer.Release()
	require.Zero(t, flow.session.queuedBytes, "the delivered datagram's bytes were not released")
	require.EqualValues(t, 1, flow.session.service.udp.stats().DatagramsDelivered)

	// A cleared deadline means "wait", which is the ordinary handler read. The
	// zero time is the reset net.PacketConn documents.
	require.NoError(t, flow.SetReadDeadline(time.Time{}))
	result := make(chan error, 1)
	go func() {
		readBuffer := buf.NewSize(MaxPayloadSize)
		defer readBuffer.Release()
		_, readErr := flow.ReadPacket(readBuffer)
		result <- readErr
	}()
	select {
	case err = <-result:
		t.Fatalf("a cleared deadline returned %v instead of waiting", err)
	case <-time.After(150 * time.Millisecond):
	}
	queueDatagram(t, flow, "waited")
	select {
	case err = <-result:
		require.NoError(t, err)
	case <-time.After(4 * time.Second):
		t.Fatal("the datagram queued under a cleared deadline never reached the reader")
	}

	// A close still unblocks a bounded read: the deadline and the done channel
	// are separate exits and Close wins over a long deadline.
	require.NoError(t, flow.SetReadDeadline(time.Now().Add(time.Hour)))
	blocked := make(chan error, 1)
	go func() {
		readBuffer := buf.NewSize(MaxPayloadSize)
		defer readBuffer.Release()
		_, readErr := flow.ReadPacket(readBuffer)
		blocked <- readErr
	}()
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, flow.Close())
	select {
	case err = <-blocked:
		require.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(4 * time.Second):
		t.Fatal("Close did not unblock a read waiting on a long deadline")
	}
}

// TestUDPFlowReadDeadlineIsUsedByTheSniffLoop is the production consumer: the
// router's fragmented-QUIC sniff sets a read deadline and expects the conn to
// stop waiting at it. This pins the semantics the route layer depends on
// without rebuilding the router: a bounded read returns a timeout, and the
// staged first datagram is therefore never parked forever.
func TestUDPFlowReadDeadlineIsUsedByTheSniffLoop(t *testing.T) {
	flow := newDeadlineFlowFixture(t, UDPLimits{IdleTimeout: time.Minute})
	// C.ReadPayloadTimeout is what the router passes when the action sets none.
	require.NoError(t, flow.SetReadDeadline(time.Now().Add(200*time.Millisecond)))
	started := time.Now()
	_, err := flow.ReadPacket(buf.NewSize(MaxPayloadSize))
	require.ErrorIs(t, err, os.ErrDeadlineExceeded)
	require.Less(t, time.Since(started), 5*time.Second)
	// The deadline is one-shot in the same way net.PacketConn's is: a later read
	// with no new deadline is an ordinary blocking read.
	require.NoError(t, flow.SetReadDeadline(time.Time{}))
	require.True(t, flow.session.service.udp.stats().Associations == 1,
		"the association was dropped by a read timeout")
}
