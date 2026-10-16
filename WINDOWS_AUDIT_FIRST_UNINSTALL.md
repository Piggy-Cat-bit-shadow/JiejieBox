# first uninstall: the option IS passed, customUnInit IS invoked, nothing is written

```text
WINDOWS_AUDIT = PARTIAL
P0-4          = COMPLETE (closed; not revisited)
CHANNEL       = FIXED (append works; complete marker lines; monotonic growth)
OPEN          = first uninstall contributes 0 bytes, cause narrowed to one place
RELEASE_READY = NO
RELEASE       = not published; latest public release remains v0.1.10
v0.1.11       = untouched
```

Branch `audit/windows-client-lifecycle` and tag `v0.1.15` both at
`d8222fc1642edf4bb4307c527c9cac9ed20309b0` - verified equal before the build was dispatched.

## Correction to my earlier wording, as instructed

I wrote "the uninstaller never writes". What is measured is narrower and I am stating only that:

```text
1-install            541 bytes
2-reinstall         1092 bytes
3-first-uninstall   1092 bytes   <- 0 increment
final               1805 bytes
```

The final uninstall DOES add 713 bytes, so the uninstaller as a whole is not silent - it is the
**first** uninstall that contributes nothing. Every phase now has its own snapshot, so each phase's
increment is recorded rather than inferred.

## P0-A: customUnInit IS invoked - proved from the generated artefact

`generated-nsis/templates_nsis_uninstaller.nsh` (263 lines), the real archived output:

```text
    5: Function un.onInit
   31: !insertmacro initMultiUser
   33: !ifmacrodef customUnInit
   34:   !insertmacro customUnInit
```

So `un.onInit` does reach `customUnInit`, and the macro is defined (otherwise `!ifmacrodef` would
skip it and the diagnostics would be absent for a different reason). The guard
`$ciDiagnosticPath != ""` must therefore be what skips the write.

A preprocessed (`/PPO`) capture would settle whether our `GetParameters`/`GetOptions` body is in
the final uninstaller byte-for-byte. It is the one piece of P0-A still outstanding, and it needs
makensis, which this environment does not have.

## P0-B: no UAC relay, and the harness demonstrably passes the option

Two independent measurements, both from audit run 38884578460:

```text
preconditions       elevated=YES service=absent
harness passed: "C:\Program Files\Jiejiebox\sing-box\Uninstall sing-box.exe" /S /allusers /CI-DIAGNOSTIC-PATH=D:\a\_temp\jiejiebox-installer-diagnostic.txt
uninstaller processes BEFORE the call: 0
uninstaller processes AFTER  the call: 0
```

* The runner is already elevated, so `initMultiUser` has no reason to spawn an elevated child. The
  UAC-relay hypothesis is not supported here.
* The harness **does** now pass `/CI-DIAGNOSTIC-PATH=` - printed from the composed argument list,
  so this is not an inference.
* No uninstaller image is left behind either side of the call, so the uninstaller runs to
  completion synchronously and orphans nothing.

## The contradiction this leaves, stated exactly

Three facts now hold at once:

1. the generated uninstaller invokes `customUnInit`;
2. the harness passes `/CI-DIAGNOSTIC-PATH=` to the uninstaller, with the value printed;
3. the uninstall pass writes **zero** bytes.

For all three to be true, `customUnInit` must be running with `$ciDiagnosticPath` empty - that is,
the option is on the command line but `GetOptions` does not set `$R1` from it in the uninstaller
image. The installer proves the same construct works, so the parser is not the first suspect even
though the argument is.

**P0-C's bootstrap probe is the right next step and is deliberately not yet written.** The brief is
explicit that a sidecar probe must not depend on `/CI-DIAGNOSTIC-PATH=` itself, and the external
route is to be preferred. The external route has now been exhausted: it proves the argument is
passed and proves nothing is left running, but it cannot observe what the inside of `un.onInit`
saw. So the next run needs the CI-only bootstrap probe at the top of `customUnInit`, writing
`$EXEDIR`, the raw `${GetParameters}` text, `$pid`, and the UAC inner/outer flag to a fixed temp
sidecar - removed once the cause is known.

Note the ordering trap the brief warns about, which is the same one already fixed in `customInit`:
a probe that reports the parsed path cannot be written through `ciDiagnostic`, because the guard
that would suppress it is the very thing under test.

## New audit finding, recorded and not pursued

```text
INSTALL_PATH_NESTING = NEEDS_AUDIT
```

The real command line exposes `C:\Program Files\Jiejiebox\sing-box\`. Two places in `installer.nsh`
disagree about that:

```text
chooseForkInstallationDirectory        sets $INSTDIR to ...\Jiejiebox
showInstallationDirectoryValidationPage
  ${StrContains} $0 "${APP_FILENAME}" $INSTDIR
  ${if} $0 == ""
    StrCpy $INSTDIR "$INSTDIR\${APP_FILENAME}"     appends sing-box
  ${endif}
```

so `...\Jiejiebox` becomes `...\Jiejiebox\sing-box`. That may contradict the fork's own stated
intent that the fork owns a distinct `Jiejiebox` installation directory. Recorded, not investigated
- the service line is the current priority.

## State

```text
branch   audit/windows-client-lifecycle @ d8222fc1642edf4bb4307c527c9cac9ed20309b0
tag      v0.1.15                        @ d8222fc1642edf4bb4307c527c9cac9ed20309b0
worktree clean
v0.1.11  c781d96c635…  untouched
latest public release  v0.1.10
```
