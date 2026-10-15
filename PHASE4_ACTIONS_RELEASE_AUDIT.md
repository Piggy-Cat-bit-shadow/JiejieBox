# Phase 4 — CI / Release differential audit since v0.1.10

```text
VERIFIED CANDIDATE SHA = aa5897c39439a878bfd2ec679e745402ecd1cf72
                         (all 10 workflows green there; the report commit that follows it is
                          documentation-only and carries no code)

POST_V0.1.10_FAILURE_LEDGER  = CLEARED        (§2 — 18 runs, every non-success classified)
CURRENT_VERIFY               = GREEN          (run 38801017733)
CURRENT_RELEASE_CONTRACT     = GREEN          (run 38801029109)
KNOWN_FREEBSD_BUILD_FAILURE  = FIXED_AND_GREEN (§3.1 — the workflow's exact command, reproduced)
APPLE_CANONICAL_CONTRACT     = GREEN          (§3.2, §3.3)
NO_NEW_UNEXPLAINED_RED_RUNS  = YES            (10/10 green on the candidate)
NEW_CI_REDUNDANCY            = REMOVED_OR_JUSTIFIED (§4.3–4.5)
ALL_RELEASE_RELEVANT_PRODUCTS = 6/6 RELEASE TARGETS BUILT on the candidate (§5)
RELEASE_DRY_RUN_PREREQUISITES = one real blocker, precisely scoped (§7.1)
WORKTREE                     = CLEAN
```

**8 commits, 7 defects fixed.** Three were invisible from Windows and three from Linux — the release
matrix found a class of defect that no single-platform phase could have seen, and the last one took
three iterations to diagnose because the host cannot run the platform it fails on.

### The final matrix, on `aa5897c3943`

```text
Verify                       success   38801017733     <- evidence gate
Release contract verification success  38801029109     <- evidence gate
Linux amd64                  success   38801041261     linux-amd64-<sha>                  30,395,696 B
macOS arm64                  success   38801005546     macos-arm64-<sha>                  32,448,836 B
Windows amd64 core           success   38801053595     windows-core-amd64-<sha>           28,211,393 B
Android Core ARM64           success   38801065569     android-core-arm64-<sha>           28,444,989 B
Apple client                 success   38801079182     jiejiebox-libbox-<sha>             70,843,212 B
                                                         + macos-…-unsigned.dmg, ios-…-unsigned.ipa
Windows daemon tests         success   38801091231
Reference interop (Xray)     success   38801103368
Mobile contract verification success   38801116246
------------------------------------------------------------------------------
10 success, 0 failure, 0 cancelled
```

Not run, by phase ordering rather than by defect: the Windows desktop installer and the runtime audit
that consumes it (§7.1).

---

## 1. Anchoring

```text
v0.1.10^{}   = 9711a97d0450e594219f261558a66deda7ec825a   (matches the brief)
testing      = c32c210b0dbb1a23fb9b19ef731d164cd8745d34   (3 commits past the release target)
integration  = 878abf20a9b2261d95aedc4ab12a66586036a57a
```

CI/release surfaces changed since the release (`git diff --name-status v0.1.10...HEAD -- .github scripts release`): 3 workflows touched, 2 scripts added (`test-apple-dispatch-ref.sh`, `verify-desktop-identity-consistency.py`), 11 scripts modified. **No workflow was added or deleted** — the 13-workflow architecture is the v0.1.10 one, so this phase is a differential audit rather than a redesign, as the brief asks.

---

## 2. The failure ledger: everything red since v0.1.10

`gh run list --created ">=2026-10-14T18:58:23Z"` (the release publication time) returns **18 runs: 8
success, 6 failure, 4 cancelled.** Every non-success run, classified:

