# Windows audit: P0-1…P0-4 status, with one confirmed real bug

```text
WINDOWS_AUDIT = PARTIAL
RELEASE_READY = NO
```

Branch `audit/windows-client-lifecycle` @ `c785b9bb7d57deff073e5e89b31519ba8aaf966f`, tagged
`v0.1.15` locally (a diagnostic marker, never published). `testing`, `v0.1.11` and every public
release untouched. Nothing published; latest public release remains `v0.1.10`.

## P0-4: CONFIRMED REAL BUG in the generated instance guard

The archive added in P0-2 finally makes this decidable from the real artefact rather than from
upstream's template plus an inference. From
`generated-nsis/templates_nsis_include_allowOnlyOneInstallerInstance.nsh` as built:

```text
L 66:  Get-CimInstance ... | ? {$_.Path -and $_.Path.StartsWith('$INSTDIR', 'CurrentCultureIgnoreCase')}
L 94:  ... | % { Stop-Process -Id $_.ProcessId $0 }
L 97:  taskkill /IM "${_FILE}" /FI "PID ne $pid"                    <- fallback only
L118:  Goto doStopProcess            (isUpdated path, no prompt)
L122:  DetailPrint "$(closingApplication)"
```

Three defects, each the exact thing upstream's "installer terminating itself during update" work
corrected, and none of them inherited here:

1. **The PowerShell branch does not exclude the current installer's own PID.** L94 stops every
   process whose path is under `$INSTDIR`, with no `/FI "PID ne $pid"` equivalent - while the
   `taskkill` fallback on L97 *does* have it. So the protection exists on the path that is used
   only when PowerShell is unavailable, and is absent on the path that is normally taken.
2. **The path match has no directory boundary.** Upstream appends a separator
   (`StartsWith('$INSTDIR\')`); this build uses a bare `StartsWith('$INSTDIR')`, so
   `C:\Program Files\Jiejiebox` also matches `C:\Program Files\Jiejiebox-old` and `Jiejiebox2` -
   the adjacent-directory false kill the audit brief called out by name.
3. **The fork's patch removes the confirmation and the safe exit.** The patch deletes

   ```nsis
   MessageBox MB_OKCANCEL|MB_ICONEXCLAMATION "$(appRunning)" /SD IDOK IDOK doStopProcess
   Quit
   ```

   leaving `doStopProcess:` to fall through to directly. Under `isUpdated` that is the intended
   unattended behaviour, but it also means the non-update path no longer has a Quit: it falls
   through and kills. The patch **did** apply (L122 shows the patched `closingApplication`
   string), so this is not a failed application - it is an application with a stale semantic,
   which is the "patch still applies but means something else now" case in the brief.

Consequence to test: during an in-app update the installer is running from a path under
`$INSTDIR`, so it can match its own filter and terminate itself mid-update - silently, with the
updater seeing an abrupt exit rather than an error.

**Fix direction (not yet applied):** port upstream's hardening onto the fork's identity - a
trailing-separator boundary in the filter, and self-PID exclusion in the PowerShell branch -
then re-point the patch at the current template so the next upstream bump cannot re-introduce
the drift.

## P0-1: DONE, published

The reason the uninstall side wrote nothing was structural: `Var ciDiagnosticPath` sat **inside**
`!ifndef BUILD_UNINSTALLER` (line 83) … `!else` (172) … `!endif` (176), so the uninstaller pass
never declared it and `customUnInit` could not touch it. My earlier attempt to initialise it in
`customUnInit` failed to compile precisely because of that.

Now:

* `Var ciDiagnosticPath` is declared **outside** the conditional, so it is a global in both passes;
* `customUnInit` parses `/CI-DIAGNOSTIC-PATH=` exactly as `customInit` does;
* with no such option the variable stays empty and every `ciDiagnostic` write is skipped, so a
  normal installation behaves exactly as before.

`ciDiagnostic` itself is defined unconditionally (nesting depth 0 at its definition), so it is
visible in both passes.

## P0-3: markers added; the channel still produced nothing

The uninstall marker set is in place and ordered:

```text
customUnInit   ENTER / EXIT
customUnInstall ENTER
customUnInstall branch=standalone (isUpdated false)
customUnInstall daemon path resolved; FileExists checked next
customUnInstall daemon PRESENT; calling service uninstall
serviceCommand ENTER action=uninstall   /   EXIT action=uninstall      (pre-existing macro)
customUnInstall EXIT
```

Branch identity is reported by literal markers inside each side of the `isUpdated` test rather
than by interpolating `${isUpdated}`, which expands to a comparison and would split the
`ciDiagnostic` argument - an arity bug already paid for once.

**But the produced `installer-diagnostic.txt` is still 162 bytes of stale-looking content:**

```text
[dataTransition] EXIT op=Rollback
<blank>
onDataDirectory
gram Files\sing-box
s currently valid: 'CN=Jiejiebox CI Test Code Signing', expires 2026-10-18T07:32:40Z
```

That is not a clean prefix of any marker sequence, and it is byte-identical in shape across runs
at different timestamps. Something other than an append is producing it, and until that is
understood the uninstall sequence cannot be read. Note also that `ciDiagnostic` opens the path
with mode `a` (append), so a fresh run should at minimum *grow* the file - it does not.

The service-state probe is unaffected and still says what it said: uninstaller exits 0, service
`RUNNING`, PID alive, SCM key present, `DeleteFlag` empty, `markedForDelete=False`.

## P0-2: DONE

The producer now archives, into `build/records/generated-nsis/`:

* all 20 app-builder-lib NSIS template files as built, including `uninstaller.nsh`,
  `allowOnlyOneInstallerInstance.nsh` and `installSection.nsh`;
* the fork's own `installer.nsh` beside them, for diffing intent against outcome;
* `PROVENANCE.txt` with the patch `sha256`
  (`06785cc112443275dd664987c3c718648bc26bf6ad39ca3d67dba89eb1ef02c3`), the claimed core/desktop
  identity, and the file list.

It fails the step if fewer than three templates are found, so the archive can never be silently
empty. `app-builder-lib`/`electron-builder` versions print `unknown` - `require()` cannot resolve
them from the repo root; resolving them is a small follow-up.

## P0-5: not reached

Product fixes wait on the uninstall sequence, which waits on the diagnostic-file question above.
P0-4 is the exception: it is confirmed from the generated artefact and does not depend on that.

## Next actions, in order

1. **Fix P0-4** (self-PID exclusion + directory boundary) and re-point the patch, with a test
   that fails on a bare `StartsWith` and on a missing self-exclusion.
2. **Explain the 162-byte diagnostic file.** Most likely candidates: the uninstaller is given a
   different path than the installer (so it opens a different file), or the file is being
   truncated by something outside `ciDiagnostic`. Worth checking whether `customUnInit`'s
   `${GetParameters}` sees the option at all in the uninstaller pass - `electron-builder` runs
   the uninstaller with `/S /allusers` and a copied temp image.
3. Only then the service-still-running root cause, which is what the sequence was for.
