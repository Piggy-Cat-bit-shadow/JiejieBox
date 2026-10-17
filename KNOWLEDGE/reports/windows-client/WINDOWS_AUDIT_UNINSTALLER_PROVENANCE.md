# Uninstaller provenance capture: implemented, not yet observed; hypothesis untested

```text
WINDOWS_AUDIT = PARTIAL
P0-4          = COMPLETE (closed; not revisited)
CHANNEL       = FIXED (append works; complete marker lines; monotonic growth)
P0-C0         = IMPLEMENTED, RESULT NOT YET OBTAINED
OPEN          = first uninstall contributes 0 bytes
RELEASE_READY = NO
RELEASE       = not published; latest public release remains v0.1.10
v0.1.11       = untouched
```

**I did not obtain the answer to P0-C0.** The capture is implemented and published, but the last
two audit runs failed before the lifecycle record was written, so no hash comparison exists yet.
Reporting the direction and the blocker rather than a conclusion.

## The hypothesis, and why it is worth the priority it was given

`STALE_UNINSTALLER_AFTER_REINSTALL`. If the reinstall leaves an older uninstaller in place and only
the clean install writes a current one, then the first uninstall runs a stale binary - which would
explain, in one mechanism, all four observations that a `GetOptions` quirk cannot explain together:
the first uninstall writing nothing, the final uninstall writing normally, the first uninstall
possibly using an older service identity, and `jiejiebox-daemon` therefore staying RUNNING.

The asymmetry is the argument: identical code and identical argument form do not produce different
behaviour in the first and final uninstalls. A different binary does.

**Nothing in this round confirms or refutes it.** What follows is what was built to decide it.

## What is implemented and published

`Save-UninstallerProvenance` is called at the five points the brief names:

```text
after-install            after-reinstall          before-first-uninstall
after-clean-reinstall    before-final-uninstall
```

Each call copies the installed uninstaller aside - before it can delete itself - and records its
absolute path, sha256, size, LastWriteTimeUtc, PE FileVersion and ProductVersion, Authenticode
status and signer, and the registry entry's UninstallString, InstallLocation and DisplayVersion.
Every hash is collected into a table, and the end of the run states the verdict rather than leaving
a reader to compare numbers:

```text
distinct sha256 values: N
  N > 1 -> DIFFERENT BINARIES ran; parameter handling is not the first suspect, the replacement of
           the installed uninstaller during reinstall or clean install is.
  N == 1 -> the SAME binary ran; a parameter-parsing difference is not excluded and the bootstrap
            probe is justified.
```

Strict per-phase increments are now computed from the live snapshot sizes and printed as four
separate deltas. This corrects a reporting error of mine: the earlier "+713 bytes for the final
uninstall" was the clean reinstall and the final uninstall added together, and the two steps cannot
support that attribution between them. Whether the clean-reinstall snapshot exists was already
answered - it does, `4-clean-reinstall` - so the numbers are now separable and will be stated
individually.

## Two defects I introduced, both found by the runs failing

1. **`Cannot bind argument to parameter 'Path' because it is null.`** `Test-Path -LiteralPath`
   throws on a null or empty path, and the provenance function runs at points where the
   installation root may legitimately not be established yet. The probe became the failure it was
   meant to describe. Both path tests are now guarded and the copy is skipped with a recorded
   reason. Fixed in `1bcbfe46b11`.
2. **The digest could come back null.** `Get-FileHash` failure would put a null into the identity
   table and take the entire comparison down. It is now computed from the bytes in a `try/finally`
   that disposes the hasher, and a failure is recorded as text rather than as null.

## The blocker, stated exactly

Audit runs 38886831682 and 38888122961 both failed and uploaded only
`daemon-service-smoke-test.txt` and `secure-service-install.txt` - **no lifecycle record at all**,
and no per-phase diagnostic snapshots. A run that fails before its first record write uploads
nothing from that gate, so the provenance evidence does not reach the artifact even if the function
works.

Every log also shows, in the cleanup step:

```text
The term 'Get-InstallationState' is not recognized as a name of a cmdlet, function, script file,
or executable program.
```

`Get-InstallationState` **is** defined in the harness (line 74), so this is a second, separate call
site - the job's leftover-cleanup step, not the lifecycle test. It runs with
`$ErrorActionPreference = "Continue"` and therefore does not fail the job by itself, but it has to
be resolved: it fires on every failed run and obscures the real failure in the log.

## The next run must first make the failure legible

Before the provenance question can be answered, a run has to get far enough to write a record. In
order:

1. Fix or remove the `Get-InstallationState` call in the cleanup step, so a failed run shows its
   real cause instead of a misleading secondary error.
2. Add a `trap`/`catch` around the lifecycle body that writes the record and the per-phase snapshots
   **before** propagating - a probe that only reports on success cannot diagnose a failure, which is
   the same lesson as the diagnostic channel, applied one level up.
3. Then re-run and read `distinct sha256 values` plus the four deltas.

The result is a single number and four numbers. If the hashes differ, the entire `GetOptions`
investigation stops and the question becomes which lifecycle step replaces the uninstaller - for
which the electron-builder order (`uninstallOldVersion`, `WriteUninstaller`, the `isUpdated` branch)
is already localised from the earlier generated-template audit.

## State

```text
branch   audit/windows-client-lifecycle @ 1bcbfe46b11aefe347518c942454dca5aa632b43
tag      v0.1.15                        @ 1bcbfe46b11aefe347518c942454dca5aa632b43
worktree clean
v0.1.11  c781d96c635…  untouched
latest public release  v0.1.10
INSTALL_PATH_NESTING   recorded earlier, not pursued
```