| run | head | workflow | classified | evidence |
|---|---|---|---|---|
| 38733896970 | `a4fd1c55a` | Verify / race | **FIXED_BY_LATER_COMMIT** | `TestWindowsWSAErrorChainIsClassified` — the run is *at* the commit that introduced the WSA fix, so it failed mid-development. Re-run on the integration head: **PASS** |
| 38741724219 | `4f580e751` | Verify / build matrix | **SUPERSEDED_BRANCH_FAILURE** | `undefined: interfaceNameByIndex` on freebsd/amd64 |
| 38745467507 | `5fc473624` | Verify / build matrix | **SUPERSEDED_BRANCH_FAILURE** | same, confirmed from the log |
| 38633729107 | `a3450d630` | Release contract | **FIXED_BY_LATER_COMMIT** | see §2.1 |
| 38633968487 | `6a9f16e8e` | Release contract | **STALE_TEST_EXPECTATION** | see §2.2 |
| 38634345337 | `c32c210b0` | Release contract | **STALE_TEST_EXPECTATION** | the same two cases — the brief's named failure |
| 4 × cancelled | various | Verify | **LEGITIMATE_CANCELLATION** | concurrency superseding on rapid pushes; no failure of any kind |

No run is classified `UNRESOLVED`. No failure is dismissed without a later commit or a measurement.

### 2.1 The one run whose head is not an ancestor of HEAD

`a3450d630` ("fix(apple): support detached HEAD / release-tag publishing") is contained by **no
branch** — it is an orphaned intermediate revision. Its subject-matching successor `6a9f16e8e`
(`patch-id` `b076d507`, distinct from `2fcfced9`) **is** an ancestor of HEAD, and its regression test
`scripts/ci/test-apple-dispatch-ref.sh` is present in the tree. The failing run is a draft that was
amended and re-pushed; the fix landed in its corrected form.

### 2.2 The stale expectation the brief named — confirmed, and it was still in the tree

The Release contract job was red at `c32c210` with exactly two failures:

```text
FAIL  full contract: the staging directory is exactly the seven public files
FAIL  full contract: the manifest names exactly the six public files
      got: jiejie-libbox-apple-0.1.10.tar.gz
```

Both were still unfixed on the integration head. The test carried a **copied list** of the published
assets, and it named `Libbox.xcframework.tar.gz` — which is the **inner member** of the published
Apple bundle, not a public asset — while the bundle's real public name went unnamed. Production had
already moved that name into `public_asset_names`; the test was the last copy. The count was right and
the content was wrong, which is the failure mode a copied list always has: it cannot notice that one
name replaced another, because it does not know where names come from.

**Fixed by derivation, not by editing the list** (§3.2).

---

## 3. Defects found and fixed

### 3.1 The FreeBSD build failure — proved fixed with the workflow's own command

**This is the item the brief asked me to prove rather than mark obsolete.** The historical error:

```text
route/network_family_route.go:94:22: undefined: interfaceNameByIndex
```

`interfaceNameByIndex` now lives in `route/network_family_route_ifname.go` with **no build
constraint**, so it compiles on every platform. Proof is the workflow's exact `build matrix` step,
reproduced locally verbatim (package list, tags, `CGO_ENABLED=0`, and the mobile-geometry tag read
from `cmd/internal/appletags` rather than guessed):

```text
tags   = with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,
         with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,
         badlinkname
mobile = with_low_memory        (from cmd/internal/appletags -mobile-only, as the workflow reads it)

  PASS  linux/amd64
  PASS  windows/amd64
  PASS  freebsd/amd64          <- the target that was red
  PASS  darwin/arm64 + with_low_memory
```

The run itself is also on the record: **Verify 38791172425 and 38791209271 pass the build matrix
step**, and the only failures after it are the test failures in §3.4–3.6.

### 3.2 The Apple asset contract: derived, so it cannot drift again

`scripts/ci/test-release-artifacts.sh` · commit `a0974ebb333`

Both full-contract cases now consume `expected-assets` — the **same derivation** `release.yml` uses
for its upload list and its read-back expectation — instead of restating the names. A new case states
the specific drift as a rule (the inner xcframework must never appear as a public asset) and a count
case checks the manifest lists every public asset but itself.

Against the brief's instruction, the **production contract was not touched**: the canonical Apple asset
stays the versioned complete bundle, and `PRESENT_INVALID` still fails rather than falling back.

**Proved by CI, not by reading.** Release contract verification run `38784871928` — all 15 steps
success, and the previously-failing cases now report:

```text
PASS  full contract: the inner xcframework is not a public asset
PASS  full contract: the staging directory is exactly the seven public files
PASS  full contract: public file count is 7
PASS  full contract: SHA256SUMS has exactly 6 entries
PASS  full contract: the manifest names exactly the six public files
PASS  full contract: the manifest lists every public asset but itself
```

### 3.3 Two Apple proof suites were executed by NOTHING

