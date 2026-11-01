# v0.1.15 handoff: install path fixed, gate 6 blocked on one uninstall settle

```text
CANDIDATE_VERSION  = 0.1.15   (tag re-pointed; NEVER published; no new number spent)
FINAL_RELEASE_SHA  = none
INSTALLER_GATE6    = FAIL
HANG_OWNER         = install-side blocker FIXED; remaining failure is on the FIRST UNINSTALL
PRODUCT_FIX        = MessageBox default button on the silent path (works - see below)
RELEASE_ARTIFACTS  = built, architecture + payload audits pass
DRY_RUN            = not run          RELEASE_PUBLISHED = no
PUBLIC_READBACK    = n/a              TESTING_HEAD      = c32c210b (unchanged)
RELEASE            = HELD
```

Branch: `integration/release-next` = `37d3519a7a4db8bfaf951317f101092eb3ea31ae`, tagged `v0.1.15`
(force-updated; the tag has never been published and no release exists for it).
Desktop: `dee9cd23535fdebcecbf68bf99235b4884f3baab` on `diag/installer-lifecycle-markers`.

## One product fix is proven

The install-side hang is **gone**. Latest run (audit 38857749610, producer 38857103254):

```text
> install    /S   exit code 0        <- was: never exited, 900 s timeout
> reinstall  /S   exit code 0
```

The fix: the `dataMigrationCleanupFailed` MessageBox sat on the automatic silent path with no
default button, so under `/S` nothing could dismiss it. Adding a default **after the message
text** (the placement `installer.nsh` already uses twice) removed the block. Measured by the
lifecycle markers: the last marker was `[dataTransition] EXIT op=Commit` with
`descendants: NONE`, i.e. the install body completed and then blocked on a modal dialog.

## What remains, and the two facts that pin it

```text
> first uninstall        exit code 0
  timed out after 300 s waiting for: after first uninstall uninstall to settle
> clean reinstall        exit code 2
  installer lifecycle smoke test failed: the clean reinstall exited 2
```

The state dump taken moments after the timeout shows:

```text
service command line: (empty)     <- the service IS gone
derived install root: (empty)
layout version:       2           <- the layout key is retained
uninstall matches:    0
```

and `[3/5]` calls `Assert-Uninstalled ... -DataPreserved`, so the retained layout key is
**expected** and no longer part of the wait — I changed that condition, and the run above
executed the change (the committed harness at the audited SHA has it at line 473, and the
failure cites line 644, matching that revision).

So the only remaining unsatisfied condition is the **service still being present inside the
300 s window**, even though it is gone by the time the dump is taken (~9 s later). The likely
cause is a service that lingers before it fully disappears — `sc.exe` reported 1060
("does not exist") right after, so it does go away, just not fast enough, or
`Get-DaemonServiceCommandLine` sees a stale/`StopPending` entry.

## Next step (one run, no new version number)

Do **not** bump the version. Add to the settle condition's failure path a bounded report of the
service state at timeout — `sc.exe query` output and `Get-Service` status — so the run says
whether it was absent, `StopPending`, or marked for deletion. Then either:

* the service is genuinely slow to deregister → the wait or the uninstall's service-removal
  sequencing is the thing to fix, in the harness or in `executeDaemonServiceCommand
  "uninstall"`; or
* the harness is sampling the wrong service/view → fix the harness.

Both are one-line-ish once the state is visible. Then re-point `v0.1.15` and re-run producer +
gate 6.

## Cost of my mistakes this round, so they are not repeated

Three of four `v0.1.15` attempts died on **my** errors, not the product's, and each cost a
~15-minute build:

1. `|/SD IDOK` inside the flag list → makensis emitted a **structurally broken installer**;
   7-Zip extracted only part of it and the payload audit failed. Correct form is `/SD` **after**
   the message text.
2. Markers in `customUnWelcomePage` / `customUnInstallCheck` → `StrCmp not valid outside Section
   or Function`. `ciDiagnostic` expands to `${if}`, so it is legal only inside a Section or
   Function; those two are top-level MUI page macros.
3. `"ENTER isUpdated=${isUpdated}"` → `requires 2 parameter(s), passed 4`. `${isUpdated}`
   expands to a comparison and splits the argument. Markers interpolating `${ACTION}`,
   `${OPERATION}` or `${1}` are fine — those are single tokens.

Guards for all three now exist as scripts rather than as memory, in `<WORKSPACE_ROOT>\_p12_scratch\`
(`mark4.ps1`, `strip_illegal.ps1`, `repoint_0115.ps1`).

## Version-number ledger (nothing published)

```text
v0.1.11  c781d96c635...  HELD failed candidate, tag untouched, must stay
v0.1.12  3f4e5b559ac...  failed; the instrumented build that located the install hang
v0.1.13  3066876e6af...  failed; my broken /SD placement
v0.1.14  41529f7c6db...  failed; install+reinstall fixed, uninstall never reached
v0.1.15  37d3519a7a4...  CURRENT; install+reinstall green, first-uninstall settle open
```

If a further failed iteration is needed, keep re-pointing `v0.1.15`. Spend `0.1.16` only on a
build that is actually a release candidate.
