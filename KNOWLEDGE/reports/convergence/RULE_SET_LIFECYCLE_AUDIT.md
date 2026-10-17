# Audit Result — rule-set / resource lifecycle (narrow scope)

## 1. Baseline

- **branch**: `testing` — this fork's main branch (`origin/HEAD -> origin/testing`; there is no `main` ref in this tree)
- **starting commit**: `dbfcca7deb1`
- **working tree status**: clean at start (`git status --porcelain` empty), clean at end
- **knowledge baseline used for routing**: `KNOWLEDGE/BASELINE.md` tag `v0.1.9`; delta check `git log --oneline v0.1.9..HEAD -- route/rule/ route/rule_set_updater.go adapter/router.go experimental/cachefile` = only FakeIP/cachefile commits + the adblock action → the `core-lifecycle-runtime` module still describes the tree, and `INDEX.md` routed me to it

## 2. Lifecycle map

Every answer below is read from source, with the anchor.

| question | answer | anchor |
|---|---|---|
| **who creates a rule-set?** | exactly one site: `Router.Initialize`, one object per tag, guarded against a duplicate tag across the whole `rule_set` list | `route/router.go:128-140` |
| is there a second entry point? | no. `NewRuleSet` is called only there; DNS rules reference the same objects by tag through `Router.RuleSet` | `git grep NewRuleSet(` → 1 production caller; consumers: `dns/router.go:2441`, `protocol/tun/inbound.go:420,427`, `route/rule/rule_item_rule_set.go:34` |
| **who owns?** | the Box's `adapter.Scope`: `scope.Add(ruleSet.Close)` at `StartStateInitialize`, `scope.Add(ruleSetUpdater.Close)` at `StartStateStart` | `route/router.go:148-150`, `:209-212` |
| **who references?** | `RuleSetItem.Start` (route rules, `PostStart`), `DNSRule.Start` (`StartStateStart`), the TUN inbound's `route_address_set` (`Start`) — all `IncRef`, all released by the same Scope | `route/rule/rule_item_rule_set.go:31-52`, `route/rule/rule_dns.go:415`, `protocol/tun/inbound.go:617,625,659` |
| **who replaces?** | nobody. A rule-set is immutable for the life of a Box; an *update* replaces its `rules` slice in place under `s.access` | `route/rule/rule_set_remote.go:216-224`, `rule_set_local.go:144-152` |
| **who closes?** | the Scope drain, in reverse registration order: updater first, then each rule-set | `adapter/lifecycle.go:218`, `route/router.go:149,211` |
| **who cancels?** | `RemoteRuleSet.cancel` (aborts an in-flight fetch) and the updater's own `cancel` | `route/rule/rule_set_remote.go:328`, `rule_set_updater.go:47-50` |
| **when does the old object lose its last reference on reload?** | at `Box.Close` → `Scope.Close`: the old `Router` is dropped with the old Box, the Scope drain closes the updater goroutine and each rule-set, and `RuleSetItem.Close`/`releaseRouteSetRefs` drop the refs | `box.go:1219-1253` (close order), `adapter/lifecycle.go:218-284` |
| **who owns the refresh timer/goroutine?** | `RuleSetUpdater`: one goroutine, one timer, started at `PostStart`, closed by the Scope | `route/rule/rule_set_updater.go:43-93` |
| **who owns the local file watcher?** | `LocalRuleSet.watcher`, created in the constructor, started in `StartContext`, closed by `Close` | `route/rule/rule_set_local.go:63-75`, `:88-96`, `:195-198` |
| **what owns the `.srs` mapping on iOS?** | `srs.ReadMmap`'s storage, released by `runtime.AddCleanup`; `Upgrade()` copies to heap and the mapping is released when the storage is unreachable | `common/srs/mmap.go:ReadMmap`, `option/rule_set.go:343` |
| **does a client wrapper keep a second copy?** | no. `experimental/libbox` has no rule-set reference at all (`git grep -i ruleset -- experimental/libbox` → none); the GUI's input is a JSON string | — |

