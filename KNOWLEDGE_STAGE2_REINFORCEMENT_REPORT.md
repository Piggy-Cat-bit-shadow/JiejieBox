# Stage 2 knowledge-base reinforcement report

Independent re-verification and strengthening of the `Piggy-Cat-bit-shadow/sing-box` fork KNOWLEDGE
base. Stage 1 produced a usable draft; this round re-derived every load-bearing claim from source and
Git, rebuilt the subsystem map, reinforced commit provenance, and reconciled the repository's
competing historical documents.

```
KNOWLEDGE_STAGE2 = COMPLETE

PRODUCTION_CODE_CHANGED = NO
RUNTIME_BEHAVIOR_CHANGED = NO
PROTOCOL_BEHAVIOR_CHANGED = NO
ABI_CHANGED = NO
GIT_HISTORY_REWRITTEN = NO

RELEASE_BASELINE_VERIFIED = YES
PRODUCTION_BASELINE_VALID = YES
UPSTREAM_BASELINE_VERIFIED = YES

STAGE1_CLAIMS_REAUDITED = YES
FORK_DELTA_REAUDITED = YES
COMMIT_PROVENANCE_REINFORCED = YES
SUBSYSTEM_MAP_REBUILT = YES
INVARIANTS_REAUDITED = YES
EVIDENCE_LEVELS_REAUDITED = YES
OLD_DOCS_RECONCILED = YES

CROSS_FILE_CONSISTENCY = PASS
FUTURE_AI_INCREMENTAL_READ = PASS

READY_FOR_FINAL_KNOWLEDGE_REVIEW = YES
```

---

## A. Baseline

All values measured on the worktree; the command that produced each is in `KNOWLEDGE/BASELINE.md`.

| Field | Value |
|---|---|
| Repository | `Piggy-Cat-bit-shadow/sing-box` |
| Current branch (knowledge revision) | `testing` |
| Current HEAD | `f45f2734130bab12e9c69d2aad37fae12113f896` (Stage-1 docs commit, 2026-10-12) |
| **Production baseline** | `a267e99e0bf1811dc975b013e2e8d7a15ab40d23` |
| Release baseline | tag **`v0.1.6`** = the same SHA, 2026-10-12 11:53:53 +0800 |
| **Upstream baseline** | `e4b7e692f4545890bfc9d43960328e1449fa803b` = `refs/remotes/upstream/testing` |
| **Fork branch point** | `0d91c4547921a7b72e9c9913e2bc6f3a556801e2` (2026-09-22) |
| First fork-authored commit | `d53c867b7` — "feat(http): add inbound masquerade handler" |
| Stage-1 date | 2026-10-12 |
| Stage-2 review date | 2026-10-12 |
| **Production delta after baseline** | **none** — `v0.1.6..HEAD` is exactly one documentation commit |
| Fork range | `e4b7e692..a267e99` = **1,834** commits, 1,474 files, +403,956 / −6,207 |
| Ancestry check | ancestors(`a267e99`) = 4,661 = 2,827 inherited + 1,834 fork-side |

**Stage-1's recorded SHA values were confirmed correct.** Its framing was not: it treated `e4b7e692`
as the fork point, when `e4b7e692` is the **upstream tip and an ancestor** of the fork baseline, so it
cannot be a divergence point. The real branch point is one commit below the first fork-authored
commit.

### A.1 The provenance model — the largest correction in this round

The first reading of this repository assumed `e4b7e692..a267e99` was "1,834 fork-authored commits".
It is not. Measured decomposition:

| Class | Count | Test |
|---|---|---|
| **Fork-authored** | **1,505** | fork-side **and** no upstream twin |
| **Upstream-authored orphans** (pre-rewrite copies) | **329** | fork-side **but** an upstream twin exists |

Mechanism, verified against the live GitHub API for `SagerNet/sing-box`: **upstream force-rewrites its
`testing` branch.** All 15 commits of the replayed `v1.15.0-alpha.11` line share one committer
timestamp (`2026-10-09T12:37:25+08:00`) while their author dates span 2026-09-01 → 2026-09-28 — the
signature of a replay. The fork forked from the **pre-rewrite** lineage and kept the original objects,
so its history carries content-duplicates of work already inside the baseline's ancestry.

Confirmed beyond the heuristic: three pairs are **patch-id identical** and two files are **blob
identical**. The 343 `世界`-authored commits in the range carry only **61 distinct subjects** (up to 11
copies each), so counting them individually inflates upstream work ~5.6×.

**Two traps this creates, both now documented:**

1. **The provenance test's direction was inverted in this project's own tooling.** 
   `git rev-list --count <sha> --not --remotes=upstream` returns **0 for upstream-side and ≥1 for
   fork-side**. An earlier working assumption had it backwards, which would mislabel all 1,834
   commits. `BASELINE.md` now carries the command, both expected values, and a second confirming test.
2. **The test compares branches, not tags.** `6afeff4c0` is an ancestor of **none** of the 42 upstream
   remote refs yet **is** tagged upstream (`v1.15.0-alpha.11`). So the honest statement about the
   fork's own `upstream-53-ledger.md` is *"reachable from no current upstream branch"*, not *"no
   upstream ref"*.

### A.2 Release identity

