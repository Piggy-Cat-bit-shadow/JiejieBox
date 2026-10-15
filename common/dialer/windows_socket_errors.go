package dialer

import (
	"errors"
	"syscall"
)

// Windows socket errors, in the spelling Windows actually puts on the wire.
//
// # Why this file exists, and why the values are written numerically
//
// Go's syscall package on Windows defines ENETUNREACH as 0x20000043 - that is, 67 with the
// application-defined error bit set. The Winsock error for the same condition is 10051. They are
// DIFFERENT NUMBERS, and the one that reaches this code is 10051:
//
//	net/fd_windows.go:174    if _, ok := err.(syscall.Errno); ok {
//	                             err = os.NewSyscallError("connectex", err)
//
// The standard library wraps the raw errno from ConnectEx with no translation, so a real Windows
// "network unreachable" arrives as syscall.Errno(10051) and NOT as syscall.ENETUNREACH. Every
// errors.Is(err, syscall.ENETUNREACH) check for these conditions is therefore dead on Windows.
//
// The consequence is not cosmetic. It made the fast-fallback path inert in production once already -
// it existed and worked in tests while the real classifiers never matched - and the same shape was
// found again in this round: the four "kernel told us the path is unusable" checks never fired on the
// platform whose reported failure is exactly one of them.
//
// The values are spelled out rather than referenced from golang.org/x/sys/windows so that the
// classifier is ONE implementation on every platform. A build-tagged version would leave the Windows
// arm untestable from the machine this was fixed on, which is how the original dead check survived.
//
// See https://learn.microsoft.com/en-us/windows/win32/winsock/windows-sockets-error-codes-2
const (
	wsaENETUNREACH   = syscall.Errno(10051)
	wsaEHOSTUNREACH  = syscall.Errno(10065)
	wsaEADDRNOTAVAIL = syscall.Errno(10049)
	wsaENETDOWN      = syscall.Errno(10050)

	// wsaECONNREFUSED is here for the OPPOSITE reason: it must never be read as a path failure. The
	// peer answered, which proves the path works. Its exclusion has the same defect - the raw 10061
	// never matched syscall.ECONNREFUSED - and it is the more dangerous of the two, because a missed
	// refusal is a healthy family blamed for a server being down.
	wsaECONNREFUSED = syscall.Errno(10061)

	// wsaECONNRESET is "the peer answered and then closed". It belongs with wsaECONNREFUSED and for
	// the same reason, and it was found by extending the protocol/group table rather than by reading
	// this file: every caller that classifies a reset against syscall.ECONNRESET misses it on Windows,
	// where the value is 10054.
	//
	// The failure is silent in the safe direction TODAY only because a reset falls through to each
	// classifier's conservative default. That is luck rather than design, and it is fragile: the first
	// caller to add a broader fallback - "any network error is worth another member", say - would
	// suddenly retry a flow whose destination had already spoken, which is the one thing the reset
	// branch exists to prevent.
	wsaECONNRESET = syscall.Errno(10054)

	// wsaETIMEDOUT is the kernel's connect timeout. It is kept out of PathFailureErrnos for the reason
	// recorded there, and lives in TimeoutErrnos instead.
	wsaETIMEDOUT = syscall.Errno(10060)

	// wsaECONNABORTED and wsaEPIPE complete the peer-lifecycle triple with wsaECONNRESET above. Both
	// are matched only by PeerGoneErrnos, which explains why a log-severity question turned out to need
	// the same numbers as the routing ones.
	wsaECONNABORTED = syscall.Errno(10053)
	wsaEPIPE        = syscall.Errno(10032)
)

// ErroredWith reports whether err matches any of the given errnos, in either the POSIX spelling this
// package has always used or the raw Winsock spelling Windows actually produces.
//
// # Why this is exported
//
// The dead-comparison defect is not local to this package. protocol/group classifies the same two
// conditions - a kernel-unreachable path, and a listener that refused - against the same `syscall.E*`
// constants, and on Windows every one of those comparisons is dead there too. A second private copy
// of the WSA values in that package is exactly the drift this file exists to prevent ("the classifier
// is ONE implementation on every platform"), so this function and the two sets below are exported and
// protocol/group calls them rather than restating them.
//
// The import direction is checked rather than assumed: common/dialer does NOT depend on
// protocol/group (`go list -deps ./common/dialer/` contains no protocol package), so the import added
// there cannot cycle.
func ErroredWith(err error, errnos ...syscall.Errno) bool {
	for _, errno := range errnos {
		if errors.Is(err, errno) {
			return true
		}
	}
	return false
}

