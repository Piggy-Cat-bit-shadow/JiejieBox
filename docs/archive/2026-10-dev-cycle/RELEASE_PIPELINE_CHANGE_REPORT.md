# RELEASE_PIPELINE_CHANGE_REPORT

Everything changed on the release/CI surface in this work, with its verification status. Ordered by
what it protects. **No product code is included**, because none was changed.

```text
COMMITS        70e4cb52a, 4d90e8cc3, e0af1beed, 7060e0cac, 6737404d1, 857a4ed38, d2782aa24
PRODUCT CODE   unchanged
```

---

## 1. Release-blocking defects found and fixed

### 1.1 The Windows capability gate read the LINUX package graph — FIXED

```text
symptom   FAIL: protocol/masque is not in the release build's package graph
          reported against a WINDOWS artifact, non-deterministically
cause     `go list` reads GOOS/GOARCH/CGO_ENABLED from its own environment; the build step's
          `export` does not cross a GitHub Actions step boundary, so the check described the runner
fix       the capability step exports the target explicitly, from workflow-level values, and prints
          the target plus the host
verified  windows-core #37 (38445964597) success, and again in later runs
```

`server-linux-amd64.yml` and `client-macos.yml` carried the same latent shape at 11 further
invocations — correct only because each runner's platform happens to match its product. All are now
explicit.

### 1.2 The same gate was also a SIGPIPE false-negative generator — FIXED

```text
symptom   the identical error, but from a different cause
cause     `go list -deps | grep -qx` under `set -o pipefail`: grep exits at its match, closing the
          read end; go list is still writing, takes SIGPIPE, exits 141, and pipefail fails the
          pipeline even though grep SUCCEEDED
measured  the Windows graph is 1214 packages and the masque line is the LAST one, so grep always
          exits while go list still has output queued - the race is the typical case
fix       the graph is written to a file and the consumer reads that file
verified  negative tests: present -> PASS; genuinely absent -> FAIL; producer fails -> FAIL named as
          a producer failure. windows-core #40 success.
```

A second real instance was found by the repo-wide audit and fixed the same way:
`test-apple-signing.sh`'s "no signing material is tracked" check piped `git ls-files` — **209 KB** at
this HEAD, three times the 64 KiB pipe buffer — into `grep -qiE`. Because that check is negated, a
false pipeline failure reported tracked signing material on a clean tree.

### 1.3 The Android artifact carried two non-public files, and the first fix was a silent no-op — FIXED

```text
symptom   FAIL: unexpected file(s) in the release tree: SHA256SUMS.txt, libbox.provenance
cause     the android workflow uploads those two as build evidence beside its release payload
fix #1    `rm -f release-assets/libbox.provenance ...` - a ROOT-RELATIVE path that matched nothing,
          and `rm -f` succeeds when it removes nothing, so the step reported success while doing
          nothing. Only a real dry run could catch this.
fix #2    the files are searched for under the staging tree; the step reports what it removed, or
          says none were present
```

The deeper point is recorded in `RELEASE_PIPELINE_FULL_AUDIT.md` §5: the mock `gh` did not reproduce
`gh run download`'s artifact-directory nesting, which is why fix #1 passed its own suite. **That
fixture gap is the reason a change to this step is not trustworthy until the mock is corrected** — see
`RELEASE_PIPELINE_REFACTOR_PLAN.md` Batch 2.

### 1.4 A fairness test could fail before the normal lane was ever scheduled — FIXED

```text
symptom   TestNeitherLaneStarvesWhenBothAreBusy: "0" is not positive
cause     the test waited for the two lanes COMBINED to reach 200 grants, then asserted neither had
          been starved. A busy high lane satisfies 200 alone, so the wait returned with
          counts[normal] == 0 and the liveness assertion failed on a scheduler that starved nothing.
measured  ~200 grants accumulate in well under a millisecond locally (high ~1000, normal ~900), which
          is less time than a loaded macOS runner needs to schedule the second goroutine
fix       wait for EACH lane to reach a floor
verified  60 repetitions: 60 RUN, 60 PASS, 0 FAIL
```

### 1.5 Apple resolver: three real bugs, found by executing it — FIXED

```text
1  `ls-remote` ignored APPLE_CLIENT_REF / APPLE_CLIENT_SHA, so a configured override would have been
   silently ignored in the run-wide resolution
2  a short TAG name never peeled: the candidate list asked for a ref literally named `v1`, found the
   TAG, never requested the peel line, fell through to the branch arm and returned the BRANCH TIP
3  `checkout macos` died with "APPLE_CLIENT_REPOSITORY: parameter not set", because `resolve_sha` runs
   inside a command substitution and the refs file it sources does not survive into the caller -
   exactly the path the CI macOS job takes (`submodules: false`)
verified  scripts/ci/check-apple-resolver-posix.py - 23 checks, 23 passed
```

Bug 2 is the most serious: `v1` returning the branch tip is the precise failure mode the
source-authority change exists to remove.

---

## 2. Structural changes

| change | why | verified |
|---|---|---|
| Apple source authority moved from the `clients/apple` gitlink to the Apple client repository | the gitlink made every client commit a two-repository pin | resolver executed: 23 checks |
| `resolve-apple-source` job — one resolution per **workflow run** | a per-workspace freeze cannot keep separate runners in step | resolver job **success** in a real run (#80) |
| three non-test gitlink gates made non-fatal by default | each would have failed the first build | `APPLE_CLIENT_REQUIRE_GITLINK=1` restores them |
| BUILD-INFO `apple_sha_source` corrected | it said "parent clients/apple gitlink", which had become **false** | source |
| iOS TestFlight DerivedData: one fresh directory per run, shared by resolve and archive | the resolve output was deleted and redone inline | source only — **the A/B needs a Mac and is NOT RUN** |

---

## 3. Things deliberately NOT changed

```text
release allowlist                     NOT widened - it caught the android extra files, which is its job
unexpected-file pruning               NOT made generic - would hide the failure it exists to detect
release tooling ref (P0)              NOT pinned yet - needs its own dry run
release self-test dedup               NOT done - depends on the tooling ref being pinned first
check-apple-source-selection.sh       NOT rewritten - ~22 gitlink assertions; outstanding
the wireguard census family           NOT touched - root cause narrowed, needs the macOS diagnostic
MASQUE / Naive / protocol code        UNTOUCHED
```

---

## 4. The change that would have been wrong

For each defect above, the tempting shortcut and why it was refused:

```text
host-graph gate      add `|| true` or drop the check      -> would stop detecting a missing package
SIGPIPE gate         `set +o pipefail` or widen retries    -> would hide real producer failures
android extras       widen the allowlist                   -> would admit a future unexpected file
trafficsched         move it to deep_checks                -> would stop testing liveness on the fast path
wireguard census     loosen the assertions                 -> would either block correct code or admit a
                                                             broken endpoint, with no evidence which
```

Each of those converts a loud, diagnosable failure into a quiet one, which is the trade this whole body
of work exists to refuse.
