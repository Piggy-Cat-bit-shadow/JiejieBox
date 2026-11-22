package main

// Negative tests for the gate's TRUST: the activity selector, the denominator, the identity the
// verdict stands on, the completeness of -write-budget, and the refusal of a budget that was
// derived from the ledger it is asked to judge.
//
// Each test reproduces the pre-fix behaviour next to the fixed one where that is possible without
// the old code: a differential assertion ("the old selector matched this run; the new one does not")
// is stronger evidence than a comment saying so. The CLI-level before/after runs are recorded in
// reports/M1-gate.md, because they compare two BINARIES (the HEAD build and this one) and cannot be
// a Go test of this package.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ruleByID(t *testing.T, budget budgetFile, id string) budgetRule {
	t.Helper()
	for _, rule := range budget.Rules {
		if rule.ID == id {
			return rule
		}
	}
	t.Fatalf("the budget has no rule %q", id)
	return budgetRule{}
}

// TestActivitySelectorStopsAnotherActivityCoveringAMissingCase is the first residual.
//
// The gate matrix contains two empty/direct/forced cases that differ only in their activity history:
// the plain one and the traffic one. With no activity term in the selector, a base rule was answered
// by BOTH (six observations for a three-run baseline), so deleting the plain case's runs entirely
// still left the rule green on the traffic runs: a case that disappeared was covered by a different
// case. The first half of this test reproduces that behaviour with an activity-less rule; the second
// half asserts the shipped rule refuses to be covered.
func TestActivitySelectorStopsAnotherActivityCoveringAMissingCase(t *testing.T) {
	full := syntheticGateLedger()
	budget, err := deriveBudget(full, budgetProvenance{Kind: provenanceDerived})
	if err != nil {
		t.Fatalf("the baseline could not be turned into a budget: %v", err)
	}

	// The plain (activity=none) empty/forced case never ran. Everything else is intact.
	var withoutPlain []recordMap
	for _, record := range full {
		if record.specField("case") == "empty" &&
			record.specField("node_proto") == "direct" &&
			record.specField("gc_mode") == "forced" &&
			record.specActivity() == "none" {
			continue
		}
		withoutPlain = append(withoutPlain, record)
	}

	// The baseline itself: the old selector matched six records (three plain + three traffic), the
	// new one matches three. This is the "observed count changes" the budget diff shows.
	pinned := ruleByID(t, budget, "base-idle-live-heap")
	if got := activityKey(pinned.Activity); got != "none" {
		t.Fatalf("base-idle-live-heap selects activity %q, want \"none\"", got)
	}
	if values, matched, _ := observedValues(pinned, full); len(values) != 1 || matched != 1 {
		t.Fatalf("the activity-pinned rule matched %d record(s) with %d value(s) on a one-repetition "+
			"ledger, want 1 and 1 (the old selector matched 2, one per activity, which is the bug)",
			matched, len(values))
	}

	// The pre-fix selector: the same rule with no activity term. It still finds the traffic runs.
	legacy := pinned
	legacy.Activity = nil
	if values, matched, _ := observedValues(legacy, withoutPlain); matched == 0 || len(values) == 0 {
		t.Fatalf("the activity-less selector no longer matches the traffic run, so this test no " +
			"longer reproduces the pre-fix hole it exists to demonstrate")
	}

	// The shipped selector: no observation at all, so a REQUIRED rule has to fail instead of being
	// answered by the other activity's run.
	values, matched, _ := observedValues(pinned, withoutPlain)
	if matched != 0 || len(values) != 0 {
		t.Fatalf("the activity-pinned rule matched %d record(s) (%d valued) for a case that did not "+
			"run; another activity is covering for it", matched, len(values))
	}
	failed := false
	for _, result := range evaluateBudget(budget, withoutPlain) {
		if result.ID != "base-idle-live-heap" {
			continue
		}
		if result.Status != statusFailed {
			t.Fatalf("a missing activity's case was evaluated as %s, want failed (%s)", result.Status, result.Reason)
		}
		if !strings.Contains(result.Reason, "required evidence missing") {
			t.Fatalf("the failure does not say the evidence is missing: %q", result.Reason)
		}
		failed = true
	}
	if !failed {
		t.Fatal("base-idle-live-heap was not evaluated")
	}

	// And the traffic rules are the mirror image: they name activity=traffic, so they are answered
	// by the traffic run and only by it.
	traffic := ruleByID(t, budget, "traffic-verified")
	if got := activityKey(traffic.Activity); got != "traffic" {
		t.Fatalf("traffic-verified selects activity %q, want \"traffic\"", got)
	}
	if values, matched, _ := observedValues(traffic, full); len(values) != 1 || matched != 1 {
		t.Fatalf("traffic-verified matched %d record(s) with %d value(s), want 1 and 1", matched, len(values))
	}
}

