# Root test hygiene audit (read-only)

## Method

Read-only. No file in `<WORKSPACE_ROOT>\singbox-consolidation` was created, modified, moved, renamed or
deleted. The protected branch `pending/device-axis-reload-window-tests` was **not** read, checked out
or diffed (it appeared only as a name in `git branch -a` output).

Ran, with `PATH` prefixed by mingit + `<WORKSPACE_ROOT>\_gotool\go\bin` (go1.25.5 windows/amd64):

- `git ls-files '*.go'` (no slash) → the real root package is `box.go`, `box_lifecycle.go`,
  `debug.go`, `debug_http.go`, `debug_stub.go`, `debug_unix.go`. **`box_status.go` and
  `box_validation.go` do not exist anywhere in the tree** — the brief's file list is wrong on that
  point; everything else checks out.
- Top-level `func|type|var|const` extraction from the 6 root non-test files → the unexported root
  package surface (`releasePowerGovernor` box.go:55, `applyPauseEvent` box.go:81, `Box` box.go:99,
  `newStatusSurface` box.go:1313, `ensureURLTestServices` box.go:295, `lifecycle` box_lifecycle.go:81,
  `deviceAxis` box_lifecycle.go:70, `reconcileScreenState` :228, `reconcileLockState` :247, …).
- Same extraction over all 28 root `_test.go` → root-only test helpers (`newBoxFromConfig`,
  `statusTestBox`, `statusTestLeaf`, `errStatusNoDial`, `newAppleBridge`, `testClock`, `bridgePolicy`,
  …) and their per-file consumers.
- Per-file package clause, line counts, `//go:build` presence, `func Test…` census and doc comments.
- `go vet -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" .` → **exit 0** (server profile, not the
  Darwin-only `DEFAULT_BUILD_TAGS`, per AGENTS.md).
- `git grep` + a filesystem sweep (3061 files, catching untracked `KNOWLEDGE/*.md` and scratch
  `_consolidation/*.txt`) for every filename and bare stem; `-run`/`go test` scan over `.github/`,
  `Makefile`, `scripts/`.
- `git log --diff-filter=A` per file for provenance (regression gate vs. one-off artifact).
- Full suite was **not** run (per instruction).

**Proof technique for the box_test files.** Go compiles `package box` and `package box_test` in one
directory as two distinct packages, and forbids `box_test` from naming unexported `box` identifiers.
`go vet .` exit 0 therefore *proves* no `box_test` file references an unexported root identifier.
Independently confirmed no `box_test` file imports `reflect`, `unsafe` or uses `go:linkname`, so
there is no internals smuggling.

## Summary

- root tests: 28
- package box (internal): **9**   package box_test (external): **19**
- safe to move: **0**   unsafe to move: **28**   should stay in root: **28**
  - 9 blocked by **package semantics** — permanent, unfixable without rewriting
  - 19 blocked by **one shared root-only fixture** (`newBoxFromConfig`) — mechanical, not semantic
- development-process names found: **4** — `agentf_hop_scalar_ownership_attack_test.go` (attack),
  `box_lifecycle_stress_test.go` (stress), `box_static_validation_final_wire_test.go` (final),
  `box_static_validation_true_final_test.go` (final/true_final)
- files referenced elsewhere in the repo (tracked files): **20** (list in the table). Add
  `box_static_validation_dns_demand_test.go` if untracked `KNOWLEDGE/modules/routing-protocols.md`
  counts. Exactly **3** have no reference anywhere: `box_physicalpath_wire_test.go`,
  `box_status_lifecycle_test.go`, `urltest_services_test.go`.
- **No root test filename appears in `.github/`, `Makefile` or `scripts/`.** CI references no file by
  name, so *no rename breaks a build or a CI filter*.

Two findings that change the parent's plan, stated up front:

1. **The 28 files cannot be split by directory without editing test bodies.** Every one of the 9
   `package box` files names an unexported root identifier or builds `&Box{…}` from unexported fields;
   every one of the 19 `box_test` files calls the root-only fixture `newBoxFromConfig`. A move is a
   KEEP under the governing rule.
2. **These tests are compiled by CI and never executed by it.** `.github/workflows/verify.yml:269`
   runs `go test -run '^$' ./...` — zero tests execute — and no other step targets the root package
   (`verify.yml:403` races `./route/... ./adapter/... …`, never `.`). This is documented at
   `KNOWLEDGE/modules/ci-release.md:54-57` and `:446`.

## Classification table

`unexported root ids used?` = identifier (definition site) → use sites, in the file's own lines.

| file | pkg | class | subsystem | unexported root ids used? | uses Box as harness? | regression gate? | process-named? | referenced elsewhere | move target | MOVE SAFE? | rename suggested | reason |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| agentf_hop_scalar_ownership_attack_test.go | box | E | root status view / physicalpath | yes — `statusTestBox` (box_status_lifecycle_test.go:158)→:41; `statusTestLeaf` (:80)→:31,32,40; `errStatusNoDial` (:41)→:37 | no (builds `&Box{}` via helper) | no | **yes** (`attack`) | KNOWLEDGE/modules/mtu-and-geometry.md:224,273 | — | **no** | yes → `box_status_hop_scalar_copy_test.go` | one-off Agent-F probe; KNOWLEDGE says do not call it a cross-kind test |
| box_close_test.go | box | A | root Box teardown | yes — `&Box{ctx:…, scope:…}` literal on `Box`'s unexported fields (box.go:99-100,135) → :37-40, :97, :117, :133 | no | yes (`closeOnce` fix) | no | KNOWLEDGE/ACCEPTANCE.md:26; 5 docs | — | **no** | no | unexported-field literal is unfixable from any other package |
| box_cross_kind_cycle_test.go | box_test | F | outbound ↔ DNS transport | no root-package ids; **hosts** `newBoxFromConfig` (:91) | yes (`box.New` :96) | yes | no | ACCEPTANCE.md:40; COMMIT-INDEX.md:75; 6 more | — | **no** | no | fixture host for 19 files; moving it orphaning 18 consumers |
| box_fast_path_group_policy_test.go | box_test | B | protocol/group + route/fast_path | no | yes | no | no | KNOWLEDGE/modules/fast-path-group-policy.md:44 (acceptance row :311) | protocol/group | **no** | no | calls `newBoxFromConfig` ×6 |
| box_lifecycle_ordering_test.go | box | A | root lifecycle ↔ common/power | yes — `applyPauseEvent` (box.go:81)→:90; `lifecycle{…}` (box_lifecycle.go:81)→:93,:122; `deviceAxis` (:70) via `probingAxis` :134 | no | yes | no | ACCEPTANCE.md:121; INVARIANTS.md:174,342 | — | **no** | no | pins release ORDER of the real bridge |
| box_lifecycle_reconcile_test.go | box | D | root lifecycle ↔ common/power | yes — `reconcileScreenState` (box_lifecycle.go:228)→:77,200,226,252; `reconcileLockState` (:247)→:78,201,227,253 | no | yes (`645d96b4`) | no | scratch only | — | **no** | no | names two unexported methods |
| box_lifecycle_stress_test.go | box | A | root lifecycle ↔ common/power | yes (indirect) — `newAppleBridge`/`bridgePolicy`/`newTestClock` (box_lifecycle_test.go:61,102,40), whose signatures carry unexported `lifecycle`//`*testClock` → :33-36,:87-90,:128-129,:179-180 | no | no | **yes** (`stress`) | ACCEPTANCE.md:27; apple-screen-state-acceptance.md ×4 | — | **no** | yes → `box_lifecycle_cycles_test.go` | cannot compile outside `package box` |
| box_lifecycle_test.go | box | A | root lifecycle ↔ common/power | yes — `applyPauseEvent` (box.go:81)→:70; `lifecycle{}` (box_lifecycle.go:81)→:72 | no | yes | no | docs/fork/apple-screen-state-acceptance.md ×5 | — | **no** | no | defines the shared package-`box` lifecycle fixtures |
| box_physicalpath_networks_test.go | box_test | B | common/physicalpath + route | no | yes (`instance.Start()`) | yes | no | ROUTE_AWARE…_FINAL.md; scratch | common/physicalpath | **no** | no | calls `newBoxFromConfig` ×5 |
| box_physicalpath_route_aware_test.go | box_test | B | route reachability → physicalpath | no | yes | yes | no | KNOWLEDGE/modules/path-selection-validation.md:279 | route | **no** | no | calls `newBoxFromConfig` ×5 |
| box_physicalpath_route_dedup_test.go | box_test | D | common/physicalpath reporting | no | yes | yes (`a64058d1d`) | no | ROUTE_AWARE…_FINAL.md; scratch | common/physicalpath | **no** | no | calls `newBoxFromConfig` ×2 |
| box_physicalpath_test.go | box_test | C | common/physicalpath ↔ Box | no | yes | yes | no | docs/fork/physicalpath-controlpath-model.md:118 | common/physicalpath | **no** | no | calls `newBoxFromConfig` ×4 |
| box_physicalpath_unconstrained_test.go | box_test | D | route `final` residual | no | yes | yes (`f2dd95d66`) | no | path-selection-validation.md:336,371 | route | **no** | no | calls `newBoxFromConfig` ×6 |
| box_physicalpath_wire_test.go | box_test | C | real wire through Box | no | yes (+SOCKS/UDP/TCP echo servers :213-270) | yes | no | **none** | — | **no** | no | calls `newBoxFromConfig` ×4; also local helpers `freeTCPPort`/`start*` |
| box_power_test.go | box | A | root ↔ common/power bridge | yes — `releasePowerGovernor` (box.go:55)→:90,105,108; `applyPauseEvent` (box.go:81)→:33-62,:142-162 | no | no | no | docs/fork/lifecycle-scope-upstream-sync-report.md | — | **no** | no | unit-tests two unexported root functions |
| box_static_validation_consumer_demand_test.go | box_test | D | httpclient demand | no | yes | yes (`ef105942c`) | no | scratch only | — | **no** | no | calls `newBoxFromConfig` ×3 |
| box_static_validation_detour_test.go | box_test | D | httpclient detour | no | yes | yes (`fd6c44dcf`) | no | CORE_0.1.6_FINAL_CLOSURE_REPORT.md | — | **no** | no | calls `newBoxFromConfig` ×4 |
| box_static_validation_dns_demand_test.go | box_test | D | dns transport demand | no | yes | yes (`fc7b0f17d`) | no | untracked routing-protocols.md:17 | — | **no** | no | calls `newBoxFromConfig` ×3 |
| box_static_validation_false_deny_test.go | box_test | D | box_validation (in box.go) | no | yes | yes (`133e6ed4f`) | no | ACCEPTANCE.md:38 | — | **no** | no | calls `newBoxFromConfig` ×2; 11-case matrix |
| box_static_validation_final_wire_test.go | box_test | D | cross-kind adapter facts | no | yes | yes (`ff0040079`) | **yes** (`final`) | ACCEPTANCE.md:40; path-selection-validation.md:338 | — | **no** | yes → `box_static_validation_published_facts_test.go` | calls `newBoxFromConfig` ×8 |
| box_static_validation_no_crash_test.go | box_test | D | validation diagnostics | no | yes | yes (`133e6ed4f`) | no | CORE_0.1.6_FINAL_CLOSURE_REPORT.md; archive | — | **no** | no | calls `newBoxFromConfig` ×1, local `svStartWithoutPanic` |
| box_static_validation_ownership_parity_test.go | box_test | D | physicalpath ownership | no | yes | yes (`cf5a25bbe`) | no | scratch only | — | **no** | no | calls `newBoxFromConfig` ×3 |
| box_static_validation_ownership_test.go | box_test | D | physicalpath ownership | no | yes | yes (`91f595906`) | no | CORE_0.1.6_FINAL_CLOSURE_REPORT.md | — | **no** | no | calls `newBoxFromConfig` ×4 |
| box_static_validation_resolver_parity_test.go | box_test | D | dialer resolver parity | no | yes | yes (`04591f8ac`) | no | scratch only | — | **no** | no | calls `newBoxFromConfig` ×2 |
| box_static_validation_true_final_test.go | box_test | D | validation contracts | no | yes | yes (`31f250ff1`) | **yes** (`true_final`) | ACCEPTANCE.md:39 | — | **no** | yes → `box_static_validation_provable_demand_test.go` | calls `newBoxFromConfig` ×8 |
| box_status_lifecycle_test.go | box | A | root status view lifecycle | yes — `newStatusSurface` (box.go:1313)→:165; `&Box{ctx,scope,statusView,statusResolver}` literal →:169-174 | builds `&Box{}` directly | yes (`d1292bff5`) | no | **none** | — | **no** | no | needs `newStatusSurface` **and** unexported fields |
| box_status_test.go | box_test | A | root status surface | no | yes | yes (`d1292bff5`) | no | docs/fork/v016-nextstage-final-report.md | — | **no** | no | calls `newBoxFromConfig` ×8 + 2 local walk helpers |
| urltest_services_test.go | box | A | common/urltest injection | yes — `ensureURLTestServices` (box.go:295)→:29,47,67,86,98,99,143,152,163,172 | no | yes (`a1d7c66c1`) | no | **none** | — | **no** | no | unit-tests an unexported root function |

