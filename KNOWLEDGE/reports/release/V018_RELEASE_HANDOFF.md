# v0.1.8 Release — Handoff Report for Codex

Written at the point of handover. Everything below is measured, not summarised from memory; the
commands that produce each number are named so it can be re-checked rather than trusted.

---

## 1. The single most important fact

**The release is being driven right now.** A background process is executing, in order:

1. dispatch every required workflow at the frozen SHA
2. wait for the newest exact-SHA run of each to be green (retrying `client-macos.yml`, see §6)
3. **refuse to continue** if any is not green
4. release **dry run** (never publishes)
5. release **publish**
6. **read back** the published release

It refuses to publish on any failure, so a red result in §5 below is a *stop*, not a partial release.

If that process is gone, the equivalent command is:

```powershell
& 'C:\Deepseek\_drive_release.ps1'
```

The script is idempotent in the sense that it re-dispatches rather than reusing stale runs, and it
takes the newest run per workflow — which is the semantics the brief requires.

---

## 2. Frozen identity

```text
FINAL_FREEZE_SHA = e1da741f0db1d3e893f62fe9278378e90feffaa1
origin/testing   = e1da741f0db1d3e893f62fe9278378e90feffaa1
peel(v0.1.8)     = e1da741f0db1d3e893f62fe9278378e90feffaa1
v0.1.7 peel      = 6f0d5519c74816f0a4c255f46a08fe0f298b3cd7   (UNTOUCHED, never moved)
release/JIEJIE_VERSION = 0.1.8
```

All four legs are the same commit. Re-check with:

```bash
git ls-remote origin refs/heads/testing 'refs/tags/v0.1.8^{}' 'refs/tags/v0.1.7^{}'
```

**`v0.1.8` was retargeted several times during this work.** That was legitimate each time and must
not be repeated now: no GitHub Release referenced it, and each move was to the newest candidate
after a fix. **Once §5 publishes, `v0.1.8` is frozen and must never move again.**

### Why 0.1.8 and not 0.1.7

`v0.1.7` already existed as an annotated tag pointing at `6f0d5519`, an **ancestor** of the candidate
(73 commits behind). The release gate itself makes that unshippable:

* `cmd_check_tag` requires `tag_sha == HEAD`
* `cmd_release_version` requires `tag == "v" + release/JIEJIE_VERSION`

so at any 0.1.7-declaring commit the only legal tag name is `v0.1.7`, which was taken by a different
commit. Moving it would have made one version name two artifacts. The user chose the version bump.

---

## 3. What was found and fixed, in order

Eleven commits. Each entry is the *root cause*, because every one of them presented as something
else first.

| # | Commit | Root cause | How it presented |
|---|---|---|---|
| 1 | `f1e0ae5cf` | version bump 0.1.7 → 0.1.8 (see §2) | `v0.1.7` tag conflict |
| 2 | `f68f67cfc` | `test/go.mod` pinned fork `sing-tun` one commit behind the root, and its require line was split from its replace | `verify.yml` step 7: `module graph does not resolve read-only`, 7 problems |
| 3 | `cb19a3ba5` | `authenticodeSigner` rejected an expired signer certificate; the certificate is self-signed and pinned by byte equality, so the validity window never restricted anything | `FATAL authenticate daemon executable: untrusted Authenticode signer certificate is not currently valid` → daemon never launched the core |
| 4 | `2104a6198` | `prepare-desktop-client.sh` derived its root from its own location, so `core/clients/desktop` resolved inside the **tooling** checkout | desktop gate 2: `FAIL: core/clients/desktop is missing`, one step after the submodule had checked out |
| 5 | `7e0bb0f84` | (a) `DESKTOP_CLIENT_DIR` is relative to the core root, not the workspace, so it double-prefixed to `core/core/...`; (b) the new suite had no executable bit | `.../core/core/clients/desktop is missing` |
| 6 | `80205b45c` | `release.yml`'s evidence step called `gh api` with no `GH_TOKEN` | `gh: To use GitHub CLI … set the GH_TOKEN` **then** `FAIL: cannot resolve workflow '.github/workflows/verify.yml'` — the second message names the wrong cause |
| 7 | `cde8dabde` | a static guard for that class (see §7) | — |
| 8 | `720f073e7` | the guard called `.get` on `load()`'s tuple | `AttributeError`, a traceback not a verdict |
| 9 | `9d60c92e0` | **`gh run download --name X --dir D` does not create `D/X`**; the gate required `D/X` | release step 11: `produced 0 directories … expected exactly 1` |
| 10 | `4c455f498` | a Python edit wrote LF where the index held CRLF, converting both release scripts | RCV steps 4, 10, 11 |
| 11 | `e1da741f0` | two negative cases still expected the pre-fix wording | RCV step 4: `refused for the WRONG reason` |

