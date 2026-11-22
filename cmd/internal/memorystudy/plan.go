package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// runFlags is the command line. The parameter names match the round's task book on purpose; the
// syntax is this repository's (a single main package with flags, like every other cmd/internal
// tool) rather than a custom parser.
type runFlags struct {
	caseName     string
	nodes        int
	groups       int
	nodeProto    string
	configSource string
	activity     string
	idle         string
	postIdle     string
	gcMode       string
	heldFlows    int
	trafficBytes int

	// changedNodes is the K axis of the provider refresh. -1 is the default and means the
	// historical behaviour (every node's port moves); 0 and up is an explicit count.
	changedNodes int

	runs   int
	output string
	csv    string

	gate        bool
	budgetPath  string
	writeBudget string
	from        string
	wide        bool
	// referenceManifest archives a reference capture; selfCheck lets -gate verify the reference
	// ledger against the budget derived from it.
	referenceManifest string
	selfCheck         bool
	// provided records which flags the caller actually set. "runs=1" and "runs was left at its
	// default" are different statements when a ledger read back from disk has three repetitions.
	provided map[string]bool

	gomaxprocs int
	godebug    string
	inittrace  bool

	child       bool
	runID       string
	heapprofile string
	dumpFixture string
	timeout     time.Duration
	verbose     bool
	keepLogs    bool
}

// planSpecs turns the requested case into the child processes that must run.
//
// One child per GC discipline is not a convenience: a forced collection inside a natural-idle run
// destroys the very thing the natural run measures, and a natural idle cannot be reconstructed from
// a forced one. They are separate processes so neither can contaminate the other.
func planSpecs(flags *runFlags) ([]runSpec, error) {
	gcModes, err := parseGCModes(flags.gcMode)
	if err != nil {
		return nil, err
	}
	idle, err := parseDurationList(flags.idle)
	if err != nil {
		return nil, err
	}
	activity, err := parseActivityList(flags.activity)
	if err != nil {
		return nil, err
	}
	base := runSpec{
		Case:         flags.caseName,
		Nodes:        flags.nodes,
		Groups:       flags.groups,
		NodeProto:    flags.nodeProto,
		ConfigSource: flags.configSource,
		Activity:     activity,
		Idle:         idle,
		PostIdle:     flags.postIdle,
		HeldFlows:    flags.heldFlows,
		TrafficBytes: flags.trafficBytes,
	}
	if changed, changedErr := changedNodesFromFlags(flags); changedErr != nil {
		return nil, changedErr
	} else if changed != nil {
		base.ChangedNodes = changed
	}
	specs := make([]runSpec, 0, len(gcModes))
	for _, mode := range gcModes {
		spec := base
		spec.GCMode = mode
		normalized, normalizeErr := normalizeSpec(spec)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		specs = append(specs, normalized)
	}
	return specs, nil
}

func parseGCModes(raw string) ([]string, error) {
	var modes []string
	for _, part := range strings.Split(raw, ",") {
		switch strings.TrimSpace(part) {
		case "natural", "forced":
			modes = append(modes, strings.TrimSpace(part))
		case "both":
			modes = append(modes, "natural", "forced")
		case "":
		default:
			return nil, fmt.Errorf("unknown -gc %q (want natural, forced or both)", part)
		}
	}
	if len(modes) == 0 {
		modes = []string{"forced"}
	}
	return modes, nil
}

// parseDurationList normalises the idle list. Durations are kept as strings because that is exactly
// what the stage name is derived from, and a stage name that is re-formatted from a time.Duration
// would turn "1m30s" into "1m30s" only sometimes.
func parseDurationList(raw string) ([]string, error) {
	var durations []string
	var previous time.Duration
	for _, part := range strings.Split(raw, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		parsed, err := time.ParseDuration(trimmed)
		if err != nil {
			return nil, fmt.Errorf("-idle entry %q: %w", trimmed, err)
		}
		if parsed < 0 {
			return nil, fmt.Errorf("-idle entry %q is negative", trimmed)
		}
		if parsed < previous {
			return nil, fmt.Errorf("-idle entries must be non-decreasing: %q comes after %q", trimmed, previous)
		}
		previous = parsed
		durations = append(durations, trimmed)
	}
	if len(durations) == 0 {
		durations = []string{"0s"}
	}
	return durations, nil
}

func parseActivityList(raw string) ([]string, error) {
	var activities []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		trimmed := strings.TrimSpace(part)
		switch trimmed {
		case "none", "probe", "traffic":
			if !seen[trimmed] {
				seen[trimmed] = true
				activities = append(activities, trimmed)
			}
		case "":
		default:
			return nil, fmt.Errorf("unknown -activity %q (want none, probe, traffic, or a comma list)", trimmed)
		}
	}
	if len(activities) == 0 {
		activities = []string{"none"}
	}
	return activities, nil
}

