# CI, evidence and release

> **Scope correction, 2026-10-28:** the v0.1.9 workflow census below is historical, not an
> inventory of the current tree. In particular `.github/workflows/windows-boxdd-tests.yml`
> declares a Windows runner and path-filtered push/PR plus manual dispatch for boxdd tests.
> This is `VERIFIED_SOURCE`, not evidence that any particular run executed or passed. Re-read
> the current workflows and run records before relying on other gate-coverage claims below.

**Later run evidence:** GitHub Actions [Verify 40602193497](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40602193497)
and [Android Core ARM64 40602193557](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40602193557)
both report success at `1ff0af39110d4bd4044c817a0bf209ff01e21f7a`. These run-level results are
`VERIFIED_CI` for that SHA and workflows only; they do not establish later Cronet pin behavior,
individual test coverage, artifact publication or device behavior.

**Scope:** `.github/workflows/*`, `scripts/ci/*`, `verify-evidence/*`, `release/*`, and what a future
AI may and may not claim as "verified".

**Load this module when** changing a workflow, a gate script, a release step, or **before citing any
existing evidence at all**.

**The single most important rule in this module:** *a CI job declaration is not evidence that the job
ran, a unit test is not interop, a static audit is not device behaviour, and one platform's PASS is
not all-platform PASS.* Every row below is graded on that basis.

---

## 1. Historical workflow census at v0.1.9

**Measured at the `v0.1.9` baseline. There were four automatic gates in that census.** The previous
version of this section named `verify.yml` as "the only automatic gate" and listed nine workflows; the
tree then had twelve, and three of them were entirely absent from that list. A reader who trusted the old
table would have believed that a `push` which moved release tooling, or the mobile contract, or the
Windows runtime audit, was unchecked.

| Workflow | push | PR | dispatch | `workflow_run` | Notes |
|---|---|---|---|---|---|
| **`verify.yml`** ("Verify") | `testing` | all | YES | — | the **source** gate |
| **`release-contract-verify.yml`** ("Release contract verification") | `testing`, `release/**`, `fix/release-asset-contract` — **path-filtered** | path-filtered | YES | — | the **release-tooling** gate; see below |
| **`mobile-contract.yml`** | YES | — | YES | — | mobile contract |
| `android-core-arm64.yml` | `testing` — **`paths:` filtered** | — | YES | — | Android ABI |
| `client-desktop-windows-runtime-audit.yml` | — | — | YES | **YES** | runs after the desktop producer, not on push |
| `client-apple.yml` | — | — | YES | — | |
| `client-desktop-windows.yml` | — | — | YES | — | |
| `client-macos.yml` | — | — | YES | — | |
| `interop-xray.yml` | — | — | YES | — | |
| `release.yml` | — | — | YES | — | |
| `server-linux-amd64.yml` | — | — | YES | — | |
| `windows-core-amd64.yml` | — | — | YES | — | |

Required-check / branch-protection configuration is **not in the repository**; do not guess at it. What
*is* in the repository is the set of workflows that could contribute a check, and that set is the table
above.

### The push gate is path-filtered, and the filter is the contract

`release-contract-verify.yml` runs on a push **only when release tooling moved**. Its own comment gives
the reasoning, and it is the rule to preserve: *"product source, docs and reports are not its input, and
a push that touches none of the paths below cannot change its verdict."* The path list is declared
**once** as a YAML anchor (`&release_tooling_paths`) and aliased into `pull_request:`, *"so the push and
`pull_request` triggers cannot drift into asserting different things about the same files."*

`workflow_dispatch` is kept **on purpose** even though the push trigger exists: *"the final frozen SHA
still has to be able to obtain this evidence ON DEMAND, because a `[skip ci]` dependency sync never
triggers a push run."* Removing it to "clean up" the triggers would remove the only way a frozen
candidate can obtain its own release evidence.

> **Two path filters, two different traps.** `android-core-arm64.yml` and
> `release-contract-verify.yml` both narrow their push trigger. Adding a new top-level directory can
> therefore **silently drop** Android ABI verification or release-contract verification for a change
> that genuinely affects it. Check the list before assuming either was checked — and if you add a file
> that the release path depends on, add it to `release_tooling_paths` in the same change.

### `verify.yml` — the source gate, step by step

