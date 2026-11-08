package configtx

// C05 — the transaction's failure cases, one per way a configuration change actually fails:
// a crash with a candidate pending, an active document that no longer validates, a storage
// write that fails, and a rollback that cannot be written either.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// jsonValidator accepts any non-empty well-formed looking document: these cases are about the
// transaction, not the configuration language.
func jsonValidator(content []byte) error {
	if len(content) == 0 || content[0] != '{' {
		return errors.New("not a JSON object")
	}
	return nil
}

func newStore(t *testing.T, options Options) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	if options.Validator == nil {
		options.Validator = jsonValidator
	}
	store, err := Open(dir, options)
	require.NoError(t, err)
	return store, dir
}

func readFile(t *testing.T, dir string, name string) (string, bool) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", false
	}
	return string(content), true
}

func TestCommitMakesTheCandidateActiveAndKeepsThePreviousAsLastGood(t *testing.T) {
	store, dir := newStore(t, Options{})
	first, err := store.Stage([]byte(`{"config":"first"}`))
	require.NoError(t, err)
	_, err = store.Commit(first)
	require.NoError(t, err)

	second, err := store.Stage([]byte(`{"config":"second"}`))
	require.NoError(t, err)
	result, err := store.Commit(second)
	require.NoError(t, err)
	require.True(t, result.Persisted)
	require.Equal(t, `{"config":"second"}`, string(result.Active))
	require.Equal(t, `{"config":"first"}`, string(result.LastGood))

	active, err := store.Active()
	require.NoError(t, err)
	require.Equal(t, `{"config":"second"}`, string(active))
	lastGood, err := store.LastGood()
	require.NoError(t, err)
	require.Equal(t, `{"config":"first"}`, string(lastGood))
	require.False(t, store.Pending())
	_, exists := readFile(t, dir, pendingName)
	require.False(t, exists, "a committed candidate leaves no pending file")
}

func TestAnInvalidCandidateNeverBecomesPending(t *testing.T) {
	store, _ := newStore(t, Options{})
	_, err := store.Stage([]byte(`[]`))
	require.ErrorIs(t, err, ErrInvalidConfig)
	require.False(t, store.Pending(), "a rejected candidate must not survive as a pending file")
}

func TestAStaleCandidateIsRefused(t *testing.T) {
	store, _ := newStore(t, Options{})
	staged, err := store.Stage([]byte(`{"config":"first"}`))
	require.NoError(t, err)
	// Another writer stages a different candidate after this caller validated.
	_, err = store.Stage([]byte(`{"config":"other"}`))
	require.NoError(t, err)

	_, err = store.Commit(staged)
	require.ErrorIs(t, err, ErrStaleCandidate, "committing a candidate nobody staged last would write an unvalidated document")
}

func TestRecoverDiscardsAPendingCandidateFromACrash(t *testing.T) {
	store, dir := newStore(t, Options{})
	first, err := store.Stage([]byte(`{"config":"first"}`))
	require.NoError(t, err)
	_, err = store.Commit(first)
	require.NoError(t, err)
	// A crash after staging the candidate: pending exists, active is still the old document.
	_, err = store.Stage([]byte(`{"config":"second"}`))
	require.NoError(t, err)

	reopened, err := Open(dir, Options{Validator: jsonValidator})
	require.NoError(t, err)
	recovery, err := reopened.Recover()
	require.NoError(t, err)
	require.True(t, recovery.DiscardedPending)
	require.False(t, recovery.RolledBack)
	require.Equal(t, `{"config":"first"}`, string(recovery.Active), "the active document from before the attempt is authoritative")
	require.False(t, reopened.Pending())
}

func TestRecoverFallsBackToLastGoodWhenActiveIsCorrupt(t *testing.T) {
	store, dir := newStore(t, Options{})
	first, err := store.Stage([]byte(`{"config":"first"}`))
	require.NoError(t, err)
	_, err = store.Commit(first)
	require.NoError(t, err)
	second, err := store.Stage([]byte(`{"config":"second"}`))
	require.NoError(t, err)
	_, err = store.Commit(second)
	require.NoError(t, err)

	// Corruption after the fact: the active document no longer validates.
	require.NoError(t, os.WriteFile(filepath.Join(dir, activeName), []byte("garbage"), 0o644))
	recovery, err := store.Recover()
	require.NoError(t, err)
	require.True(t, recovery.RolledBack)
	require.Equal(t, `{"config":"first"}`, string(recovery.Active), "last-good is the fallback")
	active, err := store.Active()
	require.NoError(t, err)
	require.Equal(t, `{"config":"first"}`, string(active))
}

