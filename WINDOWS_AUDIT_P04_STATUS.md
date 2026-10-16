# P0-4 hardening: implemented and verified against git, blocked by pnpm's stricter patch parser

```text
WINDOWS_AUDIT = PARTIAL
RELEASE_READY = NO
RELEASE       = not published; latest public release remains v0.1.10
v0.1.11       = untouched
```

Branch `audit/windows-client-lifecycle`. Every core commit this round was made with
`--amend` onto the `v0.1.15` diagnostic tag, so no new version number was spent and nothing was
published.

## What is done

**P0-4, the confirmed bug, is fixed in the patch.** The fork's app-builder-lib patch removed the
confirmation MessageBox but never inherited the hardening upstream added afterwards. Verified
against **app-builder-lib 26.15.3's own template** - the version this patch applies to, NOT
upstream main, which has since gained the fix and would have hidden the gap:

```text
BEFORE (26.15.3 + the fork's patch):
  FIND_PROCESS PowerShell : StartsWith('$INSTDIR')           no directory boundary
  KILL_PROCESS PowerShell : Stop-Process -Id $_.ProcessId    NO self-PID exclusion
  taskkill fallback       : /FI "PID ne $pid"                already correct

AFTER:
  both PowerShell lines   : $$_.Path -and $$_.ProcessId -ne $pid -and
                            $$_.Path.StartsWith(''$INSTDIR\'', 'CurrentCultureIgnoreCase'')
```

The self-protection previously existed only on the branch taken when PowerShell is UNAVAILABLE,
while an in-app update launches the installer from a path under `$INSTDIR` - so it matched its own
filter and could terminate itself, and a killed process reports an abrupt exit rather than an
error.

**Verified by applying it, not by reading it.** `git apply` to a pristine 26.15.3 tree is clean and
the resulting template passes 7/7 checks: PID exclusion in both branches, directory boundary in
both, fallback intact, no bare `StartsWith`, no `$$$_` corruption. The section diff against the
original patch shows only `allowOnlyOneInstallerInstance.nsh` changed; the other five sections are
byte-identical.

**P0-2, the archive.** The producer now stores all 20 app-builder-lib NSIS templates as built, the
fork's own `installer.nsh`, and `PROVENANCE.txt` with the patch sha256 and the claimed
core/desktop/version identity. Run 38867726694 produced it successfully. That archive is what made
P0-4 decidable from the artefact instead of from upstream's template plus an inference.

## The blocker, stated exactly

`pnpm install --frozen-lockfile` refuses the patch:

```text
[ERR_PNPM_INVALID_PATCH] Applying patch ".../patches/app-builder-lib@26.15.3.patch" failed:
hunk header integrity check failed
```

while `git apply` accepts the same bytes and produces the intended result. The two disagree, so
"it applies" is not sufficient evidence for this file - which is the lesson of the round.

What was established about it:

* The lockfile hash **is** the patch's sha256 - proved by checking the old value against the old
  patch (`7c56308a…` matched exactly) - and it is updated to `066a18be…` to match.
* Every hunk header's declared counts match its body under a strict counter (11 hunks, 0
  mismatched).
* Both candidate counts for the second hunk were tested empirically against a pristine tree:
  `-91,7 +91,11` is accepted by `git apply`, `-91,8 +91,12` is rejected as a corrupt patch.
* The patch ends without a trailing blank line, is LF-only, and carries the same `index`/`---`/
  `+++` header shape as the five sections that pnpm has always accepted.

So the remaining difference is something pnpm's parser enforces that `git apply` and a
content-counting parser do not. It cannot be reproduced here: this environment has no Node, no
npm and no pnpm, and the producer only surfaces the failure after a ~3 minute dependency install
inside a ~15 minute run.

**The next step is one command in an environment that has pnpm**, and it settles it immediately:

```bash
cd clients/desktop && pnpm install --frozen-lockfile
```

Then iterate on the two hunks until pnpm accepts, and re-run the producer. If the parser turns out
to be intolerant of the multi-line comment blocks I added inside the hunk, the minimal fallback is
to reduce each replacement to a single line with no comment - the same fix, expressed without
adjacent added lines.

## Cost of my mistakes this round, recorded

Four attempts were lost to the same class of error - trusting a check that did not model the real
consumer:

1. `git apply` passed on a patch whose hunk counts were wrong, because git is lenient. pnpm is not.
2. A header recompute used a pattern that made the counts optional and wrote literal `0`s back,
   damaging the file; it was rebuilt from the original.
3. A second recompute counted the artifact of splitting on `LF` (a trailing empty element) as a
   context line, which flipped a header the wrong way.
4. The generator had a header pinned to a value copied from a log line, which silently re-broke
   what had just been fixed.

Each is now guarded by an explicit check rather than a note, and the count question was finally
settled by testing both candidates against the real tree instead of reasoning about them.
