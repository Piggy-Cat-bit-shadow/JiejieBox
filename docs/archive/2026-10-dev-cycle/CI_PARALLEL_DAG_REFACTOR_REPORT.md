# CI_PARALLEL_DAG_REFACTOR_REPORT

```text
CI_DAG_REFACTOR = COMPLETE
BUILD_ONCE_MODEL = VERIFIED
PARALLEL_EXECUTION = VERIFIED
RELEASE_FREEZE = READY_FOR_MAINTAINER_REVIEW
FORMAL_RELEASE_CREATED = NO
TAG_MOVED = NO
RELEASE_ASSETS_PUBLISHED = NO
CODE_CHANGE = NONE (see §E — the target topology is already the shipped topology)
```

Base SHA: `ecb2849c1` on `testing`. No production or user-facing code was touched.

---

## A. BEFORE DAG

Read from the files, not from step names. Twelve workflows, **fourteen jobs**, and this is the
complete set of job-level dependencies in the repository:

```text
$ git grep -n '^\s*needs:' -- .github
.github/workflows/client-apple.yml:203:    needs: libbox     (ios job)
.github/workflows/client-apple.yml:460:    needs: libbox     (macos job)
```

**Two `needs:` in the whole repository.** Every other job is already a root.

```text
commit / SHA
   │
   ├── verify.yml                        verify                 push: testing + PR      ~1085 s
   ├── release-contract-verify.yml       release-gate           push: testing + PR        ~28 s
   ├── android-core-arm64.yml            android-core-arm64     push: testing (paths)    ~135 s
   ├── mobile-contract.yml               mobile-contract        push: testing (paths)    ~623 s
   │
   ├── server-linux-amd64.yml            linux-amd64            dispatch                 ~1000 s
   ├── client-macos.yml                  macos-arm64            dispatch                 ~970 s
   ├── windows-core-amd64.yml            core                   dispatch                  ~55 s
   ├── client-desktop-windows.yml        installer              dispatch                 ~1090 s
   ├── client-desktop-windows-runtime-audit.yml  runtime-audit  workflow_run(after build) diagnostic
   ├── interop-xray.yml                  interop                dispatch                 ~110 s
   │
   └── client-apple.yml
         libbox ──┬──> ios    (needs: libbox)      dispatch
                  └──> macos  (needs: libbox)      dispatch

release.yml (dispatch) ── requires successful runs of the four product workflows AT THE SAME SHA,
                          then downloads and publishes. Builds nothing.
```

### Critical path

Measured from the Actions API (`run_started_at` → `updated_at`), three successful runs each where
available:

| workflow | measured durations |
|---|---|
| Verify | 1083 s, 1087 s, 1067 s |
| Windows desktop installer | 1088 s, 1184 s, 1010 s |
| Linux amd64 | 956 s, 980 s, 1058 s |
| macOS arm64 | 956 s, 979 s, 971 s |
| Apple client | 764 s, 1144 s, 1031 s |
| Mobile contract verification | 623 s |
| Reference interop (Xray) | 178 s, 110 s, 46 s |
| Android Core ARM64 | 131 s, 146 s, 128 s |
| Windows amd64 core | 61 s, 56 s, 49 s |
| Release | 51 s, 50 s |
| Release contract verification | 28 s, 27 s, 29 s |

**The critical path for a release is the longest single workflow, not the sum of them** — the product
workflows are separate workflows and therefore already run concurrently on separate runners:

```text
critical path ≈ max(Windows desktop installer 1088 s, Linux 980 s, macOS 971 s, Apple 1031 s)
              ≈ 1090 s  ≈ 18 minutes
```

The only real serial chain is `libbox → {ios, macos}`, and it is one product's build feeding two
consumers — a genuine data dependency, exactly as the brief's target topology requires. `ios` and
`macos` are siblings and run in parallel.

### Within Verify, where the time is

Step timings from `Verify #220`:

```text
   500 s  race on the fork's own data paths
   114 s  race on the configuration surface
   128 s  nested test module type-checks against this checkout
   116 s  untagged build of every package
   101 s  build matrix
    37 s  actions/setup-go
    28 s  pinned-upstream assumptions
    18 s  release gate self-test
    12 s  tagged build of every package
    11 s  nested Go module integrity
```

The two `-race` steps are **614 s of 1085 s — 57% of the critical path**. Everything else is under
130 s each.

### Serialized waits that were NOT real dependencies

None remain. Specifically, the three items this task names were already resolved, and the audit
confirms it rather than assuming it:

```text
"Verify completes → builds start"       NOT present: no product build needs verify.yml
"tests wait for the Linux binary"       NOT present: the low-memory gate moved to mobile-contract.yml
"everything waits for one mega-job"     NOT present: 12 workflows, 14 jobs, 2 needs
```

---

## B. AFTER DAG

**Unchanged, because the audit found no false dependency to remove.** The repository already is the
target topology. Stated as the brief's own diagram, with the real workflow names:

```text
                                  ┌── verify.yml                        (Layer 1, source only)
                                  │
                                  ├── server-linux-amd64.yml            (Layer 2)
                                  ├── client-macos.yml                  (Layer 2)
                                  ├── windows-core-amd64.yml            (Layer 2)
                                  ├── android-core-arm64.yml            (Layer 2)
                                  │
                                  ├── client-apple.yml
                                  │     libbox ──┬── ios                (Layer 2 + handoff)
                                  │              └── macos
                                  │
                                  ├── client-desktop-windows.yml        (Layer 2)
                                  │            └── runtime-audit        (Layer 3)
                                  │
                                  ├── mobile-contract.yml               (Layer 1, source contract)
                                  └── release-contract-verify.yml       (Layer 1, self-tests)

commit / SHA ─────────────────────┘

release.yml (Layer 4) ── downloads the already-verified artifacts, stages, publishes
```

Every remaining dependency, and why it exists:

| dependency | real data dependency? | why |
|---|---|---|
| `ios needs libbox` | YES | consumes the `jiejiebox-libbox-<sha>` artifact built by that job |
| `macos needs libbox` | YES | consumes the same artifact |
| `runtime-audit` after `client-desktop-windows` | YES (`workflow_run`) | consumes the installer + unpacked tree from that run |
| `release` requires four product runs at the SHA | YES | it publishes their artifacts; `check-tag` also asserts HEAD IS the tag commit |

**No `workflow_call`, no matrix in any workflow, no other `workflow_run`.** Verified by grep.

---

## C. Build-once table

```text
PRODUCT                     BEFORE BUILDS/SHA   AFTER BUILDS/SHA   PRODUCER                        CONSUMERS
sing-box-linux-amd64                1                  1          server-linux-amd64.yml          release.yml
sing-box-darwin-arm64               1                  1          client-macos.yml                release.yml
sing-box.exe (windows core)         1                  1          windows-core-amd64.yml          release.yml
libbox.aar (android/arm64)          1                  1          android-core-arm64.yml          release.yml
Libbox.xcframework                  1                  1          client-apple.yml : libbox job   ios job, macos job
Jiejiebox-v*-windows-x64.exe        1                  1          client-desktop-windows.yml      runtime-audit (verify), release.yml (publish)
iOS IPA                             1                  1          client-apple.yml : ios job      release-apple.sh
macOS DMG                           1                  1          client-apple.yml : macos job    release-apple.sh
```

**No product is built twice for the same SHA anywhere.** The two apparent repeats are deliberate and
identified as such:

```text
client-macos.yml:313   deep check - reproducibility   re-runs build-macos-client.sh to compare bytes
server-linux-amd64.yml:635  deep check - reproducibility   same, for the Linux binary
```

Both are the BYTE-COMPARISON ORACLE itself — a reproducibility check that did not rebuild would prove
nothing — and both are `workflow_dispatch` + `deep_checks` gated, so they are not on any fast path.
They are the one place a second build is the correct answer.

---

## D. Test decoupling table