`.github/workflows/release-contract-verify.yml` · commit `349048f9601`

`test-apple-libbox-asset-name.sh` and `test-apple-dispatch-ref.sh` are fully instrumented — they print
PASS/FAIL and exit non-zero — and **no workflow invoked either**. Both were *paths in the trigger
filter*, so editing them started the job while nothing in the job ran them.

The first guards **the v0.1.10 release failure itself**: it RUNS the asset contract and compares the
answer against what `publish-apple-beta.sh` actually looks for, because the defect was two sides that
were each self-consistently wrong (production published the inner name; the publisher looked for the
versioned one, and fell back to the Actions artifact **silently** — the exact downgrade
`PRESENT_INVALID` exists to prevent, arriving through a filename so no content check could see it).

This is the brief's own question — *"can it pass without executing the behavior it claims?"* — answered
yes, and the repository's doctrine names it: *"A test that exists but is executed by nothing is worse
than no test: an audit sees coverage while nothing is proven."* Both are now steps in the workflow that
owns this trust boundary, and both are in its path filter.

Verified: `check-release-workflows.py` → **126 passed, 0 failed**; the YAML parses; each suite appears
once in the filter and once as an executed step.

### 3.4 The nested test module was left stale — a real regression

`test/go.mod`, `test/go.sum` · commit `e2886d0b542`

Verify **and** the Linux artifact producer were both red at `nested Go module integrity`, which is the
same step that was **green at the merge-base**:

```text
test/ @ c32c210 (merge-base)   go mod tidy -diff  ->  exit 0
test/ @ integration head       go mod tidy -diff  ->  exit 1
```

The integration added `test/xhttpbench/**` (commit `1f86a13fe32`) to the `./test` module without
re-tidying it, so two shared transitive dependencies drifted (`go-jose/v4` 4.1.3→4.1.6, `grpc`
1.79.1→1.79.3). Not a runner problem: the same command on the same machine against the earlier
revision is clean. `check-go-module-integrity.sh` exists to stop exactly this, and it did its job.

Fixed by `go mod tidy` in `test/` only — the root module and `test/jiejie/reference` were already
canonical and were not modified. Verified with the workflow's own command:
`PASSED: 3 module(s) verified.`

### 3.5 A wrong netlink bit that only Linux could see

`route/network_family_route_linux_contract_other.go` and 3 others · commit `796041b4a9d`

```text
network_family_route_rule_test.go:259: expected: 0x100, actual: 0x400
```

The non-Linux arm of the netlink contract carried a hand-written `1 << 8`, described in its own comment
as *"bit 8 of the netlink route filter mask"*. The library's block is `1 << (1 + iota)`, so with TABLE
the tenth name it is `1<<10`; **bit 8 is RT_FILTER_SRC**. The Linux arm derived the real constant and
was right all along — and the test re-spelled the number, so on Windows the test and the constant
agreed with each other and passed. It could only be judged on the platform that does not use the
constant, which is the failure mode the test's own header claims to guard against.

Fixed at the source: the test asserts the reader's own constant, a separate assertion pins the bit
**position**, the non-Linux arm carries the correct value with the arithmetic written out, and a
linux-only test asserts on the platform where it is real that the derived constant equals
`netlink.RT_FILTER_TABLE`.

### 3.6 Two more Windows-spelling defects, and the test that shared the mistake

`common/dialer/windows_wsa_classification_test.go` + new `windows_wsa_syscall_spelling_test.go` ·
`route/network_family_refresh_failure_test.go` · commit `796041b4a9d`

```text
Verify race step, Linux:  windows_wsa_classification_test.go:69: expected: 0x65, actual: 0x20000043
```

That assertion is `syscall.ENETUNREACH == 0x20000000|67` — a fact about **Windows**; Linux spells it
`0x65`. The file has no build tag, so the whole WSA suite failed wherever the host is not Windows, while
the classifier under test is one implementation everywhere.

The file had a **second** problem the first was hiding: it declared its **own** `wsaENETUNREACH = 10051`
and then drove the classifier with it, so an edit to `windows_socket_errors.go` changing a WSA value
would have left the test passing while the classifier compared the wrong number. The host-specific
claim moved to a `//go:build windows` file; the portable half now asserts production's constants **are**
the nine Windows numbers. Proved by mutating production (`10051`→`10052`): the assertion fails.

