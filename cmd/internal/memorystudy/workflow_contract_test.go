package main

// The CI gate's FAILURE-PROPAGATION contract, asserted against the workflow that owns it.
//
// `cmd/internal/memorystudy`'s unit tests prove the evaluator can fail. They cannot prove that a CI
// run turns that failure into a failed job, because the propagation lives in the workflow: a `throw`
// after a non-zero exit, an artifact upload marked `if: always()`, and the `if-no-files-found` policy
// that decides whether a run which wrote NOTHING is green or red.
//
// # Why the assertions are functions over a DOCUMENT plus a MUTATION sweep
//
// Independent review showed that a test which only looks for substrings can pass a workflow that has
// regressed: moving the diagnosis marker AFTER the `throw` still contained both strings, and adding
// `continue-on-error: true` to the gate step still passed. Both mutations silently destroy the
// property being tested. So the contract is expressed as checks over (document, raw text), and the
// tests below run deliberate mutations through the SAME checks and require them to fail. A check that
// cannot reject its own mutation is not a check.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const workflowPath = "../../../.github/workflows/windows-boxdd-tests.yml"

type workflowStep struct {
	Name            string            `yaml:"name"`
	If              string            `yaml:"if"`
	Run             string            `yaml:"run"`
	Uses            string            `yaml:"uses"`
	ContinueOnError *bool             `yaml:"continue-on-error"`
	With            map[string]string `yaml:"with"`
}

type workflowJob struct {
	Name            string         `yaml:"name"`
	ContinueOnError *bool          `yaml:"continue-on-error"`
	Steps           []workflowStep `yaml:"steps"`
}

type workflowDocument struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

func readGateWorkflow(t *testing.T) (workflowDocument, string) {
	t.Helper()
	path, err := filepath.Abs(workflowPath)
	require.NoError(t, err)
	content, err := os.ReadFile(path)
	require.NoError(t, err, "the gate workflow must exist at %s", path)
	// Normalise line endings: a Windows checkout can hand this file CRLF while the mutation anchors
	// below are written with LF. The contract is about the YAML's content, not its encoding, and a
	// platform-dependent test is a test that fails on the platform the gate actually runs on.
	raw := strings.ReplaceAll(string(content), "\r\n", "\n")
	var document workflowDocument
	require.NoError(t, yaml.Unmarshal([]byte(raw), &document), "the gate workflow must parse")
	return document, raw
}

func memoryBudgetJob(document workflowDocument) (workflowJob, bool) {
	job, loaded := document.Jobs["memory-budget"]
	return job, loaded
}

func stepNamed(job workflowJob, name string) (workflowStep, bool) {
	for _, step := range job.Steps {
		if step.Name == name {
			return step, true
		}
	}
	return workflowStep{}, false
}

// contractProblems returns every violation in the document. It never calls t.Fatal, so the mutation
// tests can run it over a doctored document and inspect the result.
func contractProblems(document workflowDocument, raw string) []string {
	var problems []string
	job, loaded := memoryBudgetJob(document)
	if !loaded {
		return append(problems, "the memory-budget job is missing")
	}
	if job.ContinueOnError != nil && *job.ContinueOnError {
		problems = append(problems, "the memory-budget job sets continue-on-error")
	}
	for _, step := range job.Steps {
		if step.ContinueOnError != nil && *step.ContinueOnError {
			problems = append(problems, "step "+step.Name+" sets continue-on-error")
		}
	}

	gate, found := stepNamed(job, "evaluate the budget")
	if !found {
		return append(problems, "the evaluate-the-budget step is missing")
	}
	if !strings.Contains(gate.Run, "-gate") {
		problems = append(problems, "the gate step does not run -gate")
	}
	if !strings.Contains(gate.Run, "$LASTEXITCODE") {
		problems = append(problems, "the gate step does not read the real exit code")
	}
	if !strings.Contains(gate.Run, "throw") {
		problems = append(problems, "a non-zero gate is not turned into a failure")
	}
	markerLine, throwLine := -1, -1
	for index, line := range strings.Split(gate.Run, "\n") {
		trimmed := strings.TrimSpace(line)
		if markerLine < 0 && strings.Contains(line, "Set-Content") && strings.Contains(line, "memory-gate.exit.txt") {
			markerLine = index
		}
		// A THROW STATEMENT starts the line; the word inside a comment must not count, or a comment
		// mentioning "thrown" would be read as the failure path.
		if throwLine < 0 && strings.HasPrefix(trimmed, "throw") {
			throwLine = index
		}
	}
	if markerLine < 0 {
		problems = append(problems, "the gate step does not write the exit marker")
	} else if throwLine >= 0 && markerLine > throwLine {
		problems = append(problems, "the exit marker is written AFTER the throw, so a refused gate leaves no diagnosis")
	}

	var upload *workflowStep
	for index := range job.Steps {
		if strings.HasPrefix(job.Steps[index].Uses, "actions/upload-artifact") {
			upload = &job.Steps[index]
			break
		}
	}
	if upload == nil {
		return append(problems, "the memory-budget job does not upload its evidence")
	}
	if strings.TrimSpace(upload.If) != "always()" {
		problems = append(problems, "the upload step does not run when the gate failed")
	}
	if upload.With["if-no-files-found"] != "error" {
		problems = append(problems, "a run that produced no evidence is not required to fail")
	}
	if !strings.Contains(upload.With["path"], "memory-gate.exit.txt") {
		problems = append(problems, "the uploaded set does not include the exit marker")
	}
	return problems
}