## Files that MUST stay in root, and precisely why

**Nine files, hard-blocked. Moving any of these is impossible in principle — there is no rewrite that
preserves content, because the names do not exist outside the directory.**

1. `box_close_test.go` — `&Box{ctx: ctx, scope: scope}` at **:37-40, :97, :117, :133**. `Box`'s
   `ctx` and `scope` fields are unexported (`box.go:100`, `box.go:135`). No exported constructor
   builds a Box with only these two set, so this test has no external form.
2. `box_status_lifecycle_test.go` — `newStatusSurface` at **:165** (defined `box.go:1313`, unexported),
   plus the `&Box{ctx:…, scope:…, statusView:…, statusResolver:…}` literal at **:169-174**
   (`box.go:100,135,147,148`). Two independent hard blockers.
3. `box_power_test.go` — `releasePowerGovernor` **:90, :105, :108** (`box.go:55`) and
   `applyPauseEvent` **:33, :36, :39, :42, :47, :48, :56, :57, :62, :142, :148, :157, :158, :159,
   :162** (`box.go:81`).
4. `box_lifecycle_test.go` — `applyPauseEvent` **:70** (`box.go:81`); `lifecycle{governor:…, device:…}`
   **:72** (`box_lifecycle.go:81`, unexported type, unexported fields). Also the fixture host for
   `box_lifecycle_stress_test.go` and `box_lifecycle_reconcile_test.go`.
