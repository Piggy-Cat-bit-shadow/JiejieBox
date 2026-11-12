package trusttunnel

import (
	"sync"
	"time"
)

// UDPLimits bounds the server half of _udp2. An association is one client
// 4-tuple of a stream; every association owns a queue that the handler reads.
//
// The zero value takes the defaults below, so a service built without explicit
// limits is still bounded: a client that mints 4-tuples cannot grow the
// server's maps, goroutines and buffers without bound. A datagram that would
// cross an association limit is dropped and counted, and the associations that
// were admitted keep working; a datagram that would cross a queue byte limit is
// dropped as a full socket buffer would be. Both are refusals at admission, not
// evictions, so a healthy flow is never torn down to make room.
//
// The per-flow item queue (udpFlowQueueSize) and the HTTP layer limits
// (serverMaxConcurrentStreams, serverMaxIncomingStreams) stay in force; these
// limits are in addition to them.
type UDPLimits struct {
	// SessionAssociations is the number of associations one _udp2 stream may
	// hold at the same time.
	SessionAssociations int
	// ServiceAssociations is the number of associations all streams together
	// may hold at the same time.
	ServiceAssociations int
	// SessionQueuedBytes is how many datagram bytes one stream may have
	// waiting in its association queues.
	SessionQueuedBytes int
	// ServiceQueuedBytes is how many datagram bytes all streams together may
	// have waiting.
	ServiceQueuedBytes int
	// IdleTimeout closes an association that carried no datagram in either
	// direction for this long. It is the only path that returns an association
	// slot without the stream ending, so a client that opens associations and
	// goes quiet cannot hold them for the life of the session.
	IdleTimeout time.Duration
}

const (
	// The association defaults are per _udp2 stream and per service. A DNS
	// client, a QUIC connection and a game session each use a handful of
	// 4-tuples; 256 per stream is generous, and 8192 across a service keeps the
	// worst case at a few thousand goroutines, not an unbounded number.
	defaultSessionAssociations = 256
	defaultServiceAssociations = 8192

	// The defaults hold at most four 64 KiB datagrams per association on
	// average: UDP is lossy, and a queue that deep is already a buffer bloat
	// signal rather than a smoothing window.
	defaultSessionQueuedBytes = 4 << 20
	defaultServiceQueuedBytes = 32 << 20

	// defaultUDPAssociationIdleTimeout is longer than the DNS and QUIC idle
	// windows a client sees, so a live association is not expired under it.
	defaultUDPAssociationIdleTimeout = 2 * time.Minute
)

func (l UDPLimits) withDefaults() UDPLimits {
	if l.SessionAssociations <= 0 {
		l.SessionAssociations = defaultSessionAssociations
	}
	if l.ServiceAssociations <= 0 {
		l.ServiceAssociations = defaultServiceAssociations
	}
	if l.SessionQueuedBytes <= 0 {
		l.SessionQueuedBytes = defaultSessionQueuedBytes
	}
	if l.ServiceQueuedBytes <= 0 {
		l.ServiceQueuedBytes = defaultServiceQueuedBytes
	}
	if l.IdleTimeout <= 0 {
		l.IdleTimeout = defaultUDPAssociationIdleTimeout
	}
	return l
}

// UDPStats reports the service-wide UDP accounting. A service that returned to
// rest reports zero associations and zero queued bytes; the refusal counters are
// monotonic. It is a diagnostic entry point, not a control knob.
type UDPStats struct {
	// Associations is the number of live associations.
	Associations int
	// QueuedBytes is the number of datagram bytes waiting in their queues.
	QueuedBytes int
	// RefusedAssociations counts datagrams refused because opening an
	// association would cross a session or service association limit.
	RefusedAssociations uint64
	// DroppedDatagrams counts datagrams dropped because a queue byte limit or
	// the per-flow item queue was full.
	DroppedDatagrams uint64
	// DatagramsReceived counts client datagrams read from the stream before any
	// admission decision. This is the INGRESS segment of the Q36 attribution:
	// a datagram counted here did reach the tunnel, whatever happened next.
	DatagramsReceived uint64
	// DatagramsSent counts replies written to the client successfully. Together
	// with DatagramsReceived it separates "the target never answered" from "the
	// tunnel failed to return the answer".
	DatagramsSent uint64
	// AssociationsExpired counts associations reclaimed by the idle sweep. It
	// is the DRAIN segment: the association slot and queued bytes were returned
	// without the stream ending.
	AssociationsExpired uint64
	// DatagramsDelivered counts client datagrams read out of their association
	// queue by the handler. After this point the tunnel no longer owns them, so
	// this is the only ingress segment whose fate is the handler's to report.
	DatagramsDelivered uint64
	// DatagramsDiscarded counts client datagrams the tunnel dropped AFTER they
	// were received and admitted: the association (or the stream) closed while
	// the datagram was still queued, or it was admitted and then lost to a
	// failed delivery. It is the missing term that makes the ingress segments
	// conserve:
	//
	//	DatagramsReceived == DatagramsDelivered + DroppedDatagrams +
	//	    RefusedAssociations + DatagramsDiscarded   (at rest)
	//
	// where "at rest" means QueuedBytes == 0. A non-zero value is a normal
	// outcome of a close under load, not an error.
	DatagramsDiscarded uint64
	// RepliesFailed counts handler replies that could not be written to the
	// client, either because the stream was already closing or because the
	// transport write failed. Sent and Failed are the two halves of the reply
	// segment: without Failed, a target that answered on a breaking tunnel was
	// indistinguishable from a target that never answered.
	RepliesFailed uint64
}

