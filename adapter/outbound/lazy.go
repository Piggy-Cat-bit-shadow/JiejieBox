package outbound

import (
	"context"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// Deferred (lazy) outbound construction.
//
// # What problem this solves
//
// A configuration or subscription can name thousands of nodes while a session uses a handful. Today
// every one of them is a fully expanded runtime object before the first byte of traffic: a dialer, a
// TLS configuration, and - for the heavier protocols - a transport. MEASURED on this tree, a
// never-dialled Trojan node costs ~2.1 KiB of live heap, and a measured profile is hundreds of
// thousands of them.
//
// A LazyOutbound keeps an immutable, protocol-owned RECIPE and builds the runtime object on first
// demand. The recipe is what the node is; the runtime object is what the node can do. Keeping those
// separate is the whole point: metadata queries (tag, type, network, dependencies, capabilities),
// start stages and scope registration must never expand the node, and the manager must never hold a
// lock across the first build.
//
// # Why the state machine is not just "sync.Once"
//
// A build can fail (and must be retryable without leaking the partial object), it can be overtaken
// by Close (and a late result must not be published), and several goroutines can demand the same
// node at once (and the node must be built exactly once). Those are five distinct states, and the
// transitions between them are what this file implements:
//
//	never-built --build start--> building --success--> ready
//	     ^                          |                    |
//	     |                    failure (discard)          | Close/Evict
//	     +--------------------------+                    v
//	                                +---------------> retiring --> closed
//
// `retiring` is the state a runtime is released THROUGH: the owner has decided the expansion must
// go away (eviction of an idle node, or Close during a build), and the built object may still be
// reachable from an in-flight dial. Nothing new is published from it.
//
// # Who may release an expansion, and what each rule is for
//
// Three independent facts can keep an expansion alive, and Evict refuses unless ALL of them say
// "nothing is using it":
//
//	dialing   a hand-off is inside DialContext/ListenPacket right now.
//	leased    the MANAGER's hand-off lease is outstanding (Manager.AcquireOutbound). This is the
//	          window the object cannot see by itself: the router acquired the object and has not
//	          reached DialContext yet, and a release there made that hand-off fail with a closed
//	          object. The manager calls AcquireExpansionLease on every path that hands an object
//	          out, so the window between "lease granted" and "dialing counted" is closed too.
//	conns     a connection this expansion produced is still open. An expansion is only a candidate
//	          for reclamation when it is idle, and an open connection is not idle. The counter is
//	          carried by a wrapper that forwards to the real connection (see lazyConn), so the
//	          resolved connection keeps every capability the caller needs - Upstream() is what
//	          common.Cast and N.UnwrapWriter follow.
//
// # What a demand gets during an eviction window
//
// Nothing is refused. An eviction is a short teardown, and a demand that lands inside it waits for
// that teardown to settle and then rebuilds (acquireRuntime's retiring branch), bounded by the
// CALLER's context. Returning os.ErrClosed there was a defect: the node was about to become
// buildable again, and the refusal was invisible to a user as "file already closed".
//
// # What this type deliberately does NOT do
//
// It does not choose WHICH nodes to evict, does not share one runtime between tags, and does not
// assume anything about the protocol. The sharing question needs a key with the provider scope, the
// configuration semantics and the generation in it (see the report); the eviction POLICY needs the
// group/route model. This type is the object-level primitive those layers would call.

// LazyState is the lifecycle state of one deferred outbound.
type LazyState uint8

const (
	// LazyStateNeverBuilt: only the recipe exists. Nothing of the protocol runtime has been built.
	LazyStateNeverBuilt LazyState = iota
	// LazyStateBuilding: one goroutine is running the builder. Every other demand waits for it.
	LazyStateBuilding
	// LazyStateReady: the runtime exists and is published to every demand of this object.
	LazyStateReady
	// LazyStateRetiring: the owner decided the expansion must go away; no new result may be
	// published, and the runtime is being released. A late build is discarded, never published.
	LazyStateRetiring
	// LazyStateClosed: terminal. No dial succeeds and no build starts.
	LazyStateClosed
)

func (s LazyState) String() string {
	switch s {
	case LazyStateNeverBuilt:
		return "never-built"
	case LazyStateBuilding:
		return "building"
	case LazyStateReady:
		return "ready"
	case LazyStateRetiring:
		return "retiring"
	case LazyStateClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// DeferredOutboundConstructor is the manager seam: a publisher that knows how to create a light
// recipe for a node instead of the expanded object.
//
// `owner` is the publication scope the node is being installed under: "" for the static
// configuration (`Create`/`Replace`) and the provider tag for an owner-managed publication
// (`ReplaceOwned`). It is passed through because a product restriction like "only provider-fed cold
// nodes" is a decision the installer must be able to make HERE, without a second seam: the scope is
// part of the node's identity, and the same constructor must not guess it from the tag.
//
// `handled` is what keeps the seam optional and honest. A constructor that cannot represent this
// node returns handled=false, and the manager falls through to the ordinary registry. A deferred
// path must therefore never be a partial reimplementation of the eager one: it either covers an
// option shape provably, or the eager constructor keeps ownership - including its error stage.
type DeferredOutboundConstructor func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any, owner string) (adapter.Outbound, bool, error)

// LazyBuilder builds one runtime outbound from an immutable recipe.
//
// The context is the NODE's lifetime context, not the dial's: the eager path constructs outbounds
// with the Box (or provider) context, and an object that captured a dial context would be built
// against a context that is cancelled the moment the dial returns. Close cancels this context, so a
// builder that does slow work can abandon it, but a dial timeout must not poison a node that is
// still able to serve.
type LazyBuilder[T any] func(ctx context.Context, recipe T) (adapter.Outbound, error)

// LazyOutbound is a deferred adapter.Outbound.
//
// It implements adapter.Lifecycle so the existing Scope & LifecycleGroup contract keeps owning the
// teardown: Start(Initialize) registers one cleanup on the scope the manager hands out, and that
// cleanup is what closes the expansion. The built object is started through a per-build child Scope
// owned by this type, so evicting this object releases the runtime's resources without leaving the
// expansion's cleanup closures reachable from a parent scope (the "old object tree stays strongly
// referenced" failure).
type LazyOutbound[T any] struct {
	Adapter
	recipe  T
	builder LazyBuilder[T]
	logger  log.ContextLogger

	// baseContext is the construction context (the Box or provider context). The node lifetime
	// context is derived from it ON FIRST BUILD, not here: a never-dialled node needs no context
	// object, and a 10 000-node profile must not pay one per node. lifetime/cancel are non-nil
	// exactly while an expansion exists or is in flight; cancel aborts an in-flight build on Close.
	baseContext context.Context
	lifetime    context.Context
	cancel      context.CancelFunc

	access sync.Mutex
	state  LazyState
	// runtime is the published expansion. It is non-nil exactly in LazyStateReady.
	runtime adapter.Outbound
	// buildScope owns the expansion's cleanups (what the built object registered during Start). It
	// is per-build, so eviction closes ONLY this expansion and drops the closures with it.
	buildScope *adapter.Scope
	// settled is closed when the transition in flight finishes: a BUILD (waiters block on it rather
	// than starting a second build) or an EVICTION (a demand waits on it and then rebuilds, instead of
	// being refused with os.ErrClosed for a node that is about to become buildable again). The two
	// uses never overlap - a build is only in flight from never-built, an eviction only from ready -
	// so one channel carries both signals and the object pays for one word, not two. See the struct
	// comment about the allocator size class.
	settled chan struct{}
	// buildErr is the last failed build's error. A waiter that arrives after a failed build gets
	// that error instead of immediately starting another doomed attempt; the NEXT demand retries.
	buildErr error
	// builds counts build attempts, including failed ones. It is the observable instrument the
	// lifecycle tests assert on: a correctly single-flighted build must not move for waiters.
	builds int
	// dialing counts hand-offs that are using the runtime right now. Eviction refuses while it is
	// non-zero, so an expansion cannot be released under a dial that already obtained it.
	dialing int
	// leased counts hand-off leases the MANAGER granted for this object (AcquireExpansionLease).
	// It is separate from dialing because the two windows are different: the manager hands the
	// object out before the dialer reaches DialContext, and an eviction there is exactly the
	// release-under-a-live-hand-off this counter exists to prevent.
	leased int32
	// conns counts connections this expansion produced that have not been closed. An open
	// connection is what "not idle" means for a node, so eviction refuses while it is non-zero.
	conns int32
	// lastUse is the unix-nano time of the last hand-off through this node (a publish, a dial that
	// ended, a lease grant, or a connection closing). The reclamation policy reads it to evict the
	// least recently used expansion first; zero means the node was never expanded.
	lastUse int64
	// owner is the reclaim owner (the Manager that published this object), or nil for an object built
	// outside a manager. It is how the object tells the ONE policy owner that an expansion appeared or
	// that the existing idle walk reached it; the object itself never decides WHICH node to evict.
	owner *Manager
	// expansionEstimate is the protocol's own per-expansion size estimate, in bytes, or zero when it
	// does not provide one. It is a constant for a protocol, set once at construction, and it exists
	// so the byte budget can be protocol-specific instead of hard-coding one protocol's cost.
	//
	// The counters and this estimate are 32-bit and live under access rather than as atomics: this
	// object is a per-node allocation. MEASURED: 40 added bytes put the object at 520 B (allocator
	// class 576) against a 480 B base (class 480), i.e. each added word costs more than itself at the
	// boundary - which is why there is one `settled` channel here and not two.
	expansionEstimate int32
	// closing distinguishes a Close-in-flight retirement from an Evict-in-flight one. Both use
	// LazyStateRetiring while their resources are being torn down, but only one of them is allowed
	// to leave the object revivable: without this, a Close that lands inside Evict's unlocked
	// teardown window returns nil and the node is rebuilt on the next demand - a terminal state that
	// silently un-terminates itself.
	closing bool
	// highestStage is the furthest start stage the MANAGER has run on this object. A build that
	// happens later replays every stage the manager already reached, exactly as Replace does for a
	// replacement created after Start finished.
	highestStage adapter.StartStage
	started      bool
	// haveIdlePolicy records whether the reference manager already told this object whether it may
	// keep idle connections. A build that happens later must inherit that answer instead of
	// starting from the default.
	haveIdlePolicy bool
	keepIdle       bool
}

var _ adapter.Outbound = (*LazyOutbound[struct{}])(nil)
var _ adapter.Lifecycle = (*LazyOutbound[struct{}])(nil)

// NewLazyOutbound wraps an immutable recipe in a deferred outbound.
//
// `owner` supplies Type/Tag/Network/Dependencies and must describe the SAME node the builder will
// produce. `baseContext` is the construction context (the Box or provider context); it becomes the
// parent of the node lifetime context.
func NewLazyOutbound[T any](owner Adapter, logger log.ContextLogger, baseContext context.Context, recipe T, builder LazyBuilder[T]) *LazyOutbound[T] {
	if baseContext == nil {
		baseContext = context.Background()
	}
	// The owner's slices are borrowed from the caller. Copy them so the published object cannot be
	// mutated through the caller's slice: a recipe is immutable by contract.
	owner.network = append([]string(nil), owner.network...)
	owner.dependencies = append([]string(nil), owner.dependencies...)
	return &LazyOutbound[T]{
		Adapter:      owner,
		recipe:       recipe,
		builder:      builder,
		logger:       logger,
		baseContext:  baseContext,
		highestStage: adapter.StartStateInitialize,
	}
}

// State reports the lifecycle state. It is read-only and never builds.
func (lz *LazyOutbound[T]) State() LazyState {
	lz.access.Lock()
	defer lz.access.Unlock()
	return lz.state
}

// Builds reports how many build attempts have run. It is the observable build counter.
func (lz *LazyOutbound[T]) Builds() int {
	lz.access.Lock()
	defer lz.access.Unlock()
	return lz.builds
}

// Built reports the published runtime, or false when the node is not expanded. It never builds and
// never pins the runtime, so it is for diagnostics and capability forwarding that re-checks.
func (lz *LazyOutbound[T]) Built() (adapter.Outbound, bool) {
	lz.access.Lock()
	defer lz.access.Unlock()
	if lz.state != LazyStateReady {
		return nil, false
	}
	return lz.runtime, true
}

// WithRuntime runs visit with the published runtime while holding a hand-off reference, so an
// eviction or Close cannot release the object underneath it. It returns false - and visits nothing -
// when the node is not expanded; callers must not treat that as an error, because a capability
// query must not force an expansion.
func (lz *LazyOutbound[T]) WithRuntime(visit func(adapter.Outbound)) bool {
	lz.access.Lock()
	if lz.state != LazyStateReady {
		lz.access.Unlock()
		return false
	}
	runtime := lz.runtime
	lz.dialing++
	lz.access.Unlock()
	defer lz.releaseRuntime()
	visit(runtime)
	return true
}

// SetIdleConnectionsPolicy remembers the reference manager's answer for FUTURE expansions. The
// caller is expected to also forward the same answer to the current runtime; the protocol wrapper
// does both.
func (lz *LazyOutbound[T]) SetIdleConnectionsPolicy(keep bool) {
	lz.access.Lock()
	lz.haveIdlePolicy = true
	lz.keepIdle = keep
	lz.access.Unlock()
}

// Start implements adapter.Lifecycle. Only Initialize has work: it registers the cleanup that owns
// this object's teardown on the scope the manager created for it. No stage builds the node, and no
// stage touches the protocol - a configured node is a description until something dials it.
func (lz *LazyOutbound[T]) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	lz.access.Lock()
	if !lz.started || stage > lz.highestStage {
		lz.highestStage = stage
	}
	lz.started = true
	lz.access.Unlock()
	if stage == adapter.StartStateInitialize && scope != nil {
		// The name is what the bounded close diagnostic reports if this cleanup ever blocks. The
		// cleanup runs LAST in the child scope (cleanups run in reverse registration order), after
		// the per-build scope's own cleanups were already released by Close.
		scope.AddNamed("outbound/lazy["+lz.Tag()+"]", lz.Close)
	}
	return nil
}

// DialContext builds the node on first demand and hands the dial to the expansion.
func (lz *LazyOutbound[T]) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	runtime, err := lz.acquireRuntime(ctx)
	if err != nil {
		return nil, err
	}
	defer lz.releaseRuntime()
	conn, err := runtime.DialContext(ctx, network, destination)
	if err != nil {
		return nil, err
	}
	return lz.trackConn(conn), nil
}

