package main

import (
	"math"
	"runtime"
	"runtime/metrics"
)

// sampleNames is the exact metric set the ledger reads. Keeping it a fixed list rather than
// reflecting over metrics.All() is deliberate: the ledger's columns are a contract, and a Go upgrade
// that adds a metric must not silently change what "the same measurement" means.
//
// Every name here exists in Go 1.27. newSampler validates that, and a name that a future toolchain
// drops becomes an explicit null with a reason instead of a zero.
var sampleNames = []string{
	"/gc/heap/live:bytes",
	"/gc/heap/objects:objects",
	"/gc/heap/goal:bytes",
	"/gc/heap/allocs:bytes",
	"/gc/heap/allocs:objects",
	"/gc/heap/allocs-by-size:bytes",
	"/gc/scan/heap:bytes",
	"/gc/scan/stack:bytes",
	"/gc/scan/globals:bytes",
	"/gc/scan/total:bytes",
	"/gc/stack/starting-size:bytes",
	"/gc/cycles/total:gc-cycles",
	"/gc/cycles/forced:gc-cycles",
	"/gc/cycles/automatic:gc-cycles",
	"/gc/gogc:percent",
	"/gc/gomemlimit:bytes",
	"/memory/classes/heap/objects:bytes",
	"/memory/classes/heap/stacks:bytes",
	"/memory/classes/heap/unused:bytes",
	"/memory/classes/heap/released:bytes",
	"/memory/classes/heap/free:bytes",
	"/memory/classes/metadata/mspan/inuse:bytes",
	"/memory/classes/metadata/mspan/free:bytes",
	"/memory/classes/metadata/mcache/inuse:bytes",
	"/memory/classes/metadata/mcache/free:bytes",
	"/memory/classes/metadata/other:bytes",
	"/memory/classes/os-stacks:bytes",
	"/memory/classes/profiling/buckets:bytes",
	"/memory/classes/total:bytes",
	"/sched/gomaxprocs:threads",
	"/sched/threads/total:threads",
	"/sched/goroutines:goroutines",
	"/sched/goroutines/running:goroutines",
	"/sched/goroutines/runnable:goroutines",
	"/sched/goroutines/waiting:goroutines",
	"/sched/goroutines/not-in-go:goroutines",
	"/sched/goroutines-created:goroutines",
	"/cpu/classes/gc/total:cpu-seconds",
	"/cpu/classes/scavenge/total:cpu-seconds",
	"/cpu/classes/user:cpu-seconds",
}

// sampler reads the metric set in one pass.
//
// # Why every read is kind-checked
//
// runtime/metrics answers an unknown name with KindBad rather than an error, and a caller that
// reads Uint64() off a KindBad sample gets 0. That 0 is indistinguishable from "this costs nothing"
// and it is exactly how a missing device metric becomes a favourable line in a report. This
// sampler refuses to produce a value it cannot prove the kind of: the field stays null and the
// reason goes into the reading's unsupported map.
type sampler struct {
	samples []metrics.Sample
	index   map[string]int
	// static holds the reasons a name is unavailable on this toolchain; dynamic holds the reasons
	// found during the LAST read. Keeping them apart is what stops a stage-specific reason from
	// sticking to every later reading of the same metric.
	static  map[string]string
	dynamic map[string]string
}

func newSampler() *sampler {
	known := make(map[string]metrics.ValueKind, 128)
	for _, descriptor := range metrics.All() {
		known[descriptor.Name] = descriptor.Kind
	}
	instance := &sampler{
		index:   make(map[string]int, len(sampleNames)),
		static:  make(map[string]string),
		dynamic: make(map[string]string),
	}
	for _, name := range sampleNames {
		kind, exists := known[name]
		if !exists || kind == metrics.KindBad {
			instance.static[name] = "this Go build publishes no such runtime metric"
			continue
		}
		instance.index[name] = len(instance.samples)
		instance.samples = append(instance.samples, metrics.Sample{Name: name})
	}
	return instance
}

func (s *sampler) read() {
	if len(s.samples) == 0 {
		return
	}
	s.dynamic = make(map[string]string)
	metrics.Read(s.samples)
}

// unsupportedNow is the reason set for the reading that was just taken.
func (s *sampler) unsupportedNow() map[string]string {
	if len(s.static) == 0 && len(s.dynamic) == 0 {
		return nil
	}
	merged := make(map[string]string, len(s.static)+len(s.dynamic))
	for key, value := range s.static {
		merged[key] = value
	}
	for key, value := range s.dynamic {
		merged[key] = value
	}
	return merged
}

