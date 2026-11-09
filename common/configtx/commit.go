package configtx

// 1B — configuration, rule references and provider snapshots commit together.
//
// Writing `active.json` and then `references.json` leaves a window in which a restart can read the
// NEW document with the OLD references. The combined record is the commit point: it carries the
// document text and its references (and the previous complete combination), is written to a temp
// file, and is renamed into place only after the legacy files needed by older builds are written.
// A crash before the rename leaves the previous record authoritative; a crash after it leaves a
// complete new record. A reference-write failure aborts before the legacy active document changes,
// so the disk still points at the previous complete combination and the caller reports degraded.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	E "github.com/sagernet/sing/common/exceptions"
)

const (
	commitName          = "commit.json"
	commitTempName      = "commit.next"
	commitCandidateName = "commit.candidate"
)

// commitCandidate marks that a legacy active document may have been overwritten by a transaction
// that has NOT reached the commit point yet. Recovery uses it to avoid promoting that candidate.
type commitCandidate struct {
	Revision string `json:"revision"`
}

func (s *Store) readCommitCandidate() (*commitCandidate, error) {
	encoded, err := os.ReadFile(s.path(commitCandidateName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var candidate commitCandidate
	if err := json.Unmarshal(encoded, &candidate); err != nil {
		return nil, err
	}
	if candidate.Revision == "" {
		return nil, E.New("commit candidate has no revision")
	}
	return &candidate, nil
}

func (s *Store) writeCommitCandidate(revision string) error {
	encoded, err := json.Marshal(commitCandidate{Revision: revision})
	if err != nil {
		return err
	}
	return s.writeFile(filepath.Join(s.dir, commitCandidateName), encoded)
}

// committedGeneration is one complete, recoverable combination.
type committedGeneration struct {
	Content    string            `json:"content"`
	Revision   string            `json:"revision"`
	References map[string]string `json:"references,omitempty"`
}

// commitRecord is the store's authoritative recovery record. LastGood is the previous complete
// combination, kept for the case where the current document no longer validates.
type commitRecord struct {
	Current  committedGeneration  `json:"current"`
	LastGood *committedGeneration `json:"lastGood,omitempty"`
}

func (s *Store) readCommitRecord() (*commitRecord, error) {
	encoded, err := os.ReadFile(s.path(commitName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, E.Cause(ErrUnavailable, "read commit record")
	}
	var record commitRecord
	if err := json.Unmarshal(encoded, &record); err != nil {
		return nil, E.Cause(ErrUnavailable, "decode commit record")
	}
	if record.Current.Content == "" {
		return nil, E.New("commit record has no current generation")
	}
	return &record, nil
}

func (s *Store) writeCommitTemp(record *commitRecord) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.writeFile(filepath.Join(s.dir, commitTempName), encoded)
}

// previousCombination returns the combination the store would recover right now, for the new
// record's LastGood. It prefers the previous combined record and falls back to the legacy files.
func (s *Store) previousCombination() *committedGeneration {
	if record, err := s.readCommitRecord(); err == nil && record != nil && record.Current.Content != "" {
		previous := record.Current
		return &previous
	}
	current, err := os.ReadFile(s.path(activeName))
	if err != nil || (s.validator != nil && s.validator(current) != nil) {
		return nil
	}
	previous := committedGeneration{Content: string(current), Revision: digestOf(current)[:16]}
	if stored, err := s.References(previous.Revision); err == nil && len(stored) > 0 {
		previous.References = stored
	}
	return &previous
}

// commitCombined writes one complete combination and moves the commit point atomically.
func (s *Store) commitCombined(staged *Staged, pending []byte, references map[string]string) *Result {
	result := &Result{Active: pending}
	lastGood := s.previousCombination()
	record := &commitRecord{
		Current: committedGeneration{
			Content:    string(pending),
			Revision:   staged.Revision,
			References: copyReferenceMap(references),
		},
		LastGood: lastGood,
	}
	if err := s.writeCommitTemp(record); err != nil {
		result.Persisted = false
		result.Warning = "commit record was not written: " + err.Error()
		return result
	}
	removeTemp := func() { _ = s.remove(commitTempName) }

	// Legacy last-good: older builds read it and it is not the commit point.
	if current, readErr := os.ReadFile(s.path(activeName)); readErr == nil {
		if s.validator == nil || s.validator(current) == nil {
			if writeErr := s.write(lastGoodName, current); writeErr != nil && result.Warning == "" {
				result.Warning = writeErr.Error()
			} else if writeErr == nil {
				result.LastGood = current
			}
		}
	}
	// References first: if this fails, the legacy active still points at the previous document.
	if err := s.writeReferencesLocked(staged.Revision, references); err != nil {
		removeTemp()
		result.Persisted = false
		result.Warning = "references were not saved: " + err.Error()
		return result
	}
	// The marker is written BEFORE the legacy active is replaced: if the process dies between the
	// two, recovery can tell that active.json is an uncommitted candidate and must not promote it.
	if err := s.writeCommitCandidate(staged.Revision); err != nil {
		removeTemp()
		_ = s.removeReferencesLocked(staged.Revision)
		result.Persisted = false
		result.Warning = "commit candidate marker was not written: " + err.Error()
		return result
	}
	cleanupCandidate := func() { _ = s.remove(commitCandidateName) }
	// Legacy active: an older build keeps working, and the active-directory test still fails here
	// before the new record becomes authoritative.
	if err := s.write(activeName, pending); err != nil {
		removeTemp()
		cleanupCandidate()
		result.Persisted = false
		// The references must not describe a configuration that never reached the disk.
		if removeErr := s.removeReferencesLocked(staged.Revision); removeErr != nil && result.Warning == "" {
			result.Warning = removeErr.Error()
		}
		if result.Warning == "" {
			result.Warning = err.Error()
		} else {
			result.Warning += "; " + err.Error()
		}
		return result
	}
	// The commit point.
	if err := os.Rename(s.path(commitTempName), s.path(commitName)); err != nil {
		removeTemp()
		cleanupCandidate()
		// Put the legacy active back so both the old and the new reader see the previous
		// complete combination. For a first commit there is no previous document: removing the
		// candidate leaves an empty, honest store.
		if lastGood != nil {
			_ = s.write(activeName, []byte(lastGood.Content))
		} else {
			_ = s.remove(activeName)
		}
		_ = s.removeReferencesLocked(staged.Revision)
		result.Persisted = false
		result.Warning = "commit record was not published: " + err.Error()
		return result
	}
	result.Persisted = true
	cleanupCandidate()
	return result
}

func copyReferenceMap(references map[string]string) map[string]string {
	if len(references) == 0 {
		return nil
	}
	copied := make(map[string]string, len(references))
	for key, id := range references {
		if key != "" && id != "" {
			copied[key] = id
		}
	}
	return copied
}