// changedNodesFromFlags resolves the K axis the caller actually asked for.
//
// The zero value of runFlags is NOT a request for K=0: a hand-built flags value (a test, or an
// embedder calling the planner) leaves every int at 0, and treating that as "a byte-identical
// changed document" would silently attach a K axis to a static case and be refused. Only an
// EXPLICIT -changed-nodes 0 means K=0, which is why the provided set participates; a positive value
// is unambiguous without it, and -1 is the historical "every node changed".
func changedNodesFromFlags(flags *runFlags) (*int, error) {
	if flags.changedNodes < -1 {
		return nil, fmt.Errorf("-changed-nodes must be -1 (every node) or a non-negative count, got %d", flags.changedNodes)
	}
	if flags.changedNodes > 0 {
		changed := flags.changedNodes
		return &changed, nil
	}
	if flags.changedNodes == 0 && flags.provided != nil && flags.provided["changed-nodes"] {
		changed := 0
		return &changed, nil
	}
	return nil, nil
}

type session struct {
	flags    *runFlags
	stamp    string
	workDir  string
	lines    []string
	records  []recordMap
	planned  int
	failures int
}

func newSessionStamp() string {
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return time.Now().UTC().Format("20060102T150405Z")
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix)
}

// runMeasure executes every planned child, merges their JSONL, and writes the CSV view.
func runMeasure(flags *runFlags) (*session, error) {
	specs, err := planSpecs(flags)
	if err != nil {
		return nil, err
	}
	return runSpecs(flags, specs)
}

// runSpecs executes an explicit list of child runs. The gate uses it with its own fixed matrix so
// that a cached regression compares the same cases every time rather than whatever the caller's
// flags happened to say.
func runSpecs(flags *runFlags, specs []runSpec) (*session, error) {
	current := &session{flags: flags, stamp: newSessionStamp()}
	var err error
	if flags.output != "" && flags.output != "-" {
		current.workDir = flags.output + ".d"
	} else {
		current.workDir, err = os.MkdirTemp("", "memorystudy-")
		if err != nil {
			return nil, err
		}
	}
	if err = os.MkdirAll(current.workDir, 0o755); err != nil {
		return nil, err
	}

	// Runs are serial on purpose. Two children measuring at once would share the CPU, the page
	// cache and the memory bandwidth, and every one of those is part of what is being measured.
	for index := 0; index < flags.runs; index++ {
		for _, spec := range specs {
			runID := fmt.Sprintf("%s-%02d-%s-%s", current.stamp, index, specTag(spec), randomSuffix())
			current.planned++
			if err = current.runChild(spec, runID); err != nil {
				// A failed child does not stop the session: the remaining cases are still worth
				// measuring, and the failure is already on the record. The exit code at the end
				// is what tells a caller that the session is not complete evidence.
				current.failures++
				fmt.Fprintf(os.Stderr, "memorystudy: run %s failed: %v\n", runID, err)
			}
		}
	}
	if err = current.flush(); err != nil {
		return current, err
	}
	return current, nil
}

func specTag(spec runSpec) string {
	activity := strings.Join(spec.Activity, "+")
	return fmt.Sprintf("%s-n%d-g%d-%s-%s-%s-%s", spec.Case, spec.Nodes, spec.Groups, spec.NodeProto,
		spec.ConfigSource, activity, spec.GCMode)
}

func randomSuffix() string {
	suffix := make([]byte, 2)
	if _, err := rand.Read(suffix); err != nil {
		return "00"
	}
	return hex.EncodeToString(suffix)
}