// ListenPacket builds the node on first demand and hands the packet listener to the expansion.
func (lz *LazyOutbound[T]) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	runtime, err := lz.acquireRuntime(ctx)
	if err != nil {
		return nil, err
	}
	defer lz.releaseRuntime()
	conn, err := runtime.ListenPacket(ctx, destination)
	if err != nil {
		return nil, err
	}
	return lz.trackPacketConn(conn), nil
}

// trackConn is what makes "an open connection pins the expansion" true. It is a transparent wrapper:
// the embedded connection keeps Read/Write/deadlines/addresses, and Upstream plus
// Reader/WriterReplaceable keep this tree's capability discovery working through it (common.Cast and
// N.UnwrapWriter walk Upstream, which is how half-close and the splice path still find the real
// socket). Without those the wrapper would silently downgrade every deferred connection from
// half-close to a full close, which is a business regression rather than a memory trade.
func (lz *LazyOutbound[T]) trackConn(conn net.Conn) net.Conn {
	lz.access.Lock()
	lz.conns++
	lz.touchLocked()
	lz.access.Unlock()
	return &lazyConn{Conn: conn, release: lz.connClosed}
}

// trackPacketConn is the packet half of trackConn. A packet connection that does not implement
// N.NetPacketConn is returned unwrapped: wrapping it in that interface would make this type claim
// ReadPacket/WritePacket the runtime does not have, and the failure would surface as a panic on the
// first datagram rather than as a missing optimisation. A non-N packet connection is therefore pinned
// only for the duration of its dial (the lease and the dialing counter), which is documented here
// instead of being silently assumed.
func (lz *LazyOutbound[T]) trackPacketConn(conn net.PacketConn) net.PacketConn {
	netPacketConn, isNetPacketConn := conn.(N.NetPacketConn)
	if !isNetPacketConn {
		return conn
	}
	lz.access.Lock()
	lz.conns++
	lz.touchLocked()
	lz.access.Unlock()
	return &lazyPacketConn{NetPacketConn: netPacketConn, release: lz.connClosed}
}

