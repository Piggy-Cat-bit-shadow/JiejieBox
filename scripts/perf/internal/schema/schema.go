// Package schema defines the one result format every performance run in this
// repository is written in.
//
// Two rules shape it:
//
//   - A missing value is null, never zero. "0 MiB/s" and "not measured" are
//     different facts and a report that cannot tell them apart is worse than no
//     report.
//   - Units are part of the field name. A field called "goodput" forces every
//     reader to guess; "goodput_mib_per_s" does not.
//
// The schema is additive: protocol-specific numbers live under
// Scenario.Extra and Rep.Extra rather than being flattened into a shared field
// that means something different per protocol.
package schema

import "time"

// Version is the schema version written into every run.
const Version = "perf-run/v1"

// Kind distinguishes a real end-to-end run from a Go micro-benchmark run. The
// two are never ranked against each other, and both carry the same RunID so a
// micro-benchmark can be joined to the e2e run it was taken next to.
type Kind string

const (
	KindE2E   Kind = "e2e"
	KindMicro Kind = "micro"
)

// Run is one scenario, one binary, one network profile.
type Run struct {
	SchemaVersion string    `json:"schema_version"`
	RunID         string    `json:"run_id"`
	Kind          Kind      `json:"kind"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	Source        Source    `json:"source"`
	Host          Host      `json:"host"`
	Network       Network   `json:"network"`
	Scenario      Scenario  `json:"scenario"`
	Environment   []EnvVar  `json:"environment,omitempty"`
	Reps          []Rep     `json:"reps"`
	Summary       []Summary `json:"summary"`
	Artifacts     Artifacts `json:"artifacts"`
	Failures      []Failure `json:"failures,omitempty"`
	Notes         []string  `json:"notes,omitempty"`
	Method        string    `json:"method,omitempty"`
}

// Source ties a number to the code that produced it.
type Source struct {
	Repo          string            `json:"repo,omitempty"`
	Branch        string            `json:"branch,omitempty"`
	Commit        string            `json:"commit,omitempty"`
	CommitSubject string            `json:"commit_subject,omitempty"`
	CommitTime    string            `json:"commit_time,omitempty"`
	Dirty         *bool             `json:"dirty"`
	DirtyFiles    []string          `json:"dirty_files,omitempty"`
	GoVersion     string            `json:"go_version,omitempty"`
	BuildTags     []string          `json:"build_tags,omitempty"`
	CGOEnabled    *bool             `json:"cgo_enabled"`
	Modules       map[string]string `json:"modules,omitempty"`
}

// Host is the machine and the sampling regime.
type Host struct {
	OS                 string  `json:"os"`
	Arch               string  `json:"arch"`
	Kernel             string  `json:"kernel,omitempty"`
	Distro             string  `json:"distro,omitempty"`
	DistroVersion      string  `json:"distro_version,omitempty"`
	WSL                bool    `json:"wsl"`
	WSLKernelRelease   string  `json:"wsl_kernel_release,omitempty"`
	Glibc              string  `json:"glibc,omitempty"`
	CPUModel           string  `json:"cpu_model,omitempty"`
	CPUCores           int     `json:"cpu_cores,omitempty"`
	MemTotalBytes      *uint64 `json:"mem_total_bytes"`
	ClkTck             *int    `json:"clk_tck"`
	SamplingIntervalMS int     `json:"sampling_interval_ms"`
	SampleLimitNote    string  `json:"sample_limit_note,omitempty"`
}

// Network describes the path between the two proxy processes. An empty
// Topology means the run could not establish what it was measuring.
type Network struct {
	Topology      string      `json:"topology"`
	Profile       string      `json:"profile,omitempty"`
	DelayMS       *float64    `json:"delay_ms"`
	JitterMS      *float64    `json:"jitter_ms"`
	LossPercent   *float64    `json:"loss_percent"`
	BandwidthMbps *float64    `json:"bandwidth_mbps"`
	MTU           *int        `json:"mtu"`
	Qdiscs        []Qdisc     `json:"qdiscs,omitempty"`
	Interfaces    []Interface `json:"interfaces,omitempty"`
	Namespace     string      `json:"namespace,omitempty"`
	Note          string      `json:"note,omitempty"`
}

// Qdisc is one traffic-control rule as installed, not as intended.
type Qdisc struct {
	Device string `json:"device"`
	Kind   string `json:"kind"`
	Parent string `json:"parent,omitempty"`
	Args   string `json:"args"`
}

// Interface is one interface as seen from the namespace that ran the traffic.
type Interface struct {
	Name      string   `json:"name"`
	MTU       int      `json:"mtu"`
	Addresses []string `json:"addresses,omitempty"`
}

// Scenario names the protocol under test and the shape of the load.
type Scenario struct {
	ID            string         `json:"id"`
	Protocol      string         `json:"protocol"`
	Mode          string         `json:"mode,omitempty"`
	Transport     string         `json:"transport,omitempty"`
	Security      string         `json:"security,omitempty"`
	ServerVersion string         `json:"server_version,omitempty"`
	ClientVersion string         `json:"client_version,omitempty"`
	ServerConfig  string         `json:"server_config,omitempty"`
	ClientConfig  string         `json:"client_config,omitempty"`
	Extra         map[string]any `json:"extra,omitempty"`
}

// Rep is one measured repetition. It is the raw row: summaries are derived from
// these, never the other way round.
type Rep struct {
	Index           int            `json:"index"`
	Streams         int            `json:"streams"`
	Pattern         string         `json:"pattern"`
	PayloadBytes    int64          `json:"payload_bytes"`
	DurationSeconds float64        `json:"duration_seconds"`
	GoodputMiBPerS  *float64       `json:"goodput_mib_per_s"`
	Wire            *Wire          `json:"wire"`
	Latency         *Latency       `json:"latency"`
	UDP             *UDP           `json:"udp"`
	Client          *Proc          `json:"client"`
	Server          *Proc          `json:"server"`
	Loadgen         *Proc          `json:"loadgen"`
	Integrity       *Integrity     `json:"integrity"`
	Extra           map[string]any `json:"extra,omitempty"`
	Warnings        []string       `json:"warnings,omitempty"`
}

// Wire is what crossed the emulated link, as counted by the kernel.
//
// Payload goodput and wire throughput are different questions: the ratio
// between them is the protocol's overhead, and it is only visible when both are
// recorded.
type Wire struct {
	TxBytes       *int64   `json:"tx_bytes"`
	RxBytes       *int64   `json:"rx_bytes"`
	TxPackets     *int64   `json:"tx_packets"`
	RxPackets     *int64   `json:"rx_packets"`
	GbitPerSecond *float64 `json:"gbit_per_s"`
	PPS           *float64 `json:"pps"`
	OverheadRatio *float64 `json:"overhead_ratio"`
	Source        string   `json:"source,omitempty"`
}

// Latency is in milliseconds. Percentiles are only meaningful with enough
// samples; Count is recorded so a reader can reject a p99 taken from 20
// operations.
type Latency struct {
	Count  int      `json:"count"`
	MinMS  *float64 `json:"min_ms"`
	P50MS  *float64 `json:"p50_ms"`
	P95MS  *float64 `json:"p95_ms"`
	P99MS  *float64 `json:"p99_ms"`
	MaxMS  *float64 `json:"max_ms"`
	MeanMS *float64 `json:"mean_ms"`
}

// UDP counts datagrams, not bytes only: a datagram protocol can lose packets
// while its byte counter keeps climbing.
type UDP struct {
	Sent        int64    `json:"sent"`
	Received    int64    `json:"received"`
	Lost        *int64   `json:"lost"`
	LossPercent *float64 `json:"loss_percent"`
	JitterMS    *float64 `json:"jitter_ms"`
	PacketSize  *int     `json:"packet_size"`
}

// Proc is one process's resource cost.
//
// CPUSeconds comes from wait4 rusage (exact) or from /proc deltas (sampled);
// Source says which. RSSPeak is VmHWM. GoHeap is null unless a profile endpoint
// was available for that process.
type Proc struct {
	PID              int      `json:"pid"`
	CPUSeconds       *float64 `json:"cpu_seconds"`
	UserSeconds      *float64 `json:"user_seconds"`
	SysSeconds       *float64 `json:"sys_seconds"`
	CPUSecondsPerGiB *float64 `json:"cpu_seconds_per_gib"`
	RSSStartBytes    *int64   `json:"rss_start_bytes"`
	RSSEndBytes      *int64   `json:"rss_end_bytes"`
	RSSPeakBytes     *int64   `json:"rss_peak_bytes"`
	ThreadsPeak      *int     `json:"threads_peak"`
	FDsPeak          *int     `json:"fds_peak"`
	Samples          int      `json:"samples"`
	CPUSource        string   `json:"cpu_source,omitempty"`
	GoHeapPeakBytes  *int64   `json:"go_heap_peak_bytes"`
	ExitCode         *int     `json:"exit_code"`
}

// Integrity proves the bytes that moved were the bytes that were sent.
type Integrity struct {
	VerifiedBytes int64  `json:"verified_bytes"`
	OK            bool   `json:"ok"`
	Detail        string `json:"detail,omitempty"`
}

// Summary is one metric reduced across repetitions.
//
// Median and MAD are always present when n >= 1; P95/P99 only when the sample
// count makes them meaningful (n >= 20 for P95, n >= 100 for P99).
type Summary struct {
	Metric  string   `json:"metric"`
	Unit    string   `json:"unit"`
	Streams int      `json:"streams,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
	N       int      `json:"n"`
	Median  *float64 `json:"median"`
	MAD     *float64 `json:"mad"`
	Min     *float64 `json:"min"`
	Max     *float64 `json:"max"`
	P95     *float64 `json:"p95,omitempty"`
	P99     *float64 `json:"p99,omitempty"`
}

// Artifacts are paths relative to the run directory.
type Artifacts struct {
	RunJSON      string   `json:"run_json,omitempty"`
	SummaryMD    string   `json:"summary_md,omitempty"`
	ClientConfig string   `json:"client_config,omitempty"`
	ServerConfig string   `json:"server_config,omitempty"`
	ClientLog    string   `json:"client_log,omitempty"`
	ServerLog    string   `json:"server_log,omitempty"`
	Profiles     []string `json:"profiles,omitempty"`
}

// Failure is a step that did not produce a number, with the reason kept
// separate from the number it replaced.
type Failure struct {
	Stage    string `json:"stage"`
	Message  string `json:"message"`
	ExitCode *int   `json:"exit_code,omitempty"`
	LogPath  string `json:"log_path,omitempty"`
	Class    string `json:"class,omitempty"`
}

// EnvVar is one environment variable that could change the result, recorded so
// a rerun is reproducible.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