// runChild starts one child process, captures its JSONL and its stderr, and records the outcome.
//
// The child is a separate operating system process - not a goroutine, not a second call into the
// same measurement code - because the things under test are process-global: GOMAXPROCS, the
// sampling rate, the adaptive stack size, the arena and the P caches. Running two cases in one
// process would measure the first case's residue in the second.
func (s *session) runChild(spec runSpec, runID string) error {
	outputPath := filepath.Join(s.workDir, runID+".jsonl")
	errorPath := filepath.Join(s.workDir, runID+".stderr.log")
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer func() { _ = outputFile.Close() }()
	errorFile, err := os.Create(errorPath)
	if err != nil {
		return err
	}
	defer func() { _ = errorFile.Close() }()

	arguments := childArguments(spec, runID)
	timeout := s.flags.timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, executablePath(), arguments...)
	command.Stdout = outputFile
	command.Stderr = errorFile
	command.Env = childEnvironment(s.flags)
	started := time.Now()
	runErr := command.Run()
	wall := time.Since(started)

	exitCode := 0
	if runErr != nil {
		exitCode = -1
		var exitError *exec.ExitError
		if ok := asExitError(runErr, &exitError); ok {
			exitCode = exitError.ExitCode()
		}
	}
	_ = outputFile.Sync()
	_ = errorFile.Sync()

	lines, readErr := readLines(outputPath)
	if readErr != nil {
		return readErr
	}
	runEndSeen := false
	for _, line := range lines {
		if strings.Contains(line, `"stage":"run_end"`) && strings.Contains(line, `"status":"ok"`) {
			runEndSeen = true
			break
		}
	}
	s.lines = append(s.lines, lines...)

	result := stageRecord{
		SchemaVersion: schemaVersion,
		Record:        recordChildResult,
		Status:        statusOK,
		RunID:         runID,
		PID:           os.Getpid(),
		Seq:           -1,
		Stage:         "child_result",
		ObservedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		Spec:          spec,
		Identity:      sessionIdentity(),
		Readings:      readings{},
		ExitCode:      &exitCode,
		LogPath:       errorPath,
		WallMillis:    wall.Milliseconds(),
		RunEndSeen:    runEndSeen,
	}
	if runErr == nil && !runEndSeen {
		result.Status = statusFailed
		result.FailureCause = "the child exited 0 without writing a run_end record; the run is not " +
			"complete evidence even though the process thought it succeeded"
	} else if runErr != nil {
		result.Status = statusFailed
		result.FailureCause = runErr.Error()
	}
	if ctx.Err() == context.DeadlineExceeded {
		result.Status = statusFailed
		result.FailureCause = fmt.Sprintf("the child exceeded the %s timeout and was killed", timeout)
	}
	encoded, marshalErr := json.Marshal(result)
	if marshalErr == nil {
		s.lines = append(s.lines, string(encoded))
	}
	if result.Status != statusOK {
		return fmt.Errorf("%s", result.FailureCause)
	}
	if s.flags.verbose {
		fmt.Fprintf(os.Stderr, "memorystudy: %s ok in %s (%d lines)\n", runID, wall.Round(time.Millisecond), len(lines))
	}
	return nil
}

// asExitError is errors.As without the import, kept tiny so the failure path has no dependency that
// could itself fail.
func asExitError(err error, target **exec.ExitError) bool {
	exitError, ok := err.(*exec.ExitError)
	if !ok {
		return false
	}
	*target = exitError
	return true
}

// childArguments reproduces the whole spec on the child's command line: a child that had to read
// defaults from somewhere else could silently measure a different case than the one recorded.
func childArguments(spec runSpec, runID string) []string {
	return []string{
		"-child",
		"-run-id", runID,
		"-case", spec.Case,
		"-nodes", strconv.Itoa(spec.Nodes),
		"-groups", strconv.Itoa(spec.Groups),
		"-node-proto", spec.NodeProto,
		"-config-source", spec.ConfigSource,
		"-activity", strings.Join(spec.Activity, ","),
		"-idle", strings.Join(spec.Idle, ","),
		"-post-idle", spec.PostIdle,
		"-gc", spec.GCMode,
		"-held-flows", strconv.Itoa(spec.HeldFlows),
		"-traffic-bytes", strconv.Itoa(spec.TrafficBytes),
		"-changed-nodes", changedNodesArgument(spec),
	}
}

// changedNodesArgument renders the K axis for the child. nil stays the historical -1 ("every node
// moved") rather than being dropped, so the child's own spec echo always says which case it ran.
func changedNodesArgument(spec runSpec) string {
	if spec.ChangedNodes == nil {
		return "-1"
	}
	return strconv.Itoa(*spec.ChangedNodes)
}

// childEnvironment applies the startup policy the run asks for.
//
// GOMAXPROCS and GODEBUG go into the child's ENVIRONMENT rather than being set from Go code after
// main starts, because that is the only way they are in force from process start. A run that called
// runtime.GOMAXPROCS(1) after the runtime had already sized its per-P caches would not be measuring
// a single-P process.
func childEnvironment(flags *runFlags) []string {
	environment := os.Environ()
	// The parent's own measurement knobs must not leak into a child that did not ask for them.
	environment = filterEnv(environment, "GOMAXPROCS")
	if flags.gomaxprocs > 0 {
		environment = append(environment, "GOMAXPROCS="+strconv.Itoa(flags.gomaxprocs))
	}
	environment = filterEnv(environment, "GODEBUG")
	godebug := flags.godebug
	if flags.inittrace {
		if godebug != "" {
			godebug += ","
		}
		godebug += "inittrace=1"
	}
	if godebug != "" {
		environment = append(environment, "GODEBUG="+godebug)
	}
	return environment
}

