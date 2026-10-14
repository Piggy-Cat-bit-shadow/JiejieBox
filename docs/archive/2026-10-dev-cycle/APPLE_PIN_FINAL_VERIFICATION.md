# APPLE_PIN_FINAL_VERIFICATION

Task: 0.1.6 closure task C — Apple pin / bash CI / submodule reachability verification,
plus the remediation and cleanup rounds that followed it.

Round 1 closed F1–F6 on the parent side and settled the branding-contract conflict
**without re-freezing the Apple repository**. It also found and fixed a latent
false-negative bug in the provenance gate itself, and corrected a brand-spelling error
introduced during the remediation.

Round 2 was evidence/documentation cleanup only: the pre-fix transcripts were deleted
rather than kept, this report's HEAD fields were split so the base and the verified branch
tip can no longer be confused, and two superseded Apple documents were marked for what
they are. No production code changed in round 2.

No Apple/UI content was modified. No `main`/`testing` branch was touched. The frozen Apple
SHA did not change: **`90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07` remains final.**

---

## 0. Required result block

```text
BASE_PARENT_HEAD               = 96a94f584d30691cadd44dab83b64d56fc7a1fec
VERIFIED_BRANCH_HEAD           = cfc77dc51ab7322e993b8ae0b94b96db59e48f47
APPLE_GITLINK                  = 90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07
VERIFIED_APPLE_SHA             = 90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07
SUBMODULE_HEAD                 = 90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07
APPLE_ORIGIN_MAIN              = 90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07
FROZEN_SHA_REACHABLE_FROM_MAIN = YES
APPLE_PIN_GATES_RUN            = 25
APPLE_PIN_GATES_PASS           = 25
AUTHORITATIVE_STALE_PINS       = 0
HISTORICAL_OLD_SHA_MATCHES     = 47
APPLE_PIN_VERIFICATION_COMPLETE = YES
```

### The two HEAD fields are different things

| Field | Value | Meaning |
| --- | --- | --- |
| `BASE_PARENT_HEAD` | `96a94f584…` | the integration line this branch was cut from — `origin/integration/0.1.6-final-freeze`. It is the **base**, not the verified tree. |
| `VERIFIED_BRANCH_HEAD` | `cfc77dc51…` | the tree every gate in §3 was actually run against. Every file in `verify-evidence/` records this SHA in its own header. |

An earlier revision of this report printed a single `PARENT_HEAD = 96a94f584…` line, which
read as though the gates had been run at the freeze commit. They were not: they were run at
the branch tip, which carries the fixes. `BASE_PARENT_HEAD` and `VERIFIED_BRANCH_HEAD` are
deliberately not the same commit; that difference *is* the remediation.

The report and the evidence are committed **after** `VERIFIED_BRANCH_HEAD`, as a
documentation-only commit. That commit changes no script, test, workflow or patch, so it
cannot invalidate the results recorded against `cfc77dc51…`; to re-verify, check out
`cfc77dc51…` and re-run `verify-evidence/gates.tsv`.

All four conjunctive conditions hold:

| Condition | Result |
| --- | --- |
| `gitlink == VERIFIED_APPLE_SHA == submodule HEAD` | **YES** |
| frozen SHA is an ancestor of `origin/main` | **YES** (`exit 0`) |
| Apple pin gates all green | **YES** — 25 / 25 |
| machine-consumed stale pin `= 0` | **YES** |

Machine-readable tally: `verify-evidence/gates-final.tsv`.

---

## 1. The branding-contract decision (why the Apple SHA did NOT move)

The original report classified F3 as "a defect in the frozen Apple source". That was
**wrong**, and the remediation established what is actually true.

The pinned Apple repository enforces its own naming boundary in
`clients/apple/scripts/dev/audit_apple_ui_boundary.py`, which passes `20 PASS / 0 FAIL`:

```python
#: The one brand override this fork is allowed to make. `INFOPLIST_KEY_CFBundleDisplayName` is the
#: smallest safe lever: it changes what SpringBoard, the Dock and Spotlight show and nothing else.
BRAND_DISPLAY_NAME = "Jijiebox"

#: Settings that carry a user-visible name but must keep their upstream value, because they are
#: identity, protocol or signing surface rather than presentation.
PROTECTED_NAME_SETTINGS = (
    "PRODUCT_NAME",
    "PRODUCT_BUNDLE_IDENTIFIER",
    "BASE_PACKAGE_IDENTIFIER",
)
```

