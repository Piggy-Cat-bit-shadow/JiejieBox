//go:build with_naive_outbound

package naive

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/resourceguard"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// Native creation accounting for the Cronet engine (audit Q14).
//
// # Where the "real creation" boundary is, and why it is neither DialEarly nor the engine
//
// A Naive flow is not a socket. One Cronet engine keeps HTTP/2 (or HTTP/3) connections to the
// proxy and multiplexes every CONNECT stream over them, so counting DialEarly calls counts
// REQUESTS and counting engines counts OUTBOUNDS. Neither is a physical connection, and using them
// as if they were was the gap the audit recorded as "the outer token pretends to be the
// connection".
//
// The physical connection is created by Chromium's socket pool, and this pin's native library
// exposes that boundary as a callback: `Cronet_Engine_SetDialer` (cronet-go include/cronet_c.h,
// `Cronet_DialerFunc`) is invoked by the engine when it needs a NEW TCP socket, and
// `Cronet_Engine_SetUdpDialer` when it needs a UDP socket. cronet-go installs the configured
// `N.Dialer` as that callback (engine_cgo.go `SetDialer`). Wrapping the dialer this outbound hands
// to `cronet.NewNaiveClient` therefore puts the admission gate and the counters AT the real
// creation point: one dialer call is one physical connection Chromium is creating, and a CONNECT
// that reuses a pooled connection calls it zero times.
//
// # What this seam can and cannot observe
//
// Observable, exactly:
//
//	creation          one dialer entry that hands Chromium a live socket
//	creation failure  one dialer entry that returns an error (Chromium turns it into a net error)
//	concurrency       creations in flight, and their high-water mark
//	admission         refusals, expired waits and cancellations, kept apart from dial failures
//	late creation     a socket handed over after the client lifetime ended during the dial
//
// NOT observable at this pin, and therefore never reported as a number:
//
//	reuse callback    there is none. Reuse is the ABSENCE of a creation for a CONNECT, which is why
//	                  the counters are named after creations and not after requests.
//	close callback    `Cronet_DialerFunc` has no on_close (the UDP callback has one, but cronet-go
//	                  owns it), and a TCP socket's file descriptor is transferred to Chromium, so
//	                  this process no longer holds the handle it would have to watch. A physical
//	                  TCP connection's close is invisible here.
//
// The residency ledger below reports that boundary instead of hiding it: creation counts are real,
// lifetime counts are unavailable, and the reader is told which is which rather than handed zeroes
// that look like measurements.

// nativeCreationAdmissionWait bounds how long the native creation callback may wait for an
// admission slot.
//
// # Why this seam refuses instead of queueing
//
// The callback runs on a thread owned by Chromium's network service. Blocking it does not park one
// of our goroutines; it stops the engine's socket work, and a permit whose release needed work on
// that same thread would turn a queue into a deadlock. A bounded, short wait keeps the admission
// decision local: under any normal load the gate admits immediately (the measured failure this
// policy is built against was 201 concurrent handshakes, and a socket pool creates a handful), and
// when it cannot, the flow gets a retryable local error instead of stalling the engine.
var nativeCreationAdmissionWait = 250 * time.Millisecond

// nativeCreationLedger counts what the engine did at the socket boundary. Every field is written
// under the same atomic discipline, so a reader sees one consistent history.
type nativeCreationLedger struct {
	attempts    atomic.Uint64
	created     atomic.Uint64
	late        atomic.Uint64
	failed      atomic.Uint64
	refused     atomic.Uint64
	timedOut    atomic.Uint64
	canceled    atomic.Uint64
	inflight    atomic.Int64
	maxInFlight atomic.Int64
}

// NativeCreationStats is one snapshot of the ledger.
type NativeCreationStats struct {
	// Attempts is how many times the engine asked for a socket.
	Attempts uint64
	// Created is how many of those ended with a live socket handed to Chromium.
	Created uint64
	// LateCreated is the subset of Created that finished after the client lifetime ended.
	LateCreated uint64
	// Failed is how many ended with a dial error.
	Failed uint64
	// Refused, TimedOut and Canceled are admission outcomes. None of them is a dial failure and
	// none of them arms the fast-fail window for the proxy address.
	Refused  uint64
	TimedOut uint64
	Canceled uint64
	// InFlight is how many ADMITTED creations are running right now, and MaxInFlight its
	// high-water mark. A creation waiting for an admission slot is not in flight: it holds no
	// object yet, which is exactly what the ledger's Creating state means.
	InFlight    int64
	MaxInFlight int64
}