// TestPerDenominatorMustBePositive is the second residual.
//
// A zero denominator was always refused; a NEGATIVE one was accepted, and dividing by it flipped the
// sign of the observation. A "cost per flow" rule with a negative denominator then compared a
// negative number against a positive ceiling, which cannot fail - the rule was green for any
// measurement, including an infinitely expensive one.
func TestPerDenominatorMustBePositive(t *testing.T) {
	full := syntheticGateLedger()
	budget, err := deriveBudget(full, budgetProvenance{Kind: provenanceDerived})
	if err != nil {
		t.Fatal(err)
	}
	rule := ruleByID(t, budget, "traffic-per-flow-live-heap")
	if rule.Per != "spec.held_flows" {
		t.Fatalf("the per-flow rule divides by %q", rule.Per)
	}

	for _, denominator := range []string{"0", "-1", "-4"} {
		denominator := denominator
		t.Run("held_flows="+denominator, func(t *testing.T) {
			broken := syntheticGateLedger()
			for index, record := range broken {
				if record.stage() != stageTrafficHeld {
					continue
				}
				spec := record["spec"].(map[string]any)
				spec["held_flows"] = json.Number(denominator)
				broken[index] = record
			}
			values, matched, missing := observedValues(rule, broken)
			if matched != 1 {
				t.Fatalf("the record was not selected (matched=%d), so the denominator was not reached", matched)
			}
			if len(values) != 0 || missing != 1 {
				t.Fatalf("a denominator of %s produced %d value(s) with %d missing; it must be refused",
					denominator, len(values), missing)
			}
			for _, result := range evaluateBudget(budget, broken) {
				if result.ID != rule.ID {
					continue
				}
				if result.Status != statusFailed {
					t.Fatalf("a denominator of %s was evaluated as %s, want failed", denominator, result.Status)
				}
			}
			// And the same ledger cannot be used to derive a threshold for the rule either: a
			// baseline that lost the denominator must not mint one.
			if _, err := deriveBudget(broken, budgetProvenance{Kind: provenanceDerived}); err == nil {
				t.Fatalf("a budget was derived from a ledger whose per-flow denominator is %s", denominator)
			}
		})
	}
}

// cleanTree is the live identity of a clean checkout at sha. It is a parameter of the identity
// checks rather than something the test probes, so every case below is independent of the tree the
// test happens to run in.
func cleanTree(sha string) liveIdentity {
	clean := false
	return liveIdentity{
		SHA: sha, Origin: liveOriginGit, Dirty: &clean, DirtyPaths: nil,
		BuildSHA: sha, BuildDirty: &clean, BuildInfo: true,
	}
}

// cleanLedger is a ledger that says it was measured on a clean checkout at sha, through git.
func cleanLedger(sha string) ledgerSummary {
	clean := false
	return ledgerSummary{
		SHA: sha, DetectedBy: detectedByGit, GitSHA: sha, GitDirty: &clean,
		BuildSHA: sha, BuildDirty: &clean, Dirty: false,
	}
}

