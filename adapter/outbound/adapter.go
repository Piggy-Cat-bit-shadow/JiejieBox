package outbound

import (
	"github.com/sagernet/sing-box/option"
)

type Adapter struct {
	outboundType string
	outboundTag  string
	network      []string
	dependencies []string
	// domainResolver is the DNS server tag this outbound resolves names through, or "".
	//
	// It is NOT in dependencies, and the two must not be confused: dependencies is the start-order
	// edge (detour, group members) and it names OUTBOUNDS, while this names a DNS TRANSPORT. Keeping
	// them apart is what lets the outbound sort treat a resolver as a peer of another namespace
	// instead of failing with "dependency not found".
	//
	// It is exported through DomainResolverReference because the DNS transport manager needs the
	// outbound half of the cross-kind graph it cannot see: its own transports declare a detour into
	// an outbound through References(), and the closing edge - that outbound resolving through that
	// transport - lives only here. See cross_kind_cycle.go.
	domainResolver string
	// serverIsDomain records whether this outbound's OWN server address is a domain, which is the
	// condition under which the runtime builds a ResolveDialer and therefore the condition under
	// which `route.default_domain_resolver` is read at all.
	//
	// It is deliberately NOT `domainResolver != ""`: an object may name a resolver it never uses, and
	// an object with no resolver of its own may still resolve its server through the default. The
	// cross-kind graph needs the second fact to decide whether the default is an edge, and conflating
	// the two either misses a real loop or invents one.
	serverIsDomain bool
}

func NewAdapter(outboundType string, outboundTag string, network []string, dependencies []string) Adapter {
	return Adapter{
		outboundType: outboundType,
		outboundTag:  outboundTag,
		network:      network,
		dependencies: dependencies,
	}
}

// NewAdapterWithDialerOptions captures the resolver facts an outbound's dialer options carry.
//
// `serverIsDomain` is variadic so that only a caller that KNOWS the answer supplies it; an outbound
// that has not established the fact reports false, which is the conservative direction for a check
// whose job is to REFUSE.
func NewAdapterWithDialerOptions(outboundType string, outboundTag string, network []string, dialOptions option.DialerOptions, serverIsDomain ...bool) Adapter {
	var dependencies []string
	if dialOptions.Detour != "" {
		dependencies = []string{dialOptions.Detour}
	}
	var domainResolver string
	if dialOptions.DomainResolver != nil {
		domainResolver = dialOptions.DomainResolver.Server
	}
	adapter := NewAdapter(outboundType, outboundTag, network, dependencies)
	adapter.domainResolver = domainResolver
	adapter.serverIsDomain = len(serverIsDomain) > 0 && serverIsDomain[0]
	return adapter
}

// DomainResolverReference reports the DNS server tag this outbound resolves names through, or "".
//
// The value is taken from the dialer options the outbound was constructed with, which is the same
// source common/dialer reads when it builds the ResolveDialer. Declaring it here rather than
// re-deriving it from the options later is what makes the cross-kind graph a view of the objects
// that will actually dial, so the two cannot disagree.
func (a *Adapter) DomainResolverReference() string {
	return a.domainResolver
}

// ResolvesDomainServer reports whether this outbound's own server address is a domain.
//
// It answers "would this object build a ResolveDialer", which is what decides whether
// `route.default_domain_resolver` is ever consulted on its behalf. `DomainResolverReference() != ""`
// is NOT a substitute for it, and using one for the other either misses a real loop or invents one.
func (a *Adapter) ResolvesDomainServer() bool {
	return a.serverIsDomain
}

func (a *Adapter) Type() string {
	return a.outboundType
}

func (a *Adapter) Tag() string {
	return a.outboundTag
}

func (a *Adapter) Network() []string {
	return a.network
}

func (a *Adapter) Dependencies() []string {
	return a.dependencies
}
