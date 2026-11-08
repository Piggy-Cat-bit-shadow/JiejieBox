package urltest

// P02B — the deduplication key: when may two callers share one measurement?
//
// A probe is equivalent to another only when every fact that can change its answer is the
// same. The coordinator's bound is a concurrency limit, not a deduplication rule, so the key
// has to name the whole question:
//
//   - the instance/configuration doing the measuring (two boxes or a temp validation box must
//     never share an answer with the live one);
//   - the network epoch (an answer from the previous network says nothing about this one);
//   - the node and the path it is measured through (a detour changes the route);
//   - the target URL and the expected status (a 204-only probe is a different question);
//   - the method and the TLS policy (a HEAD may be answered differently, and a TLS fingerprint
//     changes what the server sees).
//
// Anything not in this key must be something that cannot change the result. That is the rule
// that keeps a "shared" measurement from becoming a wrong one.

// MeasurementKey is the full identity of one measurement.
type MeasurementKey struct {
	// Instance identifies the configuration/box performing the probe.
	Instance string
	// NetworkEpoch is the network generation the probe belongs to.
	NetworkEpoch uint64
	// Node is the outbound tag whose delay is measured.
	Node string
	// Path is the resolved detour chain the probe goes through.
	Path string
	// Target is the URL being probed.
	Target string
	// ExpectedStatus is the status policy of the probe (empty means any response).
	ExpectedStatus string
	// Method is the HTTP method ("HEAD" when unspecified by the caller).
	Method string
	// TLSFingerprint identifies the TLS policy the probe presents.
	TLSFingerprint string
	// Purpose separates a formal health measurement from a manual display or a diagnostic:
	// they may ask the same question, but their evidence is recorded under different owners.
	Purpose MeasurementPurpose
}

// MeasurementPurpose names who asked, because the answer is stored for a different owner.
type MeasurementPurpose string

const (
	// PurposeHealth is a formal health round: its evidence may move the selection.
	PurposeHealth MeasurementPurpose = "health"
	// PurposeDisplay is a user-visible test (a delay shown in a list) that must not move the
	// formal health state by itself.
	PurposeDisplay MeasurementPurpose = "display"
	// PurposeDiagnostic is a diagnostic probe, recorded as an observation, never as health.
	PurposeDiagnostic MeasurementPurpose = "diagnostic"
)

// Equivalent reports whether two measurements ask exactly the same question.
func (k MeasurementKey) Equivalent(other MeasurementKey) bool {
	return k == other
}

// SharesEvidenceWith reports whether an answer for k may be reused for other.
//
// Only health evidence is reusable across callers, and only between health probes: a display
// or diagnostic answer is an observation for its caller, not a health verdict for the group.
func (k MeasurementKey) SharesEvidenceWith(other MeasurementKey) bool {
	if k.Purpose != PurposeHealth || other.Purpose != PurposeHealth {
		return false
	}
	return k.Equivalent(other)
}

// String renders the key for logs and tests, without any credential-bearing URL detail.
func (k MeasurementKey) String() string {
	return k.Instance + "|" + k.Node + "|" + k.Path + "|" + k.Target + "|" + k.ExpectedStatus + "|" + k.Method + "|" + k.TLSFingerprint
}
