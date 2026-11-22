//go:build unix

package srs

import (
	"os"

	"golang.org/x/sys/unix"
)

func mmapFile(file *os.File, size int, removeFile bool) ([]byte, func(), error) {
	data, err := unix.Mmap(int(file.Fd()), 0, size, unix.PROT_READ, unix.MAP_PRIVATE)
	if err != nil {
		return nil, nil, err
	}
	// A transient temp file has no other name-holder: unlink it now so a crash cannot leave the
	// mapping's backing file behind. A PERSISTED artifact is the opposite - the name is the record
	// other readers and the artifact store's leases refer to, so it is never removed here.
	if removeFile {
		_ = os.Remove(file.Name())
	}
	return data, func() {
		unix.Munmap(data)
	}, nil
}