`v0.1.6` = `a267e99e0`. The fork's `v0.1*` tags share one namespace with upstream's 633 `v1.*` tags, and
**`v0.1.0` is an upstream tag** (`0ca7d1595`, 2022-08-12, 世界) — there is no `v0.1.1` or `v0.1.2`.
`git describe --tags e4b7e692` reports `v1.14.3-38-ge4b7e692f`, and `v1.14.3` points at `7054cac51`:
**`git describe` must not be used for provenance or release identity.** Every fork *release* tag is
annotated (a counting trap); the fork also has lightweight tags, so this is a rule about release tags
only.

---

## B. Parallel audit

### B.1 Sub-agents used

Fourteen delegations across two layers, all in one shared worktree with read-only discipline; the
master agent performed the convergence, the conflict resolution and every file written under
`KNOWLEDGE/`.

| Layer | Agent | Focus | Deliverable |
|---|---|---|---|
| 1 | A | current source truth, no knowledge files read | 1,306 lines |
| 1 | B | fork Git evolution, 40 theme clusters, 236-row index | 738 lines |
| 1 | C | verification truth, evidence levels, CI gates | 815 lines |
| 1 | D | upstream relationship, twin ledger | 587 lines |
| 1 | E | adversarial critique of Stage-1 | 71 numbered findings |
| 1 | F | historical reports / docs archaeology | 608 lines + 155-row inventory |
| 2 | S | sniff, independent | 696 lines |
| 2 | P | chain proxy / detour / ControlPath | 1,124 lines |
| 2 | O | Direct Offload, traffic class, scheduler | 1,107 lines |
| 2 | R | build / pruning / release artifacts | 680 lines + reachability analyzer |
| 2 | M | MTU / geometry / buffer ownership | 910 lines |
| 2 | L | libbox / platform contract | 1,120 lines |
| 2 | N | DNS | 1,578 lines + 4 CSV artefacts |
| 2 | V | **adversarial verification of the KB claims** | 31 claims, 681 lines |

Three master-agent findings were dispatched into running agents for adjudication rather than accepted:
the Apple ABI pin, the `ControlPath` documentation contradiction, and the release-tree allowlist.

### B.2 Scope actually covered

| Measure | Value |
|---|---|
| Fork commits coarse-scanned | **1,834 / 1,834** (raw listings kept) |
| Commits deep-read (full diff) | ~330 across 40 theme clusters |
| Subsystems audited | 15 (the 13 modules plus the two explicitly not covered) |
| Test commands actually executed | **27**, including 8 full suites and 3 build configurations |
| Tests observed passing at the baseline | **>1,400** (1,317 in one MASQUE+geometry run alone) |
| Root design-guarantee suite | **green**, `ok 5.459 s`, exit 0 (correct tag set) |
| Historical documents inspected | **155** (`.md`/`.txt` = 103 files / 2.03 MB; `docs/fork` all files = 115 / 9.99 MB) |
| SHA tokens in the final KB | **249**, all resolving to real commits |
| CI gate scripts checked for reachability | **107 / 107** (75 reachable, 32 unreachable) |

### B.3 The eight tasks the knowledge base must support

Every one was simulated against the *new* module set; the results are in §G.

---

## C. Stage-1 review

Stage-1 was read as **claims**, not as a source. Every SHA it cited resolves (65/65), and its baseline
SHAs were correct. Its framing, coverage and evidence grading were not.

| Category | Count | Notes |
|---|---|---|
| **CONFIRMED** | 14 | the Scope ownership rules; epoch monotonicity; `IdleConnectionReleaser` vs `IdleConnectionKeeper`; ControlPath≠PhysicalPath as a *concept*; read-only validation *in intent*; provable-only demand; control groups are not hops; Apple one-source/one-Libbox; branding≠identity; release re-verifies bytes; MASQUE bootstrap vs inner resolver; the three MTU quantities; the 1,834-commit range; `RequiredNetworks` as a follow-up |
| **CORRECTED** | 17 | see §E — the eight that change a reader's decision are listed there |
| **MISSING** | 20 subsystems | `common/trafficsched`, `common/trafficclass`, `protocol/group/loadbalance`, `route/failover.go`, `route/bypass_verdict.go`, `common/dialer/profile.go`, `protocol/direct`, `common/bufgeom`, `dns/transport/group`, `internal/memmetrics`, `transport/v2rayxhttp`, `protocol/vless/encryption`, `cmd/internal/*`, `option/*`, `daemon/*`, `include/*` tag polarity, `test/jiejie`, `test/interop`, `.fpm_*`/Docker packaging, and `docs/FORK-DIFF.md` itself |
| **DOWNGRADED_INVARIANT** | 3 | two rules restated as conventions; one blanket `VERIFIED` split into per-rule evidence with two rows downgraded |
| **UPGRADED_EVIDENCE** | 2 | the sniffer's QUIC ownership proof (address-range + poisoning + `cap` bound); the lifecycle ordering tests' vacuity guards |
| **REMOVED_DUPLICATION** | 1 | the `Path.PhysicalPath` type, which does not exist |
| **NEW_PROVENANCE** | 46 | the evolution topics in `COMMIT-INDEX.md`, each with introducing/correcting/hardening/superseded commits |
| **SUPERSEDED** | 3 documents | plus 4 more marked for archival |
| **REFUTED / STALE** | 5 | the Apple pin framing; the DBus leak; the release-tree allowlist; `verify-evidence/`; `docs/BUILD-PROFILES.md` |

