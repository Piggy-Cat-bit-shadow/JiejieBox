# Windows client audit — status at handoff

```text
WINDOWS_AUDIT = PARTIAL
RELEASE_READY = NO
```

Branch: `audit/windows-client-lifecycle` (created from `94cb4efcecd`).
`testing`, `v0.1.11` and every public release untouched. Nothing published.

## The correction this round produced, which matters more than the plumbing

I recorded, and reported to you, that "the daemon's `service uninstall` is never executed during
the uninstall, because there is no `[service uninstall]` marker in the diagnostic channel".

**That was a false negative, and the channel could not have carried the record either way.**
`$ciDiagnosticPath` is a single NSIS variable initialised only in `customInit` — the
**installer's** callback. `customInit` never runs during an uninstall, so every uninstall-side
marker was inert: the guard `$ciDiagnosticPath != ""` was false and nothing was written. The
absence of uninstall markers was therefore guaranteed regardless of whether the code ran.

The service-state probe is unaffected by this and stands on its own: it is what actually
established that the uninstaller exits 0 with the service RUNNING, PID alive and an empty
`DeleteFlag`.

## What the audit established before that, from source

Verified rather than assumed:

1. **`customUnInstall` runs before the install tree is deleted.** electron-builder's
   `uninstaller.nsh`: `!insertmacro customUnInstall` at line 157, `RMDir /r $INSTDIR` at line 187.
   So the daemon executable **is** present when the teardown runs, and the `FileExists` guard is
   not the cause.
2. **The daemon ships in the payload.** The architecture audit lists
   `resources/daemon/sing-box-daemon.exe` at 80,692,080 bytes, so `FileExists "$0"` holds.
3. **`deleteAppDataOnUninstall: false`**, so the default uninstall carries no
   `--delete-app-data`; `$keepUninstallData` stays `${BST_CHECKED}` and the layout key is
   retained by design. The harness already models this with `-DataPreserved`.
4. **The fork patches `allowOnlyOneInstallerInstance.nsh`**, which is exactly where upstream's
   own "installer terminating itself during update" fix landed:

   ```
   -      MessageBox MB_OKCANCEL|MB_ICONEXCLAMATION "$(appRunning)" /SD IDOK IDOK doStopProcess
   -      Quit
   -
          doStopProcess:
   ```

   The upstream template **excludes the installer's own PID only in the `taskkill` fallback**
   (`/FI "PID ne $pid"`); the PowerShell path
   (`Get-CimInstance … | % { Stop-Process -Id $_.ProcessId }`) has **no self-exclusion**. In the
   fork, `isUpdated` goes straight to `doStopProcess` with no prompt, so during an in-app update
   the installer kills every process under the install directory — including, on a re-launched
   or resident installer, itself. **This is the highest-value open lead and it is not yet
   confirmed for our tree**; it needs the generated `.nsi` to settle.

## The blocker I hit, so it is not repeated

Adding the diagnostic-path initialisation to `customUnInit` fails the build:

```text
Usage: StrCpy $(user_var: output) str [maxlen] [startoffset]
Error in macro customUnInit on macroline 12
!include: error in script: "uninstaller.nsh" on line 34
```

`customUnInit` expands at top level in the uninstaller pass, where `Var` is not legal, and the
preceding `Var applicationDataDirectory` / `Var daemonDataDirectory` / `Var installationID`
(lines 173-175) sit under an `!else` of a `BUILD_UNINSTALLER` conditional. Moving or guarding a
`Var` there needs the generated `uninstaller.nsh` in hand to see which branch the uninstaller
pass actually takes. **That is the one artifact this audit still needs**, and it is obtainable:
have the producer upload the generated `uninstaller.nsh` and the `.nsi` alongside the existing
build records.

## Next actions, in order

1. **Capture the generated NSIS.** Add the generated `uninstaller.nsh`, `installer.nsi` and the
   post-patch `allowOnlyOneInstallerInstance.nsh` to `build/records/`. Everything above about
   ordering and self-exclusion becomes settled rather than inferred.
2. **Resolve the `Var` placement** with that file, then open the uninstall diagnostic channel and
   finally see the uninstall sequence.
3. **Then** the service-still-running question has a real record instead of an inferred one.
4. The `installer-service.ps1` `WaitForExit()` hardening remains a separate, still-unclaimed
   improvement.

## Fixed and verified, unchanged by this round

* **install / reinstall exit 0** — the `dataMigrationCleanupFailed` MessageBox now carries a
  default button, placed after the message text.
* **core no longer swallows the stop error** — `experimental/boxdd/cmd_service_windows.go`
  returns it, with a falsified regression test
  (`cmd_service_uninstall_contract_test.go`).
* `Get-ServiceState` disposes its `ServiceController`, so the harness can no longer pin the
  service it is waiting to disappear. Ruled out as the cause of this failure, and recorded as
  such.

## Ledger

```text
v0.1.11  c781d96c635...  HELD failed candidate, tag untouched
v0.1.15  5688fd0eff1...  current candidate; last published state 4e680f9cc40 built cleanly
nothing published; latest public release remains v0.1.10
```
