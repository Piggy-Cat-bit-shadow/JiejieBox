//go:build !windows

package libbox

import "syscall"

// platformDialErrnoClass adds nothing on this platform: a POSIX socket error IS the errno constant
// the shared classifier already matches with errors.Is, so there is no second spelling to map. The
// function exists so the classifier has one call shape everywhere.
func platformDialErrnoClass(syscall.Errno) dialErrnoClass {
	return dialErrnoNone
}
