package main

// Negative tests for the GATE's own integrity.
//
// The gate is a measurement of a measurement: if it certifies a ledger that lost a run, a run_end,
// a stage or a process identity, then every rule it answers was answered about a different
// population than the one the budget was derived from, and the exit code is a lie. Each case below
// reached "17 pass, exit 0" before `requireCompleteEvidence` / `validateBudgetStructure` existed
// (the tampered ledgers are kept in `.debug-post-0.1.55-2026-11-22/cases/E/data/`).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	gateTestChildPID  = 4242
	gateTestParentPID = 1717
)

// completeRun builds one run's records in the shape a real ledger has: a parent-written
// child_result, then the child's stages with contiguous indexes and one PID. The spec is on every
// line because completeness now includes "which case did this run measure".
func completeRun(id string, spec runSpec) []recordMap {
	stages := []string{
		stageSetup, stageConfigParse, stageConfigBuild, stageConstruct, stageFixtureServers,
		stageServiceStart, stageReady, stageClose, stageRunEnd,
	}
	encodedSpec := map[string]any{
		"case":          spec.Case,
		"nodes":         json.Number(fmt.Sprintf("%d", spec.Nodes)),
		"groups":        json.Number(fmt.Sprintf("%d", spec.Groups)),
		"node_proto":    spec.NodeProto,
		"config_source": spec.ConfigSource,
		"activity":      activityList(spec.Activity),
		"gc_mode":       spec.GCMode,
		"held_flows":    json.Number(fmt.Sprintf("%d", spec.HeldFlows)),
		"traffic_bytes": json.Number(fmt.Sprintf("%d", spec.TrafficBytes)),
	}
	records := []recordMap{{
		"record":       recordChildResult,
		"status":       statusOK,
		"run_id":       id,
		"pid":          gateTestParentPID,
		"stage":        recordChildResult,
		"stage_index":  0,
		"run_end_seen": true,
		"spec":         encodedSpec,
	}}
	for index, stage := range stages {
		records = append(records, recordMap{
			"record":      recordStage,
			"status":      statusOK,
			"run_id":      id,
			"pid":         gateTestChildPID,
			"stage":       stage,
			"stage_index": index,
			"spec":        encodedSpec,
		})
	}
	return records
}

func activityList(list []string) []any {
	encoded := make([]any, 0, len(list))
	for _, entry := range list {
		encoded = append(encoded, entry)
	}
	return encoded
}

// stampLedgerIdentity gives every stage record the identity block a real ledger carries, so the
// gate can get past requireCandidateIdentity and reach the budget-provenance decision this test is
// about. detected_by=declared is only accepted together with an explicit -source-sha assertion.
func stampLedgerIdentity(records []recordMap, sha string) {
	for _, record := range records {
		identity, _ := record["identity"].(map[string]any)
		if identity == nil {
			identity = map[string]any{}
			record["identity"] = identity
		}
		identity["source"] = map[string]any{
			"sha":         sha,
			"detected_by": detectedByDeclared,
		}
		identity["build"] = map[string]any{
			"goos":       runtime.GOOS,
			"goarch":     runtime.GOARCH,
			"go_version": runtime.Version(),
		}
	}
}

// completeSession is a ledger with every planned run present: the POSITIVE control for the checker.
func completeSession(t *testing.T, repetitions int) (*session, *runFlags) {
	t.Helper()
	flags := &runFlags{runs: repetitions}
	var records []recordMap
	for repetition := 0; repetition < repetitions; repetition++ {
		for specIndex, spec := range gateSpecs(false) {
			records = append(records, completeRun(runTestID(repetition, specIndex), spec)...)
		}
	}
	return &session{records: records, planned: len(gateSpecs(false)) * repetitions, flags: flags}, flags
}

func runTestID(repetition, specIndex int) string {
	return "20260101T000000Z-test-" + string(rune('a'+repetition)) + "-" + string(rune('0'+specIndex))
}

func TestCompleteEvidenceAcceptsAFullSession(t *testing.T) {
	for _, repetitions := range []int{1, 3} {
		current, flags := completeSession(t, repetitions)
		if err := requireCompleteEvidence(current, flags); err != nil {
			t.Fatalf("%d repetition(s): a complete session must be accepted, got %v", repetitions, err)
		}
	}
}

