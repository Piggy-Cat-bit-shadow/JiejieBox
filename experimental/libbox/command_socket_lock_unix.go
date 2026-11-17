//go:build !windows

package libbox

import (
	"os"
	"syscall"
)

// lockCommandSocketPath takes the exclusive, non-blocking ownership lock for a command socket path.
//
// Non-blocking is the point: a second instance must find out immediately that another one owns the path
// instead of waiting, and it must fail BEFORE it reaches the probe-then-remove step. See
// acquireCommandSocket for why the removal needs this lock to be sound.
func lockCommandSocketPath(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return nil, err
	}
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func unlockCommandSocketPath(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}
