# CI, evidence and release

**Scope:** `.github/workflows/*`, `scripts/ci/*`, `verify-evidence/*`, `release/*`, and what a future
AI may and may not claim as "verified".

**Load this module when** changing a workflow, a gate script, a release step, or **before citing any
existing evidence at all**.

**The single most important rule in this module:** *a CI job declaration is not evidence that the job
ran, a unit test is not interop, a static audit is not device behaviour, and one platform's PASS is
not all-platform PASS.* Every row below is graded on that basis.

---

## 1. What actually runs

| Workflow | Trigger | Blocking? |
|---|---|---|
| **`verify.yml`** ("Verify") | `push` to `testing`, **all** `pull_request`, `workflow_dispatch` | **YES — the only automatic gate** |
| `android-core-arm64.yml` | `push` to `testing` with a `paths:` filter | push only, path-filtered |
| `client-apple.yml` | **`workflow_dispatch` only** | no |
| `client-desktop-windows.yml` | **`workflow_dispatch` only** | no |
| `client-macos.yml` | **`workflow_dispatch` only** | no |
| `interop-xray.yml` | **`workflow_dispatch` only** | no |
| `release.yml` | **`workflow_dispatch` only** | no |
| `server-linux-amd64.yml` | **`workflow_dispatch` only** | no |
| `windows-core-amd64.yml` | **`workflow_dispatch` only** | no |

Required-check / branch-protection configuration is **not in the repository**; do not guess at it.

> **Android's path filter is a silent-coverage trap.** `android-core-arm64.yml` runs only when a listed
> path changes. Adding a new top-level directory can therefore **silently drop Android ABI
> verification** for changes that do affect it. Check the list before assuming Android was checked.

### `verify.yml` — the whole automatic gate, step by step

| Step | What it runs | Proves | Cannot prove |
|---|---|---|---|
| release gate self-test | `scripts/ci/test-release-artifacts.sh` | the release selection logic refuses bad evidence — against a **mock `gh`** | that the real `gh`/API behaves so |
| release ACCEPTANCE self-test | `scripts/ci/verify-release-acceptance-test.sh` | 18 synthetic API snapshots: 1 accepted, 15 rejected, 2 escape-hatch | same — synthetic |
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

> **The critical gap, stated exactly:** `verify.yml` is the only automatic gate and it contains **no
> `go test` invocation that executes the root package**. The 25 root `box_*_test.go` files — every
> start-time-validation, physical-path and lifecycle contract test — are **compiled by CI and never
> executed by CI**. See §4 for the correct tag set to run them yourself.

**Merging a PR does not run the artifact workflows.** The artifacts (Apple, macOS, desktop Windows,
Linux server, Android core, interop) are all manual dispatch.

---

## 2. Unwired gates — the largest systemic risk in this repository

Scripts that exist, are named after a gate, and that **nothing runs**:

**Gate class** — `check-libbox-abi.sh` (**0 referrers** anywhere outside its own usage block),
`apple-abi-provenance.sh`, `apple-abi-probe.sh`, `probe-accessors.py` (referenced **only** by
`check-libbox-abi.sh`, so orphaning one script orphaned its whole subtree), `check-satelite-one-core-pin.py`,
`test-apple-links.py`, `verify-cronet-ios-archive.sh`, `run-jiejie-suite.sh`, `acceptance/jiejie-masque-vps.sh`,
`ci/windows/Test-DaemonServiceRegister.ps1`.

**Comment-only mentions that read like wiring** — `verify-release-acceptance.sh` (mentioned in a
`verify.yml` comment, never invoked), `verify-fork-handoff.sh` (same — and `verify.yml`'s comment says
it *"proves the documents agree with each other"*), `check-macos-app-name.sh`, `check-macos-client-config.sh`.

