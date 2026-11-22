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