**Stage-1's own measured limits were honest** and are preserved: all 58 of its SHAs resolved, and its
"no legacy document was safely removable" disposition matched this round's conclusion (`DELETE = 0`).

---

## D. Knowledge structure changes

The user-facing template suggested 11 modules. The real code and history cluster into **13**, and the
count is a consequence of the evidence, not of a target.

| Action | Count | Detail |
|---|---|---|
| Modules **retained** in concept | 3 | `core-lifecycle.md` → `core-lifecycle-runtime.md`; `routing-protocols.md` → split into `path-selection-validation.md` + `dns-demand.md` + `data-path-optimization.md` + `masque-connectip.md` + `sniff.md`; `platform-release.md` → split into `build-pruning-release.md` + `ci-release.md` + `libbox-platform-contract.md` |
| Modules **split** | 3 → 10 | the reason is stated in `modules/README.md`: each Stage-1 module mixed subsystems with **no shared invariant**. `routing-protocols.md` covered sniff, MASQUE, Naive, DNS, routing and validation; a reader changing sniff was forced through MASQUE and DNS knowledge. |
| Modules **added** | 3 | `naive-http-transport.md` (Stage-1 mislabelled a 64-commit subsystem as a dependency bump); `buffer-ownership.md` (the fork's largest correctness surface, with no home); `mtu-and-geometry.md` (three separate quantities Stage-1 compressed into a phrase) |
| Modules **removed** | 3 | `core-lifecycle.md`, `platform-release.md`, `routing-protocols.md` — all content carried forward; verified that nothing outside `KNOWLEDGE/` referenced them |
| Root files **rewritten** | 6 | `BASELINE.md`, `PROJECT.md`, `ARCHITECTURE.md`, `INVARIANTS.md`, `ACCEPTANCE.md`, `COMMIT-INDEX.md`, `README.md` |
| `AGENTS.md` **rewritten** | 1 | now names the three things that most often go wrong here |
| Constraint **honoured** | — | every module declares its path set, so `AGENTS.md`'s "map changed paths to modules" is now implementable |

Size: **194 lines → 369 KB across 21 files.** The growth is not the goal and is not the success
criterion; §G is.

**A safety property was added.** `modules/README.md` states that the map and the modules are committed
together, and that a link which does not resolve is a defect rather than a missing document. That
matters because this round edited the knowledge base **in place** in a shared worktree; a mid-edit
reader could see a map pointing at files that did not exist yet. The final state was checked: all 20
backticked module references resolve.

---

## E. Major corrections

Only the corrections that change what a future AI would **do**. Each was independently verified.

### E.1 The Apple pin has no wired verifier — stated precisely

Stage-1's `ACCEPTANCE.md` marked the Apple row "pinned source, ABI, provenance … VERIFIED" and cited
`check-libbox-abi.sh` as an evidence location. The facts:

* `check-libbox-abi.sh:98` holds `VERIFIED_APPLE_SHA="90a0e9ae…"`; `git ls-tree a267e99 clients/apple`
  is `d0524912…`, so the script's own layer 5 **would fail**.
* **Nothing invokes that script** — not a workflow, not another script, not a `Makefile` target.
* `client-apple.yml` is **not** in `verify-release-acceptance.sh`'s required set, and `verify.yml` does
  not run the ABI gate, so release acceptance is **structurally incapable** of noticing the drift.
* The released revision `d0524912…` appears in **no** tracked non-`.git` file.
* Every check that **does** run is self-referential to the gitlink: they prove iOS and macOS agree, and
  forbid a second SHA — they cannot say the revision is the *intended* one.
* `c7014ddc1` is proof by demonstration: it moved the gitlink 33 minutes before the release commit and
  **nothing objected**.

> **Correct wording: "the Apple pin has no wired verifier, and the one candidate verifier is both
> unwired and stale."** Not "the gate is broken" — that implies a gate that runs. What is `UNVERIFIED`
> is ABI call-site compatibility at the pinned revision; the recorded PASSes are two pin generations
> earlier. Packaging, signing-environment, branding, naming and source-selection gates **are** wired.

### E.2 The release path is broken as wired — a production follow-up

`release.yml:69` sets `RELEASE_EXTRA_PLATFORMS: true`, expanding the contract to four targets, and the
`target_field` table was extended for Windows and Android. **The release-tree allowlist was not:**
`release-artifacts.sh:575-592` accepts only `{binary, binary.sha256, buildinfo, LICENSE}` per target,
while Windows uploads three more files and Android two more. `verify` therefore dies with
`unexpected file(s) in the release tree`, and the gate's self-test cannot catch it (its fixtures are
still the two-product ones).

### E.3 `verify-evidence/` is stale by its own written rule

All 13 logs name `### HEAD: cfc77dc51…`, **101 commits behind** the release baseline, and its own
`README.md:7-8` says such a log *"is stale and must be regenerated rather than trusted"*. Worse: 4 of
the 10 covered scripts changed **after** that HEAD, including `check-libbox-abi.sh` and
`clients/apple` itself. So **anything at the production baseline resting only on `verify-evidence/` is
`UNVERIFIED` at the baseline.** The directory's `PARTIAL … EXIT=0` transcript is also the clearest
example in the repository of a gate that skips and reports success.

### E.4 The `systemd-resolved` leak is **fixed**, and the document justifying its own CI exclusion is stale