| Step | What it runs | Proves | Cannot prove |
|---|---|---|---|
| release gate self-test | `scripts/ci/test-release-artifacts.sh` | the release selection logic refuses bad evidence — against a **mock `gh`** | that the real `gh`/API behaves so |
| gofmt | `gofmt -l` on 10 dirs | formatting | anything semantic |
| gomobile single-source | `test-gomobile-toolchain.sh` | no version literal outside one declaration | its network half is **opt-in** via `GOMOBILE_TOOLCHAIN_TEST_INSTALL=1` and not run here |
| nested module integrity | `check-go-module-integrity.sh --require-checked test` | the `test` module resolves the same fork pins as root | runtime behaviour |
| test module type-checks | `go vet` inside `test/` | the nested module compiles against **this** tree | that its tests pass |
| build-tag policy + linkname | `go test -count=1 ./cmd/internal/...` | tag composition and the `-checklinkname=0` contract | — |
| pinned-upstream assumptions | `scripts/ci/verify-upstream-assumptions.sh` | upstream revision changes that alter *meaning* | — |
| **untagged build** | `go build ./...` **and `go test -run '^$' ./...`** | every package builds untagged, and **every test binary compiles and links** | **that any test PASSES** — `-run '^$'` runs zero tests |
| tagged build | `go build -tags … ./...` | links under the server profile | — |
| build matrix | cross-builds linux/amd64, windows/amd64, freebsd/amd64, darwin/arm64 for 8 package trees | those packages compile per-GOOS | nothing runs |
| cronet provenance | `verify-cronet-provenance.sh apple` | the `replace` directives resolve to the fork archive bytes | — |
| **race, fork data paths** | `go test -race … ./common/trafficclass/... ./common/trafficsched/... ./common/dialer/... ./protocol/direct/... ./protocol/tun/... ./route/... ./adapter/...` | races in those 7 trees | **the root package is not in the list**, and neither is `./protocol/masque` |
| race, config surface | `go test -race … ./option/...` | races in `option` | — |

> **Historical v0.1.9 observation:** the root acceptance suite was compiled but not executed by the
> then-declared source gate. Current `verify.yml` additionally declares a root-package test filtered
> to `TestNetLogEventCount`. It does not follow that all root acceptance tests run; inspect the
> current commands and actual run records. See §4 for the tag set to run root tests locally.

**Merging a PR does not run the artifact workflows.** The artifacts (Apple, macOS, desktop Windows,
Linux server, Android core, interop) are all manual dispatch that the release path then consumes.

---

## 2. Gate wiring — what runs, what is declared, and what is merely present

**Re-measured at the `v0.1.9` baseline.** The previous version of this section was titled *"Unwired
gates — the largest systemic risk"* and listed twelve scripts as running from nothing. **Four of them
are now genuinely wired**, so that list would send a reader to re-wire something that already runs:

| Script | Previous claim | Measured now |
|---|---|---|
| `verify-release-acceptance.sh` | "mentioned in a comment, never invoked" | **invoked** — `release-contract-verify.yml:209` (self-test) and `verify.yml` |
| `verify-fork-handoff.sh` | "same — comment only" | **invoked** — `verify.yml` |
| `check-macos-app-name.sh` | "comment only" | **invoked** — `client-apple.yml` |
| `check-macos-client-config.sh` | "comment only" | **invoked** — `client-macos.yml` |

### The distinction the old list collapsed, and which still matters