**The one sentence**: a rule-set is created once by the Router, referenced by counting, owned by the Box's Scope, and released when that Scope drains — with the refresh goroutine registered *after* the sets it refreshes, so reverse-order drain stops the producer before its inputs.

## 3. Confirmed findings

**None. No lifecycle defect was confirmed in this area.**

The prompt allows this outcome (§18 C) and requires evidence, so here is what was checked and how the negative was established:

| # | §5 item | verdict | evidence |
|---|---|---|---|
| 1 | duplicate initialisation | **no** | one construction site, duplicate-tag guard, and route/DNS/TUN all reach the *same* object through `Router.RuleSet`. Verified at runtime by a reload test that removes the tag: the new Box has no such object at all |
| 2 | reload leaves old resources | **no** | both `Close` calls are Scope-owned; asserted by `TestTheRouterHandsItsRuleSetResourcesToTheScope` (rules gone, goroutine stopped after the drain) and mutation-verified by deleting each `scope.Add` |
| 3 | watcher / updater / goroutine leak | **no** | the updater loop returns on `ctx.Done()` and on `WaitProviderRefresh` returning false (both ctx-aware: `common/power/governor.go:991-1010`); `fswatch.Watcher.Close` is nil-safe and idempotent, and stops its debounce timers (`fswatch@v0.1.2/watcher.go:106-127`) — so a Box that fails *between* `Initialize` and `Start` cannot panic in `Close` |
| 4 | client double-holding heavy state | **no** | `experimental/libbox` references no rule-set; the cache file stores the raw downloaded bytes, not a parsed form (`adapter.SavedBinary`) |
| 5 | failure-path cleanup | **no** | `StartContext` failure → `task.FastFail` returns → the Box closes the Scope it already populated. The one skipped call on that path is `HTTPStartContext.Close`, which only closes idle HTTP connections on transports owned by the HTTP client manager (`adapter/http.go:24-45`) |
| 6 | orderings | **correct** | refs are taken at `StartStateStart` (DNS) and `PostStart` (route rules), *before* the `Cleanup` pass at `Started`; TUN takes its refs in `Start`, also before. Mutation-verified by moving the pass earlier |
| 7 | `Cleanup` vs rule matching | **correct by design** | an unreferenced set drops its rules at `Started` and nothing can take a reference afterwards (`RuleSetItem.Start` is the only route-rule ref site and it has already run) |

### Two observations that are NOT defects, recorded so they are not re-audited

1. **`Cleanup`/`Close`/`updateOnce` write `s.rules` without `s.access`, while `Match` reads it with no lock at all.** This is upstream's design (`git log -S` → `8304295c48f`, fork-side count **0**; identical in `refs/remotes/upstream/testing`). Making it race-free means putting a lock or an `atomic.Value` on the per-connection match path — a change to the matcher core that the prompt explicitly forbids without proof of a real problem, and `-race` is `BLOCKED_BY_ENVIRONMENT` here (§6). Direction if it is ever revisited: the fix belongs upstream.
2. **A watched-but-unreferenced local rule-set re-parses on every file change.** `Cleanup` frees its rules at startup, and a later file event reloads them again. Suppressing that reload would trade a bounded memory observation for *stale data* the moment anything did reference the set — a worse trade, so it is left alone. This is the "don't invent work" boundary.

## 4. Fixes

- **files changed**: none in production code.
- **exact fix**: no fix was warranted. §3 records why each candidate was rejected, including the two upstream-shaped observations above.
- **why this is the minimal fix**: the minimal correct action for an audit that finds no defect is to add the missing *evidence*, which is §6's three test files. Manufacturing a code change here would have meant touching the lock-free match path for a benign word-teardown, or suppressing a reload that a future consumer might need.

## 5. Similar-pattern audit

