# Comprehensive knowledge review — 2026-10-31 (second pass)

## Scope and target

This is a second, full-inventory pass on the same date as the delta calibration in
[`CALIBRATION-2026-10-31.md`](CALIBRATION-2026-10-31.md). It does **not** move the production
baseline: the source tree is unchanged from the first pass.

| Field | Value |
|---|---|
| Repository / branch | `Piggy-Cat-bit-shadow/JiejieBox` / `testing` |
| Production baseline | `0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28` (unchanged) |
| Release baseline | `v0.1.34`, annotated tag object `e2dad0bf040`, peeled to `0067b03` |
| Prior release | `v0.1.33` = `a0bbf666d`, an ancestor of `0067b03` |
| Reviewed HEAD | `f434b4620a0913cb433b1e88baaf8169dac5e96d` |
| `0067b03..HEAD` | 9 commits, 16 files, +3,353/−867; `CHANGELOG.md`, `README.md`, `docs/**`, `KNOWLEDGE/**` only — no `.go`, CI, build, dependency, pin or release-config change |
| Worktree | clean except the pre-existing modified client gitlinks (`clients/android`, `clients/apple`); not touched or staged |
| Toolchain | no Go toolchain on the review host; tests were **not** executed |

The two release tags were missing from this clone. Both were fetched as immutable objects
(`git fetch --no-tags origin refs/tags/v0.1.34:...`, same for `v0.1.33`) and their peeled commits
verified; no branch, gitlink or worktree was moved.

## What was reviewed

The 9-commit delta is documentation-only, so the protocol's delta review cannot find defects by
itself. `REFRESH.md` §3 therefore applies: inventory every current entry, sample anchors and
cross-module claims, and expand where sampling finds drift.

* Mechanical sweep of all 117 `KNOWLEDGE/**/*.md` files: relative links and anchors, backticked
  paths/`file:line` anchors, Go symbols, commit SHAs, and stated file/line/commit counts.
* Six independent, read-only source audits: core/routing, DNS/sniff, transports, data-path/memory,
  platform/build/CI, and the root documents. Each was told that source and tests override the
  module, that `reports/**` is historical, and not to edit anything.
* Cross-file pass over `AGENTS.md`, `README.md`, `BASELINE.md`, `PROJECT.md`, `ARCHITECTURE.md`,
  `INVARIANTS.md`, `ACCEPTANCE.md`, `COMMIT-INDEX.md`, `INDEX.md`, and `modules/README.md`.

## Corrections made

**New coverage**

* `modules/dns-demand.md`: a full **QNAME-wide NXDOMAIN negative cache** section — eligibility,
  key namespace, remaining-TTL rebuild, capacity, `disable_cache`/`disable_expire`, trim/purge,
  exact-cache-first order, the `adblock` relationship, tests and cautions.
* `INVARIANTS.md` A21 (NXDOMAIN widening), `ACCEPTANCE.md` 4.6/4.7, and `INDEX.md` task/keyword/
  source-path routes for the cache.
* `modules/naive-http-transport.md`: the fork's client-engine controls (`insecure_concurrency`,
  `_single_engine`, QUIC congestion/receive-window).
* `modules/xhttp-client-transport.md`: the XMUX failure breaker and the packet-up opening-pair
  no-gather exemption (the old text overstated gathering).
* `modules/masque-connectip.md` / `modules/core-lifecycle-runtime.md` / `modules/README.md`:
  endpoint-local host-port registration and the shared `adapter.FlowLogger` ownership boundary.
* `modules/build-pruning-release.md` / `modules/ci-release.md`: the current 19-workflow inventory
  (five uncatalogued Linux/Apple workflows are now named as direct-inspection sources).
* `modules/libbox-platform-contract.md`: the `experimental/libbox/internal/**` build-input subtree.
* `modules/data-path-optimization.md`: `BlockerLocalFamilyUnavailable` as a host-property blocker,
  distinct from the configured family strategy.

**Wrong claims and counts**

* `experimental/boxdd/**`: 52 → **55 `.go` files (51 non-test) + 1 `.proto`**; signer failure paths
  7 → **6**; command/service/update line anchors corrected.
* `constant/goos/zgoos_*.go`: 20 → **16**; `//go:generate` directives: 5 → **3 real** directives
  (a fourth hit is quoted prose); Cronet native modules: 30 → **29**.
* `transport/http`: 90 files/137 commits (v0.1.6) → **97 `.go` files / 149 commits** at the
  baseline, with the old values retained as the historical census.
* `common/physicalpath`: historical 35 commits / 8,543 lines / 16 tests → kept as the v0.1.9
  census, with current **37 commits / 9,407 lines / 21 files (17 tests)** added.
* `common/trafficsched`: 30 → **31 commits**; `common/routeflow/reach.go`: 340 → **352 lines**;
  `dns/router.go`: ~2,628 → **2,753 lines**, `Start` `:237` → **`:252`**.
* `experimental/libbox`: `~85` → **110 `.go` files (90 non-test)**.
* `clients/desktop`: `SagerNet/sing-box-for-desktop` (upstream) → **the fork's
  `Piggy-Cat-bit-shadow/sing-box-for-desktop`**.
* Apple ABI pin in `ci-release.md`: `3aedd741…` → **`044e6d5…`**, matching the gitlink and
  `check-libbox-abi.sh`.
* `check-macos-client-config.sh` was recorded as **invoked**; it is a comment only
  (`client-macos.yml:46`) and remains unwired.
* `verify-evidence/`: 14 logs → **13**; "101 commits behind" restated against `a267e99` with the
  current distance (**1,214**). The 101 value was correct only for the old v0.1.6 comparison.