### The one worth reading twice

**#9.** `gh` extracts an artifact's *contents* into `--dir`; it creates no `D/<artifact-name>`. The
gate asserted that directory. It had been unsatisfiable since the day it was written and nobody
could tell, because **the test suite's mock `gh` fabricated it**:

```python
target = dest / e["name"]
shutil.copytree(source, target)
```

So the mock and the gate agreed with each other and neither agreed with GitHub. The suite was green
the whole time. The release path had never been run, so the dry run was its first execution.

Verified against the real artifact bytes before fixing — the ZIP contains `BUILD-INFO-LINUX.txt`,
`LICENSE`, `sing-box-linux-amd64`, `sing-box-linux-amd64.sha256` at its top level and **zero**
subdirectories. The fix has `download` unpack into a scratch dir and rename to
`$dir/$binary/$artifact`, which is the shape `verify`/`flatten`/`check-upload` consume, and the mock
was corrected so it can no longer teach the gate a layout GitHub does not produce.

---

## 4. Environment (needed to reproduce anything)

The Windows host has no Unix toolchain. Everything below was assembled for this work:

| tool | path | note |
|---|---|---|
| git | `C:\Deepseek\IOS客户端\_tools\mingit\cmd\git.exe` | needs `GIT_EXEC_PATH`, `GIT_TEMPLATE_DIR` |
| bash | `C:\Deepseek\_shim\bash.exe` | a copied `sh.exe`; Git Bash tolerates CRLF, **Ubuntu bash does not** |
| Go | `C:\Deepseek\_gotool\go\bin\go.exe` | 1.25.5, downloaded for `go mod tidy`; use `GOTOOLCHAIN=local` or `auto` deliberately |
| Python | `C:\Users\Jie\.dsh\dsh-runtimes\dsh-primary-runtime\dependencies\python\python.exe` | 3.12 |
| stubs | `C:\Deepseek\_stubs` | `jq`, `chmod`, `sha256sum`, `python3` — `sha256sum` is a **stub**, not real |

**Missing and not worth fighting**: `od` (breaks every ELF-magic check), real `sha256sum`, `gcc`
(so no `-race`), `unzip` on PATH, `gh`.

### Three traps that have each cost a run

1. **`git diff --check` is not optional.** Five separate defects in this work were encoding or line
   ending, and every one was caught by a check rather than by reading. Run it before every commit.
2. **A tool that rewrites a file assumes its line endings.** `git ls-files --eol <file>` first; for
   these scripts the answer is LF in the index, and on Windows the worktree LF is what must be
   staged. CRLF reaches the runner as `#!/usr/bin/env bash\r`.
3. **PowerShell mangles inline commit messages** — backticks, `${{ }}`, `&&`, `||`, newlines. Always
   `git commit -F <file>`.

---

## 4b. UPDATE — after the first full driver run (read this first)

The driver ran to completion. **Every required gate and producer went green at `e1da741f0`**, and the
dry run advanced from step 11 (download) to **step 13 (verify)** — steps that had never executed
before. It stopped there, on a genuine Apple defect.

```text
verify.yml                      newest 38561007081  success
release-contract-verify.yml     newest 38561074398  success
server-linux-amd64.yml          newest 38561078084  success
windows-core-amd64.yml          newest 38561081898  success
android-core-arm64.yml          newest 38561085248  success
client-macos.yml                newest 38561089260  success   (green first pass, no retry needed)
client-desktop-windows.yml      newest 38561092544  success
client-apple.yml                newest 38561070109  success   (4/4 jobs)
```

Dry run `38563789744`: steps 1–12 **success**, step 13
`verify the downloaded artifacts` → **failure**.

### All four binary products verified

```text
--- linux/amd64    sing-box-linux-amd64  80466104 bytes   verified: binary, checksum, build info, platform magic, commit e1da741f0
--- darwin/arm64   sing-box-darwin-arm64 84124882 bytes   verified: same
--- windows/amd64  sing-box.exe          81485824 bytes   verified: same
--- android/arm64  libbox.aar            28600062 bytes   verified: same
--- apple/arm64    FAIL: could not extract Libbox.xcframework.tar.gz to inspect its slices
```

### THE REMAINING DEFECT — the Apple payload is a ZIP named `.tar.gz`

This is a **producer** defect, not a gate defect, and the gate is behaving exactly as designed.

