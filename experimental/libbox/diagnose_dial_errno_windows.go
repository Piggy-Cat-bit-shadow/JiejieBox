//go:build windows

package libbox

import "syscall"

// The Winsock codes a real dial failure arrives as on Windows. They are spelled numerically because
// Go's syscall package deliberately exposes POSIX-shaped pseudo-values rather than these, and the
// numbers are fixed by the Winsock API: they must not be "discovered" at runtime from the message.
const (
	wsaECONNREFUSED uintptr = 10061
	wsaECONNRESET   uintptr = 10054
	wsaENETUNREACH  uintptr = 10051
	wsaEHOSTUNREACH uintptr = 10065
	wsaENETDOWN     uintptr = 10050
)

// platformDialErrnoClass maps a raw Windows errno onto the dial families.
func platformDialErrnoClass(errno syscall.Errno) dialErrnoClass {
	switch uintptr(errno) {
	case wsaECONNREFUSED:
		return dialErrnoRefused
	case wsaECONNRESET:
		return dialErrnoReset
	case wsaENETUNREACH, wsaEHOSTUNREACH, wsaENETDOWN:
		return dialErrnoUnreachable
	default:
		return dialErrnoNone
	}
}
