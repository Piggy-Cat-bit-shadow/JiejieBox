# v0.1.15 status: install fixed, uninstall now leaves the layout key behind

```text
CANDIDATE_VERSION     = 0.1.15 (tag re-pointed three times; never published)
FINAL_RELEASE_SHA     = none
INSTALLER_GATE6       = FAIL
HANG_OWNER            = FIXED for install; uninstall leaves HKLM\SOFTWARE\Jiejiebox\jiejiebox behind
PRODUCT_FIX           = MessageBox /SD IDOK  (works: install + reinstall now exit 0)
RELEASE_ARTIFACTS     = built, audits pass, not released
DRY_RUN               = not run
RELEASE_PUBLISHED     = no
PUBLIC_READBACK       = n/a
TESTING_HEAD          = 32c210b (unchanged)
RELEASE               = HELD
```

## What is fixed and proven

The original hang is **gone**. On `v0.1.15` (producer run 38855633279, audit run 38856290439):

```text
> install    /S   exit code 0        <- was: never exited
> reinstall  /S   exit code 0
> first uninstall exit code 0
```

The fix is the measured one: the `dataMigrationCleanupFailed` MessageBox sat on the automatic
silent path with no default button, so under `/S` nothing could dismiss it. Adding a default,
**after the message text** (the placement this file already uses twice), removes the block.

The producer is green: installer built, architecture audit pass, payload audit pass.

## What now blocks, precisely

The first uninstall exits 0, then the harness waits for the machine to settle and times out:

```text
timed out after 300 s waiting for: after first uninstall uninstall to settle
installer lifecycle smoke test failed: the clean reinstall exited 2
```

`Assert-Uninstalled` waits for the daemon service **and** the layout registry key to disappear.
Measured state afterwards:

```text
service command line: (empty)      <- the service IS correctly removed
uninstall matches:    0            <- the uninstall entry IS correctly removed
layout version:       2            <- but the layout key SURVIVES
installation id:      {046B283B-...}
HKLM:\SOFTWARE\Jiejiebox\jiejiebox  <- still present
```

So the surviving state is exactly one thing: **`HKLM\SOFTWARE\Jiejiebox\jiejiebox` is not
deleted by the uninstaller.**

The deletion exists and is reached in the source — `!insertmacro customUnInstall`, line 2148:

```nsis
!insertmacro setInstallationLayoutRegistryView
DeleteRegKey HKLM "${INSTALLATION_LAYOUT_REGISTRY_KEY}"
!insertmacro restoreInstallerRegistryView
```

`customUnInstall` did run (its ENTER marker is present in earlier logs and the uninstaller
returned 0), and the key is still there afterwards. The leading suspects, in the order I would
check them, are:

1. **Registry view mismatch.** `setInstallationLayoutRegistryView` selects a view before the
   delete. The installer writes the key under one view; if the uninstall pass selects the other,
   `DeleteRegKey` misses and still returns success. This fits every observation — exit 0, key
   intact, uninstall entry (written by a different mechanism) correctly removed.
2. **The undecorated `${INSTALLATION_LAYOUT_REGISTRY_KEY}` may not carry the `SOFTWARE\...`
   path in the uninstall pass**, so the delete targets a different key.
3. A `DeleteRegKey` that fails silently because the key has a subkey still open.

I did not get to test these, and I am not going to guess at which it is: the check is one
marker plus one run, and a wrong guess costs a 15-minute build.

## The next run should

1. Add one marker immediately after the `DeleteRegKey` line reporting `$0`-independent evidence
   that the key is gone — e.g. a `ReadRegStr` into a register followed by
   `ciDiagnostic "uninstallLayoutKey" "after delete: <value>"`, and one *before* it reporting
   what is read back. That distinguishes "did not target the right key/view" from "targeted it
   and failed".
2. If it is a view mismatch, the smallest correct fix is for the uninstall to delete in **both**
   views, or to use the same view selection the install used — whichever the file's own
   convention supports.
3. Re-run producer + gate 6 on the **same** `v0.1.15` tag. Do not spend a new version number on
   this: the number has never been published and the tag is already re-pointed, so keep moving it
   until gate 6 is green, then release.

## Cost of my mistakes this round, recorded so they are not repeated

Three of the four `v0.1.15` attempts failed on **my** errors, not the product's:

* `|/SD IDOK` inside the flag list → makensis emitted a **broken installer** that 7-Zip could
  only partly extract (`no app-*.7z payload`). Correct form is `/SD` after the message.
* Markers in `customUnWelcomePage` / `customUnInstallCheck` → `StrCmp not valid outside Section
  or Function`. `ciDiagnostic` expands to `${if}`, so it is only legal inside a Section or
  Function; those two are top-level MUI page macros.
* `"ENTER isUpdated=${isUpdated}"` → `requires 2 parameter(s), passed 4`. `${isUpdated}` expands
  to a comparison, so it splits the argument. Other markers interpolate `${ACTION}`, `${OPERATION}`
  and `${1}`, which are single tokens, and those are fine.

Each is now guarded by a check rather than by memory, and the guards live in
`C:\Deepseek\_p12_scratch\` (`mark4.ps1`, `strip_illegal.ps1`, `repoint_0115.ps1`).

## State

```text
v0.1.11  c781d96c635...  held failed candidate, tag untouched
v0.1.12  3f4e5b559ac...  failed candidate (the instrumented build that located the hang)
v0.1.13  3066876e6af...  failed candidate (my broken /SD placement)
v0.1.14  41529f7c6db...  install+reinstall fixed, uninstall unreachable
v0.1.15  dc3aeea74d0...  current: install+reinstall pass, uninstall leaves the layout key
desktop  dee9cd23535fdebcecbf68bf99235b4884f3baab on diag/installer-lifecycle-markers
nothing published
```
