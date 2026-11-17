//go:build windows

package libbox

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockCommandSocketPath takes the exclusive, non-blocking ownership lock for a command socket path.
//
// LockFileEx with LOCKFILE_FAIL_IMMEDIATELY is the Windows equivalent of flock(LOCK_EX|LOCK_NB): the
// second instance fails at once instead of waiting, and it fails BEFORE the probe-then-remove step that
// used to delete a live instance's socket. One byte is locked, which is all the exclusion needs; the file
// itself is only a rendezvous.
func lockCommandSocketPath(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return nil, err
	}
	err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
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
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
	_ = file.Close()
}
