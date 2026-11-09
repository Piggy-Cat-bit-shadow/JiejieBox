package configtx

// 09 — a process interrupted between the legacy active write and the publish of the authoritative
// commit record must not recover the uncommitted candidate.
//
// These tests crash INSIDE the write hook (panic), which skips every defer: the temp record, the
// candidate marker and the legacy active are left exactly as a killed process would leave them.
// A fresh Store is then opened on the same directory.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func stageAndCrash(t *testing.T, directory string, candidate []byte, references map[string]string) {
	t.Helper()
	crash := "simulated process death after active write"
	store, err := Open(directory, Options{
		WriteFile: func(path string, content []byte) error {
			if err := os.WriteFile(path, content, 0o600); err != nil {
				return err
			}
			if filepath.Base(path) == activeName && bytes.Equal(content, candidate) {
				panic(crash)
			}
			return nil
		},
	})
	require.NoError(t, err)
	staged, err := store.Stage(candidate)
	require.NoError(t, err)
	require.PanicsWithValue(t, crash, func() {
		_, _ = store.CommitWithReferences(staged, references)
	})
}

func recoverFresh(t *testing.T, directory string) *Recovery {
	t.Helper()
	store, err := Open(directory, Options{})
	require.NoError(t, err)
	recovery, err := store.Recover()
	require.NoError(t, err)
	return recovery
}

func TestCrashBeforeTheFirstCommitPointLeavesNoRecoverableConfiguration(t *testing.T) {
	directory := t.TempDir()
	candidate := []byte(`{"log":{"level":"error"}}`)
	stageAndCrash(t, directory, candidate, map[string]string{"gate": "digest-new"})
	_, err := os.Stat(filepath.Join(directory, commitName))
	require.ErrorIs(t, err, os.ErrNotExist, "the commit point was never published")

	recovery := recoverFresh(t, directory)
	require.Empty(t, recovery.Active, "an uncommitted first configuration must not be recovered")
	require.True(t, recovery.ActiveCleared)
	_, err = os.Stat(filepath.Join(directory, activeName))
	require.ErrorIs(t, err, os.ErrNotExist, "the candidate active is removed")
	_, err = os.Stat(filepath.Join(directory, commitCandidateName))
	require.ErrorIs(t, err, os.ErrNotExist, "the candidate marker is consumed")
	_, err = os.Stat(filepath.Join(directory, pendingName))
	require.ErrorIs(t, err, os.ErrNotExist, "the interrupted pending candidate is discarded")
	references, err := recoveryStoreReferences(directory, string(candidate))
	require.NoError(t, err)
	require.Empty(t, references, "the candidate's references do not become active references")
}

func TestCrashDuringLegacyUpgradeKeepsTheLegacyDocument(t *testing.T) {
	directory := t.TempDir()
	legacy := []byte(`{"log":{"level":"warn"}}`)
	require.NoError(t, os.WriteFile(filepath.Join(directory, activeName), legacy, 0o600))
	legacyReferences := map[string]string{"gate": "digest-old"}
	writeLegacyReferences(t, directory, legacy, legacyReferences)

	candidate := []byte(`{"log":{"level":"error"}}`)
	stageAndCrash(t, directory, candidate, map[string]string{"gate": "digest-new"})

	recovery := recoverFresh(t, directory)
	require.True(t, recovery.RolledBack)
	require.Equal(t, legacy, recovery.Active)
	require.Equal(t, legacyReferences, recovery.References)
	active, err := os.ReadFile(filepath.Join(directory, activeName))
	require.NoError(t, err)
	require.Equal(t, legacy, active, "legacy active is restored")
	references, err := recoveryStoreReferences(directory, string(candidate))
	require.NoError(t, err)
	require.Empty(t, references, "the candidate's references are removed")
}

