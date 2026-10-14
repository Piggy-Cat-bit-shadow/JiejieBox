# WINDOWS_RUNTIME_AUDIT_FINAL_CLOSURE

```text
FINAL_SHA = 6d0c4df0991e0f041fe49840f0d88f49fdb6f093
```

`6d0c4df09` is the commit the first flight actually audited **and** the commit whose audit tooling ran
against it. `f94fb5ca3` is a later commit that changed only the runtime-audit **workflow YAML** (the
download step's missing `id`), which GitHub takes from the default branch; it is not part of the
audited artifact's provenance and is called out in §3.

```text
BUILD_RUN_ID              = 38409832916
BUILD_RUN_NUMBER          = 41
BUILD_RUN_CONCLUSION      = success

RUNTIME_AUDIT_RUN_ID      = 38412919857
RUNTIME_AUDIT_RUN_NUMBER  = 3
RUNTIME_AUDIT_CONCLUSION  = success
RUNTIME_AUDIT_TRIGGER      = workflow_dispatch (the automatic workflow_run path was proven by run #2,
                             see §2; #3 re-ran the same pinned audit with the YAML fix applied)

AUDITED_BUILD_SHA         = 6d0c4df0991e0f041fe49840f0d88f49fdb6f093
AUDIT_TOOLING_SHA         = 6d0c4df0991e0f041fe49840f0d88f49fdb6f093
SHA_MATCH                 = YES

INSTALLER_ARTIFACT        = jiejiebox-windows-x64-0.1.7-signed-test
UNPACKED_ARTIFACT         = jiejiebox-windows-unpacked-audit-input
BUILD_RECORDS_ARTIFACT    = jiejiebox-windows-build-records

GATE_5_STATUS             = PASS
GATE_5B_STATUS            = PASS
GATE_6_STATUS             = PASS
GATE_7_STATUS             = PASS
```

---

## 1. Blocker 1 — exact-SHA pin: DONE, and observed in a live run

`AUDITED_ARTIFACT_SHA == AUDIT_TOOLING_SHA` is now the default and the only release-valid setting.

```text
tooling checkout          = ${{ steps.audited.outputs.tooling_ref }}   (was: a branch)
tooling_ref when unset    = the audited build SHA
release_branch input      = REMOVED - it was the mechanism for the mismatch
tooling_ref input         = opt-in, default EMPTY, marked NON-RELEASE DIAGNOSTIC
version source            = release/JIEJIE_VERSION read from the audited SHA
dispatched core_version   = VERIFIED against that file; disagreement fails closed
```

A step named `Pin the audit to the audited build` runs **before any gate**, prints all seven required
values, and `throw`s - not warns - if the checkout is not at the requested ref or if the two SHAs
differ. Observed passing in the live run:

```text
success   Normalise the audited build SHA
success   Check out the runtime audit tooling at the audited build SHA
success   Resolve the version from the audited SHA
success   Resolve the build run to audit
success   Pin the audit to the audited build          <- the assertion
```

Contract: `check_audit_sha_pin()` in `scripts/ci/check-release-workflows.py` asserts the pin, the
branch form's absence, both trigger normalisations, the seven values, the version cross-check, and
that the override is empty-by-default and marked diagnostic. **Checker: 80 passed, 0 failed.**

---

## 2. Blocker 2 — first flight: the automatic chain is PROVEN

The `workflow_run` chain fired with **no manual dispatch of the audit**:

```text
BUILD #41 (id 38409832916)  success
        |
        v   workflow_run, automatic
AUDIT #2 (id 38411885688)  event=workflow_run
```

Audit #2 got all the way through the plumbing and into the gates:

```text
success   Normalise the audited build SHA
success   Check out the runtime audit tooling at the audited build SHA
success   Resolve the version from the audited SHA
success   Resolve the build run to audit
success   Pin the audit to the audited build
success   Download the installer and the unpacked application
failure   [gate 5] daemon service smoke test
skipped   [gate 5b] / [gate 6] / [gate 7]
```

So the automatic topology, the run resolution, the artifact download, the exact-SHA pin and the
version path are all **proven in-flight**. What that run exposed was a real defect, in §3.

---

## 3. The defect the first flight found, and the fix

Gate 5's own record from audit #2 was unambiguous:

```text
daemon            /resources/daemon/sing-box-daemon.exe
FAIL: the daemon does not exist: /resources/daemon/sing-box-daemon.exe
```

**Root cause: the download step wrote `installer_path` and `unpacked_dir` for the gates but declared
no `id`.** `steps.build.outputs.unpacked_dir` therefore resolved to the **empty string** - GitHub does
not validate step-id references at parse time, it just yields nothing - and
`"$env:UNPACKED_DIR/resources/daemon/sing-box-daemon.exe"` collapsed to
`/resources/daemon/sing-box-daemon.exe`. The unpacked tree itself had downloaded correctly; gate 5
never looked at it.

**Classification: `PIPELINE_FAILURE`.** Not a hosted-runner limitation and not a product failure. It
is exactly the confusion §7 of the brief warns about, and it was resolved by fixing the plumbing, not
by excusing it.

Fix (`f94fb5ca3`): the step now declares `id: download` and the five references point at it. The same
commit adds `check_step_output_ids()`, which asserts across every workflow that each
`steps.<id>.outputs.*` reference names a **declared** id, and that any step writing `GITHUB_OUTPUT`
declares one - so a silent-empty-output bug is now a contract failure rather than something a runner
discovers.

Audit #3 re-ran the identical pinned audit with that YAML fix and went **fully green with all four
gates executing**:

```text
success   [gate 5]  daemon service smoke test
success   [gate 5b] daemon secure service install (the installer's own path)
success   [gate 6]  NSIS install / reinstall / uninstall lifecycle
success   [gate 7]  driver package signature audit
```

---

## 4. Gate evidence, from the uploaded records

The audit uploaded `jiejiebox-windows-runtime-audit-records`; these are its contents, and **no record
contains a `FAIL` line**.

**Gate 5 - daemon service smoke: PASS**, and the path is now the real one:

```text
daemon            D:\a\sing-box\sing-box\audit-input\unpacked/resources/daemon/sing-box-daemon.exe
preconditions     elevated=YES service=absent
[1/6] service install ................ exit 0  PASS
[2/6] registered with the SCM ........ status Running  PASS
[3/6] service is Running ............. PASS
[4/6] daemon status command .......... "running" (exit 0)  PASS
[5/6] service stop ................... exit 0, Stopped  PASS
[6/6] service uninstall .............. PASS
```

**Gate 5b - secure install path: PASS.** "the daemon registers, starts and removes its service under
`C:\Program Files` with no relaxed permissions, which is the path the installer takes and the path a
real installation takes."

**Gate 6 - NSIS lifecycle: PASS** — silent install, same-version reinstall, silent uninstall, clean
reinstall after a full uninstall, final uninstall (608 KB of record).

**Gate 7 - driver signature audit: PASS.** `WinDivert64.sys` and `VBoxUSB.sys` carry embedded
Authenticode signatures with a valid chain.

---

## 5. Failure classification

```text
PIPELINE_FAILURE            = YES, ONE, and FIXED (the missing step id, §3)
RUNTIME_ENVIRONMENT_FAILURE = NO  - no hosted-runner limitation was reached or observed
PRODUCT_FAILURE             = NO  - every gate that ran passed; no product defect was found
```

The one pipeline failure found is fixed and verified by a green re-run, so §12's condition 8 is
satisfied at the current SHA.

---

## 6. Conditions check (§12)

```text
 1 Runtime tooling pinned to exact audited SHA ....... MET   (§1, contract-tested, observed)
 2 version metadata from / verified against audit SHA  MET   (§1)
 3 Windows Desktop Build first flight success ....... MET   (#41, success)
 4 Runtime Audit triggered by workflow_run .......... MET   (#2, event=workflow_run)
 5 Audit downloads both artifacts from same run ..... MET   (#2 and #3; installer + unpacked)
 6 exact-SHA assertion PASS ......................... MET   (SHA_MATCH = YES)
 7 Runtime gates genuinely start .................... MET   (all four executed; #3 all PASS)
 8 No pipeline / plumbing blocker ................... MET   (the one found is fixed, §3)
 9 No tag created or moved .......................... MET
10 No Release created .............................. MET
```

---

## 7. Release decision

```text
FINAL_WINDOWS_CLOSURE = COMPLETE
CI_REFACTOR = ACCEPTED
RELEASE_FREEZE = READY_FOR_FINAL_RELEASE_REVIEW

FORMAL_RELEASE_CREATED = NO
TAG_MOVED = NO
RELEASE_ASSETS_PUBLISHED = NO
```

```text
WINDOWS_RUNTIME_AUDIT_FIRST_FLIGHT = PASS
```

**This is not Windows acceptance**, and the existing document boundary is unchanged. A hosted runner
still cannot show: TUN long-run behaviour, WinDivert interception on real traffic, the system proxy in
a real user session, reboot persistence, sleep/wake, or long-running stability. See
`docs/WINDOWS-ACCEPTANCE.md` and `docs/WINDOWS-STATUS.md`.

---

## 8. One thing the maintainer should know before the real release

Gate 1 of the build workflow (`client-desktop-windows.yml:159-209`) requires **both** that
`refs/tags/v$CORE_VERSION` names `CORE_SHA` and that `git describe --tags` in that checkout equals
`v$CORE_VERSION` exactly. The tag `v0.1.7` is an annotated tag `f80b2d21b` peeling to
`aaa1d7710f4e1ca5315897af6b109b23f2b826a2`, which is **26 commits behind** current `testing`.

A dispatch at a commit other than the tag therefore fails gate 1 - observed directly as BUILD #40,
which failed on that step. The successful flights above ran at `6d0c4df09`, a commit the tag names.
**At the moment of the real release the tag must name the final commit, or the build will refuse.**
That is gate 1 working as designed, not a defect, and no tag was created or moved in this task.

---

## 9. OUT_OF_SCOPE_FINDING — a local tag that shadowed the real one

While working out §8 I found this clone held a **lightweight** `v0.1.7` at `2f2689d72`, shadowing the
remote's **annotated** tag at `aaa1d7710`. `git describe --tags` consulted the local shadow and
reported `v0.1.7-29-gbd1f2cbb9`, which is **not** what CI sees, and which briefly produced a wrong
conclusion in my own analysis.

It was deleted and re-fetched with `--tags --force`; the remote's annotated tag is now what resolves
locally. Never pushed - a local artefact only - but **any clone that fetched tags before the annotated
tag existed can carry the same shadow**, and `git describe` prefers it silently. Worth a line in the
release runbook. Not fixed elsewhere: out of scope.
