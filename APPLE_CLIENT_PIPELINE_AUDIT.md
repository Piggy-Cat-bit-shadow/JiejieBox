# APPLE_CLIENT_PIPELINE_AUDIT

```text
APPLE_PIPELINE_VERIFIED = NO - no full Apple client SUCCESS exists at the current HEAD
FULL_APPLE_E2E          = NOT RUN
REAL FAILURE_FOUND      = YES, identified from a real run (§3)
PRODUCT_CODE_CHANGED    = NO
```

Per §1596 of the brief: without a real full Apple client SUCCESS at the current HEAD, the task is not
complete. That is stated up front rather than implied by the length of what follows.

---

## 1. Baseline, refreshed

```text
HEAD                               9a78eabde (and moving with each report commit)
Apple UI repository                Piggy-Cat-bit-shadow/sing-box-for-apple
APPLE_CLIENT_BRANCH                main
APPLE_CLIENT_REPOSITORY            resolved in release/apple-client-refs.env
```

The last full Apple success is **#73 at `9f5adf61d`**, and the brief is right that it cannot be used as
evidence: `client-apple.yml` and `apple-client-source.sh` have both changed substantially since, and
the source-authority model itself has changed. Nothing below cites #73.

---

## 2. The DAG as it actually is now

```text
resolve-apple-source            (ubuntu-latest; the ONLY place a branch tip is resolved)
   │  outputs: apple_client_sha, apple_client_repository, apple_client_branch,
   │           apple_client_ref, apple_client_source_mode
   ├── libbox    needs: resolve-apple-source
   │      checkout(tag) ─ setup-go ─ setup-xcode ─ gomobile ─ Apple regression tests
   │      ─ verify submodule revision ─ warm cache ─ signing preflight
   │      ─ build Libbox ─ pack ─ upload jiejiebox-libbox-<sha>
   ├── ios       needs: [resolve-apple-source, libbox]
   │      checkout(submodules: recursive) ─ select source ─ verify submodule
   │      ─ download Libbox ─ validate+install ─ build unsigned IPA ─ verify ─ BUILD-INFO ─ upload
   └── macos     needs: [resolve-apple-source, libbox]
          checkout(submodules: false) ─ select source ─ checkout macos source
          ─ verify ─ download Libbox ─ validate+install ─ overlays
          ─ build unsigned DMG ─ verify ─ BUILD-INFO ─ upload
```

`ios` and `macos` remain siblings. Both edges into them are real data dependencies — a published SHA
and a downloaded artifact — and neither is a false serialisation.