**Test tags referenced by nothing** — `trafficresearch` (`common/trafficsched/adaptive_research_test.go`),
`p2ios` (three `experimental/libbox` files), `tlsfragment_integration`
(`common/tlsfragment/integration_test.go`, also `t.Skipf`-gated), `force_stdio` (referenced only by a
`Makefile` target that is itself dead). **Eight test functions in these files never run in any declared
configuration.**

**A dead `Makefile` target** — `make test` cannot pass: its first command is untagged, and `TAGS_TEST`
(used on two lines) is **never defined anywhere in the repository**, so it expands empty. The original
Stage-1 knowledge base pointed readers at the root suite without ever naming a tag set, which fails the
same way.

**Method note for anyone re-deriving this:** a naive extraction of `run:` blocks **under-detects**
(it misses a script invoked from inside another step's multi-line shell block, e.g.
`check-reference-coverage.sh` at `server-linux-amd64.yml:742`). Verify each candidate individually.

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
`release-artifacts.sh`, `apply-desktop-branding-overlay.py`, `build-apple-libbox.sh`
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

### The evidence that does not exist at all

| Claim | Where it is announced | What exists |
|---|---|---|
| Stress results over the 30 packages several branch lines touched | `BRANCH_CONSOLIDATION_TO_TESTING_FINAL.md:452-458` | **Nothing.** Line 451 is literally `<!--STRESS_RESULTS-->` and the next section begins at `:453`. This is the only HTML-comment placeholder in any root report. |
| `-race` results for any package | all reports | None anywhere; consistently reported as not-run, and the CI step that would produce them leaves no record in the tree. |
| Apple submodule reachability | `CORE_0.1.6_FINAL_CLOSURE_REPORT.md:132-138` | Needs a host with the submodule and network. |
| Device / emulator / iOS / Android runtime runs | the Apple reports | None. `APPLE-PARENT-FINALIZE-REPORT.md:198-203` records the gate layers as `SKIP` and the overall verdict as **`PARTIAL`** because a layer was skipped, *"which is deliberately not reported as PASS"*. |
| Live Xray / quic-go / masque-go interop | `interop-xray.yml`, `server-linux-amd64.yml` | Workflow declarations only. No artifact in `verify-evidence/`. The QUICHE live interop tests were **deleted** (`0a3993a8c`). |
| Windows PE / MSI / Authenticode / driver-signature / service-lifecycle | nine `scripts/ci/windows/*.ps1`, `client-desktop-windows.yml` | Declarations only. `windows-core-amd64.yml` runs on **ubuntu-latest** and only inspects symbols; the desktop workflow is dispatch-only; no `.ps1` output is archived in the tree. |
| Docker-backed fixtures | `test/docker_test.go`, `test/openvpn_test.go` | Not run — no daemon. Note `test/docker_test.go` **has no skip guard**. |

### The repository's own release verdict

`docs/RELEASE-CERTIFICATE.md` opens with:

```text
NOT READY FOR A STABLE MILESTONE
```

and lists two blockers that "cannot be closed from the development environment": **no real data plane
has ever been exercised** (no TUN, no root, no Linux, no Apple NetworkExtension — L0 route exclusion,
L2 splice, the flow table, DNS hijack on a device, FakeIP on a device and network transitions all have
**zero executed evidence**), and **the Apple products are built and signed by a manual workflow that
has not run against this tree**.

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
bash scripts/ci/check-libbox-abi.sh          # UNWIRED, and its layer 5 would be RED — see §5
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

### The design (sound, and verified in the workflow text)

`release.yml` (`workflow_dispatch` only, requires `inputs.tag`, `dry_run` defaults **false**):

1. `test the release gate` — the gate's own self-tests run **before** the gate is trusted.
2. `check-tag` — `HEAD` **is** the tagged commit; tooling is checked out to `tooling/` so the tag
   checkout cannot delete the script.
3. `publish-flag` — anything not exactly `true`/`false` fails.
4. `plan` — selects **one** successful run per product workflow **for this exact SHA**, with a 90-minute
   timeout. Missing / failed / cancelled / skipped → fail, printing the run URLs.
