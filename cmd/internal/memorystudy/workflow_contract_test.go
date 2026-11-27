package main

// The CI gate's FAILURE-PROPAGATION contract, asserted against the workflow that owns it.
//
// `cmd/internal/memorystudy`'s unit tests prove the evaluator can fail. They cannot prove that a CI
// run turns that failure into a failed job, because the propagation lives in the workflow: a `throw`
// after a non-zero exit, an artifact upload marked `if: always()`, and the `if-no-files-found` policy
// that decides whether a run which wrote NOTHING is green or red.
//
// The independent review found exactly that hole: the upload step used the default `warn`, and the gate
// can refuse before it writes its report (missing run, truncated ledger, mixed identity). Such a run
// produced no artifacts and still reported success. These assertions pin the fixed contract, and they
// are read-only reviewable: no workflow is dispatched and no network is used.

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
	Name string            `yaml:"name"`
	If   string            `yaml:"if"`
	Run  string            `yaml:"run"`
	Uses string            `yaml:"uses"`
	With map[string]string `yaml:"with"`
}

type workflowJob struct {
	Name            string         `yaml:"name"`
	ContinueOnError bool           `yaml:"continue-on-error"`
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
	var document workflowDocument
	require.NoError(t, yaml.Unmarshal(content, &document), "the gate workflow must parse")
	return document, string(content)
}

func findStep(t *testing.T, job workflowJob, name string) workflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("the memory-budget job has no step named %q", name)
	return workflowStep{}
}

// TestTheGateWorkflowFailsTheJobWhenTheGateRefuses is the `throw` half of propagation.
func TestTheGateWorkflowFailsTheJobWhenTheGateRefuses(t *testing.T) {
	document, _ := readGateWorkflow(t)
	job, loaded := document.Jobs["memory-budget"]
	require.True(t, loaded, "the memory-budget job must exist")
	step := findStep(t, job, "evaluate the budget")
	require.Contains(t, step.Run, "-gate", "the step must run the gate")
	require.Contains(t, step.Run, "$LASTEXITCODE", "the step must read the gate's real exit code")
	require.Contains(t, step.Run, "throw", "a non-zero gate must throw, not be swallowed")
	require.Contains(t, step.Run, "memory-gate.exit.txt",
		"the exit code must be written down BEFORE the throw, so a pre-report refusal still leaves evidence")
}

// TestAMissingArtifactFailsTheJob is the upload half: silent emptiness must be red.
func TestAMissingArtifactFailsTheJob(t *testing.T) {
	document, _ := readGateWorkflow(t)
	job, loaded := document.Jobs["memory-budget"]
	require.True(t, loaded)
	var upload *workflowStep
	for index := range job.Steps {
		if strings.HasPrefix(job.Steps[index].Uses, "actions/upload-artifact") {
			upload = &job.Steps[index]
			break
		}
	}
	require.NotNil(t, upload, "the memory-budget job must upload its evidence")
	require.Equal(t, "always()", strings.TrimSpace(upload.If),
		"the upload must run even when the gate failed, otherwise the diagnosis is lost")
	require.Equal(t, "error", upload.With["if-no-files-found"],
		"a run that produced no evidence must fail; `warn` lets a silent run stay green")
	require.Contains(t, upload.With["path"], "memory-gate.exit.txt",
		"the uploaded set must include the exit marker written before the failure is thrown")
}

// TestTheJobIsNotAllowedToFail keeps the two assertions above from being neutralised one level up.
func TestTheJobIsNotAllowedToFail(t *testing.T) {
	document, _ := readGateWorkflow(t)
	job, loaded := document.Jobs["memory-budget"]
	require.True(t, loaded)
	require.False(t, job.ContinueOnError,
		"continue-on-error would report a failed gate as a successful job")
	for _, step := range job.Steps {
		require.NotEqual(t, "continue-on-error", strings.TrimSpace(step.If))
	}
}