5. `box_lifecycle_ordering_test.go` — `applyPauseEvent` **:90**; `lifecycle{…}` **:93, :122**;
   the `deviceAxis` contract **:132-134** (`box_lifecycle.go:70`).
6. `box_lifecycle_reconcile_test.go` — `bridge.reconcileScreenState` **:77, :200, :226, :252** and
   `bridge.reconcileLockState` **:78, :201, :227, :253** (`box_lifecycle.go:228`, `:247`).
7. `box_lifecycle_stress_test.go` — calls `newAppleBridge` **:36, :90, :129, :180**,
   `newTestClock` **:33, :87, :128, :179** and `bridgePolicy` **:34, :88, :129, :180**, all defined in
   `box_lifecycle_test.go` (`:61`, `:40`, `:102`) and carrying the unexported `lifecycle` type and
   `*testClock` in their signatures. It cannot compile outside `package box`.
8. `urltest_services_test.go` — `ensureURLTestServices` **:29, :47, :67, :86, :98, :99, :143, :152,
   :163, :172** (`box.go:295`).
9. `agentf_hop_scalar_ownership_attack_test.go` — `statusTestBox` **:41** and `statusTestLeaf`
   **:31, :32, :40** plus `errStatusNoDial` **:37**, all defined in `box_status_lifecycle_test.go`
   (`:158`, `:80`, `:41`) in `package box`. `statusTestLeaf`'s fields (`leafType`, `state`,
   `dependsOn`, `lastError`, `lastErrorPhase`) are unexported, so the helper cannot be exported to a
   child package without rewriting it — and file 2 above forces its host to stay in `package box`.

