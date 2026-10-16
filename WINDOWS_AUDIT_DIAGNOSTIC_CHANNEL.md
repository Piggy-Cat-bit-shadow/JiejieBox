# Diagnostic channel: the uninstaller writes NOTHING, and the file never grows

```text
WINDOWS_AUDIT = PARTIAL
P0-4          = COMPLETE (closed earlier, not revisited)
RELEASE_READY = NO
RELEASE       = not published; latest public release remains v0.1.10
v0.1.11       = untouched
```

Branch `audit/windows-client-lifecycle` @ `65ba21ec1330354d58cd25fede7e750130e10804`, committed with
`--amend` onto the `v0.1.15` diagnostic tag. No new version number; nothing published.

## Layers 1 and 2 of the brief: ANSWERED, and they were the cause of one of the two false negatives

The exact command lines, printed from run 38876089118:

```text
> install        ...Jiejiebox-v0.1.15-windows-x64.exe /S /CI-DIAGNOSTIC-PATH=D:\a\_temp\jiejiebox-installer-diagnostic.txt
> reinstall      ...Jiejiebox-v0.1.15-windows-x64.exe /S /CI-DIAGNOSTIC-PATH=D:\a\_temp\jiejiebox-installer-diagnostic.txt
> first uninstall  C:\Program Files\Jiejiebox\sing-box\Uninstall sing-box.exe /S /allusers
```

**The uninstaller was never given `/CI-DIAGNOSTIC-PATH=`.** Confirmed in the source:
`Get-InstallArguments` appends it; `Invoke-Uninstall` built a hardcoded `@("/S", "/allusers")`.

The uninstaller is a separate executable image and `customUnInit` reads its own command line, so
`$ciDiagnosticPath` stayed empty in the uninstall pass and every uninstall-side marker was inert.

**This was the second, independent cause of the same false negative.** The first was that the
uninstall pass never initialised `$ciDiagnosticPath` at all (the variable lived inside the
`!ifndef BUILD_UNINSTALLER` block). Either mistake alone leaves the file with no uninstall markers,
which is why "there is no `[service uninstall]` marker" looked like evidence that the daemon's
uninstall never ran. It was not evidence, twice over.

Both are now fixed: the variable is declared outside the conditional and parsed in `customUnInit`,
and `Invoke-Uninstall` appends the option.

Per-phase snapshots were added as well (`Save-DiagnosticSnapshot`, called after each of the five
phases), because one appended file read only at the end cannot distinguish the phase that failed
from the ones that succeeded.

## What the fix revealed: the uninstaller writes nothing, and the file does not grow

Four snapshots from run 38878500264, taken after install, reinstall, the first uninstall, and the
final read:

```text
installer-diagnostic.1-install.txt          162 bytes
installer-diagnostic.2-reinstall.txt        162 bytes
installer-diagnostic.3-first-uninstall.txt  162 bytes
installer-diagnostic.txt                    162 bytes
```

**All four are byte-identical**, and that is the finding:

```text
5b 63 75 73 ... 5a 0a 0d 0a
[customInstall] EXIT
op=Commit

ll
ore Authenticode signer certificate is currently valid: 'CN=Jiejiebox CI Test Code Signing', expires 2026-10-18T09:03:54Z
```

Three things follow, none of them inferred:

1. **The uninstaller contributed zero bytes.** The file's leading content is the *install* phase's
   `[customInstall] EXIT`. Passing the option changed nothing, so the uninstall pass still is not
   writing - the remaining suspect is that `customUnInit`'s `${GetParameters}`/`${GetOptions}` do
   not see the option in the uninstaller's own image, which is the next layer to prove.
2. **The file does not grow.** `ciDiagnostic` opens with mode `a`, so eight-plus writes across
   install, reinstall and two uninstalls must make it larger. It stays at exactly 162 bytes from
   the first snapshot to the last. A constant byte count across four reads is not something an
   appending writer can produce, so something is truncating or overwriting it, or the writes are
   not reaching this path at all.
3. **The content is not a marker sequence.** `[customInstall] EXIT` then a bare `op=Commit` on the
   next line - but `ciDiagnostic` writes `"[${STAGE}] ${DETAIL}$\r$\n"`, which would make that one
   line `[customInstall] EXIT op=Commit`. The fragments `ll` and
   `ore Authenticode signer certificate is currently valid: …` are slices cut from the middle of a
   longer message, not output of that macro.

So the file is not accumulating marker writes. It looks like a fixed-size window that something
replaces, and the next measurement is to have `ciDiagnostic` itself report the file's size
immediately before and after each write - which separates "the write never happened" from "the
write happened and something else reset the file".

## Cost of my mistakes here, recorded

I reported twice that the absence of an uninstall marker proved the daemon's uninstall never ran.
It proved nothing, for two independent reasons, and in both cases the check I trusted had not been
shown to be capable of producing the positive result. The per-phase snapshot exists so that
"this phase wrote nothing" is now a measured statement rather than an inference from silence.

## State

```text
branch   audit/windows-client-lifecycle @ 65ba21ec1330354d58cd25fede7e750130e10804
worktree clean
v0.1.11  c781d96c635…  untouched
v0.1.15  diagnostic marker only; never published
latest public release  v0.1.10
```