// A run whose run_end line was dropped is not complete evidence: the run_end is the child's own
// statement that every stage before it finished. Deleting 5 of 6 was exit 0 before the check.
func TestCompleteEvidenceRefusesAMissingRunEnd(t *testing.T) {
	current, flags := completeSession(t, 1)
	var kept []recordMap
	for _, record := range current.records {
		if record.stage() == stageRunEnd && record.runID() == runTestID(0, 0) {
			continue
		}
		kept = append(kept, record)
	}
	current.records = kept
	if err := requireCompleteEvidence(current, flags); err == nil {
		t.Fatal("a run without a run_end must be refused")
	}
}

// A session that lost an entire run is refused: the budget's thresholds are per-repetition, so one
// repetition cannot be compared against a three-repetition baseline. Keeping 1 of 3 was exit 0.
func TestCompleteEvidenceRefusesAFewerThanPlannedRepetitions(t *testing.T) {
	current, flags := completeSession(t, 3)
	cut := len(current.records) / 3
	current.records = current.records[:len(current.records)-cut]
	if err := requireCompleteEvidence(current, flags); err == nil {
		t.Fatal("a session with fewer runs than planned must be refused")
	}
}

// A stage that no budget rule observes can still be deleted, and the remaining ledger then answers
// the rules from an incomplete lifecycle. The stage_index gap is what makes that visible.
func TestCompleteEvidenceRefusesAStageGap(t *testing.T) {
	current, flags := completeSession(t, 1)
	var kept []recordMap
	dropped := false
	for _, record := range current.records {
		if !dropped && record.stage() == stageReady && record.runID() == runTestID(0, 2) {
			dropped = true
			continue
		}
		kept = append(kept, record)
	}
	current.records = kept
	if err := requireCompleteEvidence(current, flags); err == nil {
		t.Fatal("a ledger with a stage gap must be refused")
	}
}

// The required stage set is checked independently of the indexes: losing the construct stage (which
// no gate rule reads) must not look like a complete run.
func TestCompleteEvidenceRefusesAMissingCoreStage(t *testing.T) {
	current, flags := completeSession(t, 1)
	var kept []recordMap
	index := 0
	for _, record := range current.records {
		if record.runID() == runTestID(0, 1) && record.stage() == stageConstruct {
			continue
		}
		kept = append(kept, record)
		index++
	}
	current.records = kept
	if err := requireCompleteEvidence(current, flags); err == nil {
		t.Fatal("a run that never reached construct must be refused")
	}
}

// Two processes writing one run id is not one measurement: it is two halves of two measurements.
func TestCompleteEvidenceRefusesMixedProcessIDs(t *testing.T) {
	current, flags := completeSession(t, 1)
	for _, record := range current.records {
		if record.runID() == runTestID(0, 3) && record.stage() == stageClose {
			record["pid"] = gateTestChildPID + 1
		}
	}
	if err := requireCompleteEvidence(current, flags); err == nil {
		t.Fatal("a run with two child processes must be refused")
	}
}

// A child the parent recorded as failed is not evidence, whatever the stages it left behind say.
func TestCompleteEvidenceRefusesAFailedChild(t *testing.T) {
	current, flags := completeSession(t, 1)
	for _, record := range current.records {
		if record.runID() == runTestID(0, 4) && record.recordKind() == recordChildResult {
			record["status"] = statusFailed
			record["failure_cause"] = "exited 0 without writing a run_end"
		}
	}
	if err := requireCompleteEvidence(current, flags); err == nil {
		t.Fatal("a session with a failed child must be refused")
	}
}

