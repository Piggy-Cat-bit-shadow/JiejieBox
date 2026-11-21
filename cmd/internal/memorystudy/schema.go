package main

// schemaVersion is the contract every consumer of the JSONL depends on.
//
// Bump it when a field is removed, renamed, or changes meaning. Adding a field does not need a
// bump, but a reader that would misread a MISSING field has to be updated with the writer either
// way: an absent measurement is always null, never 0, and a reader that sums nulls as zero turns
// "unsupported on this platform" into "it costs nothing".
const schemaVersion = "memorystudy/1"

// recordStage is written by a child process for one measurement. recordChildResult is written by
// the parent, once per child, because a process cannot report its own exit status.
const (
	recordStage       = "stage"
	recordChildResult = "child_result"
)

// A stage is ok when it ran, unsupported when this fixture or host cannot produce it at all, and
// failed when it was supposed to run and did not. Only ok is evidence; the gate treats the other
// two as "not measured", never as a pass.
const (
	statusOK          = "ok"
	statusFailed      = "failed"
	statusUnsupported = "unsupported"
)

// Stage names are the ledger's spine: the same string in every run, every report and every budget
// rule, so a rule that compares two runs is comparing the same boundary.
const (
	// stageEarliestObservable is the first measurement this process can take. Everything the Go
	// runtime and the imported packages' init functions did before it is NOT captured; see the
	// unobservable list that the record carries.
	stageEarliestObservable = "earliest_observable"
	// stageSetup is the libbox-Setup analogue: every protocol registry the product registers.
	stageSetup = "setup"
	// stageFixtureServers is the measurement harness's own cost, kept on its own boundary so it
	// cannot be mistaken for a cost of the product.
	stageFixtureServers = "fixture_servers"
	// stageConfigBuild is the fixture document being turned into bytes - the input side of the
	// configuration-processing cost.
	stageConfigBuild = "config_build"
	// stageConfigParse is the document being decoded into the option tree.
	stageConfigParse = "config_parse"
	// stageConstruct is box.New: options become outbounds, inbounds, router and services.
	stageConstruct = "construct"
	// stageServiceStart is Box.Start returning: listeners bound, services started.
	stageServiceStart = "service_start"
	// stageReady is the first accepted connection on the inbound, i.e. the listener really is
	// accepting rather than merely constructed.
	stageReady = "ready"
	// stageProbe is a completed URL test round.
	stageProbe = "probe"
	// stageTrafficHeld is N established flows sitting quiet through the relay.
	stageTrafficHeld = "traffic_held"
	// stageTrafficVerified is the same flows after the integrity and half-close checks.
	stageTrafficVerified = "traffic_verified"
	// stageClose is after Box.Close and after the goroutines have come back down.
	stageClose = "close"
	// stageRunEnd is the child's last word. Its presence is what makes a run complete evidence.
	stageRunEnd = "run_end"
)

// idleStage names an idle checkpoint. Idle checkpoints are cumulative within one run: "30s" then
// "300s" means the process has been idle for thirty seconds at the first and three hundred at the
// second, not five and a half minutes.
func idleStage(duration string) string { return "idle_" + duration }

// idleAgainStage names the post-activity idle checkpoint.
func idleAgainStage(duration string) string { return "idle_again_" + duration }