- **checked** (same root cause: "a correct object that nobody owns" / "a scope entry that was never registered"):
  - `RuleSetUpdater` — owned (`scope.Add`), stops on cancel, gate wired;
  - `LocalRuleSet.watcher` — owned by `Close`;
  - `processSearcher`, neighbour resolvers, `processCache` — all `scope.Add`-ed in the same function that creates them, checked because they are the neighbours of the rule-set ownership block (`route/router.go:151-249`);
  - `ReferenceManager`'s retirement walk — does not include rule-sets, and correctly does not: a rule-set is not a reusable pool (`route/reference.go`, `route/reusable_owner_inventory_test.go`);
  - `rule_network_scope.go`, `rule_action.go` — hold no rule-set state.
- **additional bugs found**: none.
- **explicitly not expanded into**: `common/geoip`/`common/geosite` (§7), the matcher/compiled-rule representation, the `.srs` codec, the remote-download HTTP layer, `ReferenceManager`'s pool inventory.

## 6. Verification

- **tests** (new, all committed):
  - `route/rule/rule_set_updater_lifecycle_test.go` — 3 tests: the updater goroutine stops on `Close` (observed as the HTTP request count ceasing), the fork's sleep gate blocks and releases periodic refreshes, the initial pass is deliberately exempt
  - `route/router_rule_set_lifecycle_test.go` — 4 tests: the real `Router` through all four start stages on a real `adapter.Scope` (rules live → both objects dead after the drain, goroutine stopped); **the failure path** (a remote rule-set whose server answers 500 fails `Start`, and the rollback closes a local file rule-set whose watcher was created but never started); an unreferenced set releases its rules at startup; the `Cleanup` pass runs *after* every rule took its reference
  - `daemon/rule_set_reload_test.go` — 1 test on the real reload path (`StartOrReloadService` → a new Box): live rule-set, rebuilt live on reload, and **absent** after a reload that removes it
- **reload test**: `daemon/rule_set_reload_test.go` (three consecutive Boxes in one process), plus the Scope-drain test at the Router level.
- **updater/watcher check**: updater — the three `route/rule` tests (goroutine exit, sleep gate, initial-pass exemption); watcher — `TestAFailedStartClosesEveryRuleSetItAlreadyOwned` now *drives* the shape that mattered (a watcher created at `Initialize` whose `StartContext` never ran, closed by the rollback) inside a `NotPanics`, so the earlier source-only reading of `fswatch.Watcher.Close` is replaced by an executed guarantee.
- **flake control**: the timing-based tests were repeated — Router tests ×20, updater tests ×10, daemon reload ×5 — with no failures. One assertion from the first draft (the drain test pinning the fetch counter to exactly 1) was a latent flake, because `PostStart` starts the updater whose first timer fires immediately; it now asserts that the fetch happened and lets the rule assertion state the outcome.
- **goroutine/resource check**: the refresh loop is the only goroutine a rule-set owns; its exit is asserted twice (unit and Router level) by request counts, not by `runtime.NumGoroutine`.
- **mutation verification** — each mutation applied, the named test run, the mutation reverted:

  | mutation | test that failed |
  |---|---|
  | updater loop ignores `ctx.Done()` | `TestTheUpdaterDoesNotOutliveItsClose` |
  | `powerGovernor` left nil (the dropped context lookup) | `TestAPeriodicRefreshWaitsForTheDeviceToWake`, `TestTheInitialPassIsNotDeferred` |
  | `scope.Add(ruleSet.Close)` deleted | `TestTheRouterHandsItsRuleSetResourcesToTheScope` |
  | `scope.Add(updater.Close)` deleted | `TestTheRouterHandsItsRuleSetResourcesToTheScope` |
  | `Cleanup` pass moved to `StartStateStart` | `TestTheCleanupPassRunsAfterEveryRuleTookItsReference` |
  | `Cleanup` made a no-op | `TestAnUnreferencedRuleSetDropsItsRulesAtStartup` |
  | `scope.Add(ruleSet.Close)` deleted, viewed from the failure path | `TestAFailedStartClosesEveryRuleSetItAlreadyOwned` |

