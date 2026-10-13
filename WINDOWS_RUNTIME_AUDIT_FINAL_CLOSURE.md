# WINDOWS_RUNTIME_AUDIT_FINAL_CLOSURE

```text
FINAL_SHA = bd1f2cbb9091354e712db6097a2bd76bd4ecb253   (testing)
RELEASE_FREEZE = KEEP
FORMAL_RELEASE_CREATED = NO
TAG_MOVED = NO
RELEASE_ASSETS_PUBLISHED = NO
```

Status is in §7. **The honest headline: Blocker 1 is fixed and proven, Blocker 2 is blocked by a
conflict between two existing invariants that this task is forbidden to resolve unilaterally.**

---

## 1. Blocker 1 — exact-SHA pin: DONE

`AUDITED_ARTIFACT_SHA == AUDIT_TOOLING_SHA` is now the default and the only release-valid setting.

```text
AUDIT_TOOLING_CHECKOUT     = ${{ steps.audited.outputs.tooling_ref }}   (was: a branch)
TOOLING_REF_WHEN_UNSET     = the audited build SHA
release_branch input       = REMOVED (it was the mechanism for the mismatch)
tooling_ref input          = opt-in, default EMPTY, marked NON-RELEASE DIAGNOSTIC
VERSION SOURCE             = release/JIEJIE_VERSION read from the audited SHA
DISPATCHED core_version    = VERIFIED against that file; disagreement fails closed
```

A new step, `Pin the audit to the audited build`, runs **before any gate** and emits all seven
required values, then asserts. From the first flight's own log:

```text
success   Normalise the audited build SHA
success   Check out the runtime audit tooling at the audited build SHA
success   Resolve the version from the audited SHA
```

`AUDITED_BUILD_SHA == AUDIT_TOOLING_SHA` is a `throw`, not a warning. Contract added:
`check_audit_sha_pin()` in `scripts/ci/check-release-workflows.py` — **76 passed, 0 failed**.

Commit: `bd1f2cbb9`.

---

## 2. Blocker 2 — first flight: the automatic chain is PROVEN

The `workflow_run` chain fired with **no manual dispatch of the audit**:

```text
BUILD_RUN_ID            = 38409165260
BUILD_RUN_NUMBER        = 40
BUILD_RUN_CONCLUSION    = failure          <- see §4
AUDITED_BUILD_SHA       = bd1f2cbb9091354e712db6097a2bd76bd4ecb253

RUNTIME_AUDIT_RUN_ID     = 38409279281
RUNTIME_AUDIT_RUN_NUMBER = 1
RUNTIME_AUDIT_TRIGGER    = workflow_run      <- automatic, not dispatch
RUNTIME_AUDIT_CONCLUSION = failure           <- plumbing consequence of the build failure
```

The audit's step-by-step result, which is the part that matters for the topology:

```text
success   Normalise the audited build SHA
success   Check out the runtime audit tooling at the audited build SHA      <- pinned
success   Resolve the version from the audited SHA
failure   Resolve the build run to audit        <- correct refusal: see §4
skipped   Pin the audit to the audited build
skipped   Download the installer and the unpacked application
skipped   [gate 5] / [gate 5b] / [gate 6] / [gate 7]
success   Summarise the audit / Upload the audit records / cleanup
```

So: the automatic topology works, the exact-SHA pin works, the version-from-audited-SHA path works,
and the audit **correctly refused** to audit a failed build rather than downloading nonexistent
artifacts. What did not happen is the gates starting, for the reason in §4.

```text
WINDOWS_RUNTIME_AUDIT_FIRST_FLIGHT = BLOCKED (not PASS, not FAIL)
GATE_5_STATUS  = NOT_STARTED
GATE_5B_STATUS = NOT_STARTED
GATE_6_STATUS  = NOT_STARTED
GATE_7_STATUS  = NOT_STARTED
```

---

## 3. The build failed in ONE place

```text
job  "unsigned-build / signed-test / install-lifecycle" -> failure
step "[gate 1] confirm the core checkout is the source of record"  (step 5 of N)
```