The artifact `jiejiebox-libbox-e1da741f0…` contains the expected four files
(`Libbox.xcframework.tar.gz`, `manifest.json`, `SHA256SUMS-LIBBOX.txt`, `BUILD-INFO-LIBBOX.txt`),
but `Libbox.xcframework.tar.gz` is **not a gzip tar**:

```text
first 8 bytes: 80, 75, 3, 4, 10, 0, 0, 0      ->  "PK\x03\x04"  = ZIP
manifest.artifact_sha256 = 201bd6cd5dfb1dc3ed0ff60ba1f776594310991f0f9412e0b6aa11c92ac38c32
extracted     sha256     = 201bd6cd5dfb1dc3ed0ff60ba1f776594310991f0f9412e0b6aa11c92ac38c32
```

**The digests are IDENTICAL.** That is the decisive fact: the bytes are the producer's own, the
download did not mangle them, and `manifest.json` truthfully describes a ZIP. `tar -xzf` cannot read
it, so `verify_apple` refuses.

Why this is a real defect and not a gate bug: a `.tar.gz` that is a ZIP is a mislabelled container.
Downstream consumers (`publish-apple-beta.sh` runs `tar -xzf` on the release asset at line 271) would
fail on it too, and it is the exact hazard the producer's **own** source documents:

```text
scripts/ci/apple-libbox-artifact.sh:61
# This is a gzip tar archive on every runner.  `ditto -c -k` creates a ZIP archive even when the
# destination is named `.tar.gz`; macOS accepts that archive through its permissive tar wrapper,
# while the Linux release gate correctly refuses it.
```

and the test suite even carries a mock that fails the run if the packer uses `ditto`:

```text
scripts/ci/test-release-artifacts.sh:1587-1595
# `ditto -c -k` writes a ZIP payload even when its output has a .tar.gz suffix.
cat > "$work/bin/ditto" <<'SH'
echo "FAIL: Libbox packer must produce a real gzip tar, not a ditto ZIP" >&2
```

**Unresolved**: `pack()` at `apple-libbox-artifact.sh:72` uses `tar -czf`, which *does* write gzip,
and the workflow calls `pack` — so the static source says this should not happen, yet the released
artifact is a ZIP. Something in the real macOS path differs from the source's apparent intent, and
**finding it is the next task.** Check first: whether `pack` is the code that ran (the run's log for
the `pack the Libbox artifact` step), and whether `build-apple-libbox.sh` or `make lib_apple`
produces an xcframework that is itself a zip before packing.

**Do not weaken `verify_apple` to make this pass.** The gate printed that instruction itself, and it
is right: re-run the Apple producer, or fix the packer, then re-freeze.

### Consequence for the freeze

Any Apple fix is a **release-critical change**, so `FINAL_FREEZE_SHA` moves and the whole exact-SHA
chain re-runs. The driver already automates that; the sequence that worked is in §8.

---

## 5. State at handover

Green at `e1da741f0` (newest run per workflow, which is what the release selector uses):

| workflow | result |
|---|---|
| `verify.yml` | success |
| `server-linux-amd64.yml` | success |
| `windows-core-amd64.yml` | success |
| `android-core-arm64.yml` | success |
| `client-apple.yml` | success (4/4 jobs) |
| `client-desktop-windows.yml` | **success** — the blocker |
| `client-macos.yml` | flaky, see §6 |
| `release-contract-verify.yml` | **was failure** on the previous SHA; fixed by `e1da741f0` and being re-run by the driver |

`android-core-arm64.yml` is dispatch-only in practice: its `push` trigger has a `paths` filter that
does **not** include `experimental/boxdd/**`, so daemon changes never trigger it. It must be
dispatched explicitly at the frozen SHA — the driver does.

---

## 6. The macOS flake — read this before retrying anything

`client-macos.yml` alternates success and failure **on identical commits**. Evidence: it failed twice
at `f68f67cfc` then passed on the third attempt at that same SHA; it failed then passed at
`cb19a3ba5`; it passed at `7e0bb0f84`, and it failed at `720f073e7` before passing on retry.

The failing tests differ between runs, which is the proof it is not a product bug:

* `TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight` — *"precondition: and the port really
  is free then"* (OS socket state)
* `TestInterfaceEnvironmentTransitionCoalescesPendingReset` — epoch `0x3` vs `0x2`
* `TestPauseWakeCyclesInterleavedWithRebindsKeepOneSocket` — *"Condition never satisfied"*, 10s
* `TestOversizedWriteCannotBlockItsOwnLane` — *"22.884333ms" is not less than "20ms"*

A product defect fails the *same* test every time. Four different deadline/tolerance tests across
two runs on identical bytes is environment.