// uintValue returns the reading, or nil when it is absent or has the wrong kind. The kind is
// re-checked on every read, not only at construction: a sample whose name the runtime stops
// resolving would otherwise be reported as a zero.
func (s *sampler) uintValue(name string) *uint64 {
	position, ok := s.index[name]
	if !ok {
		return nil
	}
	value := &s.samples[position].Value
	if value.Kind() != metrics.KindUint64 {
		s.noteKind(name, value.Kind())
		return nil
	}
	reading := value.Uint64()
	return &reading
}

func (s *sampler) floatValue(name string) *float64 {
	position, ok := s.index[name]
	if !ok {
		return nil
	}
	value := &s.samples[position].Value
	if value.Kind() != metrics.KindFloat64 {
		s.noteKind(name, value.Kind())
		return nil
	}
	reading := value.Float64()
	return &reading
}

// nonzeroBuckets counts the histogram buckets that have seen at least one event. It is a shape
// signal from the always-on allocation statistics, NOT the heap-profile bucket table (that one is
// /memory/classes/profiling/buckets:bytes).
func (s *sampler) nonzeroBuckets(name string) *uint64 {
	position, ok := s.index[name]
	if !ok {
		return nil
	}
	value := &s.samples[position].Value
	if value.Kind() != metrics.KindFloat64Histogram {
		s.noteKind(name, value.Kind())
		return nil
	}
	var count uint64
	for _, bucket := range value.Float64Histogram().Counts {
		if bucket > 0 {
			count++
		}
	}
	return &count
}

func (s *sampler) noteKind(name string, kind metrics.ValueKind) {
	if kind == metrics.KindBad {
		s.dynamic[name] = "runtime/metrics reported KindBad for this name at read time"
		return
	}
	s.dynamic[name] = "unexpected metric kind " + kindName(kind)
}

// kindName names a metric kind. runtime/metrics exposes the enum without a String method, and a
// bare number in an error message is not an explanation.
func kindName(kind metrics.ValueKind) string {
	switch kind {
	case metrics.KindUint64:
		return "uint64"
	case metrics.KindFloat64:
		return "float64"
	case metrics.KindFloat64Histogram:
		return "float64 histogram"
	case metrics.KindBad:
		return "bad"
	default:
		return "unknown"
	}
}

// readGo assembles the Go half of a reading and copies the sampler's unsupported list into it.
func (s *sampler) readGo() goReadings {
	s.read()
	reading := goReadings{
		LiveHeapBytes:          s.uintValue("/gc/heap/live:bytes"),
		LiveObjects:            s.uintValue("/gc/heap/objects:objects"),
		HeapObjectsBytes:       s.uintValue("/memory/classes/heap/objects:bytes"),
		HeapStacksBytes:        s.uintValue("/memory/classes/heap/stacks:bytes"),
		HeapUnusedBytes:        s.uintValue("/memory/classes/heap/unused:bytes"),
		HeapReleasedBytes:      s.uintValue("/memory/classes/heap/released:bytes"),
		HeapFreeBytes:          s.uintValue("/memory/classes/heap/free:bytes"),
		TotalBytes:             s.uintValue("/memory/classes/total:bytes"),
		ProfilingBucketsBytes:  s.uintValue("/memory/classes/profiling/buckets:bytes"),
		CumulativeAllocBytes:   s.uintValue("/gc/heap/allocs:bytes"),
		CumulativeAllocObjects: s.uintValue("/gc/heap/allocs:objects"),
		RuntimeThreads:         s.uintValue("/sched/threads/total:threads"),
		GOMAXPROCS:             s.uintValue("/sched/gomaxprocs:threads"),
		Metadata: metadataReadings{
			MSpanInuseBytes:  s.uintValue("/memory/classes/metadata/mspan/inuse:bytes"),
			MSpanFreeBytes:   s.uintValue("/memory/classes/metadata/mspan/free:bytes"),
			MCacheInuseBytes: s.uintValue("/memory/classes/metadata/mcache/inuse:bytes"),
			MCacheFreeBytes:  s.uintValue("/memory/classes/metadata/mcache/free:bytes"),
			OtherBytes:       s.uintValue("/memory/classes/metadata/other:bytes"),
			OSStacksBytes:    s.uintValue("/memory/classes/os-stacks:bytes"),
		},
		Scan: scanReadings{
			HeapBytes:    s.uintValue("/gc/scan/heap:bytes"),
			StackBytes:   s.uintValue("/gc/scan/stack:bytes"),
			GlobalsBytes: s.uintValue("/gc/scan/globals:bytes"),
			TotalBytes:   s.uintValue("/gc/scan/total:bytes"),
		},
		GC: gcReadings{
			CyclesTotal:     s.uintValue("/gc/cycles/total:gc-cycles"),
			CyclesForced:    s.uintValue("/gc/cycles/forced:gc-cycles"),
			CyclesAutomatic: s.uintValue("/gc/cycles/automatic:gc-cycles"),
			GoalBytes:       s.uintValue("/gc/heap/goal:bytes"),
		},
		CPU: cpuReadings{
			GCTotalSeconds:      s.floatValue("/cpu/classes/gc/total:cpu-seconds"),
			ScavengeTotalSecond: s.floatValue("/cpu/classes/scavenge/total:cpu-seconds"),
			UserTotalSeconds:    s.floatValue("/cpu/classes/user:cpu-seconds"),
		},
	}
	reading.AllocSizeClassesTouched = s.nonzeroBuckets("/gc/heap/allocs-by-size:bytes")
	// runtime.MemProfileRate is the value the profiler is actually using, which is not necessarily
	// what the binary was built with or what the environment asked for.
	rate := int64(runtime.MemProfileRate)
	reading.MemProfileRateBytes = &rate

	// /gc/heap/live:bytes is published at the END of a GC cycle. In a natural run - which by
	// definition has forced nothing - the first readings happen before the runtime has collected
	// anything, and the metric reads 0. Reporting that 0 would put "this process holds no heap" in
	// the ledger, which is the most favourable possible lie; it is reported as unknown instead, and
	// /memory/classes/heap/objects:bytes (always available) is the natural-mode heap reading.
	if reading.GC.CyclesTotal != nil && *reading.GC.CyclesTotal == 0 {
		s.dynamic["/gc/heap/live:bytes"] = "the runtime publishes the live-heap metric at the end of " +
			"a GC cycle and no cycle has run in this natural-mode process yet"
		reading.LiveHeapBytes = nil
	}
	reading.LiveHeapFromLastGC = reading.LiveHeapBytes != nil
	return reading
}