- **regression result**: `go test` on `./route/rule/` (×2), `./route/` (423 s), `./dns/`, `./adapter/`, `./protocol/tun/`, `./daemon/` — **all PASS**. The new timing-based tests were run three times consecutively without flakes. `go build ./...` with `DEFAULT_BUILD_TAGS_OTHERS` PASS.
- **`-race`**: `BLOCKED_BY_ENVIRONMENT` — this host has no C toolchain (`CGO_ENABLED=1` cannot build `runtime/cgo`), so the lock-free `Match`/`Cleanup` shape in §3 could not be examined under the race detector. That is the one guarantee this audit could not measure.

## 7. GeoIP/GeoSite conclusion

- **any reachable legacy loader**: **no runtime loader exists.**
  - `common/geoip` and `common/geosite` are imported **only** by `cmd/sing-box` (`cmd_geosite.go`, `cmd_geosite_export.go`, `cmd_geosite_matcher.go`) — CLI subcommands, never linked into `route`, `box`, `libbox` or `daemon`.
  - No `geoip.New` / `geosite.New` call site exists outside `cmd/`.
  - A rule carrying `geosite`, `geoip` or `source_geoip` is a **hard error at construction**, not a silent no-op: `route/rule/rule_default.go:124-132` and `route/rule/rule_dns.go:200-205` return "deprecated in sing-box 1.8.0 and removed in sing-box 1.12.0".
  - `option.RouteOptions.GeoIP/Geosite` still exist as vestigial fields marked `schema:"omit"`; nothing reads them at runtime.
- **any runtime cost**: **none in the tunnel process.** The `maxminddb-golang` dependency is reachable only through the CLI's `geosite export` path, so a `sing-box run` / `libbox` build does not load, map or hold any legacy Geo database.
- **action taken**: **none**, deliberately, per §1.1 — there is no reachable legacy loader to fix.

## 8. Final conclusion

- **real optimisation gain**: **none claimed.** Nothing was changed, so nothing improved — and the audit found no waste worth removing in the lifecycle. The two theoretical savings that were considered (suppressing reloads of unreferenced local sets, and the `updateOnce` re-parse-then-drop for an unreferenced remote set) both reduce to upstream-shaped behaviour whose "fix" trades a bounded observation for worse semantics.
- **bugs fixed**: **0.** Seven new tests pin the wiring that previously had none; every one is mutation-verified against the regression it exists for.
- **remaining risk**:
  - **P2 — the lock-free `rules` slice** (`Match` vs `Close`/`Cleanup`/`updateOnce`): upstream's design, benign slice-header teardown in practice, unmeasurable here without `-race`. Not fork-introduced, not fixed.
  - **P2 — `pauseManager` on `RemoteRuleSet` is carried but unused** (upstream field); the sleep gate now lives in the updater, where it is wired and tested. Leaving the dead field is a smaller change than removing it from a type that upstream owns.
  - **P3 — an unreferenced remote set pays a download and a parse every interval, then drops the result** (`updateOnce`'s `refs == 0` branch). Upstream shape; harmless, wasteful, and out of the narrow scope.
- **whether this area is worth further work**: **no.** The conclusion the prompt pre-authorises applies verbatim:

  > 当前 sing-box 1.15 fork 的 rule-set 资源已经正确接入现有统一生命周期，没有发现重复初始化、reload 残留、updater/watcher 泄漏或客户端重复重型持有。GeoIP/GeoSite 也不存在有效运行时负担。本方向继续优化的收益过低，不建议继续投入。

  with one qualification this audit adds: the wiring was **correct but unevidenced**, and it now has evidence that fails when it regresses.

## 9. Git

- **final commit**: `9e5a74a5056` — `test(rule-set): pin the lifecycle wiring that nothing covered` (3 files, +534, tests only)
- **git status**: clean; `testing` ahead of `origin/testing` by 19 commits (the 18 from the two previous campaigns plus this one), **not pushed**
