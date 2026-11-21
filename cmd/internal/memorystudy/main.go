package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// main is the entry point for both halves of the instrument: the default mode plans and runs child
// processes, and -child is the single-case process the planner starts.
//
// The two modes live in one binary on purpose. A separate child binary would have to be built and
// kept in step with the planner; a self-exec keeps the measured and the measuring code the same
// revision by construction.
func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "memorystudy:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := &runFlags{}
	command := flag.NewFlagSet("memorystudy", flag.ContinueOnError)
	command.SetOutput(os.Stderr)
	command.Usage = func() {
		fmt.Fprint(os.Stderr, usageText)
		command.PrintDefaults()
	}

	command.StringVar(&flags.caseName, "case", "empty", "fixture case: empty | ordinary | heavy")
	command.IntVar(&flags.nodes, "nodes", -1, "ordinary nodes in the configuration (ordinary defaults to 100, heavy to 10)")
	command.IntVar(&flags.groups, "groups", -1, "selection groups over the nodes (ordinary defaults to 2, empty to 0)")
	command.StringVar(&flags.nodeProto, "node-proto", "", "node kind: trojan | direct | heavy (the case decides by default)")
	command.StringVar(&flags.activity, "activity", "none", "activity history: none | probe | traffic | a comma list")
	command.StringVar(&flags.idle, "idle", "0s", "cumulative idle checkpoints, e.g. 30s,300s")
	command.StringVar(&flags.postIdle, "post-idle", "0s", "idle after the activity history, e.g. 30s")
	command.StringVar(&flags.gcMode, "gc", "forced", "reading discipline: natural | forced | both (each is its own process)")
	command.IntVar(&flags.heldFlows, "held-flows", 4, "flows held open through the relay for -activity traffic")
	command.IntVar(&flags.trafficBytes, "traffic-bytes", 64<<10, "payload bytes pushed through each flow")

	command.IntVar(&flags.runs, "runs", 1, "independent repetitions of the planned case list")
	command.StringVar(&flags.output, "output", "-", "merged JSONL path (- for stdout); per-run raw files go to <path>.d/")
	command.StringVar(&flags.csv, "csv", "", "optional CSV view of the merged ledger")

	command.BoolVar(&flags.gate, "gate", false, "run the fast budget matrix and evaluate it against -budget")
	command.StringVar(&flags.budgetPath, "budget", "", "budget JSON for -gate (default testdata/budget.json next to the source)")
	command.StringVar(&flags.writeBudget, "write-budget", "", "derive a budget JSON from a baseline and write it here")
	command.StringVar(&flags.from, "from", "", "read an existing merged JSONL instead of running anything (with -write-budget)")
	command.BoolVar(&flags.wide, "wide", false, "add the 1000- and 10000-node scale cases to the gate matrix")

	command.IntVar(&flags.gomaxprocs, "gomaxprocs", 0, "start every child with GOMAXPROCS=n in its environment (0 leaves it alone)")
	command.StringVar(&flags.godebug, "godebug", "", "start every child with this GODEBUG string in its environment")
	command.BoolVar(&flags.inittrace, "inittrace", false, "add inittrace=1 to the children's GODEBUG and keep their stderr")

	command.BoolVar(&flags.child, "child", false, "internal: run one case in this process and write its JSONL to stdout")
	command.StringVar(&flags.runID, "run-id", "", "internal: the run id the parent assigned")
	command.DurationVar(&flags.timeout, "timeout", 15*time.Minute, "per-child timeout")
	command.BoolVar(&flags.verbose, "verbose", false, "log each child's outcome to stderr")

	if err := command.Parse(args); err != nil {
		return err
	}
	provided := make(map[string]bool)
	command.Visit(func(visited *flag.Flag) { provided[visited.Name] = true })
	applyCaseDefaults(flags, provided)

	if flags.child {
		return runChildMode(flags, stdout)
	}
	if flags.writeBudget != "" {
		return runWriteBudget(flags)
	}
	if flags.gate {
		return runGate(flags, stdout)
	}
	current, err := runMeasure(flags)
	if err != nil {
		return err
	}
	if current.failures > 0 {
		return fmt.Errorf("%d of %d child run(s) failed; see the child_result records", current.failures, current.planned)
	}
	return nil
}

// applyCaseDefaults fills the node and group counts a case implies, but only for flags the caller
// did not set: an explicit -nodes 0 is a meaningful request and must not be overwritten by a
// default that happens to be 100.
func applyCaseDefaults(flags *runFlags, provided map[string]bool) {
	if !provided["nodes"] {
		switch flags.caseName {
		case "ordinary":
			flags.nodes = 100
		case "heavy":
			flags.nodes = 10
		default:
			flags.nodes = 0
		}
	}
	if !provided["groups"] {
		if flags.caseName == "empty" {
			flags.groups = 0
		} else {
			flags.groups = 2
		}
	}
	if flags.nodeProto == "" {
		flags.nodeProto = "trojan"
	}
}

