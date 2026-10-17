# Diagnostic channel: FIXED and trustworthy. The uninstaller still writes nothing.

```text
WINDOWS_AUDIT = PARTIAL
P0-4          = COMPLETE (closed; not revisited)
CHANNEL       = FIXED (accumulates, complete marker lines, monotonic growth)
RELEASE_READY = NO
RELEASE       = not published; latest public release remains v0.1.10
v0.1.11       = untouched
```

Branch `audit/windows-client-lifecycle` and tag `v0.1.15` both at
`0ec3d1696e234680d239ac102a2989b73111db45`. No new version number; nothing published.

## The root cause was exactly as you said, and it explains every anomaly at once

`FileOpen ... a` preserves the contents but leaves the file pointer at the **start** of the file -
it does not append by itself. `ciDiagnostic` had no `FileSeek`, so every marker overwrote the
previous one from offset 0, and a short write after a long one left the tail of the long string
behind.

That single omission accounts for everything that was previously attributed to an unknown writer:
the file never grew, its size sat near 162 bytes, `[customInstall] EXIT` was followed by a bare
`op=Commit`, and fragments like `ll` and `ore Authenticode signer certificate ...` appeared. Those
fragments are not marker text and never were - they are the residue of overwritten lines.

**There was no second writer.** The truncate/overwrite investigation is closed.

The fix is three words, `FileSeek $9 0 END`, placed between `FileOpen` and `FileWrite`, with a
comment recording the semantics so it is not removed as redundant.

The second fix: `customInit`'s ENTER marker was its **first** statement, before the option was
parsed, so `$ciDiagnosticPath` was still empty and the guard skipped the write. The marker was
structurally incapable of ever appearing. It now sits after `GetParameters` → `GetOptions` →
`StrCpy $ciDiagnosticPath`, matching `customUnInit`, which already had the right order.

## Measured result

Per-phase snapshots from audit run 38882371919:

```text
installer-diagnostic.1-install.txt            541 bytes
installer-diagnostic.2-reinstall.txt         1092 bytes   grew
installer-diagnostic.3-first-uninstall.txt   1092 bytes   unchanged
installer-diagnostic.txt                     1805 bytes   grew
```

Sizes increase monotonically as each phase runs, which is what an appending writer produces and
what the constant 162 bytes never did. And the content is now **complete marker lines**:

```text
[customInit] ENTER
[customInit] diagnostic channel opened; INSTDIR=C:\Program Files\Jiejiebox\sing-box
[customInit] EXIT
[preflight] exit=0
[preflight] exit=0 -PrepareApplicationDataDirectory
[customInstall] ENTER
[serviceCommand] ENTER action=install
[service install] exit=0  DEBUG[0000] core Authenticode signer certificate is currently valid: ...
[serviceCommand] EXIT action=install
[dataTransition] ENTER op=Commit
[dataTransition] EXIT op=Commit
[customInstall] EXIT
```

`[serviceCommand] ENTER action=install` and its `EXIT` are separate lines with their own markers,
`[dataTransition]` has both ENTER and EXIT, and the certificate text is correctly part of the
`[service install] exit=0` line rather than split across two - all of which the overwriting
behaviour had made impossible.

Note also the first marker: `INSTDIR=C:\Program Files\sing-box`, without "Jiejiebox". That is the
**precondition cleanup** removing an older upstream-style installation, and it is now visible only
because the channel works.

## What the channel immediately established: the uninstaller writes nothing

The first-uninstall snapshot is **exactly the same size as the reinstall snapshot** (1092 bytes) and
its tail is still the install phase's `[customInstall] EXIT`. The uninstall phase added **zero
bytes**, so:

* the per-phase snapshots confirm the uninstaller is the only silent participant;
* passing `/CI-DIAGNOSTIC-PATH=` to it - which the harness now does - changed nothing;
* therefore `customUnInit` is either not running in the uninstaller image, or its
  `${GetParameters}`/`${GetOptions}` do not see the option there.

That is now a clean, single question with a trustworthy instrument pointed at it, which is exactly
what the channel work was for. The next measurement is inside `customUnInit`: report
`${GetParameters}` verbatim and whether `${GetOptions}` set the path, before any other work in that
macro.

## Also fixed this round, and worth keeping

* `Invoke-Uninstall` now appends `/CI-DIAGNOSTIC-PATH=` (it previously hardcoded
  `@("/S", "/allusers")`), so the uninstaller is at least given the option.
* `Save-DiagnosticSnapshot` is called after all five phases, so "this phase wrote nothing" is a
  measured statement rather than an inference from silence.

## A publishing mistake worth recording

Twice this round the producer built the wrong commit: the branch ref was stale at an earlier commit
while the tag pointed at the new one, so `--ref <branch>` dispatched the old tree. Both branch and
tag are now verified to resolve to the same SHA before a build is dispatched, and the submodule
content is verified through the API rather than `git show` - which for a submodule path prints only
the gitlink, not the file.

## State

```text
branch   audit/windows-client-lifecycle @ 0ec3d1696e234680d239ac102a2989b73111db45
tag      v0.1.15                        @ 0ec3d1696e234680d239ac102a2989b73111db45
worktree clean
v0.1.11  c781d96c635…  untouched
latest public release  v0.1.10
```
