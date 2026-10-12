# Hard invariants

- A `Scope` owns reverse cleanup. Concurrent closes share a drain/result; a close winning over start must not report successful start. **VERIFIED**: `adapter/lifecycle.go` and lifecycle tests.
- Network transitions advance the existing epoch before coordinator publication. Do not create a parallel epoch or rebuild/wake resources merely because they are stale. **VERIFIED**.
- Memory trim is idle-only: it must not reset the network, dial, wake suspended work, or terminate active flows. `ReuseSuspect` is a stronger sleep/reuse-boundary action, not a trim substitute. **VERIFIED**.
- Route control choices are not physical hops. Validation must fail only on demands/proofs it can establish; unknown flow delivery is not invented. **VERIFIED**.
- DNS answers/cache commits are bound to the current policy/network epoch; a FakeIP server cannot become a destination resolver. **VERIFIED**.
- Fragmented QUIC sniffing continues only for `ErrNeedMoreData`; bounded rounds and buffer ownership are hostile-input guards. **VERIFIED**.
- Apple snapshot reconciliation is level-only. It must not fabricate sleep/wake edges or release a pause based only on a screen read. **VERIFIED**.
- Apple iOS/macOS builds use one pinned source revision and one core-built Libbox artifact; branding display must not alter product identity. **VERIFIED**.
- Release acceptance is fail-closed for failed, cancelled, timed-out, or skipped product jobs, and re-verifies provenance/hash/architecture. **VERIFIED**.

## Current conventions

Use capability-specific retirement interfaces, keep source/build provenance in artifacts, and preserve targeted contract tests with a change. These are mature conventions, not immutable architecture.
