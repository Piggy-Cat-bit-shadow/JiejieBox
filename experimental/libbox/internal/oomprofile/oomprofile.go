//go:build darwin || linux || windows

package oomprofile

import (
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
	"unsafe"
)

type stackRecord struct {
	Stack []uintptr
}

// memProfileRecord mirrors internal/profilerecord.MemProfileRecord of the PINNED toolchain.
//
// It is a mirror, not a definition: runtimeMemProfileInternal writes records into a slice of this
// type using the layout of the real one, so every field, its order and its type must match the
// toolchain exactly. A mismatch is silent at compile time and reads the wrong bytes at run time.
//
// Go 1.27 replaced the pair (AllocBytes, FreeBytes) with the single per-object size. Before:
//
//	type MemProfileRecord struct {
//		AllocBytes, FreeBytes     int64
//		AllocObjects, FreeObjects int64
//		Stack                     []uintptr
//	}
//
// 56 bytes, Stack at offset 32. Go 1.27.4:
//
//	type MemProfileRecord struct {
//		ObjectSize                int64
//		AllocObjects, FreeObjects int64
//		Stack                     []uintptr
//	}
//
// 48 bytes, Stack at offset 24, and InUseBytes is derived from the object size rather than measured.
//
// The 0.1.52 Network Extension SIGSEGV was this mirror left on the old layout: the runtime wrote
// 48-byte records into a 56-byte slice, so every record after the first had its Stack slice header
// read from inside the wrong record. Record 1's "data pointer" was record 0's length field, which
// is what the crash reports as a dereference of a small address (0x14). TestMemProfileRecordMirrorsToolchain
// now fails loudly when the pinned toolchain moves this type again.
type memProfileRecord struct {
	ObjectSize                int64
	AllocObjects, FreeObjects int64
	Stack                     []uintptr
}

func (r *memProfileRecord) InUseBytes() int64 {
	return r.InUseObjects() * r.ObjectSize
}

func (r *memProfileRecord) InUseObjects() int64 {
	return r.AllocObjects - r.FreeObjects
}

type blockProfileRecord struct {
	Count  int64
	Cycles int64
	Stack  []uintptr
}

type label struct {
	key   string
	value string
}

type labelSet struct {
	list []label
}

type labelMap struct {
	labelSet
}

func WriteFile(filePath string, name string) error {
	writer, ok := profileWriters[name]
	if !ok {
		return fmt.Errorf("unsupported profile %q", name)
	}
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	err = writer(file)
	if err != nil {
		_ = os.Remove(filePath)
		return err
	}
	err = file.Close()
	if err != nil {
		_ = os.Remove(filePath)
		return err
	}
	return nil
}

var profileWriters = map[string]func(io.Writer) error{
	"allocs":       writeAlloc,
	"block":        writeBlock,
	"goroutine":    writeGoroutine,
	"heap":         writeHeap,
	"mutex":        writeMutex,
	"threadcreate": writeThreadCreate,
}

func writeHeap(w io.Writer) error {
	return writeHeapInternal(w, "")
}

func writeAlloc(w io.Writer) error {
	return writeHeapInternal(w, "alloc_space")
}

func writeHeapInternal(w io.Writer, defaultSampleType string) error {
	var profile []memProfileRecord
	n, _ := runtimeMemProfileInternal(nil, true)
	var ok bool
	for {
		profile = make([]memProfileRecord, n+50)
		n, ok = runtimeMemProfileInternal(profile, true)
		if ok {
			profile = profile[:n]
			break
		}
	}
	return writeHeapProto(w, profile, int64(runtime.MemProfileRate), defaultSampleType)
}

func writeGoroutine(w io.Writer) error {
	return writeRuntimeProfile(w, "goroutine", runtimeGoroutineProfileWithLabels)
}

func writeThreadCreate(w io.Writer) error {
	return writeRuntimeProfile(w, "threadcreate", func(p []stackRecord, _ []unsafe.Pointer) (int, bool) {
		return runtimeThreadCreateInternal(p)
	})
}