`verify-release-acceptance.sh` is the sharpest example, because it is now *invoked* while still being
*not a gate on the release path*. `release-contract-verify.yml:210-222` states this explicitly: the step
that moved there is the **self-test** (*"it proves that the strict acceptance checker still refuses bad
evidence across 18 API snapshots"*), and the comment goes on to say the engine

> *"is unchanged and is still reachable from nothing in the release path; what changed is which workflow
> claims to gate on it."*

So there are **three distinct states**, and conflating any two of them produces a wrong answer:

```text
WIRED        something in a triggered workflow invokes it      -> a red run is possible
DECLARED     a workflow comments that it is a gate             -> a red run is NOT possible
PRESENT      the script exists and nothing references it       -> nothing happens at all
```

`verify-release-acceptance.sh` is **WIRED as a self-test, and its engine is still not a release-path
gate.** The reason recorded in the workflow is worth keeping: while `verify.yml` ran that self-test,
`verify.yml` was *"DECLARING the acceptance engine as a gate, although nothing on the release path
called it"* — and *"a second may-release authority that no release consults is exactly the drift the
single-authority rule exists to prevent."* The release path's authority is `release.yml` plus
`release-artifacts.sh` (§5), not this engine.

### Still genuinely unwired at this baseline

`check-satelite-one-core-pin.py`, `test-apple-links.py`, `verify-cronet-ios-archive.sh`,
`run-jiejie-suite.sh`.

`check-libbox-abi.sh` is **no longer in this list**, and it is split deliberately: its `--pin-only`
half runs in `verify.yml` (cheap, git-only, catches a gitlink move that lands without the
`VERIFIED_*_SHA` constant moving with it), and the full gate runs in `client-apple.yml` against the
checked-out client source. The subtree scripts it owns (`apple-abi-provenance.sh`,
`apple-abi-probe.sh`, `probe-accessors.py`) are reached only through it, so they are now reached
through a wired gate when that gate runs.

**Deleted in the Windows surface audit** (`refactor/windows-upstream-minimal-surface`):
`ci/windows/Test-DaemonServiceRegister.ps1` and `ci/windows/Test-InstallerLegacyAdoption.ps1` were
both in the PRESENT state — the script exists and nothing references it — so they were removed
rather than left as dead weight. The same pass checked
`clients/desktop/scripts/test-legacy-service-ownership.ps1` and `test-install-directory.ps1` and
**kept** both: the first is referenced by `verify-desktop-identity-consistency.py:348`, the second is
the only regression cover for the install-directory macro. Neither is invoked by any workflow
either, so they remain PRESENT and want wiring, not deletion.

**Historical v0.1.9 inventory; recheck current workflow references:** test tags referenced by nothing then — `trafficresearch` (`common/trafficsched/adaptive_research_test.go`),
`p2ios` (three `experimental/libbox` files), `tlsfragment_integration`
(`common/tlsfragment/integration_test.go`, also `t.Skipf`-gated), `force_stdio` (referenced only by a
`Makefile` target that is itself dead). **Test functions in these files never run in any declared
configuration at that historical review.**

**A dead `Makefile` target** — `make test` cannot pass: its first command is untagged, and `TAGS_TEST`
(used on two lines) is **never defined anywhere in the repository**, so it expands empty.

**Method note for anyone re-deriving this:** a naive extraction of `run:` blocks **under-detects**
(it misses a script invoked from inside another step's multi-line shell block, e.g.
`check-reference-coverage.sh` at `server-linux-amd64.yml:742`). The same naive extraction now also
**over-detects unwiredness** for anything reached through `release-contract-verify.yml`, which the old
list predates. Verify each candidate individually, against every workflow, including the two new ones.

---

## 3. `verify-evidence/` — stale by its own written rule

`verify-evidence/README.md:5-8` states the directory's integrity rule:

> *"…A log whose header names a different `HEAD` than the branch tip is stale and must be regenerated
> rather than trusted."*

**Every one of the 14 logs names `### HEAD: cfc77dc51ab7322e993b8ae0b94b96db59e48f47`.** That is 101
commits behind `a267e99` and 102 behind the knowledge revision, and it is a **strict ancestor** — old,
not divergent.

Worse than old: `git diff --name-status cfc77dc5 HEAD -- scripts/ci .github/workflows release clients`
returns **15 modified + 1 added** files, including `check-libbox-abi.sh`,
`check-apple-source-selection.sh`, `test-apple-signing.sh`, `apple-client-source.sh`,
`release-artifacts.sh`, `apply-desktop-branding-overlay.py` (retired since this transcript — the
static branding now lives in the pinned desktop fork, and `inject-desktop-release-version.py`
replaced it), `build-apple-libbox.sh`
**and `clients/apple` itself**. The evidence therefore describes both an older tree **and** older
versions of the gates it covers.

`README.md:53-59` also records that pre-fix transcripts were **deliberately deleted** so the directory
would not read as a mix of current and superseded results — which means a re-checker cannot see the
failures, only the post-fix passes. And `check-libbox-abi.log` ends with
`PARTIAL: … the call sites were NOT verified by this run` while `gates.tsv` records the row as exit 0
— a gate that skips and reports success, which is exactly the failure mode
`check-go-module-integrity.sh:165-167` names.

> **Evidence level: `VERIFIED_ARTIFACT` at `cfc77dc5`, and STALE. It is not evidence for `a267e99`.**
> Anything at the production baseline that rests **only** on `verify-evidence/` is `UNVERIFIED` at the
> production baseline.

### Evidence absent from the historical v0.1.9 review

This table records what that review found in the tree at the time. It is not a claim that later
GitHub Actions runs or external evidence do not exist; see the scoped run records above and inspect
the relevant run for the target under review.

| Claim | Where it is announced | What exists |
|---|---|---|
| Stress results over the 30 packages several branch lines touched | `KNOWLEDGE/reports/release/BRANCH_CONSOLIDATION_TO_TESTING_FINAL.md:452-458` | **Nothing.** Line 451 is literally `<!--STRESS_RESULTS-->` and the next section begins at `:453`. This is the only HTML-comment placeholder in any root report. |
| `-race` results for any package | all reports | None anywhere; consistently reported as not-run, and the CI step that would produce them leaves no record in the tree. |
| Apple submodule reachability | `KNOWLEDGE/reports/release/CORE_0.1.6_FINAL_CLOSURE_REPORT.md:132-138` | Needs a host with the submodule and network. |
| Device / emulator / iOS / Android runtime runs | the Apple reports | None. `KNOWLEDGE/reports/release/APPLE-PARENT-FINALIZE-REPORT.md:198-203` records the gate layers as `SKIP` and the overall verdict as **`PARTIAL`** because a layer was skipped, *"which is deliberately not reported as PASS"*. |
| Live Xray / quic-go / masque-go interop | `interop-xray.yml`, `server-linux-amd64.yml` | Workflow declarations only. No artifact in `verify-evidence/`. The QUICHE live interop tests were **deleted** (`0a3993a8c`). |
| Windows PE / MSI / Authenticode / driver-signature / service-lifecycle | nine `scripts/ci/windows/*.ps1`, `client-desktop-windows.yml` | Declarations only. `windows-core-amd64.yml` runs on **ubuntu-latest** and only inspects symbols; the desktop workflow is dispatch-only; no `.ps1` output is archived in the tree. |
| Docker-backed fixtures | `test/docker_test.go`, `test/openvpn_test.go` | Not run — no daemon. Note `test/docker_test.go` **has no skip guard**. |

### The repository's own release verdict

At the historical review, `docs/RELEASE-CERTIFICATE.md` opened with:

```text
NOT READY FOR A STABLE MILESTONE
```

and listed missing data-plane and Apple signing evidence for that review. Do not generalize those
historical absence claims to later releases; obtain evidence for the exact target and platform.

**A third claim in that document is now stale, and it is the one most likely to be repeated.** It
records a *known, unfixed* goroutine leak in `dns/transport/local.(*DBusResolvedResolver).loopUpdateStatus`
and justifies keeping the `test/` module's box-level tests out of push CI on the strength of it.
**The leak is fixed** — by `6b495134c`, which added `signalLoopStop`/`signalLoopDone`/`signalLoopOnce`, a
stop-channel `select`, `RemoveSignal`, and a **join** in `Close()`
(`local_resolved_linux.go:80-82`, `:206-207`, `:239-241`, `:374`, `:385`). See `dns-demand.md` §6.

> **Two consequences:** do not re-report the leak, and **do not apply the fix that document proposes** —
> closing the dbus signal channel would panic, because dbus is the sender. The CI exclusion it justified
> can be revisited; that is a CI decision, not a knowledge one.

This is also a good illustration of the general rule in this module: **a document in this repository is
a claim with a date, not a fact.** The paths it cites (`:347-349`, `:216`) no longer resolve to the code
it describes.

---

## 4. The commands a re-checker must actually run

```powershell
# --- environment (Windows amd64) ---
$env:GOROOT='C:\src\_toolchain\goroot'; $env:GOPATH="$env:USERPROFILE\go"
$env:GOMODCACHE="$env:GOPATH\pkg\mod"; $env:GOTOOLCHAIN='local'; $env:CGO_ENABLED='0'
$env:PATH="C:\src\_toolchain\goroot\bin;C:\src\MinGit\cmd;$env:PATH"
Set-Location 'C:\src\kb-stage2'
$OTHERS = (Get-Content release\DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()
$ALL    = (Get-Content release\DEFAULT_BUILD_TAGS -Raw).Trim()

# 1. THE ROOT DESIGN-GUARANTEE SUITE. Correct tag set is OTHERS, not DEFAULT_BUILD_TAGS.
#    DEFAULT_BUILD_TAGS carries with_naive_outbound, whose cronet dependency cannot build here.
#    Measured at the release baseline: ok, 5.459 s, exit 0.
go test -count=1 -timeout 10m -tags "$OTHERS" .

# 2. Named contract tests only
go test -count=1 -tags "$OTHERS" -v -run 'TestFalseDenyMatrix|TestConsumerDemandContractIsProvableOnly|TestTheCrossKindGraphReadsPublishedAdapterFacts|TestDestinationDNSOwnershipIsVacuousForEveryLocalDialer' .

# 3. Lifecycle ordering + stress + goroutine census
go test -count=1 -tags "$OTHERS" -v -run 'TestTheReuseVerdict|TestTheSleepEdge|TestOneHundred|TestNoResurrectionAfterClose|TestTheBridgeUnderConcurrent' .

# 4. Subsystem suites that are green untagged (measured)
go test -count=1 ./common/sniff/... ./common/runtimecoord/... ./common/physicalpath/... ./adapter/... ./route/... ./dns/... ./experimental/libbox/... ./common/dialer/...

# 5. MASQUE + bufgeom NEED tags. Measured: 412 s, 1317 PASS / 0 FAIL / 0 SKIP, exit 0.
#    (bufgeom 0.09 s, transport/masque 8.0 s, protocol/masque 408 s — the last is dominated by
#     TestMasqueRestartCyclesDoNotLeakLoops at 240 s and TestMasqueLifecycleKeepsOneLoop at 150 s.)
go test -count=1 -timeout 15m -tags "$OTHERS" -v ./common/bufgeom/... ./transport/masque/... ./protocol/masque/...

# 6. The scheduler (slow: 106 s, dominated by four contention experiments)
go test -count=1 ./common/trafficsched/... ./common/trafficclass/...

# 7. Race — needs cgo; BLOCKED on a host without a C compiler
go test -race -count=1 -timeout 30m -tags "$ALL" ./common/trafficclass/... ./common/trafficsched/... ./common/dialer/... ./protocol/direct/... ./protocol/tun/... ./route/... ./adapter/... ./option/...

# 8. Gates — every *.sh needs bash, absent in a MinGit-only environment
bash scripts/ci/verify-upstream-assumptions.sh
bash scripts/ci/check-go-module-integrity.sh --require-checked test
bash scripts/ci/verify-cronet-provenance.sh apple
bash scripts/ci/check-apple-source-selection.sh
bash scripts/ci/check-libbox-abi.sh --pin-only  # wired in verify.yml; full gate in client-apple.yml
```

**Measured in this audit, so a re-checker knows what "expected" looks like:**

| Command | Result |
|---|---|
| `go build ./...` | **exit 0** |
| `go build -tags "$OTHERS" ./...` | **exit 0** |
| `go build -tags "$ALL" ./...` | **exit 1** — cronet build constraints, as designed |
| `go test -count=1 .` | **exit 1** — 12 tag-absence failures |
| `go test -count=1 -tags "$ALL" .` | **setup failure** — the test binary does not build |
| `go test -count=1 -tags "$OTHERS" .` | **exit 0**, `ok 5.459 s` |
| `go test -count=1 ./...` (untagged, 168 pkgs) | **exit 1** — 78 ok / 3 FAIL (`root`, `protocol/masque`, `common/tls`) / 87 no-test-files |
| `go test -count=1 ./common/sniff/...` | **exit 0**, `ok 5.83 s`; the documented flake did **not** reproduce |
| `go test -count=1 ./experimental/libbox/...` | **exit 0**, `ok 0.708 s`; verbose 39 PASS / 0 FAIL / 0 SKIP |
| `go test -count=1 -tags "$OTHERS" -v ./common/bufgeom/... ./transport/masque/... ./protocol/masque/...` | **exit 0**, 412 s, 1317 PASS / 0 FAIL / 0 SKIP |
| `go test -count=1 ./common/trafficsched/... ./common/trafficclass/...` | **exit 0**, 106.4 s / 0.03 s; 51 tests, 0 skipped |
| `go test -race …` | **BLOCKED** — `go: -race requires cgo` |

**Known-failing and known-flaky set, so it is not re-diagnosed as a regression:**

| Item | Why it fails | Not a regression because |
|---|---|---|
| `common/tls` `TestWindowsClient*TLS13` | a **real remote handshake**; this host's Schannel has no TLS-1.3-over-TCP | `NETWORK_DEPENDENT`, recorded in `docs/fork/v016-final-closure-ledger.md:298` |
| `common/sniff` `TestAgentFFrameShape*`, `TestTheSniffCensusMovesForExactlyOneLeakedGoroutine` | **load-sensitive**; the failing test name differs run to run | documented 5/5 passes in isolation on both the fix and the untouched base. A single green run here does **not** prove non-flakiness. |
| `protocol/group` `TestRecheckSingleFlightCollapsesABurst` | load-sensitive | 20 consecutive isolated runs clean on both trees |
| `common/trafficsched` under `-count=10` | ~18 minutes of deliberate sleeps trips a timeout | every individual test passes |
| `protocol/masque` `TestMasqueRestartCyclesDoNotLeakLoops` | **330 s alone** — slow, not hung | measured |
| `common/trafficsched` wall clock | 106.2 s vs a 106.4 s baseline | measured |

**The host-specific set does NOT reproduce.** Reports name `common/windivert`, `common/tlsspoof` and
`common/tls` as "3 pre-existing environmental failures". Measured here: `common/windivert` → **25 PASS /
0 SKIP / 0 FAIL** and `common/tlsspoof` → **9 PASS / 0 SKIP / 0 FAIL**, including every
`TestIntegration*` the reports call `windivert: open SCM: Access is denied`. The honest reading is that
the claim was **host-specific, not tree-specific** — it describes a machine's WinDivert/Administrator
state and does not survive a change of host.

---

## 5. Release provenance

### The single authority for the asset list

**`scripts/ci/release-artifacts.sh` owns the canonical public asset list, and it derives it in exactly
one function.** This is the rule that the `v0.1.8` release defect bought:

> **ONE FACT = ONE OWNER.** `public_asset_names()` (`release-artifacts.sh:1671`) iterates
> `TARGET_WORKFLOWS` and emits each target's `tarball` name plus the two non-tarball artifacts. Four
> subcommands consume it and none of them re-derives it: `expected-assets` (`:1706`), `check-upload`
> (`:1744`), `publish-args` (`:1821`) and `verify-public-release` (`:1778`).

**What went wrong, because it is the failure mode to avoid repeating.** `v0.1.8` shipped with **six**
assets where the contract required **seven** (`Libbox.xcframework.tar.gz` was missing). The cause was
not a missing check — it was **three authorities over one fact**: `check-upload` derived its list from
`TARGET_WORKFLOWS` and passed; `release.yml`'s `gh release create` carried a *hardcoded literal* of six
filenames and uploaded six; and `release.yml`'s read-back carried *the same six literals*, so it
compared six against six and the workflow went green. **Self-consistently wrong.** A count-only check
cannot detect this, which is why the read-back compares **by name**.

The workflow is now forbidden from holding a second copy: `check-release-workflows.py` asserts that the
publish step *takes* its list from `publish-args` and that it contains **no** literal asset filename.
Adding the literal back is a regression to the exact defect, and the checker fails.

### `release.yml` — draft-first, and why the order is the safety property

`workflow_dispatch` only, requiring `inputs.tag`; `dry_run` defaults to **false**. **Seventeen steps**,
in this order, and the order is load-bearing:

```text
 1 check out the tagged commit                    (the SHA is the identity)
 2 check out the release tooling at that revision
 3 bind the tooling identity to the released commit
 4 require exact-SHA source and tooling evidence
 5 upload the evidence record
 6 resolve and validate the tag                   check-tag
 7 resolve the release version                    release-version
 8 decide whether this run may publish            publish-flag: anything but true/false dies
 9 select the CI runs for this commit             plan: ONE successful run per product workflow,
                                                  for THIS exact SHA
10 download the verified artifacts                from the SELECTED runs, not a fresh query
11 stage the Windows desktop installer
12 verify the downloaded artifacts                per platform: binary + .sha256 + BUILD-INFO, a real
                                                  sha256sum -c, platform read from the file's own
                                                  header, recorded commit must match
13 flatten the release assets
14 confirm the upload set is exactly the public contract        check-upload
─── the irreversible half ───
15 create the release (draft) and upload the canonical asset set   --draft, assets from publish-args
16 verify the draft carries exactly the public contract             verify-public-release
17 publish the draft as the stable release                          gh release edit --draft=false
                                                                    then verify-public-release AGAIN
```

**`publish-args` is all-or-nothing.** It validates that every asset in the contract exists on disk and
prints **nothing at all** on failure, so a partial list can never reach `gh release create`. That is a
deliberate property, not an implementation detail: printing paths as they are validated would let the
release be created with whatever happened to be checked before the failure.

**Draft-first is what stops a defective release being publicised.** The release is created as a
**draft**, the contract is verified **by name** against the draft, and only then is it published. A
missing asset therefore stops at a draft the public cannot see. `v0.1.8` is the counter-example: it was
not draft-first, so the six-asset defect was public before anything compared it to the contract.

> **So the claim "product CI produces/proves artifacts; release downloads and re-verifies those exact
> bytes rather than rebuilding them" is TRUE** — but it is scoped to the platforms `plan` selects, and
> those producers are `workflow_dispatch`-only, so they must have been dispatched manually first.

**Platform coverage.** `release.yml` sets `RELEASE_EXTRA_PLATFORMS: true`, and `release-artifacts.sh`
accepts exactly the strings `true`/`false` (anything else dies). With it set, **Windows and Android
artifacts ARE in the release set**; the Apple IPA/DMG is still excluded.

### A defect this module documented — now FIXED

An earlier revision of this section described a **live defect**: the release-tree allowlist was built
as four names per target and never extended for the extra files the Windows and Android producers
upload, so `verify` would die with `unexpected file(s) in the release tree` whenever
`RELEASE_EXTRA_PLATFORMS=true` — which the release workflow itself sets.

**That is fixed at the `v0.1.9` baseline.** The allowlist now carries an Apple branch
(`release-artifacts.sh:1378`, `Libbox.xcframework.tar.gz SHA256SUMS-LIBBOX.txt manifest.json
BUILD-INFO-LIBBOX.txt`) alongside the per-target rows (`:1381`), and the `die` moved to `:1396`. Two
other stale statements in the same file were corrected at the same time: the header comment that said
Windows/Android were *"deliberately NOT here"* now records that it used to say that (`:79`), and the
loop that printed *"both platforms"* now explains why that was wrong (`:1403`). The gate's own
self-test was extended to a **six-entry** canonical `SHA256SUMS` (`test-release-artifacts.sh:1884`),
where the old fixture asserted two rows and could not have caught the defect.

**This is recorded as a fixed defect rather than deleted, because the shape recurs:** an allowlist and a
producer's upload set are two statements of one fact, and the way they fail is by agreeing with each
other while both are wrong.

### What is out of scope

`scripts/ci/verify-release-acceptance.sh` enforces a **default required-workflow set** of
`verify.yml`, `interop-xray.yml` and `server-linux-amd64.yml`. **`client-apple.yml` is not in that
set.** `verify.yml` now runs the **pin-only** half of the Apple ABI gate, and the full gate runs in
`client-apple.yml`; both catch a gitlink/constant disagreement, but release acceptance itself is still
**structurally incapable** of noticing Apple-pin drift because the engine is **not called by the
release path at all**; the release path's authority is `release.yml` + `release-artifacts.sh`.

### The Apple source authority — **INVERTED at this baseline**

**The previous revision of this module stated the opposite of the truth, so read this as a correction
and not as an addition.** It said the refs file *"declares the `clients/apple` gitlink as the sole
authority"* and that the refs file is *"forbidden from recording a SHA"*. That was the older model.
`release/apple-client-refs.env` now says, in its own words:

> *"the authority is the client repository: its tip is resolved to an exact SHA at the start of a"*
> build — `apple-client-refs.env:31`

and

> *"The submodule and its gitlink still exist and still work: the local default checkout, the … [gitlink
> is no longer the] authority, and a published build is no longer asserted to equal the gitlink — that
> assertion is precisely what made the pin manual. Set `APPLE_CLIENT_REQUIRE_GITLINK=1` to get [the hard
> equality back]"* — `apple-client-refs.env:42-45`