func (lz *LazyOutbound[T]) connClosed() {
	lz.access.Lock()
	if lz.conns > 0 {
		lz.conns--
	}
	lz.touchLocked()
	lz.access.Unlock()
}

// Close releases the expansion and makes the object terminal. It is idempotent and safe from any
// goroutine, including while a build is in flight: the in-flight result is then discarded instead of
// published, and the resources it registered are released through its per-build scope.
func (lz *LazyOutbound[T]) Close() error {
	lz.access.Lock()
	switch lz.state {
	case LazyStateClosed:
		lz.access.Unlock()
		return nil
	case LazyStateRetiring:
		// Two retirements share this state. If this Close started it, the teardown in flight will
		// land on closed. If an EVICTION started it, this Close must still be terminal: mark it so
		// the eviction's tail settles on closed instead of putting the node back to never-built.
		lz.closing = true
		lz.access.Unlock()
		return nil
	case LazyStateBuilding:
		// The builder observes the cancellation and must not publish. Mark the state first so even a
		// builder that ignores its context cannot publish: buildRuntime checks `late` under this same
		// lock before it assigns the runtime.
		lz.state = LazyStateRetiring
		lz.closing = true
		cancel := lz.cancel
		lz.access.Unlock()
		if cancel != nil {
			cancel()
		}
		return nil
	case LazyStateReady:
		lz.state = LazyStateRetiring
		lz.closing = true
		runtime := lz.runtime
		scope := lz.buildScope
		cancel := lz.cancel
		owner := lz.owner
		lz.runtime = nil
		lz.buildScope = nil
		lz.lifetime = nil
		lz.cancel = nil
		lz.access.Unlock()
		if owner != nil {
			owner.NoteExpansionReleased()
		}
		if cancel != nil {
			cancel()
		}
		if scope != nil {
			// The built object registered its resources on this scope during Start. Closing it is
			// the teardown the eager lifecycle would perform, and it drops the closures that would
			// otherwise keep the expansion reachable from a parent scope.
			err := scope.Close()
			lz.finishClose()
			return err
		}
		lz.finishClose()
		return common.Close(runtime)
	default: // LazyStateNeverBuilt
		lz.state = LazyStateClosed
		lz.lifetime = nil
		lz.cancel = nil
		lz.access.Unlock()
		return nil
	}
}

