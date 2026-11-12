package v2rayhttp

// Network-generation ownership for the HTTP/2 pool.
//
// # What this file adds, and why the pool is the place
//
// The transport keeps established HTTP/2 connections and hands every stream out on one of them. A
// physical network change is delivered to the transport as `Reset` (an InterfaceUpdateListener
// call), which closes the pool - but the listener phase runs AFTER the network manager has claimed
// the new generation, and a demand that arrives in that window used to be handed a connection built
// for the previous network. The audit's F-15 states the consequence precisely: "a dial during the
// listener phase can reuse a pre-transition pooled session; no protocol reads the epoch at pool
// hand-out".
//
// The pool is the only component that can answer "is this connection still from the generation that
// is current", so the check belongs at the hand-out (`dialHTTP2`) rather than in each caller. The
// generation clock is common/runtimecoord's, the same clock the network manager publishes and the
// dialer compares against, so "previous generation" has ONE definition in the tree.
//
// # Why the whole transport is replaced instead of closing idle connections
//
// `http2.Transport.CloseIdleConnections` does not stop a connection that still carries a stream from
// taking a NEW stream: such a connection is busy, not idle. A pool that only closed idle connections
// would therefore still lend the previous generation under load - exactly when the transition
// matters. Retiring the transport object is what makes the previous generation unreachable for new
// streams while its running streams finish.
//
// # One mechanism, two owners
//
// The pool and its registration are exported because protocol/v2raygrpclite is the SAME shape: one
// `http2.Transport` behind one client, handed out to gRPC streams. The audit's Q12 finding was that
// only this pool had an owner and the gRPC one was left out, so the fix must not be a second copy of
// the mechanism - it must be this one, used by both. The pool therefore also answers a generation
// snapshot (GenerationPool.Snapshot), which is what feeds each owner's residency ledger (audit Q15):
// a retired generation is DRAINING until its last request returns, not a number that disappeared.
//
// # What is deliberately NOT done here
//
//   - It does not kill a stream that is already running. The retired transport is drained: its
//     connections keep serving the streams they already have, and are closed when the last request
//     on that transport returns. Cutting them would be the defect this check exists to prevent,
//     one layer up.
//   - It does not gate on "the transition has committed". The epoch is claimed before the reset body
//     runs, and the transport cannot see whether the network actually changed. A false positive
//     therefore costs one extra connection (the listener retires an idle generation of the claimed
//     epoch anyway); a false negative would lend a connection the audit proved stale.

import (
	"io"
	"sync"

	"github.com/sagernet/sing-box/common/runtimecoord"

	"golang.org/x/net/http2"
)

// GenerationRegistration returns the registration for this client, or nil in a build with no
// coordinator. A nil registration makes every generation check a no-op, which is the previous
// behaviour exactly - an embedder that builds a transport without a runtime coordinator is not
// broken by this.
func GenerationRegistration(coordinator *runtimecoord.Coordinator, label string) (*runtimecoord.Registration, func()) {
	if coordinator == nil {
		return nil, nil
	}
	return coordinator.Register(label)
}

// GenerationStale reports whether a pooled generation belongs to a previous network.
func GenerationStale(registration *runtimecoord.Registration) bool {
	return registration != nil && registration.Stale()
}

// AcknowledgeGeneration records that the pool produced a connection on the current network.
//
// It is called exactly where the resource becomes current: when a request on the pool received a
// response and the generation did not change while it was running. A discarded late result does not
// acknowledge: it is not evidence that the pool can produce a connection on the new network.
func AcknowledgeGeneration(registration *runtimecoord.Registration) {
	if registration != nil {
		registration.Acknowledge()
	}
}