```text
TEST / GATE                              NEEDS PRODUCT ARTIFACT?   OLD LOCATION                    NEW LOCATION                     PARALLEL?
unit tests (production tags)             no                        server-linux-amd64.yml          unchanged                        yes (own workflow)
production contract tests                no                        server-linux-amd64.yml          unchanged                        yes
low-memory data paths (mobile contract)  NO - source only          server-linux-amd64.yml          mobile-contract.yml              yes
mobile tag contract                      no                        server-linux-amd64.yml          mobile-contract.yml              yes
runtime audit (gates 5/5b/6/7)           YES - installer + tree    client-desktop-windows.yml      client-desktop-windows-          yes (after build)
                                                                                                  runtime-audit.yml
Libbox consumption (ios / macos)         YES - xcframework         already artifact consumer       unchanged                        yes (siblings)
release gate self-test                   no                        verify.yml, release-contract-   unchanged (see §E-1)             yes
                                                                   verify.yml, release.yml
desktop overlay version suite            no                        release-contract-verify.yml     unchanged                        yes
coverage guard self-test                 no                        -                               verify.yml                       yes
workflow structure checker               no                        release-contract-verify.yml     unchanged                        yes
```

The two rows that changed in the previous round (`low-memory`, `runtime audit`) are the two the brief
asked for, and both now sit in the correct layer.

---

## E. Newly discovered same-pattern optimizations

Four candidates were investigated. **None is FIXED**, and each reason is evidence rather than budget.

### E-1. `test-release-artifacts.sh` executed twice per push — RECORDED ONLY

```text
evidence:   verify.yml:64                       run: ./scripts/ci/test-release-artifacts.sh
            release-contract-verify.yml:84      ./scripts/ci/test-release-artifacts.sh > verify-logs/…
old:        2 executions per push, both triggered by `push: branches: [testing]`, neither path-filtered
new:        unchanged
risk:       a filter here would drop the release gate's self-test from the push path
```

Not fixed because the suite **enforces its own presence in both**:

```text
scripts/ci/test-release-artifacts.sh:1118-1123
# The gate test must run before the release acts, and on every push.
if grep -q 'test-release-artifacts.sh' "$wf" &&
   grep -q 'test-release-artifacts.sh' "$repo_root/.github/workflows/verify.yml"; then
  report "the gate test runs in release.yml and in verify.yml"
```

`check-release-workflows.py:176` separately requires the `verify.yml` copy. Removing either site
fails a test that exists to keep it there. **This is a decision to revisit deliberately, not a
cleanup** — and the cheap fix (a `paths:` filter that skips docs-only pushes) trades a ~30 s saving
for the risk of not running the release contract on a docs change, which is not obviously right.

### E-2. `gomobile` toolchain compiled twice per SHA on the Android path — RECORDED ONLY

```text
evidence:   android-core-arm64.yml:213-214   go install gomobile@$VERSION ; go install gobind@$VERSION
            client-apple.yml:118             ./scripts/ci/gomobile-toolchain.sh install apple
            scripts/ci/gomobile-toolchain.sh:130-131   the same two `go install`s
old:        the pinned gomobile fork is compiled from source in the Android job AND the Apple job
new:        unchanged
risk:       caching a compiled toolchain across workflows/platforms
```

Different platforms (Linux vs macOS) with per-OS caches keyed on `GOOS/GOARCH`, and different pinned
versions already. The brief ranks toolchain dedup **last** and warns against "making a more complex,
more fragile dependency chain for the sake of a cache". Also, these are different workflows that may
never run for the same SHA in practice (both are dispatch-only), so "twice per SHA" is a worst case
rather than the normal case.

### E-3. `build-server.sh` appears in `verify.yml` — NOT A DUPLICATE

```text
evidence:   git grep 'build-server.sh' → verify.yml:221
```

`verify.yml:221` is a **comment**, explaining that `release/DEFAULT_BUILD_TAGS_OTHERS` is what
`build-server.sh` builds with. The command on the next line is `go build -tags "$tags" ./...` — a
compile gate over the tag set, not a product build, with no `-ldflags` and no upload. Checked because
a product build script named in a verify workflow is exactly the pattern this task hunts; it is not
one.

