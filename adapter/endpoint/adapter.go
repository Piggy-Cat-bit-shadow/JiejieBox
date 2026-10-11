package endpoint

import "github.com/sagernet/sing-box/option"

type Adapter struct {
	endpointType string
	endpointTag  string
	network      []string
	dependencies []string
	// domainResolver is the explicit `domain_resolver` this endpoint was constructed with, captured
	// from its real DialerOptions rather than re-derived later.
	//
	// # Why the adapter owns this fact
	//
	// The cross-kind cycle graph walks ADAPTER OBJECTS. A fact that lives only in the configuration
	// text, or only in a concrete endpoint type, is invisible to it - and a graph that re-parses the
	// config or type-switches on the protocol is a second model of the runtime rather than a view of
	// it. `adapter/outbound.Adapter` already publishes the same fact for outbounds
	// (`DomainResolverReference`), so this is the endpoint half of one contract, not a new mechanism.
	domainResolver string
	// serverIsDomain records whether this endpoint's OWN remote/server address is a domain, which is
	// the condition under which the runtime builds a ResolveDialer and therefore the condition under
	// which `route.default_domain_resolver` is read at all.
	//
	// It is deliberately NOT `domainResolver != ""`: those are different facts, and conflating them
	// makes the graph charge an edge to an object that never consults the default.
	serverIsDomain bool
}

func NewAdapter(endpointType string, endpointTag string, network []string, dependencies []string) Adapter {
	return Adapter{
		endpointType: endpointType,
		endpointTag:  endpointTag,
		network:      network,
		dependencies: dependencies,
	}
}

// NewAdapterWithDialerOptions captures the resolver facts an endpoint's dialer options carry.
//
// `serverIsDomain` is variadic so that only a caller that KNOWS the answer supplies it. An endpoint
// whose remote address cannot be a domain, or a caller that has not established the fact, gets
// `false` - the conservative direction for a check whose job is to REFUSE: charging an object that
// may never read the network default is the false deny this distinction exists to prevent.
func NewAdapterWithDialerOptions(endpointType string, endpointTag string, network []string, dialOptions option.DialerOptions, serverIsDomain ...bool) Adapter {
	var dependencies []string
	if dialOptions.Detour != "" {
		dependencies = []string{dialOptions.Detour}
	}
	var domainResolver string
	if dialOptions.DomainResolver != nil {
		domainResolver = dialOptions.DomainResolver.Server
	}
	adapter := NewAdapter(endpointType, endpointTag, network, dependencies)
	adapter.domainResolver = domainResolver
	adapter.serverIsDomain = len(serverIsDomain) > 0 && serverIsDomain[0]
	return adapter
}

// DomainResolverReference reports the DNS server tag this endpoint resolves names through, or "".
//
// This is the endpoint half of the contract `adapter/outbound.Adapter` already implements, and it
// exists because the cross-kind graph walks adapters: without it an endpoint's own `domain_resolver`
// was invisible to the cycle check even though the field is on every tunnel endpoint's options and
// the dial path really reads it (`protocol/wireguard/endpoint.go`, `protocol/openvpn/client.go`).
func (a *Adapter) DomainResolverReference() string {
	return a.domainResolver
}

// ResolvesDomainServer reports whether this endpoint's own remote address is a domain.
//
// It answers "would this object build a ResolveDialer", which is what decides whether
// `route.default_domain_resolver` is ever consulted on its behalf. `domainResolver != ""` is NOT a
// substitute: an object may name a resolver it never uses, and an object with no resolver may still
// resolve its server through the default.
func (a *Adapter) ResolvesDomainServer() bool {
	return a.serverIsDomain
}

func (a *Adapter) Type() string {
	return a.endpointType
}

func (a *Adapter) Tag() string {
	return a.endpointTag
}

func (a *Adapter) Network() []string {
	return a.network
}

func (a *Adapter) Dependencies() []string {
	return a.dependencies
}