5. `download` — from the *selected* runs, not a fresh query.
6. **`verify`** — per platform: binary + `.sha256` + `BUILD-INFO`, a real `sha256sum -c`, platform read
   from the file's own header, and the recorded commit must match. *"A hash for one platform does not
   cover the other."*
7. `create the release` — `gh release create … --target "$sha"` with `upload/*.tar.gz` and
   `upload/SHA256SUMS`. Not draft, not prerelease.

> **So the claim "product CI produces/proves artifacts; release downloads and re-verifies those exact
> bytes rather than rebuilding them" is TRUE** — but it is scoped to the platforms the plan step
> selects, and it depends on 4–5 `workflow_dispatch`-only workflows having been dispatched manually
> first.

**Platform coverage, measured at the release baseline:** `release.yml:69` sets
`RELEASE_EXTRA_PLATFORMS: true`, and `release-artifacts.sh:77-85` accepts exactly the strings
`true`/`false` (anything else dies). With it set, **Windows and Android artifacts ARE in the release
set**; only the Apple IPA/DMG is still excluded. The comment at `release-artifacts.sh:63-67` still says
*"Windows, Android and the Apple IPA/DMG are deliberately NOT here"* — **that sentence is stale for
Windows and Android.**

### A live defect in the release path, measured at the release baseline

`release.yml:69` sets `RELEASE_EXTRA_PLATFORMS: true`, which is what the contract checks:

```sh
# release-artifacts.sh:69-86
TARGET_WORKFLOWS=( ".github/workflows/server-linux-amd64.yml" ".github/workflows/client-macos.yml" )
case "${RELEASE_EXTRA_PLATFORMS:-false}" in
  false) ;;
  true)  TARGET_WORKFLOWS+=( ".github/workflows/windows-core-amd64.yml"
                            ".github/workflows/android-core-arm64.yml" ) ;;
  *) die "RELEASE_EXTRA_PLATFORMS must be exactly true or false" ;;
esac
```

The `target_field` table **was** extended — `:110-122` carries full Windows and Android rows for
`binary`, `buildinfo`, `magic`, `platform`, `arch` and `tarball`, so `verify_platform` itself is fine.

**But the release-tree allowlist at `:575-592` is still built as four names per target and never
extended for the extra files those two products upload:**

```sh
allowed+=( LICENSE )
for wf in "${TARGET_WORKFLOWS[@]}"; do
  allowed+=( "$(target_field "$wf" binary)" "$(target_field "$wf" binary).sha256" "$(target_field "$wf" buildinfo)" )
done
while IFS= read -r f; do … [ "$ok" -eq 1 ] || unexpected+=… done < <(find "$dir" -type f -exec basename {} \;)
[ -n "$unexpected" ] && die "unexpected file(s) in the release tree: …"
```

| Product | Files uploaded | Matches the allowlist? |
|---|---|---|
| Linux, macOS | binary, `binary.sha256`, `BUILD-INFO-*.txt`, `LICENSE` | **yes** — exactly |
| **Windows** (`windows-core-amd64.yml`) | those four **plus** the ZIP, `SHA256SUMS`, `build-info.json` | **no** — three unexpected |
| **Android** (`android-core-arm64.yml`) | those four **plus** `libbox.provenance`, `SHA256SUMS.txt` | **no** — two unexpected |

So the `verify` step dies with `unexpected file(s) in the release tree` **whenever
`RELEASE_EXTRA_PLATFORMS=true`**, which the release workflow itself sets. The gate's own self-test
does not cover it: `test-release-artifacts.sh:532-533` asserts `SHA256SUMS` has exactly **two** rows —
the fixtures are still the two-product ones.

Two more stale statements in the same file: the header at `:63-67` still says *"Windows, Android and
the Apple IPA/DMG are deliberately NOT here"* — written before those rows were added — and `:594`
still prints *"both platforms verified"* from a loop that can now run four times.