// TestTheGateWorkflowKeepsItsFailureContract runs the checks over the real workflow.
func TestTheGateWorkflowKeepsItsFailureContract(t *testing.T) {
	document, raw := readGateWorkflow(t)
	require.Empty(t, contractProblems(document, raw), "the gate workflow must keep its failure-propagation contract")
}

// mutation applies one textual change and returns the re-parsed document plus the new text.
func mutate(t *testing.T, raw string, old string, new string) (workflowDocument, string) {
	t.Helper()
	require.Contains(t, raw, old, "the mutation anchor must exist; otherwise the test proves nothing")
	mutated := strings.Replace(raw, old, new, 1)
	var document workflowDocument
	require.NoError(t, yaml.Unmarshal([]byte(mutated), &document))
	return document, mutated
}

// TestTheContractRejectsADiagnosisMovedAfterTheThrow is the review's first mutant: a marker written
// after the throw is never reached, so a refused gate leaves the reviewer with nothing.
func TestTheContractRejectsADiagnosisMovedAfterTheThrow(t *testing.T) {
	document, raw := readGateWorkflow(t)
	mutated, text := mutate(t, raw,
		"          \"gate_exit=$gateExit\" | Set-Content -LiteralPath \"$env:RUNNER_TEMP\\memory-gate.exit.txt\" -Encoding utf8\n          if ($gateExit -ne 0) {",
		"          if ($gateExit -ne 0) {")
	mutatedAfterThrow, _ := mutate(t, text,
		"          throw \"the memory budget gate failed with exit code $gateExit; the ledger and its verdict are attached to this run\"\n",
		"          throw \"the memory budget gate failed with exit code $gateExit; the ledger and its verdict are attached to this run\"\n          \"gate_exit=$gateExit\" | Set-Content -LiteralPath \"$env:RUNNER_TEMP\\memory-gate.exit.txt\" -Encoding utf8\n")
	require.Empty(t, contractProblems(document, raw))
	problems := contractProblems(mutatedAfterThrow, text)
	require.NotEmpty(t, problems, "moving the exit marker after the throw must be rejected")
	t.Logf("mutant rejected: %v", problems)
	_ = mutated
}

// TestTheContractRejectsAContinueOnErrorGate is the review's second mutant: marking the step
// continue-on-error keeps the diagnosis but reports the job green.
func TestTheContractRejectsAContinueOnErrorGate(t *testing.T) {
	_, raw := readGateWorkflow(t)
	document, text := mutate(t, raw,
		"      - name: evaluate the budget\n        shell: pwsh\n",
		"      - name: evaluate the budget\n        continue-on-error: true\n        shell: pwsh\n")
	problems := contractProblems(document, text)
	require.NotEmpty(t, problems, "a continue-on-error gate must be rejected")
	t.Logf("mutant rejected: %v", problems)
}

// TestTheContractRejectsAWarnUpload is the third mutant: the original defect, back again.
func TestTheContractRejectsAWarnUpload(t *testing.T) {
	_, raw := readGateWorkflow(t)
	document, text := mutate(t, raw,
		"          if-no-files-found: error\n",
		"          if-no-files-found: warn\n")
	problems := contractProblems(document, text)
	require.NotEmpty(t, problems, "a warn-on-missing-artifact upload must be rejected")
	t.Logf("mutant rejected: %v", problems)
}

// TestTheContractRejectsAMissingUploadPath is the fourth mutant: the marker is written but never
// uploaded, so the reviewer still cannot see it.
func TestTheContractRejectsAMissingUploadPath(t *testing.T) {
	_, raw := readGateWorkflow(t)
	document, text := mutate(t, raw,
		"            ${{ runner.temp }}/memory-gate.exit.txt\n",
		"")
	problems := contractProblems(document, text)
	require.NotEmpty(t, problems, "an upload that omits the exit marker must be rejected")
	t.Logf("mutant rejected: %v", problems)
}
