package configtx

// C2 — references are persisted with the configuration that used them.
//
// The active document says WHAT to run; a versioned dependency says WHICH bytes it ran. A
// recovery that only restores the document can pick a newer artifact than the document used.
// These references are written by the same Commit that promotes the document, in a sidecar file
// keyed by the document revision, so a later recovery asks for the exact versions instead of the
// newest ones.
//
// The sidecar is not the active document and never replaces it. If writing it fails, the
// configuration still runs and the commit reports a warning: exact recovery of the dependencies is
// degraded, which is a smaller and more honest failure than refusing a working configuration.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	E "github.com/sagernet/sing/common/exceptions"
)

const (
	referencesName = "references.json"
	// referencesKept bounds the sidecar: recovery only needs the active revision, and a small
	// number of previous ones keeps a manual inspection useful without growing forever.
	referencesKept = 16
)

// References returns the exact artifact versions recorded for one document revision.
func (s *Store) References(revision string) (map[string]string, error) {
	if revision == "" {
		return nil, nil
	}
	encoded, err := os.ReadFile(s.path(referencesName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, E.Cause(ErrUnavailable, "read references")
	}
	var stored map[string]map[string]string
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return nil, E.Cause(ErrUnavailable, "decode references")
	}
	references := stored[revision]
	if len(references) == 0 {
		return nil, nil
	}
	copied := make(map[string]string, len(references))
	for key, id := range references {
		copied[key] = id
	}
	return copied, nil
}

// removeReferencesLocked drops one revision's sidecar entry after a failed commit, so a
// configuration that never reached the disk does not leave references behind. The caller holds
// s.access.
func (s *Store) removeReferencesLocked(revision string) error {
	encoded, err := os.ReadFile(s.path(referencesName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var stored map[string]map[string]string
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return err
	}
	if _, loaded := stored[revision]; !loaded {
		return nil
	}
	delete(stored, revision)
	updated, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	return s.writeFile(filepath.Join(s.dir, referencesName), updated)
}

// writeReferencesLocked merges one revision's references into the sidecar. The caller holds
// s.access (Commit does).
func (s *Store) writeReferencesLocked(revision string, references map[string]string) error {
	if revision == "" || len(references) == 0 {
		return nil
	}
	stored := make(map[string]map[string]string)
	if encoded, err := os.ReadFile(s.path(referencesName)); err == nil {
		_ = json.Unmarshal(encoded, &stored)
	}
	copied := make(map[string]string, len(references))
	for key, id := range references {
		if key != "" && id != "" {
			copied[key] = id
		}
	}
	if len(copied) == 0 {
		return nil
	}
	stored[revision] = copied
	if len(stored) > referencesKept {
		revisions := make([]string, 0, len(stored))
		for key := range stored {
			if key != revision {
				revisions = append(revisions, key)
			}
		}
		sort.Strings(revisions)
		for _, old := range revisions[:len(revisions)-(referencesKept-1)] {
			delete(stored, old)
		}
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	return s.writeFile(filepath.Join(s.dir, referencesName), encoded)
}