`docs/RELEASE-CERTIFICATE.md:70-85` records the leak as unfixed and uses it to keep the `test/` module's
box-level tests out of push CI. `git log -S'signalLoopStop' e4b7e692..a267e99` returns exactly
`6b495134c`, which added `signalLoopStop`/`signalLoopDone`/`signalLoopOnce`, a stop-channel `select`,
`RemoveSignal`, and a **join** in `Close()` — all present at `a267e99`. **Two consequences:** do not
re-report the leak, and **do not apply the fix that document proposes** (closing the dbus signal
channel would panic, because dbus is the sender).

### E.5 `Path.PhysicalPath` does not exist

Stage-1 and one design document both implied a `PhysicalPath` type. `git grep 'type PhysicalPath'`
returns nothing. `Path` has `ControlPath []string` and `Hops []Hop`; `PhysicalPath []string` is a field
of a **different type**, `PathNode`. The correction matters because the whole point of the model is
that the two lists have different orders and different memberships.

### E.6 Two documents contradict the code on `ControlPath` ordering, and one is 51 minutes stale

`reversePacketOrder` reverses `Hops` and **deliberately does not reverse `ControlPath`** — fixed by
`d46717dbd` at 12:54:53. `docs/fork/physicalpath-controlpath-model.md` explicitly states the opposite
and was last touched at 10:03:43, by `e201b5add`. The same file still lists a test name that was
renamed. **The code is right; the design document is 51 minutes behind it.**

### E.7 The build-profile document contradicts the profile files and the workflows

`docs/BUILD-PROFILES.md:43-44` says the *client* profile "adds `with_utls`". **`with_utls` is in both
profile files; `with_naive_outbound` is the only difference.** The same document's claim that "no
fork-side list to update, and no CI step that has to be told about it" is false: registering a
capability touches **nine** places, including a 30-path list in a workflow, a ~55-case capability
script and frozen golden tag strings for eight platforms. And its claim that push workflows run "one
product build and capability/config/runtime checks" is false — `verify.yml` does none of those.

### E.8 A shipped capability is described as deleted

`scripts/ci/build-macos-client.sh:19-22` tells a reader that `with_clash_api` was removed and "the Clash
API was deleted from this fork". **All three clauses are false at the release baseline**: the file the
script reads contains the tag, `include/clashapi.go:1` names it, and the Clash API is probed live by CI.
The identical claim in `build-server.sh` was retracted in place; the macOS half never was. This is the
single most dangerous stale comment found, because it tells a reader a shipped capability is gone.

### E.9 Reproducibility traps in the test commands themselves

* The root design-guarantee suite is **green** — but only with `DEFAULT_BUILD_TAGS_OTHERS`. Untagged it
  fails 12 tests on tag absence; with `DEFAULT_BUILD_TAGS` it does not even build. A reviewer following
  `CORE_0.1.6_FINAL_CLOSURE_REPORT.md:18`'s stated tag source gets a false "broken" reading.
* **No CI job executes the root suite at all.** `verify.yml`'s untagged step runs `go test -run '^$'`
  (zero tests) and its race step's package list omits `.`. All 25 root contract test files are compiled
  by CI and never run by it.
* `make test` cannot pass: its first command is untagged, and `TAGS_TEST` is never defined.

### E.10 The "3 pre-existing environmental failures" set does not reproduce

Reports name `common/windivert`, `common/tlsspoof` and `common/tls`. Measured here: **25 PASS / 0 FAIL**
and **9 PASS / 0 FAIL** for the first two, including every `TestIntegration*` the reports call
`windivert: open SCM: Access is denied`. The claim described a machine's WinDivert/Administrator state,
not a property of the tree.

### E.11 A known-bogus cost figure is still quoted in a design document

`docs/fork/direct-offload.md:210` states "1461 ns, 2856 B, 26 allocs" for userspace flow setup. The
benchmark file **names that number as its own fixture's cost** — *"a benchmark whose number describes
its own fixture is worse than no benchmark, because it is quoted"* — and it was fixed two days after the
document quoted it. Re-measured: **104 B, 2 allocs**.

### E.12 The fork's own documents have already retracted things their bodies still assert

`docs/fork/v016-final-closure-ledger.md` retracts its own section in a banner at `:4-13` while the body
at `:465` still says the hysteria2 ceiling is BLOCKED. This is the general shape of every long ledger
here, and `ci-release.md` now teaches it as a rule: **the banner is current, the body is a record.**

---

## F. Commit provenance

Per subsystem, with the counts a future reader can act on. Full evolution tables are in
`COMMIT-INDEX.md` and each module's **Provenance** section.

| Subsystem | OWN | UPSTREAM-INHERITED | UPSTREAM-ABSORBED | EXTERNAL-ADAPTED | SUPERSEDED / ARCHIVE-ONLY |
|---|---|---|---|---|---|
| Path selection & validation | 16 | — | — | — | 5 documents |
| Core lifecycle & runtime | 13 | 4 anchors | 1 | — | — |
| Sniff | 9 | — | 3 | — | — |
| DNS | 16 files / 20 pairs | 1 (`dhcp.go` body) | 20 | — | 5 |
| MASQUE / CONNECT-IP | 24 | 1 (`b83089ec`) | — | 3 (quic-go/masque-go, connect-ip-go) | 2 |
| Native Naive / HTTP | 22 | — | 3 | 1 (`klzgrad/forwardproxy@naive`) | 2 |
| Data path (offload/class/sched) | 22 | — | — | 1 (QUIC defaults) | 1 doc row |
| Power & mobile lifecycle | 16 | 1 family (×11 copies) | — | — | 1 revert, 2 archived |
| Build / pruning / release | 18 | 2 | 2 | — | 5 branch lines retired |
| libbox / platform | 12 | 1 family | — | — | — |
| Buffer ownership / MTU | 12 | — | — | — | — |