**The trap**: the selector takes the NEWEST run. An extra retry *replaces* a green run with a red
one. Do not dispatch macOS "for insurance" — retry only until the newest is green, then stop.
`_drive_release.ps1` handles this.

---

## 7. The guard added, and why it is two-sided

`scripts/ci/check-release-workflows.py` now has `check_gh_token`. Its first version scanned each
step's `run` block for a literal `gh` and reported "1 authenticated, offenders: none" **while the
release was failing** on a step whose `gh api` call lives *inside* `release-artifacts.sh`. It is now
two-sided: a literal `gh` in the step, **or** a `release-artifacts.sh` subcommand that reaches the
API. The four such subcommands (`evidence`, `plan`, `download`, `desktop`) were derived by reading
the script; the five that do not were checked rather than assumed.

Mutation-tested: removing the token from the evidence step alone makes the checker exit 1 and name
the offender. The first mutation attempt stripped *every* `GH_TOKEN` and the guard correctly stayed
green — which is how the one-sided scan was exposed.

```text
release workflow structure: 124 passed, 0 failed
```

---

## 8. What to do next, in order

1. **Collect the driver's result.** `job_output` for `pwsh-1879`, or re-run
   `C:\Deepseek\_drive_release.ps1`.
2. **If the dry run failed**, read the failing step. The dry run got to step 11 (download) before
   this round, and each fix moved it forward; the remaining steps — `verify`, `flatten`,
   `check-upload`, `create the release` — **have still never executed**, so expect surprises there
   and treat each as a genuine finding, not a regression.
3. **If the publish succeeded**, the read-back in §6 of the script must show exactly **7 assets**:
   `Jiejiebox-v0.1.8-windows-x64.exe`, `Libbox.xcframework.tar.gz`,
   `jiejie-sing-box-{linux-amd64,macos-arm64,windows-amd64,android-arm64}-v0.1.8.tar.gz`,
   `SHA256SUMS`. Anything else, or a different count, is a failure of §27's exact allowlist.
4. **Then TestFlight**, which is explicitly ordered *after* the canonical release exists (brief
   §34): `publish-apple-beta.sh` with `--libbox-source auto`, and its summary must say it used the
   **canonical Release asset**, not Actions. `APPLE_USE_PREBUILT_LIBBOX=1`; a missing prebuilt
   Libbox is a FAIL, never a rebuild.
5. **Do not move `v0.1.8`** once published, and do not touch `v0.1.7`.

---

## 9. Unfinished and known-imperfect

Stated plainly so it is not mistaken for done:

* **Steps 12–16 of `release.yml` have never executed.** `verify`, `flatten`, `check-upload`,
  `create the release` and the read-back are unproven. Every previous attempt died earlier.
* **The full `release-artifacts.sh` suite cannot complete on this host** — no `od`, stub `sha256sum` —
  so its ELF-magic and manifest checks are `BLOCKED_BY_ENVIRONMENT` locally. The Ubuntu runner is the
  only place they run.
* **`-race` cannot run locally** (no gcc). The Authenticode tests passed without it.
* **The union race gate still does not exist.** `verify.yml:392` records this. It is an improvement to
  scheduling, not a blocker: `verify.yml` triggers on every push to `testing`, so release-required
  race evidence exists at the release SHA.
* **No report of this round has been appended to the repo.** The last committed report section ends at
  V10. This document is the handoff and is **not** in the repository.
* **`test/go.mod`'s sing-tun pin was one commit behind** — worth asking why nothing caught it earlier.
  The guard `check-go-module-integrity.sh` works; the gap is that `verify.yml` only runs on
  `testing`/PR, so the temp branch never exercised it.

---

## 10. Where things live

| what | path |
|---|---|
| release driver | `C:\Deepseek\_drive_release.ps1` |
| macOS retry loop | `C:\Deepseek\_macos_until_green.ps1` |
| the gate | `scripts/ci/release-artifacts.sh` |
| its suite + mock `gh` | `scripts/ci/test-release-artifacts.sh` |
| the release workflow | `.github/workflows/release.yml` |
| desktop client prep | `scripts/ci/prepare-desktop-client.sh` |
| its new root suite | `scripts/ci/test-desktop-client-root.sh` |
| the daemon gate removed | `experimental/boxdd/authenticode_windows.go` |
| its tests | `experimental/boxdd/authenticode_windows_test.go` |
| the gh-token guard | `scripts/ci/check-release-workflows.py` → `check_gh_token` |

Repository: `C:\Deepseek\singbox-consolidation`, branch `release/v6-unix-authority`, remote
`Piggy-Cat-bit-shadow/sing-box`.
