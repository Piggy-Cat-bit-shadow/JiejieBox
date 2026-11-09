package configtx

// 09 — a REAL process death between the legacy active write and the commit point.
//
// The child process runs this package's own test binary, writes active.json, and exits immediately
// with os.Exit, so no defer and no in-process cleanup runs. The parent reopens the directory and
// must not promote the uncommitted candidate.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	crashHelperEnv = "CONFIGTX_CRASH_HELPER"
	crashExitCode  = 23
)

func TestConfigtxCrashHelper(t *testing.T) {
	if os.Getenv(crashHelperEnv) != "1" {
		t.Skip("helper process only")
	}
	directory := os.Getenv("CONFIGTX_CRASH_DIRECTORY")
	candidate := []byte(os.Getenv("CONFIGTX_CRASH_CANDIDATE"))
	store, err := Open(directory, Options{
		WriteFile: func(path string, content []byte) error {
			if err := os.WriteFile(path, content, 0o600); err != nil {
				return err
			}
			if filepath.Base(path) == activeName && string(content) == string(candidate) {
				// The process dies here: no defer, no cleanup, exactly like a kill.
				os.Exit(crashExitCode)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	staged, err := store.Stage(candidate)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = store.CommitWithReferences(staged, map[string]string{"gate": "digest-new"})
	t.Fatal("the helper should have exited before returning")
}

func runCrashHelper(t *testing.T, directory string, candidate []byte) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=TestConfigtxCrashHelper", "-test.count=1")
	command.Env = append(os.Environ(),
		crashHelperEnv+"=1",
		"CONFIGTX_CRASH_DIRECTORY="+directory,
		"CONFIGTX_CRASH_CANDIDATE="+string(candidate),
	)
	output, err := command.CombinedOutput()
	require.Error(t, err, "the helper process must die before returning: %s", string(output))
	exitError, isExit := err.(*exec.ExitError)
	require.True(t, isExit, "the helper must exit with a status: %v: %s", err, string(output))
	require.Equal(t, crashExitCode, exitError.ExitCode(), "helper output: %s", string(output))
}

func TestProcessDeathBeforeTheFirstCommitPointLeavesNoConfiguration(t *testing.T) {
	directory := t.TempDir()
	candidate := []byte(`{"log":{"level":"error"}}`)
	runCrashHelper(t, directory, candidate)

	_, err := os.Stat(filepath.Join(directory, commitName))
	require.ErrorIs(t, err, os.ErrNotExist, "the commit point was never published")
	recovery := recoverFresh(t, directory)
	require.Empty(t, recovery.Active)
	require.True(t, recovery.ActiveCleared)
	_, err = os.Stat(filepath.Join(directory, commitCandidateName))
	require.ErrorIs(t, err, os.ErrNotExist, "the candidate marker is consumed by recovery")
}

func TestProcessDeathDuringLegacyUpgradeKeepsTheLegacyDocument(t *testing.T) {
	directory := t.TempDir()
	legacy := []byte(`{"log":{"level":"warn"}}`)
	require.NoError(t, os.WriteFile(filepath.Join(directory, activeName), legacy, 0o600))
	legacyReferences := map[string]string{"gate": "digest-old"}
	writeLegacyReferences(t, directory, legacy, legacyReferences)

	candidate := []byte(`{"log":{"level":"error"}}`)
	runCrashHelper(t, directory, candidate)

	recovery := recoverFresh(t, directory)
	require.True(t, recovery.RolledBack)
	require.Equal(t, legacy, recovery.Active)
	require.Equal(t, legacyReferences, recovery.References)
}