// Evict releases an idle expansion and returns the node to the recipe. It reports whether it
// evicted: false means the node was not ready, or something is still using it. The node can be
// expanded again by the next demand - that is the whole point of eviction being safe here: a recipe
// can always rebuild, and nothing about the expansion is an identity the caller holds.
//
// # The three "in use" facts it checks
//
// `dialing` (a hand-off inside DialContext), `leased` (a manager hand-off lease that has not reached
// DialContext yet) and `conns` (a connection this expansion produced that is still open). All three
// are checked under the same lock that installs LazyStateRetiring, so there is no window between the
// check and the release: a demand either increments its counter first (and the eviction then refuses)
// or observes the retirement and waits for it (acquireRuntime).
//
// # What the caller must guarantee, and why this method cannot check it
//
// It must only be called for a protocol whose expansion holds no resource that outlives the
// hand-off beyond what its Start registered on the scope (a pooled transport or multiplexer would
// have to be drained first). The Trojan prototype satisfies that, and its eligibility rule is what
// enforces it: an eligible shape has no transport and no multiplex, so a connection it hands out
// owns its socket rather than a share of a pool.
func (lz *LazyOutbound[T]) Evict() bool {
	lz.access.Lock()
	if lz.state != LazyStateReady || lz.dialing > 0 || lz.leased > 0 || lz.conns > 0 {
		lz.access.Unlock()
		return false
	}
	runtime := lz.runtime
	scope := lz.buildScope
	cancel := lz.cancel
	settled := make(chan struct{})
	lz.settled = settled
	lz.state = LazyStateRetiring
	lz.runtime = nil
	lz.buildScope = nil
	lz.lifetime = nil
	lz.cancel = nil
	lz.access.Unlock()
	if cancel != nil {
		cancel()
	}
	var releaseErr error
	if scope != nil {
		// Exactly one of these runs, and the criterion is the one buildRuntime uses: a per-build
		// scope exists exactly when the built object was started, and then the scope IS its owner.
		// Without a scope the object was never started and has no owner, so it is closed directly.
		releaseErr = scope.Close()
	} else {
		releaseErr = common.Close(runtime)
	}
	if releaseErr != nil && lz.logger != nil {
		lz.logger.Error("close lazy outbound [", lz.Tag(), "]: ", releaseErr)
	}
	lz.access.Lock()
	if lz.state == LazyStateRetiring {
		// A Close that landed inside the teardown window wins: the object is terminal, and the next
		// demand must not resurrect it.
		if lz.closing {
			lz.state = LazyStateClosed
		} else {
			lz.state = LazyStateNeverBuilt
		}
	}
	lz.settled = nil
	close(settled)
	owner := lz.owner
	lz.access.Unlock()
	if owner != nil {
		owner.NoteExpansionReleased()
	}
	return true
}

