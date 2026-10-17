# Phase 5 — FINAL release close-out: HELD at the pre-publication gate

```text
RELEASE = FAILED (HELD — nothing published, tag exists)

VERSION              = 0.1.11
TAG                  = v0.1.11
FINAL_RELEASE_SHA    = c781d96c635b12962a175e7f6f5a334adab27f11
TAG_SHA              = c781d96c635b12962a175e7f6f5a334adab27f11
TAG_SHA_MATCH        = YES (verified against the remote peeled ref)

PHASE4_VERIFIED_PREDECESSOR = aa5897c39439a878bfd2ec679e745402ecd1cf72
PHASE4_REPORT_HEAD          = e8e80990c87f3df2c82faa3dd89ccfca9fb2b71e
VERSION_COMMIT              = c781d96c635b12962a175e7f6f5a334adab27f11

ALL_REQUIRED_RUNS_EXACT_SHA = YES
ALL_REQUIRED_RUNS_GREEN     = YES (11 runs, all green at FINAL_RELEASE_SHA)

DRY_RUN                     = PASS   (run 38837824911)
DRY_RUN_PRODUCT_SHA         = c781d96c635b12962a175e7f6f5a334adab27f11
DRY_RUN_TOOLING_SHA         = c781d96c635b12962a175e7f6f5a334adab27f11
DRY_RUN_IDENTITY_MATCH      = YES

FORMAL_RELEASE_RUN          = NOT DISPATCHED — see the blocker
FORMAL_PUBLISH              = NOT ATTEMPTED
RELEASE_ID / RELEASE_URL    = none (no release, no draft exists)
PUBLIC_READBACK             = NOT APPLICABLE (nothing published)

WORKTREE_CLEAN              = YES
BRANCH_CLEANUP              = DEFERRED (publication did not complete)
```

**The release is held deliberately. Everything up to publication is proven; one
acceptance gate is deterministically red, and publishing over it would ship a known
failure.**

---

## The blocker, precisely

```text
WINDOWS_RUNTIME_AUDIT = FAIL (deterministic, reproduced)

run 38837786208  gate 6  failure
run 38839399129  gate 6  failure   <- re-dispatched to test flakiness; identical
run 38617365367  gate 6  success   <- the PREVIOUS release's installer (v0.1.10)
```

`[gate 6] NSIS install / reinstall / uninstall lifecycle`, from
`scripts/ci/windows/Test-InstallerLifecycle.ps1`:

```text
installer lifecycle smoke test failed: install did not finish within 900 seconds;
in silent mode that means a prompt appeared that /S could not answer
```

### What is NOT wrong

The installation itself **succeeds**. The gate's own diagnostics, captured by its
`-DiagnosticPath` channel, show a complete install:

```text
derived install root: c:\program files\jiejiebox\sing-box
layout version:       2
service command line: "...\resources\daemon\sing-box-daemon.exe" run --working-directory C:\ProgramData\jiejiebox-daemon
uninstall registry:   DisplayName "Jiejiebox 0.1.11", DisplayVersion "0.1.11"
installed tree:       resources\daemon\* (usbip2_*, VBoxUSB*, WinDivert64.sys), native\windows_share.node, icons, licenses
process running:      sing-box-daemon  C:\Program Files\Jiejiebox\sing-box\resources\daemon\sing-box-daemon.exe
[service install] exit=0  core Authenticode signer certificate is currently valid
```

So: files staged, service registered and Running, registry entry written, payload
present and signed. **The installer process then never exits**, and the gate's own
timeout is what fails.

### Why it is a regression rather than a flake

The same gate, same runner image, same script passed on the previous release's
installer (run 38617365367, gate 6 `success`). It has now failed twice in a row on the
v0.1.11 installer with byte-identical symptoms. Flakiness is excluded by the re-run;
the variable that changed is the artifact.

### What was ruled out

* **Not the audit tooling.** `scripts/ci/windows/` is unchanged between the two runs —
  the audit checks the tooling out at the audited SHA, and every earlier gate on the
  same code path (5, 5b) passed in both runs.
* **Not the version identity.** The installer carries `0.1.11`, the registry entry
  agrees, and the audit's version resolution passed.
* **Not the dry-run.** `release.yml` staged and verified this exact installer
  (`PASS: desktop Jiejiebox-v0.1.11-windows-x64.exe verified against c781d96c… (version
  0.1.11)`), so the artifact is internally consistent. The failing gate is a *runtime*
  behaviour, which the staging verification does not exercise.
* **Not a missing prompt in source.** `git grep MessageBox -- scripts/ci/windows/`
  finds it only in the test's own comment; the blocking prompt would be in the installer
  binary, which is built from the pinned desktop client plus the core payload.

### The most probable cause, stated as a hypothesis and not as fact

Gate 5b installs the daemon as a **secure service** and leaves it Running; gate 6 then
runs the **full client installer** into the same root. The `daemon data dir` diagnostic
prints an unresolved path (`C:\Users\runneradmin\..\..\ProgramData\jiejiebox-daemon`)
where `C:\ProgramData\…` is expected, which points at path handling on the
already-installed / upgrade path. An installer waiting on a mutex, a service stop, or a
page that `/S` cannot answer, while the daemon from gate 5b is still live, is consistent
with every observed symptom. **This has not been proven** — proving it needs a Windows
runner and a debug of the installer's own exit path.

---

## What IS proven and should not be redone

Everything else in the Phase-5 chain is complete and green at `FINAL_RELEASE_SHA`.

### Version preparation and freeze

