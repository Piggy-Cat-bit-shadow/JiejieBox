package configtx

// C05 — the last-good configuration transaction.
//
// A configuration change has four steps that must not be confused with one another: stage a
// candidate, validate it, apply it to the running box, and save it as last-good. Getting the
// order wrong is how a daemon ends up unable to start: a candidate that was half written, a
// pending file from a crash that is later read as configuration, or a broken active document
// with no fallback left.
//
// This store owns the DISK half of that transaction:
//
//   - Stage writes the candidate to a pending file and validates it. A candidate that fails
//     validation is removed; the active document is untouched.
//   - Commit is the only transition that changes what will start next: the current active is
//     copied to last-good (when it validates), then the pending file is renamed over active.
//     A crash between the two leaves the OLD active authoritative and a pending file that
//     Recover discards.
//   - Recover runs at startup: a pending file means an interrupted attempt and is removed; an
//     active document that no longer validates falls back to last-good; when last-good is
//     broken too, the active document is CLEARED so the control plane can repair it instead of
//     looping on a config that cannot work.
//   - Rollback restores last-good explicitly, and if that write fails it clears active rather
//     than leaving a document that is neither the new nor the old one.
//
// Failures are classified so a caller can tell "the candidate is invalid" from "the storage is
// unavailable" from "the caller cancelled or timed out".

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	E "github.com/sagernet/sing/common/exceptions"
)

const (
	activeName   = "active.json"
	lastGoodName = "last-good.json"
	pendingName  = "pending.json"
)

// Classified failures.
var (
	// ErrInvalidConfig means the candidate did not pass validation.
	ErrInvalidConfig = errors.New("config transaction: invalid configuration")
	// ErrUnavailable means the store could not read or write its files.
	ErrUnavailable = errors.New("config transaction: storage unavailable")
	// ErrStaleCandidate means the staged candidate is no longer the one being committed.
	ErrStaleCandidate = errors.New("config transaction: staged candidate is stale")
	// ErrNoLastGood means there is no last-good document to fall back to.
	ErrNoLastGood = errors.New("config transaction: no last-good configuration")
	// ErrRollbackFailed means restoring last-good failed; ActiveCleared says the store then
	// removed the active document instead of leaving an unknown one.
	ErrRollbackFailed = errors.New("config transaction: rollback failed")
)

// Validator checks a candidate document. The caller supplies it: the store owns ordering and
// durability, not the configuration language.
type Validator func(content []byte) error

// Options configures a store.
type Options struct {
	// Validator checks a document before it can become active. Nil accepts everything.
	Validator Validator
	// WriteFile is the low-level write, replaced in tests to inject storage failures.
	WriteFile func(path string, content []byte) error
	// RemoveFile is the low-level removal, replaced with WriteFile in tests.
	RemoveFile func(path string) error
	// Context is observed by every operation that can take time.
	Context context.Context
}

// Staged is a candidate that passed validation and is pending on disk.
type Staged struct {
	Digest   string
	Revision string
}

// Result reports a transition that changed what will start next.
type Result struct {
	// Active is the document now in force.
	Active []byte
	// LastGood is the fallback after the transition, empty when there is none.
	LastGood []byte
	// ActiveCleared reports that the store removed the active document because no usable
	// document remained.
	ActiveCleared bool
	// Persisted reports whether the transition reached the disk. A commit that applied in
	// memory but failed to persist says so instead of pretending it rolled back.
	Persisted bool
	// Warning carries a persistence failure that did not undo the transition.
	Warning string
}

// Store is a directory holding the active, last-good and pending documents.
type Store struct {
	access     sync.Mutex
	dir        string
	validator  Validator
	writeFile  func(path string, content []byte) error
	removeFile func(path string) error
	ctx        context.Context
}

