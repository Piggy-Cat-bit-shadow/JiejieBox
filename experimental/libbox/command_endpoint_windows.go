//go:build windows

package libbox

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isConnectionRefused reports whether err is a refused connect.
//
// # Why this needs its own file, and its own constant
//
// On Windows the WSA error code and syscall.ECONNREFUSED are NOT the same value: a refused connect
// reports 10061 (WSAECONNREFUSED, from winsock2.h), while syscall.ECONNREFUSED is the
// APPLICATION_ERROR encoding 0x20000016. Comparing against the wrong one silently answers "cannot
// tell" for the most ordinary failure there is, which is how a dead core would be reported as
// unknown instead of down. Measured on the pinned toolchain:
//
//	err   = dial tcp 127.0.0.1:57722: connectex: No connection could be made because the target
//	        machine actively refused it.
//	errno = 10061
//	errors.Is(err, syscall.ECONNREFUSED)          = false
//	errors.Is(err, windows.WSAECONNREFUSED)       = true
func isConnectionRefused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED)
}
