package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
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
	command.StringVar(&flags.nodeProto, "node-proto", "", "node kind: trojan | direct | heavy | mixed (the case decides by default)")
	command.StringVar(&flags.configSource, "config-source", "", "where the nodes live: static | provider (default static; provider needs -nodes >= 1)")
	command.StringVar(&flags.activity, "activity", "none", "activity history: none | probe | traffic | a comma list")
	command.StringVar(&flags.idle, "idle", "0s", "cumulative idle checkpoints, e.g. 30s,300s")
	command.StringVar(&flags.postIdle, "post-idle", "0s", "idle after the activity history, e.g. 30s")
	command.StringVar(&flags.gcMode, "gc", "forced", "reading discipline: natural | forced | both (each is its own process)")
	command.IntVar(&flags.heldFlows, "held-flows", 4, "flows held open through the relay for -activity traffic")
	command.IntVar(&flags.trafficBytes, "traffic-bytes", 64<<10, "payload bytes pushed through each flow")
	command.IntVar(&flags.changedNodes, "changed-nodes", -1, "provider refresh K axis: how many nodes change in the CHANGED document (-1 keeps the historical all-nodes change; 0 is a byte-identical document)")

	command.IntVar(&flags.runs, "runs", 1, "independent repetitions of the planned case list")
	command.StringVar(&flags.output, "output", "-", "merged JSONL path (- for stdout); per-run raw files go to <path>.d/")
	command.StringVar(&flags.csv, "csv", "", "optional CSV view of the merged ledger")

	command.BoolVar(&flags.gate, "gate", false, "run the fast budget matrix and evaluate it against -budget")
	command.StringVar(&flags.budgetPath, "budget", "", "budget JSON for -gate (default testdata/budget.json next to the source)")
	command.StringVar(&flags.writeBudget, "write-budget", "", "derive a budget JSON from a baseline and write it here")
	command.StringVar(&flags.from, "from", "", "read an existing merged JSONL instead of running anything (with -write-budget, or with -gate to judge that ledger)")
	command.StringVar(&flags.referenceManifest, "reference-manifest", "", "with -write-budget -from: the archived reference manifest that makes the ledger a reference (without it the budget is provenance=derived and cannot gate)")
	command.BoolVar(&flags.selfCheck, "self-check", false, "with -gate: allow the budget to be the reference ledger's own budget (verifies the reference against itself)")
	command.StringVar(&flags.sourceSHA, "source-sha", "", "with -gate: the revision this run certifies, for a tree where neither git nor the binary's VCS stamp can be read; it must equal the ledger's (and the checkout's, when observable)")
	command.BoolVar(&flags.wide, "wide", false, "add the 1000- and 10000-node scale cases to the gate matrix")

	command.IntVar(&flags.gomaxprocs, "gomaxprocs", 0, "start every child with GOMAXPROCS=n in its environment (0 leaves it alone)")
	command.StringVar(&flags.godebug, "godebug", "", "start every child with this GODEBUG string in its environment")
	command.BoolVar(&flags.inittrace, "inittrace", false, "add inittrace=1 to the children's GODEBUG and keep their stderr")

	command.StringVar(&flags.heapprofile, "heapprofile", "", "internal (child): write a heap profile here before the process exits; .allocs carries allocation sites")
	command.StringVar(&flags.dumpFixture, "dump-fixture", "", "internal: write the generated configuration document(s) to this directory and exit")
	command.BoolVar(&flags.child, "child", false, "internal: run one case in this process and write its JSONL to stdout")
	command.StringVar(&flags.runID, "run-id", "", "internal: the run id the parent assigned")
	command.DurationVar(&flags.timeout, "timeout", 15*time.Minute, "per-child timeout")
	command.BoolVar(&flags.verbose, "verbose", false, "log each child's outcome to stderr")

	if err := command.Parse(args); err != nil {
		return err
	}
	provided := make(map[string]bool)
	command.Visit(func(visited *flag.Flag) { provided[visited.Name] = true })
	flags.provided = provided
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
		ConfigSource: flags.configSource,
		Activity:     nil,
		Idle:         nil,
		PostIdle:     flags.postIdle,
		GCMode:       flags.gcMode,
		HeldFlows:    flags.heldFlows,
		TrafficBytes: flags.trafficBytes,
	}
	// The child rebuilds the spec from its own command line, so the K axis has to be restored here
	// too. Dropping it here would make the child measure K=N while its ledger said K=1 (or K=0),
	// which is exactly the kind of quiet mislabelling the axis exists to prevent.
	if changed, changedErr := changedNodesFromFlags(flags); changedErr != nil {
		return changedErr
	} else if changed != nil {
		spec.ChangedNodes = changed
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
	if flags.dumpFixture != "" {
		return dumpFixtureFiles(spec, flags.dumpFixture)
	}
	heapProfilePath = flags.heapprofile
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

// runWriteBudget derives a budget from a reference ledger (or by capturing one now).
//
// # The completeness check is the gate's check
//
// -write-budget used to accept any JSONL: a ledger that lost 5 of 6 run_end records, or half its
// repetitions, still produced thresholds. That made the budget the weak link the gate explicitly
// refuses at evaluation time: the gate read a file derived from evidence it would never have
// accepted. Both entry points now run the same requireCompleteLedger, so what the thresholds are
// derived from is by construction as complete as what the gate will certify.
//
// # derived vs reference
//
// Capturing a matrix in this invocation, or deriving from a ledger with an archived manifest, mints
// a REFERENCE budget. Deriving from a bare ledger mints a DERIVED budget, which -gate refuses: a
// budget derived from the candidate passes every rule by construction, so it is not a gate.
func runWriteBudget(flags *runFlags) error {
	planned := gateSpecs(flags.wide)
	kind := provenanceReference
	provenance := budgetProvenance{Kind: kind}
	var records []recordMap
	var err error

	if flags.from != "" {
		lines, readErr := readLines(flags.from)
		if readErr != nil {
			return readErr
		}
		records, err = decodeLines(lines)
		if err != nil {
			return err
		}
		ledgerSHA, hashErr := sha256File(flags.from)
		if hashErr != nil {
			return hashErr
		}
		provenance.LedgerPath = flags.from
		provenance.LedgerSHA256 = ledgerSHA
		repetitions, repetitionErr := repetitionsFor(flags, planned, records)
		if repetitionErr != nil {
			return repetitionErr
		}
		if flags.referenceManifest != "" {
			manifest, manifestErr := readManifest(flags.referenceManifest)
			if manifestErr != nil {
				return manifestErr
			}
			if !strings.EqualFold(manifest.LedgerSHA256, ledgerSHA) {
				return fmt.Errorf("the reference manifest %s records ledger %s but %s hashes to %s; "+
					"the manifest describes different evidence", flags.referenceManifest,
					manifest.LedgerSHA256, flags.from, ledgerSHA)
			}
			manifestSHA, manifestHashErr := sha256File(flags.referenceManifest)
			if manifestHashErr != nil {
				return manifestHashErr
			}
			provenance.ManifestPath = flags.referenceManifest
			provenance.ManifestSHA = manifestSHA
			if manifest.Repetitions > 0 {
				// The archived capture is authoritative: it was the contract the reference ledger
				// was measured under, and a caller who forgets -runs 3 must not silently weaken the
				// completeness check to one repetition.
				repetitions = manifest.Repetitions
			}
			provenance.Kind = provenanceReference
		} else {
			provenance.Kind = provenanceDerived
		}
		if err = requireCompleteLedger(records, repetitions, planned); err != nil {
			return err
		}
	} else {
		flags.output = budgetRunOutput(flags)
		current, runErr := runSpecs(flags, planned)
		if runErr != nil {
			return runErr
		}
		if current.failures > 0 {
			return fmt.Errorf("%d of %d child run(s) failed; a session with a failed child cannot be "+
				"the reference the thresholds are derived from", current.failures, current.planned)
		}
		if err = requireCompleteLedger(current.records, flags.runs, planned); err != nil {
			return err
		}
		records = current.records
		ledgerSHA, hashErr := sha256File(flags.output)
		if hashErr != nil {
			return hashErr
		}
		provenance.LedgerPath = flags.output
		provenance.LedgerSHA256 = ledgerSHA
		summary, summaryErr := summariseLedger(records)
		if summaryErr != nil {
			return summaryErr
		}
		provenance.ManifestPath = flags.output + ".manifest.json"
		if err = writeManifest(provenance.ManifestPath,
			manifestFromLedger(summary, len(records), flags.output, ledgerSHA, flags.runs, planned)); err != nil {
			return err
		}
		manifestSHA, hashErr := sha256File(provenance.ManifestPath)
		if hashErr != nil {
			return hashErr
		}
		provenance.ManifestSHA = manifestSHA
	}
	budget, err := deriveBudget(records, provenance)
	if err != nil {
		return err
	}
	if err = writeBudgetFile(flags.writeBudget, budget); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "memorystudy: wrote %d budget rules to %s from %d records "+
		"(provenance=%s, ledger_sha256=%s, %d repetition(s) x %d spec(s))\n",
		len(budget.Rules), flags.writeBudget, len(records), budget.Provenance.Kind,
		budget.Provenance.LedgerSHA256, budget.Provenance.Repetitions, budget.Provenance.Specs)
	if budget.Provenance.Kind != provenanceReference {
		fmt.Fprintf(os.Stderr, "memorystudy: this budget is provenance=%s: -gate will refuse it. "+
			"To mint a reference, capture the matrix in this invocation (omit -from) or pass the "+
			"archived -reference-manifest.\n", budget.Provenance.Kind)
	}
	return nil
}