func filterEnv(environment []string, name string) []string {
	prefix := name + "="
	filtered := environment[:0]
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// flush writes the merged JSONL and, if asked, the CSV view.
func (s *session) flush() error {
	if s.flags.output != "" && s.flags.output != "-" {
		file, err := os.Create(s.flags.output)
		if err != nil {
			return err
		}
		writer := bufio.NewWriterSize(file, 1<<20)
		for _, line := range s.lines {
			if _, err = writer.WriteString(line); err != nil {
				_ = file.Close()
				return err
			}
			if err = writer.WriteByte('\n'); err != nil {
				_ = file.Close()
				return err
			}
		}
		if err = writer.Flush(); err != nil {
			_ = file.Close()
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
	} else {
		writer := bufio.NewWriterSize(os.Stdout, 1<<20)
		for _, line := range s.lines {
			_, _ = writer.WriteString(line)
			_ = writer.WriteByte('\n')
		}
		_ = writer.Flush()
	}

	records, err := decodeLines(s.lines)
	if err != nil {
		return err
	}
	s.records = records
	if s.flags.csv != "" {
		return writeCSV(s.flags.csv, records)
	}
	return nil
}

// csvColumns is the flat view of the ledger. It is deliberately a fixed list: a spreadsheet that
// gains a column when the runtime gains a metric is a spreadsheet nobody can compare across Go
// versions.
var csvColumns = []struct {
	header string
	path   string
}{
	{"live_heap_bytes", "readings.go.live_heap_bytes"},
	{"live_objects", "readings.go.live_objects"},
	{"heap_objects_bytes", "readings.go.heap_objects_bytes"},
	{"heap_stacks_bytes", "readings.go.heap_stacks_bytes"},
	{"heap_released_bytes", "readings.go.heap_released_bytes"},
	{"total_bytes", "readings.go.total_bytes"},
	{"mspan_inuse_bytes", "readings.go.metadata.mspan_inuse_bytes"},
	{"mcache_inuse_bytes", "readings.go.metadata.mcache_inuse_bytes"},
	{"mcache_free_bytes", "readings.go.metadata.mcache_free_bytes"},
	{"profiling_buckets_bytes", "readings.go.profiling_buckets_bytes"},
	{"scan_total_bytes", "readings.go.scan.total_bytes"},
	{"cumulative_alloc_bytes", "readings.go.cumulative_alloc_bytes"},
	{"memprofile_rate_bytes", "readings.go.memprofile_rate_bytes"},
	{"runtime_threads", "readings.go.runtime_threads"},
	{"goroutines", "readings.goroutines.current"},
	{"process_threads", "readings.process.threads"},
	{"process_fds", "readings.process.file_descriptors"},
	{"working_set_bytes", "readings.process.working_set_bytes"},
	{"private_commit_bytes", "readings.process.private_commit_bytes"},
	{"connections", "readings.objects.connections"},
	{"outbounds", "readings.objects.outbounds"},
	{"delta_entry_live_heap", "deltas.from_entry.live_heap_bytes"},
	{"delta_prev_live_heap", "deltas.from_previous.live_heap_bytes"},
	{"delta_entry_per_node", "deltas.from_entry.live_heap_per_node_bytes"},
	{"delta_prev_per_node", "deltas.from_previous.live_heap_per_node_bytes"},
	{"device_phys_footprint", "readings.device.phys_footprint_bytes"},
}

func writeCSV(path string, records []recordMap) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	writer := csv.NewWriter(file)
	header := []string{
		"run_id", "record", "status", "case", "nodes", "groups", "node_proto", "activity", "gc_mode",
		"stage", "offset_ns", "stage_duration_ns", "source_sha", "go_version", "goos", "goarch",
	}
	for _, column := range csvColumns {
		header = append(header, column.header)
	}
	if err = writer.Write(header); err != nil {
		return err
	}
	for _, record := range records {
		row := []string{
			record.runID(),
			record.recordKind(),
			record.status(),
			record.specField("case"),
			record.specField("nodes"),
			record.specField("groups"),
			record.specField("node_proto"),
			record.specField("activity"),
			record.specField("gc_mode"),
			record.stage(),
			valueString(record, "offset_ns"),
			valueString(record, "stage_duration_ns"),
			valueString(record, "identity.source.sha"),
			valueString(record, "identity.build.go_version"),
			valueString(record, "identity.build.goos"),
			valueString(record, "identity.build.goarch"),
		}
		for _, column := range csvColumns {
			row = append(row, valueString(record, column.path))
		}
		if err = writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return err
	}
	return file.Sync()
}

// valueString renders a resolved value for CSV. A missing measurement is an EMPTY cell, not "0":
// a spreadsheet sums empty cells as nothing and a person reads them as nothing, which is the
// behaviour a missing reading needs.
func valueString(record recordMap, path string) string {
	value, ok := record.resolve(path)
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		if number, ok := numeric(value); ok {
			return floatString(number)
		}
		return fmt.Sprint(typed)
	}
}

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err = scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}