**Nineteen `box_test` files, mechanically blocked.** None names an unexported root *package*
identifier (proved by `go vet .` = exit 0 and by the absence of `reflect`/`unsafe`/`go:linkname`).
All nineteen call `newBoxFromConfig`, defined once at `box_cross_kind_cycle_test.go:91`, and that
helper is visible only to tests in the root directory.

## Files that could move, and the exact blocker or non-blocker

- **Blocker, all 19 `box_test` files:** `newBoxFromConfig` (`box_cross_kind_cycle_test.go:91-101`).
  Its body uses **only exported API** — `include.Context`, `json.UnmarshalExtendedContext[option.Options]`,
  `box.New(box.Options{…})` — so the helper is **portable**: copied verbatim into a child package it
  compiles unchanged. The blocker is therefore *mechanical, not semantic*: moving one of the 18
  consumers requires either duplicating those 10 lines (adds code — not "content unchanged"), or
  extracting them to a shared exported test-support package (adds a package), or moving all 19
  together.
- **Non-blocker, worth stating:** `box_static_validation_*` and `box_physicalpath_*` files import **no
  child package under test** — only `include`, `option`, and (in `box_status_test.go`)
  `common/physicalpath`. Their subject is the root `Box`'s start-time behaviour reached through the
  real production config loader, which is exactly the class-A/C reason to keep them in root. The
  "subsystem" column names the code they exercise, not the package they belong to.
- **Not movable at all:** the 9 `package box` files above. Changing only their package clause
  (`box` → `box_test`) is equally impossible — the same identifiers vanish.
- **Content-preserving change that would unblock nothing:** none. There is no edit that makes any of
  the 28 files movable while leaving its assertions intact.

## Rename recommendations

No rename is required for correctness, and **no rename breaks any reference** — CI, `Makefile` and
`scripts/` reference no root test filename, and `KNOWLEDGE/ACCEPTANCE.md`'s re-check commands filter
by *test function name* (`-run TestFalseDenyMatrix .`, `-run TestCrossKind .`), not by filename. The
file `_consolidation/*.txt` hits are untracked scratch lists.

Recommended, cosmetic only, and each requires updating the prose that names the old file:

| file → proposed | reason | references to update |
|---|---|---|
| `agentf_hop_scalar_ownership_attack_test.go` → `box_status_hop_scalar_copy_test.go` | "agentf"/"attack" is a development-process label; the file proves `PathStatus.Hops` scalars are copies | `KNOWLEDGE/modules/mtu-and-geometry.md:224` and `:273` (the caution quotes the old name) |
| `box_static_validation_true_final_test.go` → `box_static_validation_provable_demand_test.go` | "true_final" is a process marker; content is provable-only demand + resolver graph edge | `KNOWLEDGE/ACCEPTANCE.md:39` |
| `box_static_validation_final_wire_test.go` → `box_static_validation_published_facts_test.go` | "final_wire" is a process marker; content is cross-kind adapter facts | `KNOWLEDGE/ACCEPTANCE.md:40`; `KNOWLEDGE/modules/path-selection-validation.md:338` |
| `box_lifecycle_stress_test.go` → `box_lifecycle_cycles_test.go` | "stress" implies a load harness; it is 4 bounded cycle/leak assertions | `KNOWLEDGE/ACCEPTANCE.md:27`; `docs/fork/apple-screen-state-acceptance.md:661-670` |

