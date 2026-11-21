//go:build noprofile

package main

// heapProfileSampleCount returns -1: this build links NO heap-profile consumer, so the linker is
// expected to set runtime.disableMemoryProfiling and proc.go to force MemProfileRate to 0 even
// when the linked godebug default says 512 KiB. The probe reports -1 rather than 0 so "no consumer"
// cannot be mistaken for "a consumer that saw no samples".
func heapProfileSampleCount() int64 {
	return -1
}
