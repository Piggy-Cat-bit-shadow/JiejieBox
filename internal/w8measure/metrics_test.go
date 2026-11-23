package w8measure

// M — independent W8 measurement harness (evidence only; no production code).
//
// This package contains TEST FILES ONLY. It is deliberately not part of any product
// build: `go test ./internal/w8measure` is the only entry point, and every measured
// action goes through a production entry point (remote rule-set/provider HTTP path,
// ConnectionManager.NewConnection relay copy path, Box/StartedService lifecycle).
//
// # Sample discipline
//
// Every measurement point is one PROCESS (one `go test -run <Test>` invocation) that
// appends exactly one JSON row to $W8_OUT. The driver script
// scripts/measure/w8/run.ps1 (and run.sh) starts >=5 such processes per point, alternating
// reference/candidate, and aggregates the medians. `-count=N` inside one process is NOT
// used: it would share the heap, the page cache and the mapped pages between samples.
//
// # Identities in every row
//
//	w8.side     reference | candidate      (supplied by the driver)
//	w8.sha      git commit measured        (supplied by the driver)
//	w8.tree     clean | dirty:<n>          (supplied by the driver)
//	w8.tags     build tags                 (supplied by the driver)
//	go          runtime.Version()
//	low_memory  sing's build-tag geometry
//
// # What is separated
//
//	heap        HeapAlloc/HeapObjects/HeapInuse/Mallocs/TotalAlloc (runtime.ReadMemStats)
//	process     RSS, PeakRSS, OS read/write operation counters, handle/FD count
//	natural     sampled with no forced collection
//	forced      explicitly labelled "after_gc" attribution only, never in a product path
//
// The harness never installs a forced GC or FreeOSMemory anywhere in a product call path.
// The only collections this package asks for are labelled attribution points after a
// measured action has returned, and each row says which numbers those are.

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/common"
)

// w8Mem is one runtime.MemStats sample. TotalAlloc is cumulative; every other field is
// live at the instant of the read.
type w8Mem struct {
	HeapAlloc    uint64 `json:"heap_alloc"`
	HeapInuse    uint64 `json:"heap_inuse"`
	HeapObjects  uint64 `json:"heap_objects"`
	HeapReleased uint64 `json:"heap_released"`
	TotalAlloc   uint64 `json:"total_alloc"`
	Mallocs      uint64 `json:"mallocs"`
	Frees        uint64 `json:"frees"`
	NumGC        uint32 `json:"num_gc"`
}

func w8ReadMem() w8Mem {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return w8Mem{
		HeapAlloc:    stats.HeapAlloc,
		HeapInuse:    stats.HeapInuse,
		HeapObjects:  stats.HeapObjects,
		HeapReleased: stats.HeapReleased,
		TotalAlloc:   stats.TotalAlloc,
		Mallocs:      stats.Mallocs,
		Frees:        stats.Frees,
		NumGC:        stats.NumGC,
	}
}

// w8Proc is one process-level sample. The OS counters are read from the kernel, not
// inferred from Go allocations.
type w8Proc struct {
	RSS       uint64 `json:"rss"`
	PeakRSS   uint64 `json:"peak_rss"`
	ReadOps   uint64 `json:"read_ops"`
	WriteOps  uint64 `json:"write_ops"`
	ReadBytes uint64 `json:"read_bytes"`
	WriteBytes uint64 `json:"write_bytes"`
	HandleCnt uint64 `json:"handles"`
	MemOK     bool   `json:"mem_ok"`
	IOOK      bool   `json:"io_ok"`
	HandleOK  bool   `json:"handle_ok"`
}

func w8ReadProc() w8Proc {
	rss, peak, memOK := readProcessMemory()
	readOps, writeOps, readBytes, writeBytes, ioOK := readProcessIO()
	handles, handleOK := readHandleCount()
	return w8Proc{
		RSS:        rss,
		PeakRSS:    peak,
		ReadOps:    readOps,
		WriteOps:   writeOps,
		ReadBytes:  readBytes,
		WriteBytes: writeBytes,
		HandleCnt:  handles,
		MemOK:      memOK,
		IOOK:       ioOK,
		HandleOK:   handleOK,
	}
}

// w8ProcSampler samples PeakRSS/IO at the boundaries of a measured action. The OS peak
// working set is monotonic, so a single before/after pair is exact for the whole process
// lifetime; the delta is what the action added.
type w8ProcSampler struct {
	proc   w8Proc
	mem    w8Mem
	forced bool
}

// w8Begin captures the natural state before an action. It does NOT collect.
func w8Begin() w8ProcSampler {
	return w8ProcSampler{proc: w8ReadProc(), mem: w8ReadMem()}
}

