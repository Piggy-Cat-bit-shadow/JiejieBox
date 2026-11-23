package masque

// Releasing the inner stack when a tunnel session RETIRES.
//
// # The gap this closes
//
// transport/device/deferred.go defers building the userspace stack until the first demand and can
// close it again through Deferred.Release, but nothing in the product path called Release: the only
// precondition it could check was its own operation counter, so a caller had to prove the returned
// flows, the outbound queue, the return path and the callbacks had all gone. This file is that
// caller, and it is deliberately the narrowest one that can be proved:
//
//	release at a session's retirement transition, and only there.
//
// # What one session owns, and what it does not
//
// A MASQUE client endpoint has exactly ONE userspace stack (one Deferred facade, created in
// StartStateInitialize, stable for the endpoint's whole life). A tunnel session does not own that
// stack; it is the thing that CARRIES the stack's flows. So ending a session is not permission to
// destroy the stack by itself - what it is, is the only moment at which the stack provably has no
// carrier:
//
//	object                       owner                 released here?
//	---------------------------  --------------------  -------------------------------------------
//	Deferred facade              endpoint / scope      no  - it IS the stable identity
//	inner userspace stack        the facade            YES - Deferred.Release
//	inner flows (stack conns)    the inner stack       with the stack (Close fails them)
//	outbound queue               whoever asked          NOT PROVEN -> Release refuses if one exists
//	return path, MTU, addresses  the facade            no  - facade state, survives the release
//	packet writer                the facade            no  - facade state, survives the release
//	server DNS/routes/PREF64     the endpoint          no  - the server's configuration, not the
//	                                                      session's; replaced by the next capsule
//	tunnel session, HTTP conn    transport/masque     no  - the transport's, and the reconnect
//	                                                      engine needs it to build the next session
//	router callbacks             the inner stack       with the stack; and they carry no reference
//	                                                      to the session, so none can observe one
//
// The two shapes that made the old Release unusable are exactly the two rows that say "survives":
// the return path lives on the facade (so an attached one is not lost), and the flows live on the
// stack (so releasing it is what ends them - nothing else can enumerate them on this side).
//
// # Why an epoch, and not "no local dial"
//
// A CONNECT-IP tunnel may carry REMOTE-INITIATED flows: the peer opens a TCP connection or sends a
// datagram to the address the server assigned us, and the endpoint never dials anything. A tunnel
// with such a flow is perfectly busy, so "no local Dial" is not idleness and must never release
// anything. What is released here is not an idle stack: it is the stack of a session that has ENDED.
//
// The proof that only a retired session's stack can be released is an epoch, not a counter:
//
//	sessionLifecycle.begin()   a session became current        (transport/masque: connect published)
//	sessionLifecycle.retire()  that session ended              (transport/masque: run() returned)
//
// begin and retire are delivered by transport/masque on the SESSION LOOP goroutine, in that order,
// one pair per session, and a new session's begin cannot be delivered before the previous session's
// run() has returned (the loop is serial). The retirement transition therefore observes
// "established==true and this is the newest epoch", then records "no session is current" BEFORE it
// calls Release, and it holds the state machine's lock across the release, so a begin that arrives
// later cannot interleave: it waits, and when it proceeds the epoch is higher and nothing of the
// released stack is reachable by it.
//
// # The residual race, and why it is safe
//
// A business dial can be running while a session retires. Two orderings exist and both are safe:
//
//	dial increments the facade's in-flight count first -> Release refuses, nothing is torn down;
//	Release clears the published generation first      -> the dial's beginOp sees a generation that
//	                                                      is no longer published, retries, and builds
//	                                                      the next one.
//
// Both are the facade's existing contract (see beginOp/Release in transport/device/deferred.go). A
// refused release is NOT retried and not made into a poll: the next retirement is the next
// opportunity, and "keep the stack built" is the safe fallback. No timer, no ticker, no second
// reclamation centre - the device is the only thing that frees anything here.

import (
	"sync"

	"github.com/sagernet/sing-box/transport/device"
	E "github.com/sagernet/sing/common/exceptions"
)

// sessionLifecycle is the ownership state machine that makes "retire, then release" a fact rather
// than an assumption.
//
// It has no clock and no counter of traffic: the only things it knows are which session is current
// and whether one is established at all. Both are written only from transport/masque's session loop,
// through the ClientHandlerWithTunnelTransport notification the endpoint already receives.
type sessionLifecycle struct {
	// access is held across the release itself, not just the decision: that is what makes a begin
	// that arrives during a release wait until the released stack is wholly gone.
	access sync.Mutex
	// sequence is the epoch source; it never decreases and is never reset, so an epoch reported in a
	// log line or a diagnostic cannot be confused with a later one.
	sequence uint64
	// current is the epoch of the session that may carry traffic, or 0 when no session is current.
	current uint64
	// established is true between a begin and its retirement. It is separate from current==0 so a
	// duplicate "unknown" notification cannot be mistaken for a second retirement.
	established bool
	// retirements counts the effective retirements (a begin followed by its end). Every retirement is
	// accounted for exactly once by one of the three outcomes below, which is what makes the ledger
	// checkable rather than a set of independent counters:
	//
	//	released  a built generation was actually closed
	//	empty     the session never built the stack, so there was nothing to close
	//	skipped   the release was refused or impossible; the generation stayed built
	retirements uint64
	released    uint64
	empty       uint64
	skipped     uint64
	lastErr     error
}