// stageRecord is one JSONL line written by a child process.
//
// Every line carries the full identity, not just the first line of a run. A ledger is evidence:
// a line that can only be read by joining it with another line is a line that gets quoted without
// its provenance the moment somebody greps the file.
type stageRecord struct {
	SchemaVersion string `json:"schema_version"`
	Record        string `json:"record"`
	Status        string `json:"status"`
	RunID         string `json:"run_id"`
	// PID is the process that wrote this line. For a child stage that is the measured process; for
	// a parent-written child_result it is the parent, and child_pid names the child.
	PID             int    `json:"pid"`
	ChildPID        int    `json:"child_pid,omitempty"`
	Seq             int    `json:"seq"`
	Stage           string `json:"stage"`
	StageIndex      int    `json:"stage_index"`
	ObservedAt      string `json:"observed_at"`
	OffsetNS        int64  `json:"offset_ns"`
	StageDurationNS int64  `json:"stage_duration_ns"`

	Spec     runSpec  `json:"spec"`
	Identity identity `json:"identity"`

	Readings readings `json:"readings"`
	Deltas   deltas   `json:"deltas"`

	Traffic *trafficReport `json:"traffic,omitempty"`
	Probe   *probeReport   `json:"probe,omitempty"`

	// Unobservable lists the boundaries this stage is standing on but cannot see past. It is not
	// decoration: "the runtime's own startup is not in this number" is part of the number.
	Unobservable []string `json:"unobservable,omitempty"`
	Notes        []string `json:"notes,omitempty"`
	Error        string   `json:"error,omitempty"`

	// ExitCode is set on child_result lines only.
	ExitCode     *int   `json:"exit_code,omitempty"`
	LogPath      string `json:"log_path,omitempty"`
	WallMillis   int64  `json:"wall_millis,omitempty"`
	RunEndSeen   bool   `json:"run_end_seen,omitempty"`
	FailureCause string `json:"failure_cause,omitempty"`
}

// runSpec is the normalised case a child was asked to run. It is echoed on every line so two runs
// can be compared without trusting a filename.
type runSpec struct {
	Case         string   `json:"case"`
	Nodes        int      `json:"nodes"`
	Groups       int      `json:"groups"`
	NodeProto    string   `json:"node_proto"`
	Activity     []string `json:"activity"`
	Idle         []string `json:"idle"`
	PostIdle     string   `json:"post_idle"`
	GCMode       string   `json:"gc_mode"`
	HeldFlows    int      `json:"held_flows"`
	TrafficBytes int      `json:"traffic_bytes"`
}

// identity is what the run was: source revision, toolchain, build configuration, and the runtime
// policy that was ACTUALLY in force. Every value here is read from the running process; none of it
// is a value the runner hoped for.
type identity struct {
	Source      sourceIdentity    `json:"source"`
	Build       buildIdentity     `json:"build"`
	Policy      policyIdentity    `json:"policy"`
	Overlay     overlayIdentity   `json:"overlay"`
	Device      deviceIdentity    `json:"device"`
	Unsupported map[string]string `json:"unsupported,omitempty"`
}

type sourceIdentity struct {
	SHA        string `json:"sha"`
	Short      string `json:"short"`
	Dirty      bool   `json:"dirty"`
	DetectedBy string `json:"detected_by"`
	// GitSHA and GitDirty are the run-time working tree, which is what a reviewer needs when a
	// binary was built before the last edit. BuildSHA is what the linker stamped.
	GitSHA        string   `json:"git_sha,omitempty"`
	GitDirty      *bool    `json:"git_dirty,omitempty"`
	BuildSHA      string   `json:"build_sha,omitempty"`
	BuildDirty    *bool    `json:"build_dirty,omitempty"`
	Branch        string   `json:"branch,omitempty"`
	ModuleVersion string   `json:"module_version,omitempty"`
	Root          string   `json:"root,omitempty"`
	DirtyPaths    []string `json:"dirty_paths,omitempty"`
	// DeclaredOrigin explains a revision that was declared rather than measured, e.g. a baseline
	// built from an archive of a known commit.
	DeclaredOrigin string `json:"declared_origin,omitempty"`
}

type buildIdentity struct {
	GoVersion    string            `json:"go_version"`
	GOOS         string            `json:"goos"`
	GOARCH       string            `json:"goarch"`
	Tags         []string          `json:"tags"`
	TagsRaw      string            `json:"tags_raw"`
	CGOEnabled   string            `json:"cgo_enabled"`
	GOAMD64      string            `json:"goamd64,omitempty"`
	GOEXPERIMENT string            `json:"goexperiment,omitempty"`
	LDFlags      string            `json:"ldflags,omitempty"`
	BuildMode    string            `json:"build_mode,omitempty"`
	Compiler     string            `json:"compiler,omitempty"`
	Settings     map[string]string `json:"settings,omitempty"`
	BinaryPath   string            `json:"binary_path,omitempty"`
	BinaryBytes  int64             `json:"binary_bytes,omitempty"`
}

