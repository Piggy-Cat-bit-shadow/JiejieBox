package httpclient

import (
	"context"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/service"
)

var (
	_ adapter.HTTPClientManager = (*Manager)(nil)
	_ adapter.LifecycleService  = (*Manager)(nil)
	_ adapter.Referrer          = (*Manager)(nil)
)

type Manager struct {
	ctx                      context.Context
	logger                   log.ContextLogger
	access                   sync.Mutex
	defines                  map[string]option.HTTPClient
	sharedTransports         map[string]*sharedManagedTransport
	managedTransports        []*ManagedTransport
	defaultTag               string
	defaultTransport         *sharedManagedTransport
	defaultTransportFallback func() (*ManagedTransport, error)
}

type sharedManagedTransport struct {
	managed *ManagedTransport
	shared  *sharedState
}

func NewManager(ctx context.Context, logger log.ContextLogger, clients []option.HTTPClient, defaultHTTPClient string) *Manager {
	defines := make(map[string]option.HTTPClient, len(clients))
	for _, client := range clients {
		defines[client.Tag] = client
	}
	defaultTag := defaultHTTPClient
	if defaultTag == "" && len(clients) > 0 {
		defaultTag = clients[0].Tag
	}
	return &Manager{
		ctx:              ctx,
		logger:           logger,
		defines:          defines,
		sharedTransports: make(map[string]*sharedManagedTransport),
		defaultTag:       defaultTag,
	}
}

func (m *Manager) Initialize(defaultTransportFallback func() (*ManagedTransport, error)) {
	m.defaultTransportFallback = defaultTransportFallback
}

func (m *Manager) Name() string {
	return "http-client"
}

func (m *Manager) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	if m.defaultTag != "" {
		sharedTransport, err := m.resolveShared(m.defaultTag)
		if err != nil {
			return E.Cause(err, "resolve default http client")
		}
		m.defaultTransport = sharedTransport
	}
	// Every declared client's `detour` is checked against the outbound namespace HERE, at start.
	//
	// # Why the reference and not the transport
	//
	// `dialer.NewDetour` resolves the tag LAZILY: it stores the name and only asks the outbound
	// manager on the first dial (`common/dialer/detour.go`, `DetourDialer.init`). Every other site
	// that accepts a detour therefore refuses a dangling one at start - a DNS transport through
	// `dialer.InitializeDetour`, an outbound through the start-order sort's dependency check, a
	// rule-set through its `http_client` lookup - while an http client STARTED with one and failed
	// later, on the first request, with the same message the others give up front:
	//
	//	dns transport   REFUSED  "outbound detour not found: ghost"
	//	outbound        REFUSED  "dependency[ghost] not found for outbound[s]"
	//	http client     STARTED  (before this check)
	//
	// The fix is deliberately NOT "resolve every defined client here". `resolveShared` constructs a
	// transport, and a client that nothing references would then be built at every start - a new
	// failure mode for configurations that work today, which is the false-deny class this whole
	// audit exists to remove. Asking the outbound manager for a tag constructs nothing.
	// The manager is read from the context because this type does not own it: `adapter.HTTPClientManager`
	// is registered before the outbound manager in some construction orders, and the same context
	// lookup is what `dialer.NewWithOptions` uses to build the detour dialer itself - so the check
	// asks the SAME registry the dial will.
	outboundManager := service.FromContext[adapter.OutboundManager](m.ctx)
	for tag, define := range m.defines {
		detour := define.Detour
		if detour == "" {
			continue
		}
		if outboundManager == nil {
			return E.New("http_client[", tag, "] declares detour ", detour,
				", and no outbound manager is available to resolve it")
		}
		detourObject, loaded := outboundManager.Outbound(detour)
		if !loaded {
			return E.New("http_client[", tag, "] declares detour ", detour,
				", and no outbound or endpoint with that tag exists; the client would fail on its ",
				"first request instead")
		}
		// The reference EXISTING is not the same as the reference WORKING. A detour that cannot
		// carry the transports this client's CONFIGURED version needs fails every request, and the
		// configured version is the only thing that decides which those are - see
		// HTTPClientTransportRequirement for why `version: 3` needs UDP alone, or UDP AND TCP, purely
		// because of disable_version_fallback.
		//
		// This is a static preflight, so it only refuses what is PROVABLE: an object that reports no
		// networks at all is left alone by CheckDetourCapability rather than assumed to carry none.
		if capabilityErr := CheckDetourCapability(tag, detourObject, HTTPClientTransportRequirement(define.Options())); capabilityErr != nil {
			// The reference exists; the detour simply cannot serve every transport this client
			// needs. Those requests fail at run time, and refusing the start here would also refuse
			// every other request the configuration can serve - including clients it never uses.
			// Reported once, at warn level.
			m.logger.Warn(capabilityErr)
		}
	}
	scope.Add(m.close)
	return nil
}

func (m *Manager) DefaultTransport() adapter.HTTPTransport {
	m.access.Lock()
	defer m.access.Unlock()
	if m.defaultTransport == nil && m.defaultTransportFallback != nil {
		transport, err := m.defaultTransportFallback()
		if err != nil {
			m.logger.Error(E.Cause(err, "create default http client"))
			return nil
		}
		m.managedTransports = append(m.managedTransports, transport)
		m.defaultTransport = &sharedManagedTransport{
			managed: transport,
			shared:  &sharedState{},
		}
	}
	if m.defaultTransport == nil {
		return nil
	}
	return newSharedRef(m.defaultTransport.managed, m.defaultTransport.shared)
}