Its audit asserts `PRODUCT_NAME: 42 occurrence(s), identical to 089d35e`. Commit `7271382`
("show Jijiebox as the app's name") says `PRODUCT_NAME` was *"Deliberately unchanged …
identity or protocol surface"* — it drives the `sing-box://` URL scheme, Profile types and
the built bundle name.

The parent's overlays demanded the exact opposite: brand `PRODUCT_NAME` too, rename the
built product, and rewrite the scheme `BuildableName`. Three conflicts, not one:

| | Parent (before) | Apple (enforced) |
| --- | --- | --- |
| `PRODUCT_NAME` | → brand (`JijieBox.app`) | must stay `sing-box` |
| iOS display name | `JijieBox` | `Jijiebox` |
| scheme buildable | rewritten to a branded `.app` | must follow `PRODUCT_NAME` |

**Resolution (A′):** brand display and product identity are separate. The parent brands
**only** `INFOPLIST_KEY_CFBundleDisplayName`; `PRODUCT_NAME` is identity surface and is
never written. The built bundle stays `sing-box.app` and the scheme is validated, never
rewritten. That satisfies both repositories, so the Apple repository stays closed and
`90a0e9ae` remains the final frozen SHA.

### The brand string

`JijieBox` — J-i-e-j-i-e-B-o-x, **9 characters**. The pinned client commits the same word
with a lower-case b (`Jijiebox`), which is an accepted starting state normalised by the
overlays. Near-misses (`Jijiebox`, `JijieBox` — one "e" short) are deliberately **not**
accepted, so a typo is refused rather than propagated.

> During remediation the brand was briefly written as the 8-character near-miss
> `JijieBox` (J-i-j-i-e-B-o-x). That was a real defect, it was caught by a fixture that
> asserted the wrong spelling could be rejected, and it is fixed everywhere. A regression
> check now exists: `brand-is-jiejiebox`.

---

## 2. Apple pin identity and reachability (task §3–§5)

```bash
$ git ls-files -s clients/apple
160000 90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07 0	clients/apple

$ git submodule status clients/apple          # pre-init: leading '-' == not initialised
-90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07 clients/apple

$ git submodule update --init clients/apple
Submodule path 'clients/apple': checked out '90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07'

$ git -C clients/apple rev-parse HEAD
90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07

$ git -C clients/apple merge-base --is-ancestor 90a0e9ae… origin/main ; echo $?
0

$ git -C clients/apple ls-remote --heads origin
d1224bb5081b3df5d0ecc55b1bd3d72ea6c60628  refs/heads/dev
90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07  refs/heads/main
```

`VERIFIED_APPLE_SHA` has exactly **one** machine-consumed source —
`scripts/ci/check-libbox-abi.sh:103` — and it is compared against the live superproject
gitlink, not against a hidden constant:

```bash
check_gitlink() {
  actual="$(cd "$repo_root" && git ls-tree HEAD "clients/$path" | awk '{print $3}')"
  ...
}
check_gitlink apple "$VERIFIED_APPLE_SHA" apple
```

```text
PASS: apple gitlink is the verified revision (90a0e9aef4c0)
```

`release/apple-client-refs.env` deliberately carries **no** SHA (0 `*_SHA=` lines), so
`AUTHORITATIVE_STALE_PINS = 0` is by construction.

`clients/apple` also has a nested submodule (`Frameworks/Runestone`) that the task's
non-recursive init leaves empty; the overlays require it. `git -C clients/apple submodule
update --init --recursive` resolves it.

---

## 3. Final gate results

| # | Gate | Exit |
| --- | --- | --- |
| 1 | `apple-abi-provenance.sh` (`APPLE_ABI_CHECK_REMOTE=1`) | 0 |
| 2 | `check-libbox-abi.sh` | 0 |
| 3 | `check-apple-source-selection.sh` | 0 |
| 4 | `test-apple-signing.sh` | 0 |
| 5 | `test-apple-macos-name.sh` | 0 |
| 6 | `test-apple-beta-publish.sh` | 0 |
| 7 | `verify-fork-handoff.sh` | 0 |
| 8 | `apple-client-source.sh assert-on-branch` | 0 |
| 9 | `apple-client-source.sh assert-shared-source` | 0 |
| 10 | `apple-client-source.sh sha ios` | 0 |
| 11 | `apple-client-source.sh sha macos` | 0 |
| 12 | `apple-client-source.sh branch ios` | 0 |
| 13 | `apple-client-source.sh dir ios` | 0 |
| 14 | `apple-client-source.sh verify ios` | 0 |
| 15 | `apple-client-source.sh verify macos` | 0 |
| 16 | `apple-client-source.sh checkout macos` (idempotent) | 0 |
| 17 | gitlink recorded == frozen SHA | 0 |
| 18 | `git submodule status` reports no drift | 0 |
| 19 | frozen SHA reachable from `origin/main` | 0 |
| 20 | `VERIFIED_APPLE_SHA` is the single machine source | 0 |
| 21 | inline CI submodule check (workflow lines 132-143) | 0 |
| 22 | no `assert-distinct` callers remain | 0 |
| 23 | no `.git`-as-directory guard remains | 0 |
| 24 | brand is `JiejieBox` in both overlays | 0 |
| 25 | `PRODUCT_NAME` is protected in both overlays | 0 |