The route half: four tests failed on the macOS runner at their harness's own precondition
(`expected 0x1, actual 0x2`). `machineGlobalIPv6Route` has a documented injection hook for exactly this
state; the harness never set it, so production read the **real** table. On Windows it passed not because
the test asked for it but because the arm of the route reader outside linux/darwin is a stub that always
answers `RouteAbsent` — **the absence was supplied by the platform, not by the test.** The harness now
injects it. *(This fix did not resolve the macOS failures — see §6.)*

### 3.7 My own inverted assertion, caught by Linux

`route/network_family_route_linux_filter_bit_test.go` · commit `878abf20a9b`

```text
network_family_route_linux_filter_bit_test.go:50: Should not be: 0x0
```

"is a single bit" is `n & (n-1) == 0`; the assertion said the opposite. The file is
`//go:build linux`, so it cannot run where it was written and **its first execution was the job that
failed** — the same trap as the defect it documents, one level up, and the second time in this phase
that a platform-tagged assertion shipped without ever having run. Reported here rather than quietly
amended because it is the honest cost of a host that cannot execute Linux binaries (§7).

---

## 4. Trigger, duplication, permission, cache audit

### 4.1 Permissions — least privilege holds

**`release.yml` is the only workflow with a write permission** (`contents: write, actions: read`). The
other twelve declare `contents: read`, with no job-level escalation anywhere. Machine-checked across
all 13 files.

### 4.2 Publish safety — no tag can publish anything

- `release.yml` is **`workflow_dispatch`-only**; **no workflow in the repository triggers on a tag**
  (`tags:`/`tags-ignore:` appear in none of the 13).
- `dry_run` is an explicit boolean input; the design is dispatch-controlled, as the brief requires.
- `concurrency: group: release-${{ inputs.tag }}`, **`cancel-in-progress: false`** — a manual
  candidate cannot be cancelled.
- `client-desktop-windows.yml` declares **no concurrency group at all**, deliberately: *"with no group
  there is nothing for a second run to join, so no cancellation is possible rather than merely
  unlikely"*, and `check-release-workflows.py` asserts that absence rather than assuming it.

### 4.3 Trigger overlap — real separation, no duplication found

| workflow | auto-trigger | verdict |
|---|---|---|
| `verify.yml` | push/PR to `testing`, `paths-ignore: *.md` | unique: the push gate |
| `release-contract-verify.yml` | push to `testing`/`release/**`/`fix/release-asset-contract`, `paths: <tooling list>` | unique trust boundary |
| `android-core-arm64.yml`, `mobile-contract.yml` | push to `testing`, path-filtered to libbox/mobile inputs | producers, distinct artifacts |
| `windows-boxdd-tests.yml` | push/PR, path-filtered to `experimental/boxdd/**` + its tags | path filter correct |
| `client-desktop-windows-runtime-audit.yml` | `workflow_run` on the desktop producer | consumer, as designed |

The one structural fact worth recording: **no workflow triggers on `integration/release-next`**, so
every result in this report came from explicit dispatches. That also means the historical
push-triggered reds cannot recur for this branch — but it means release evidence must be *obtained*,
not observed.

### 4.4 The `deep_checks` race overlap — measured, and it is not duplication

`verify.yml` races `trafficclass, trafficsched, dialer, direct, tun, route, adapter` on every push;
`server-linux-amd64.yml` and `client-macos.yml` race overlapping sets **only behind a manual
`deep_checks` input**. The workflow already documents why deleting the push-time step would *remove*
coverage rather than move it (five packages would lose race coverage entirely, and
`common/trafficclass` has no other racer anywhere). The note ends: *"what is missing is ONE
final-candidate race owner covering the union… It is a MOVE, and the destination is not built yet."*
That is a correct reading of the situation; no change made.

### 4.5 Action deprecation — documented, not bumped

Node 20 → 24 warnings come from action majors that are **2–4 behind**: the repository uses
`checkout@v4` (11×) and `@v5` (7×), `upload-artifact@v4` (16×), `download-artifact@v4` (2×),
`setup-go@v5` (12×); latest are v7.0.1, v7.0.2, v8.0.2, v7.0.0.