func TestCrashDuringAnUpdateKeepsTheCommittedCombination(t *testing.T) {
	directory := t.TempDir()
	committed := []byte(`{"log":{"level":"warn"}}`)
	store, err := Open(directory, Options{})
	require.NoError(t, err)
	staged, err := store.Stage(committed)
	require.NoError(t, err)
	committedReferences := map[string]string{"gate": "digest-old"}
	_, err = store.CommitWithReferences(staged, committedReferences)
	require.NoError(t, err)

	candidate := []byte(`{"log":{"level":"error"}}`)
	stageAndCrash(t, directory, candidate, map[string]string{"gate": "digest-new"})

	recovery := recoverFresh(t, directory)
	require.True(t, recovery.Combined)
	require.Equal(t, committed, recovery.Active)
	require.Equal(t, committedReferences, recovery.References)
	// Legacy active was overwritten by the crash; recovery converges it to the record.
	active, err := os.ReadFile(filepath.Join(directory, activeName))
	require.NoError(t, err)
	require.Equal(t, committed, active)
}

func TestCrashDuringASameDocumentReferenceUpdateKeepsTheOldReferences(t *testing.T) {
	directory := t.TempDir()
	document := []byte(`{"log":{"level":"error"}}`)
	store, err := Open(directory, Options{})
	require.NoError(t, err)
	staged, err := store.Stage(document)
	require.NoError(t, err)
	oldReferences := map[string]string{"gate": "digest-old"}
	_, err = store.CommitWithReferences(staged, oldReferences)
	require.NoError(t, err)

	// Same document text, different dependency version. The active write content equals the
	// previous document, so the crash hook has to key on the reference marker instead.
	crash := "simulated process death after same-document active write"
	interrupted, err := Open(directory, Options{
		WriteFile: func(path string, content []byte) error {
			if err := os.WriteFile(path, content, 0o600); err != nil {
				return err
			}
			if filepath.Base(path) == commitCandidateName {
				panic(crash)
			}
			return nil
		},
	})
	require.NoError(t, err)
	stagedAgain, err := interrupted.Stage(document)
	require.NoError(t, err)
	require.PanicsWithValue(t, crash, func() {
		_, _ = interrupted.CommitWithReferences(stagedAgain, map[string]string{"gate": "digest-new"})
	})

	recovery := recoverFresh(t, directory)
	require.True(t, recovery.Combined)
	require.Equal(t, document, recovery.Active)
	require.Equal(t, oldReferences, recovery.References, "the old exact reference survives")
}

func TestCrashAfterTheCommitPointRecoversTheNewCombinationAndCleansUp(t *testing.T) {
	directory := t.TempDir()
	document := []byte(`{"log":{"level":"error"}}`)
	store, err := Open(directory, Options{})
	require.NoError(t, err)
	staged, err := store.Stage(document)
	require.NoError(t, err)
	references := map[string]string{"gate": "digest-new"}
	_, err = store.CommitWithReferences(staged, references)
	require.NoError(t, err)

	// Simulate a crash after the record rename but before the marker/temp cleanup.
	require.NoError(t, os.WriteFile(filepath.Join(directory, commitCandidateName), []byte(`{"revision":"stale"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, commitTempName), []byte("{stale"), 0o600))

	for attempt := 0; attempt < 2; attempt++ {
		recovery := recoverFresh(t, directory)
		require.True(t, recovery.Combined)
		require.Equal(t, document, recovery.Active)
		require.Equal(t, references, recovery.References, "repeated recovery is identical")
	}
	_, err = os.Stat(filepath.Join(directory, commitCandidateName))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(directory, commitTempName))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func writeLegacyReferences(t *testing.T, directory string, document []byte, references map[string]string) {
	t.Helper()
	stored := map[string]map[string]string{digestOf(document)[:16]: references}
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, referencesName), encoded, 0o600))
}

func recoveryStoreReferences(directory string, document string) (map[string]string, error) {
	store, err := Open(directory, Options{})
	if err != nil {
		return nil, err
	}
	references, err := store.References(digestOf([]byte(document))[:16])
	if err != nil {
		return nil, err
	}
	return references, nil
}