// policyIdentity is the Runtime policy in force, read from the process rather than from the flags
// that were supposed to produce it.
type policyIdentity struct {
	GOMAXPROCS       int    `json:"gomaxprocs"`
	GOMAXPROCSOrigin string `json:"gomaxprocs_origin"`
	GOMAXPROCSEnv    string `json:"gomaxprocs_env,omitempty"`
	// GODEBUGEnv is what the process inherited. GODEBUILDBuildDefault is the Go version's default
	// string stamped by the linker; the two are NOT the same thing and an empty env does not mean
	// "no GODEBUG in force".
	GODEBUGEnv            string `json:"godebug_env,omitempty"`
	GODEBUGBuildDefault   string `json:"godebug_build_default,omitempty"`
	MemProfileRateBytes   *int64 `json:"memprofile_rate_bytes"`
	MemProfileRateOrigin  string `json:"memprofile_rate_origin"`
	AdaptiveStackStart    *bool  `json:"adaptivestackstart"`
	AdaptiveStackStartObs bool   `json:"adaptivestackstart_observable"`
	AdaptiveStackReason   string `json:"adaptivestackstart_reason,omitempty"`
	// GODebugNonDefault is the runtime's own count of every GODEBUG setting that has actually
	// fired with a non-default behaviour. It is the only "did this setting do anything" signal Go
	// publishes.
	GODebugNonDefault map[string]uint64 `json:"godebug_non_default_events,omitempty"`
	GOMEMLIMITBytes   *uint64           `json:"gomemlimit_bytes,omitempty"`
	GOGCPercent       *uint64           `json:"gogc_percent,omitempty"`
}

// overlayIdentity records whether any product runtime overlay is linked. memorystudy is a plain
// Go binary on purpose: measuring the product's overlay while also changing it would make the
// instrument part of the experiment. It reports what it inherited instead of pretending.
type overlayIdentity struct {
	Mode                 string `json:"mode"`
	LinkedGODEBUGDefault string `json:"linked_godebug_default,omitempty"`
	Detail               string `json:"detail"`
}

type deviceIdentity struct {
	Supported          bool    `json:"supported"`
	PhysFootprintBytes *uint64 `json:"phys_footprint_bytes"`
	PeakFootprintBytes *uint64 `json:"peak_footprint_bytes"`
	Reason             string  `json:"reason"`
	// HostProcessAreNotFootprint is always present on a host run: a Windows working set is not an
	// Apple phys_footprint and must never be quoted as one.
	HostProcessAreNotFootprint bool `json:"host_process_metrics_are_not_footprint"`
}

type readings struct {
	Go         goReadings        `json:"go"`
	Goroutines goroutineReadings `json:"goroutines"`
	Process    processReadings   `json:"process"`
	Objects    objectReadings    `json:"objects"`
	Device     deviceReadings    `json:"device"`
	// Unsupported explains every null above. A null with no entry here is a bug in the instrument,
	// not a platform limitation.
	Unsupported map[string]string `json:"unsupported,omitempty"`
}

