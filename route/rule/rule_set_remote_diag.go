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

// remoteStagedTestHook, when non-nil, receives the path of the just-staged source file before it is
// parsed or published, so a test can rewrite it inside the verify/parse/publish window. Production is
// nil; the loader re-hashes the bytes it uses, so a rewrite is detected rather than trusted.
var remoteStagedTestHook func(path string)

// derivedOpenTestHook, when non-nil, runs after open() has snapshotted a version's manifest and
// before it decides to discard a never-usable generation. A test can repair the same artifact in that
// window and prove the late discard re-reads the state instead of deleting the repaired record.
// Production is nil.
var derivedOpenTestHook func(point string)

func remoteDerivedTestPoint(point string) {
	if remoteDerivedTestHook != nil {
		remoteDerivedTestHook(point)
	}
}