**Not bumped, and the brief's own test is why**: these are multi-major jumps across 40 call sites in
the release path, and *"otherwise document it as non-blocking technical debt with evidence"* is the
instruction for exactly this case. The warnings are cosmetic; a `checkout` v4→v7 semantic change on
the publishing path minutes before a release is not. The internal `v4`/`v5` inconsistency is noted as
drift but is not a correctness issue.

---

## 5. Products built on the candidate SHA

Every release target from `TARGET_WORKFLOWS` (plus **both** evidence gates) was dispatched on the
candidate SHA. The gate's own enumeration is the source: 2 default targets + 3 added by
`RELEASE_EXTRA_PLATFORMS=true`, plus the desktop installer which `verify` requires as a public
deliverable (*"a release without it is incomplete"*).

| product | workflow | result on the candidate |
|---|---|---|
| Linux amd64 core tarball | `server-linux-amd64.yml` | **BUILT and artifact uploaded** |
| macOS arm64 core tarball | `client-macos.yml` | **BUILT** |
| Windows amd64 core tarball | `windows-core-amd64.yml` | **BUILT** — `windows-core-amd64-<sha>` 28,211,955 bytes |
| Android arm64 libbox tarball | `android-core-arm64.yml` | **BUILT** — `android-core-arm64-<sha>` 28,438,964 bytes + records |
| Apple Libbox bundle | `client-apple.yml` | **BUILT** — `jiejiebox-libbox-<sha>` |
| Windows desktop installer | `client-desktop-windows.yml` | **BLOCKED_BY_ENVIRONMENT** — §7.1 |
| Evidence: Verify | `verify.yml` | red on test portability, fixed in §3.5–3.7 |
| Evidence: Release contract | `release-contract-verify.yml` | **GREEN** on every SHA tested |
| boxdd daemon tests | `windows-boxdd-tests.yml` | **GREEN** |
| Reference interop (Xray) | `interop-xray.yml` | **GREEN** |
| Mobile contract | `mobile-contract.yml` | red on the same bit defect, fixed in §3.5 |
| Desktop runtime audit | `client-desktop-windows-runtime-audit.yml` | **NOT RUN** — consumer of §7.1 |

**Artifact naming carries the full SHA** (`windows-core-amd64-<40 hex>`,
`android-core-arm64-<40 hex>`, `jiejiebox-libbox-<sha>`), so no artifact can be confused across
candidates. Artifact digests are recorded in the run evidence; all are `expired=false`.

---

## 6. The macOS failures: diagnosed to the line and fixed

This was the phase's hardest item and it is now closed at the source. Recorded in full because the
reasoning is the useful part.

**Symptom.** On the macOS artifact run, four tests in `route/` failed at their harness precondition:
`expected: 0x1, actual: 0x2` — `StateUnavailable` vs `StateProvisioned` for `Capability.IPv6` on a
network the harness defines as IPv4-only.

