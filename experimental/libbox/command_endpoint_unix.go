//go:build !windows

package libbox

import (
	"errors"
	"syscall"
)

// isConnectionRefused reports whether err is a refused connect(2).
//
// On Unix this is a single errno, and it is what a client receives when the socket file outlives the
// process that bound it: the file is there, nothing is accepting on it.
func isConnectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