Sub-suite tallies: `check-apple-source-selection: 65 passed, 0 failed`;
`test-apple-signing: 134 passed, 0 failed`; `test-apple-macos-name: 90 passed, 0 failed,
0 skipped`; `apple-abi-provenance: FAIL: 0   WARN: 0`.

---

## 4. Findings fixed in this round

### F1 — `assert-on-branch` could never accept a submodule

`apple-client-source.sh` tested the git dir with `-d`; a submodule's `.git` is a gitfile.
Fixed to `-e`. **A second, unreported occurrence was found** in `checkout_macos`
(`$submodule/.git`), which made `checkout macos` impossible on every correct checkout — the
message differed, so the original sweep missed it. A third `-d` (the `git clone` target,
where `.git` really is a directory) was hardened to `-e` so the file has one rule.

### F2 — CI and release scripts called a removed action

`assert-distinct` was renamed to `assert-shared-source` when the two-source model was
replaced. **Five** callers still used the old name, not two:

| Caller | Effect |
| --- | --- |
| `.github/workflows/client-apple.yml:234` (ios job) | job aborts |
| `.github/workflows/client-apple.yml:496` (macos job) | job aborts |
| `scripts/release-apple.sh` | release aborts |
| `scripts/publish-apple-beta.sh` | publish aborts |
| macOS job "check out the macOS Apple source" | **asserted the opposite** of the truth |

That last one required the macOS checkout to *differ* from the iOS checkout — which is
necessarily false under one shared source, so it failed every correct build. It now
requires them to be equal.

Two further wiring defects in the same job: the parent checkout used `submodules: false`,
which made `checkout macos` unsatisfiable (it needs `clients/apple` as its local clone
source), and the job header still documented "Repo B @ MACOS_APPLE_SHA / branch dev".
`check-apple-source-selection.sh` was rewritten for the one-source model (459 → 626 lines,
65 checks) — it previously died on `MACOS_APPLE_BRANCH: unbound variable`.

Two machine-readable reports told the reader the SHA came from
`release/apple-client-refs.env (MACOS_APPLE_SHA)`, a file that has contained no SHA for
some time; `publish-apple-beta.sh` even printed `distinct sources: PASS (the two SHAs
differ, as they must)` — the exact inverse of the truth.

### F3 — the branding contract (see §1)

Resolved on the parent side. Both overlays rewritten to write one setting each; both
`check-macos-app-name.sh` and the workflow's prepared-tree step now assert the two names in
**opposite** directions (display name must be branded, identity must not be), and a bundle
named `JijieBox.app` is now the regression they catch.

### F4 — stale ABI provenance patches

All four `docs/fork/apple-*.patch` files described a tree that no longer exists. They are
now generated by script as real diffs whose post-image **is** the pinned blob:

```
base 9adb1f822  index 2639c93..8f066b5  pinned 8f066b5e6   GlobalChecksModifier.swift
base 9adb1f822  index aaddf49..c7a3bd4  pinned c7a3bd498   RootHelperService.swift
base 9adb1f822  index 576bac2..9787829  pinned 9787829c2   IOSRootHelperService.swift
base 9adb1f822  index 228716d..f026db6  pinned f026db6f9   BridgeTunTracker.swift
base 0ad1730ab  index e6ce034..020f5b9  pinned 020f5b93c   ExtensionPlatformInterface.swift
base 9adb1f822  index f24475d..020f5b9  pinned 020f5b93c   ExtensionPlatformInterface.swift (implementer)
base 8d06a99d6  index c47cdac..48b97ff  pinned 48b97ffe3   ExtensionProvider.swift
base f77289f7c  index ffbeb70..451736a  pinned 451736a84   ScreenStateObserver.swift
```