**Why it was inverted.** Under the gitlink-authority model every client release was a
**two-repository operation**: the build could only mirror a submodule someone had already bumped by
hand, so each client commit needed a second commit here and a push. The workflow states the replacement
model plainly:

> *"The AUTHORITY is the Apple client repository, not this repository's gitlink, so this gate compares
> the checkout against the RESOLVED source rather than against the gitlink. Requiring the gitlink made
> every client release a two-repository operation."* — `client-apple.yml:234-237`

**The two authorities now:**

| | Authority | How it is used |
|---|---|---|
| **Build selection** | the **client repository tip**, resolved to an exact SHA at the start of a build | what a published Apple artifact is actually built from |
| **Gitlink** (`clients/apple`) | the local default checkout; **not** the build authority | equality with the resolved source is a **note**, not a failure |
| `APPLE_CLIENT_REQUIRE_GITLINK=1` | opt-in | restores the **hard** gitlink equality (`client-apple.yml:285`, `:442`) |

**The measured pin.** `VERIFIED_APPLE_SHA="3aedd7417488e33a69fe2d46dc34539780d76157"` at
`scripts/ci/check-libbox-abi.sh`, and `git ls-tree HEAD clients/apple` is **the same commit**. It was
`6e559a4` until 2026-10-27: the gitlink advanced for the v0.1.28 path-monitor canary and the constant
did not, which the new `--pin-only` push gate now fails. But because the gitlink no longer *selects*
the build, **that agreement is weaker evidence than this module previously claimed** — it is the two
having been moved together by hand, not a proof the build used it.