If the parent wants *zero* documentation churn, rename none of them: the names are bad but harmless.

## Names deliberately NOT changed

- **`box_cross_kind_cycle_test.go`** — no process name, but do **not** rename or move it. It is the
  single host of `newBoxFromConfig` for 19 files; any change to it has the largest blast radius in
  the set.
- **`box_close_test.go`, `box_power_test.go`, `box_status_test.go`, `box_status_lifecycle_test.go`,
  `box_lifecycle*.go` tests** — names carry no process marker and each maps 1:1 to a documented
  acceptance row or a `box.go`/`box_lifecycle.go` construct. Renaming would desynchronise
  `KNOWLEDGE/ACCEPTANCE.md` §1.6, §1.7, §9.1 for no gain.
- **`box_static_validation_false_deny_test.go`** — "false_deny" is domain vocabulary (the validator
  must not over-refuse), not a process marker; `KNOWLEDGE/ACCEPTANCE.md:38` names it and
  `TestFalseDenyMatrix`.
- **`box_physicalpath_wire_test.go`, `urltest_services_test.go`,
  `box_static_validation_ownership_parity_test.go`, `box_static_validation_resolver_parity_test.go`**
  — "wire"/"parity" describe the technique, not a development stage.

## Checks blocked by environment

None. Every check the brief asked for was run:

- Compile/vet of the root package and all 28 root test files: `go vet -tags "$(cat
  release/DEFAULT_BUILD_TAGS_OTHERS)" .` → **exit 0** (unexported-identifier usage in `box_test`
  files is thereby excluded).
- `//go:build` tags: **none** in any of the 28 files (checked all headers).
- Filename references: `git grep` plus a 3061-file filesystem sweep covering untracked
  `KNOWLEDGE/*.md` and `_consolidation/*.txt`; `-run`/`go test` scan of `.github/`, `Makefile`,
  `scripts/`.

Deliberately **not** run, per instruction: the full test suite. Two runs that would raise confidence
if the parent wants them, both slow and both outside this mandate:

- `go test -run '^$' -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" .` — compiles the root test
  binary but executes nothing; confirms what `go vet` already showed.
- `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run '<Test>' .` — the
  `KNOWLEDGE/ACCEPTANCE.md` re-check form. Useful as a **before/after oracle**: capture the passing
  test list now, and confirm it is byte-identical after any hygiene change.

One environment caveat worth recording: `release/DEFAULT_BUILD_TAGS` is the **Darwin** profile and
cannot build on this Windows host; `DEFAULT_BUILD_TAGS_OTHERS` was used throughout, per
`KNOWLEDGE/modules/ci-release.md` AI caution 4.

**Concurrency observation, not caused by this audit.** Between my first and last `git status` calls,
another actor modified six tracked files in this shared workspace: `KNOWLEDGE/BASELINE.md`,
`KNOWLEDGE/INDEX.md`, `KNOWLEDGE/modules/README.md`, `KNOWLEDGE/modules/core-lifecycle-runtime.md`,
`KNOWLEDGE/modules/data-path-optimization.md`, `KNOWLEDGE/modules/fast-path-group-policy.md`. This
audit wrote no file inside the repository: `git status --short -- '*.go'` is empty and the root test
count is still 28. The edit to `fast-path-group-policy.md` did shift one cited line (the
`box_fast_path_group_policy_test.go` acceptance row moved `:294` → `:311`); the citation above uses
the current number. Line-number citations into `KNOWLEDGE/` and `docs/` are the least stable part of
this report — the `file:line` citations into root `.go` and `_test.go` files are stable, since no Go
file changed.
