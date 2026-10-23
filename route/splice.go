package route

import (
	"context"
	"io"
	"net"
	"net/netip"
	"slices"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// Spliced-stream liveness.
//
// # The hole this closes
//
// A handover that sing-tun accepts takes the connection out of userspace: `spliceConnection` returns
// true and NewConnection returns before the copy loops start. That is the point of the feature, and it
// is also why the half-close observation (conn_halfclose.go) never arms for these flows: the observer
// is started by a copy direction finishing, and no copy direction exists.
//
// For a DIRECT TCP flow with tracking on - the only shape that can be handed over, because the target
// must be a kernel socket - the remaining liveness signal was the userspace stack's own keepalive,
// which ships with `idle = tcpEstablishedTimeout` (2h04m), interval 75s and count 9. A peer that
// finished sending and then disappeared was therefore noticed three hours later, if at all; the
// connection stayed in the traffic table and the connection list in the meantime, and only an explicit
// close-all cleared it. Proxy flows never show this: their outbound stream is not a syscall.Conn, the
// handover is refused, and the copy path's observation covers them in under two minutes.
//
// # Why a keepalive and not the copy observer
//
// The observer works by reading the finished side. After a handover the userspace stack's bytes move
// between descriptors in the kernel, so a second reader would either steal stream data or fight the
// handover - exactly the "two managers on one socket" failure this code must not introduce. A
// keepalive needs no reader: it configures the TCP state machine that already owns the socket, and the
// stack itself then detaches the connection when the peer stops answering, which runs the SAME release
// path as a normal end (`detachConn` -> `spliceDetach` -> `spliceRelease` -> `OnClose` -> the tracker's
// close). No new timer, no second owner, no bytes touched.
//
// # What the timings mean
//
// A spliced flow is not known to be half-closed when the handover happens, so the idle cannot be the
// half-close observer's 20s: that would probe every idle Direct connection. Two minutes of complete
// silence is not a working connection, and three unanswered probes thirty seconds apart give the
// verdict. The bound is therefore 2m + 3*30s = 3.5 minutes after the peer stops answering - an order of
// magnitude below the platform default it replaces, and the same order as the copy path's own bound.
// A live peer answers a probe in its kernel without waking the application, so a healthy long-poll is
// not disturbed, and a suspended peer whose socket still exists keeps its connection, which is
// correct.
const (
	// spliceKeepAliveIdle is how long a handed-over stream must be completely silent before the
	// userspace stack starts probing the peer's kernel.
	spliceKeepAliveIdle = 2 * time.Minute
	// spliceKeepAliveInterval is the gap between unanswered probes.
	spliceKeepAliveInterval = 30 * time.Second
	// spliceKeepAliveCount is how many unanswered probes mean the peer is gone. The stack raises
	// ETIMEDOUT and detaches the connection, which releases the flow.
	spliceKeepAliveCount = 3
)

// spliceKeepAliveOptions carries the production timings with a test override for each, the same
// zero-means-constant convention as halfCloseOptions.
type spliceKeepAliveOptions struct {
	idle     time.Duration
	interval time.Duration
}

func (m *ConnectionManager) spliceKeepAliveTimings() (idle time.Duration, interval time.Duration) {
	timings := m.spliceKeepAlive
	idle = timings.idle
	if idle <= 0 {
		idle = spliceKeepAliveIdle
	}
	interval = timings.interval
	if interval <= 0 {
		interval = spliceKeepAliveInterval
	}
	return idle, interval
}

// armSplicedLiveness gives a handed-over stream the only liveness signal userspace still owns.
//
// Called after the handover is confirmed and never before: a refused handover falls back to the copy
// loops, which arm the half-close observer instead. Returns whether the configuration was accepted;
// a refusal is logged and otherwise ignored, because a connection that cannot be configured is still
// better served by the stack's default keepalive than by being closed here.
func (m *ConnectionManager) armSplicedLiveness(ctx context.Context, target keepAliveConfigurable) bool {
	if target == nil {
		return false
	}
	idle, interval := m.spliceKeepAliveTimings()
	err := target.SetKeepAliveConfig(net.KeepAliveConfig{
		Enable:   true,
		Idle:     idle,
		Interval: interval,
		Count:    spliceKeepAliveCount,
	})
	if err != nil {
		m.logHalfClose(ctx, "spliced flow: keepalive unavailable, liveness stays with the stack default: ", err)
		return false
	}
	m.spliceLivenessArmed.Add(1)
	return true
}

type spliceTarget struct {
	socket        tun.SpliceSocket
	offload       N.PacketOffload
	readCounters  []N.CountFunc
	writeCounters []N.CountFunc
}

func unwrapSpliceTarget(conn any, allowOffload bool) (spliceTarget, bool) {
	// Delegates to the reason-aware variant so there is a single implementation of
	// the walk. Keeping two copies would let the reason threading drift from the
	// decision it is supposed to describe.
	target, _, ok := unwrapSpliceTargetWithReason(conn, allowOffload)
	return target, ok
}

func (m *ConnectionManager) spliceClose(ctx context.Context, conn io.Closer, remote io.Closer, onClose N.CloseHandlerFunc) N.CloseHandlerFunc {
	return func(err error) {
		if err == nil {
			m.logger.DebugContext(ctx, "connection finished")
		} else if !E.IsClosedOrCanceled(err) {
			m.logger.ErrorContext(ctx, "connection closed: ", err)
		} else {
			m.logger.TraceContext(ctx, "connection closed")
		}
		if onClose != nil {
			onClose(err)
		}
		common.Close(conn, remote)
	}
}

// drainCachedSource moves the bytes a wrapper has already accepted from the TUN into the outbound
// before a handover is attempted. It returns the unwrapped reader, the counters it accumulated and
// the first write error.
//
// A write error here is terminal for the connection, not a fallback: the bytes that were already
// handed to the kernel socket cannot be replayed through the copy loops, and the caller closes the
// connection instead. The extraction exists so that failure is reachable from a unit test without a
// real TUN stack; the behaviour is unchanged.
func drainCachedSource(source io.Reader, remote io.Writer) (io.Reader, []N.CountFunc, error) {
	var readCounters []N.CountFunc
	for {
		source, readCounters = N.UnwrapCountReader(source, readCounters)
		cachedReader, isCached := source.(N.CachedReader)
		if !isCached {
			break
		}
		buffer := cachedReader.ReadCached()
		if buffer == nil {
			break
		}
		dataLen := buffer.Len()
		_, err := remote.Write(buffer.Bytes())
		buffer.Release()
		if err != nil {
			return source, readCounters, err
		}
		for _, counter := range readCounters {
			counter(int64(dataLen))
		}
	}
	return source, readCounters, nil
}

func (m *ConnectionManager) spliceConnection(ctx context.Context, conn net.Conn, remoteConn net.Conn, onClose N.CloseHandlerFunc) (bool, error) {
	// Every path out of this function records exactly one outcome, and the caller records
	// the one case in which this function is not called at all. That is what makes
	// Attempts == Accepted + sum(Reasons) hold for the stream diagnostics without a second
	// counter, and it is why the recording sits at the returns rather than in the caller.
	destination, writeCounters := N.UnwrapCountWriter(conn, nil)
	goConn, isGoConn := N.CastWriter[*tun.GoConn](destination)
	if !isGoConn {
		// Not a TUN stream: the connection is proxied in userspace by design, so there is
		// nothing that could be handed to a socket.
		m.tcpSpliceDiagnostics.recordOutcome(spliceReasonSourceNotGoConn)
		return false, nil
	}
	target, targetReason, isTarget := unwrapSpliceTargetWithReason(remoteConn, false)
	if !isTarget {
		// The outbound side refused, at one of the points the classifier can name. The
		// reason is used as recorded: it is produced by the same walk the decision uses.
		m.tcpSpliceDiagnostics.recordOutcome(tcpSpliceTargetReason(targetReason))
		return false, nil
	}
	source, readCounters, err := drainCachedSource(conn, remoteConn)
	if err != nil {
		// Data that had to be forwarded before the handover could not be. The
		// connection is finished by the caller rather than falling back, so this is a
		// terminal outcome and not a rejection.
		m.tcpSpliceDiagnostics.recordOutcome(spliceReasonCachedWriteFailed)
		return false, err
	}
	goReader, isGoReader := N.CastReader[*tun.GoConn](source)
	if !isGoReader || goReader != goConn {
		// The reader and the writer do not resolve to the same TUN stream, so splicing
		// would join one connection's download to another's upload.
		m.tcpSpliceDiagnostics.recordOutcome(spliceReasonSourceReaderWriterMismatch)
		return false, nil
	}
	if goConn.Splice(target.socket, tun.SpliceOptions{
		ReadCounters:  append(readCounters, target.writeCounters...),
		WriteCounters: append(target.readCounters, writeCounters...),
		OnClose:       m.spliceClose(ctx, conn, remoteConn, onClose),
	}) {
		m.tcpSpliceDiagnostics.recordOutcome(spliceReasonHandoverAccepted)
		// Bytes now move between descriptors in the kernel, so nothing in userspace will observe
		// this connection again. Recorded at the confirmed handover and nowhere else - which is
		// also the exact limit of the claim: the handover was accepted, and what the kernel does
		// with it afterwards is not observable here.
		if state := managedConnStateOf(remoteConn); state != nil {
			state.markKernelOwned()
		}
		// Userspace will never read this connection again, so the half-close observer cannot see
		// it. The stack's own keepalive is the only remaining way to learn that the peer is gone;
		// arm it with the splice timings rather than the two-hour platform default.
		m.armSplicedLiveness(ctx, goConn)
		return true, nil
	}
	// Splice declined and returns a bare bool. Its false paths inside sing-tun include the
	// platform lacking socket support, a stream that is not established, one that is already
	// spliced or has a handover pending, owner.Attach being refused (typically an existing
	// owner), and socket conversion failing. None of that is observable from here, so this is
	// recorded as one outcome rather than guessed at - the packet path documents the same
	// decision for the same reason.
	m.tcpSpliceDiagnostics.recordOutcome(spliceReasonSpliceRejected)
	return false, nil
}

type spliceSource struct {
	natConn       *tun.UDPNatConn
	cachedReaders []N.CachedPacketReader
	readCounters  []N.CountFunc
	writeCounters []N.CountFunc
}

func unwrapSpliceSource(conn N.PacketConn) (spliceSource, bool) {
	// Delegates to the reason-aware variant; see unwrapSpliceTarget above.
	source, _, ok := unwrapSpliceSourceWithReason(conn)
	return source, ok
}

func (s *spliceSource) takeCached() []*N.PacketBuffer {
	var cached []*N.PacketBuffer
	for _, cachedReader := range s.cachedReaders {
		packet := cachedReader.ReadCachedPacket()
		if packet == nil {
			continue
		}
		if packet.Buffer == nil {
			N.PutPacketBuffer(packet)
			continue
		}
		cached = append(cached, packet)
	}
	return cached
}

func (m *ConnectionManager) splicePacketConnection(ctx context.Context, conn N.PacketConn, remote any, metadata *adapter.InboundContext, destinationAddress netip.Addr, udpTimeout time.Duration, onClose N.CloseHandlerFunc) (N.PacketConn, bool) {
	var nat tun.PacketNAT
	source := conn
	fakeIPConn, isFakeIP := conn.(*fakeIPNATPacketConn)
	if isFakeIP {
		source = fakeIPConn.NetPacketConn
	}
	// The session records exactly one outcome, once, when its decision is final. See
	// spliceDiagnostics: attempts and acceptances are derived from this same array, so a
	// second write here would both cost another atomic on the session path and break the
	// invariant Attempts == Accepted + sum(Reasons).
	spliceSource, sourceReason, isSpliceSource := unwrapSpliceSourceWithReason(source)
	if !isSpliceSource {
		m.spliceDiagnostics.recordOutcome(sourceReason)
		return conn, false
	}
	target, targetReason, isTarget := unwrapSpliceTargetWithReason(remote, true)
	if !isTarget {
		m.spliceDiagnostics.recordOutcome(targetReason)
		return conn, false
	}
	if destinationAddress.IsValid() {
		nat.Destination = M.SocksaddrFrom(destinationAddress, metadata.Destination.Port)
	} else {
		nat.Destination = metadata.Destination
	}
	switch {
	case isFakeIP:
		nat.Origin = metadata.OriginDestination
		nat.FakeIP = true
	case metadata.RouteOriginalDestination.IsValid() && metadata.RouteOriginalDestination != metadata.Destination:
		nat.Origin = metadata.RouteOriginalDestination
	case destinationAddress.IsValid() && metadata.Destination.IsIP():
		nat.Origin = metadata.Destination
	}
	nat.Unidirectional = !isFakeIP && metadata.UDPDisableDomainUnmapping && !metadata.Destination.IsIP()
	cached := spliceSource.takeCached()
	if spliceSource.natConn.Splice(target.socket, tun.SplicePacketOptions{
		SpliceOptions: tun.SpliceOptions{
			ReadCounters:  append(spliceSource.readCounters, target.writeCounters...),
			WriteCounters: append(target.readCounters, spliceSource.writeCounters...),
			OnClose:       m.spliceClose(ctx, conn, remote.(io.Closer), onClose),
		},
		Timeout:       udpTimeout,
		NAT:           nat,
		Cached:        cached,
		Offload:       target.offload,
		FrontHeadroom: N.CalculateFrontHeadroom(remote),
		RearHeadroom:  N.CalculateRearHeadroom(remote),
	}) {
		// The socket was found and the handover was accepted; packets now bypass userspace.
		m.spliceDiagnostics.recordOutcome(spliceReasonHandoverAccepted)
		// As on the stream path: confirmed handover, so userspace will never see this flow's
		// activity again and its silence must not be read as idleness.
		if state := managedConnStateOf(remote); state != nil {
			state.markKernelOwned()
		}
		return conn, true
	}
	// Splice() declined, and it returns a bare bool. Its false paths inside sing-tun
	// include platform support, NAT expressibility, owner.Attach being refused, socket
	// conversion and family mismatch - none of which is observable from here, so
	// inferring which one happened would mean guessing from a single fact.
	//
	// An earlier version split this into attach_failed and platform_unsupported based on
	// whether a socket was present. That distinction is not real: a socket is always
	// present whenever this line is reached, so the split could only ever have reported
	// one of the two, and it would have been wrong about the cause.
	m.spliceDiagnostics.recordOutcome(spliceReasonSpliceRejected)
	if len(cached) == 0 {
		return conn, false
	}
	for _, packet := range slices.Backward(cached) {
		for _, counter := range spliceSource.readCounters {
			counter(int64(packet.Buffer.Len()))
		}
		source = bufio.NewCachedPacketConn(source, packet.Buffer, packet.Destination)
		N.PutPacketBuffer(packet)
	}
	if isFakeIP {
		fakeIPConn.NetPacketConn = bufio.NewNetPacketConn(source)
		return conn, false
	}
	return source, false
}

func packetTimeout(metadata *adapter.InboundContext) time.Duration {
	if metadata.UDPTimeout > 0 {
		return metadata.UDPTimeout
	}
	protocol := metadata.Protocol
	if protocol == "" {
		protocol = C.PortProtocols[metadata.Destination.Port]
	}
	if protocol != "" {
		return C.ProtocolTimeouts[protocol]
	}
	return 0
}