**Three provenance corrections that matter:**

1. **`b83089ec` ("Add MASQUE support") is `UPSTREAM-INHERITED`** — present *below* `e4b7e692` and
   reachable from upstream refs. MASQUE is **not** a fork invention; the fork added the dataplane.
   **`f4c0c412` (the naiveproxy dependency update) is likewise upstream-side.** Stage-1's
   `PROJECT.md` cited exactly these two commits as examples of "changes visible in this history", and
   they are the two commits definitionally **absent** from the delta it was describing.
2. **`7e13f461` is `OWN`, not an upstream absorption.** Its commit body states that the fork's own
   refactor dropped a registration and this commit fixes the resulting leak. Stage-1 placed it in a
   sentence whose predicate was "They are OWN" while describing it as absorbed.
3. **`38b7a586` ("Resolve Windows connection owners…") is a no-op in the released tree.** The file it
   touches is byte-identical at `e4b7e692` and `a267e99`; `git diff … -- common/process` is empty.
   Stage-1 filed it as evidence of Windows owner resolution.

**The provenance vocabulary that did not exist is now defined** — `OWN`, `UPSTREAM-INHERITED`,
`UPSTREAM-ABSORBED`, `EXTERNAL-ADAPTED`, `SUPERSEDED`, `ARCHIVE-ONLY` — with a rule attached:
`EXTERNAL-ADAPTED` requires a **quoted** source, and two were found and are cited verbatim.

---

## G. Evidence matrix

`SOURCE` = `VERIFIED_SOURCE`, `UNIT` = `VERIFIED_UNIT`, etc. `BLOCKED` = `BLOCKED_BY_ENVIRONMENT`.

| Subsystem | SOURCE | GIT | UNIT | INTEGRATION | INTEROP | CI | ARTIFACT | DEVICE | UNVERIFIED | BLOCKED |
|---|---|---|---|---|---|---|---|---|---|---|
| Core lifecycle | ✓ | ✓ | ✓ | — | — | decl. | — | — | device | `-race` |
| Path selection | ✓ | ✓ | ✓ (+mutations) | — | — | decl. | — | — | cross-kind graph as a whole | `-race` |
| DNS | ✓ | ✓ | ✓ | ✓ (in-process) | — | decl. | — | — | multi-platform matrix, device | `-race`, D-Bus |
| Sniff | ✓ | ✓ | ✓ + fuzz | — | — | decl. | — | — | the 64-round cap; race | `-race` |
| MASQUE | ✓ | ✓ | ✓ (1,317 tests) | ✓ | **decl. only** | decl. | — | — | device, live PTB | `-race` |
| Naive / HTTP | ✓ | ✓ | ✓ | ✓ | **decl. only** | decl. | — | — | official-client interop | cannot build client |
| Data path | ✓ | ✓ | ✓ | ✓ | — | decl. | — | — | real-link shaping | `-race` |
| Buffer ownership | ✓ | ✓ | ✓ (`!race`) | — | — | decl. | — | — | splice execution, kTLS | `-race`, Linux |
| Power / mobile | ✓ | ✓ | ✓ (+ordering) | ✓ | — | decl. | — | **✓ one iPhone run** | iPad, macOS, other OS | `-race` |
| MTU / geometry | ✓ | ✓ | ✓ | ✓ | — | decl. | — | — | real NIC | `-race` |
| libbox / platform | ✓ | ✓ | ✓ | — | — | decl. | **stale** | — | both platforms' device behaviour | no SDK/Xcode |
| Build / pruning | ✓ | ✓ | ✓ | — | — | decl. | — | — | reproducibility | no bash |
| CI / release | ✓ | ✓ | ✓ | — | — | **no run record** | stale | — | every dispatch-only workflow | no bash, no `gh` |

**No row is `VERIFIED_CI`.** No workflow-run record, run ID or artifact digest exists in the
repository, so every CI statement is a statement about a **declaration**.

**`SKIP` is never a pass**, and the fork's own skip wording says so ("NOT a pass", "SKIP, not a
pass"). One gate prints `PARTIAL` with **exit 0** by design, which is the same failure mode by another
route.

---

## H. Old docs disposition

**`DELETE = 0`.** Every candidate carries a unique number, a unique device observation, or a unique
trap. This matches Stage 1's own conclusion and was re-tested rather than inherited.

