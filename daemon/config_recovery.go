package daemon

// C05 — the daemon's last-good configuration transaction.
//
// `StartOrReloadService` closes the running Box BEFORE it builds the next one, so a profile that
// fails to build or start leaves the daemon with nothing running. That is the exact window a
// last-good transaction exists for, and it is opt-in for two reasons:
//
//   - an embedder that manages configuration files itself (the desktop app) must not have a
//     second copy written behind its back, and an iOS NetworkExtension has no writable cache to
//     put one in;
//   - silently starting the PREVIOUS profile when the requested one is broken changes what the
//     user is running. The report below makes that visible instead of hiding it, and a caller
//     that does not want the behaviour simply does not enable it.
//
// The transaction itself (stage, validate, commit, recover, rollback) lives in common/configtx
// and is driven here: a successful start commits the applied profile as the last-good one; a
// failed start (with recovery enabled) starts the last-good profile and records what happened;
// a failed start without a usable last-good keeps the error and leaves the store clean.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/sagernet/sing-box/common/configtx"
)

// configtxStore is the transaction store type, aliased so the service struct does not need the
// configtx import.
type configtxStore = configtx.Store

// ConfigRecoveryInfo is one recovery event, kept for the caller to surface.
type ConfigRecoveryInfo struct {
	Time time.Time `json:"time"`
	// FailedRevision identifies the profile that failed to start.
	FailedRevision string `json:"failedRevision"`
	// ActiveRevision identifies the profile now running (the recovered one).
	ActiveRevision string `json:"activeRevision"`
	// Error is the failure of the requested profile.
	Error string `json:"error"`
}

// LastConfigRecovery returns the most recent recovery, or nil when the last start was normal.
func (s *StartedService) LastConfigRecovery() *ConfigRecoveryInfo {
	s.configRecoveryAccess.Lock()
	defer s.configRecoveryAccess.Unlock()
	return s.lastConfigRecovery
}

// configStore returns the store, or nil when recovery is not configured.
func (s *StartedService) configStore() *configtx.Store {
	s.configRecoveryAccess.Lock()
	defer s.configRecoveryAccess.Unlock()
	return s.configRecovery
}

// Persistence outcomes for a started configuration.
const (
	persistenceSaved         = "saved"
	persistenceDegraded      = "degraded"
	persistenceNotConfigured = "not_configured"
)

// persistRunningConfig commits one successfully started profile as the last-good document and
// reports what the disk did with it.
//
// A committed-but-unsaved configuration is still running: the Box was started from it and it is
// what the user is using. What it cannot do is survive the next failed start, because a later
// recovery returns to the stored last-good document. Reporting that distinction is the difference
// between "committed" and "committed, persistence degraded" - and it used to be invisible here:
// both a refused Stage and a Commit that wrote nothing were discarded.
//
// A configured directory that could not even be OPENED is a degradation too, not "no store": the
// caller asked for recovery and did not get it, and the reason has to say so instead of reporting
// the same state as a daemon that never asked for a store.
func (s *StartedService) persistRunningConfig(profile string) (status string, warning string) {
	s.configRecoveryAccess.Lock()
	store := s.configRecovery
	directory := s.recoveryDirectory
	openError := s.recoveryOpenError
	s.configRecoveryAccess.Unlock()
	if store == nil {
		if directory == "" {
			return persistenceNotConfigured, ""
		}
		if openError != nil {
			return persistenceDegraded, "the configured recovery directory could not be opened: " + openError.Error()
		}
		return persistenceDegraded, "the configured recovery directory is not open"
	}
	staged, err := store.Stage([]byte(profile))
	if err != nil {
		return persistenceDegraded, err.Error()
	}
	references := s.runningArtifactReferences()
	result, err := store.CommitWithReferences(staged, references)
	if err != nil {
		return persistenceDegraded, err.Error()
	}
	if result.Persisted {
		s.rememberPersistedReferences(references)
		// The running Box must also retain them: its own periodic refresh collects older
		// versions, and the one a recovery would return to has no reader between updates.
		s.serviceAccess.RLock()
		instance := s.instance
		s.serviceAccess.RUnlock()
		if instance != nil && instance.artifactRefs != nil {
			instance.artifactRefs.RetainAll(references)
		}
	}
	if !result.Persisted {
		return persistenceDegraded, result.Warning
	}
	// Persisted, but the store keeps a warning when it could not also keep the previous document
	// as last-good. The new configuration is on disk and in force; the recovery target is not.
	return persistenceSaved, result.Warning
}

// runningArtifactReferences snapshots the exact versions the running Box published, for the
// configuration transaction to persist with the document.
func (s *StartedService) runningArtifactReferences() map[string]string {
	s.serviceAccess.RLock()
	instance := s.instance
	s.serviceAccess.RUnlock()
	if instance == nil || instance.artifactRefs == nil {
		return nil
	}
	return instance.artifactRefs.Snapshot()
}

