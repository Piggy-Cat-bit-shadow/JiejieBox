# Phase 5 continue — gate 6 diagnosis: PRODUCT_REGRESSION, isolated by control experiment

```text
ROOT_CAUSE = NOT yet pinned to a line. The installer reaches a fully installed state and then
             does not exit; the behaviour is introduced by the a917e616 desktop work
             (installer.nsh +240/-12 plus installer-stop-service.ps1,
             installer-remove-legacy-service.ps1, installer-legacy-service-ownership.ps1),
             and is NOT caused by the audit tooling, the runner, or a preceding gate.

CLASSIFICATION = PRODUCT_REGRESSION
                 (installer lifecycle/exit), proven by the control below.

FIRST_BAD_COMMIT/PATH = narrowed, not pinned. Any of the six desktop commits between the
             known-good 917d2a4e and the v0.1.11 pin a917e616 could be it; the
             install-lifecycle ones are 1603c4e48, 78b25f46f, 2d4dd0321, a917e6161.
             Pinning it needs installer builds at intermediate desktop SHAs.

FIX_COMMIT = none yet
DESKTOP_SHA = a917e616190050b845061cc3798ff7cf49d4c3a1 (unchanged)
NEW_CORE_SHA = none. c781d96c635b12962a175e7f6f5a334adab27f11 is still the frozen release SHA
             and is now known-bad for release.
VERSION/TAG_DECISION = v0.1.11 stays the HELD candidate identity; do NOT move it. Release the
             fixed code as v0.1.12 (no project rule found that says otherwise), per §7 of the
             brief.

GATE6_ALONE        = FAILURE   run 38841527963   (fresh runner, gates 5/5b skipped)
GATE6_AFTER_5B     = FAILURE   runs 38837786208, 38839399129  (full audit)
CONTROL_v0.1.10    = SUCCESS   run 38842997048   (SAME isolation, same runner image, same gate)
FRESH_INSTALL      = installs correctly, then the installer process does not exit (900s timeout)
REINSTALL          = never reached
UNINSTALL          = never reached
CLEAN_REINSTALL    = never reached

ALL_EXACT_SHA_GATES = not re-run (no new core SHA yet)
DRY_RUN             = PASS (unchanged, on the now-held SHA)
RELEASE             = HELD
```

## The decisive experiment

The brief's first question was whether the installer hangs *by itself* or only after gates 5/5b.
That is now measured, not inferred, because the workflow gained a `gates: all|gate6` input
(`all` by default) that skips gates 5 and 5b:

```text
v0.1.11 installer, gate 6 ALONE, fresh runner   -> FAILURE (900 s silent-install timeout)
v0.1.10 installer, gate 6 ALONE, SAME flag      -> SUCCESS
```

Same runner image, same workflow revision, same gate, same arguments. **The only variable is the
artifact.** So:

* hypothesis A (gate 5b leaves an orphan/handle) is **falsified** — gates 5/5b did not even run;
* `PRECEDING_GATE_CONTAMINATION` is excluded;
* `RUNNER_ENVIRONMENT` is excluded by the control;
* the gate itself is not obviously wrong, because it passes on the previous release's installer.

This is a genuine **product regression**, and it is release-blocking in exactly the way Phase 5
treated it: the installer must exit, not merely finish installing.

## What was added to make this measurable

Committed as `7ca90ea61ff` (diagnostics only; no behaviour change, `gates: all` unchanged):

1. **`Show-ProcessSnapshot`**, captured **before** the kill, written to its own record file:
   installer PID/image/path/start/CPU, `Responding`, window title and handle, thread and handle
   counts; the full **descendant tree** via `Win32_Process` parent links with command lines;
   `sing-box-daemon` / `electron` / `powershell` / `nsExec` / `Uninstall` matches whether or not
   they are descendants; visible top-level windows. Read-only, so it is safe on a failing path.