// begin records that a new session is now the current one and returns its epoch.
func (l *sessionLifecycle) begin() uint64 {
	l.access.Lock()
	defer l.access.Unlock()
	l.sequence++
	l.current = l.sequence
	l.established = true
	return l.current
}

// retireLocked moves the machine to "no session current" and reports the epoch that ended. The caller
// must hold access, which is what lets the retirement transition keep the machine locked across the
// release without re-entering it.
//
// ok is false when no session was established: an "unknown transport" notification for a session this
// handler never saw begin (a first connect attempt failing before a session exists, or a duplicate)
// is not a retirement and must not release anything.
func (l *sessionLifecycle) retireLocked() (epoch uint64, ok bool) {
	if !l.established {
		return 0, false
	}
	epoch = l.current
	l.current = 0
	l.established = false
	l.retirements++
	return epoch, true
}

func (l *sessionLifecycle) noteReleased() { l.released++ }

func (l *sessionLifecycle) noteEmpty() { l.empty++ }

func (l *sessionLifecycle) noteSkipped(cause error) {
	l.skipped++
	l.lastErr = cause
}

// sessionReleaseDiagnostics is a snapshot of the retirement ledger, for tests and for an operator
// answering "did the release ever run, and why not". The invariant it carries is
// Retirements == Released + Empty + Skipped.
type sessionReleaseDiagnostics struct {
	Sequence    uint64
	Current     uint64
	Established bool
	Retirements uint64
	Released    uint64
	Empty       uint64
	Skipped     uint64
	LastError   error
}

func (l *sessionLifecycle) snapshot() sessionReleaseDiagnostics {
	l.access.Lock()
	defer l.access.Unlock()
	return sessionReleaseDiagnostics{
		Sequence:    l.sequence,
		Current:     l.current,
		Established: l.established,
		Retirements: l.retirements,
		Released:    l.released,
		Empty:       l.empty,
		Skipped:     l.skipped,
		LastError:   l.lastErr,
	}
}

// errSessionReleaseNotDeferred is recorded, never returned to a user: a system endpoint does not own
// a deferred stack, so its construction and destruction stay in Start/Close (host routes, sockets
// and the interface are Start's business there).
var errSessionReleaseNotDeferred = E.New("this endpoint does not own a deferred stack, so there is nothing to release")

// retireSession is the ONE call site of Deferred.Release in the product path.
//
// It runs on transport/masque's session-loop goroutine, from the "tunnel transport is now unknown"
// notification that connect() sends after the session's run() has returned. The condition it
// enforces is stated in the file comment above; in one sentence:
//
//	release the stack only while the session that just ended is the newest one, no session is
//	current, and no operation of the facade is inside the stack.
//
// It never waits for a dial to finish, never polls, and never runs on a timer. A refusal leaves the
// generation built - the original "defer construction only" behaviour - and the next retirement is
// the next opportunity.
func (c *ClientEndpoint) retireSession() {
	lifecycle := &c.sessionLifecycle
	// Held across the release: a begin that arrives while a stack is being torn down waits, and then
	// sees a machine with no session, a higher sequence, and a device that will rebuild on demand.
	lifecycle.access.Lock()
	defer lifecycle.access.Unlock()
	epoch, ok := lifecycle.retireLocked()
	if !ok {
		return
	}
	tunnelDevice := c.device
	if tunnelDevice == nil {
		// The endpoint was never initialized, so no stack can exist. Counted as empty rather than as a
		// release: the ledger must stay exact (Retirements == Released + Empty + Skipped) without
		// claiming a saving that did not happen.
		lifecycle.noteEmpty()
		return
	}
	releaser, isDeferred := tunnelDevice.(device.Deferred)
	if !isDeferred {
		lifecycle.noteSkipped(errSessionReleaseNotDeferred)
		c.logger.Debug("masque: session ", epoch, " retired; ", errSessionReleaseNotDeferred)
		return
	}
	if !releaser.Built() {
		// The session never demanded the stack (a tunnel that came up and went down without a single
		// flow or inbound packet), so there is nothing to close. Counted separately from a release:
		// claiming a saving that did not happen would make the ledger useless.
		lifecycle.noteEmpty()
		return
	}
	if err := releaser.Release(); err != nil {
		lifecycle.noteSkipped(err)
		c.logger.Debug("masque: session ", epoch, " retired; the inner stack was kept (skipped ",
			lifecycle.skipped, "): ", err)
		return
	}
	lifecycle.noteReleased()
	c.logger.Debug("masque: session ", epoch, " retired; released the inner stack (release #",
		lifecycle.released, ")")
}

// sessionReleases reports the retirement ledger. It is a read-only query and takes only the state
// machine's lock (never the device's), so a caller cannot deadlock against a session transition it
// is not part of; a caller that races a release in progress waits for it rather than observing a
// half state.
func (c *ClientEndpoint) sessionReleases() sessionReleaseDiagnostics {
	return c.sessionLifecycle.snapshot()
}
