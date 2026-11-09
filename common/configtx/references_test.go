package configtx

// C2 — references are persisted with the document revision that used them.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReferencesArePersistedWithTheirRevision(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory, Options{})
	require.NoError(t, err)

	first := []byte(`{"log":{"level":"error"}}`)
	stagedFirst, err := store.Stage(first)
	require.NoError(t, err)
	firstReferences := map[string]string{"gate-abc": "digest-a"}
	result, err := store.CommitWithReferences(stagedFirst, firstReferences)
	require.NoError(t, err)
	require.True(t, result.Persisted)
	require.Empty(t, result.Warning)

	stored, err := store.References(stagedFirst.Revision)
	require.NoError(t, err)
	require.Equal(t, firstReferences, stored)

	// A second configuration must not erase the first revision's record.
	second := []byte(`{"log":{"level":"warn"}}`)
	stagedSecond, err := store.Stage(second)
	require.NoError(t, err)
	secondReferences := map[string]string{"gate-abc": "digest-b"}
	_, err = store.CommitWithReferences(stagedSecond, secondReferences)
	require.NoError(t, err)
	stored, err = store.References(stagedFirst.Revision)
	require.NoError(t, err)
	require.Equal(t, firstReferences, stored)
	stored, err = store.References(stagedSecond.Revision)
	require.NoError(t, err)
	require.Equal(t, secondReferences, stored)

	// A fresh store handle reads the same sidecar: this is state, not memory.
	reopened, err := Open(directory, Options{})
	require.NoError(t, err)
	stored, err = reopened.References(stagedFirst.Revision)
	require.NoError(t, err)
	require.Equal(t, firstReferences, stored)
}

func TestAFailedReferenceWriteKeepsTheConfigurationAndReportsAWarning(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory, Options{
		WriteFile: func(path string, content []byte) error {
			if filepath.Base(path) == referencesName {
				return errors.New("injected: references are not writable")
			}
			return os.WriteFile(path, content, 0o644)
		},
	})
	require.NoError(t, err)

	profile := []byte(`{"log":{"level":"error"}}`)
	staged, err := store.Stage(profile)
	require.NoError(t, err)
	result, err := store.CommitWithReferences(staged, map[string]string{"gate": "digest"})
	require.NoError(t, err)
	require.True(t, result.Persisted, "the document reached the disk; only the pin is degraded")
	require.Contains(t, result.Warning, "references were not saved")
	active, err := store.Active()
	require.NoError(t, err)
	require.Equal(t, profile, active)
	references, err := store.References(staged.Revision)
	require.NoError(t, err)
	require.Empty(t, references)
}
