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
	"os"
	"path/filepath"
	"testing"
)

const (
	gateTestChildPID  = 4242
	gateTestParentPID = 1717
)

// completeRun builds one run's records in the shape a real ledger has: a parent-written
// child_result, then the child's stages with contiguous indexes and one PID.
func completeRun(id string) []recordMap {
	stages := []string{
		stageSetup, stageConfigParse, stageConfigBuild, stageConstruct, stageFixtureServers,
		stageServiceStart, stageReady, stageClose, stageRunEnd,
	}
	records := []recordMap{{
		"record":       recordChildResult,
		"status":       statusOK,
		"run_id":       id,
		"pid":          gateTestParentPID,
		"stage":        recordChildResult,
		"stage_index":  0,
		"run_end_seen": true,
	}}
	for index, stage := range stages {
		records = append(records, recordMap{
			"record":      recordStage,
			"status":      statusOK,
			"run_id":      id,
			"pid":         gateTestChildPID,
			"stage":       stage,
			"stage_index": index,
		})
	}
	return records
}

// completeSession is a ledger with every planned run present: the POSITIVE control for the checker.
func completeSession(t *testing.T, repetitions int) (*session, *runFlags) {
	t.Helper()
	flags := &runFlags{runs: repetitions}
	var records []recordMap
	for repetition := 0; repetition < repetitions; repetition++ {
		for specIndex := range gateSpecs(false) {
			records = append(records, completeRun(runTestID(repetition, specIndex))...)
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
// threshold all reached exit 0 before this validation existed.
func TestBudgetStructureIsRefusedWhenItCannotFail(t *testing.T) {
	minimum := 1.0
	valid := budgetRule{ID: "ok-rule", Case: "empty", NodeProto: "direct", GCMode: "forced", Stage: stageClose, Metric: "readings.live_heap_bytes", Min: &minimum}
	for _, testCase := range []struct {
		name  string
		rules any
	}{
		{"no rules", []budgetRule{}},
		{"duplicate id", []budgetRule{valid, valid}},
		{"no threshold", []budgetRule{{ID: "no-threshold", Case: "empty", NodeProto: "direct", GCMode: "forced", Stage: stageClose, Metric: "readings.live_heap_bytes"}}},
		{"no id", []budgetRule{{Case: "empty", NodeProto: "direct", GCMode: "forced", Stage: stageClose, Metric: "readings.live_heap_bytes", Min: &minimum}}},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"schema_version": budgetSchemaVersion,
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

	// The positive control: one rule with a threshold is a budget.
	body, err := json.Marshal(map[string]any{"schema_version": budgetSchemaVersion, "rules": []budgetRule{valid}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = readBudgetFile(writeTestBudget(t, string(body))); err != nil {
		t.Fatalf("a well-formed one-rule budget must be accepted: %v", err)
	}
}
