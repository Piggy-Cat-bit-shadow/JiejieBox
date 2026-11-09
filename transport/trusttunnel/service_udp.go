package trusttunnel

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing/common/buf"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// udpFlowQueueSize bounds the datagrams queued for one flow; beyond it new
// datagrams are dropped, as a full socket buffer would.
const udpFlowQueueSize = 64

type udpFlowKey struct {
	source      netip.AddrPort
	destination netip.AddrPort
}

// udpSession is one _udp2 stream: a frame reader and a table of flows keyed by
// the frame 4-tuple.
type udpSession struct {
	service *Service
	ctx     context.Context
	writer  http.ResponseWriter
	user    string
	source  M.Socksaddr

	access      sync.Mutex
	flows       map[udpFlowKey]*udpFlow
	queuedBytes int
	closed      bool

	writeAccess sync.Mutex
	writeClosed bool

	// refused and dropped count this stream's admission refusals for the
	// close-time diagnostic line.
	refused atomic.Int64
	dropped atomic.Int64

	// done ends the idle sweeper.
	done chan struct{}
}

func (s *Service) serveUDP(ctx context.Context, writer http.ResponseWriter, request *http.Request, username string, source M.Socksaddr) {
	writer.WriteHeader(http.StatusOK)
	err := flush(writer)
	if err != nil {
		return
	}
	s.logger.DebugContext(ctx, "trusttunnel: UDP stream from ", source)
	session := &udpSession{
		service: s,
		ctx:     ctx,
		writer:  writer,
		user:    username,
		source:  source,
		flows:   make(map[udpFlowKey]*udpFlow),
		done:    make(chan struct{}),
	}
	go session.expireIdle()
	err = session.loop(request.Body)
	session.close()
	if refused, dropped := session.refused.Load(), session.dropped.Load(); refused > 0 || dropped > 0 {
		s.logger.DebugContext(ctx, "trusttunnel: UDP stream from ", source, " ended with ", refused, " associations refused and ", dropped, " datagrams dropped")
	}
	switch {
	case err == nil, err == io.EOF:
	case isStreamClosed(err), E.IsClosedOrCanceled(err):
		s.logger.DebugContext(ctx, "trusttunnel: UDP stream from ", source, " closed: ", err)
	default:
		s.logger.ErrorContext(ctx, "trusttunnel: UDP stream from ", source, ": ", err)
	}
}

func (s *udpSession) loop(body io.Reader) error {
	reader := frameReader{reader: body}
	for {
		source, destination, payload, err := reader.ReadClientFrame()
		if err != nil {
			return err
		}
		s.dispatch(source, destination, payload)
	}
}

// dispatch admits one client datagram to its association. A new association is
// refused when the stream or the service already holds its limit, and a
// datagram is dropped when its queue would cross a byte limit or the per-flow
// item queue is full. Both are admission refusals: the associations already
// admitted keep working, and no flow is torn down to make room.
func (s *udpSession) dispatch(source M.Socksaddr, destination M.Socksaddr, payload *buf.Buffer) {
	key := udpFlowKey{source.AddrPort(), destination.AddrPort()}
	length := payload.Len()
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		payload.Release()
		return
	}
	flow, loaded := s.flows[key]
	if !loaded {
		limits := s.service.udp.limits
		if len(s.flows) >= limits.SessionAssociations {
			s.refused.Add(1)
			s.service.udp.refuseAssociation()
			s.access.Unlock()
			payload.Release()
			return
		}
		if !s.service.udp.acquireAssociation() {
			s.refused.Add(1)
			s.access.Unlock()
			payload.Release()
			return
		}
		if !s.reserveBytesLocked(length) {
			s.service.udp.releaseAssociation()
			s.access.Unlock()
			payload.Release()
			return
		}
		flow = &udpFlow{
			session:     s,
			key:         key,
			source:      source,
			destination: destination,
			packets:     make(chan *buf.Buffer, udpFlowQueueSize),
			done:        make(chan struct{}),
		}
		flow.touch()
		s.flows[key] = flow
	} else if !s.reserveBytesLocked(length) {
		s.access.Unlock()
		payload.Release()
		return
	}
	s.access.Unlock()
	if !flow.push(payload) {
		// The flow closed between the admission and the queue, or the item
		// queue was full: the reserved bytes go back, and a flow that never
		// reached its handler returns its association slot too.
		s.dropped.Add(1)
		s.releaseQueuedBytes(length)
		s.service.udp.dropDatagram()
		if !loaded {
			flow.Close()
		}
		return
	}
	if !loaded {
		metadataSource := source
		if !source.Addr.IsValid() || source.Addr.IsUnspecified() {
			// A zero source (mihomo, sing-trusttunnel, our client) still means the
			// client: route rules by source keep working.
			metadataSource = s.source
		}
		go s.service.handler.NewPacketConnection(s.ctx, flow, Metadata{
			User:        s.user,
			Source:      metadataSource,
			Destination: destination,
			Network:     N.NetworkUDP,
		})
	}
}