`ExtensionPlatformInterface.swift` is cited by two patches; its hunks are split so each
patch carries the anchors it is cited for (2 call hunks, 1 implementer hunk, 13 unassigned
hunks that belong to neither claim).

The pinned `ScreenStateObserver.swift` is a 731-line protocol-based rewrite, so the three
observer anchors were re-pointed at declarations that actually exist there
(`protocol DarwinNotifyAPI: AnyObject {`, `func recordScreenState(on: Bool)`,
`func recordLockState(locked: Bool)`), and `scripts/ci/apple-abi-probe/observer.swift` was
rewritten to mirror them plus an adapter that forwards each method to
`LibboxCommandServer`. Both legs of the probe were run against a real Libbox slice:

```text
iOS   green leg (callsites + implementer + observer): exit=0  errors=0
macOS leg        (must compile away):                 exit=0  errors=0
```

### F5 — false positive in the private-key check

`BEGIN .*PRIVATE KEY` matched a doc comment and two PEM-redaction fixtures in
`common/physicalpath/`. The pattern is now anchored at column 0
(`^-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`), which was proven against fixtures to still
detect a real key block and to ignore both the comment and the Go string literals.

### F6 — `verify macos` precondition

`verify macos` defaulted to the ephemeral `build/apple-client-macos` and failed when it did
not exist, reading like a pin mismatch. It now falls back to the shared authoritative
checkout **only** when no directory was named and the ephemeral tree is absent; a named
directory that does not exist is still an error, and `MACOS_APPLE_DIR` is unchanged so CI's
per-platform working trees are unaffected.

---

## 5. A bug found in the gate itself

While verifying F4 the provenance gate reported three anchors as misattributed that were
demonstrably present in the patch. The cause was in the gate, not the data:

```bash
set -euo pipefail
...
if ! patch_added_lines "$patches_dir/$patch" "$path" | grep -Fq -- "$anchor"; then
```

`grep -q` exits the moment it matches, sending SIGPIPE to the writer. Under `pipefail` the
**pipeline** status becomes 141 even though the match was found, so the check reports a
false negative. It is size-dependent — the same anchor passes against a short patch and
fails against a long one — which is why it lay dormant until a 725-line patch was
generated:

```text
without pipefail : pipeline status = 0   (match found)
with pipefail    : pipeline status = 141 (SIGPIPE -> false FAIL)
match position   : line 59 of 725
```

The gate now uses a `case` glob with no pipeline, and the same pattern was removed from the
tier-1 and tier-4 checks (where `git show | grep -q` was equally exposed). The gate went
from `FAIL: 8` to `FAIL: 0   WARN: 0`.

---

## 6. Stale pin sweep (task §7)

Machine-consumed paths only (`scripts`, `release`, `.github`, `Makefile`):

```text
AUTHORITATIVE_STALE_APPLE_PIN = 0
```

| Old Apple SHA | Hits in machine-consumed paths | Explanation |
| --- | --- | --- |
| `bb5309b4…` (previous gitlink) | 0 | fully retired |
| `5911580a…` | 0 | docs only |
| `2b23330d…` | 1 | prose in a comment in `apple-abi-probe/callsites.swift` |
| `32f915ba…` | 1 | the **desktop** gitlink, not Apple |

Historical matches, retained deliberately and correctly **not** globally rewritten:

```text
HISTORICAL_OLD_SHA_MATCHES = 47 occurrences in 19 files
```

All five `assert-distinct` callers are gone; every remaining mention is commentary that
explains what was removed.

---

## 7. Constraints observed (task §8)

```text
Apple/UI main modified                  NO  (still 90a0e9ae on branch main, 0 dirty)
Apple feature branch rebuilt            NO
frozen Apple SHA changed to pass a gate NO  (unchanged)
historical old SHAs globally replaced    NO
main / testing modified                 NO  (see the two refs below)
```

The `testing` ref, stated precisely because it is easy to misquote:

```text
LOCAL  testing            = 9f9eea55d4d911899c6f4e7bc597920686d4bc1b   (this clone, untouched)
REMOTE origin/testing     = 6cc45d3f49a3908ff150045d3a113f98920eaaea   (never pushed to)
```

They differ, and that is the pre-existing state of this clone, not something this work
caused: the local `testing` was already behind `origin/testing` when the branch was cut, and
nothing in this task fetched into, reset, or pushed `testing`. An earlier revision printed a
bare `testing still 9f9eea55d`, which was true of the local ref but read as though it
described the remote. The parent repository has no `main` branch at all.