// udpBudget is the service-wide half of UDPLimits. Session counters live on
// udpSession; the fixed order is session lock, then this lock.
type udpBudget struct {
	limits UDPLimits

	access              sync.Mutex
	associations        int
	queuedBytes         int
	refusedAssociations uint64
	droppedDatagrams    uint64
	datagramsReceived   uint64
	datagramsSent       uint64
	associationsExpired uint64
	datagramsDelivered  uint64
	datagramsDiscarded  uint64
	repliesFailed       uint64
}

func newUDPBudget(limits UDPLimits) *udpBudget {
	return &udpBudget{limits: limits.withDefaults()}
}

// acquireAssociation takes one service-wide association slot.
func (b *udpBudget) acquireAssociation() bool {
	b.access.Lock()
	defer b.access.Unlock()
	if b.associations >= b.limits.ServiceAssociations {
		b.refusedAssociations++
		return false
	}
	b.associations++
	return true
}

func (b *udpBudget) releaseAssociation() {
	b.access.Lock()
	defer b.access.Unlock()
	if b.associations > 0 {
		b.associations--
	}
}

// acquireBytes takes n service-wide queued bytes.
func (b *udpBudget) acquireBytes(n int) bool {
	b.access.Lock()
	defer b.access.Unlock()
	if b.queuedBytes+n > b.limits.ServiceQueuedBytes {
		b.droppedDatagrams++
		return false
	}
	b.queuedBytes += n
	return true
}

func (b *udpBudget) releaseBytes(n int) {
	if n <= 0 {
		return
	}
	b.access.Lock()
	defer b.access.Unlock()
	b.queuedBytes -= n
	if b.queuedBytes < 0 {
		b.queuedBytes = 0
	}
}

func (b *udpBudget) refuseAssociation() {
	b.access.Lock()
	b.refusedAssociations++
	b.access.Unlock()
}

func (b *udpBudget) dropDatagram() {
	b.access.Lock()
	b.droppedDatagrams++
	b.access.Unlock()
}

func (b *udpBudget) noteReceived() {
	b.access.Lock()
	b.datagramsReceived++
	b.access.Unlock()
}

func (b *udpBudget) noteSent() {
	b.access.Lock()
	b.datagramsSent++
	b.access.Unlock()
}

func (b *udpBudget) noteExpired() {
	b.access.Lock()
	b.associationsExpired++
	b.access.Unlock()
}

func (b *udpBudget) noteDelivered() {
	b.access.Lock()
	b.datagramsDelivered++
	b.access.Unlock()
}

// noteDiscarded counts datagrams the tunnel received and then dropped without
// delivering them. n is the number of datagrams, not a byte count.
func (b *udpBudget) noteDiscarded(n uint64) {
	if n == 0 {
		return
	}
	b.access.Lock()
	b.datagramsDiscarded += n
	b.access.Unlock()
}

func (b *udpBudget) noteReplyFailed() {
	b.access.Lock()
	b.repliesFailed++
	b.access.Unlock()
}

func (b *udpBudget) stats() UDPStats {
	b.access.Lock()
	defer b.access.Unlock()
	return UDPStats{
		Associations:        b.associations,
		QueuedBytes:         b.queuedBytes,
		RefusedAssociations: b.refusedAssociations,
		DroppedDatagrams:    b.droppedDatagrams,
		DatagramsReceived:   b.datagramsReceived,
		DatagramsSent:       b.datagramsSent,
		AssociationsExpired: b.associationsExpired,
		DatagramsDelivered:  b.datagramsDelivered,
		DatagramsDiscarded:  b.datagramsDiscarded,
		RepliesFailed:       b.repliesFailed,
	}
}
