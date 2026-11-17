//go:build !badlinkname || !tfogo_checklinkname0 || race

package runtimeinfo

// collectGoroutines is the stub. It is compiled when the linkname is not permitted, when the build
// cannot pass -checklinkname=0, or under the race detector, where checkptr refuses the out-of-bounds
// read the mirror is built on (see goroutine_badlinkname.go).
func collectGoroutines() *GoroutineReport {
	return nil
}