// TestCandidateIdentityMustBeTheCheckout is the third residual.
//
// The candidate ledger must describe THIS checkout (on CI, the commit under test). The baseline's
// revision is a different matter and is tested separately: it is a fixed reference and is expected
// to be older.
//
// Every case below is a false pass that the first version of this check allowed: it compared the
// revision only, so a ledger could claim a clean tree (dirty=false) on a dirty checkout, claim
// detected_by=buildinfo with no build stamp to check it against, or name an unknown source.
func TestCandidateIdentityMustBeTheCheckout(t *testing.T) {
	head := strings.Repeat("c", 40)
	other := strings.Repeat("d", 40)
	dirty := true
	for _, testCase := range []struct {
		name     string
		summary  ledgerSummary
		live     liveIdentity
		explicit string
		wantOK   bool
		reason   string
	}{
		{"candidate is the checkout", cleanLedger(head), cleanTree(head), "", true, ""},
		{"candidate is another tree", func() ledgerSummary {
			// The binary is this checkout's; only the revision the ledger claims is different.
			summary := cleanLedger(other)
			summary.BuildSHA = head
			return summary
		}(), cleanTree(head), "", false, "this checkout is"},
		{"binary stamped elsewhere", func() ledgerSummary {
			summary := cleanLedger(head)
			summary.BuildSHA = other
			return summary
		}(), cleanTree(head), "", false, "different builds"},
		{"binary was built elsewhere", func() ledgerSummary {
			summary := cleanLedger(head)
			summary.BuildSHA = other
			return summary
		}(), cleanTree(head), "", false, "built from"},
		{"candidate has no revision", ledgerSummary{}, cleanTree(head), "", false, "no source revision"},

		// The live sources were unreadable and the caller asserted the revision: that is allowed,
		// and recorded as an assertion.
		{"no live source, assertion agrees", cleanLedger(head), liveIdentity{Origin: liveOriginNone}, head, true, ""},
		{"no live source, assertion differs", cleanLedger(head), liveIdentity{Origin: liveOriginNone}, other, false, "-source-sha says"},
		{"no live source, no assertion", cleanLedger(head), liveIdentity{Origin: liveOriginNone}, "", false, "cannot determine"},
		{"assertion contradicts live", cleanLedger(head), cleanTree(head), other, false, "contradicts"},

		// P1-1 negative 1: the ledger's dirty claim is checked against the live tree, not believed.
		{"ledger says clean, tree is dirty", cleanLedger(head), func() liveIdentity {
			live := cleanTree(head)
			live.Dirty = &dirty
			live.DirtyPaths = []string{"cmd/internal/memorystudy/verify.go"}
			return live
		}(), "", false, "dirty=false"},
		{"ledger says dirty, tree is clean", func() ledgerSummary {
			summary := cleanLedger(head)
			summary.Dirty = true
			summary.GitDirty = &dirty
			return summary
		}(), cleanTree(head), "", false, "dirty=true"},
		{"clean claim lists dirty paths", func() ledgerSummary {
			summary := cleanLedger(head)
			summary.DirtyPaths = []string{"some/file.go"}
			return summary
		}(), cleanTree(head), "", false, "lists"},

		// P1-1 negative 2: the claimed source of the revision must be checkable.
		{"buildinfo claim without a build stamp", func() ledgerSummary {
			summary := cleanLedger(head)
			summary.DetectedBy = detectedByBuildInfo
			return summary
		}(), func() liveIdentity {
			live := cleanTree(head)
			live.BuildInfo = false
			live.BuildSHA = ""
			live.BuildDirty = nil
			return live
		}(), "", false, "cannot be checked"},
		{"unknown detected_by", func() ledgerSummary {
			summary := cleanLedger(head)
			summary.DetectedBy = "somebody-told-me"
			return summary
		}(), cleanTree(head), "", false, "only accepts"},
		{"no detected_by", func() ledgerSummary {
			summary := cleanLedger(head)
			summary.DetectedBy = ""
			return summary
		}(), cleanTree(head), "", false, "does not say how"},
		{"declared without an assertion", func() ledgerSummary {
			summary := cleanLedger(head)
			summary.DetectedBy = detectedByDeclared
			summary.DeclaredOrigin = "git archive of the reference"
			summary.GitSHA = ""
			summary.GitDirty = nil
			return summary
		}(), cleanTree(head), "", false, "DECLARED"},
		{"declared with an assertion", func() ledgerSummary {
			summary := cleanLedger(head)
			summary.DetectedBy = detectedByDeclared
			summary.DeclaredOrigin = "git archive of the reference"
			summary.GitSHA = ""
			summary.GitDirty = nil
			return summary
		}(), cleanTree(head), head, true, ""},

		// The source block is checked for internal coherence, so a single edited field is visible.
		{"git_sha disagrees with sha", func() ledgerSummary {
			summary := cleanLedger(head)
			summary.GitSHA = other
			return summary
		}(), cleanTree(head), "", false, "incoherent"},
		{"git_dirty disagrees with dirty", func() ledgerSummary {
			summary := cleanLedger(head)
			summary.GitDirty = &dirty
			return summary
		}(), cleanTree(head), "", false, "disagrees with its source.dirty"},
		{"git_dirty missing", func() ledgerSummary {
			summary := cleanLedger(head)
			summary.GitDirty = nil
			return summary
		}(), cleanTree(head), "", false, "records no git_dirty"},
		{"dirty state unknown and unasserted", cleanLedger(head), func() liveIdentity {
			live := cleanTree(head)
			live.Dirty = nil
			return live
		}(), "", false, "not observable"},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			err := requireCandidateIdentity(testCase.summary, testCase.live, testCase.explicit)
			if testCase.wantOK {
				if err != nil {
					t.Fatalf("a matching candidate was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("a candidate that is not this checkout must be refused")
			}
			if !strings.Contains(err.Error(), testCase.reason) {
				t.Fatalf("the refusal does not explain the cause (%q): %v", testCase.reason, err)
			}
		})
	}
}

