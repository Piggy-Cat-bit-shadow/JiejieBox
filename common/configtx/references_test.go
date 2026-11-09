package configtx

// C2/1B — configuration, references and the recovery record.

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

	// The combined record is what recovery reads, and it names the same pair.
	recovery, err := store.Recover()
	require.NoError(t, err)
	require.True(t, recovery.Combined)
	require.Equal(t, first, recovery.Active)
	require.Equal(t, firstReferences, recovery.References)

	// A second configuration must not erase the first revision's legacy record.
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

	// A fresh store handle reads the same record: this is state, not memory.
	reopened, err := Open(directory, Options{})
	require.NoError(t, err)
	recovered, err := reopened.Recover()
	require.NoError(t, err)
	require.Equal(t, second, recovered.Active)
	require.Equal(t, secondReferences, recovered.References)
}

func TestTheSameDocumentWithNewReferencesIsANewCombination(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory, Options{})
	require.NoError(t, err)

	profile := []byte(`{"log":{"level":"error"}}`)
	staged, err := store.Stage(profile)
	require.NoError(t, err)
	firstReferences := map[string]string{"gate": "digest-a"}
	_, err = store.CommitWithReferences(staged, firstReferences)
	require.NoError(t, err)

	// The document text is identical; only the dependency version changed.
	stagedAgain, err := store.Stage(profile)
	require.NoError(t, err)
	secondReferences := map[string]string{"gate": "digest-b"}
	_, err = store.CommitWithReferences(stagedAgain, secondReferences)
	require.NoError(t, err)

	recovery, err := store.Recover()
	require.NoError(t, err)
	require.Equal(t, profile, recovery.Active)
	require.Equal(t, secondReferences, recovery.References, "the new combination wins")
	require.Equal(t, staged.Revision, stagedAgain.Revision, "the document revision is unchanged")
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
	require.False(t, result.Persisted, "the combination did not commit")
	require.Contains(t, result.Warning, "references were not saved")
	_, err = store.Active()
	require.Error(t, err, "the disk must still point at the previous combination, which is empty here")
	references, err := store.References(staged.Revision)
	require.NoError(t, err)
	require.Empty(t, references)

	// A later successful commit (with a writable references file) works.
	working, err := Open(directory, Options{})
	require.NoError(t, err)
	staged, err = working.Stage(profile)
	require.NoError(t, err)
	result, err = working.CommitWithReferences(staged, map[string]string{"gate": "digest"})
	require.NoError(t, err)
	require.True(t, result.Persisted)
}

func TestACommitPointThatCannotBePublishedKeepsThePreviousCombination(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory, Options{})
	require.NoError(t, err)

	first := []byte(`{"log":{"level":"error"}}`)
	stagedFirst, err := store.Stage(first)
	require.NoError(t, err)
	firstReferences := map[string]string{"gate": "digest-a"}
	_, err = store.CommitWithReferences(stagedFirst, firstReferences)
	require.NoError(t, err)

	// A directory where the record file belongs: the atomic rename cannot replace it.
	require.NoError(t, os.Remove(filepath.Join(directory, commitName)))
	require.NoError(t, os.Mkdir(filepath.Join(directory, commitName), 0o755))

	second := []byte(`{"log":{"level":"warn"}}`)
	stagedSecond, err := store.Stage(second)
	require.NoError(t, err)
	secondReferences := map[string]string{"gate": "digest-b"}
	result, err := store.CommitWithReferences(stagedSecond, secondReferences)
	require.NoError(t, err)
	require.False(t, result.Persisted)
	require.Contains(t, result.Warning, "commit record was not published")

	// The legacy active was restored to the previous complete combination, and B's references
	// must not have survived the failed commit.
	active, err := store.Active()
	require.NoError(t, err)
	require.Equal(t, first, active)
	stored, err := store.References(stagedSecond.Revision)
	require.NoError(t, err)
	require.Empty(t, stored)

	// A recovery cannot read the damaged record and falls back to the legacy files with a warning.
	require.NoError(t, os.Remove(filepath.Join(directory, commitName)))
	require.NoError(t, os.Mkdir(filepath.Join(directory, commitName), 0o755))
	recovery, err := store.Recover()
	require.NoError(t, err)
	require.False(t, recovery.Combined)
	require.Equal(t, first, recovery.Active)
	require.NotEmpty(t, recovery.Warning)
}

func TestACorruptCombinationRecordFallsBackToTheLegacyActive(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory, Options{})
	require.NoError(t, err)
	profile := []byte(`{"log":{"level":"error"}}`)
	staged, err := store.Stage(profile)
	require.NoError(t, err)
	_, err = store.CommitWithReferences(staged, map[string]string{"gate": "digest"})
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(directory, commitName), []byte("{not json"), 0o644))
	recovery, err := store.Recover()
	require.NoError(t, err)
	require.False(t, recovery.Combined)
	require.Equal(t, profile, recovery.Active)
	require.NotEmpty(t, recovery.Warning)
}

func TestCorruptCurrentCombinationFallsBackToThePreviousOneWithItsReferences(t *testing.T) {
	directory := t.TempDir()
	validatorCalls := 0
	store, err := Open(directory, Options{
		Validator: func(content []byte) error {
			validatorCalls++
			if string(content) == "broken" {
				return errors.New("invalid")
			}
			return nil
		},
	})
	require.NoError(t, err)

	first := []byte("first")
	stagedFirst, err := store.Stage(first)
	require.NoError(t, err)
	firstReferences := map[string]string{"gate": "digest-a"}
	_, err = store.CommitWithReferences(stagedFirst, firstReferences)
	require.NoError(t, err)

	// Stage a document that validation will reject after a restart: the validator is switched by
	// writing through a second store that accepts it, then the original store recovers.
	accepting, err := Open(directory, Options{})
	require.NoError(t, err)
	second := []byte("broken")
	stagedSecond, err := accepting.Stage(second)
	require.NoError(t, err)
	secondReferences := map[string]string{"gate": "digest-b"}
	_, err = accepting.CommitWithReferences(stagedSecond, secondReferences)
	require.NoError(t, err)
	require.Greater(t, validatorCalls, 0)

	recovery, err := store.Recover()
	require.NoError(t, err)
	require.True(t, recovery.RolledBack)
	require.True(t, recovery.Combined)
	require.Equal(t, first, recovery.Active)
	require.Equal(t, firstReferences, recovery.References)
}