### `check-apple-source-selection.sh` was rewritten and IS wired

The previous revision said this script *"could not have caught [pin drift] **by design** — every one of
its checks is self-referential to the gitlink"*, and that **nothing runs it**. Both halves are now
wrong:

* **It is wired** — `client-apple.yml:231` runs it (`./scripts/ci/check-apple-source-selection.sh`).
* **It was rewritten** to assert the *opposite* of what the old text assumed. It keeps the single-copy
  scheme checks (`:124-129` "the refs file records no SHA", `:209-210` "neither SHA may be hardcoded"),
  because those still prevent a *duplicate* authority — but it now also asserts that **moving the
  gitlink does not move the iOS source**, and that **dropping the gitlink does not change the resolved
  source**. That is exactly the property the inverted-authority model depends on.

**Measured:** the audit ran it on this host — **72 passed, 0 failed**, exit 0. `VERIFIED_UNIT` for the
script's own assertions; it does not prove the resolved revision is the *intended* one, which is the
narrow residual gap.

**The wording to keep:** *"the Apple pin has no wired verifier"* is **no longer the right sentence** — a
selector and a suite are wired. What remains unwired is any check that the **resolved revision is the one
someone intended**, as opposed to merely self-consistent.

### The refs guard: it read the caller's environment, not the file