**This is a FOLLOW-UP for the release owner. It is a knowledge finding, not something this knowledge
base fixes.**

### What is out of scope

`scripts/ci/verify-release-acceptance.sh` enforces a **default required-workflow set** of
`verify.yml`, `interop-xray.yml` and `server-linux-amd64.yml`. **`client-apple.yml` is not in that
set**, and `verify.yml` does not run the Apple ABI gate — so release acceptance is **structurally
incapable** of noticing Apple-pin drift.

### The Apple pin has no wired verifier — state this precisely

The fork adopted a deliberate **single-authority** pin design: `release/apple-client-refs.env` declares
the `clients/apple` gitlink as the sole authority, the refs file is **forbidden** from recording a SHA
(`check-apple-source-selection.sh:124-136`), and the workflow body is **forbidden** from hardcoding one
(`:209-210`). That correctly eliminated duplicate-SHA drift.

It also removed every cross-check against an *intended* revision. The last candidate was the
`VERIFIED_APPLE_SHA` constant in `scripts/ci/check-libbox-abi.sh`, and:

* the constant no longer matches the gitlink (`90a0e9ae…` vs `d0524912…`), so the script's own layer 5
  would fail;
* **nothing runs the script** — no workflow, no other script, no `Makefile` target;
* `c7014ddc1` is proof by demonstration: it moved the pin and **nothing objected**;
* `check-apple-source-selection.sh` could not have caught it **by design** — every one of its checks is
  self-referential to the gitlink (they prove iOS and macOS *agree*, and forbid a second copy; they
  cannot say the revision is the intended one);
* the released revision `d0524912…` is recorded nowhere in a tracked non-`.git` file.

> **Correct wording: "the Apple pin has no wired verifier, and the one candidate verifier is both
> unwired and stale."** Do **not** write "the Apple ABI gate is broken" — that implies a gate that runs.
>
> **What is therefore NOT verified for v0.1.6:** ABI call-site compatibility (the Swift/Kotlin clients
> against the libbox surface) **at the pinned revision**. The recorded PASSes are at
> `bb5309b4a`/`90a0e9ae` — two pin generations earlier — and do not transfer.
>
> **What IS wired and gated:** Apple packaging/install validity, signing environment, branding, macOS
> naming, and source selection — see `libbox-platform-contract.md`.

---

## 6. Build profiles (summary — full detail in `build-pruning-release.md`)

| Profile | Contents |
|---|---|
| `DEFAULT_BUILD_TAGS` | the **Darwin** profile. Includes `with_naive_outbound`. **Cannot build on Windows.** |
| `DEFAULT_BUILD_TAGS_OTHERS` | the same **minus** `with_naive_outbound`. **This is the general/server set.** |
| `DEFAULT_BUILD_TAGS_WINDOWS` | `DEFAULT_BUILD_TAGS` plus `with_purego` |

`verify.yml:214-215` states in-tree that `DEFAULT_BUILD_TAGS` is *"deliberately NOT used here: it
carries with_naive_outbound, and no Linux product links cronet."* The names are misleading and the
repository knows it — **a reviewer who follows `CORE_0.1.6_FINAL_CLOSURE_REPORT.md:18`'s stated tag
source and runs the root package on Windows gets a setup failure and may wrongly conclude the root
suite is broken.**

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

1. **Do not claim anything is `VERIFIED_CI`.** No workflow-run record, run ID or artifact digest exists
   in the repository. Every CI statement is a statement about a **declaration**.
2. **Do not cite `verify-evidence/` for the production baseline.** Its own README disqualifies it.
3. **Do not cite the root `box_*_test.go` suite as executed by CI.** It is compiled and never run.
4. **Do not use `DEFAULT_BUILD_TAGS` to run the root package outside Apple.** Use
   `DEFAULT_BUILD_TAGS_OTHERS`.
5. **Do not run `protocol/masque` or the root package untagged and treat the failures as regressions.**
   They are tag-absence.
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