// TestToolchainTagFixtureAndPlatformMismatchesAreRefused is the rest of the third residual: the
// budget's thresholds are only comparable against the world they were derived in.
func TestToolchainTagFixtureAndPlatformMismatchesAreRefused(t *testing.T) {
	head := strings.Repeat("c", 40)
	baseline := budgetBaseline{
		SourceSHA:      strings.Repeat("b", 40),
		GOOS:           "windows",
		GOARCH:         "amd64",
		GoVersion:      "go1.27.4",
		Tags:           []string{"with_purego,with_quic"},
		FixtureVersion: fixtureVersion,
		Runs:           []string{"reference-run"},
	}
	provenance := &budgetProvenance{Kind: provenanceReference, LedgerSHA256: strings.Repeat("e", 64)}
	manifest := budgetFile{SchemaVersion: budgetSchemaVersion, Baseline: baseline, Provenance: provenance}
	summary := cleanLedger(head)
	summary.GoVersion = "go1.27.4"
	summary.GOOS = "windows"
	summary.GOARCH = "amd64"
	summary.Tags = []string{"with_quic", "with_purego"}
	summary.FixtureVersion = fixtureVersion
	summary.Runs = []string{"candidate-run"}
	if _, err := verifyGateInputsAgainst(manifest, summary, false, cleanTree(head), ""); err != nil {
		t.Fatalf("a compatible budget was refused: %v", err)
	}

	// The baseline revision differs from the candidate's in the control above, which is the normal
	// case and must not be an error; the warning list says why when they are equal.
	equal := manifest
	equal.Baseline.SourceSHA = head
	verification, err := verifyGateInputsAgainst(equal, summary, false, cleanTree(head), "")
	if err != nil {
		t.Fatalf("a budget captured at the candidate's revision was refused outright: %v", err)
	}
	if !verification.BaselineIsCandidate {
		t.Fatal("a budget captured at the candidate's revision was not reported as such")
	}
	found := false
	for _, warning := range verification.Warnings {
		if strings.Contains(warning, "cannot detect a regression introduced at this revision") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the report does not warn that the reference cannot see a regression at its own revision: %v",
			verification.Warnings)
	}

	for _, testCase := range []struct {
		name   string
		mutate func(*budgetFile, *ledgerSummary)
		reason string
	}{
		{"toolchain", func(b *budgetFile, s *ledgerSummary) { s.GoVersion = "go1.26.1" }, "incompatible toolchain"},
		{"build tags", func(b *budgetFile, s *ledgerSummary) { s.Tags = []string{"with_purego"} }, "incompatible build tags"},
		{"platform", func(b *budgetFile, s *ledgerSummary) { s.GOOS, s.GOARCH = "linux", "amd64" }, "incompatible platform"},
		{"fixture version", func(b *budgetFile, s *ledgerSummary) { s.FixtureVersion = "memorystudy-fixture/2" }, "incompatible fixture"},
		{"fixture document", func(b *budgetFile, s *ledgerSummary) {
			b.Provenance.FixtureDigests = map[string]string{specSignature(gateSpecs(false)[0]): "aaaa"}
			s.FixtureDigests = map[string]string{specSignature(gateSpecs(false)[0]): "bbbb"}
		}, "other document"},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			brokenBudget := manifest
			brokenSummary := summary
			provenanceCopy := *provenance
			brokenBudget.Provenance = &provenanceCopy
			brokenBudget.Provenance.FixtureDigests = nil
			testCase.mutate(&brokenBudget, &brokenSummary)
			_, err := verifyGateInputsAgainst(brokenBudget, brokenSummary, false, cleanTree(head), "")
			if err == nil {
				t.Fatalf("a %s mismatch was compared instead of refused", testCase.name)
			}
			if !strings.Contains(err.Error(), testCase.reason) {
				t.Fatalf("the refusal does not name the %s mismatch (%q): %v", testCase.name, testCase.reason, err)
			}
		})
	}
}