// persistedReferenceSnapshot returns the exact versions of the configuration the store would
// recover right now, so collection keeps them alive even while no reader holds a lease.
func (s *StartedService) persistedReferenceSnapshot() map[string]string {
	s.configRecoveryAccess.Lock()
	defer s.configRecoveryAccess.Unlock()
	if len(s.persistedReferences) == 0 {
		return nil
	}
	copied := make(map[string]string, len(s.persistedReferences))
	for key, id := range s.persistedReferences {
		copied[key] = id
	}
	return copied
}

// rememberPersistedReferences records the references of the configuration that just became the
// store's active document.
func (s *StartedService) rememberPersistedReferences(references map[string]string) {
	s.configRecoveryAccess.Lock()
	defer s.configRecoveryAccess.Unlock()
	if len(references) == 0 {
		s.persistedReferences = nil
		return
	}
	copied := make(map[string]string, len(references))
	for key, id := range references {
		copied[key] = id
	}
	s.persistedReferences = copied
}

// recoverLastGood returns the last-good profile when it is usable and different from the failed
// one, together with the revision and the exact artifact versions it used. An empty result means
// there is nothing to recover.
func (s *StartedService) recoverLastGood(failedProfile string) (profile string, revision string, references map[string]string, usable bool) {
	if !s.recoverToLastGood {
		return "", "", nil, false
	}
	store := s.configStore()
	if store == nil {
		return "", "", nil, false
	}
	recovery, err := store.Recover()
	if err != nil || len(recovery.Active) == 0 {
		return "", "", nil, false
	}
	if bytes.Equal(recovery.Active, []byte(failedProfile)) {
		// The requested profile IS the last-good one: retrying cannot help.
		return "", "", nil, false
	}
	activeProfile := string(recovery.Active)
	activeRevision := configRevisionOf(activeProfile)
	references = recovery.References
	if !recovery.Combined {
		// A store written before the combined record keeps its sidecar lookup; this is the
		// compatibility path, not the new transaction.
		references, _ = store.References(activeRevision)
	}
	return activeProfile, activeRevision, references, true
}

// recordConfigRecovery stores the recovery event.
func (s *StartedService) recordConfigRecovery(failedProfile string, activeProfile string, failure error) {
	info := &ConfigRecoveryInfo{
		Time:           time.Now(),
		FailedRevision: configRevisionOf(failedProfile),
		ActiveRevision: configRevisionOf(activeProfile),
	}
	if failure != nil {
		info.Error = failure.Error()
	}
	s.configRecoveryAccess.Lock()
	s.lastConfigRecovery = info
	s.configRecoveryAccess.Unlock()
}

// configRevisionOf identifies a profile document: a document identity, not a parse result, so
// two identical documents share a revision and any edit produces another.
func configRevisionOf(profile string) string {
	if profile == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(profile))
	return hex.EncodeToString(sum[:])[:16]
}

// validConfigDocument is the store validator: the profile must at least be a JSON object. A
// document that is valid JSON but unusable still fails later, at build or start time, and the
// recovery path handles it; rejecting garbage here keeps the pending file meaningful.
func validConfigDocument(content []byte) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(content, &probe); err != nil {
		return err
	}
	return nil
}

// ConfigureConfigRecovery points the last-good transaction at a directory and says whether a failed
// start may fall back to the stored document. It is the entry a product uses when the persistent
// directory is only known after construction (the daemon resolves its per-user directory on the
// first start), and the same thing NewStartedService does when the directory is in its options.
//
// An empty directory disables recovery. A directory that cannot be opened is remembered, not
// swallowed: a report must be able to say "configured but degraded" instead of "not configured".
func (s *StartedService) ConfigureConfigRecovery(directory string, recoverToLastGood bool) {
	s.configRecoveryAccess.Lock()
	defer s.configRecoveryAccess.Unlock()
	s.recoverToLastGood = recoverToLastGood
	s.recoveryDirectory = directory
	s.recoveryOpenError = nil
	s.configRecovery = nil
	if directory == "" {
		return
	}
	store, err := configtx.Open(directory, configtx.Options{
		Validator: validConfigDocument,
		Context:   s.ctx,
	})
	s.recoveryOpenError = err
	if err == nil {
		s.configRecovery = store
		// Seed the retained versions from the document the store would recover today, so a
		// process that starts after a crash still protects them from collection.
		s.persistedReferences = nil
		if active, activeErr := store.Active(); activeErr == nil {
			if references, referenceErr := store.References(configRevisionOf(string(active))); referenceErr == nil {
				s.persistedReferences = references
			}
		}
	}
}

// openConfigRecovery prepares the store when the caller configured a directory in the constructor.
func (s *StartedService) openConfigRecovery(directory string) {
	if directory == "" {
		return
	}
	s.ConfigureConfigRecovery(directory, s.recoverToLastGood)
}

// discardPendingCandidate removes a staged candidate that will never be committed, so a later
// start does not mistake it for an interrupted attempt.
func (s *StartedService) discardPendingCandidate() {
	store := s.configStore()
	if store == nil {
		return
	}
	_, _ = store.Recover()
}