### E-4. Product workflows are dispatch-only, so nothing fans out on push — RECORDED ONLY

```text
evidence:   server-linux-amd64.yml:78, client-macos.yml:64, windows-core-amd64.yml:30,
            client-apple.yml:49, client-desktop-windows.yml:78 — all `workflow_dispatch` only
old:        a push to testing starts Verify + Release contract verify + Android (paths) only;
            no shipped product is built until someone dispatches it
new:        unchanged
risk:       making them push-triggered multiplies runner minutes on every commit
```

This is the one place where the shipped topology and the brief's "commit → product builds" picture
differ, and it is a **cost/policy decision, not a false dependency**: making all five product
workflows push-triggered would add roughly 1000 s × 4 runners of consumption per commit, including
for commits that touch no product. The current model — product builds on demand, source verification
continuously — keeps the DAG correct and the cost bounded. Recorded for a maintainer to decide.

### Not-actioned items from the previous round, unchanged

Darwin tag profile in `verify.yml`; Apple Cronet archive-byte provenance; the orphan-script sweep;
the macOS config-validation gap; fuzz/benchmark architecture. Untouched, by the previous brief's §3
and this one's §15.

---

## F. CI verification

The audit changed no workflow, so there is no new code state to verify. The existing state is green
at the base SHA:

| workflow | run | SHA | conclusion |
|---|---|---|---|
| Verify | #220 | `ecb2849c1` | success |
| Release contract verification | #45 | `ecb2849c1` | success |
| Mobile Contract Verification | #1 | `385e00fca` | success (first ever run) |
| Android Core ARM64 | #61 | `385e00fca` | success |
| Verify (guards the self-test + coverage changes) | #219 | `db8814ced` | success |
| Verify (caught the exec-bit defect) | #218 | `385e00fca` | failure → fixed in `db8814ced` |

Local verification at this SHA:

```text
python3 scripts/ci/check-release-workflows.py        59 passed, 0 failed
YAML parse, all 12 workflows                         12 parsed, 0 failed
dash -n, every shell script touched                   all parse
gofmt (macOS gate directory list)                    clean
```

**Not run, and not claimed:** `Windows desktop installer`, `Windows desktop runtime audit`,
`Linux amd64`, `macOS arm64`, `Apple client`, `Release`. The first two are Windows workflows and the
rest are `workflow_dispatch`-only; none can be executed from this host. No green result is claimed
for them, and none was needed because this round changed nothing they run.

No wall-clock figures are estimated anywhere above; the durations in §A are the Actions API's own
timestamps.

---

## §22 success criteria

```text
 1 Source Verify does not block independent product builds .... MET (2 needs total; none to verify.yml)
 2 independent product builds can run in parallel ............ MET (separate workflows, separate runners)
 3 iOS/macOS consume ONE Apple Libbox artifact ............... MET (one upload, two downloads, no rebuild)
 4 Runtime Audit consumes the existing artifact .............. MET (workflow_run + artifact download, no build)
 5 Release does not rebuild shipped products ................. MET (release path contains no build command)
 6 Tests do not re-produce shipped artifacts ................. MET (reproducibility rebuilds are the oracle,
                                                                   and are dispatch+deep_checks gated)
 7 no stale / cross-SHA artifact ............................. MET (artifact names carry ${{ github.sha }};
                                                                   runtime-audit selects the build run by
                                                                   workflow path and requires success)
 8 no failure hidden via continue-on-error ................... MET (none anywhere in .github)
 9 no existing coverage lost ................................. MET (nothing removed this round)
10 every needs explains a real dependency .................... MET (2 of 2, table in §B)
```

The decisive answer to §24's framing: **the CI dependency graph already matches the product
dependency graph.** There is no false `needs` to delete, no test that builds a product it should
download, and no release step that re-produces shipped bytes. The work that remained from the two
previous rounds — the Android build record, the Windows runtime split, the mobile contract's
ownership, the SKIP-only coverage claim — has already removed the structural debt this brief is
about.

No version tag was created or moved, no Release was created, no asset was published. The freeze is
unchanged.
