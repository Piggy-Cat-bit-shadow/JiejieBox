package dialer

import (
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
)

// LocalDialSurface is the set of dialing capabilities a chain offers for a flow THIS MACHINE dials.
//
// # Why this type exists
//
// The capabilities used to be discovered at the point of use, by type assertion on whatever object a layer
// happened to hold. That is fine while each of them is independent, and it stopped being fine once they
// were not: an outbound stored the chain at the narrowest type an ATTEMPT needs
// (`ParallelInterfaceDialer`), the object inside it was a wrapper that also implemented the richer
// candidate-planning surface (`ParallelNetworkDialer`), and the plannable surface is where sniffed-domain
// recovery for a literal destination lives. The narrower declaration lost the capability, the assertion at
// the point of use silently failed, and the recovery machinery sat one call away while real traffic could
// not reach it. It took two rounds to find, because every test handed the richer object straight to the
// function that wanted it.
//
// Resolving the surface once, at construction, from the object the constructor actually produced, removes
// the possibility: a field cannot be declared at a type that drops a capability it was built with, and a
// missing REQUIRED capability is a construction error instead of a silent degradation.
//
// # What is required and what is optional
//
// Attempt and Network are required, because a chain built for local dialling always has them and because
// the difference between having Network and not having it is not a lost optimisation - it is a flow that
// cannot be moved off an address its own sniffed domain could have replaced.
//
// Resolve and LocalFamily are optional, and that is a statement about the configuration rather than about
// quality: a chain built without a domain resolver has no resolve wrapper, and a leaf that cannot describe
// this machine's addressing has no verdict. Both degrade to "no opinion", which is the direction every
// other optional capability in this package takes.
//
// # Why a detour does not use this
//
// A detour routes to another OUTBOUND instead of creating a socket, so its dialer has no candidate plan and
// no recovery of its own: the destination of a detoured flow is the peer's business, and this machine's
// addressing must never rewrite it. Those chains keep the package helpers, which is a different shape
// rather than a degraded one.
type LocalDialSurface struct {
	// Attempt is the chain as a per-attempt dialer: DialContext and ListenPacket.
	Attempt N.Dialer
	// Network is the candidate-planning surface. Required.
	Network ParallelNetworkDialer
	// Resolve is the resolve wrapper, when the chain has one.
	Resolve ResolveDialer
	// LocalFamily reports whether this machine has a usable path for an address's family, when the chain
	// can answer.
	LocalFamily LocalFamilyDialer
}

// NewLocalDialSurface resolves the surface of a chain built for local dialling, and reports a construction
// error when a required capability is missing.
//
// # Why a missing capability is an error and not a nil field
//
// Every other failure in the constructors that call this is a returned error an operator can read. A nil
// Network, by contrast, would have to be handled at the point of use - which means either a panic or a
// silent fallback to the narrower call, and the silent fallback is exactly the defect this type exists to
// remove. The error names the defect AS a defect, because no configuration can cause it: it means the
// dialer factory stopped producing what it is contracted to produce.
func NewLocalDialSurface(chain N.Dialer) (LocalDialSurface, error) {
	if chain == nil {
		return LocalDialSurface{}, E.New("local dial surface: the dialer factory returned no dialer")
	}
	surface := LocalDialSurface{Attempt: chain}
	network, isNetwork := chain.(ParallelNetworkDialer)
	if !isNetwork {
		return LocalDialSurface{}, E.New("local dial surface: the dialer chain produced an object that does ",
			"not offer the candidate-planning surface a locally dialled flow is scheduled through; without ",
			"it a literal destination cannot be recovered from its sniffed domain, which is the difference ",
			"between a fallback and a dead tunnel. This is a defect in the dialer factory rather than in ",
			"the configuration")
	}
	surface.Network = network
	// The optional capabilities are read from the SAME object, so a chain that has them is not forced to
	// advertise them a second way and cannot be read at a type that drops them.
	if resolver, isResolver := chain.(ResolveDialer); isResolver {
		surface.Resolve = resolver
	}
	if localFamily, isLocalFamily := chain.(LocalFamilyDialer); isLocalFamily {
		surface.LocalFamily = localFamily
	}
	return surface, nil
}
