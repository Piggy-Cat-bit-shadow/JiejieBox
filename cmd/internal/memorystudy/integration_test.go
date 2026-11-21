package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The integration tests run the child MEASUREMENT CODE IN A REAL SECOND PROCESS.
//
// That is the point of the whole instrument: GOMAXPROCS, the sampling rate and the adaptive stack
// size are process-global, and a test that measured two cases in one process would be measuring the
// residue of the first in the second. So the child is spawned as a separate operating system
// process - here by re-executing this test binary with an environment marker that TestMain turns
// into `runChild` - and the assertions are made on the JSONL it left behind.
//
// Using the test binary rather than a pre-built memorystudy.exe keeps the test honest about what it
// is testing: the same `runChild` code the command runs, built with whatever tags `go test` was
// given, whose build settings the ledger then reports.

const (
	childSpecEnv  = "MEMORYSTUDY_CHILD_SPEC"
	childOutEnv   = "MEMORYSTUDY_CHILD_OUT"
	childRunIDEnv = "MEMORYSTUDY_CHILD_RUN_ID"
)

// TestMain doubles as the child entry point. A test binary whose environment carries a spec runs
// one measurement and exits; otherwise it runs the tests.
func TestMain(m *testing.M) {
	if encoded := os.Getenv(childSpecEnv); encoded != "" {
		os.Exit(runEmbeddedChild(encoded))
	}
	os.Exit(m.Run())
}

func runEmbeddedChild(encoded string) int {
	var spec runSpec
	if err := json.Unmarshal([]byte(encoded), &spec); err != nil {
		fmt.Fprintln(os.Stderr, "memorystudy child: bad spec:", err)
		return 2
	}
	outputPath := os.Getenv(childOutEnv)
	if outputPath == "" {
		fmt.Fprintln(os.Stderr, "memorystudy child: no output path")
		return 2
	}
	file, err := os.Create(outputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "memorystudy child: create output:", err)
		return 2
	}
	runErr := runChild(spec, os.Getenv(childRunIDEnv), file)
	if closeErr := file.Close(); closeErr != nil && runErr == nil {
		runErr = closeErr
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "memorystudy child:", runErr)
		return 1
	}
	return 0
}