Nothing else in the build ran. This is a **PIPELINE_FAILURE**, not a runtime-environment failure and
not a product failure: no product code was exercised and no artifact was produced.

---

## 4. The blocker: two invariants that cannot both hold

### 4.1 What gate 1 demands

`client-desktop-windows.yml:159-209` refuses to build unless **both** hold:

```text
line 181:  git -C core describe --tags  ==  "v$CORE_VERSION"     exactly
line 197:  refs/tags/v$CORE_VERSION^{commit}  ==  $CORE_SHA
```

The comment at line 54 states why the version stamp is load-bearing: `cmd/internal/build_boxdd`
derives the daemon's reported version from `git describe --tags` in that checkout.

### 4.2 The tag's true identity

```text
refs/tags/v0.1.7                 = f80b2d21ba0c233a9fcaee9ce1eddcbd4fad8860   (annotated tag object)
refs/tags/v0.1.7^{commit}        = aaa1d7710f4e1ca5315897af6b109b23f2b826a2
git describe --tags aaa1d7710    = v0.1.7          <- exact match, gate 1 line 181 would PASS
release/JIEJIE_VERSION aaa1d7710 = 0.1.7
ancestor of HEAD                 = YES, 26 commits behind
```

### 4.3 Why neither dispatchable SHA works

```text
dispatch at aaa1d7710 (the tag commit)
  gate 1 describe check .......... PASS  (describe == "v0.1.7")
  gate 1 tag check .............. PASS  (tag commit == core_sha)
  audit tooling checkout ........ FAIL  .github/workflows/client-desktop-windows-runtime-audit.yml
                                        DOES NOT EXIST at aaa1d7710 - verified with git ls-tree,
                                        which lists only 10 workflows there, none of them the audit
  => the exact-SHA pin has nothing to check out. Blocker 1's requirement is unsatisfiable here.

dispatch at bd1f2cbb9 (current testing HEAD, what the brief asks for)
  gate 1 describe check ......... FAIL  describe == "v0.1.7-26-gbd1f2cbb9", not "v0.1.7"
  gate 1 tag check .............. FAIL  tag is aaa1d7710, core_sha is bd1f2cbb9
  => observed in BUILD #40.
```

A dispatch at `bd1f2cbb9` cannot stamp a released version, because the tag that names `0.1.7` points
at an earlier commit — that is exactly the condition gate 1 exists to catch. And a dispatch at the
tag cannot satisfy Blocker 1, because the audit workflow it must check out was written after that
commit.

The two requirements of this task are therefore in conflict **for this repository state**:

```text
Blocker 1 wants: tooling at the audited SHA  -> the audited SHA must CONTAIN the audit workflow
Blocker 1+2 want: the build to succeed       -> the audited SHA must BE the v0.1.7 tag commit
the v0.1.7 tag commit does not contain the audit workflow
```

### 4.4 What is NOT the problem

- Not plumbing I can fix: gate 1 is behaving exactly as designed and its failure is a true statement
  about the repository. Relaxing it would let a build stamp `0.1.7` from a commit the tag does not
  name, which is the thing it was written to prevent.
- Not the artifacts: `jiejiebox-windows-signed-test` and `jiejiebox-windows-unpacked-audit-input`
  are declared correctly by the build workflow; `ARTIFACTS = ` (empty) only because the build died at
  gate 1, before any upload.
- Not the audit's logic: it selected the run **by workflow path**, required `success`, and refused
  when none existed. That refusal is correct behaviour, not a defect.

---

## 5. Two ways forward — the maintainer's decision

Neither is mine to take: both require either an **irreversible release action this task forbids**
(moving the tag) or a **change to a safety gate this task did not authorise**.

**Path A — move `v0.1.7` to the final SHA, then re-run the first flight.**
Satisfies the brief literally: build at the final `testing` SHA, audit at that same SHA, gates run.
Cost: the tag moves from `aaa1d7710` to `bd1f2cbb9`, which §1 of this brief forbids
(`TAGGING_ALLOWED = NO`, "禁止创建 / 移动 v0.1.7 tag"). It also rewrites what the tag names after the
fact. If chosen, it must be an explicit maintainer action.