// CurrentGeneration is the network generation that is current NOW.
//
// It reads the coordinator rather than the registration on purpose: the registration's epoch is
// "the generation this pool was last rebuilt against", and comparing it with itself would never
// detect a change that happened during a dial. The coordinator's epoch is the one clock the network
// manager publishes and the dialer compares against, so a late result is detected by one number
// taken twice from one source.
func CurrentGeneration(coordinator *runtimecoord.Coordinator) uint64 {
	if coordinator == nil {
		return 0
	}
	return coordinator.Epoch()
}

// generationRegistration returns this client's registration through the shared helper; the method
// exists so the client's call sites read as one object.
func generationRegistration(coordinator *runtimecoord.Coordinator, label string) (*runtimecoord.Registration, func()) {
	return GenerationRegistration(coordinator, label)
}

// generationStale reports whether the pooled generation belongs to a previous network.
func (c *Client) generationStale() bool {
	return GenerationStale(c.registration)
}

// acknowledgeGeneration records that the pool produced a connection on the current network.
func (c *Client) acknowledgeGeneration() {
	AcknowledgeGeneration(c.registration)
}

// currentGeneration is the network generation that is current NOW.
func (c *Client) currentGeneration() uint64 {
	return CurrentGeneration(c.coordinator)
}

// retireGeneration replaces the pooled transport with a fresh one when the current generation is no
// longer the one the pool was built for. The previous transport is drained by `GenerationPool.Finish`,
// not closed here.
func (c *Client) retireGeneration() {
	if c.pool == nil || c.newTransport == nil {
		// HTTP/1 has no pooled connection to lend: every dial opens its own socket, so there is
		// nothing for a generation check to retire.
		return
	}
	c.pool.Retire(c.newTransport())
}

// staleGenerationError reports a creation whose result belongs to a generation that ended while it
// was running. It is a retryable condition, not a configuration error: the caller's next attempt
// starts entirely on the current network.
type staleGenerationError struct{}

func (staleGenerationError) Error() string {
	return "v2rayhttp: the network changed while the connection was being established; the result was discarded"
}

// Generation is one pool generation: the transport that is (or was) current, and how many requests
// are still using it. The count is what lets a retired generation finish its streams before it is
// closed.
type Generation struct {
	transport *http2.Transport
	inflight  int
	retired   bool
	sequence  uint64
}

// Transport is the pooled transport this generation hands streams out on.
func (g *Generation) Transport() *http2.Transport {
	if g == nil {
		return nil
	}
	return g.transport
}

// Sequence is this generation's ordinal in its pool. It is a POOL-LOCAL number (not runtimecoord's
// network generation): it exists so a ledger can say "these objects belong to a retired generation"
// without reading the pool's internals.
func (g *Generation) Sequence() uint64 {
	if g == nil {
		return 0
	}
	return g.sequence
}

// GenerationPool owns the current generation and the retired generations that still carry a stream.
type GenerationPool struct {
	access     sync.Mutex
	current    *Generation
	draining   []*Generation
	sequence   uint64
	terminated uint64
}

// NewGenerationPool starts a pool whose first generation is transport.
func NewGenerationPool(transport *http2.Transport) *GenerationPool {
	pool := &GenerationPool{sequence: 1}
	pool.current = &Generation{transport: transport, sequence: 1}
	return pool
}

// Begin pins the current generation for one request. The request must call Finish exactly once when
// its response body is done (closed or read to EOF).
func (p *GenerationPool) Begin() *Generation {
	if p == nil {
		return nil
	}
	p.access.Lock()
	defer p.access.Unlock()
	generation := p.current
	generation.inflight++
	return generation
}

// Retire makes next the current generation and marks the previous one retired. A retired generation
// with no request in flight is closed now; one that still carries a stream is drained when its last
// request finishes.
func (p *GenerationPool) Retire(next *http2.Transport) {
	if p == nil || next == nil {
		return
	}
	p.access.Lock()
	previous := p.current
	p.sequence++
	p.current = &Generation{transport: next, sequence: p.sequence}
	previous.retired = true
	release := previous.inflight == 0
	if !release {
		p.draining = append(p.draining, previous)
	}
	p.access.Unlock()
	if release {
		// No request of this generation is running, so nothing can be cut: the transport is
		// closed now rather than left for a reuse that must not happen.
		p.releaseGeneration(previous)
	}
}