// spawnChild runs one case in a separate process and returns its decoded ledger.
func spawnChild(t *testing.T, spec runSpec) ([]recordMap, string, time.Duration) {
	t.Helper()
	directory := t.TempDir()
	outputPath := filepath.Join(directory, "child.jsonl")
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	runID := "test-" + specTag(spec)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(self, "-test.run=^$", "-test.timeout=600s")
	command.Env = append(os.Environ(),
		childSpecEnv+"="+string(encoded),
		childOutEnv+"="+outputPath,
		childRunIDEnv+"="+runID,
	)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	started := time.Now()
	runErr := command.Run()
	elapsed := time.Since(started)
	if runErr != nil {
		t.Fatalf("child %s failed: %v\nstderr:\n%s", runID, runErr, stderr.String())
	}
	lines, err := readLines(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	records, err := decodeLines(lines)
	if err != nil {
		t.Fatalf("%v\nstderr:\n%s", err, stderr.String())
	}
	if len(records) == 0 {
		t.Fatalf("child %s wrote no records; stderr:\n%s", runID, stderr.String())
	}
	return records, stderr.String(), elapsed
}

func stageOf(t *testing.T, records []recordMap, stage string) recordMap {
	t.Helper()
	for _, record := range records {
		if record.stage() == stage && record.recordKind() == recordStage {
			return record
		}
	}
	t.Fatalf("the ledger has no %q stage; it has %v", stage, runStages(records, records[0].runID()))
	return nil
}

func numberAt(t *testing.T, record recordMap, path string) float64 {
	t.Helper()
	value, ok := record.resolve(path)
	if !ok || value == nil {
		t.Fatalf("stage %s has no %s", record.stage(), path)
	}
	number, ok := numeric(value)
	if !ok {
		t.Fatalf("stage %s has a non-numeric %s (%T)", record.stage(), path, value)
	}
	return number
}

// TestChildrenAreSeparateProcesses is the isolation experiment.
//
// Two cases, two processes, two PIDs and two independent starting heaps. If the planner ever ran
// both cases in one process, the second case's earliest-observable heap would carry the first
// case's warm pools and the difference would be obvious here.
func TestChildrenAreSeparateProcesses(t *testing.T) {
	forced := runSpec{Case: "empty", Nodes: 0, Groups: 0, NodeProto: "direct",
		Activity: []string{"none"}, Idle: []string{"0s"}, PostIdle: "0s", GCMode: "forced",
		HeldFlows: 4, TrafficBytes: 1024}
	natural := forced
	natural.GCMode = "natural"

	first, _, _ := spawnChild(t, forced)
	twin, _, _ := spawnChild(t, forced)
	second, _, _ := spawnChild(t, natural)

	firstPID := numberAt(t, first[0], "pid")
	secondPID := numberAt(t, second[0], "pid")
	if firstPID == secondPID {
		t.Fatalf("both cases ran under pid %v: the ledger is not measuring independent startups", firstPID)
	}

	wanted := []string{
		stageEarliestObservable, stageSetup, stageFixtureServers, stageConfigBuild,
		stageConfigParse, stageConstruct, stageServiceStart, stageReady, idleStage("0s"),
		stageClose, stageRunEnd,
	}
	for _, records := range [][]recordMap{first, second} {
		stages := runStages(records, records[0].runID())
		position := 0
		for _, stage := range stages {
			if position < len(wanted) && stage == wanted[position] {
				position++
			}
		}
		if position != len(wanted) {
			t.Fatalf("the ledger did not contain the spine %v in order; got %v", wanted, stages)
		}
	}

	// The instrument's own proof that it is measuring a cold process: two independent startups of
	// the SAME case must land within a few percent of each other at the earliest observable point.
	// A second case in the same process would be far outside that.
	//
	// The comparison is against heap/objects rather than the live-heap metric because the latter
	// only exists once a collection has run; both of these runs are forced, so the two are equal
	// here - and using the always-available one keeps the assertion valid for either mode.
	entryFirst := numberAt(t, stageOf(t, first, stageEarliestObservable), "readings.go.heap_objects_bytes")
	entryTwin := numberAt(t, stageOf(t, twin, stageEarliestObservable), "readings.go.heap_objects_bytes")
	if difference := entryFirst - entryTwin; difference > entryFirst*0.10 || difference < -entryFirst*0.10 {
		t.Fatalf("two independent startups of the same case differ by %.0f bytes at the earliest "+
			"observable point (%.0f vs %.0f): they are not independent cold processes",
			difference, entryFirst, entryTwin)
	}

	// Every line has to be self-describing, or a grep of the file produces unreviewable quotes.
	identity := first[0]["identity"].(map[string]any)
	for _, key := range []string{"source", "build", "policy", "overlay", "device"} {
		if _, ok := identity[key]; !ok {
			t.Fatalf("the ledger line has no identity.%s", key)
		}
	}
	if schema, _ := first[0]["schema_version"].(string); schema != schemaVersion {
		t.Fatalf("schema_version = %q", schema)
	}
	source := identity["source"].(map[string]any)
	if sha, _ := source["sha"].(string); sha == "" {
		t.Fatal("the ledger does not carry a source revision")
	}
	if _, ok := source["detected_by"]; !ok {
		t.Fatal("the ledger does not say how the source revision was determined")
	}
	build := identity["build"].(map[string]any)
	if goos, _ := build["goos"].(string); goos == "" {
		t.Fatal("the ledger does not carry GOOS")
	}
	if _, ok := build["tags"]; !ok {
		t.Fatal("the ledger does not carry the build tags")
	}
	overlay := identity["overlay"].(map[string]any)
	if mode, _ := overlay["mode"].(string); mode == "" {
		t.Fatal("the ledger does not record the overlay mode")
	}
	device := identity["device"].(map[string]any)
	if supported, _ := device["supported"].(bool); supported {
		t.Fatal("a host process claimed to have a device footprint")
	}
	if value, ok := device["phys_footprint_bytes"]; !ok || value != nil {
		t.Fatalf("a host run must report a null phys_footprint, got %v (present %v)", value, ok)
	}
	// The policy block must carry the ACTUAL sampling rate, not a hoped-for one.
	policy := identity["policy"].(map[string]any)
	if _, ok := policy["memprofile_rate_bytes"]; !ok {
		t.Fatal("the ledger does not record the sampling rate in force")
	}
	if observable, _ := policy["adaptivestackstart_observable"].(bool); observable {
		t.Fatal("the ledger claims to observe adaptivestackstart; it has no exported accessor")
	}

	// A run that did not end is not evidence.
	for _, records := range [][]recordMap{first, second, twin} {
		end := stageOf(t, records, stageRunEnd)
		if end.status() != statusOK {
			t.Fatalf("the run ended with status %s: %s", end.status(), end.resolveString("error"))
		}
	}
}

// resolveString is a tiny accessor for error text.
func (m recordMap) resolveString(path string) string {
	value, ok := m.resolve(path)
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return text
}

// TestForcedReadingsActuallyCollect proves the gc discipline is what it claims.
//
// A "forced" ledger that never ran a collection would produce the same numbers as a natural one,
// and every budget derived from it would describe a process that never returned its garbage.
func TestForcedReadingsActuallyCollect(t *testing.T) {
	spec := runSpec{Case: "empty", Nodes: 0, Groups: 0, NodeProto: "direct",
		Activity: []string{"none"}, Idle: []string{"0s"}, PostIdle: "0s", GCMode: "forced",
		HeldFlows: 4, TrafficBytes: 1024}
	records, _, _ := spawnChild(t, spec)
	entry := numberAt(t, stageOf(t, records, stageEarliestObservable), "readings.go.gc.cycles_forced")
	end := numberAt(t, stageOf(t, records, stageRunEnd), "readings.go.gc.cycles_forced")
	stageCount := float64(len(runStages(records, records[0].runID())))
	if end-entry < forcedGCRounds*(stageCount-2) {
		t.Fatalf("a forced run recorded only %.0f forced collections across %.0f stages "+
			"(want at least %d per stage): the readings were not taken after a collection",
			end-entry, stageCount, forcedGCRounds)
	}

	// A natural run must not force a collection WHILE IT IS MEASURING. It is allowed to force one
	// during Start - the product does, and the ledger says so - but the idle interval itself is the
	// process left alone, and a collection there would make "natural idle" mean "idle after a GC".
	natural := spec
	natural.GCMode = "natural"
	naturalRecords, _, _ := spawnChild(t, natural)
	ready := numberAt(t, stageOf(t, naturalRecords, stageReady), "readings.go.gc.cycles_forced")
	idle := numberAt(t, stageOf(t, naturalRecords, idleStage("0s")), "readings.go.gc.cycles_forced")
	if idle != ready {
		t.Fatalf("a natural run forced %.0f collection(s) between the ready reading and the idle "+
			"reading; a natural idle has to be the process left alone", idle-ready)
	}

	// And the natural run must report the live-heap metric as unknown until a collection has
	// happened, rather than as zero.
	entryRecord := stageOf(t, naturalRecords, stageEarliestObservable)
	cycles := numberAt(t, entryRecord, "readings.go.gc.cycles_total")
	live := entryRecord.resolveLiveHeap()
	if cycles == 0 && live != nil {
		t.Fatalf("a process with no completed GC reported a live heap of %d; the runtime only "+
			"publishes that metric at the end of a cycle, and a zero would read as an empty process", *live)
	}
	if cycles == 0 {
		if _, explained := entryRecord["readings"].(map[string]any)["unsupported"]; !explained {
			t.Fatal("a null live heap with no completed GC carries no explanation")
		}
	}
}

// resolveLiveHeap returns the live-heap reading, or nil when it is null.
func (m recordMap) resolveLiveHeap() *uint64 {
	value, ok := m.resolve("readings.go.live_heap_bytes")
	if !ok || value == nil {
		return nil
	}
	number, ok := numeric(value)
	if !ok {
		return nil
	}
	converted := uint64(number)
	return &converted
}

// TestTrafficFixtureThroughTheRealRelay is the business check.
//
// A memory improvement that breaks the relay is not an improvement, and the failure mode - a
// half-close turned into a full close - is invisible to every idle-connection budget.
func TestTrafficFixtureThroughTheRealRelay(t *testing.T) {
	if testing.Short() {
		t.Skip("this holds real flows through the real relay; -short skips it")
	}
	const flows = 3
	spec := runSpec{Case: "empty", Nodes: 0, Groups: 0, NodeProto: "direct",
		Activity: []string{"traffic"}, Idle: []string{"0s"}, PostIdle: "0s", GCMode: "forced",
		HeldFlows: flows, TrafficBytes: 16 << 10}
	records, stderr, elapsed := spawnChild(t, spec)

	held := stageOf(t, records, stageTrafficHeld)
	if held.status() != statusOK {
		t.Fatalf("the flows were not established: %s", held.resolveString("error"))
	}
	connections := numberAt(t, held, "readings.objects.connections")
	if connections < flows {
		t.Fatalf("the product's connection manager reports %.0f connections for %d held flows; "+
			"the reading was taken before the flows were established\n%s", connections, flows, stderr)
	}

	verified := stageOf(t, records, stageTrafficVerified)
	report := verified["traffic"].(map[string]any)
	for _, key := range []string{"verified", "request_intact", "response_intact",
		"half_close_propagated", "response_after_half_close"} {
		if value, _ := report[key].(bool); !value {
			t.Fatalf("traffic.%s is false: the relay lost the business path while being measured "+
				"(stage status %s)\n%s", key, verified.status(), stderr)
		}
	}
	if status := verified.status(); status != statusOK {
		t.Fatalf("the verification stage is %s: %s", status, verified.resolveString("error"))
	}
	requestBytes := numberAt(t, verified, "traffic.request_bytes")
	responseBytes := numberAt(t, verified, "traffic.response_bytes")
	if requestBytes != float64(flows*16<<10) {
		t.Fatalf("the relay carried %.0f request bytes, want %d", requestBytes, flows*16<<10)
	}
	if responseBytes <= requestBytes {
		t.Fatalf("the reply (%.0f bytes) is not larger than the request (%.0f bytes): the "+
			"verification payload is not what it claims", responseBytes, requestBytes)
	}

	// The teardown still has to return to the floor: a verified flow that leaves a goroutine behind
	// is a leak the business check cannot see on its own.
	end := stageOf(t, records, stageRunEnd)
	entryGoroutines := numberAt(t, stageOf(t, records, stageEarliestObservable), "readings.goroutines.current")
	endGoroutines := numberAt(t, end, "readings.goroutines.current")
	if endGoroutines > entryGoroutines+2 {
		t.Fatalf("%.0f goroutines remain after the flows were closed (entry %.0f): the relay did "+
			"not come back to rest", endGoroutines, entryGoroutines)
	}
	t.Logf("three flows: %.0f request and %.0f reply bytes verified, %d processes, %s",
		requestBytes, responseBytes, 1, elapsed.Round(time.Millisecond))
}

// TestGateMatrixDerivesAndPassesItsOwnBudget runs the real fast matrix, turns it into a budget and
// evaluates it against the same ledger.
//
// # Why a self-comparison is the right first assertion
//
// The checked-in budget is derived from runs on a specific machine. This test cannot know whether
// this machine's absolute numbers are the same - and it must not silently skip because they are not.
// What it CAN prove, on any machine, is that every rule in the gate matrix has an observation: no
// rule names a stage the runner does not produce, no metric path is a typo, and no rule is skipped
// because the case it selects never ran. A gate whose rules never match anything would report
// "0 pass, 0 fail" and, without this test, look green.
func TestGateMatrixDerivesAndPassesItsOwnBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("this runs the whole gate matrix; -short skips it")
	}
	if runtimeIsShort() {
		t.Skip("the gate matrix is not a -short test")
	}
	specs := gateSpecs(false)
	var ledger []recordMap
	for _, spec := range specs {
		records, stderr, _ := spawnChild(t, spec)
		for _, record := range records {
			if record.recordKind() == recordChildResult || record.stage() == "error" {
				continue
			}
			ledger = append(ledger, record)
		}
		if len(records) == 0 {
			t.Fatalf("%s produced no ledger\n%s", specTag(spec), stderr)
		}
	}
	budget, err := deriveBudget(ledger, "")
	if err != nil {
		t.Fatalf("the gate matrix could not be turned into a budget: %v", err)
	}
	if len(budget.Rules) == 0 {
		t.Fatal("the budget has no rules")
	}
	for _, result := range evaluateBudget(budget, ledger) {
		if result.Status == statusUnsupported {
			t.Fatalf("rule %s has no observation in this machine's own ledger: %s", result.ID, result.Reason)
		}
		if result.Status != statusOK {
			t.Fatalf("rule %s did not pass against the ledger it was derived from: observed %v, budget %s",
				result.ID, result.Observed, result.Budget)
		}
	}
	if baseline := budget.Baseline; baseline.GOOS == "" || baseline.SourceSHA == "" {
		t.Fatalf("the derived budget does not record its platform and source: %+v", baseline)
	}
}

