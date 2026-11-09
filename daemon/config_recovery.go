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
func (s *StartedService) persistRunningConfig(profile string) (status string, warning string) {
	store := s.configStore()
	if store == nil {
		return persistenceNotConfigured, ""
	}
	staged, err := store.Stage([]byte(profile))
	if err != nil {
		return persistenceDegraded, err.Error()
	}
	result, err := store.Commit(staged)
	if err != nil {
		return persistenceDegraded, err.Error()
	}
	if !result.Persisted {
		return persistenceDegraded, result.Warning
	}
	// Persisted, but the store keeps a warning when it could not also keep the previous document
	// as last-good. The new configuration is on disk and in force; the recovery target is not.
	return persistenceSaved, result.Warning
}

// recoverLastGood returns the last-good profile when it is usable and different from the failed
// one. An empty result means there is nothing to recover.
func (s *StartedService) recoverLastGood(failedProfile string) (string, bool) {
	if !s.recoverToLastGood {
		return "", false
	}
	store := s.configStore()
	if store == nil {
		return "", false
	}
	recovery, err := store.Recover()
	if err != nil || len(recovery.Active) == 0 {
		return "", false
	}
	if bytes.Equal(recovery.Active, []byte(failedProfile)) {
		// The requested profile IS the last-good one: retrying cannot help.
		return "", false
	}
	return string(recovery.Active), true
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

// openConfigRecovery prepares the store when the caller configured a directory.
func (s *StartedService) openConfigRecovery(directory string) {
	if directory == "" {
		return
	}
	store, err := configtx.Open(directory, configtx.Options{
		Validator: validConfigDocument,
		Context:   s.ctx,
	})
	if err != nil {
		return
	}
	s.configRecoveryAccess.Lock()
	s.configRecovery = store
	s.configRecoveryAccess.Unlock()
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