func TestRecoverClearsActiveWhenEvenLastGoodIsBroken(t *testing.T) {
	store, dir := newStore(t, Options{})
	first, err := store.Stage([]byte(`{"config":"first"}`))
	require.NoError(t, err)
	_, err = store.Commit(first)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, activeName), []byte("garbage"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, lastGoodName), []byte("garbage too"), 0o644))
	recovery, err := store.Recover()
	require.NoError(t, err)
	require.True(t, recovery.ActiveCleared, "nothing usable remains, so the repair is to clear active")
	require.Empty(t, recovery.Active)
	_, err = store.Active()
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestAPersistenceFailureReportsTheAttemptWithoutLosingThePreviousActive(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, Options{Validator: jsonValidator})
	require.NoError(t, err)
	first, err := store.Stage([]byte(`{"config":"first"}`))
	require.NoError(t, err)
	_, err = store.Commit(first)
	require.NoError(t, err)

	// The next commit's active write fails, but staging still worked.
	failing, err := Open(dir, Options{
		Validator: jsonValidator,
		WriteFile: func(path string, content []byte) error {
			if filepath.Base(path) == activeName {
				return errors.New("disk full")
			}
			return os.WriteFile(path, content, 0o644)
		},
	})
	require.NoError(t, err)
	second, err := failing.Stage([]byte(`{"config":"second"}`))
	require.NoError(t, err)
	result, err := failing.Commit(second)
	require.NoError(t, err, "a persistence failure is reported in the result, not as a lost configuration")
	require.False(t, result.Persisted)
	require.NotEmpty(t, result.Warning)
	require.Equal(t, `{"config":"second"}`, string(result.Active), "the attempt itself happened")

	// What would start is still the previous document, and the pending candidate is still there.
	onDisk, err := store.Active()
	require.NoError(t, err)
	require.Equal(t, `{"config":"first"}`, string(onDisk))
	require.True(t, failing.Pending(), "a candidate that could not be committed stays pending for the next attempt")
}

func TestRollbackRestoresLastGood(t *testing.T) {
	store, _ := newStore(t, Options{})
	first, err := store.Stage([]byte(`{"config":"first"}`))
	require.NoError(t, err)
	_, err = store.Commit(first)
	require.NoError(t, err)
	second, err := store.Stage([]byte(`{"config":"second"}`))
	require.NoError(t, err)
	_, err = store.Commit(second)
	require.NoError(t, err)

	result, err := store.Rollback()
	require.NoError(t, err)
	require.True(t, result.Persisted)
	require.Equal(t, `{"config":"first"}`, string(result.Active))
	active, err := store.Active()
	require.NoError(t, err)
	require.Equal(t, `{"config":"first"}`, string(active))
}

func TestARollbackThatCannotBeWrittenClearsActive(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir, Options{Validator: jsonValidator})
	require.NoError(t, err)
	first, err := store.Stage([]byte(`{"config":"first"}`))
	require.NoError(t, err)
	_, err = store.Commit(first)
	require.NoError(t, err)
	second, err := store.Stage([]byte(`{"config":"second"}`))
	require.NoError(t, err)
	_, err = store.Commit(second)
	require.NoError(t, err)

	failing, err := Open(dir, Options{
		Validator: jsonValidator,
		WriteFile: func(string, []byte) error { return errors.New("read-only filesystem") },
	})
	require.NoError(t, err)
	result, err := failing.Rollback()
	require.ErrorIs(t, err, ErrRollbackFailed)
	require.True(t, result.ActiveCleared, "a rollback that cannot be written must not leave an unknown active document")
	_, statErr := os.Stat(filepath.Join(dir, activeName))
	require.True(t, os.IsNotExist(statErr), "the active document is cleared so the control plane can repair it")
}

func TestRollbackWithoutLastGoodIsClassified(t *testing.T) {
	store, _ := newStore(t, Options{})
	_, err := store.Rollback()
	require.ErrorIs(t, err, ErrNoLastGood)
}

func TestCancellationAndDeadlineAreNotStorageFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store, err := Open(t.TempDir(), Options{Validator: jsonValidator, Context: ctx})
	require.NoError(t, err)
	_, err = store.Stage([]byte(`{"config":"first"}`))
	require.ErrorIs(t, err, context.Canceled)

	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer timeoutCancel()
	<-timeoutCtx.Done()
	timedOut, err := Open(t.TempDir(), Options{Validator: jsonValidator, Context: timeoutCtx})
	require.NoError(t, err)
	_, err = timedOut.Stage([]byte(`{"config":"first"}`))
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