Both refs and the integration line are recorded on every run in
`verify-evidence/pin-identity.log`.

Writes were confined to a dedicated worktree on branch `fix/0.1.6-apple-pin-gates`
(derived from `origin/integration/0.1.6-final-freeze`): a fetch of remote-tracking refs, the
parent-side script/workflow/test changes, the regenerated patches, and this report plus
`verify-evidence/`. The overlays' edits to `clients/apple` were reverted after every
exercise: `git -C clients/apple status --porcelain` is empty and its HEAD is `90a0e9ae`.

---

## 8. Cleanup round (round 2)

Documentation and evidence only; no production behaviour changed.

**Evidence replaced, not extended.** The pre-fix transcripts were deleted rather than kept
beside the new ones. They recorded `FAIL: 8` from the provenance gate,
`MACOS_APPLE_BRANCH: unbound variable` from the selection gate, and the old
`Jijiebox`-plus-branded-`PRODUCT_NAME` overlay behaviour — none of which describes anything
in the tree any more. `verify-evidence/` now holds 15 files, all regenerated at
`VERIFIED_BRANCH_HEAD`, with a `README.md` stating where each came from and what was
deliberately not kept. Small per-action transcripts were folded into grouped logs instead of
being kept as separate files for the appearance of completeness.

**Two superseded Apple documents marked.** `docs/fork/apple-screen-state-initial-facts.patch`
is a prepared fix for a file the pinned client has since replaced: its base `d3e8035` was the
29-line `#if os(iOS)` observer, and at `90a0e9ae` that file is a 731-line implementation in
which the initial-state fact is first-class
(`ScreenStatePublishing.reconcileScreenState(on:)`, driven by
`observe(source:provenance:epoch:)` reading through `notify.getState(token:)`). The patch now
carries a `HISTORICAL / NON-AUTHORITATIVE — DO NOT APPLY` header, and the section of
`docs/fork/apple-artifact-verification.md` that presented it as a "Prepared fix" now says the
same. Worth recording: `git apply --check` on it still **succeeds** at a 536-line offset,
because the old hunk's context recurs further down the rewritten file — a clean `--check` is
a false positive here, which is exactly why the header exists rather than a silent deletion.
It was never in the provenance gate's `patch_files`, so it never affected a gate result.

**Three comments corrected** — the last places still describing the removed two-source model
as current, in `prepare-apple-client.sh` (twice) and `check-apple-links.py`. Each was
verified against the pin before rewriting: the "differences" are `#if os(...)` branches of
one file, and the About section holds exactly one fork source and one fork releases after the
link overlay, measured rather than assumed.

### NON_BLOCKER recorded, not fixed

```text
NON_BLOCKER — power-report-draft shim is inert
  file    : scripts/ci/prepare-apple-client.sh (the promote_calls / promote_defs guard)
  root    : LibboxPromotePowerReportDraft is now absent from the pinned Apple client, from
            both platform working trees, and from experimental/libbox in the core. The guard
            is "if the provider calls the symbol, the platform interface must define it", so
            with zero call sites it passes vacuously.
  relation: it is one of the comments this round corrected, which is how it was noticed.
  why not fixed: it degrades correctly and re-arms by itself if a client revision calls the
            symbol again. Deleting the guard would remove a working safety net; changing it
            would be new work on a path that does not affect Apple pin or release
            correctness. Recorded here instead.
```

No other production bug was found. Nothing here blocks the Apple release or the pin.

---

## 9. Verdict

The two gaps this task existed to close are closed and proven, and the remediation round
additionally made the surrounding gate suite honest:

- **Submodule reachability** — from an uninitialised `clients/apple`, a fresh
  `git submodule update --init` checks out exactly `90a0e9ae…`, and `git submodule status`
  reports no drift.
- **Frozen SHA reachability** — `90a0e9ae…` is an ancestor of `origin/main` (`exit 0`) and
  is advertised by the live remote as `refs/heads/main`.
- **Identity** — `gitlink == VERIFIED_APPLE_SHA == submodule HEAD == origin/main`.
- **Gates** — 25 / 25 pass; `AUTHORITATIVE_STALE_PINS = 0`.

`APPLE_PIN_VERIFICATION_COMPLETE = YES`.

The Apple repository was **not** re-opened: the branding conflict was a parent-side
contract error, and correcting it there is what keeps `90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07`
the final frozen Apple SHA.