* `include/tz_ios.go` is not "always compiled": it has no explicit build tag, but its `_ios.go`
  filename constrains it to iOS.
* `common/physicalpath` test-file count in `COMMIT-INDEX.md`: 6 → **5**; `PROJECT.md` census rows
  now **partition 1,834** subjects and the process/evidence share is 43.7%, not 44.7%.
* `modules/README.md`: boxdd 52 → 55; the "build tags + CI gates/release artifacts" route is split
  between `build-pruning-release.md` and `ci-release.md`.
* `INDEX.md`: SIGPIPE/pipefail, Windows-runtime and release-identity routes now point at files that
  actually own the subject (the first at the historical report only).
* `PROJECT.md` "current release identity": stale `cb71cf80` / `0.1.31` replaced by the baseline
  `0067b03` / `0.1.34` and the v0.1.34 release facts.

**Stale anchors refreshed (symbol names kept as the stable locators)**

* `dns-demand.md` §1/§2/§3/§6 — the whole generation, reverse-mapping, cache-key, commit, FakeIP
  and `systemd-resolved` tables; the removed `reverseMappingGenerationCurrent` predicate is no
  longer cited as current.
* `path-selection-validation.md` §2/§3/§4/§5/§7 — manager start, dry-run apparatus, group/failover
  tables, seam table and the `route-aware` test characterization (the old "comment contradicts its
  own assertions" defect no longer exists).
* `core-lifecycle-runtime.md`, `data-path-optimization.md`, `buffer-ownership.md`,
  `mtu-and-geometry.md`, `power-mobile-lifecycle.md`, `sniff.md`, `masque-connectip.md`,
  `naive-http-transport.md`, `libbox-platform-contract.md`, `build-pruning-release.md`,
  `ci-release.md`, `windows-daemon-security.md`.
* `mtu-and-geometry.md` PTB path: wrong file (`protocol/masque/client.go`) → the real
  `transport/masque/client.go:704-715`.
* `dns-adblock-action.md` §7: storage key now names `nxdomainCacheKey` (canonical name + QCLASS +
  transport + subnet + environment) and the cache lifecycle rows were added.

## Open findings and limits

1. **`rewrite_ttl` versus a zero SOA header TTL (UNVERIFIED, source contradiction).**
   `applyResponseOptions` normalizes the response TTLs before `commitCacheState` calls
   `storeNXDomain`, so `extractNegativeTTL` re-reads `min(rewritten header TTL, SOA MINIMUM)`.
   For header TTL `0` with MINIMUM `> 0`, a positive rewrite appears able to promote the verdict.
   The existing `TestNXDomain_ZeroSOATTLIsNotPromotedByNameWide` uses `min(0,0)` and sets no
   `RewriteTTL`, so it does not cover the shape. Recorded in `dns-demand.md` and `INVARIANTS.md`
   A21; **no runtime code was changed**. A focused test would settle it.
2. **No test execution.** The review host has no Go toolchain (and no WSL). Every `go test`
   re-check in this pass is `BLOCKED_BY_ENVIRONMENT`, not a pass. Previous calibration results
   remain scoped to their own SHA and host.
3. **No device, interop, native-archive or release-asset byte checks.** The v0.1.34 Release was
   verified through the GitHub API (tag exists, non-draft, non-prerelease, published
   2026-10-29 05:34:42Z); its assets were not downloaded or re-hashed here.
4. **Child repositories were not inspected.** Parent gitlinks and Go pins were re-read; Apple,
   Android, Desktop and nested Dashboard source claims remain scoped to their recorded revisions.
   Local client worktrees were left untouched (they sit at revisions other than the parent pins).
5. **Not every historical line anchor was re-derived.** The audits opened every explicit anchor
   reported as suspect; modules without a finding retain their prior calibration. Symbol names,
   not line numbers, are the stable locator (see `modules/README.md`).
6. Historical reports remain **ARCHIVE**: none was deleted or rewritten into current truth.

## Refresh status

```text
KNOWLEDGE_REFRESH = COMPLETE (source/knowledge review; execution and device limits above)
OLD_PRODUCTION_BASELINE = 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28
NEW_PRODUCTION_BASELINE = 0067b03e377fbfbe5ecc7ae01d0deaf9e1e10f28  (unchanged: docs-only delta)

PRODUCTION_CODE_CHANGED_BY_REFRESH = NO
RUNTIME_BEHAVIOR_CHANGED_BY_REFRESH = NO
PROTOCOL_BEHAVIOR_CHANGED_BY_REFRESH = NO
ABI_CHANGED_BY_REFRESH = NO
GIT_HISTORY_REWRITTEN = NO

DELTA_CLASSIFIED = YES (DOC/RELEASE only)
AFFECTED_SUBSYSTEMS_REAUDITED = YES (all 19 module files inventoried; findings fixed)
COMMIT_PROVENANCE_UPDATED = YES (v0.1.33/v0.1.34 objects fetched and peeled; no baseline move)
INVARIANTS_REAUDITED = YES (A21 added; A1 proof SHAs marked pre-rewrite)
ACCEPTANCE_UPDATED = YES (4.6/4.7 added; stale pin/count/log rows corrected)
HISTORICAL_DOCS_RECONCILED = YES (kept as archive; one stale route demoted to history)
CROSS_FILE_CONSISTENCY = PASS (links, anchors, module registry, baseline/version and counts)
FUTURE_AI_INCREMENTAL_READ = PASS (delta is docs-only; modules carry current source anchors)
```