// TestBudgetDerivedFromTheCandidateCannotCertifyIt is the fifth residual.
//
// The whitewash: run the matrix, feed that ledger to -write-budget, and gate the same ledger with
// the budget derived from it. Every rule passes by construction. Two independent refusals exist: a
// DERIVED budget is never a certification reference, and a reference budget whose provenance run ids
// ARE the candidate's is circular (the explicit -self-check exists only to verify the archived
// reference against its own budget).
func TestBudgetDerivedFromTheCandidateCannotCertifyIt(t *testing.T) {
	head := strings.Repeat("c", 40)
	runs := []string{"run-1", "run-2"}
	summary := cleanLedger(head)
	summary.GoVersion = "go1.27.4"
	summary.GOOS = "windows"
	summary.GOARCH = "amd64"
	summary.Tags = []string{"with_purego"}
	summary.FixtureVersion = fixtureVersion
	summary.Runs = runs
	baseline := budgetBaseline{
		SourceSHA: head, GOOS: "windows", GOARCH: "amd64", GoVersion: "go1.27.4",
		Tags: []string{"with_purego"}, FixtureVersion: fixtureVersion, Runs: runs,
	}
	derived := budgetFile{SchemaVersion: budgetSchemaVersion,
		Baseline:   baseline,
		Provenance: &budgetProvenance{Kind: provenanceDerived, LedgerSHA256: strings.Repeat("f", 64)},
	}
	if _, err := verifyGateInputsAgainst(derived, summary, false, cleanTree(head), ""); err == nil {
		t.Fatal("a budget derived from a ledger (provenance=derived) certified a candidate")
	} else if !strings.Contains(err.Error(), "derived") {
		t.Fatalf("the refusal does not explain that the budget is derived: %v", err)
	}
	if _, err := verifyGateInputsAgainst(derived, summary, true, cleanTree(head), ""); err == nil {
		t.Fatal("-self-check let a derived budget through; it exists for the reference ledger only")
	}

	// Even when marked as a reference, a budget whose provenance runs are the candidate's is the
	// same evidence judged by itself.
	circular := budgetFile{SchemaVersion: budgetSchemaVersion,
		Baseline:   baseline,
		Provenance: &budgetProvenance{Kind: provenanceReference, LedgerSHA256: strings.Repeat("f", 64)},
	}
	if _, err := verifyGateInputsAgainst(circular, summary, false, cleanTree(head), ""); err == nil {
		t.Fatal("a reference budget derived from the candidate's own ledger certified it")
	} else if !strings.Contains(err.Error(), "circular") {
		t.Fatalf("the refusal does not explain the circularity: %v", err)
	}
	// The self-check is the explicit way to verify the archived reference against its own budget.
	if _, err := verifyGateInputsAgainst(circular, summary, true, cleanTree(head), ""); err != nil {
		t.Fatalf("the explicit self-check of the reference ledger was refused: %v", err)
	}

	// A genuine reference (different evidence) is accepted, even at the same revision.
	genuine := budgetFile{SchemaVersion: budgetSchemaVersion,
		Baseline:   baseline,
		Provenance: &budgetProvenance{Kind: provenanceReference, LedgerSHA256: strings.Repeat("a", 64)},
	}
	genuine.Baseline.Runs = []string{"older-1", "older-2"}
	if _, err := verifyGateInputsAgainst(genuine, summary, false, cleanTree(head), ""); err != nil {
		t.Fatalf("a reference budget from a different session was refused: %v", err)
	}
}