// The two groups the classifier matches on, as errno sets so the POSIX and WSA spellings can never
// drift apart.
var (
	// PathFailureErrnos are the conditions where the kernel told us this family cannot carry the
	// packet.
	//
	// ETIMEDOUT is deliberately ABSENT, in both spellings. A bare errno is not a net.Error, so a kernel
	// connect timeout is NOT classified here as a path failure: the scheduler's own deadline check is
	// what classifies a real stall, and that is the division of labour
	// TestWindowsWSAErrorChainIsClassified pins for WSAETIMEDOUT (10060). Adding it would relabel every
	// timeout as "this family cannot carry the packet" and hand the family a hard verdict it has not
	// earned. protocol/group, whose policy question is different - "would another member help?" -
	// matches the timeout in its own right at its own call site.
	PathFailureErrnos = []syscall.Errno{
		syscall.ENETUNREACH, wsaENETUNREACH,
		syscall.EHOSTUNREACH, wsaEHOSTUNREACH,
		syscall.EADDRNOTAVAIL, wsaEADDRNOTAVAIL,
		syscall.ENETDOWN, wsaENETDOWN,
	}
	// PeerAnsweredErrnos are the conditions where the network DELIVERED PACKETS and a LISTENER
	// replied. They are evidence the path works, so recording one would penalise a healthy family.
	//
	// ECONNREFUSED only, and that is deliberate rather than an omission: a refusal is the one answer
	// that says a listener is reachable, which is what protocol/group's ledger means by "the member is
	// healthy". A RESET also proves the path carried packets, but it is the shape of a destination-side
	// failure reported in band by a proxy protocol, so blaming the member for it would take a working
	// outbound out of the rotation. The two facts are therefore separate sets - see ResetErrnos below -
	// because they licence different conclusions from the same underlying observation.
	PeerAnsweredErrnos = []syscall.Errno{
		syscall.ECONNREFUSED, wsaECONNREFUSED,
	}

	// ResetErrnos are "something on the path answered and then closed".
	//
	// They prove the path works and they are NOT evidence about the member, which is why they are not
	// in PeerAnsweredErrnos. Their consumer is RetryThisFlow, where a reset is the one error that must
	// be TERMINAL for the flow: a second member would very likely reproduce it, and the flow's only
	// alternate is worth more than a second look at a destination that already spoke.
	//
	// On Windows this value is 10054 and never syscall.ECONNRESET (0x20000017), so every
	// errors.Is(err, syscall.ECONNRESET) comparison misses what production delivers. Today that falls
	// through to each classifier's conservative default, which happens to agree - which is luck, not
	// design, and stops being true the moment a caller adds a broader fallback.
	ResetErrnos = []syscall.Errno{
		syscall.ECONNRESET, wsaECONNRESET,
	}

	// UnreachableErrnos are the conditions where this member's own first hop is gone, WITHOUT the
	// kernel-timeout member of PathFailureErrnos.
	//
	// The distinction is a policy one and it belongs to the caller: protocol/group demotes a member
	// from the rotation on these - "a listener refused it or its network is gone" - and a connect that
	// timed out is not evidence of either, so it must not be in that set. Keeping the two sets side by
	// side is what lets that caller express its own rule without restating any WSA number.
	UnreachableErrnos = []syscall.Errno{
		syscall.EHOSTUNREACH, wsaEHOSTUNREACH,
		syscall.ENETUNREACH, wsaENETUNREACH,
	}

	// TimeoutErrnos are the kernel's own connect timeouts, in both spellings.
	//
	// They are deliberately NOT in PathFailureErrnos and must not be folded into it: a bare errno is not
	// a net.Error, so a kernel connect timeout is not evidence that a family cannot carry packets -
	// only the scheduler knows whether a stall was the network's fault. That split is pinned by
	// TestWindowsWSAErrorChainIsClassified.
	//
	// The set exists because a caller's QUESTION can differ from the family classifier's. protocol/group
	// asks "would another member help?", and its documented answer for a kernel connect timeout is yes,
	// so it matches this set. Sharing the numbers keeps the Winsock spelling in one place without
	// forcing one policy on both callers.
	TimeoutErrnos = []syscall.Errno{
		syscall.ETIMEDOUT, wsaETIMEDOUT,
	}

	// PeerGoneErrnos are "this socket's peer went away mid-conversation", and they exist because a
	// LOG-SEVERITY question turned out to need the same numbers as the routing questions above.
	//
	// protocol/anytls and protocol/shadowtls downgrade a peer lifecycle error to debug, because a
	// scanner that opens a connection and leaves is noise rather than an operator-visible fault, while
	// a refused or unreachable handshake target stays at error. Both asked it with the same raw list:
	//
	//	for _, expected := range []syscall.Errno{
	//	    syscall.ECONNRESET, syscall.ECONNABORTED, syscall.EPIPE,
	//	} {
	//	    if errors.Is(err, expected) { return true }
	//	}
	//
	// which is the dead-on-Windows shape for the third time in this release: the Winsock values are
	// 10054, 10053 and 10032, so on Windows NOTHING matched and every scanner disconnect was logged at
	// error. The consequence here is severity rather than routing - it cannot lose a flow - so it is
	// the mildest instance of the class; it is fixed in the same place rather than with a third local
	// list, because a local list is how the first two survived.
	//
	// Why these three together, when ResetErrnos is a separate set: the QUESTION is different. ResetErrnos
	// answers "must this FLOW be terminal", where a reset is the one error a second member would
	// reproduce, and its doc gives that consumer. This set answers "is this a peer lifecycle event or an
	// operator-visible fault", which is why EPIPE and ECONNABORTED belong to it and not to that one.
	// Sharing the numbers does not force one policy on both callers - the same reasoning TimeoutErrnos
	// records above.
	PeerGoneErrnos = []syscall.Errno{
		syscall.ECONNRESET, wsaECONNRESET,
		syscall.ECONNABORTED, wsaECONNABORTED,
		syscall.EPIPE, wsaEPIPE,
	}
)