// Finish releases one request and closes the retired generation when it was the last one.
func (p *GenerationPool) Finish(generation *Generation) {
	if p == nil || generation == nil {
		return
	}
	p.access.Lock()
	generation.inflight--
	release := generation.retired && generation.inflight == 0
	if release {
		for index, candidate := range p.draining {
			if candidate == generation {
				p.draining = append(p.draining[:index], p.draining[index+1:]...)
				break
			}
		}
	}
	p.access.Unlock()
	if release {
		// Every request of this generation has returned. What is left is an idle pool that can
		// only be reached through the retired transport, so it is closed now.
		p.releaseGeneration(generation)
	}
}

// closeIdle closes idle connections of the current generation AND of the drained ones. It never cuts
// a stream: `http2.Transport.CloseIdleConnections` only closes connections with no active stream.
func (p *GenerationPool) CloseIdle() {
	if p == nil {
		return
	}
	p.access.Lock()
	transports := make([]*http2.Transport, 0, 1+len(p.draining))
	transports = append(transports, p.current.transport)
	for _, generation := range p.draining {
		transports = append(transports, generation.transport)
	}
	p.access.Unlock()
	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
}

// CloseAll force-closes every connection of every generation. It is the Client.Close contract: a
// closed client owns nothing, including the streams that were still running.
func (p *GenerationPool) CloseAll() {
	if p == nil {
		return
	}
	p.access.Lock()
	generations := make([]*Generation, 0, 1+len(p.draining))
	generations = append(generations, p.current)
	generations = append(generations, p.draining...)
	p.access.Unlock()
	for _, generation := range generations {
		p.releaseGeneration(generation)
	}
}

// GenerationSnapshot is the pool's object-identity snapshot for a residency ledger (audit Q15).
//
// Sequence is the current generation's ordinal, CurrentInflight the requests holding the current
// generation, RetiredDraining the retired generations still holding a request, and
// RetiredInflight how many requests they hold between them. A retired generation is NOT subtracted
// from the live count when it is retired: it stays Draining until its last request returns, and only
// then is counted in Terminated.
type GenerationSnapshot struct {
	Sequence        uint64
	CurrentInflight int
	RetiredDraining int
	RetiredInflight int
	Terminated      uint64
}

// Snapshot reads the pool's generation state. It takes the pool lock once and allocates nothing.
func (p *GenerationPool) Snapshot() GenerationSnapshot {
	if p == nil {
		return GenerationSnapshot{}
	}
	p.access.Lock()
	defer p.access.Unlock()
	snapshot := GenerationSnapshot{
		Sequence:        p.sequence,
		CurrentInflight: p.current.inflight,
		RetiredDraining: len(p.draining),
		Terminated:      p.terminated,
	}
	for _, generation := range p.draining {
		snapshot.RetiredInflight += generation.inflight
	}
	return snapshot
}

// releaseGeneration closes a generation's transport and counts one completed release. A retired
// generation is only ever released once: Retire releases it when nothing is in flight, Finish when
// its last request returns.
func (p *GenerationPool) releaseGeneration(generation *Generation) {
	ResetTransport(generation.transport)
	p.access.Lock()
	p.terminated++
	p.access.Unlock()
}

// generationBody releases the pinned generation when the response body is closed or read to EOF.
// The release is once-only: a caller that reads the last byte and then closes is the normal case,
// and it must not decrement the generation's in-flight count twice.
type generationBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *generationBody) Read(buffer []byte) (int, error) {
	read, err := b.ReadCloser.Read(buffer)
	if err != nil {
		b.once.Do(b.release)
	}
	return read, err
}

func (b *generationBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}