**Verified real (run #80, `388e51f75`)**: `resolve-apple-source` = **success**. That is the first
production execution of the workflow-wide resolve-once change, so the resolver job, its outputs and
the seeding path all work on a runner. That part is no longer only source-verified.

---

## 3. THE REAL FAILURE, from run #80

```text
workflow = Apple client    run = 38435494422    number = #80    SHA = 388e51f75
resolve-apple-source   success
libbox                 failure   at step "Apple regression tests"
ios / macos            skipped
```

The failing step runs several suites and exits on the first real failure:

```text
FAIL: could not apply the overlay; skipping its assertions
FAIL: could not apply the overlay; skipping branding assertions
test-apple-signing: 103 passed, 2 failed
##[error]Process completed with exit code 1.
```

**So the first failing thing is a test-suite fixture problem, not a product compile.** Per §1597 the
audit starts from the first failing step, and the first failing step is a suite that could not build
the overlay fixture it needs.

Two independent causes were already identified for this surface in earlier rounds:

```text
1  test-apple-signing.sh cannot apply the overlay on the runner, so it skips the entitlement and
   branding assertions - a fixture/preparation failure inside the suite, not a product failure.
   One such report pre-dates this task's Apple work, which is why it is not attributed to it.

2  check-apple-source-selection.sh is red BY DESIGN: its ~22 assertions and its fixture premise are
   built on the gitlink model that the source-authority change removed. Its rewrite is outstanding.
```

**Neither is a compile error, and neither is repaired here.** Both are test-side; changing them is the
`§1591` decision, and doing it without a subsequent full E2E run would leave the change unverified,
which is the state this audit is trying to end.

---

## 4. Gate inventory

| gate | where | protects | layer | verdict |
|---|---|---|---|---|
| resolve-once | `resolve-apple-source` job | two UI commits in one release | workflow | correct, and now proven on a runner |
| `assert-shared-source` | ios + macos | the same | job | correct |
| submodule revision | libbox + ios | built tree == selected tree | job | corrected this round (was a gitlink equality) |
| `test-apple-signing.sh` | libbox | no signing material; CI stays unsigned | libbox | **failing on a fixture** |
| `check-apple-source-selection.sh` | libbox | source selection | libbox | **red by design, rewrite outstanding** |
| Libbox manifest `--parent-sha` | ios + macos | the framework matches the core commit | consumer | correct |
| `check-apple-shared-libbox.sh` | release + publish | both trees link one Libbox | release | correct |
| naming contract | `test-apple-macos-name.sh` | SFM vs SFI identity | libbox + macos | correct |
| `verify-apple-signed-artifact.sh` | ios + macos | signed artifact matches profiles | consumer | SKIPs in unsigned mode, correctly |
| `apple-abi-probe.sh` / provenance | standalone | Swift/C witnesses vs the built xcframework | off-path | **no caller** - recorded, not wired |

### The naming contract, formally

```text
artifact            jiejiebox-libbox-<core sha>              upload (libbox)
                     jiejiebox-windows-unpacked-audit-input   upload (desktop, matches no release pattern)
consumer installs   via apple-libbox-artifact.sh install|validate, bound by --parent-sha
release             never publishes a Libbox artifact; it is a build input, not a deliverable
```

The Libbox artifact binds to the **core** commit and deliberately records **no** Apple revision. That
is correct and separately asserted: Libbox depends on the core, not on which UI links it. The Apple
revision belongs in the app's own BUILD-INFO, which is where §2 of the brief puts it.

---

## 5. Structural observations, recorded not acted on

```text
RFC / OBSERVATION
```

1. **`apple-abi-probe.sh` and `apple-abi-provenance.sh` have no caller.** They compile migrated Swift/C
   witnesses against a really-built `Libbox.xcframework`, which is a strong check — and nothing runs
   them. Wiring them is a real improvement and a real new failure surface, so it is a decision, not a
   cleanup.

2. **`test-apple-signing.sh` mixes two jobs**: it asserts signing architecture *and* parses fourteen
   Apple scripts. A fixture problem in the first currently fails the whole step, which is why a
   preparation failure surfaced as a signing-gate failure. Splitting the parser check from the signing
   assertions would make the failure legible without weakening either.

3. **The suite is the first thing `libbox` runs**, before the submodule revision is verified and before
   the cache is warmed. A test-side failure therefore blocks the build before the build has been given a
   chance, which is how a fixture problem came to look like a pipeline blocker.

---

## 6. What is NOT done

```text
full Apple client E2E at the current HEAD       NOT RUN
inspection of a real IPA / DMG                  NOT RUN - needs the E2E first
check-apple-source-selection.sh rewrite         NOT DONE - ~22 assertions + the fixture premise
the overlay fixture failure                     NOT FIXED - identified, one cause pre-existing
routing the census diagnostic                   not applicable - that is the macOS prompt
```

**Why the E2E is not claimed**: the brief requires the *current HEAD* to reach real SUCCESS. The last
attempt ran at `388e51f75` and stopped in `libbox` at a test fixture. Running it again without
addressing that fixture would produce the same stop, and reporting "E2E attempted" as progress would be
the substitution of activity for verification that §1559 warns against.

**The single next action that moves this**: rewrite `check-apple-source-selection.sh` for the
source-authority model and fix the overlay fixture, then dispatch `client-apple.yml` once. Everything
else in the chain — resolver, Libbox artifact contract, consumer install, naming contract — is either
already verified or unchanged.
