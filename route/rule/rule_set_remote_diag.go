package rule

import "sync/atomic"

// Bounded diagnostics for the remote derived-cache path.
//
// These counters exist so a test (and a debug log) can prove what a real remote load actually did:
// a derived HIT must decode zero source documents and write zero SRSM files, while a MISS must do
// both. They are plain atomics with no per-rule, per-URL or per-request history, so the production
// cost is one increment on paths that already do orders of magnitude more work.
//
// Naming: `sourceDecodes` counts parses of the source document (SRS or source JSON); `srsmWrites`
// counts conversions written for mapping; `heapBuilds` counts loads that fell back to the non-mapped
// path (mmapRuleSet), which is what a platform without the derived cache does.
var remoteDerivedCounters struct {
	hits          atomic.Int64
	misses        atomic.Int64
	sourceDecodes atomic.Int64
	srsmWrites    atomic.Int64
	heapBuilds    atomic.Int64
}

// remoteDerivedDiagnostics returns a snapshot of the counters.
func remoteDerivedDiagnostics() (hits int64, misses int64, sourceDecodes int64, srsmWrites int64, heapBuilds int64) {
	return remoteDerivedCounters.hits.Load(),
		remoteDerivedCounters.misses.Load(),
		remoteDerivedCounters.sourceDecodes.Load(),
		remoteDerivedCounters.srsmWrites.Load(),
		remoteDerivedCounters.heapBuilds.Load()
}

// resetRemoteDerivedDiagnostics zeroes the counters. Tests call it before the action they measure;
// production never calls it.
func resetRemoteDerivedDiagnostics() {
	remoteDerivedCounters.hits.Store(0)
	remoteDerivedCounters.misses.Store(0)
	remoteDerivedCounters.sourceDecodes.Store(0)
	remoteDerivedCounters.srsmWrites.Store(0)
	remoteDerivedCounters.heapBuilds.Store(0)
}

// remoteDerivedTestHook, when non-nil, runs at named interleaving points of the remote load so a test
// can place Close, a concurrent refresh or a collector exactly inside the window it checks. It must
// never change production state: the production value is nil and the hook only observes/blocks.
//
// Points: "derived-hit", "derived-miss", "before-source-publish", "before-commit".
var remoteDerivedTestHook func(point string)

func remoteDerivedTestPoint(point string) {
	if remoteDerivedTestHook != nil {
		remoteDerivedTestHook(point)
	}
}