// maxRetirementWaits bounds how many retirements ONE demand may wait through before it gives up with
// a diagnostic. It is a churn guard, not a normal path: an eviction can only start from Ready, so
// after one wait the loop either builds, serves or observes a terminal state. A demand that sees more
// retirements than this is looking at a policy that evicts faster than it can dial, and saying so is
// better than spinning.
const maxRetirementWaits = 8

// acquireRuntime returns the published runtime, building it exactly once under contention.
func (lz *LazyOutbound[T]) acquireRuntime(ctx context.Context) (adapter.Outbound, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	retirementWaits := 0
	for {
		lz.access.Lock()
		switch lz.state {
		case LazyStateReady:
			lz.dialing++
			lz.touchLocked()
			runtime := lz.runtime
			lz.access.Unlock()
			return runtime, nil
		case LazyStateClosed:
			lz.access.Unlock()
			return nil, os.ErrClosed
		case LazyStateRetiring:
			if lz.closing || lz.settled == nil {
				// A Close owns this retirement. It is terminal, so a demand must fail rather than
				// wait for a rebuild that will never be allowed.
				lz.access.Unlock()
				return nil, os.ErrClosed
			}
			// An EVICTION owns this retirement: the node is going back to its recipe and will be
			// buildable again the moment the teardown settles. Refusing here - returning
			// os.ErrClosed - killed a demand for an object that was about to become usable, which is
			// a user-visible failure the eviction caused. Wait for the teardown instead, bounded by
			// the CALLER's context so a slow teardown can never turn this into a stalled dial.
			settled := lz.settled
			lz.access.Unlock()
			retirementWaits++
			if retirementWaits > maxRetirementWaits {
				return nil, E.New("lazy outbound ", lz.Tag(), " is being retired repeatedly by its reclaim policy")
			}
			select {
			case <-settled:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case LazyStateBuilding:
			settled := lz.settled
			lz.access.Unlock()
			select {
			case <-settled:
				lz.access.Lock()
				state := lz.state
				buildErr := lz.buildErr
				lz.access.Unlock()
				switch state {
				case LazyStateReady:
					continue
				case LazyStateClosed, LazyStateRetiring:
					return nil, os.ErrClosed
				}
				if buildErr != nil {
					return nil, buildErr
				}
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		default: // LazyStateNeverBuilt
			if lz.builder == nil {
				lz.access.Unlock()
				return nil, E.New("lazy outbound ", lz.Tag(), " has no builder")
			}
			if err := ctx.Err(); err != nil {
				lz.access.Unlock()
				return nil, err
			}
			lz.state = LazyStateBuilding
			lz.builds++
			// The node lifetime context is created HERE, not at construction: a never-dialled node
			// needs none, and the context object is a per-node cost a 10 000-node profile must not
			// pay for nodes it never expands.
			lifetime, cancel := context.WithCancel(lz.baseContext)
			lz.lifetime = lifetime
			lz.cancel = cancel
			settled := make(chan struct{})
			lz.settled = settled
			recipe := lz.recipe
			builder := lz.builder
			highestStage := lz.highestStage
			started := lz.started
			haveIdlePolicy := lz.haveIdlePolicy
			keepIdle := lz.keepIdle
			lz.access.Unlock()
			return lz.buildRuntime(lifetime, recipe, builder, highestStage, started, haveIdlePolicy, keepIdle)
		}
	}
}

// releaseUnpublished is the ONE ownership rule for a build result that will not be published. Every
// exit from buildRuntime that does not publish goes through it, which is what makes "exactly one
// cleanup, never two" a property of the code rather than of each branch remembering it.
//
// The criterion is the same one the ready path uses for Close and Evict:
//
//	buildScope != nil  the built object was STARTed, and its per-build scope owns its teardown: the
//	                   cleanups it registered run, exactly as the eager lifecycle's release does.
//	                   common.Close is NOT also called - calling both would run the protocol's close
//	                   path on top of the cleanups that already released the same resources.
//	buildScope == nil  the object was never started, so it has no owner at all: close it directly.
//	                   This is the branch an EARLY retirement (retired while the builder ran, before
//	                   Start) takes, and the one a builder that returns (object, error) takes.
//
// `runtime` may be nil, and common.Close handles that.
func (lz *LazyOutbound[T]) releaseUnpublished(runtime adapter.Outbound, buildScope *adapter.Scope) {
	if buildScope != nil {
		if err := buildScope.Close(); err != nil && lz.logger != nil {
			lz.logger.Error("release lazy outbound [", lz.Tag(), "]: ", err)
		}
		return
	}
	if runtime == nil {
		return
	}
	if err := common.Close(runtime); err != nil && lz.logger != nil {
		lz.logger.Error("close late lazy outbound [", lz.Tag(), "]: ", err)
	}
}

// buildRuntime runs the builder and publishes its result unless the object was retired meanwhile.
func (lz *LazyOutbound[T]) buildRuntime(lifetime context.Context, recipe T, builder LazyBuilder[T], highestStage adapter.StartStage, started bool, haveIdlePolicy bool, keepIdle bool) (adapter.Outbound, error) {
	runtime, err := builder(lifetime, recipe)
	var buildScope *adapter.Scope
	if err == nil && runtime == nil {
		err = E.New("lazy outbound ", lz.Tag(), " builder returned no outbound")
	}
	// Check the retirement decision BEFORE starting the built object. A node closed while the build
	// was in flight must not get a runtime started on its behalf: nothing is published, and the
	// constructed object owns no scope resource until Start, so releasing it here is complete.
	if err == nil {
		lz.access.Lock()
		retired := lz.state == LazyStateClosed || lz.state == LazyStateRetiring
		lz.access.Unlock()
		if retired {
			lz.finishLateBuild()
			// The build SUCCEEDED but the node was retired while it ran: nothing is published, so
			// the constructed object has no owner left. Start has not run, so there is no scope to
			// close - the object itself is the only thing to release.
			lz.releaseUnpublished(runtime, nil)
			return nil, os.ErrClosed
		}
	}
	if err == nil {
		if _, isLifecycle := runtime.(adapter.Lifecycle); isLifecycle {
			// A per-build scope is what makes eviction possible: the expansion's cleanups are
			// reachable from this scope only, so closing it releases the expansion instead of the
			// parent scope keeping every built object alive through a closure.
			buildScope = adapter.NewScope(lifetime, lz.logger)
			stages := []adapter.StartStage{adapter.StartStateInitialize}
			if started && highestStage > adapter.StartStateInitialize {
				for stage := adapter.StartStateInitialize + 1; stage <= highestStage; stage++ {
					stages = append(stages, stage)
				}
			}
			for _, stage := range stages {
				startErr := runtime.(adapter.Lifecycle).Start(stage, buildScope)
				if startErr != nil {
					err = E.Cause(startErr, "start lazy outbound ", lz.Tag())
					break
				}
			}
		}
		if err == nil && haveIdlePolicy {
			if keeper, isKeeper := runtime.(adapter.IdleConnectionKeeper); isKeeper {
				keeper.SetKeepIdleConnections(keepIdle)
			}
		}
	}
	lz.access.Lock()
	settled := lz.settled
	lz.settled = nil
	late := lz.state == LazyStateClosed || lz.state == LazyStateRetiring
	if err != nil || late {
		var cancel context.CancelFunc
		if late {
			lz.state = LazyStateClosed
		} else {
			lz.state = LazyStateNeverBuilt
			lz.buildErr = err
		}
		cancel = lz.cancel
		lz.lifetime = nil
		lz.cancel = nil
		close(settled)
		lz.access.Unlock()
		if cancel != nil {
			cancel()
		}
		// One cleanup, chosen by ownership: the per-build scope when the object was started, the
		// object itself when it was not. See releaseUnpublished.
		lz.releaseUnpublished(runtime, buildScope)
		if late {
			return nil, os.ErrClosed
		}
		return nil, err
	}
	lz.runtime = runtime
	lz.buildScope = buildScope
	lz.buildErr = nil
	lz.state = LazyStateReady
	lz.dialing++
	lz.touchLocked()
	close(settled)
	lz.access.Unlock()
	// Tell the ONE reclaim owner an expansion appeared. It decides whether the budget is exceeded
	// and whether to sweep; this object never chooses a victim, and a manager-less object (a test or
	// an embedder that constructed it directly) simply has no owner to tell.
	if lz.owner != nil {
		lz.owner.NoteExpansionPublished(lz.baseContext)
	}
	return runtime, nil
}

func (lz *LazyOutbound[T]) finishClose() {
	lz.access.Lock()
	if lz.state == LazyStateRetiring {
		lz.state = LazyStateClosed
	}
	lz.access.Unlock()
}

// finishLateBuild settles the build-in-flight bookkeeping for a result that arrived after Close.
// Nothing is published; the caller simply gets os.ErrClosed.
func (lz *LazyOutbound[T]) finishLateBuild() {
	lz.access.Lock()
	if lz.state == LazyStateRetiring {
		lz.state = LazyStateClosed
	}
	settled := lz.settled
	lz.settled = nil
	cancel := lz.cancel
	lz.lifetime = nil
	lz.cancel = nil
	if settled != nil {
		close(settled)
	}
	lz.access.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (lz *LazyOutbound[T]) releaseRuntime() {
	lz.access.Lock()
	if lz.dialing > 0 {
		lz.dialing--
	}
	lz.touchLocked()
	lz.access.Unlock()
}

// touchLocked records "this node was used now" for the reclamation policy's least-recently-used
// order. The caller holds access. An int64 of unix nanoseconds rather than a time.Time keeps the
// never-dialled node's per-object cost at one word.
func (lz *LazyOutbound[T]) touchLocked() {
	lz.lastUse = time.Now().UnixNano()
}

// lazyConn is the connection a deferred expansion produced, with one extra job: it is the hook that
// says the connection is over, which is how "an open connection pins the expansion" becomes true.
//
// It is transparent on purpose. Read/Write/deadlines/addresses come from the embedded connection,
// and Upstream plus Reader/WriterReplaceable keep the capability walk working through it: common.Cast
// and N.UnwrapWriter follow Upstream, which is how the copy engine still finds CloseWrite (half-close)
// and how the splice path still finds the real SyscallConn. SyscallConn is implemented by delegation
// for callers that assert the interface directly; it reports os.ErrInvalid when the wrapped
// connection is not a syscall.Conn, which is the same answer route's own tracked wrappers give.
type lazyConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *lazyConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

func (c *lazyConn) Upstream() any           { return c.Conn }
func (c *lazyConn) ReaderReplaceable() bool { return true }
func (c *lazyConn) WriterReplaceable() bool { return true }

func (c *lazyConn) SyscallConn() (syscall.RawConn, error) {
	syscallConn, isSyscallConn := c.Conn.(syscall.Conn)
	if !isSyscallConn {
		return nil, os.ErrInvalid
	}
	return syscallConn.SyscallConn()
}

// lazyPacketConn is the packet half of lazyConn.
type lazyPacketConn struct {
	N.NetPacketConn
	once    sync.Once
	release func()
}

func (c *lazyPacketConn) Close() error {
	err := c.NetPacketConn.Close()
	c.once.Do(c.release)
	return err
}

func (c *lazyPacketConn) Upstream() any { return c.NetPacketConn }

func (c *lazyPacketConn) SyscallConn() (syscall.RawConn, error) {
	syscallConn, isSyscallConn := c.NetPacketConn.(syscall.Conn)
	if !isSyscallConn {
		return nil, os.ErrInvalid
	}
	return syscallConn.SyscallConn()
}

// ExpansionLease is the OPTIONAL hand-off lease a deferred object accepts from its manager.
//
// It exists because a release can come from two different places and only one of them was guarded:
// the manager's lease kept a REPLACED object open, but an EVICTION released the expansion without
// asking whether a hand-off had already obtained the object. A hand-off is a window, not an instant -
// the router resolves a chain, takes the object, and only then dials - and the object's own dialing
// counter starts too late to cover it. With this lease the object sees the whole window.
type ExpansionLease interface {
	// AcquireExpansionLease pins the expansion for one hand-off. ok=false means the object is
	// terminal and must not be dialled; the caller must refuse the hand-off rather than use it. The
	// release is idempotent and must be called when the hand-off ends.
	AcquireExpansionLease() (release func(), ok bool)
}

// ExpansionObservation is one deferred node's residency as the reclamation policy sees it.
//
// It is a READ-ONLY snapshot: taking it never builds, dials, or waits for another goroutine. That is
// what lets a diagnostic and a sweep share one query.
type ExpansionObservation struct {
	// Expanded reports whether a runtime is published right now.
	Expanded bool
	// IdleFor is how long the node has been unused: since its last hand-off ended, its last lease
	// was released, or its last connection closed. Zero for a node that was never expanded.
	IdleFor time.Duration
	// Dialing is how many hand-offs are inside DialContext/ListenPacket right now.
	Dialing int
	// Leased is how many manager hand-off leases are outstanding.
	Leased int
	// Conns is how many connections this expansion produced are still open.
	Conns int
	// EstimatedBytes is this protocol's own estimate of what one expansion holds, or zero when it
	// does not estimate. The reclamation policy uses it for the byte budget and falls back to its
	// own documented assumption.
	EstimatedBytes int64
	// State is the lifecycle state, so a refused eviction can say why.
	State LazyState
}

// Idle reports whether nothing is using this expansion. The idle DURATION a policy wants is its own
// decision, so it is not part of this predicate.
func (o ExpansionObservation) Idle() bool {
	return o.Expanded && o.Dialing == 0 && o.Leased == 0 && o.Conns == 0
}

// ExpansionObservation implements the reclamation policy's read side.
func (lz *LazyOutbound[T]) ExpansionObservation() ExpansionObservation {
	lz.access.Lock()
	defer lz.access.Unlock()
	observation := ExpansionObservation{
		Expanded:       lz.state == LazyStateReady,
		Dialing:        lz.dialing,
		Leased:         int(lz.leased),
		Conns:          int(lz.conns),
		EstimatedBytes: int64(lz.expansionEstimate),
		State:          lz.state,
	}
	if lz.lastUse != 0 {
		observation.IdleFor = time.Since(time.Unix(0, lz.lastUse))
	}
	return observation
}

// AcquireExpansionLease implements ExpansionLease: it pins the published expansion for one manager
// hand-off, so the window between "the router obtained this object" and "the dial reached
// DialContext" cannot be used by an eviction to release the runtime that hand-off is about to use.
//
// ok=false is reserved for the TERMINAL state, where the object must not be dialled at all. A node
// that is not expanded yet - or one whose build or eviction is in flight - returns a no-op lease and
// ok=true: the demand is served by the normal path, which waits for a build (or for an eviction to
// settle) instead of failing.
func (lz *LazyOutbound[T]) AcquireExpansionLease() (release func(), ok bool) {
	lz.access.Lock()
	switch lz.state {
	case LazyStateClosed:
		lz.access.Unlock()
		return nil, false
	case LazyStateReady:
		lz.leased++
		lz.touchLocked()
		lz.access.Unlock()
		var once sync.Once
		return func() {
			once.Do(func() {
				lz.access.Lock()
				if lz.leased > 0 {
					lz.leased--
				}
				lz.touchLocked()
				lz.access.Unlock()
			})
		}, true
	default:
		lz.access.Unlock()
		return func() {}, true
	}
}

// SetDeferredReclaimOwner installs the one object that owns the reclamation policy (the Manager).
// The manager calls it for every object its seam produced, so an object built outside a manager stays
// inert: it is still evictable by an explicit caller, it just has no policy owner to notify.
func (lz *LazyOutbound[T]) SetDeferredReclaimOwner(owner *Manager) {
	lz.access.Lock()
	lz.owner = owner
	lz.access.Unlock()
}

// SetExpansionEstimate records the protocol's own per-expansion size estimate in bytes. A protocol
// that does not call it (or passes zero) leaves the policy on its documented assumption, which is the
// honest state for a cost nobody measured.
func (lz *LazyOutbound[T]) SetExpansionEstimate(bytes int64) {
	lz.access.Lock()
	if bytes > 0 {
		lz.expansionEstimate = int32(min(bytes, 1<<31-1))
	} else {
		lz.expansionEstimate = 0
	}
	lz.access.Unlock()
}

// NotifyIdleWalk tells the reclaim owner that the EXISTING idle walk
// (adapter.IdleConnectionReleaser.CloseIdleConnections, the one route/reference.go runs for
// DEEP_IDLE, the memory pass and the resume boundary) reached this node. The owner decides whether
// that means "sweep now"; this object does not create a timer and does not evict anything itself.
func (lz *LazyOutbound[T]) NotifyIdleWalk() {
	lz.access.Lock()
	owner := lz.owner
	lz.access.Unlock()
	if owner != nil {
		owner.RequestDeferredReclaim(lz.baseContext, ReclaimCauseIdleWalk)
	}
}

// SetDeferredOutboundConstructor installs the optional deferred-construction seam.
//
// A nil constructor restores the eager-only behaviour. The seam is per Manager, so an embedder (or a
// per-Box policy) decides whether it applies, and a Manager that never installs it behaves exactly
// as before - this is the property that makes the feature opt-in rather than a rewrite.
//
// Installing the seam also arms the DEFAULT reclamation policy: deferring construction without
// reclaiming is a net memory loss as soon as anything dials every node (see reclaim.go), so the two
// cannot be enabled separately by accident. A caller that wants the object layer without a policy
// calls DisableDeferredReclaim explicitly, which is a decision it then owns.
func (m *Manager) SetDeferredOutboundConstructor(constructor DeferredOutboundConstructor) {
	if m == nil {
		return
	}
	m.access.Lock()
	m.deferredOutbound = constructor
	m.access.Unlock()
	if constructor != nil {
		state := &m.deferredReclaim
		state.access.Lock()
		if !state.enabled && state.policy == (DeferredReclaimPolicy{}) {
			state.policy = DefaultDeferredReclaimPolicy()
			state.enabled = true
		}
		state.access.Unlock()
	}
}

// constructOutbound runs the deferred seam when one is installed and falls back to the registry -
// including for an error, because a handled=false result means "the eager constructor owns this
// shape", and a handled=true error is the same error the eager constructor would have returned at
// this stage.
func (m *Manager) constructOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any, owner string) (adapter.Outbound, error) {
	m.access.RLock()
	constructor := m.deferredOutbound
	m.access.RUnlock()
	if constructor != nil {
		deferred, handled, err := constructor(ctx, router, logger, tag, outboundType, options, owner)
		if handled {
			m.attachDeferredOwner(deferred)
			return deferred, err
		}
	}
	return m.registry.CreateOutbound(ctx, router, logger, tag, outboundType, options)
}

// attachDeferredOwner is the seam's other half: the object has to be able to tell this manager that an
// expansion appeared or that the idle walk reached it, and only the manager knows it is the policy
// owner. It is a type assertion on an object the seam just returned, so it costs nothing for an eager
// result.
func (m *Manager) attachDeferredOwner(deferred adapter.Outbound) {
	if deferred == nil {
		return
	}
	if reclaimed, isReclaimed := deferred.(interface{ SetDeferredReclaimOwner(owner *Manager) }); isReclaimed {
		reclaimed.SetDeferredReclaimOwner(m)
	}
}