// Accounted is the number of attempts that reached a recorded outcome.
//
// It is the conservation check a test (or an operator) uses instead of trusting that every counter
// was incremented somewhere: with InFlight at zero, Accounted must equal Attempts.
func (s NativeCreationStats) Accounted() uint64 {
	return s.Created + s.Failed + s.Refused + s.TimedOut + s.Canceled
}

// begin books one creation attempt, before admission. A refusal is an outcome of an attempt, so
// the attempt has to exist first.
func (l *nativeCreationLedger) begin() {
	l.attempts.Add(1)
}

// startCreation marks one admitted creation as in flight.
func (l *nativeCreationLedger) startCreation() {
	inFlight := l.inflight.Add(1)
	for {
		previous := l.maxInFlight.Load()
		if inFlight <= previous || l.maxInFlight.CompareAndSwap(previous, inFlight) {
			return
		}
	}
}

func (l *nativeCreationLedger) finishCreated(late bool) {
	l.inflight.Add(-1)
	l.created.Add(1)
	if late {
		l.late.Add(1)
	}
}

func (l *nativeCreationLedger) finishFailed() {
	l.inflight.Add(-1)
	l.failed.Add(1)
}

func (l *nativeCreationLedger) finishAdmission(outcome *atomic.Uint64) {
	outcome.Add(1)
}

func (l *nativeCreationLedger) stats() NativeCreationStats {
	return NativeCreationStats{
		Attempts:    l.attempts.Load(),
		Created:     l.created.Load(),
		LateCreated: l.late.Load(),
		Failed:      l.failed.Load(),
		Refused:     l.refused.Load(),
		TimedOut:    l.timedOut.Load(),
		Canceled:    l.canceled.Load(),
		InFlight:    l.inflight.Load(),
		MaxInFlight: l.maxInFlight.Load(),
	}
}

// nativeCreationDialer is the dialer this outbound hands to the Cronet engine: the process
// admission gate and the creation ledger at the native creation callback, around the outbound's
// real dialer chain.
//
// It forwards to the configured dialer exactly as before when the guard is nil, which is every
// build without a process budget: the only cost on those platforms is the accounting.
type nativeCreationDialer struct {
	parent        N.Dialer
	guard         *resourceguard.Guard
	ledger        *nativeCreationLedger
	failKey       string
	admissionWait time.Duration
}

func newNativeCreationDialer(parent N.Dialer, guard *resourceguard.Guard, failKey string) *nativeCreationDialer {
	return &nativeCreationDialer{
		parent:        parent,
		guard:         guard,
		ledger:        &nativeCreationLedger{},
		failKey:       failKey,
		admissionWait: nativeCreationAdmissionWait,
	}
}