| Disposition | Count | Contents |
|---|---|---|
| **KEEP** | 38 | `docs/BUILD-PROFILES.md`, `docs/ENGINEERING-NOTES.md`, `docs/FORK-DIFF.md`, `docs/UPSTREAM-SYNC.md`, `docs/naive.md`, `docs/masque.md`, `docs/RELEASE-CERTIFICATE.md`, the user-facing `docs/**`, the design documents (`direct-offload.md`, `traffic-scheduler.md`, `load-balance.md`, `post-wake-reuse.md`, `power-governor.md`, `quic-dual-stack-blocker.md`, `physicalpath-controlpath-model.md`, `libbox-abi-contract.tsv`), `docs/internal/fork-splice-silent-stall.md` (**irreplaceable** — the sole record of an unfixed one-line defect), `release/*`, `.advg/*`, `test/interop/README.md`, `test/jiejie/naive-client-it/README.md` |
| **ABSORB-INTO-KNOWLEDGE** | 44 | the whole `v0.1.6` closure cluster, the static-validation and route-aware reports, the lifecycle/power/MTU ledgers, `apple-artifact-verification.md`, `apple-screen-state-*.md`, `android-abi-migration-closure.md`, `utls-fingerprint-decision.md`, `windows-*`, `upstream-53-ledger.md`, `upstream-sync-*`, and all the adjudication `.tsv`/`.json` inputs |
| **ARCHIVE** (marked, not deleted) | 26 | `verify-evidence/**` (**priority 1**), `docs/fork/v016-final-release-verdict.md` (still reads `NOT-READY` at the released SHA), `v016-release-candidate-manifest.md` (`NOT FROZEN`), `docs/WINDOWS-STATUS.md` (self-declared `FROZEN v0.1.5`), the Apple verification reports, the root closure reports, and seven stale-SHA reports |
| **DELETE** | **0** | — |

**A convention gap was found and is now named.** The fork's own intended marker,
`HISTORICAL EVIDENCE — NOT CURRENT SOURCE OF TRUTH`, **appears nowhere in the corpus** — while ten
documents do self-mark correctly by other means (a superseded banner, a `Status:` line, a retraction
note). Those ten are the model. `ci-release.md` now teaches the two rules that make them work:

* **the banner is current, the body is a record** — never read a ledger's section without its banner;
* **a document is a claim with a date, not a fact** — check whether the paths it cites still resolve.

Four contradictions were corrected in place rather than archived, because a reader needs the correction
where the stale claim lives:

| File | Was | Now |
|---|---|---|
| `scripts/ci/build-macos-client.sh:19-22` | claims `with_clash_api` was removed | **not edited** (outside the knowledge layer); the contradiction is recorded in `build-pruning-release.md` |
| `release-artifacts.sh:63-67` | "Windows, Android … deliberately NOT here" | recorded in `ci-release.md` |
| `protocol/naive/inbound.go:208-212` | claims the H3 hook is left nil under the server profile | recorded in `naive-http-transport.md` |
| `docs/fork/physicalpath-controlpath-model.md` | states the `ControlPath` reversal landed | recorded in `path-selection-validation.md` |

The prompt authorises knowledge-layer changes only, so code and script comments were **not** edited;
each contradiction is documented where a reader will meet it.

---

## I. Cross-file consistency audit

| Check | Result |
|---|---|
| Same SHA, multiple contradictory meanings | **PASS** — one baseline SHA, one upstream SHA, one fork point, one first-fork commit; every occurrence agrees |
| Every SHA token resolves | **PASS** — 249 tokens, 249 resolve; the 15 non-resolving tokens are deliberately-non-commit values (blob hashes, patch-ids, a submodule gitlink, `8388608`) |
| Internal module links resolve | **PASS** — 20/20 backticked module references resolve |
| One claim, two evidence levels | **PASS** — the `RequiredNetworks` tension is stated once, as a FOLLOW-UP, in four files with identical wording |
| A fixed problem still listed as a limitation | **PASS** — the DBus leak was found listed as unfixed in four places and corrected in all four |
| A superseded commit presented as current | **PASS** — every superseded item is labelled, and each carries the commit that superseded it |
| Upstream work presented as OWN | **PASS** — `b83089ec`, `f4c0c412`, `38b7a586` and the sniff HTTP/DNS patches are labelled `UPSTREAM-INHERITED`/`UPSTREAM-ABSORBED` with the blob or patch evidence |
| Fork work presented as upstream | **PASS** — Native Naive, `common/physicalpath`, `common/runtimecoord`, `common/power`, `common/trafficsched`, `common/bufgeom` are each verified absent at the fork point |
| Apple/Android/client contracts mixed | **PASS** — `libbox-platform-contract.md` records core-side only and says so about the three empty submodules |
| Release baseline mixed with knowledge revision | **PASS** — `BASELINE.md` separates them and states the post-baseline delta is documentation-only |
| Multi-label vocabulary | **PASS** — one status set, one evidence-level set, one provenance set, one type set; each defined where it is used |
| Placeholder / unfinished content | **PASS** — no `TODO`, `TBD`, `FIXME`, or unfilled HTML comment in `KNOWLEDGE/` |

Two inconsistencies were introduced **during** this round and caught by this audit, both now fixed:
a nonexistent `PhysicalPath.RequiredNetworks` type name, and a baseline ancestry figure (4,173) that
was the count from *all* upstream refs rather than from `upstream/testing` (2,827).

---

## J. Future-AI simulation

A new AI reads **only** `AGENTS.md`, `KNOWLEDGE/README.md`, `KNOWLEDGE/BASELINE.md`, and the one or two
modules its task needs. Can it do the ten required things?

