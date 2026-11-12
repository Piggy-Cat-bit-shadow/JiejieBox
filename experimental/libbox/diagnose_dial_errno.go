package libbox

// O02 — the errno half of the dial classification.
//
// `errors.Is(err, syscall.ECONNREFUSED)` is not portable enough to be the whole answer. On POSIX a
// socket error IS that errno. On Windows a Winsock failure arrives as WSAECONNREFUSED (10061) and
// Go's syscall.ECONNREFUSED is a synthetic pseudo-value, so errors.Is answers false for the very
// error a closed port produces - MEASURED on this host:
//
//	dial tcp 127.0.0.1:54380: connectex: No connection could be made because the target machine
//	actively refused it.  errors.Is ECONNREFUSED: false   errno: 10061
//
// A classifier that only used errors.Is would report a real refusal as `dial-failed` on Windows,
// which is precisely the "no observation" outcome these codes exist to remove. The platform file
// beside this one supplies the Winsock mapping; on POSIX it adds nothing and the errno constants
// already match.

import (
	"errors"
	"syscall"
)

// dialErrnoClass is the coarse family an errno belongs to.
type dialErrnoClass int

const (
	dialErrnoNone dialErrnoClass = iota
	dialErrnoRefused
	dialErrnoReset
	dialErrnoUnreachable
)

// classifyDialErrno reports the family of the error's underlying errno.
//
// The POSIX constants are matched with errors.Is so a wrapped error keeps working; the platform
// hook covers the systems whose raw numeric codes those constants do not describe.
func classifyDialErrno(err error) dialErrnoClass {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return dialErrnoNone
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return dialErrnoRefused
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return dialErrnoReset
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.ENETDOWN):
		return dialErrnoUnreachable
	}
	return platformDialErrnoClass(errno)
}