func writeRuntimeProfile(w io.Writer, name string, fetch func([]stackRecord, []unsafe.Pointer) (int, bool)) error {
	var profile []stackRecord
	var labels []unsafe.Pointer

	n, _ := fetch(nil, nil)
	var ok bool
	for {
		profile = make([]stackRecord, n+10)
		labels = make([]unsafe.Pointer, n+10)
		n, ok = fetch(profile, labels)
		if ok {
			profile = profile[:n]
			labels = labels[:n]
			break
		}
	}

	return writeCountProfile(w, name, &runtimeProfile{profile, labels})
}

func writeBlock(w io.Writer) error {
	return writeCycleProfile(w, "contentions", "delay", runtimeBlockProfileInternal)
}

func writeMutex(w io.Writer) error {
	return writeCycleProfile(w, "contentions", "delay", runtimeMutexProfileInternal)
}

func writeCycleProfile(w io.Writer, countName string, cycleName string, fetch func([]blockProfileRecord) (int, bool)) error {
	var profile []blockProfileRecord
	n, _ := fetch(nil)
	var ok bool
	for {
		profile = make([]blockProfileRecord, n+50)
		n, ok = fetch(profile)
		if ok {
			profile = profile[:n]
			break
		}
	}

	sort.Slice(profile, func(i, j int) bool {
		return profile[i].Cycles > profile[j].Cycles
	})

	builder := newProfileBuilder(w)
	builder.pbValueType(tagProfile_PeriodType, countName, "count")
	builder.pb.int64Opt(tagProfile_Period, 1)
	builder.pbValueType(tagProfile_SampleType, countName, "count")
	builder.pbValueType(tagProfile_SampleType, cycleName, "nanoseconds")

	cpuGHz := float64(runtimeCyclesPerSecond()) / 1e9
	values := []int64{0, 0}
	var locs []uint64
	expandedStack := runtimeMakeProfStack()
	for _, record := range profile {
		values[0] = record.Count
		if cpuGHz > 0 {
			values[1] = int64(float64(record.Cycles) / cpuGHz)
		} else {
			values[1] = 0
		}
		n := expandInlinedFrames(expandedStack, record.Stack)
		locs = builder.appendLocsForStack(locs[:0], expandedStack[:n])
		builder.pbSample(values, locs, nil)
	}

	return builder.build()
}

type countProfile interface {
	Len() int
	Stack(i int) []uintptr
	Label(i int) *labelMap
}

type runtimeProfile struct {
	stk    []stackRecord
	labels []unsafe.Pointer
}

func (p *runtimeProfile) Len() int {
	return len(p.stk)
}

func (p *runtimeProfile) Stack(i int) []uintptr {
	return p.stk[i].Stack
}

func (p *runtimeProfile) Label(i int) *labelMap {
	return (*labelMap)(p.labels[i])
}

func writeCountProfile(w io.Writer, name string, profile countProfile) error {
	var buf strings.Builder
	key := func(stk []uintptr, labels *labelMap) string {
		buf.Reset()
		buf.WriteByte('@')
		for _, pc := range stk {
			fmt.Fprintf(&buf, " %#x", pc)
		}
		if labels != nil {
			buf.WriteString("\n# labels:")
			for _, label := range labels.list {
				fmt.Fprintf(&buf, " %q:%q", label.key, label.value)
			}
		}
		return buf.String()
	}

	counts := make(map[string]int)
	index := make(map[string]int)
	var keys []string
	for i := 0; i < profile.Len(); i++ {
		k := key(profile.Stack(i), profile.Label(i))
		if counts[k] == 0 {
			index[k] = i
			keys = append(keys, k)
		}
		counts[k]++
	}

	sort.Sort(&keysByCount{keys: keys, count: counts})

	builder := newProfileBuilder(w)
	builder.pbValueType(tagProfile_PeriodType, name, "count")
	builder.pb.int64Opt(tagProfile_Period, 1)
	builder.pbValueType(tagProfile_SampleType, name, "count")

	values := []int64{0}
	var locs []uint64
	for _, k := range keys {
		values[0] = int64(counts[k])
		idx := index[k]
		locs = builder.appendLocsForStack(locs[:0], profile.Stack(idx))

		var labels func()
		if profile.Label(idx) != nil {
			labels = func() {
				for _, label := range profile.Label(idx).list {
					builder.pbLabel(tagSample_Label, label.key, label.value, 0)
				}
			}
		}
		builder.pbSample(values, locs, labels)
	}

	return builder.build()
}