| # | Requirement | Result |
|---|---|---|
| 1 | Know the production baseline | **PASS** — a table row with the SHA, plus the command to re-measure it |
| 2 | Know the upstream baseline | **PASS** — same table, plus the note that it is an *ancestor* of the baseline rather than a fork point |
| 3 | Decide which subsystem a task belongs to | **PASS** — `modules/README.md` maps 12 task descriptions to modules |
| 4 | Compute `baseline..HEAD` production delta | **PASS** — the command is in `BASELINE.md` and the expected answer (documentation-only) is stated |
| 5 | Avoid re-scanning the unchanged history | **PASS** — "if the log is empty or documentation-only, the modules are current" |
| 6 | Distinguish fork from upstream | **PASS** — the provenance test with both expected values, plus the orphan model and its two traps |
| 7 | Find the relevant SHAs | **PASS** — `COMMIT-INDEX.md` with 11 evolution topics; each module also carries its own provenance table |
| 8 | Understand why / invariants / conventions | **PASS** — `ARCHITECTURE.md` (11 boundaries), `INVARIANTS.md` (20 hard invariants, each with why/broken/proof/re-check/kind, plus conventions, optimisations and implementation details) |
| 9 | Find tests and re-check methods | **PASS** — `ACCEPTANCE.md` has 15 requirement groups; **every row carries an executable command** |
| 10 | Know which "optimisations" are dangerous | **PASS** — every module ends with an `AI cautions` list; 180+ cautions total, each naming the source rule that forbids the edit |

### The eight simulated tasks

| Task | Entry point found | Would it still need a full-repo archaeology? |
|---|---|---|
| **A. Modify sniff** | `sniff.md` — inventory, two loops, 10 ownership rules, the perf/behaviour/hardening split, 15 cautions | **No** |
| **B. Modify MASQUE** | `masque-connectip.md` — provenance verdict, capsule/session/MTU/DNS surface, 6 ownership rules, 15 cautions | **No** |
| **C. Modify chain proxy / detour** | `path-selection-validation.md` — the direction proof, the `commit` flag, cycle detection, the five seams, 12 cautions | **No** |
| **D. Modify Android lifecycle ABI** | `libbox-platform-contract.md` + `power-mobile-lifecycle.md` — the trim ladder, the policy boundary, `APIVersion`, the Android ABI gates | **No** |
| **E. Modify Apple screen-state** | `power-mobile-lifecycle.md` — level/edge/snapshot, the device evidence, the reverted attempt, `libbox-platform-contract.md` §3 | **No** |
| **F. Modify DNS routing** | `dns-demand.md` — the router, four generation axes, the cache-commit invariant, FakeIP, 12 cautions | **No** |
| **G. Modify the release artifact pipeline** | `ci-release.md` + `build-pruning-release.md` — the contract, the allowlist defect, the gate reachability map, the commands | **No** |
| **H. Memory / zero-copy optimisation** | `buffer-ownership.md` + `mtu-and-geometry.md` — 30 numbered rules, the splice defect, what is unobservable, 18 cautions | **No** |

**Two residual limits, stated rather than hidden.** A reader who needs `transport/v2rayxhttp` or
`protocol/vless/encryption` is told the modules are **silent** about them, so silence is not mistaken
for a guarantee. And a reader who needs to *execute* any gate will find the environment requirements
listed per row, including the three that cannot run on a MinGit-only Windows host.

---

## K. Follow-up — production issues found, not fixed

This round changed no production code. Five findings are recorded for their owners.

| # | Finding | Evidence | Suggested direction |
|---|---|---|---|
| **1** | **The release path fails whenever `RELEASE_EXTRA_PLATFORMS=true`**, which the workflow itself sets. The release-tree allowlist at `release-artifacts.sh:575-592` (and `:639-646`) still encodes two products while the contract admits four; Windows uploads three extra files and Android two. The gate self-test cannot catch it (its fixtures have exactly two `SHA256SUMS` rows). | `release.yml:69`; `release-artifacts.sh:80-83`, `:110-122`, `:575-592`; `windows-core-amd64.yml:396-409`; `android-core-arm64.yml:338-350`; `test-release-artifacts.sh:532-533` | Extend both allowlists from the same target table the rest of the script uses, and add a four-product fixture to the self-test. |
| **2** | **The splice silent stall** — `handed = true` is set before anything is attempted, so a `Pipe2` failure returns success with zero bytes copied, the copy loop terminates, `copyExtended` is never reached, and **the connection stops carrying data with no data-loss error**. Reachable exactly when the process is busiest. The only splice test asserts the flag on the success path. | `docs/internal/fork-splice-silent-stall.md:17-22`, `:39-46`, `:55-58` | One line: `handed = false` on that path. Then a test that fails a `Pipe2` and asserts the fallback. |
| **3** | **The Apple pin has no wired verifier** (see §E.1). The released revision is recorded nowhere in the tree, and `check-libbox-abi.sh` is both unwired and stale. | `check-libbox-abi.sh:98`, `:402-425`; `git ls-tree a267e99 clients/apple`; `verify-release-acceptance.sh:150-152` | Either wire the ABI gate into a workflow and update `VERIFIED_APPLE_SHA`, or add the released revision to a tracked provenance record. **Do not add a second SHA constant that nothing reads.** |
| **4** | **The `PathNode.RequiredNetworks` field is write-only** — two writers, zero production readers; its doc comment contradicts the validator, which uses a different model from a function parameter. The fork's own commit says it should be *"wired or removed by the file's owner"*. Related: an **orphan comment** at `dryrun.go:470-493` contradicts the code at `:313-317`, and a diagnostic branch at `:947-948` is unreachable. | `leaves.go:359`, `:446`; `dryrun.go:313-317`, `:470-493`, `:947-948`; `fc4bdd416`; `docs/fork/v016-correction-round-ledger.md:249-251` | Wire it or delete it; delete the orphan comment; delete or restore the unreachable branch. |
| **5** | **Three shipped comments state the opposite of the code**: `build-macos-client.sh:19-22` (a shipped capability described as deleted), `release-artifacts.sh:63-67` and `:594` (stale platform set and a "both platforms" message in a four-target loop), and `protocol/naive/inbound.go:208-212` (a claim about a tag profile that contains `with_quic`). | as cited; each verified against the profile files, `include/clashapi*.go`, and the CI probe | Correct the comments. The identical claim in `build-server.sh` was already retracted in place; these are its unretracted siblings. |