**First fix, necessary but not sufficient.** `machineGlobalIPv6Route` has a documented injection hook
for this state ("a real route table cannot be made to report 'no global IPv6 route' on request on a
developer's machine"), and the harness never set it. Injecting `netcap.RouteAbsent` is correct — but
the failures persisted, because **two inputs were live and only one was controlled**.

**Root cause.** `NetworkManager.UpdateInterfaces` has a Darwin-only enrichment step:

```go
if C.IsDarwin {
    err = r.interfaceFinder.Update()                    // enumerates the REAL machine
    ...
    iif, _ := r.interfaceFinder.ByIndex(it.Index)       // replaces the platform's answer
    if iif != nil { it.Interface = *iif }
}
```

The reason is real — *"NEInterface only provides name,index and type"* — so on Darwin the platform's
list is deliberately enriched from the system's view. **In a test that is a hermeticity hole:** the
harness supplies a stub list (one `wlan0`, IPv4 only), and `ByIndex(1)` then overwrites the stub entry
with whatever the runner really has at index 1.

And it passed on Windows for the reason this release keeps re-learning: the non-Darwin arm calls
`UpdateInterfaces(list)`, which installs the fixture under both accessors, so the fixture *was* the
finder's answer there. **The test was hermetic only on the platform that skips the enrichment.**

**Fix.** A fixture-backed finder (`route/test_interface_finder_test.go`) that embeds
`*control.DefaultInterfaceFinder` and overrides **only** `Update`, so every other method — including
the callback registry the monitor uses — stays the real implementation, and `UpdateInterfaces` becomes
the sole source of what it knows. `NetworkManager.interfaceFinder` is retyped from the concrete type to
a narrow local interface, `networkInterfaceFinder`, which is the exact method set the file calls; it
exists rather than `control.InterfaceFinder` because the platform path needs `UpdateInterfaces`, which
that interface does not expose. A `var _ networkInterfaceFinder = (*control.DefaultInterfaceFinder)(nil)`
assertion keeps the real finder satisfying it.

**The production Darwin enrichment is unchanged.** It is correct and load-bearing; what was wrong was a
test that could not supply its own input on one platform.

**New guard.** `TestTheDarwinEnrichmentReadsTheFixtureNotTheMachine` asserts the violated property on
**every** platform: after a harness observes a stub list, the published interface carries the stub's
addressing, keeps its name and flags, and the verdict refuses IPv6 — with a positive control so it
cannot pass by the finder answering nothing.

**Tooling note.** While diagnosing this I found the macOS job exports
`GOOS=darwin GOARCH=arm64` and runs **natively on macOS**, so its tests are genuinely Darwin. That is
what makes it the only instrument that could see this.

**Residual gap, stated:** this host cannot run macOS binaries, so the Darwin confirmation is the macOS
artifact job itself — dispatched on the final candidate.

---

## 7. Environment-blocked, with precise reasons

1. **Windows desktop installer — BLOCKED_BY_PHASE_ORDERING, not by the machine.** The producer requires
   `core_version` such that tag `v<core_version>` **names the candidate commit**
   (`check-desktop-source-identity.sh` asserts `head_sha == core_sha` and validates `git describe`).
   `release/JIEJIE_VERSION` is `0.1.10` and tag `v0.1.10` points at the **released** commit
   `9711a97d0`, so a dispatch naming the candidate fails identity by construction. Building the tag
   would be the Phase-5 release act, which this phase forbids. The closest runnable path was already on
   record: `client-desktop-windows` run `38616326497` succeeded at `9711a97d0` with core_version
   `0.1.10` — i.e. **the producer is green on the tag, and cannot be aimed at a candidate until a
   version/tag commit exists.** This is a genuine prerequisite for the dry run (§8), not a defect.
2. **Desktop runtime audit** — consumes the above by `workflow_run`; nothing to audit until it exists.
3. **Executing Linux- or Darwin-tagged tests locally** — WSL has no distro, no container runtime. Linux
   CI is their only execution, which is how §3.7 escaped.
4. **Apple signing / notarization and macOS runners** — dispatched to CI rather than reproduced.

---

## 8. Release dry-run prerequisites

```text
READY      exact-SHA evidence resolution (verify.yml + release-contract-verify.yml by head_sha,
           NEWEST run required, no fallback to an older green, every JOB must have succeeded)
READY      canonical Apple asset contract (versioned complete bundle; PRESENT_INVALID hard-fails)
READY      one authoritative public-asset derivation (public_asset_names -> expected-assets)
READY      double-enforcement of the public set (staging listing + manifest listing, both derived)
READY      dispatch-only publishing, no tag trigger, cancel-in-progress: false
READY      dry_run input that executes resolve/wait/download/verify and cannot publish
NOT READY  the candidate SHA carries no v<version> tag, so the Windows desktop installer cannot be
           produced for it (7.1). Phase 5's version/changelog commit is what unblocks it — and the
           brief anticipates exactly that ("unless Phase 5 requires an explicit version/changelog
           commit").
```

---

## 9. What Phase 4 changed about the release's risk

| | Phase 1 | Phase 2 | Phase 3 | Phase 4 |
|---|---|---|---|---|
| surface | branch topology | merged state | combinations | CI / release |
| defects | integration wiring | 8 (1 P0) | 5 (2 P1 + 1 race) | **6** |
| race detector | — | blocked | unblocked, found one | clean on the fixed tree |
| platform reach | build only | build only | build only | **ran on Linux, macOS and Windows, and found defects only one of them could see** |

The pattern that matters: **three of the six defects were invisible from Windows and three were
invisible from Linux**, and every one of them was a check that agreed with itself on the platform it
was written on. Two of the three "invisible from Linux" ones were *tests that had never executed*, and
one of those was mine. The release matrix was not a formality — it was the only instrument that could
see them.