// writeLedger writes records as JSONL, the shape the tool reads back.
func writeLedger(t *testing.T, path string, records []recordMap) {
	t.Helper()
	var builder strings.Builder
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		builder.Write(encoded)
		builder.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWriteBudgetRunsTheSameCompletenessCheckAsTheGate is the fourth residual.
//
// -write-budget accepted any JSONL, so the thresholds could be derived from evidence the gate itself
// would have refused: a ledger that lost a run_end, one repetition instead of three, or a stage. The
// budget was then the weak link in the chain. Both entry points now share requireCompleteLedger.
func TestWriteBudgetRunsTheSameCompletenessCheckAsTheGate(t *testing.T) {
	directory := t.TempDir()
	ledgerPath := filepath.Join(directory, "ledger.jsonl")

	build := func(mutate func([]recordMap) []recordMap) {
		current, _ := completeSession(t, 1)
		writeLedger(t, ledgerPath, mutate(current.records))
	}

	// The control: a complete ledger passes the same check through -write-budget.
	build(func(records []recordMap) []recordMap { return records })
	flags := &runFlags{from: ledgerPath, writeBudget: filepath.Join(directory, "budget.json"), runs: 1}
	if err := runWriteBudget(flags); err == nil {
		t.Fatal("the control ledger produced no error, but it has no metric values: derivation must " +
			"fail rather than mint thresholds - the point is that it failed AFTER the completeness check")
	}

	cases := []struct {
		name   string
		mutate func([]recordMap) []recordMap
		reason string
	}{
		{"missing run_end", func(records []recordMap) []recordMap {
			var kept []recordMap
			for _, record := range records {
				if record.stage() == stageRunEnd && record.runID() == runTestID(0, 0) {
					continue
				}
				kept = append(kept, record)
			}
			return kept
		}, "run_end record"},
		{"missing repetition", func(records []recordMap) []recordMap {
			return records[:len(records)-len(records)/len(gateSpecs(false))]
		}, "distinct spec"},
		{"missing stage", func(records []recordMap) []recordMap {
			var kept []recordMap
			for _, record := range records {
				if record.runID() == runTestID(0, 2) && record.stage() == stageConstruct {
					continue
				}
				kept = append(kept, record)
			}
			return kept
		}, "incomplete evidence"},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			build(testCase.mutate)
			flags := &runFlags{from: ledgerPath, writeBudget: filepath.Join(directory, "budget.json"), runs: 1}
			err := runWriteBudget(flags)
			if err == nil {
				t.Fatalf("a ledger with %s produced a budget", testCase.name)
			}
			if !strings.Contains(err.Error(), testCase.reason) {
				t.Fatalf("the refusal does not name the problem (%q): %v", testCase.reason, err)
			}
		})
	}
}

