//go:build !windows

package outbound

// processHandleCount is unavailable on this platform: the LN long loop records handles where the OS
// exposes a cheap process-wide count (Windows today) and reports "not measured" everywhere else
// instead of pretending a zero.
func processHandleCount() (uint32, bool) {
	return 0, false
}