// DialContext runs one physical connection creation.
//
// network is "tcp" for the engine's socket pool and "udp" for a QUIC socket; both create the same
// kind of expensive object.
func (d *nativeCreationDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, release, err := d.admit(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	started := time.Now()
	conn, dialErr := d.parent.DialContext(ctx, network, destination)
	d.guard.ObserveOutcomeFor(d.failKey, started, dialErr)
	if dialErr != nil {
		d.ledger.finishFailed()
		return nil, dialErr
	}
	// A socket that arrives after the client lifetime ended is still handed to the engine - the
	// engine owns the fd by then and the shutdown path closes it - but it is recorded, because a
	// late creation is the shape a shutdown race takes at this seam.
	d.ledger.finishCreated(ctx.Err() != nil)
	return conn, nil
}

// ListenPacket is the packet-socket half of the same creation: a QUIC socket the engine opens is
// one physical creation exactly like a TCP one.
func (d *nativeCreationDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	ctx, release, err := d.admit(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	started := time.Now()
	conn, dialErr := d.parent.ListenPacket(ctx, destination)
	d.guard.ObserveOutcomeFor(d.failKey, started, dialErr)
	if dialErr != nil {
		d.ledger.finishFailed()
		return nil, dialErr
	}
	d.ledger.finishCreated(ctx.Err() != nil)
	return conn, nil
}

// admit books one creation attempt and takes the admission permit for it.
//
// The returned context carries resourceguard's held-permit marker, so a dialer chain that
// re-enters a Naive creation does not queue behind its own cohort. The release function is always
// non-nil, so the caller can defer it unconditionally.
func (d *nativeCreationDialer) admit(ctx context.Context) (context.Context, func(), error) {
	d.ledger.begin()
	// One logical dial holds one permit however many seams it crosses. The Cronet callback is
	// reached from engine code, so a flow's own context is not available here; what can happen is a
	// dialer chain that re-enters this seam (a detour through another Naive outbound), which must
	// not queue behind a permit its own cohort already holds. See resourceguard.HasHeldPermit.
	if resourceguard.HasHeldPermit(ctx) {
		d.ledger.startCreation()
		return ctx, func() {}, nil
	}
	admitCtx, cancel := context.WithTimeout(ctx, d.admissionWait)
	permit, err := d.guard.AcquireFor(admitCtx, resourceguard.ClassNormal, d.failKey)
	cancel()
	if err != nil {
		d.recordAdmission(err, ctx.Err() != nil)
		return nil, func() {}, E.Cause(err, "naive native creation admission")
	}
	d.ledger.startCreation()
	return resourceguard.WithHeldPermit(ctx), func() { permit.Release() }, nil
}

// recordAdmission keeps the admission outcomes apart from dial failures. None of them is evidence
// about the proxy, so none of them may be reported as one.
//
// callerEnded is the caller's own context, not the seam's bounded wait: a client closing during a
// creation is a different event from this seam refusing to start one, and the two must not be
// counted in the same bucket.
func (d *nativeCreationDialer) recordAdmission(err error, callerEnded bool) {
	switch {
	case callerEnded:
		d.ledger.finishAdmission(&d.ledger.canceled)
	case errors.Is(err, resourceguard.ErrQueueFull), errors.Is(err, resourceguard.ErrRateLimited):
		d.ledger.finishAdmission(&d.ledger.refused)
	case errors.Is(err, resourceguard.ErrWaitTimeout), errors.Is(err, context.DeadlineExceeded):
		d.ledger.finishAdmission(&d.ledger.timedOut)
	case errors.Is(err, context.Canceled):
		d.ledger.finishAdmission(&d.ledger.canceled)
	default:
		// Any other local rejection is still this process's decision, not the peer's.
		d.ledger.finishAdmission(&d.ledger.refused)
	}
}

// NativeCreationStats returns this outbound's native creation counters.
func (h *Outbound) NativeCreationStats() NativeCreationStats {
	if h.nativeCreation == nil {
		return NativeCreationStats{}
	}
	return h.nativeCreation.ledger.stats()
}

// ResidencyLedger implements adapter.ResidencyLedgerReporter (audit Q14/Q15).
//
// Creating is a real count of creations in flight, read from the engine's creation callback.
// Active, Idle, Draining and Terminated are the connection LIFETIME, which has no callback at this
// pin (see the file comment): reporting them as zero would be the precise-zero lie the ledger
// exists to prevent, so the record says it is not exact and the note names the boundary.
func (h *Outbound) ResidencyLedger() adapter.ResidencyLedger {
	if h.nativeCreation == nil {
		return adapter.ResidencyLedger{
			Answered: false,
			Note:     "naive outbound was built without the native creation callback",
		}
	}
	stats := h.nativeCreation.ledger.stats()
	var creating uint64
	if stats.InFlight > 0 {
		creating = uint64(stats.InFlight)
	}
	return adapter.ResidencyLedger{
		Creating: creating,
		Answered: true,
		Exact:    false,
		Note: "native creations are counted at the engine's creation callback " +
			"(Cronet_Engine_SetDialer); reuse has no callback, and a TCP connection's close is not " +
			"observed because the socket is handed to Chromium, so Active/Idle/Draining/Terminated " +
			"are unavailable rather than zero",
	}
}