// w8BeginAttributed captures the state after a labelled forced collection. The two
// collections make the live heap attributable; they are never inside a product call.
func w8BeginAttributed() w8ProcSampler {
	runtime.GC()
	runtime.GC()
	return w8ProcSampler{proc: w8ReadProc(), mem: w8ReadMem(), forced: true}
}

func (s w8ProcSampler) delta() w8Proc {
	after := w8ReadProc()
	delta := w8Proc{
		RSS:        after.RSS,
		PeakRSS:    after.PeakRSS,
		ReadOps:    after.ReadOps - s.proc.ReadOps,
		WriteOps:   after.WriteOps - s.proc.WriteOps,
		ReadBytes:  after.ReadBytes - s.proc.ReadBytes,
		WriteBytes: after.WriteBytes - s.proc.WriteBytes,
		HandleCnt:  after.HandleCnt,
		MemOK:      after.MemOK,
		IOOK:       after.IOOK && s.proc.IOOK,
		HandleOK:   after.HandleOK,
	}
	return delta
}

// w8Identity returns the identity fields every row carries.
func w8Identity() map[string]any {
	return map[string]any{
		"w8_side": w8Env("W8_SIDE", "unknown"),
		"w8_sha":  w8Env("W8_SHA", "unknown"),
		"w8_tree": w8Env("W8_TREE", "unknown"),
		"w8_tags": w8Env("W8_TAGS", "unknown"),
		"w8_arm":  w8Env("W8_ARM", ""),
		"point":   w8Env("W8_POINT", ""),
		"go":      runtime.Version(),
		"goos":    runtime.GOOS,
		"goarch":  runtime.GOARCH,
		"numcpu":  runtime.NumCPU(),
		// sing's with_low_memory geometry. It changes the copy buffer size and is part
		// of the measurement identity, not a runtime decision.
		"low_memory": common.LowMemory,
	}
}

// w8Now is the wall clock at the start of a row, so the window is auditable against the
// driver log.
func w8Now() string { return time.Now().Format(time.RFC3339Nano) }

// w8Emit appends one JSON row to $W8_OUT and logs it. A missing W8_OUT is not an error:
// the row is still in the test log, and the aggregate step can read it from there.
func w8Emit(t *testing.T, row map[string]any) {
	t.Helper()
	if _, exists := row["t"]; !exists {
		row["t"] = w8Now()
	}
	for key, value := range w8Identity() {
		if _, exists := row[key]; !exists {
			row[key] = value
		}
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("encode row: %v", err)
	}
	t.Logf("W8ROW %s", encoded)
	output := os.Getenv("W8_OUT")
	if output == "" {
		return
	}
	file, openErr := os.OpenFile(output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if openErr != nil {
		t.Fatalf("open W8_OUT %s: %v", output, openErr)
	}
	defer file.Close()
	if _, writeErr := file.WriteString(string(encoded) + "\n"); writeErr != nil {
		t.Fatalf("write W8_OUT %s: %v", output, writeErr)
	}
}

func w8Env(name string, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}

func w8EnvInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s=%q is not an integer: %v", name, raw, err)
	}
	return value
}

func w8EnvDuration(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		t.Fatalf("%s=%q is not a duration: %v", name, raw, err)
	}
	return value
}

// w8Dir returns a fresh scratch directory inside $W8_ROOT (the driver gives every sample
// its own root, so a previous sample's page cache and files cannot be reused by accident).
func w8Dir(t *testing.T, name string) string {
	t.Helper()
	root := os.Getenv("W8_ROOT")
	if root == "" {
		root = t.TempDir()
	}
	dir := root + string(os.PathSeparator) + name
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	return dir
}

// w8Counter is a tiny atomic counter used in places where sync/atomic's typed API would
// need a wrapper type anyway.
type w8Counter struct{ value atomic.Int64 }

func (c *w8Counter) add(delta int64) { c.value.Add(delta) }
func (c *w8Counter) load() int64     { return c.value.Load() }

// w8Median returns the median of a sorted-in-place copy of values and the extremes.
func w8Median(values []float64) (median float64, low float64, high float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	sorted := append([]float64(nil), values...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	return sorted[len(sorted)/2], sorted[0], sorted[len(sorted)-1]
}

// w8Percentile returns the percentile of an unsorted sample using nearest-rank.
func w8Percentile(values []time.Duration, fraction float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	index := int(fraction * float64(len(sorted)-1))
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

// w8FormatBytes is used only in log lines.
func w8FormatBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%dB", value)
	}
	div, exp := int64(unit), 0
	for n := value / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(value)/float64(div), "KMGTPE"[exp])
}

// w8TagsFromEnv is only used to make a log line readable.
func w8TagsFromEnv() string {
	return strings.Join(strings.Fields(w8Env("W8_TAGS", "")), ",")
}
