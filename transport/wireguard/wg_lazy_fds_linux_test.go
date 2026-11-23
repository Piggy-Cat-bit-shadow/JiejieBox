//go:build linux

package wireguard

import "os"

// processFDCount is the POSIX open-descriptor count on Linux (/proc/self/fd).
func processFDCount() (int, bool) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, false
	}
	return len(entries), true
}