`release/JIEJIE_VERSION` 0.1.10 → 0.1.11, one file, one line. It is the fork's single
version authority — `build-server.sh`, `build-macos-client.sh`, `build-ios-testflight.sh`
and `check-desktop-source-identity.sh` all read it. No changelog commit: `docs/changelog.md`
is upstream's and no release script reads it, and the fork's releases carry empty bodies
(v0.1.10 verified). The commit sits on top of the Phase-4 report, as the brief requires —
the report was kept rather than reset away.

### Exact-SHA evidence (11 runs, all green, all at `FINAL_RELEASE_SHA`)

```text
Verify                       38834826263   Release contract verification  38834835138
Linux amd64                  38834843776   macOS arm64                    38834851744
Windows amd64 core           38834859611   Android Core ARM64             38834868086
Apple client                 38834875820   Windows daemon tests           38834912806
Reference interop (Xray)     38834891403   Mobile contract verification   38834899321
```

Products verified to carry the release identity independently of the badge:

```text
BUILD-INFO-LINUX.txt   version: 0.1.11   artifact_version: 0.1.11+jiejie.c781d96
                       git_commit: c781d96c635b12962a175e7f6f5a334adab27f11
                       go_version: go1.25.5   build_time_utc: 2026-10-16T01:31:50Z
sing-box-linux-amd64   artifact_sha256 ebc75ba41e1e218f1d5d9d0c4e291c8c29bd4c28ac299f8a6218f05641f19e74
                       recomputed locally: ebc75ba41e1e218f1d5d9d0c4e291c8c29bd4c28ac299f8a6218f05641f19e74  MATCH
Apple artifacts        jiejiebox-macos-arm64-0.1.11-c781d96c6-unsigned.dmg
                       jiejiebox-ios-arm64-0.1.11-c781d96c6-unsigned.ipa
```

### Tag

Annotated tag, matching the v0.1.10 convention (they are all annotated tags with a
`vX.Y.Z - Jiejiebox X.Y.Z. <summary>` message). Created only after the evidence was green.
Remote read-back:

```text
37ca76b30100d156e3e775c765653decf1560149    refs/tags/v0.1.11          (tag object)
c781d96c635b12962a175e7f6f5a334adab27f11    refs/tags/v0.1.11^{}       (peeled commit)
```

### Windows desktop producer — the Phase-4 blocker, RESOLVED

Run 38836711433, **34/34 steps success**, dispatched at `core_sha = FINAL_RELEASE_SHA`,
`core_version = 0.1.11`, `tooling_sha = FINAL_RELEASE_SHA`. The two gates that Phase 4
recorded as `BLOCKED_BY_PHASE_ORDERING` now pass:

```text
[gate 2] confirm the client's and the core's machine-global identity agree   success
[gate 3] verify the packaged daemon is the release core daemon              success
```

### Formal dry-run — PASS

Run 38837824911, 21 steps, **only the three publish steps skipped**
(`create the release (draft)`, `verify the draft`, `publish the draft`) — exactly the
intended missing action. Identity, from the run's own log:

```text
PRODUCT_SHA: c781d96c635b12962a175e7f6f5a334adab27f11
TOOLING_SHA: c781d96c635b12962a175e7f6f5a334adab27f11
RELEASE_TAG: v0.1.11
release/JIEJIE_VERSION :  0.1.11 (matches tag v0.1.11)
PASS: desktop Jiejiebox-v0.1.11-windows-x64.exe verified against c781d96c… (version 0.1.11)
PASS: manifest parent_sha    c781d96c635b12962a175e7f6f5a334adab27f11
```

Staged public asset set — **7 assets, and for the first time the canonical Apple bundle**:

```text
Jiejiebox-v0.1.11-windows-x64.exe                114,961,144
jiejie-libbox-apple-0.1.11.tar.gz                 70,738,162   <- canonical versioned bundle
jiejie-sing-box-android-arm64-v0.1.11.tar.gz      28,408,403
jiejie-sing-box-linux-amd64-v0.1.11.tar.gz        27,384,005
jiejie-sing-box-macos-arm64-v0.1.11.tar.gz        29,364,505
jiejie-sing-box-windows-amd64-v0.1.11.tar.gz      27,709,202
SHA256SUMS                                              640
```

> **A finding worth keeping:** v0.1.10 published the *old* Apple name
> (`Libbox.xcframework.tar.gz`). The canonical versioned-bundle contract had therefore
> **never been exercised by a real release** until this dry run, which is exactly what
> the Phase-2/4 Apple work was for. The dry run proves it end to end:
> `promoted jiejie-libbox-apple-0.1.11.tar.gz (the complete Apple bundle; inner
> Libbox.xcframework.tar.gz byte-identical to the verified artifact)`.

---

## How to close this out

1. **Diagnose the installer exit.** Run the lifecycle gate on a Windows runner with the
   installer's own diagnostics on, and capture what the process is waiting on after the
   payload is staged. The gate already writes an `installer-diagnostic.txt`; the missing
   piece is the installer's *exit* path, not its install path.
2. **Candidate directions**, in order of likelihood: the unresolved `daemon data dir`
   path on the already-installed path; an upgrade/repair branch entered because gate 5b
   left the service Running; a page whose `/S` default is not set.
3. **Then abandon this freeze properly**, per the brief's own rule: fix, commit, derive a
   NEW `FINAL_RELEASE_SHA`, re-run the full exact-SHA matrix, and move the tag under the
   project's failed-release/tag policy. Do **not** mix evidence across SHAs.
4. **Do not publish v0.1.11 as it stands.** The dry-run is green and the artifacts are
   consistent, but the runtime acceptance gate is deterministically red, and that gate
   is the one thing staging verification cannot substitute for.

If instead the decision is that gate 6's install-step expectation is wrong for this
architecture (the install demonstrably succeeds), that must be established as a *test*
defect with evidence — not assumed — because the identical gate passed on v0.1.10's
installer and the only variable is the artifact.
