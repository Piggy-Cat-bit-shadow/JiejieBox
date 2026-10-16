# Installer bisect — blocked by a release-identity constraint, with the fix identified

```text
LAST_GOOD   = desktop 917d2a4e (gate 6 PASS, run 38842997048)
FIRST_BAD   = NOT YET DETERMINED
FIRST_BAD_CHANGE = not determined
HANG_OWNER       = not determined
ROOT_CAUSE       = not determined beyond PRODUCT_REGRESSION
FIX_COMMIT       = none
NEW_DESKTOP_SHA  = none
NEW_CORE_SHA     = none

FRESH_INSTALL / REINSTALL / UNINSTALL / CLEAN_REINSTALL = unchanged from the held state
GATE6       = FAIL on a917e616, PASS on 917d2a4e (both measured, gate 6 alone)
RELEASE     = HELD
```

## The bisect cannot run as-is, and the reason is structural

The brief says to bisect by varying the desktop revision. **The release pipeline does not
permit that**, and the block is not a tooling accident:

1. `client-desktop-windows.yml` obtains the client with
   `git -C core submodule update --init --recursive clients/desktop`, so the desktop
   revision IS the `clients/desktop` gitlink in the core commit it is handed.
2. Varying that gitlink requires a **new core commit**.
3. `check-desktop-source-identity.sh` then requires, of that commit:

   ```sh
   describe="$(git -C "$core_dir" describe --tags)"
   [ "$describe" = "v$version" ] || FAIL
   ```

   i.e. `git describe --tags` must be **exactly** `v0.1.11`. Measured:

   ```text
   git describe --tags c781d96   -> v0.1.11            (the tagged release commit)
   git describe --tags a81201c   -> v0.1.11-3-ga81201c (any commit after it)
   ```

So a commit that varies the pin can never be built by the producer. The constraint is
`describe == v<version>` **exactly**, and the only commit that satisfies it is the one the
tag names.

`v0.1.11` names `c781d96`, and the brief forbids moving it, so varying the desktop pin and
building with the producer are mutually exclusive as things stand. That is the whole
blocker — not a missing tool, not a wrong command.

### What I did NOT do, deliberately

* **Did not move `v0.1.11`.** Verified before and after: local and remote both peel to
  `c781d96c635b12962a175e7f6f5a334adab27f11`. `git tag v0.1.11 <diag>` was refused by git
  and I left it refused rather than forcing it.
* **Did not weaken the producer's identity gate.** Adding a `DESKTOP_REF_OVERRIDE` that
  bypasses the describe assertion in the release-blocking producer is exactly the
  "weaken the normal release workflows/source-identity gates" the brief forbids.
* **Did not create release tags for bisect points.**

## The fix, which is small and matches an existing precedent

The repository **already has this pattern**, in the audit workflow:

```yaml
tooling_ref:
  description: 'NON-RELEASE DIAGNOSTIC ONLY. Leave EMPTY: the audit tooling is then
                checked out at core_sha. Set it only to test new scripts against an older
                artifact, and read the resulting report as a diagnostic, never as an
                acceptance result.'
```

and the audit sets `NON_RELEASE_DIAGNOSTIC = true` from it, warns loudly, and refuses to
report the run as acceptance evidence.

The producer needs the same two halves, and **both are required**, because the describe
assertion is a property of the commit rather than of the checkout:

```text
1. a desktop revision override (the gitlink's replacement), and
2. an acceptance of `v$version-<n>-g<sha>` for the core's describe, gated on the same
   NON_RELEASE_DIAGNOSTIC flag.
```

Design constraints I would hold it to:

* `client-desktop-windows.yml` stays the release producer; the override is an input that
  **defaults to empty and changes nothing** when unset. A `gates:`-style labelled input is
  not enough on its own here because the tag assertion must also relax, so the relaxation
  must be explicitly tied to the flag and must **fail closed** when the flag is absent.