// TestBudgetFileRoundTrip checks the checked-in artefact, when it exists.
func TestBudgetFileRoundTrip(t *testing.T) {
	path := filepath.Join("testdata", "budget.json")
	if _, err := os.Stat(path); err != nil {
		t.Skip("no checked-in budget yet: derive one with memorystudy -write-budget")
	}
	budget, err := readBudgetFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(budget.Rules) == 0 {
		t.Fatal("the checked-in budget has no rules")
	}
	for _, rule := range budget.Rules {
		if rule.ID == "" || rule.Stage == "" || rule.Metric == "" {
			t.Fatalf("a rule is incomplete: %+v", rule)
		}
		if rule.Min == nil && (rule.Max == nil || rule.Basis == "") {
			t.Fatalf("rule %s has a threshold with no recorded basis", rule.ID)
		}
		if rule.Max != nil && rule.Min == nil && len(rule.Observed) == 0 {
			t.Fatalf("rule %s has a derived threshold but no observed baselines", rule.ID)
		}
	}
	if len(budget.Baseline.Runs) == 0 || budget.Baseline.GOOS == "" {
		t.Fatal("the budget does not name the runs and platform it was derived from")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), budgetSchemaVersion) {
		t.Fatalf("the budget file does not declare %q", budgetSchemaVersion)
	}
}

// runtimeIsShort reads the -short flag without importing testing's internals twice.
func runtimeIsShort() bool {
	for _, argument := range os.Args {
		if argument == "-test.short" || strings.HasPrefix(argument, "-test.short=") {
			return true
		}
	}
	return false
}