func writeTestBudget(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "budget.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A budget whose rules cannot fail is not a budget. Empty rules, a duplicated id and a rule with no
// threshold all reached exit 0 before this validation existed. The schema/2 checks add two more ways
// a budget can silently stop measuring: a rule that lost its activity selector (which would go back
// to matching another case's runs) and a file that lost its provenance (which is what lets the gate
// refuse a budget re-derived from the candidate).
func TestBudgetStructureIsRefusedWhenItCannotFail(t *testing.T) {
	minimum := 1.0
	valid := budgetRule{ID: "ok-rule", Case: "empty", NodeProto: "direct", Activity: []string{"none"},
		GCMode: "forced", Stage: stageClose, Metric: "readings.live_heap_bytes", Min: &minimum}
	reference := &budgetProvenance{Kind: provenanceReference, LedgerSHA256: strings.Repeat("a", 64)}
	for _, testCase := range []struct {
		name       string
		rules      any
		provenance *budgetProvenance
	}{
		{"no rules", []budgetRule{}, reference},
		{"duplicate id", []budgetRule{valid, valid}, reference},
		{"no threshold", []budgetRule{{ID: "no-threshold", Case: "empty", NodeProto: "direct", Activity: []string{"none"}, GCMode: "forced", Stage: stageClose, Metric: "readings.live_heap_bytes"}}, reference},
		{"no id", []budgetRule{{Case: "empty", NodeProto: "direct", Activity: []string{"none"}, GCMode: "forced", Stage: stageClose, Metric: "readings.live_heap_bytes", Min: &minimum}}, reference},
		{"no activity", []budgetRule{{ID: "no-activity", Case: "empty", NodeProto: "direct", GCMode: "forced", Stage: stageClose, Metric: "readings.live_heap_bytes", Min: &minimum}}, reference},
		{"no case", []budgetRule{{ID: "no-case", NodeProto: "direct", Activity: []string{"none"}, GCMode: "forced", Stage: stageClose, Metric: "readings.live_heap_bytes", Min: &minimum}}, reference},
		{"empty activity list", []budgetRule{{ID: "empty-activity", Case: "empty", NodeProto: "direct", Activity: []string{}, GCMode: "forced", Stage: stageClose, Metric: "readings.live_heap_bytes", Min: &minimum}}, reference},
		{"no provenance", []budgetRule{valid}, nil},
		{"unknown provenance kind", []budgetRule{valid}, &budgetProvenance{Kind: "somebody-told-me", LedgerSHA256: strings.Repeat("a", 64)}},
		{"no ledger hash", []budgetRule{valid}, &budgetProvenance{Kind: provenanceReference}},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"schema_version": budgetSchemaVersion,
				"provenance":     testCase.provenance,
				"rules":          testCase.rules,
			})
			if err != nil {
				t.Fatal(err)
			}
			path := writeTestBudget(t, string(body))
			if _, err = readBudgetFile(path); err == nil {
				t.Fatalf("a budget with %s must be refused", testCase.name)
			}
		})
	}

	// The positive control: one rule with a threshold and a reference provenance is a budget.
	body, err := json.Marshal(map[string]any{
		"schema_version": budgetSchemaVersion,
		"provenance":     reference,
		"rules":          []budgetRule{valid},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = readBudgetFile(writeTestBudget(t, string(body))); err != nil {
		t.Fatalf("a well-formed one-rule reference budget must be accepted: %v", err)
	}

	// A legacy schema/1 budget is still READ and EVALUATED, because a hand-written budget (the CI
	// canary is one) has to be able to fail rather than merely be rejected by a version check. It
	// is marked legacy, and the gate refuses it as a certification reference.
	legacyBody, err := json.Marshal(map[string]any{
		"schema_version": legacyBudgetSchemaVersion,
		"baseline":       map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH},
		"rules": []map[string]any{{
			"id": "canary-impossible", "case": "empty", "nodes": 0, "groups": 0,
			"node_proto": "direct", "gc_mode": "forced", "stage": "close",
			"metric": "readings.this_metric_does_not_exist", "max": 1, "required": true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := writeTestBudget(t, string(legacyBody))
	legacyBudget, err := readBudgetFile(legacyPath)
	if err != nil {
		t.Fatalf("the legacy budget must stay readable so its rules can fail: %v", err)
	}
	if !legacyBudget.legacy {
		t.Fatal("a schema/1 budget was not marked legacy")
	}
	summary := ledgerSummary{SHA: strings.Repeat("b", 40), Runs: []string{"run-a"}}
	if err = requireReferenceProvenance(legacyBudget, summary, false); err == nil {
		t.Fatal("a legacy budget must be refused as a certification reference")
	}
}

// TestAnUncertifiableBudgetKeepsItsDiagnostics pins INC-04-A: the refusal report for an
// uncertifiable budget must be the SINGLE report written, with the per-rule results intact. The
// gate used to write that report and then fall through to the refusal branch, which re-wrote the
// same path from an empty result set, so a budget whose rules passed or were skipped lost the
// diagnostic that said so.
func TestAnUncertifiableBudgetKeepsItsDiagnostics(t *testing.T) {
	current, _ := completeSession(t, 1)

	cases := []struct {
		name       string
		metric     string
		max        any
		required   bool
		wantStatus string
	}{
		{"passing", "readings.diagnostic_metric", 2, true, statusOK},
		{"missing", "readings.this_metric_does_not_exist", 1, true, statusFailed},
		{"skipped", "readings.this_metric_does_not_exist", 1, false, statusUnsupported},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"schema_version": legacyBudgetSchemaVersion,
				"baseline":       map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH},
				"rules": []map[string]any{{
					"id": "diagnostic", "case": "empty", "nodes": 0, "groups": 0,
					"node_proto": "direct", "gc_mode": "forced", "stage": "close",
					"metric": testCase.metric, "max": testCase.max, "required": testCase.required,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			budget, err := readBudgetFile(writeTestBudget(t, string(body)))
			if err != nil {
				t.Fatal(err)
			}
			records := current.records
			if testCase.name == "passing" {
				// The synthetic ledger carries no readings, so a "passing" rule needs a value the
				// fixture actually produced. Clone the records and give one stage a reading rather
				// than weakening the rule to optional.
				cloned := make([]recordMap, len(records))
				for index, record := range records {
					copied := make(recordMap, len(record)+1)
					for key, value := range record {
						copied[key] = value
					}
					cloned[index] = copied
				}
				for index := range cloned {
					cloned[index]["readings"] = map[string]any{"diagnostic_metric": float64(1)}
				}
				records = cloned
			}
			directory := t.TempDir()
			flags := &runFlags{output: filepath.Join(directory, testCase.name+".jsonl")}
			summary, err := summariseLedger(records)
			if err != nil {
				t.Fatal(err)
			}
			refusal := requireReferenceProvenance(budget, summary, false)
			if !errors.Is(refusal, ErrUncertifiableBudget) {
				t.Fatalf("the fixture is not an uncertifiable budget: %v", refusal)
			}
			if gateErr := writeUncertifiableReport(flags, "budget.json", budget, records, summary, gateVerification{}, refusal); gateErr == nil {
				t.Fatal("an uncertifiable budget produced no error")
			}
			content, readErr := os.ReadFile(flags.output + ".gate.json")
			if readErr != nil {
				t.Fatalf("no gate report: %v", readErr)
			}
			var report gateReport
			if err = json.Unmarshal(content, &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Results) != 1 {
				t.Fatalf("the per-rule diagnostics were lost: results=%d", len(report.Results))
			}
			if report.Results[0].Status != testCase.wantStatus {
				t.Fatalf("rule status %q, want %q (%+v)", report.Results[0].Status, testCase.wantStatus, report.Results[0])
			}
			if report.Error == "" {
				t.Fatal("the refusal reason is missing from the report")
			}
		})
	}
}

// TestAnUncertifiableBudgetIsStillEvaluated is the CI canary's contract.
//
// The canary budget is hand-written (schema/1, no provenance): it may never certify a
// candidate, but the evaluator must still run on it, because a canary that is rejected by a
// provenance check proves nothing about whether the evaluator can fail. The gate's refusal is
// a sentinel, and the same budget evaluated against a ledger that lacks the required metric
// must produce a failed rule - the report path in main.go writes exactly these results.
func TestAnUncertifiableBudgetIsStillEvaluated(t *testing.T) {
	current, _ := completeSession(t, 1)

	canaryBody, err := json.Marshal(map[string]any{
		"schema_version": legacyBudgetSchemaVersion,
		"baseline":       map[string]any{"goos": runtime.GOOS, "goarch": runtime.GOARCH},
		"rules": []map[string]any{{
			"id": "canary-impossible", "case": "empty", "nodes": 0, "groups": 0,
			"node_proto": "direct", "gc_mode": "forced", "stage": "close",
			"metric": "readings.this_metric_does_not_exist", "max": 1, "required": true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	budget, err := readBudgetFile(writeTestBudget(t, string(canaryBody)))
	if err != nil {
		t.Fatalf("the canary budget must stay readable: %v", err)
	}
	summary := ledgerSummary{SHA: strings.Repeat("a", 40), Runs: []string{"run-a"}}
	refusal := requireReferenceProvenance(budget, summary, false)
	if refusal == nil {
		t.Fatal("an unprovenanced canary budget was accepted as a certification reference")
	}
	if !errors.Is(refusal, ErrUncertifiableBudget) {
		t.Fatalf("the refusal is not the uncertifiable-budget sentinel: %v", refusal)
	}

	results := evaluateBudget(budget, current.records)
	if len(results) != 1 {
		t.Fatalf("the canary evaluated %d rules, want 1", len(results))
	}
	if results[0].ID != "canary-impossible" || results[0].Status != statusFailed {
		t.Fatalf("the canary rule did not fail when the evaluator ran: %+v", results[0])
	}
	if !strings.Contains(results[0].Reason, "required evidence missing") {
		t.Fatalf("the canary rule failed for the wrong reason: %q", results[0].Reason)
	}
}