type goReadings struct {
	LiveHeapBytes *uint64 `json:"live_heap_bytes"`
	// LiveHeapFromLastGC says whether live_heap_bytes describes this instant or the last time the
	// runtime published it.
	//
	// # Why this field exists
	//
	// /gc/heap/live:bytes is written at the END of a GC cycle. In a natural run - the one that is not
	// allowed to force anything - two consecutive readings with no collection between them report the
	// SAME value, and a consumer that compared them would conclude the stage cost nothing. That is a
	// stale reading, not a measurement, and the difference matters: a 300-second natural idle read
	// 2 508 040 B at 30 s and 2 447 320 B at 300 s, which is one collection's worth of change, not
	// two measurements of growth. When this is false the live heap is null and heap_objects_bytes is
	// the current value to read instead.
	LiveHeapFromLastGC     bool             `json:"live_heap_from_last_gc"`
	LiveObjects            *uint64          `json:"live_objects"`
	HeapObjectsBytes       *uint64          `json:"heap_objects_bytes"`
	HeapStacksBytes        *uint64          `json:"heap_stacks_bytes"`
	HeapUnusedBytes        *uint64          `json:"heap_unused_bytes"`
	HeapReleasedBytes      *uint64          `json:"heap_released_bytes"`
	HeapFreeBytes          *uint64          `json:"heap_free_bytes"`
	TotalBytes             *uint64          `json:"total_bytes"`
	Metadata               metadataReadings `json:"metadata"`
	ProfilingBucketsBytes  *uint64          `json:"profiling_buckets_bytes"`
	Scan                   scanReadings     `json:"scan"`
	CumulativeAllocBytes   *uint64          `json:"cumulative_alloc_bytes"`
	CumulativeAllocObjects *uint64          `json:"cumulative_alloc_objects"`
	// AllocSizeClassesTouched is how many distinct allocation size classes the process has
	// exercised. It is a shape signal, not a footprint: it says whether a configuration allocates
	// in a handful of sizes or across the whole table.
	AllocSizeClassesTouched *uint64     `json:"alloc_size_classes_touched"`
	MemProfileRateBytes     *int64      `json:"memprofile_rate_bytes"`
	RuntimeThreads          *uint64     `json:"runtime_threads"`
	GOMAXPROCS              *uint64     `json:"gomaxprocs"`
	GC                      gcReadings  `json:"gc"`
	CPU                     cpuReadings `json:"cpu"`
}

type metadataReadings struct {
	MSpanInuseBytes  *uint64 `json:"mspan_inuse_bytes"`
	MSpanFreeBytes   *uint64 `json:"mspan_free_bytes"`
	MCacheInuseBytes *uint64 `json:"mcache_inuse_bytes"`
	MCacheFreeBytes  *uint64 `json:"mcache_free_bytes"`
	OtherBytes       *uint64 `json:"other_bytes"`
	OSStacksBytes    *uint64 `json:"os_stacks_bytes"`
}

type scanReadings struct {
	HeapBytes    *uint64 `json:"heap_bytes"`
	StackBytes   *uint64 `json:"stack_bytes"`
	GlobalsBytes *uint64 `json:"globals_bytes"`
	TotalBytes   *uint64 `json:"total_bytes"`
}

type gcReadings struct {
	CyclesTotal     *uint64 `json:"cycles_total"`
	CyclesForced    *uint64 `json:"cycles_forced"`
	CyclesAutomatic *uint64 `json:"cycles_automatic"`
	GoalBytes       *uint64 `json:"goal_bytes"`
}

type cpuReadings struct {
	GCTotalSeconds      *float64 `json:"gc_total_seconds"`
	ScavengeTotalSecond *float64 `json:"scavenge_total_seconds"`
	UserTotalSeconds    *float64 `json:"user_total_seconds"`
}

type goroutineReadings struct {
	Current  *uint64 `json:"current"`
	Running  *uint64 `json:"running"`
	Runnable *uint64 `json:"runnable"`
	Waiting  *uint64 `json:"waiting"`
	NotInGo  *uint64 `json:"not_in_go"`
	Created  *uint64 `json:"created"`
}

// processReadings is the HOST's view. Threads and file descriptors are real operating-system
// resources that Go's own metrics do not count; working set and private commit are the nearest host
// analogue of a footprint and are labelled as such everywhere they appear.
type processReadings struct {
	Supported          bool    `json:"supported"`
	Threads            *uint64 `json:"threads"`
	FileDescriptors    *uint64 `json:"file_descriptors"`
	WorkingSetBytes    *uint64 `json:"working_set_bytes"`
	PrivateCommitBytes *uint64 `json:"private_commit_bytes"`
	// NativeResidualBytesProxy is private commit minus Go's total. It is a PROXY for non-Go
	// resident memory (cgo arenas, mapped files, thread stacks the runtime does not count) and can
	// be negative when Go's accounting includes memory the OS has not committed. It is never a
	// device footprint.
	NativeResidualBytesProxy *int64 `json:"native_residual_bytes_proxy"`
	Reason                   string `json:"reason,omitempty"`
}