func (s *sampler) readGoroutines() goroutineReadings {
	return goroutineReadings{
		Current:  s.uintValue("/sched/goroutines:goroutines"),
		Running:  s.uintValue("/sched/goroutines/running:goroutines"),
		Runnable: s.uintValue("/sched/goroutines/runnable:goroutines"),
		Waiting:  s.uintValue("/sched/goroutines/waiting:goroutines"),
		NotInGo:  s.uintValue("/sched/goroutines/not-in-go:goroutines"),
		Created:  s.uintValue("/sched/goroutines-created:goroutines"),
	}
}

// mergeUnsupported folds one reading's unsupported reasons into a shared map. A nil map is a
// programming error here, so the helper allocates rather than dropping evidence.
func mergeUnsupported(into map[string]string, from map[string]string) map[string]string {
	if len(from) == 0 {
		return into
	}
	if into == nil {
		into = make(map[string]string, len(from))
	}
	for key, value := range from {
		into[key] = value
	}
	return into
}

// signedDelta subtracts two unsigned readings without letting the result wrap.
//
// # Why this is not just a subtraction
//
// Every runtime increment is a uint64, and "the heap went DOWN by 40 KiB" is a legitimate result:
// the release after a close, a trim, a scavenge. Computed in uint64 that -40960 becomes
// 18446744073709510656, which is larger than every budget in the file, so a regression gate that
// compared it would pass for exactly the reason it exists to catch. The other failure mode is
// clamping to zero, which hides shrinkage and makes "no change" and "freed a megabyte"
// indistinguishable.
//
// A reading above math.MaxInt64 cannot be represented as a signed delta. Rather than clamp it, the
// delta is reported as missing, because a number that cannot be represented is not a number this
// instrument is allowed to invent.
func signedDelta(after uint64, before uint64) (*int64, bool) {
	if after > math.MaxInt64 || before > math.MaxInt64 {
		return nil, false
	}
	delta := int64(after) - int64(before)
	return &delta, true
}

// deltaOf returns the signed difference of two optional readings, or nil when either side is
// missing. A missing baseline makes the delta unknown, not equal to the value.
func deltaOf(after *uint64, before *uint64) *int64 {
	if after == nil || before == nil {
		return nil
	}
	delta, ok := signedDelta(*after, *before)
	if !ok {
		return nil
	}
	return delta
}

// deltaInt is deltaOf for exact counts.
func deltaInt(after int, before int) *int64 {
	delta := int64(after) - int64(before)
	return &delta
}

// sumUint adds optional readings, returning nil if any term is missing: a partial sum presented as
// a total is worse than no total.
func sumUint(values ...*uint64) *uint64 {
	var total uint64
	for _, value := range values {
		if value == nil {
			return nil
		}
		total += *value
	}
	return &total
}

// metadataTotal is the sum of the runtime's own bookkeeping classes.
func metadataTotal(reading metadataReadings) *uint64 {
	return sumUint(
		reading.MSpanInuseBytes,
		reading.MSpanFreeBytes,
		reading.MCacheInuseBytes,
		reading.MCacheFreeBytes,
		reading.OtherBytes,
	)
}