// reserveBytesLocked takes n queued bytes for this stream and the service. The
// session lock must be held; the service budget is taken second, which is the
// fixed order. A zero-length datagram reserves nothing and is always admitted.
func (s *udpSession) reserveBytesLocked(n int) bool {
	if s.queuedBytes+n > s.service.udp.limits.SessionQueuedBytes {
		s.dropped.Add(1)
		s.service.udp.dropDatagram()
		return false
	}
	if !s.service.udp.acquireBytes(n) {
		s.dropped.Add(1)
		return false
	}
	s.queuedBytes += n
	return true
}

// releaseQueuedBytes returns n queued bytes to this stream and the service.
func (s *udpSession) releaseQueuedBytes(n int) {
	if n <= 0 {
		return
	}
	s.access.Lock()
	s.queuedBytes -= n
	if s.queuedBytes < 0 {
		s.queuedBytes = 0
	}
	s.access.Unlock()
	s.service.udp.releaseBytes(n)
}

// expireIdle closes associations that carried no datagram in either direction
// for UDPLimits.IdleTimeout. It is the only way an association slot comes back
// without the stream ending, so a client that opens associations and stops
// sending cannot hold them until the session closes. The snapshot below only
// proposes candidates; closeIfIdle re-checks the timestamp under the queue lock,
// so a datagram admitted after the snapshot keeps its association.
func (s *udpSession) expireIdle() {
	timeout := s.service.udp.limits.IdleTimeout
	interval := timeout / 4
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	if interval > 15*time.Second {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case now := <-ticker.C:
			deadline := now.Add(-timeout).UnixNano()
			s.access.Lock()
			var expired []*udpFlow
			for _, flow := range s.flows {
				if flow.lastActivity.Load() <= deadline {
					expired = append(expired, flow)
				}
			}
			s.access.Unlock()
			for _, flow := range expired {
				flow.closeIfIdle(deadline)
			}
		}
	}
}

func (s *udpSession) remove(flow *udpFlow) {
	s.access.Lock()
	defer s.access.Unlock()
	if s.flows[flow.key] == flow {
		delete(s.flows, flow.key)
	}
}

// close ends the stream: no frame is written any more and every flow is closed.
func (s *udpSession) close() {
	s.writeAccess.Lock()
	s.writeClosed = true
	s.writeAccess.Unlock()
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		return
	}
	s.closed = true
	close(s.done)
	flows := s.flows
	s.flows = nil
	s.access.Unlock()
	for _, flow := range flows {
		flow.Close()
	}
}

func (s *udpSession) writeFrame(buffer *buf.Buffer, source M.Socksaddr, destination M.Socksaddr) error {
	if buffer.Start() < ServerFrameHeadroom {
		newBuffer := buf.NewSize(ServerFrameHeadroom + buffer.Len())
		newBuffer.Advance(ServerFrameHeadroom)
		_, err := newBuffer.Write(buffer.Bytes())
		buffer.Release()
		if err != nil {
			newBuffer.Release()
			return err
		}
		buffer = newBuffer
	}
	defer buffer.Release()
	err := EncodeServerFrame(buffer, source, destination)
	if err != nil {
		return err
	}
	s.writeAccess.Lock()
	defer s.writeAccess.Unlock()
	if s.writeClosed {
		return net.ErrClosed
	}
	_, err = s.writer.Write(buffer.Bytes())
	if err == nil {
		err = flush(s.writer)
	}
	return err
}

var (
	_ N.PacketConn    = (*udpFlow)(nil)
	_ N.FrontHeadroom = (*udpFlow)(nil)
)