`scripts/ci/apple-client-source.sh` tested `MACOS_APPLE_SHA` / `MACOS_APPLE_BRANCH` **in the
environment** after sourcing the refs file, to decide whether the file had been edited back to the
two-source model. It cannot tell the file's assignments from the caller's, and the caller legitimately
carries both names:

```text
apple-client-source.sh resolve   emits MACOS_APPLE_SHA / MACOS_APPLE_BRANCH as historical aliases
                                 (:386-388, so an eval'ing caller survives `set -u`)
release-apple.sh                 exports them on to the builders                (:181)
```

So `resolve -> export -> assert-shared-source` refused the values `resolve` had printed moments
earlier, and blamed a refs file containing no such line. **`release-apple.sh testflight` deadlocked on
its own output** — a real release blocker, not a documentation defect. `ece0fb9d2` replaced the test
with `refs_declares_platform_source()`, a text test on the **file** asked *before* sourcing, so the
original intent ("every subcommand must refuse a two-source refs file") is kept while the answer no
longer depends on who called. The refs file may still not declare a per-platform source; the `export
NAME=` spelling is refused too.

Evidence: `VERIFIED_SOURCE` for the guard's text (`:88-105`); `VERIFIED_UNIT` for the behaviour — the
guard was exercised directly against ten cases (three the pre-fix guard wrongly refused, four refusals
it must keep, three fail-closed/comment near-misses), alongside three old-guard control cases that
reproduce the deadlock. All thirteen passed. The shipped script itself needs `bash`, absent on that
audit host, so running `apple-client-source.sh` end to end is `BLOCKED_BY_ENVIRONMENT`.

