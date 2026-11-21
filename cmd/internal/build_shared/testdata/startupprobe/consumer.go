//go:build !noprofile

package main

import (
	// net/http/pprof is imported for the same reason libbox imports it
	// (experimental/libbox/pprof.go): the product always links a heap-profile consumer. That
	// matters to this probe because the Go linker SILENTLY disables memory profiling when
	// runtime.memProfileInternal is unreachable, and proc.go then forces MemProfileRate to 0 after
	// the linked default has already been parsed. Without a consumer, a build that inherits the
	// 512 KiB rate would still report 0, and a startup-policy measurement would be measuring the
	// linker, not the policy.
	_ "net/http/pprof"
	"runtime/pprof"
)

// heapProfileSampleCount is what an OOM report would actually get: the number of records in the
// heap profile. It is -1 when the build has no consumer (see noprofile.go), which is distinct from
// 0 samples with a consumer.
func heapProfileSampleCount() int64 {
	return int64(pprof.Lookup("heap").Count())
}