// TestWriteBudgetWithoutAManifestMintsOnlyADerivedBudget: -from is an audit convenience, not a way
// to promote the candidate ledger into a reference.
func TestWriteBudgetWithoutAManifestMintsOnlyADerivedBudget(t *testing.T) {
	summary := ledgerSummary{SHA: strings.Repeat("c", 40), GoVersion: "go1.27.4", GOOS: "windows",
		GOARCH: "amd64", Tags: []string{"with_purego"}, FixtureVersion: fixtureVersion,
		FixtureDigests: map[string]string{}}
	planned := gateSpecs(false)
	manifest := manifestFromLedger(summary, 18, "reference.jsonl", strings.Repeat("a", 64), 3, planned)
	if manifest.Kind != provenanceReference || manifest.Repetitions != 3 || len(manifest.Specs) != len(planned) {
		t.Fatalf("the manifest does not describe the capture: %+v", manifest)
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := writeManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
	read, err := readManifest(path)
	if err != nil {
		t.Fatalf("a manifest this tool wrote could not be read back: %v", err)
	}
	if read.LedgerSHA256 != manifest.LedgerSHA256 {
		t.Fatalf("the manifest round trip lost the ledger hash: %q", read.LedgerSHA256)
	}
	// A manifest of a different kind is not a reference, whatever the file says.
	read.Kind = provenanceDerived
	if err := writeManifest(path, read); err != nil {
		t.Fatal(err)
	}
	if _, err = readManifest(path); err == nil {
		t.Fatal("a manifest that declares kind=derived was accepted as a reference")
	}
}

// TestSummariseLedgerReadsTheSourceBlock is the reader half of the identity check.
//
// The gate decides on what the JSONL says, so the source block has to survive the decode: the
// revision, the dirty flag, HOW the revision was determined, and the working-tree evidence. A field
// that the writer emits and the reader drops is a check that silently cannot run.
func TestSummariseLedgerReadsTheSourceBlock(t *testing.T) {
	summary, err := summariseLedger(syntheticGateLedger())
	if err != nil {
		t.Fatal(err)
	}
	if summary.SHA != testLedgerSHA {
		t.Fatalf("source sha = %q", summary.SHA)
	}
	if summary.DetectedBy != detectedByGit {
		t.Fatalf("detected_by = %q, want %q", summary.DetectedBy, detectedByGit)
	}
	if summary.GitSHA != testLedgerSHA {
		t.Fatalf("git_sha = %q", summary.GitSHA)
	}
	if summary.GitDirty == nil || *summary.GitDirty {
		t.Fatalf("git_dirty = %v, want a recorded false", summary.GitDirty)
	}
	if summary.Dirty {
		t.Fatal("a clean ledger was summarised as dirty")
	}
	if len(summary.DirtyPaths) != 0 {
		t.Fatalf("a clean ledger carries changed paths: %v", summary.DirtyPaths)
	}
	if summary.BuildSHA != testLedgerSHA {
		t.Fatalf("build_sha = %q", summary.BuildSHA)
	}
}

// TestSummariseLedgerRefusesAMixedIdentity: one ledger is one measurement. Two revisions, two
// toolchains or two fixtures inside one file cannot be resolved to a verdict.
func TestSummariseLedgerRefusesAMixedIdentity(t *testing.T) {
	ledger := syntheticGateLedger()
	if _, err := summariseLedger(ledger); err != nil {
		t.Fatalf("a homogeneous ledger was refused: %v", err)
	}
	broken := syntheticGateLedger()
	for _, record := range broken {
		if record.stage() == stageIdleZero() {
			// One record claims a different Go version.
			build := record["identity"].(map[string]any)["build"].(map[string]any)
			build["go_version"] = fmt.Sprintf("go0.0.%d", len(record["identity"].(map[string]any)))
			break
		}
	}
	if _, err := summariseLedger(broken); err == nil {
		t.Fatal("a ledger that mixes two toolchains was summarised")
	}
}

// stageIdleZero names the idle checkpoint the synthetic ledger always records.
func stageIdleZero() string { return idleStage("0s") }
