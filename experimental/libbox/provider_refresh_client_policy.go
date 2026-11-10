package libbox

// The client half of the manual provider refresh: how long a whole call may last, and how a missing
// RPC is reported.
//
// Two policies, kept here as plain functions rather than inline in the call, because both of them
// are decisions a caller can get wrong silently: a batch that is unbounded only shows up as a hang,
// and an RPC that does not exist on an older kernel only shows up as a refresh that never succeeds.

import (
	std_json "encoding/json"
	"time"
)

const (
	// defaultProviderRefreshTimeout is the per-provider bound used when the caller asks for none.
	// It matches the kernel's own default (`refreshOneProvider`) so the two halves agree.
	defaultProviderRefreshTimeout = 30 * time.Second
	// providerRefreshBatchProviders is how many per-provider bounds the whole call is allowed to
	// spend. It is not "the number of providers" - the caller does not say how many it is about to
	// visit - it is the factor that turns "each provider may take T" into "the whole call may take
	// at most this long".
	providerRefreshBatchProviders = 4
	// minProviderRefreshBatch keeps a very small per-provider timeout from producing a batch
	// deadline so short that even the round trip and the first token cannot fit.
	minProviderRefreshBatch = 30 * time.Second
	// maxProviderRefreshBatch is the hard ceiling. A caller that asks for a five-minute
	// per-provider bound gets a call that may last this long, not twenty minutes: the alternative
	// is an app-side call with no upper bound at all, which is the state this replaces.
	maxProviderRefreshBatch = 120 * time.Second
)

// refreshBatchDeadline derives the whole-call ceiling from the per-provider bound.
func refreshBatchDeadline(providerTimeout time.Duration) time.Duration {
	if providerTimeout <= 0 {
		providerTimeout = defaultProviderRefreshTimeout
	}
	ceiling := providerTimeout * providerRefreshBatchProviders
	if ceiling < minProviderRefreshBatch {
		return minProviderRefreshBatch
	}
	if ceiling > maxProviderRefreshBatch {
		return maxProviderRefreshBatch
	}
	return ceiling
}

// unsupportedProviderRefresh is the document that answers "this kernel has no such RPC".
type unsupportedProviderRefresh struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

// encodeUnsupportedProviderRefresh renders the capability answer.
//
// It is deliberately a successful RESULT and not an error: the generated bridge throws on a Go
// error and drops the value, so a fact the caller must act on cannot travel as one. The document
// says "supported": false and every client that understands the refresh report can carry it.
func encodeUnsupportedProviderRefresh(reason string) string {
	encoded, err := std_json.Marshal(unsupportedProviderRefresh{Supported: false, Reason: reason})
	if err != nil {
		return `{"supported":false}`
	}
	return string(encoded)
}