2. **The timeout message no longer asserts a cause.** It said *"in silent mode that means a prompt
   appeared that /S could not answer"*, which is a symptom read as a diagnosis — and it is now
   falsified, because the installer is not sitting on an NSIS page. It now lists the candidates the
   snapshot distinguishes.
3. **`gates: all|gate6`** — the isolation input.

It was exercised against **real** processes before shipping (live process with a live child → walked
3 descendants; null → handled; exited → handled), because a diagnostic that throws on the path it
exists to diagnose is worse than none.

**Note on the `tooling_ref` override:** the workflow's pin step enforces
`AUDITED_BUILD_SHA == AUDIT_TOOLING_SHA` and refuses otherwise, so new diagnostics **cannot** be
run against the held artifact. That guard is sound (it is the anti-mixing rule) but it means every
diagnostic change needs either a re-tagged artifact or a relaxation. The gate-6-alone runs therefore
used the pinned old tooling, which is why their failure text is the old wording.

## Where the regression lives (narrowed)

The desktop pin moved `917d2a4e` → `a917e616`, a six-commit fast-forward. Changed files in the
installer/runtime surface:

```text
build/installer.nsh                                  +240 -12    <- the main artefact
build/installer-stop-service.ps1                     +78  (new)
build/installer-remove-legacy-service.ps1            +107 (new)
build/installer-legacy-service-ownership.ps1         +150 (new)
build/installer-preflight.ps1                        +23 -4
build/installer-data.ps1                             +13 -2
build/check-installer.nsh                            +172 (new, static compile check)
scripts/test-install-directory.ps1                   +131 (new)
scripts/test-legacy-service-ownership.ps1            +140 (new)
src/main/identity.ts, installationLayout.ts, daemon.ts, index.ts, applicationPaths.ts
```

Ordered by how well they match the symptom (install completes, process does not exit):

1. **`customInit`/`customInstall` tail work** — the service-install `nsExec::Exec` returns 0 (the
   diagnostic channel proves it), the layout registry is written, so the hang is after the service
   step and inside or after `executeDataTransition "Commit"` / shortcut refresh.
2. **`installer-data.ps1 -Operation Commit`** — 559 lines with no explicit wait found, but it is the
   one remaining unbounded step between service install and exit.
3. **`chooseForkInstallationDirectory` + `$daemonDataDirectory`** — `customInit` now spells the
   daemon state directory as `${COMMON_APPLICATION_DATA}\...`. The runtime diagnostic prints it
   UNRESOLVED (`C:\Users\runneradmin\..\..\ProgramData\jiejiebox-daemon`). Semantically the same
   path, but it is exactly the kind of spelling that breaks a string/ownership comparison.
4. **`installer-stop-service.ps1` / `installer-remove-legacy-service.ps1`** — no unbounded wait found
   in either, so they are lower priority than their names suggest.

`installer-service.ps1` does contain `$process.WaitForExit()` with **no timeout** (line 50), but the
diagnostic channel shows that call returned (`[service install] exit=0`), so it is not the hang —
worth hardening anyway, since it is the one place an installer step waits unbounded on the daemon.

## What would pin it

Build the installer at intermediate desktop SHAs and run **gate 6 alone** against each. Each
installer build is ~20 minutes, so a bisect over six commits is two or three runs:

```text
917d2a4e (control: PASSES) -> 2d4dd0321 -> 78b25f46f -> 1603c4e48 -> a917e616 (FAILS)
```

Because the desktop commits are installer-shaped, the first bad one should be decisive. Note the
audit's `tooling_ref` guard must be satisfied (full 40-hex, and equal to `core_sha`) or the
diagnostics cannot run.

## Not to be redone

Everything Phase 5 proved on `c781d96c635` stands as a record and must not be re-litigated — the
11/11 exact-SHA gates, the desktop producer at 34/34 with both identity gates now passing, the
dry-run PASS with the canonical 7-asset contract, and the first-ever exercising of the versioned
Apple bundle. **All of it is invalidated as RELEASE evidence by this regression**, because it is
evidence for a SHA whose product does not pass its own runtime gate. It is not invalidated as
history.