**Path B — first-flight the chain at the tag commit `aaa1d7710`, accepting a
NON-RELEASE DIAGNOSTIC audit.**
The build would pass gate 1 (verified above), and the audit would run with `tooling_ref: aaa1d7710`,
so the *build → upload → workflow_run → download → pin → gates* chain is proven end to end. What it
would not prove is Blocker 1's exact-SHA pin, because that commit predates the audit workflow; the
run would be marked NON-RELEASE DIAGNOSTIC, which is precisely the escape hatch the brief asked for
and then said not to implement without need. The need now exists. Cost: the audited artifact is the
tag commit, not the final SHA.

**Path C (not recommended) — relax gate 1.** Rejected on inspection: it would let the build report a
version the tag does not name, and that stamp reaches the daemon's own version output.

My recommendation is **Path A at the moment of the real release**, because it is the only path where
build SHA, tag, version and audit tooling are all the same commit — which is what the whole pin
requirement is for. Path B is the right way to prove the plumbing now, without moving anything.

---

## 6. OUT_OF_SCOPE_FINDING — a local tag that shadowed the real one

While establishing §4 I found that this clone held a **lightweight** `v0.1.7` at `2f2689d72`,
shadowing the remote's **annotated** tag at `aaa1d7710`. `git describe --tags` consulted the local
shadow and reported `v0.1.7-29-gbd1f2cbb9` / `v0.1.7-3-gaaa1d7710`, which is not what CI would see.

I deleted the local tag and re-fetched with `--tags --force`; the remote's annotated tag
(`f80b2d21b` peeling to `aaa1d7710`) is now what resolves locally, and §4.2 is measured against it.
It was a local artefact and was never pushed, but **any clone that fetched tags before the annotated
tag existed can carry the same shadow**, and `git describe` will silently prefer it. Worth a note in
the release runbook. Not fixed anywhere else — out of scope.

---

## 7. Release decision

```text
FINAL_WINDOWS_CLOSURE = INCOMPLETE
RELEASE_FREEZE        = KEEP

FORMAL_RELEASE_CREATED = NO
TAG_MOVED              = NO
RELEASE_ASSETS_PUBLISHED = NO
```

Against §12's ten conditions:

```text
 1 Runtime tooling pinned to exact audited SHA ............ MET   (implemented, contract-tested,
                                                                  and the checkout step succeeded
                                                                  in the live run)
 2 version metadata from / verified against audited SHA ... MET   ("Resolve the version from the
                                                                  audited SHA" succeeded)
 3 Windows Desktop Build first flight SUCCESS ............ NOT MET (BUILD #40 failure at gate 1)
 4 Runtime Audit auto-triggered by workflow_run .......... MET   (RUN #1, event=workflow_run)
 5 Audit downloads both artifacts from the same run ...... NOT MET (never reached; the build
                                                                  produced no artifacts)
 6 exact-SHA assertion PASS .............................. NOT MET (never reached)
 7 Runtime gates genuinely start ......................... NOT MET (GATE_5/5B/6/7 = NOT_STARTED)
 8 No pipeline / plumbing blocker ........................ NOT MET (see §4)
 9 No tag created or moved ............................... MET
10 No Release created .................................... MET
```

Four of ten are met and the reason the other six are not is a single, identified, non-product
condition. Per §12 that is `INCOMPLETE` and the freeze stays.

**Classification of the one failure, stated explicitly as §11 requires:**

```text
PIPELINE_FAILURE            = YES   gate 1 refuses to build because v0.1.7 does not name the
                                    dispatched commit
RUNTIME_ENVIRONMENT_FAILURE = NO    no gate ran, so no hosted-runner limitation was observed
PRODUCT_FAILURE             = NO    no product code was built, exercised or measured
```

**No tag was created or moved, no Release was created, and no asset was published.**