// runChildMode is the measured process. It writes nothing but JSONL to stdout so that the parent can
// keep the file as the raw record; diagnostics go to stderr.
func runChildMode(flags *runFlags, stdout io.Writer) error {
	if flags.runID == "" {
		return fmt.Errorf("-child needs -run-id")
	}
	spec := runSpec{
		Case:         flags.caseName,
		Nodes:        flags.nodes,
		Groups:       flags.groups,
		NodeProto:    flags.nodeProto,
		Activity:     nil,
		Idle:         nil,
		PostIdle:     flags.postIdle,
		GCMode:       flags.gcMode,
		HeldFlows:    flags.heldFlows,
		TrafficBytes: flags.trafficBytes,
	}
	activity, err := parseActivityList(flags.activity)
	if err != nil {
		return err
	}
	spec.Activity = activity
	idle, err := parseDurationList(flags.idle)
	if err != nil {
		return err
	}
	spec.Idle = idle
	gcModes, err := parseGCModes(flags.gcMode)
	if err != nil {
		return err
	}
	if len(gcModes) != 1 {
		return fmt.Errorf("-child needs exactly one -gc mode, got %q", flags.gcMode)
	}
	spec.GCMode = gcModes[0]
	spec, err = normalizeSpec(spec)
	if err != nil {
		return err
	}
	return runChild(spec, flags.runID, stdout)
}

// executablePath is the binary the planner re-executes.
func executablePath() string {
	if executable, err := os.Executable(); err == nil {
		return executable
	}
	return os.Args[0]
}

// sessionIdentity is the parent's identity, used on the child_result lines it writes itself.
//
// It is captured once per process: it shells out to git, and one probe per child would triple the
// wall time of a twelve-child session to learn the same answer twelve times.
var (
	sessionIdentityOnce   sync.Once
	cachedSessionIdentity identity
)

func sessionIdentity() identity {
	sessionIdentityOnce.Do(func() {
		cachedSessionIdentity = captureIdentity(newSampler())
	})
	return cachedSessionIdentity
}

func runWriteBudget(flags *runFlags) error {
	var records []recordMap
	if flags.from != "" {
		lines, err := readLines(flags.from)
		if err != nil {
			return err
		}
		records, err = decodeLines(lines)
		if err != nil {
			return err
		}
	} else {
		flags.output = budgetRunOutput(flags)
		current, err := runSpecs(flags, gateSpecs(flags.wide))
		if err != nil {
			return err
		}
		records = current.records
	}
	budget, err := deriveBudget(records, flags.budgetPath)
	if err != nil {
		return err
	}
	if err = writeBudgetFile(flags.writeBudget, budget); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "memorystudy: wrote %d budget rules to %s from %d records\n",
		len(budget.Rules), flags.writeBudget, len(records))
	return nil
}

func budgetRunOutput(flags *runFlags) string {
	if flags.output != "" && flags.output != "-" {
		return flags.output
	}
	return "memorystudy-baseline-" + newSessionStamp() + ".jsonl"
}

func runGate(flags *runFlags, stdout io.Writer) error {
	if flags.output == "" || flags.output == "-" {
		return fmt.Errorf("-gate needs -output: a gate that does not keep its raw ledger cannot be " +
			"re-examined when it fails")
	}
	current, err := runSpecs(flags, gateSpecs(flags.wide))
	if err != nil {
		return err
	}
	// A failed child does not stop the session (the other cases are still worth measuring), so the
	// failure has to stop the GATE. Without this the gate certified a session in which every child
	// had failed: runSpecs only counted the failures, and nothing read the count.
	if current.failures > 0 {
		return fmt.Errorf("%d of %d child run(s) failed; a session with a failed child is not evidence "+
			"for a gate (the child_result records name the run and the cause)", current.failures, current.planned)
	}
	if err = requireCompleteEvidence(current, flags); err != nil {
		return err
	}
	budgetPath := flags.budgetPath
	if budgetPath == "" {
		budgetPath = defaultBudgetPath()
	}
	budget, err := readBudgetFile(budgetPath)
	if err != nil {
		return err
	}
	results := evaluateBudget(budget, current.records)
	build := captureBuildIdentity()
	report := gateReport{
		SchemaVersion:  gateSchemaVersion,
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		BudgetPath:     budgetPath,
		BudgetBaseline: budget.Baseline,
		Output:         flags.output,
		Source:         captureSourceIdentity(build),
		GoVersion:      build.GoVersion,
		GOOS:           build.GOOS,
		GOARCH:         build.GOARCH,
		Tags:           build.Tags,
		Runs:           runIDs(current.records),
		Results:        results,
		RecordCount:    len(current.records),
	}
	report.summarise()
	if err = writeGateReport(flags.output+".gate.json", report); err != nil {
		return err
	}
	printGateReport(stdout, report, budget)
	if report.Failed > 0 {
		return fmt.Errorf("%d budget rule(s) failed", report.Failed)
	}
	if report.Passed == 0 {
		return fmt.Errorf("the gate verified nothing: every rule was skipped (see the reasons above) " +
			"and a skipped rule is not a pass")
	}
	return nil
}

// defaultBudgetPath is the checked-in budget, relative to the module root, which is what the gate
// job's working directory is.
func defaultBudgetPath() string {
	return "testdata/budget.json"
}

const usageText = `memorystudy - where a Box's memory goes, stage by stage, in child processes.

Measure one case (each -gc mode is a separate child process):

  memorystudy -case empty -activity none -idle 0s -gc both \
              -output base.jsonl -csv base.csv

  memorystudy -case ordinary -nodes 1000 -groups 2 -activity none -gc forced \
              -output ordinary-1000.jsonl

  memorystudy -case ordinary -node-proto direct -nodes 8 -groups 2 -activity probe \
              -gc forced -output probe.jsonl

  memorystudy -case empty -activity traffic -held-flows 4 -traffic-bytes 65536 \
              -gc forced -output traffic.jsonl

Fast budget gate, evaluated against a baseline-derived budget:

  memorystudy -gate -runs 3 -output gate.jsonl -budget testdata/budget.json

Derive that budget from a saved baseline:

  memorystudy -write-budget testdata/budget.json -from baseline-3runs.jsonl

Exit status is non-zero when a child fails or a budget rule fails. A skipped rule is not a pass.
`