---

## 6. Build profiles (summary — full detail in `build-pruning-release.md`)

| Profile | Contents |
|---|---|
| `DEFAULT_BUILD_TAGS` | the **Darwin** profile. Includes `with_naive_outbound`; current Windows Cronet files additionally require `with_purego`. |
| `DEFAULT_BUILD_TAGS_OTHERS` | the same **minus** `with_naive_outbound`. **This is the general/server set.** |
| `DEFAULT_BUILD_TAGS_WINDOWS` | `DEFAULT_BUILD_TAGS` plus `with_purego` |

`verify.yml:214-215` states in-tree that `DEFAULT_BUILD_TAGS` is *"deliberately NOT used here: it
carries with_naive_outbound, and no Linux product links cronet."* The names are misleading and the
repository knows it. The Windows setup failure documented in the historical
`KNOWLEDGE/reports/release/CORE_0.1.6_FINAL_CLOSURE_REPORT.md` is evidence for that environment and
revision, not a claim that current Cronet lacks all non-Apple targets. See `build-pruning-release.md`
for the current pinned fork's platform tags and the scope of the Windows purego test.

**The two profiles differ in exactly one entry.** Measured, verbatim:

```text
DEFAULT_BUILD_TAGS        with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,
                          with_tailscale,with_ccm,with_ocm,with_cloudflared,with_naive_outbound,
                          with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname
DEFAULT_BUILD_TAGS_OTHERS … the same list with with_naive_outbound removed
```

