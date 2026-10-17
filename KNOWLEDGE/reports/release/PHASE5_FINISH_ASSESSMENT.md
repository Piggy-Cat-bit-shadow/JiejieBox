# Release finish: the gate-6 comparison is confounded three ways, and the hang is real

```text
RELEASE = HELD          nothing published
VERSION/TAG = 0.1.11 / v0.1.11 -> c781d96c635b12962a175e7f6f5a334adab27f11  (unmoved)
DIAGNOSTIC_PATH = PASS  (the third tooling checkout works; the version stamp now passes)
BISECT = IMPOSSIBLE through the producer/audit path
```

## The finding that matters, and it corrects an earlier phase

The pair I inherited as "LAST_GOOD vs FIRST_BAD" is not a controlled comparison. Between the
run that PASSED gate 6 and the runs that FAIL it, **three** variables changed, not one:

```text
                                  v0.1.10 (gate6 PASS)        held v0.1.11 (gate6 FAIL)
1. installer artifact             desktop 917d2a4e            desktop a917e616
2. audit harness                  $serviceName sing-box-daemon  $serviceName jiejiebox-daemon
                                  layout key SagerNet\sing-box  layout key Jiejiebox\jiejiebox
3. core identity                  v0.1.10, no identity checker  v0.1.11 + verify-desktop-
                                  requirement                   identity-consistency.py
```

The harness changed in the same commit range (`scripts/ci/windows/`, 6 files, +32/−23), and the
service name and registry key were retargeted to the fork's new identity. So "gate 6 passed at
`917d2a4e` and fails at `a917e616`" does **not** isolate the installer: the test that judged
them was not the same test.

I flagged the two-variable version of this earlier; it is three.

## What is nevertheless solid

**The hang is real and is not a harness artefact.** From `Test-InstallerLifecycle.ps1`:

```powershell
$lines.Add("[1/5] silent install")
$exitCode = Invoke-Silently -FilePath $InstallerPath -Arguments (Get-InstallArguments) -What "install"
```

`Invoke-Silently` starts the installer and `Wait-Process -Timeout 900`. **No assertion runs
before this** — `Assert-Installed` only runs if the process exits. So the failure is "the
process did not exit", full stop, and no harness string, service name or registry key can
cause it. Two identical failures on fresh runners, ~15 minutes each.

Supporting facts from the gate's own diagnostics: the install **succeeds** (files staged,
`jiejiebox-daemon` registered and Running, `Jiejiebox 0.1.11` registry entry, signed payload,
`[service install] exit=0`), and then the process stays alive.

Also solid: the last desktop commit (`2d4dd0321` → `a917e616`) touches **only**
`installer-legacy-service-ownership.ps1`, `installer-remove-legacy-service.ps1` and their test —
**no `.nsh` installer code at all**. So wherever the hang comes from, it is not a change to the
installer's script logic in that commit.

## Why I am not dumping a fix in

The remaining possible owners are all inside the installer's own execution, and I have no
instrument that can see them:

* an `nsExec`/PowerShell child that never returns on the install path,
* the `customInstall` tail (`executeDataTransition "Commit"`, shortcut refresh),
* an NSIS message-loop/timer/finish-page lifetime issue,
* or the auto-launch path.

The process snapshot added in this phase was written for exactly this and **cannot run**: the
audit pins its tooling to `core_sha` and asserts they are equal, so the diagnostic tooling never
executes against the held artifact. That is the same wall the bisect hit, and it means any fix I
attempted now would be unfalsifiable in this environment.

## The shortest honest path to a release

In the order I would take them, and each removes exactly one unknown:

1. **Instrument the installer's exit path** in `sing-box-for-desktop`: extend the existing
   CI-only `/CI-DIAGNOSTIC-PATH` markers to `customInstall` entry/exit,
   `executeDaemonServiceCommand` entry/exit, `executeDataTransition "Commit"` entry/exit, the
   finish-page callbacks, and `StartApp`. The mechanism already exists and is inert without the
   option, so this is additive and safe.
2. **Run gate 6 against that instrumented installer.** The last marker before the hang names the
   owner. That single run replaces every hypothesis in this document with a measurement.
3. **Only then fix**, smallest change, with the full lifecycle proof (fresh install, reinstall,
   uninstall, clean reinstall, no orphan process).
4. **Bump to 0.1.12** with the new desktop pin, re-earn all exact-SHA evidence, `v0.1.12`, dry-run,
   publish, read back. `v0.1.11` stays the held failed candidate.

The tooling needed for step 2 does **not** exist yet, and building it is the blocker: either the
audit must accept a named installer artifact from outside the producer (the option recorded in
`PHASE5_BISECT_IMPOSSIBLE.md`), or the held identity must be reopened — which I have declined
three times now, correctly.

## An easy-to-miss point for whoever picks this up

`Test-InstallerLifecycle.ps1` selects the installation by `jiejiebox-daemon` **and**
`HKLM:\SOFTWARE\Jiejiebox\jiejiebox`. Those are the fork's new identity. A machine carrying an
older `sing-box-daemon` installation is a different product to this harness, by design — which is
right, and also means the harness and the installer's identity changes have to move together.
They do here, but it is the reason a bisect across the identity work is not meaningful even in
principle: the measuring instrument itself changed with the thing being measured.

## State

```text
diag/desktop-bisect        709f84019c4 (diagnostic path; not release tooling)
integration/release-next   80286bc71eeafa556b2e632c7f1c8e646769ed8a (untouched)
v0.1.11                    c781d96c635b12962a175e7f6f5a334adab27f11 (unmoved, local and remote)
RELEASE                    HELD, nothing published
```