// udpFlow is one 4-tuple of a _udp2 stream as a packet conn for the handler.
type udpFlow struct {
	session     *udpSession
	key         udpFlowKey
	source      M.Socksaddr
	destination M.Socksaddr
	packets     chan *buf.Buffer
	done        chan struct{}

	queueAccess sync.Mutex
	queueClosed bool
	// lastActivity is the UnixNano of the last datagram admitted by push or
	// written by WritePacket; the idle sweeper reads it. Refusals and queue
	// drops never update it.
	lastActivity atomic.Int64
}

// touch records a datagram accepted in either direction. The caller holds
// queueAccess, except at construction before the flow is published, so the
// idle sweep's re-check cannot race with the update.
func (f *udpFlow) touch() {
	f.lastActivity.Store(time.Now().UnixNano())
}

// push queues one datagram whose bytes were already reserved. It reports false
// when the flow is closed or the item queue is full; the caller then returns the
// reservation. The queue mutex serialises push against the final drain in Close,
// so a datagram that arrives during the close cannot be left behind.
func (f *udpFlow) push(payload *buf.Buffer) bool {
	f.queueAccess.Lock()
	defer f.queueAccess.Unlock()
	if f.queueClosed {
		payload.Release()
		return false
	}
	select {
	case f.packets <- payload:
		// Only an admitted datagram is activity: a full queue drops the
		// datagram without extending the association's life.
		f.touch()
		return true
	default:
		payload.Release()
		return false
	}
}

func (f *udpFlow) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	select {
	case packet := <-f.packets:
		length := packet.Len()
		_, err := buffer.Write(packet.Bytes())
		packet.Release()
		f.session.releaseQueuedBytes(length)
		return f.destination, err
	case <-f.done:
		return M.Socksaddr{}, net.ErrClosed
	}
}

// WritePacket frames a reply: the sender becomes the frame source, the flow's
// client address the frame destination.
func (f *udpFlow) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	f.queueAccess.Lock()
	if f.queueClosed {
		f.queueAccess.Unlock()
		buffer.Release()
		return net.ErrClosed
	}
	f.touch()
	f.queueAccess.Unlock()
	sender := destination
	if !sender.IsIP() {
		sender = f.destination
	}
	return f.session.writeFrame(buffer, sender, f.source)
}

func (f *udpFlow) FrontHeadroom() int {
	return ServerFrameHeadroom
}

// Close detaches the flow, returns its queued bytes and its association slot,
// and releases everything still queued. It is safe to call more than once and
// from the sweeper, the handler and the session at the same time.
func (f *udpFlow) Close() error {
	f.closeFlow(false, 0)
	return nil
}

// closeIfIdle closes the flow only when nothing was admitted since deadline,
// re-reading the timestamp under the queue lock so a datagram that raced the
// sweeper's snapshot wins. It reports whether it closed the flow.
func (f *udpFlow) closeIfIdle(deadline int64) bool {
	return f.closeFlow(true, deadline)
}

// closeFlow ends the flow once. queueClosed is the single owner of done and of
// the final drain, so concurrent Close, expiry and handler calls cannot return
// a queued byte or an association slot twice.
func (f *udpFlow) closeFlow(checkIdle bool, deadline int64) bool {
	f.queueAccess.Lock()
	if f.queueClosed {
		f.queueAccess.Unlock()
		return false
	}
	if checkIdle && f.lastActivity.Load() > deadline {
		f.queueAccess.Unlock()
		return false
	}
	f.queueClosed = true
	close(f.done)
	queued := 0
	for {
		select {
		case packet := <-f.packets:
			queued += packet.Len()
			packet.Release()
		default:
			f.queueAccess.Unlock()
			f.session.remove(f)
			f.session.releaseQueuedBytes(queued)
			f.session.service.udp.releaseAssociation()
			return true
		}
	}
}

func (f *udpFlow) LocalAddr() net.Addr {
	return f.destination
}

func (f *udpFlow) SetDeadline(t time.Time) error {
	return os.ErrInvalid
}

func (f *udpFlow) SetReadDeadline(t time.Time) error {
	return os.ErrInvalid
}

func (f *udpFlow) SetWriteDeadline(t time.Time) error {
	return os.ErrInvalid
}

func (f *udpFlow) NeedAdditionalReadDeadline() bool {
	return true
}