**Also recorded, not a defect:** the `test/` module's box-level tests are excluded from push CI on the
strength of a leak that is **now fixed** (§E.4). Re-admitting them is a CI decision that this round did
not take.

---

## L. What was delivered

```
AGENTS.md                                 rewritten — the three things that most often go wrong here
KNOWLEDGE/README.md                       rewritten — baseline, reading order, status labels
KNOWLEDGE/BASELINE.md                     rewritten — measured baseline, provenance test, four traps
KNOWLEDGE/PROJECT.md                      rewritten — what the fork is and what it did NOT change
KNOWLEDGE/ARCHITECTURE.md                 rewritten — 11 boundaries worth preserving
KNOWLEDGE/INVARIANTS.md                   rewritten — 20 hard invariants with proof and re-check,
                                                       plus conventions / optimisations / details
KNOWLEDGE/ACCEPTANCE.md                   rewritten — 15 requirement groups, every row a command
KNOWLEDGE/COMMIT-INDEX.md                 rewritten — 11 evolution topics with superseded material
KNOWLEDGE/modules/README.md               new — task -> module map, and what is NOT covered
KNOWLEDGE/modules/core-lifecycle-runtime.md        new (was core-lifecycle.md)
KNOWLEDGE/modules/path-selection-validation.md     new (split from routing-protocols.md)
KNOWLEDGE/modules/dns-demand.md                    new (split from routing-protocols.md)
KNOWLEDGE/modules/sniff.md                         new (split from routing-protocols.md)
KNOWLEDGE/modules/masque-connectip.md              new (split from routing-protocols.md)
KNOWLEDGE/modules/naive-http-transport.md          new (planned in Stage 1, never written)
KNOWLEDGE/modules/data-path-optimization.md        new — the largest Stage-1 omission
KNOWLEDGE/modules/buffer-ownership.md              new — the correctness surface with no home
KNOWLEDGE/modules/mtu-and-geometry.md              new
KNOWLEDGE/modules/power-mobile-lifecycle.md        new (was part of core-lifecycle.md)
KNOWLEDGE/modules/libbox-platform-contract.md      new (was part of platform-release.md)
KNOWLEDGE/modules/build-pruning-release.md         new (was part of platform-release.md)
KNOWLEDGE/modules/ci-release.md                    new — evidence, gates, and what may not be claimed
                                                   (removed: core-lifecycle.md, platform-release.md,
                                                    routing-protocols.md — all content carried forward)
```

**No production code, no test, no workflow, no script and no report in the repository was modified.**
`git status` shows changes under `KNOWLEDGE/` and `AGENTS.md` only.

`KNOWLEDGE_STAGE1_REPORT.md` is left in place as the historical record of the Stage-1 round; it is
superseded by this report and by the rewritten `BASELINE.md`, and its "0 commits in
`v0.1.6..HEAD`" line is stale for the same reason Stage-1's own `BASELINE.md` was — it was written
before the commit that contains it.

---

## M. Final status block

```text
KNOWLEDGE_STAGE2 = COMPLETE

PRODUCTION_CODE_CHANGED = NO / YES     → NO
RUNTIME_BEHAVIOR_CHANGED = NO / YES    → NO
PROTOCOL_BEHAVIOR_CHANGED = NO / YES   → NO
ABI_CHANGED = NO / YES                 → NO
GIT_HISTORY_REWRITTEN = NO

RELEASE_BASELINE_VERIFIED = YES
PRODUCTION_BASELINE_VALID = YES
UPSTREAM_BASELINE_VERIFIED = YES

STAGE1_CLAIMS_REAUDITED = YES
FORK_DELTA_REAUDITED = YES
COMMIT_PROVENANCE_REINFORCED = YES
SUBSYSTEM_MAP_REBUILT = YES
INVARIANTS_REAUDITED = YES
EVIDENCE_LEVELS_REAUDITED = YES
OLD_DOCS_RECONCILED = YES

CROSS_FILE_CONSISTENCY = PASS
FUTURE_AI_INCREMENTAL_READ = PASS

READY_FOR_FINAL_KNOWLEDGE_REVIEW = YES
```

`READY_FOR_FINAL_KNOWLEDGE_REVIEW = YES` is claimed only because source facts, Git provenance, test
evidence, the knowledge base and the acceptance index were each re-derived independently and then
checked against one another. **Five production follow-ups are recorded in §K and were deliberately not
fixed** — they are source defects, not knowledge defects, and fixing them would have broken this
round's guarantee that the production code did not change.