// repetitionsFor resolves how many repetitions a ledger read from disk must contain.
//
// An explicit -runs is a claim the ledger has to satisfy. Without one, the count is inferred from
// the ledger itself and then checked for uniformity by requireCompleteLedger: inferring is a
// convenience, not a relaxation, because a ledger with 7 runs of one spec and 6 of another still
// fails.
func repetitionsFor(flags *runFlags, planned []runSpec, records []recordMap) (int, error) {
	if flags.provided["runs"] {
		return flags.runs, nil
	}
	repetitions, specs := repetitionsAndSpecs(records)
	if repetitions < 1 || specs != len(planned) {
		return 0, fmt.Errorf("the ledger contains %d repetition(s) of %d distinct spec(s), the gate "+
			"matrix has %d; pass -runs explicitly if this is intended", repetitions, specs, len(planned))
	}
	return repetitions, nil
}

func budgetRunOutput(flags *runFlags) string {
	if flags.output != "" && flags.output != "-" {
		return flags.output
	}
	return "memorystudy-baseline-" + newSessionStamp() + ".jsonl"
}

// runGate evaluates a ledger against a reference budget.
//
// `-from <ledger>` judges an existing merged JSONL (the archived reference, or a ledger somebody
// else produced) instead of running the matrix again. It goes through every check the measured path
// goes through - completeness, identity, compatibility, non-circularity - so reading a ledger back
// is not a way around them.
func runGate(flags *runFlags, stdout io.Writer) error {
	if flags.output == "" || flags.output == "-" {
		return fmt.Errorf("-gate needs -output: a gate that does not keep its raw ledger cannot be " +
			"re-examined when it fails")
	}
	planned := gateSpecs(flags.wide)
	var records []recordMap
	var repetitions int
	if flags.from != "" {
		lines, err := readLines(flags.from)
		if err != nil {
			return err
		}
		records, err = decodeLines(lines)
		if err != nil {
			return err
		}
		repetitions, err = repetitionsFor(flags, planned, records)
		if err != nil {
			return err
		}
	} else {
		current, err := runSpecs(flags, planned)
		if err != nil {
			return err
		}
		// A failed child does not stop the session (the other cases are still worth measuring), so
		// the failure has to stop the GATE. Without this the gate certified a session in which every
		// child had failed: runSpecs only counted the failures, and nothing read the count.
		if current.failures > 0 {
			return fmt.Errorf("%d of %d child run(s) failed; a session with a failed child is not "+
				"evidence for a gate (the child_result records name the run and the cause)",
				current.failures, current.planned)
		}
		records = current.records
		repetitions = flags.runs
	}
	if err := requireCompleteLedger(records, repetitions, planned); err != nil {
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
	summary, err := summariseLedger(records)
	if err != nil {
		return err
	}
	verification, err := verifyGateInputs(budget, summary, flags.selfCheck, flags.sourceSHA)
	if err != nil {
		// The refusal is the verdict, so it is written down as one: an exit code alone leaves the
		// reviewer with no record of which identity check refused and why.
		refusal := gateReport{
			SchemaVersion:  gateSchemaVersion,
			GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
			BudgetPath:     budgetPath,
			BudgetBaseline: budget.Baseline,
			Output:         flags.output,
			Source:         captureSourceIdentity(captureBuildIdentity()),
			GoVersion:      summary.GoVersion,
			GOOS:           summary.GOOS,
			GOARCH:         summary.GOARCH,
			Tags:           summary.Tags,
			Runs:           runIDs(records),
			RecordCount:    len(records),
			Verification:   &verification,
			Error:          err.Error(),
		}
		if writeErr := writeGateReport(flags.output+".gate.json", refusal); writeErr != nil {
			return writeErr
		}
		return fmt.Errorf("the gate refused to compare: %w", err)
	}
	results := evaluateBudget(budget, records)
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
		Runs:           runIDs(records),
		Results:        results,
		RecordCount:    len(records),
		Verification:   &verification,
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

Judge a ledger that already exists (the archived reference, or somebody else's):

  memorystudy -gate -from reference-3runs.jsonl -output gate.jsonl -budget testdata/budget.json
  memorystudy -gate -from reference-3runs.jsonl -output self.jsonl \
              -budget testdata/budget.json -self-check

Capture a reference (writes the budget, the raw ledger and its manifest):

  memorystudy -write-budget analysis/budget-reference.json -runs 3 \
              -output samples/gate-reference-3runs.jsonl

Re-derive that budget from the archived ledger (needs the manifest, or it is only "derived"):

  memorystudy -write-budget budget.json -from samples/gate-reference-3runs.jsonl \
              -reference-manifest samples/gate-reference-3runs.jsonl.manifest.json

Exit status is non-zero when a child fails, a budget rule fails, or the gate refuses to compare
(a candidate that is not this checkout, a toolchain/tag/fixture mismatch, or a budget derived from
the ledger it is asked to certify). A skipped rule is not a pass.
`