// Open prepares a store.
func Open(dir string, options Options) (*Store, error) {
	if dir == "" {
		return nil, E.Cause(ErrUnavailable, "empty directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, E.Cause(err, "create config transaction directory")
	}
	store := &Store{
		dir:        dir,
		validator:  options.Validator,
		writeFile:  options.WriteFile,
		removeFile: options.RemoveFile,
		ctx:        options.Context,
	}
	if store.writeFile == nil {
		store.writeFile = func(path string, content []byte) error {
			return os.WriteFile(path, content, 0o644)
		}
	}
	if store.removeFile == nil {
		store.removeFile = os.Remove
	}
	if store.ctx == nil {
		store.ctx = context.Background()
	}
	return store, nil
}

// Stage writes a candidate to the pending file and validates it.
func (s *Store) Stage(content []byte) (*Staged, error) {
	if err := s.checkContext(); err != nil {
		return nil, err
	}
	digest := digestOf(content)
	if err := s.write(pendingName, content); err != nil {
		return nil, err
	}
	if s.validator != nil {
		if err := s.validator(content); err != nil {
			// The candidate never becomes anything: remove it so a later Recover does not
			// mistake it for an interrupted commit.
			_ = s.remove(pendingName)
			return nil, E.Cause(ErrInvalidConfig, err.Error())
		}
	}
	return &Staged{Digest: digest, Revision: digest[:16]}, nil
}

// Commit promotes the staged candidate to active, copying the current active to last-good.
func (s *Store) Commit(staged *Staged) (*Result, error) {
	return s.commit(staged, nil)
}

// CommitWithReferences does what Commit does and also records the exact artifact versions this
// configuration used, keyed by the staged revision. A failure to write the references does not
// undo a configuration that already reached the disk: it is reported as a warning.
func (s *Store) CommitWithReferences(staged *Staged, references map[string]string) (*Result, error) {
	return s.commit(staged, references)
}

func (s *Store) commit(staged *Staged, references map[string]string) (*Result, error) {
	if staged == nil {
		return nil, E.New("config transaction: nothing staged")
	}
	if err := s.checkContext(); err != nil {
		return nil, err
	}
	s.access.Lock()
	defer s.access.Unlock()
	pending, err := os.ReadFile(s.path(pendingName))
	if err != nil {
		return nil, E.Cause(ErrUnavailable, "read staged candidate")
	}
	if digestOf(pending) != staged.Digest {
		// Somebody staged another candidate after this caller validated: committing THIS one
		// would write a document nobody checked.
		return nil, ErrStaleCandidate
	}
	if len(references) > 0 {
		// The document and its exact references move together; see commit.go.
		result := s.commitCombined(staged, pending, references)
		if result.Persisted {
			_ = s.remove(pendingName)
		}
		return result, nil
	}
	result := &Result{Active: pending}
	// Last-good is the document that was in force until now, and only when it is usable.
	if current, readErr := os.ReadFile(s.path(activeName)); readErr == nil {
		if s.validator == nil || s.validator(current) == nil {
			if writeErr := s.write(lastGoodName, current); writeErr != nil {
				result.Warning = writeErr.Error()
			} else {
				result.LastGood = current
			}
		}
	}
	if err := s.write(activeName, pending); err != nil {
		// The new document did not reach the disk. The previous active is still there and
		// still authoritative: this is a persistence failure, not a lost configuration.
		result.Persisted = false
		result.Warning = err.Error()
		return result, nil
	}
	result.Persisted = true
	_ = s.remove(pendingName)
	return result, nil
}

// Rollback restores last-good as active. When even that write fails, active is cleared so the
// control plane starts from a known-empty state instead of an unknown document.
func (s *Store) Rollback() (*Result, error) {
	if err := s.checkContext(); err != nil {
		return nil, err
	}
	s.access.Lock()
	defer s.access.Unlock()
	lastGood, err := os.ReadFile(s.path(lastGoodName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNoLastGood
		}
		return nil, E.Cause(ErrUnavailable, "read last-good")
	}
	if s.validator != nil {
		if validationErr := s.validator(lastGood); validationErr != nil {
			return nil, E.Cause(ErrNoLastGood, "last-good is not valid: ", validationErr.Error())
		}
	}
	result := &Result{Active: lastGood, LastGood: lastGood}
	if err := s.write(activeName, lastGood); err != nil {
		// Falling back did not work either. Leaving the previous active in place would hide
		// that the daemon is about to start from a document the caller rejected, so the store
		// removes it and says so.
		_ = s.remove(activeName)
		result.Active = nil
		result.ActiveCleared = true
		return result, E.Cause(ErrRollbackFailed, err.Error())
	}
	result.Persisted = true
	return result, nil
}

// Recovery describes what a startup recovery did.
type Recovery struct {
	// DiscardedPending reports that a pending file from an interrupted attempt was removed.
	DiscardedPending bool
	// RolledBack reports that active did not validate and last-good was restored.
	RolledBack bool
	// ActiveCleared reports that neither document was usable and active was removed.
	ActiveCleared bool
	// Combined reports that the recovered combination came from the combined commit record, so
	// References describes exactly the dependencies that record names.
	Combined bool
	// Active is what the daemon should start with, empty when nothing usable remains.
	Active []byte
	// References are the exact artifact versions the recovered document used.
	References map[string]string
	// Warning carries a removal failure.
	Warning string
}

// Recover repairs the store at startup.
func (s *Store) Recover() (*Recovery, error) {
	if err := s.checkContext(); err != nil {
		return nil, err
	}
	s.access.Lock()
	defer s.access.Unlock()
	recovery := &Recovery{}
	if _, err := os.Stat(s.path(pendingName)); err == nil {
		if removeErr := s.remove(pendingName); removeErr != nil {
			recovery.Warning = removeErr.Error()
		} else {
			recovery.DiscardedPending = true
		}
	}
	// The combined record is the authoritative commit point when it exists: it holds a complete
	// document+references pair, so a restart can never select a half-written combination.
	record, recordErr := s.readCommitRecord()
	if recordErr != nil {
		recovery.Warning = recordErr.Error()
	} else if record != nil {
		current := []byte(record.Current.Content)
		if s.validator == nil || s.validator(current) == nil {
			// The legacy active may be from a commit that crashed after writing it but before the
			// record rename. Converge it to the authoritative record so older readers see the
			// same complete combination.
			if legacy, legacyErr := os.ReadFile(s.path(activeName)); legacyErr != nil || string(legacy) != record.Current.Content {
				if writeErr := s.write(activeName, current); writeErr != nil && recovery.Warning == "" {
					recovery.Warning = writeErr.Error()
				}
			}
			recovery.Active = current
			recovery.References = copyReferenceMap(record.Current.References)
			recovery.Combined = true
			return recovery, nil
		}
		if record.LastGood != nil {
			lastGood := []byte(record.LastGood.Content)
			if s.validator == nil || s.validator(lastGood) == nil {
				if err := s.write(activeName, lastGood); err != nil {
					recovery.Warning = err.Error()
				} else {
					recovery.RolledBack = true
					recovery.Active = lastGood
					recovery.References = copyReferenceMap(record.LastGood.References)
					recovery.Combined = true
				}
				return recovery, nil
			}
		}
		// The record itself is unusable. Do not leave the legacy active pointing at it.
		if _, activeErr := os.Stat(s.path(activeName)); activeErr == nil {
			if err := s.remove(activeName); err != nil {
				recovery.Warning = err.Error()
			} else {
				recovery.ActiveCleared = true
			}
		}
		return recovery, nil
	}
	active, activeErr := os.ReadFile(s.path(activeName))
	activeValid := activeErr == nil && (s.validator == nil || s.validator(active) == nil)
	if activeValid {
		recovery.Active = active
		return recovery, nil
	}
	lastGood, lastGoodErr := os.ReadFile(s.path(lastGoodName))
	lastGoodValid := lastGoodErr == nil && (s.validator == nil || s.validator(lastGood) == nil)
	if lastGoodValid {
		if err := s.write(activeName, lastGood); err != nil {
			recovery.Warning = err.Error()
		} else {
			recovery.RolledBack = true
			recovery.Active = lastGood
		}
		return recovery, nil
	}
	// Nothing usable remains. Clearing active is the repair: the control plane can then write
	// a fresh document instead of starting from one that cannot work.
	if activeErr == nil {
		if err := s.remove(activeName); err != nil {
			recovery.Warning = err.Error()
		} else {
			recovery.ActiveCleared = true
		}
	}
	return recovery, nil
}

// Active returns the document that would start.
func (s *Store) Active() ([]byte, error) {
	content, err := os.ReadFile(s.path(activeName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, E.Cause(ErrUnavailable, "no active configuration")
		}
		return nil, E.Cause(ErrUnavailable, err.Error())
	}
	return content, nil
}

// LastGood returns the fallback document.
func (s *Store) LastGood() ([]byte, error) {
	content, err := os.ReadFile(s.path(lastGoodName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNoLastGood
		}
		return nil, E.Cause(ErrUnavailable, err.Error())
	}
	return content, nil
}

// Pending reports whether an interrupted attempt is on disk.
func (s *Store) Pending() bool {
	_, err := os.Stat(s.path(pendingName))
	return err == nil
}

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

func (s *Store) write(name string, content []byte) error {
	if err := s.writeFile(s.path(name), content); err != nil {
		return E.Cause(ErrUnavailable, err.Error())
	}
	return nil
}

func (s *Store) remove(name string) error {
	if err := s.removeFile(s.path(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// checkContext classifies cancellation and timeouts as themselves, not as storage failures.
func (s *Store) checkContext() error {
	if s.ctx == nil {
		return nil
	}
	if err := s.ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return E.Cause(context.DeadlineExceeded, "config transaction timed out")
		}
		return E.Cause(context.Canceled, "config transaction canceled")
	}
	return nil
}

// digestOf is the content identity used for staleness and revisions.
func digestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