type keysByCount struct {
	keys  []string
	count map[string]int
}

func (x *keysByCount) Len() int {
	return len(x.keys)
}

func (x *keysByCount) Swap(i int, j int) {
	x.keys[i], x.keys[j] = x.keys[j], x.keys[i]
}

func (x *keysByCount) Less(i int, j int) bool {
	ki, kj := x.keys[i], x.keys[j]
	ci, cj := x.count[ki], x.count[kj]
	if ci != cj {
		return ci > cj
	}
	return ki < kj
}

func expandInlinedFrames(dst []uintptr, pcs []uintptr) int {
	frames := runtime.CallersFrames(pcs)
	var n int
	for n < len(dst) {
		frame, more := frames.Next()
		dst[n] = frame.PC + 1
		n++
		if !more {
			break
		}
	}
	return n
}

// writeHeapProto mirrors runtime/pprof.writeHeapProto of the pinned toolchain. The sample values it
// derives changed with Go 1.27 together with the record layout: the scale correction is applied per
// object (count, object size) instead of per record (count, total bytes), and the "bytes" label is
// the object size rather than the average block size. Keeping the old arithmetic on the new record
// would not crash - it would report inuse_space inflated by the record's object count, which is the
// quieter half of the same bug.
func writeHeapProto(w io.Writer, profile []memProfileRecord, rate int64, defaultSampleType string) error {
	builder := newProfileBuilder(w)
	builder.pbValueType(tagProfile_PeriodType, "space", "bytes")
	builder.pb.int64Opt(tagProfile_Period, rate)
	builder.pbValueType(tagProfile_SampleType, "alloc_objects", "count")
	builder.pbValueType(tagProfile_SampleType, "alloc_space", "bytes")
	builder.pbValueType(tagProfile_SampleType, "inuse_objects", "count")
	builder.pbValueType(tagProfile_SampleType, "inuse_space", "bytes")
	if defaultSampleType != "" {
		builder.pb.int64Opt(tagProfile_DefaultSampleType, builder.stringIndex(defaultSampleType))
	}

	values := []int64{0, 0, 0, 0}
	var locs []uint64
	for _, record := range profile {
		hideRuntime := true
		for range 2 {
			stk := record.Stack
			if hideRuntime {
				for i, addr := range stk {
					if f := runtime.FuncForPC(addr); f != nil && (strings.HasPrefix(f.Name(), "runtime.") || strings.HasPrefix(f.Name(), "internal/runtime/")) {
						continue
					}
					stk = stk[i:]
					break
				}
			}
			locs = builder.appendLocsForStack(locs[:0], stk)
			if len(locs) > 0 {
				break
			}
			hideRuntime = false
		}

		values[0], values[1] = scaleHeapSample(record.AllocObjects, record.ObjectSize, rate)
		values[2], values[3] = scaleHeapSample(record.InUseObjects(), record.ObjectSize, rate)
		builder.pbSample(values, locs, func() {
			builder.pbLabel(tagSample_Label, "bytes", "", record.ObjectSize)
		})
	}

	return builder.build()
}

// scaleHeapSample adjusts a heap sample for its probability of appearing in the collected data, and
// returns (objects, bytes). avgSize is the size of ONE object, which is what Go 1.27's record carries;
// the pre-1.27 signature took the total size of the record and divided it by count internally.
func scaleHeapSample(count, avgSize, rate int64) (int64, int64) {
	if count == 0 || avgSize == 0 {
		return 0, 0
	}
	if rate <= 1 {
		// If rate==1 all samples were collected so no adjustment is needed.
		// If rate<1 treat as unknown and skip scaling.
		return count, count * avgSize
	}

	scale := 1 / (1 - math.Exp(-float64(avgSize)/float64(rate)))

	return int64(float64(count) * scale), int64(float64(count*avgSize) * scale)
}

type profileBuilder struct {
	start time.Time
	w     io.Writer
	err   error

	pb        protobuf
	strings   []string
	stringMap map[string]int
	locs      map[uintptr]locInfo
	funcs     map[string]int
	mem       []memMap
	deck      pcDeck
}
