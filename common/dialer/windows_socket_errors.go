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
)

// erroredWith reports whether err matches any of the given errnos, in either the POSIX spelling this
// package has always used or the raw Winsock spelling Windows actually produces.
func erroredWith(err error, errnos ...syscall.Errno) bool {
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
	// pathFailureErrnos are the conditions where the kernel told us this family cannot carry the
	// packet.
	pathFailureErrnos = []syscall.Errno{
		syscall.ENETUNREACH, wsaENETUNREACH,
		syscall.EHOSTUNREACH, wsaEHOSTUNREACH,
		syscall.EADDRNOTAVAIL, wsaEADDRNOTAVAIL,
		syscall.ENETDOWN, wsaENETDOWN,
	}
	// peerAnsweredErrnos are the conditions where the network DELIVERED PACKETS and something replied.
	// They are evidence the path works, so recording one would penalise a healthy family.
	peerAnsweredErrnos = []syscall.Errno{
		syscall.ECONNREFUSED, wsaECONNREFUSED,
	}
)