func (m *Manager) ResolveTransport(ctx context.Context, logger logger.ContextLogger, options option.HTTPClientOptions) (adapter.HTTPTransport, error) {
	if options.Tag != "" {
		if options.ResolveOnDetour {
			define, loaded := m.defines[options.Tag]
			if !loaded {
				return nil, E.New("http_client not found: ", options.Tag)
			}
			resolvedOptions := define.Options()
			resolvedOptions.ResolveOnDetour = true
			transport, err := NewTransport(ctx, logger, options.Tag, resolvedOptions)
			if err != nil {
				return nil, err
			}
			m.trackTransport(transport)
			return transport, nil
		}
		sharedTransport, err := m.resolveShared(options.Tag)
		if err != nil {
			return nil, err
		}
		return newSharedRef(sharedTransport.managed, sharedTransport.shared), nil
	}
	transport, err := NewTransport(ctx, logger, "", options)
	if err != nil {
		return nil, err
	}
	m.trackTransport(transport)
	return transport, nil
}

func (m *Manager) trackTransport(transport *ManagedTransport) {
	m.access.Lock()
	defer m.access.Unlock()
	m.managedTransports = append(m.managedTransports, transport)
}

func (m *Manager) resolveShared(tag string) (*sharedManagedTransport, error) {
	m.access.Lock()
	defer m.access.Unlock()
	if sharedTransport, loaded := m.sharedTransports[tag]; loaded {
		return sharedTransport, nil
	}
	define, loaded := m.defines[tag]
	if !loaded {
		return nil, E.New("http_client not found: ", tag)
	}
	transport, err := NewTransport(m.ctx, m.logger, tag, define.Options())
	if err != nil {
		return nil, E.Cause(err, "create shared http_client[", tag, "]")
	}
	sharedTransport := &sharedManagedTransport{
		managed: transport,
		shared:  &sharedState{},
	}
	m.sharedTransports[tag] = sharedTransport
	m.managedTransports = append(m.managedTransports, transport)
	return sharedTransport, nil
}

func (m *Manager) ResetNetwork() {
	m.access.Lock()
	defer m.access.Unlock()
	for _, transport := range m.managedTransports {
		transport.Reset()
	}
}

// CloseIdleConnections retires the idle connections of every transport this manager owns.
//
// # Why the manager needs this, and why it is not ResetNetwork
//
// These pools serve provider refresh, remote rule sets, dashboard and API traffic - work that is
// speculative and that an idle-only release cannot harm. They were the one reusable resource the
// reference manager's walk could not reach, so a resume boundary retired every pool except these,
// and the first provider refresh after an unlock could be handed a connection that had been dead
// since the device went to sleep. The gap was latency rather than correctness, because those fetches
// are staggered after a wake anyway, but it was a real gap and the only thing it needed was a method
// here: the walk decides WHEN, this decides WHAT, and every transport already knows how to drop a
// connection that has no request on it.
//
// The distinction from ResetNetwork is the same one ManagedTransport.CloseIdleConnections documents
// at length: a reset swaps the inner transport out, which is right for a network change - the old
// transport belongs to the network being left - and wrong for a resume, where the network is
// unchanged and replacing the transport would discard verdicts that are still true and race a live
// stream on the old epoch. So this walks the managed transports, not the shared refs: the ref count
// answers "is a CONSUMER holding this", which is the trim's question, while the question here is "is
// a REQUEST on this connection", and the transport's own CloseIdleConnections is the only thing that
// can answer it. `sharedRef.CloseIdleConnections` deliberately does not appear here for that reason.
//
// Each managed transport is idle-only by its own contract, and the walk holds no lock while calling
// them: `CloseIdleConnections` on a transport that is mid-`Reset` from a network transition sees a
// nil epoch and returns, which is the same no-op the memory pass already relies on.
func (m *Manager) CloseIdleConnections() {
	m.access.Lock()
	transports := make([]*ManagedTransport, len(m.managedTransports))
	copy(transports, m.managedTransports)
	m.access.Unlock()
	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
}

func (m *Manager) close() error {
	m.access.Lock()
	defer m.access.Unlock()
	var err error
	for _, transport := range m.managedTransports {
		err = E.Append(err, transport.close(), func(err error) error {
			return E.Cause(err, "close http client")
		})
	}
	m.managedTransports = nil
	m.sharedTransports = nil
	return err
}

func (m *Manager) References() []string {
	m.access.Lock()
	defer m.access.Unlock()
	var references []string
	var needDefault bool
	for _, transport := range m.managedTransports {
		if transport.detour != "" {
			references = append(references, transport.detour)
		} else if transport.defaultOutbound {
			needDefault = true
		}
	}
	if needDefault {
		outboundManager := service.FromContext[adapter.OutboundManager](m.ctx)
		if outboundManager != nil {
			defaultOutbound := outboundManager.Default()
			if defaultOutbound != nil {
				references = append(references, defaultOutbound.Tag())
			}
		}
	}
	return references
}