* The tag must be asserted to be on this history (`v$version` an ancestor of the built
  commit), so a diagnostic still cannot be built from an unrelated tree.
* The run records `CORE_SHA`, `DESKTOP_SHA_OVERRIDE`, `TOOLING_SHA`,
  `NON_RELEASE_DIAGNOSTIC = true`, and prints the "not an acceptance result" warning the
  audit already prints.
* The audit's `AUDITED_BUILD_SHA == AUDIT_TOOLING_SHA` guard should stay as it is; the
  bisect's audited SHA is the producer's head SHA, which is also its tooling revision, so
  it already holds.

With that in place the bisect is ~3 runs of the prompt's own plan, starting from the
ancestry order the brief specifies:

```text
917d2a4e   PASS (known)
  1603c4e48   isolate the fork's application identity      <- 8 installer files
  93986e039   persist subscription quota metadata          <- 0 installer files
  78b25f46f   remove the previous version's daemon service <- 2 installer files
  01d5ebedc   subscription tests                           <- 1 installer file (identity.ts)
  2d4dd0321   give the fork its own installation directory <- 2 installer files
  a917e6161   prove ownership before removing legacy svc   FAIL (known)
```

## A second constraint that shrinks the bisect to three candidates

The producer is not only tag-gated, it is **identity-gated**, and that gate silently
excludes the earlier desktop revisions:

```yaml
- name: "[gate 2] confirm the client's and the core's machine-global identity agree"
    python tooling/scripts/ci/verify-desktop-identity-consistency.py \
      --client-dir "$CLIENT_DIR" --core-dir core
```

`verify-desktop-identity-consistency.py` reads `clients/desktop/src/main/identity.ts` as
the source of the names it compares against the core. **`identity.ts` was ADDED by
`1603c4e48`** (`git log --diff-filter=A -1 -- src/main/identity.ts`), so every earlier
desktop revision lacks the file the checker needs and would fail gate 2 before any
installer was built.

Therefore the buildable-and-relevant bisect range is:

```text
917d2a4e   PASS                              (control, already measured)
  1603c4e48   <- EARLIEST BUILDABLE candidate
  93986e039
  78b25f46f   <- latest candidate before the known-bad
  a917e6161   FAIL                            (already measured)
```

**Three candidates, so at most two runs** once the override exists — and `93986e039`
touches no installer code, so if the first probe lands on `1603c4e48` the answer is likely
immediate. This also means the brief's suggested midpoint (`78b25f46f`) is still the right
first probe: FAIL there narrows to `1603c4e48` or itself; PASS narrows to the one commit
between it and `a917e6161`.

Verified alongside: the identity checker **passes at the current pin** (exit 0, 15 values
compared), so the gate is not independently broken — it is working, and it is what bounds
the bisect.

## Also worth knowing before the next attempt

* **`installer-service.ps1:50` has an unbounded `$process.WaitForExit()`** with no timeout.
  The diagnostic channel proves it returned in *this* failure (`[service install] exit=0`),
  so it is **not** this root cause — but it is the one place an installer step waits
  unbounded on the daemon, so harden it separately with its own test, not as this fix.
* **The diagnostic tooling cannot be run against the held artifact at all**, because the
  audit pins tooling to `core_sha` and then asserts they are equal. So every diagnostic
  iteration needs the diagnostic commit to also be the audited commit — which is another
  reason the producer-side override above is the right shape: it makes the diagnostic
  commit buildable, and the audit then follows it normally.

## State left behind

```text
branch HEAD        a81201c859b82c5ce27355ef9b95ec824dbffc5f (integration/release-next)
worktree           CLEAN
clients/desktop    a917e616190050b845061cc3798ff7cf49d4c3a1 (the release pin, restored)
v0.1.11            c781d96c635b12962a175e7f6f5a334adab27f11 (unmoved, local and remote)
diag branches      none (diag/installer-bisect deleted locally and on the remote)
diagnostic tags    none
RELEASE            HELD, nothing published
```
