package dialer

import (
	"context"
	"net"
	"net/netip"
	"time"

	C "github.com/sagernet/sing-box/constant"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func DialSerialNetwork(ctx context.Context, dialer N.Dialer, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	if len(destinationAddresses) == 0 {
		if !destination.IsIP() {
			panic("invalid usage")
		}
		destinationAddresses = []netip.Addr{destination.Addr}
	}
	if parallelDialer, isParallel := dialer.(ParallelNetworkDialer); isParallel {
		return parallelDialer.DialParallelNetwork(ctx, network, destination, destinationAddresses, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
	}
	var errors []error
	if parallelDialer, isParallel := dialer.(ParallelInterfaceDialer); isParallel {
		for _, address := range destinationAddresses {
			conn, err := parallelDialer.DialParallelInterface(ctx, network, M.SocksaddrFrom(address, destination.Port), strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
			if err == nil {
				return conn, nil
			}
			errors = append(errors, err)
		}
	} else {
		for _, address := range destinationAddresses {
			conn, err := dialer.DialContext(ctx, network, M.SocksaddrFrom(address, destination.Port))
			if err == nil {
				return conn, nil
			}
			errors = append(errors, err)
		}
	}
	return nil, E.Errors(errors...)
}

// DialParallelNetwork races the candidate addresses for one connection.
//
// # What changed
//
// This used to split candidates into two family groups and call DialSerialNetwork on each.
// Within a family the addresses were therefore attempted one at a time, so a blackholed
// address cost the whole connect timeout before the next was tried; and because an empty
// family fell through to pure serial, a connection whose candidates were all in one family
// had no racing at all - which is the common case whenever only one family resolved.
//
// Candidates are now interleaved and scheduled by the shared candidate scheduler: the first
// starts immediately, one more starts per fallback delay, and a definite path failure
// advances the schedule at once instead of waiting out a delay for an answer already known
// to be no.
//
// The interface and strategy arguments are unchanged; they select the per-attempt dialer.
// The signature is unchanged so existing callers keep working. What changed is that the
// function now finds the long-lived family health owner itself rather than being handed one.
func DialParallelNetwork(ctx context.Context, dialer ParallelInterfaceDialer, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, preferIPv6 bool, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	// Bind the scheduler to the dialer's own family health, when the chain leads to an owner.
	//
	// This path - the direct outbound's DialParallelNetwork, reached by the actionResolve
	// candidate list and by flow-route candidates - previously passed health: nil, so it never
	// shared the verdict the hostname path accumulates. The two paths learned about a broken
	// family independently, and a literal destination never benefited from what a hostname
	// connection had already discovered.
	//
	// The owner is DISCOVERED rather than passed, so no caller has to know about it and the
	// public signature is untouched.
	if owner, found := findFamilyHealthOwner(dialer); found {
		return dialParallelNetwork(ctx, dialer, network, destination, destinationAddresses, preferIPv6, strategy, interfaceType, fallbackInterfaceType, fallbackDelay, owner.newDualStackScheduler(fallbackDelay))
	}
	// No owner in the chain: a caller-supplied dialer. Racing still works; there is simply no
	// history to consult. It must not panic, and it must not invent a per-call health that
	// could never accumulate anything.
	return dialParallelNetwork(ctx, dialer, network, destination, destinationAddresses, preferIPv6, strategy, interfaceType, fallbackInterfaceType, fallbackDelay, &candidateScheduler{fallbackDelay: fallbackDelay})
}

// findFamilyHealthOwner walks the dialer chain looking for a long-lived health owner.
//
// The chain is short and its wrappers are known: a resolveDialer (or its parallel variant)
// wraps the real dialer, which for the direct outbound is a *DefaultDialer. Each step is
// unwrapped explicitly rather than by reflection, so adding a wrapper is a deliberate act and
// cannot silently break the lookup - and anything else that speaks the Upstream protocol is
// followed too, which is what lets a chain that passes through another package's OUTBOUND reach
// the dialer underneath it. See unwrapDialer.
func findFamilyHealthOwner(dialer N.Dialer) (familyHealthOwner, bool) {
	for depth := 0; depth < chainWalkDepth && dialer != nil; depth++ {
		if owner, ok := dialer.(familyHealthOwner); ok {
			return owner, true
		}
		next, ok := unwrapDialer(dialer)
		if !ok {
			return nil, false
		}
		dialer = next
	}
	return nil, false
}

// chainWalkDepth bounds every walk down a dialer chain.
//
// A chain is two or three hops in production. The bound exists because a wrapper whose Upstream
// returns itself, or two wrappers that return each other, would otherwise spin forever on a dial path
// where the fault would look like a hang rather than a bug.
const chainWalkDepth = 8

// unwrapDialer takes one step down a dialer chain.
//
// # The two known wrappers, by their fields
//
// `resolveDialer` and `resolveParallelNetworkDialer` are this package's own, and unwrapping them by the
// field they hold is exact: it cannot be satisfied by an object that merely looks similar.
//
// # Everything else, by the Upstream protocol - except a detour
//
// An outbound in ANOTHER package is not a wrapper this package can name - `protocol/direct.Outbound`
// holds the chain it dials through, and `common/dialer` cannot import it. The Upstream protocol is how
// that package says where the dialer below it is, and it is the same protocol `common.Cast` walks. So a
// chain that passes through an outbound reaches the real owner instead of stopping at the hop and
// concluding that this machine has no opinion about address families.
//
// A `*DetourDialer` is REFUSED rather than followed, for two independent reasons:
//
//	it is not pure          its Upstream() calls Dialer(), which triggers the lazy outbound resolution. A
//	                        capability walk must not perform one as a side effect of merely asking - the
//	                        same rule `resolvedDetour` states for the preflight.
//	it is semantically wrong what lies below a detour is the PEER's dial path. A family verdict or a
//	                        health history found there describes how this machine reaches the proxy, not
//	                        how the destination is reached, so applying it to a destination address would
//	                        refuse connections the peer can serve.
//
// The refusal costs one exotic shape - a detour whose target is a non-empty DIRECT outbound - the verdict
// it could have used, and that shape had no verdict before this walk existed either.
//
// The concrete wrappers are matched first, so nothing that resolved before resolves differently now.
func unwrapDialer(dialer N.Dialer) (N.Dialer, bool) {
	switch wrapper := dialer.(type) {
	case *resolveDialer:
		return wrapper.dialer, true
	case *resolveParallelNetworkDialer:
		return wrapper.dialer, true
	case *DetourDialer:
		return nil, false
	case interface{ Upstream() any }:
		next, isDialer := wrapper.Upstream().(N.Dialer)
		if !isDialer || next == nil {
			return nil, false
		}
		return next, true
	default:
		return nil, false
	}
}

// dialParallelNetwork is the explicit form, used by callers that already hold a scheduler.
func dialParallelNetwork(ctx context.Context, dialer ParallelInterfaceDialer, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, preferIPv6 bool, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration, scheduler *candidateScheduler) (net.Conn, error) {
	if len(destinationAddresses) == 0 {
		if !destination.IsIP() {
			panic("invalid usage")
		}
		destinationAddresses = []netip.Addr{destination.Addr}
	}

	domainStrategy := C.DomainStrategyPreferIPv4
	if preferIPv6 {
		domainStrategy = C.DomainStrategyPreferIPv6
	}
	plan := planCandidates(destinationAddresses, netip.Addr{}, domainStrategy)

	if scheduler == nil {
		scheduler = &candidateScheduler{fallbackDelay: fallbackDelay}
	}
	conn, _, err := scheduler.dial(ctx, plan, func(attemptCtx context.Context, address netip.Addr) (net.Conn, error) {
		// Each attempt keeps the full configuration the caller supplied: the network
		// strategy, the interface selection and the fallback interface selection. Racing
		// chooses WHICH address to try, never how to try it.
		attemptDestination := M.SocksaddrFrom(address, destination.Port)
		return dialer.DialParallelInterface(attemptCtx, network, attemptDestination, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
	})
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func ListenSerialNetworkPacket(ctx context.Context, dialer N.Dialer, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error) {
	if len(destinationAddresses) == 0 {
		if !destination.IsIP() {
			panic("invalid usage")
		}
		destinationAddresses = []netip.Addr{destination.Addr}
	}
	if parallelDialer, isParallel := dialer.(ParallelNetworkDialer); isParallel {
		return parallelDialer.ListenSerialNetworkPacket(ctx, destination, destinationAddresses, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
	}
	// # Why this loop is the UDP path's only fallback, and what makes it work
	//
	// Each address here gets its own socket attempt, and the FIRST address whose socket creation
	// succeeds wins outright - there is no second phase, no health check, and no way to replace a
	// winner. That is deliberate for a packet listener: unlike a stream connect, a UDP "success" is
	// not a verified outcome, so racing two packet sockets would deliver duplicate datagrams and
	// declare a winner that has never sent anything.
	//
	// The consequence is the shape of the real user-reported failure. On Windows a UDP socket to an
	// unreachable IPv6 network is created SUCCESSFULLY and only fails at the first send, so an
	// unrefused global IPv6 address wins this loop and the client is handed a dead connection - the
	// `open UDP connection to [2606:...]` and `send peer batch: unreachable network` lines in the log.
	//
	// What makes the loop sufficient is that a refusal happens BEFORE socket creation - but the gate that
	// does it is NOT the one in DefaultDialer.ListenPacket, and naming it there was wrong.
	//
	// The loop below reaches `ListenSerialInterfacePacket`, which with a non-nil strategy never calls
	// ListenPacket at all: it creates the interface-bound socket itself. So ListenPacket's gate covers the
	// ELSE branch and the direct callers, and nothing more. The gate the branch below actually depends on
	// is inside `listenSerialInterfacePacketEpoch`, after the interface selection and before the socket.
	//
	// That gate was once MISSING, and the comment above was describing it anyway - which is how a
	// load-bearing refusal can be deleted by a reader who believes another function already provides it.
	// MEASURED before it was added, on a machine with no IPv6 addressing: ListenPacket refused before
	// socket creation (the control), while ListenSerialInterfacePacket returned err=nil and an IPv6 packet
	// socket was created.
	//
	// So the gate is load-bearing twice over: the fast-fail for a stream, and the ONLY thing that can move
	// a packet listener to another family. It lives in the per-attempt dialer, on both branches, which is
	// what makes it impossible for this loop to enter a family the machine cannot reach.
	var errors []error
	if parallelDialer, isParallel := dialer.(ParallelInterfaceDialer); isParallel {
		for _, address := range destinationAddresses {
			conn, err := parallelDialer.ListenSerialInterfacePacket(ctx, M.SocksaddrFrom(address, destination.Port), strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
			if err == nil {
				return conn, address, nil
			}
			errors = append(errors, err)
		}
	} else {
		for _, address := range destinationAddresses {
			conn, err := dialer.ListenPacket(ctx, M.SocksaddrFrom(address, destination.Port))
			if err == nil {
				return conn, address, nil
			}
			errors = append(errors, err)
		}
	}
	return nil, netip.Addr{}, E.Errors(errors...)
}
