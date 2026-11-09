//go:build !windows

package v2rayxhttp

// experimentWorkingSetBytes is unavailable off Windows; the experiment reports the
// heap only there and says so instead of guessing at an RSS.
func experimentWorkingSetBytes() (uint64, bool) {
	return 0, false
}
