package outbound

import (
	"context"
	"net"
	"os"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
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
	// done is closed when the build in flight finishes. Waiters block on it rather than starting a
	// second build.
	done chan struct{}
	// buildErr is the last failed build's error. A waiter that arrives after a failed build gets
	// that error instead of immediately starting another doomed attempt; the NEXT demand retries.
	buildErr error
	// builds counts build attempts, including failed ones. It is the observable instrument the
	// lifecycle tests assert on: a correctly single-flighted build must not move for waiters.
	builds int
	// dialing counts hand-offs that are using the runtime right now. Eviction refuses while it is
	// non-zero, so an expansion cannot be released under a dial that already obtained it.
	dialing int
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
	return runtime.DialContext(ctx, network, destination)
}

// ListenPacket builds the node on first demand and hands the packet listener to the expansion.
func (lz *LazyOutbound[T]) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	runtime, err := lz.acquireRuntime(ctx)
	if err != nil {
		return nil, err
	}
	defer lz.releaseRuntime()
	return runtime.ListenPacket(ctx, destination)
}

// Close releases the expansion and makes the object terminal. It is idempotent and safe from any
// goroutine, including while a build is in flight: the in-flight result is then discarded instead of
// published, and the resources it registered are released through its per-build scope.
func (lz *LazyOutbound[T]) Close() error {
	lz.access.Lock()
	switch lz.state {
	case LazyStateClosed, LazyStateRetiring:
		lz.access.Unlock()
		return nil
	case LazyStateBuilding:
		// The builder observes the cancellation and must not publish. Mark the state first so even a
		// builder that ignores its context cannot publish: buildRuntime checks `late` under this same
		// lock before it assigns the runtime.
		lz.state = LazyStateRetiring
		cancel := lz.cancel
		lz.access.Unlock()
		if cancel != nil {
			cancel()
		}
		return nil
	case LazyStateReady:
		lz.state = LazyStateRetiring
		runtime := lz.runtime
		scope := lz.buildScope
		cancel := lz.cancel
		lz.runtime = nil
		lz.buildScope = nil
		lz.lifetime = nil
		lz.cancel = nil
		lz.access.Unlock()
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
// evicted: false means the node was not ready, or a hand-off is using it right now. The node can be
// expanded again by the next demand - that is the whole point of eviction being safe here: a
// recipe can always rebuild, and nothing about the expansion is an identity the caller holds.
//
// It must only be called for a protocol whose expansion holds no resource that outlives the
// hand-off beyond what its Start registered on the scope (a pooled transport or multiplexer would
// have to be drained first). The Trojan prototype satisfies that; the type does not enforce it.
func (lz *LazyOutbound[T]) Evict() bool {
	lz.access.Lock()
	if lz.state != LazyStateReady || lz.dialing > 0 {
		lz.access.Unlock()
		return false
	}
	runtime := lz.runtime
	scope := lz.buildScope
	cancel := lz.cancel
	lz.state = LazyStateRetiring
	lz.runtime = nil
	lz.buildScope = nil
	lz.lifetime = nil
	lz.cancel = nil
	lz.access.Unlock()
	if cancel != nil {
		cancel()
	}
	if scope != nil {
		_ = scope.Close()
	} else {
		_ = common.Close(runtime)
	}
	lz.access.Lock()
	if lz.state == LazyStateRetiring {
		lz.state = LazyStateNeverBuilt
	}
	lz.access.Unlock()
	return true
}

// acquireRuntime returns the published runtime, building it exactly once under contention.
func (lz *LazyOutbound[T]) acquireRuntime(ctx context.Context) (adapter.Outbound, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for {
		lz.access.Lock()
		switch lz.state {
		case LazyStateReady:
			lz.dialing++
			runtime := lz.runtime
			lz.access.Unlock()
			return runtime, nil
		case LazyStateClosed, LazyStateRetiring:
			lz.access.Unlock()
			return nil, os.ErrClosed
		case LazyStateBuilding:
			done := lz.done
			lz.access.Unlock()
			select {
			case <-done:
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
			done := make(chan struct{})
			lz.done = done
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

// buildRuntime runs the builder and publishes its result unless the object was retired meanwhile.
func (lz *LazyOutbound[T]) buildRuntime(lifetime context.Context, recipe T, builder LazyBuilder[T], highestStage adapter.StartStage, started bool, haveIdlePolicy bool, keepIdle bool) (adapter.Outbound, error) {
	runtime, err := builder(lifetime, recipe)
	var buildScope *adapter.Scope
	if err == nil && runtime == nil {
		err = E.New("lazy outbound ", lz.Tag(), " builder returned no outbound")
	}
	// Check the retirement decision BEFORE starting the built object. A node closed while the build
	// was in flight must not get a runtime started on its behalf: nothing is published, and the
	// constructed object owns no scope resource until Start, so dropping it here is complete.
	if err == nil {
		lz.access.Lock()
		retired := lz.state == LazyStateClosed || lz.state == LazyStateRetiring
		lz.access.Unlock()
		if retired {
			lz.finishLateBuild()
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
	done := lz.done
	lz.done = nil
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
		close(done)
		lz.access.Unlock()
		if cancel != nil {
			cancel()
		}
		if buildScope != nil {
			// Release whatever the partial start registered. A failed build must not leak, and a
			// late build must not stay alive behind a closed node.
			_ = buildScope.Close()
		}
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
	close(done)
	lz.access.Unlock()
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
	done := lz.done
	lz.done = nil
	cancel := lz.cancel
	lz.lifetime = nil
	lz.cancel = nil
	if done != nil {
		close(done)
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
	lz.access.Unlock()
}

// SetDeferredOutboundConstructor installs the optional deferred-construction seam.
//
// A nil constructor restores the eager-only behaviour. The seam is per Manager, so an embedder (or a
// per-Box policy) decides whether it applies, and a Manager that never installs it behaves exactly
// as before - this is the property that makes the feature opt-in rather than a rewrite.
func (m *Manager) SetDeferredOutboundConstructor(constructor DeferredOutboundConstructor) {
	if m == nil {
		return
	}
	m.access.Lock()
	m.deferredOutbound = constructor
	m.access.Unlock()
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
			return deferred, err
		}
	}
	return m.registry.CreateOutbound(ctx, router, logger, tag, outboundType, options)
}