> **`docs/BUILD-PROFILES.md:43-44` says the *client* profile "adds `with_utls`". That is false:**
> `with_utls` is in **both** files. `with_naive_outbound` is the only difference. A reader who trusts
> that sentence will mis-scope a tag change.

---

## 7. AI cautions

1. **Do not turn a declaration into `VERIFIED_CI`.** Cite the exact run, SHA and observed outcome;
   the later runs linked above are scoped evidence, not proof of every workflow or artifact.
2. **Do not cite `verify-evidence/` for the production baseline.** Its own README disqualifies it.
3. **Do not infer that the root `box_*_test.go` suite ran from compile-only CI or from a different
   filtered root test.** Check the current command and its run record.
4. **Select the platform's actual tag profile.** `DEFAULT_BUILD_TAGS_OTHERS` omits Naive;
   `DEFAULT_BUILD_TAGS_WINDOWS` includes it and adds `with_purego`. Dependency and native-library
   availability remain separate build/runtime prerequisites.
5. **Do not infer feature coverage from an untagged run.** Some MASQUE tests require `with_quic`;
   root package checks use the appropriate profile. Diagnose actual failures rather than assuming
   either a regression or a tag problem.
6. **Do not treat a `SKIP` as a pass.** The fork's skip wording ("NOT a pass") is deliberate.
7. **Do not treat one platform's PASS as all-platform PASS.**
8. **Do not describe the Apple ABI situation as "a broken gate".** See §5.
9. **Do not describe `release.yml` as covering all platforms.** It covers what the plan step selects,
   after 4–5 manual dispatches.
10. **Do not assume a script that exists is a gate that runs.** Check reachability first.
11. **Do not re-diagnose the known-failing/flaky set in §4 as a regression.**
12. **Do not add a top-level directory without checking `android-core-arm64.yml`'s path filter.**
13. **Do not delete `<!--STRESS_RESULTS-->`-style placeholders silently.** An unfilled placeholder is a
    record that the evidence was never produced; that is information.
14. **Do not present the fork's `docs/changelog.md` as the fork's changelog.** It is upstream's.
15. **Do not read a ledger's body without reading its banner.** `docs/fork/v016-final-closure-ledger.md`
    carries a section at `:465` stating *"HY2 ChromeParrot is BLOCKED on the pinned quic-go fork"* while
    its own header at `:4-13` retracts exactly that, states that
    `Config.InitialPacketSizeCeiling` is applied **after** the ChromeParrot override so a proven 1232
    reaches the wire, and points at `protocol/hysteria2/hard_ceiling_wire_test.go`. A reader who jumps
    to the section re-opens a closed finding. This is the general shape of every long ledger here:
    **the banner is current, the body is a record.**