// objectReadings counts what the process is actually running. These are exact counts, so 0 here
// means zero - unlike a metric, which uses null for "not available".
type objectReadings struct {
	Outbounds    int `json:"outbounds"`
	Inbounds     int `json:"inbounds"`
	Endpoints    int `json:"endpoints"`
	Providers    int `json:"providers"`
	Connections  int `json:"connections"`
	SessionsHeld int `json:"sessions_held"`
}

type deviceReadings struct {
	Supported          bool    `json:"supported"`
	PhysFootprintBytes *uint64 `json:"phys_footprint_bytes"`
	PeakFootprintBytes *uint64 `json:"peak_footprint_bytes"`
	Reason             string  `json:"reason,omitempty"`
}

// deltas carries the same increments twice, because both are legitimate questions and only one of
// them is a budget.
//
//   - FromEntry is the ABSOLUTE ledger: growth since this process's earliest observable point. It
//     is what the whole process costs the device, and it cannot be made small by hiding work before
//     a baseline.
//   - FromPrevious is the INCREMENTAL budget: what this stage added over the one before it. It is
//     what a regression gate can attribute to a change.
type deltas struct {
	FromEntry    *readingDelta `json:"from_entry"`
	FromPrevious *readingDelta `json:"from_previous"`
}

type readingDelta struct {
	Against               string `json:"against"`
	LiveHeapBytes         *int64 `json:"live_heap_bytes"`
	LiveObjects           *int64 `json:"live_objects"`
	HeapStacksBytes       *int64 `json:"heap_stacks_bytes"`
	HeapObjectsBytes      *int64 `json:"heap_objects_bytes"`
	TotalBytes            *int64 `json:"total_bytes"`
	MetadataBytes         *int64 `json:"metadata_bytes"`
	ProfilingBucketsBytes *int64 `json:"profiling_buckets_bytes"`
	ScanTotalBytes        *int64 `json:"scan_total_bytes"`
	CumulativeAllocBytes  *int64 `json:"cumulative_alloc_bytes"`
	// GCCyclesBetween is how many collections happened between the two readings this delta spans.
	//
	// A live-heap delta taken across ZERO collections is not a statement about growth: both readings
	// are the same published value, so the delta is structurally 0. This field makes that visible
	// instead of letting a natural-idle comparison report "no growth" for free.
	GCCyclesBetween *int64 `json:"gc_cycles_between"`
	Goroutines      *int64 `json:"goroutines"`
	Threads         *int64 `json:"threads"`
	FileDescriptors *int64 `json:"file_descriptors"`
	Connections     *int64 `json:"connections"`
	// LiveHeapPerNodeBytes divides the live-heap increment by the configured node count. It is only
	// emitted for cases that configure nodes, and it is only meaningful for a delta that is
	// dominated by the nodes themselves.
	LiveHeapPerNodeBytes *int64 `json:"live_heap_per_node_bytes,omitempty"`
}

// trafficReport is the business check that keeps a memory number from being bought by breaking the
// relay: exact bytes in both directions, and a response that only exists because the half-close was
// propagated.
type trafficReport struct {
	Flows                  int      `json:"flows"`
	RequestBytes           int64    `json:"request_bytes"`
	ResponseBytes          int64    `json:"response_bytes"`
	RequestIntact          bool     `json:"request_intact"`
	ResponseIntact         bool     `json:"response_intact"`
	HalfClosePropagated    bool     `json:"half_close_propagated"`
	ResponseAfterHalfClose bool     `json:"response_after_half_close"`
	Verified               bool     `json:"verified"`
	Failures               []string `json:"failures,omitempty"`
}

type probeReport struct {
	Group     string `json:"group"`
	URL       string `json:"url"`
	Members   int    `json:"members"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
	Error     string `json:"error,omitempty"`
}
