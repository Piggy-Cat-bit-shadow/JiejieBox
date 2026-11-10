package remote

// C03 — the provider's application report.
//
// A refresh is not one outcome. It can download a document and apply none of it, apply the
// nodes but be refused by a group's callbacks, apply everything and then fail to write the
// cache, or change nothing at all. Callers used to see a single error, which cannot tell
// "the nodes are live and the cache write failed" from "the document was never fetched".
//
// The report keeps those apart:
//   - requestedRevision advances for every attempt; appliedRevision only for a document whose
//     nodes were actually installed;
//   - status is unchanged / committed / partial / unknown, and PendingTags names the members
//     the document asked for that are not published;
//   - downloaded and the ancillary error are separate facts, so a post-publish cache failure
//     does not make a live update look like a failed one;
//   - RestartRequired marks the one case a retry cannot fix: groups refused the membership the
//     document would create, so the published graph differs from the document until a restart.
//
// The old Update() error contract is unchanged: it still returns the first failure, and the
// report is additional.

import (
	"sort"
	"strings"
)

// ApplyStatus is the outcome of one refresh attempt.
type ApplyStatus string

const (
	// ApplyStatusUnchanged means the server reported the document unchanged, or the fetched
	// document was identical to the applied one.
	ApplyStatusUnchanged ApplyStatus = "unchanged"
	// ApplyStatusCommitted means every requested node is published and no group refused.
	ApplyStatusCommitted ApplyStatus = "committed"
	// ApplyStatusPartial means the document was applied only in part: some members are
	// pending, or a group kept its previous membership.
	ApplyStatusPartial ApplyStatus = "partial"
	// ApplyStatusUnknown means the document was not applied at all (download or parse failed
	// before anything was published).
	ApplyStatusUnknown ApplyStatus = "unknown"
)

// ApplyReport is one refresh attempt, as the provider itself observed it.
type ApplyReport struct {
	Tag string `json:"tag"`
	// RequestedRevision counts attempts; AppliedRevision counts documents whose nodes were
	// installed. They are equal after a committed apply and differ while a document is
	// pending or refused.
	RequestedRevision uint64      `json:"requestedRevision"`
	AppliedRevision   uint64      `json:"appliedRevision"`
	Status            ApplyStatus `json:"status"`
	// Downloaded reports that a document body was read (a 304 is unchanged, not downloaded).
	Downloaded bool `json:"downloaded"`
	// RequestedTags are the published tags the document asks for, in document order.
	RequestedTags []string `json:"requestedTags,omitempty"`
	// AppliedTags are requested tags that are published.
	AppliedTags []string `json:"appliedTags,omitempty"`
	// PendingTags are requested tags that are NOT published.
	PendingTags []string `json:"pendingTags,omitempty"`
	// RetainedTags are published tags the document no longer lists: the old objects still in
	// service while retirement finishes.
	RetainedTags []string `json:"retainedTags,omitempty"`
	// GroupRejected reports that a group callback refused the membership this document would
	// create. The nodes are installed, the group kept its previous members.
	GroupRejected bool `json:"groupRejected"`
	// RestartRequired is the consequence of GroupRejected: a retry re-reads the same document
	// and is refused again; only a restart rebuilds the groups from their configuration.
	RestartRequired bool `json:"restartRequired"`
	// AncillaryError is a failure AFTER the publish (the cache write). The update is live; the
	// next refresh will download the document again.
	AncillaryError string `json:"ancillaryError,omitempty"`
	// Diagnostics are the individual failures of this attempt, redacted.
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// cloneReport copies a report so a caller cannot mutate what the provider recorded.
func cloneReport(report *ApplyReport) *ApplyReport {
	if report == nil {
		return nil
	}
	cloned := *report
	cloned.RequestedTags = append([]string(nil), report.RequestedTags...)
	cloned.AppliedTags = append([]string(nil), report.AppliedTags...)
	cloned.PendingTags = append([]string(nil), report.PendingTags...)
	cloned.RetainedTags = append([]string(nil), report.RetainedTags...)
	cloned.Diagnostics = append([]string(nil), report.Diagnostics...)
	return &cloned
}

// publishedTags is the provider's current published tag set, ascending.
func (s *ProviderRemote) publishedTags() []string {
	outbounds := s.Outbounds()
	tags := make([]string, 0, len(outbounds))
	for _, outbound := range outbounds {
		tags = append(tags, outbound.Tag())
	}
	sort.Strings(tags)
	return tags
}

// recordApply stores the report for LastApplyReport and advances the revision counters.
func (s *ProviderRemote) recordApply(report *ApplyReport) {
	if report == nil {
		return
	}
	if report.Status == "" {
		report.Status = ApplyStatusUnknown
	}
	if report.Status != ApplyStatusUnknown && report.Status != ApplyStatusUnchanged {
		s.appliedRevision.Store(report.RequestedRevision)
	}
	report.AppliedRevision = s.appliedRevision.Load()
	s.reportAccess.Lock()
	s.lastReport = cloneReport(report)
	s.reportAccess.Unlock()
}

// LastApplyReport returns the most recent attempt, or nil before the first one.
//
// It is history, not a receipt: an embedder reporting on a call it just made uses the report that
// call returned (`UpdateWithReportContext`), because this value may already belong to another
// attempt - a provider's own timer completes on its own schedule.
func (s *ProviderRemote) LastApplyReport() *ApplyReport {
	s.reportAccess.Lock()
	defer s.reportAccess.Unlock()
	return cloneReport(s.lastReport)
}

// errorText renders an error for a diagnostic, or the empty string.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// diffTags splits requested tags into applied and pending by what is published.
func diffTags(requested []string, published []string) (applied []string, pending []string) {
	publishedSet := make(map[string]bool, len(published))
	for _, tag := range published {
		publishedSet[tag] = true
	}
	for _, tag := range requested {
		if publishedSet[tag] {
			applied = append(applied, tag)
		} else {
			pending = append(pending, tag)
		}
	}
	return
}

// retainedTags are published tags the document no longer asks for.
func retainedTags(requested []string, published []string) []string {
	requestedSet := make(map[string]bool, len(requested))
	for _, tag := range requested {
		requestedSet[tag] = true
	}
	var retained []string
	for _, tag := range published {
		if !requestedSet[tag] {
			retained = append(retained, tag)
		}
	}
	return retained
}

// dedupeDiagnostics keeps the first occurrence of each diagnostic.
func dedupeDiagnostics(values []string) []string {
	seen := make(map[string]bool, len(values))
	deduped := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		deduped = append(deduped, value)
	}
	return deduped
}

// sameTagSet reports whether two tag lists contain the same tags.
func sameTagSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	return strings.Join(leftCopy, "\x00") == strings.Join(rightCopy, "\x00")
}
