//go:build !windows && !linux

package wireguard

// processFDCount is unavailable on this platform: the ledger records descriptors where the OS
// exposes a cheap count and reports "not measured" everywhere else instead of pretending a zero.
func processFDCount() (int, bool) {
	return 0, false
}
