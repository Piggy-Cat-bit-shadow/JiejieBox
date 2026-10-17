# ci-release re-audit at 8a008c5d02d2c7f7b24d43581af9360bfc110fa6

Audited `KNOWLEDGE/modules/ci-release.md` (468 lines) against working-tree source at HEAD.
Read-only: no repository file was written and no git state changed.

> **Concurrency note — read this before using the table.** I did not touch the repository, but
> `git status` is **not clean**: `KNOWLEDGE/INDEX.md`, `KNOWLEDGE/modules/README.md` and
> `KNOWLEDGE/modules/ci-release.md` were modified by **another writer while this audit ran**
> (mtimes 00:45:05–00:46:32). `ci-release.md` is now **537 lines**. Every "module location" reference
> below therefore cites the **468-line revision that existed when the audit started**, which is no
> longer the working-tree file. All *source* citations are unaffected — they are against HEAD
> `8a008c5`, which did not move. I did not revert or alter those three files.
>
> The concurrent 537-line revision has **already** added "The single authority for the asset list",
> "draft-first, and why the order is the safety property", and "A defect this module documented — now
> FIXED". Those three findings of mine are **already landed**; do not re-apply edits 1–4 below. Its
> §2 "Still genuinely unwired" list is also already corrected. What it has **not** touched is §5's
> "The Apple pin has no wired verifier" section (still at `:406-451`), which still asserts the
> superseded gitlink-authority model — see edits 6 and 7, which remain the highest-value corrections.
Delta recomputed: base `a267e99e0` → HEAD = **186 commits**; workflows **11** changed/added
(8 modified, 3 added); `scripts/ci/` **32** files (21 modified, 10 added, 1 deleted); plus
`release/JIEJIE_VERSION` and `release/apple-client-refs.env`. Counts match the brief.

Executed in this audit (not merely read):
`python3 scripts/ci/check-release-workflows.py` → **123 passed, 0 failed, 2 SKIP** (exit 0);
`bash scripts/ci/check-apple-source-selection.sh` → **72 passed, 0 failed** (exit 0);
`bash scripts/ci/test-release-artifacts.sh` → 29 of 108 failed, **every failure traceable to a
missing host tool** (`od: command not found`, `sleep: command not found`, `python3 -c` shim,
minimal `jq` shim) — see "Claims I could not verify".

## Verdict summary

- **claims CONFIRMED (still true):** §1 android path-filter trap; §1 no branch-protection config in
  the tree; §2 `TAGS_TEST` undefined; §3 the entire `verify-evidence/` staleness block; §3
  `check-libbox-abi.log` PARTIAL-vs-exit-0 row; §5 `verify-release-acceptance.sh` default required set
  omits `client-apple.yml`; §5 `verify.yml` runs no Apple ABI gate; §5 `check-libbox-abi.sh` unwired;
  §6 the three build profiles and the `docs/BUILD-PROFILES.md:43-44` error; §7.caution 1
  (`VERIFIED_CI` ceiling).
- **claims CORRECT (stale, must be fixed):** the whole §5 "live defect" (allowlist / die / header /
  "both platforms"); §5 step 7 "Not draft, not prerelease"; §5 "Apple IPA/DMG is still excluded";
  §5 "the refs file declares the gitlink as the sole authority"; §5 "92-case suite" and "fixtures are
  still the two-product ones"; §1 "the only automatic gate"; §1 verify.yml step table rows 1–2;
  §2 `check-macos-app-name.sh` "comment-only, never invoked"; §3 "15 modified + 1 added"; §5 the
  `check-apple-source-selection.sh` "self-referential by design" characterisation; §5 `5cab4ec5` pin.
- **behaviours ADD (present in source, absent from module):** `mobile-contract.yml`,
  `release-contract-verify.yml`, `client-desktop-windows-runtime-audit.yml` (a `workflow_run`-triggered
  automatic gate); the `evidence` subcommand and `REQUIRED_EVIDENCE`; the canonical public asset
  contract `public_asset_names()` as the single derivation; draft→verify→publish→re-verify;
  `publish-args` all-or-nothing; per-product payload directory allowlist; producer-side
  payload/evidence split for Android and Windows; Apple as a fifth release target; the
  `check-apple-source-selection.sh` rewrite for repo-tip authority; `require-executed-coverage.sh`;
  `check-release-workflows.py`; `check-apple-resolver-posix.py` (newly unwired); the canonical-asset
  vs Actions-fallback contract; `verify.yml` `paths-ignore`.
- **designs SUPERSEDED (old design documented as current):** the release-tree allowlist defect (fixed);
  `gh release create` with a hand-written asset list and immediate non-draft publication (replaced by
  draft-first + derived list + read-back); the gitlink-as-Apple-authority model (replaced by
  repository-tip resolution with `APPLE_CLIENT_REQUIRE_GITLINK` as an opt-in legacy check); the
  92-case release suite as a `verify.yml` step (moved to RCV, now 108 cases); the acceptance
  self-test as a `verify.yml` step (moved to RCV).

## Per-claim table

| module location | claim as written | verdict | current truth | evidence (file:line) |
|---|---|---|---|---|
| §1 table | `verify.yml` blocking = "**YES — the only automatic gate**" | CORRECT | Five workflows have non-dispatch triggers: verify, RCV, mobile-contract, android-core, desktop runtime audit | `verify.yml:35-79`; `release-contract-verify.yml:30-75`; `mobile-contract.yml:41-80`; `android-core-arm64.yml:29-51`; `client-desktop-windows-runtime-audit.yml:57-62` |
| §1 table | verify trigger = "`push` to `testing`, **all** `pull_request`" | CORRECT | Both triggers carry `paths-ignore: ["*.md"]`, so a root-markdown push/PR does not run it | `verify.yml:69-70`, `:76-77` |
| §1 table | 9 workflows listed; `release.yml`/Apple/macOS/Windows/Linux dispatch-only | CORRECT | 12 workflow files exist; three are missing from the table | `mobile-contract.yml`; `release-contract-verify.yml`; `client-desktop-windows-runtime-audit.yml` |
| §1 | "Required-check / branch-protection configuration is **not in the repository**" | CONFIRMED | No CODEOWNERS or protection config anywhere under `.github/` | `.github/` listing: only workflows, ISSUE_TEMPLATE, FUNDING.yml, renovate.json |
| §1 | Android `paths:` filter is a silent-coverage trap | CONFIRMED | 17 path entries, no `clients/`, no `scripts/ci/**` | `android-core-arm64.yml:29-51` |
| §1 step table row 1 | verify runs `test-release-artifacts.sh` | CORRECT | Not in verify.yml; owned by RCV | `verify.yml:112-126`; `release-contract-verify.yml:100-119`; checker `PASS source verify: no longer executes the 92-case release-helper suite` |
| §1 step table row 2 | verify runs `verify-release-acceptance-test.sh` (18 snapshots) | CORRECT | Moved to RCV; snapshots unchanged | `verify.yml:139-150`; `release-contract-verify.yml:209-232` |
| §1 step table rows 3–14 | gofmt, gomobile, module integrity, vet, tag policy, upstream assumptions, untagged+tagged build, matrix, cronet, 2 race steps | CONFIRMED | All present, same order | `verify.yml:160-183`, `:185-200`, `:202-218`, `:220-231`, `:233-242`, `:244-270`, `:272-300`, `:302-331`, `:354-359`, `:361-403`, `:405-418` |
| §1 "critical gap" | no `go test` runs the root package; 25 `box_*_test.go` compiled, never executed | CONFIRMED | `-run '^$'` still compiles-only; no root-package test execution anywhere in verify.yml | `verify.yml:269` |
| §2 | `check-libbox-abi.sh` has 0 referrers | CONFIRMED | Only itself, reports, `KNOWLEDGE/`, `docs/`; no workflow, script or Makefile target | grep over workflows+`scripts/`; `check-libbox-abi.sh:53-55` (own usage) |
| §2 | "Comment-only mentions that read like wiring" → `verify-release-acceptance.sh` | CONFIRMED | Still comment-only, and now *explicitly* so | `verify.yml:92-95`; `verify.yml:149-150` "reachable from nothing in the release path"; `release-contract-verify.yml:220-222` |
| §2 | `verify-fork-handoff.sh` comment-only | CONFIRMED | Mentioned in a verify.yml comment; never invoked by any workflow | `verify.yml:51`; `verify-release-acceptance.sh:4` |
| §2 | `check-macos-app-name.sh` is a comment-only mention that nothing runs | CORRECT | It **is** wired: invoked by the macOS TestFlight builder | `build-macos-testflight.sh:210`; asserted by `test-apple-macos-name.sh:745` |
| §2 | `check-macos-client-config.sh` unwired | CONFIRMED | Only docs and one comment in a sibling script | `docs/JIEJIE-MACOS-CLIENT.md:82`; `check-macos-client-headless.sh:37` |
| §2 | `apple-abi-provenance.sh`, `apple-abi-probe.sh`, `probe-accessors.py`, `check-satelite-one-core-pin.py`, `test-apple-links.py`, `verify-cronet-ios-archive.sh`, `run-jiejie-suite.sh`, `Test-DaemonServiceRegister.ps1` unwired | CONFIRMED | No workflow/script referrer for any; `probe-accessors.py` is invoked only by the unwired `check-libbox-abi.sh` | `check-libbox-abi.sh:357`; `apple-abi-provenance.sh:120` uses the *directory* `apple-abi-probe/`, not the script |
| §2 path | `scripts/ci/acceptance/jiejie-masque-vps.sh` | CORRECT | Wrong path: the script is `scripts/acceptance/jiejie-masque-vps.sh`; no `scripts/ci/acceptance/` exists | `scripts/acceptance/jiejie-masque-vps.sh`; `scripts/ci/` subdirs are `apple-abi-probe`, `windows`, `__pycache__` |
| §2 | `TAGS_TEST` "never defined anywhere" → `make test` cannot pass | CONFIRMED | Used twice, defined nowhere in the repository | `Makefile:248`, `:254`; repo-wide grep for `TAGS_TEST=` finds nothing |
| §2 | tags `trafficresearch`, `p2ios`, `tlsfragment_integration`, `force_stdio` referenced by nothing | CONFIRMED (with a nuance) | All four still run in no declared configuration; `trafficresearch` is named by a guard test that asserts it stays tagged | `common/trafficsched/adaptive_research_test.go:1`; `common/trafficsched/adaptive_controller_guard_test.go:53`; 3 × `experimental/libbox/p2ios_*_test.go:1`; `common/tlsfragment/integration_test.go:1`; `Makefile:254` |
| §3 | every log names `### HEAD: cfc77dc5…`; rule at `README.md:5-8` | CONFIRMED | 13 `.log` files (module says 14) all name `cfc77dc51ab7322e993b8ae0b94b96db59e48f47` | `verify-evidence/*.log:1-2`; `verify-evidence/README.md:5-8` |
| §3 | "101 commits behind `a267e99`", strict ancestor | CONFIRMED | — | `git rev-list --count cfc77dc5..a267e99e0` = 101; `git merge-base --is-ancestor` exit 0 |
| §3 | that diff is "15 modified + 1 added" | CORRECT | At HEAD the same path set is **49 files: 34 modified, 14 added, 1 deleted** | `git diff --name-status cfc77dc5..HEAD -- scripts/ci .github/workflows release clients` |
| §3 | `check-libbox-abi.log` ends PARTIAL while `gates.tsv` records exit 0 | CONFIRMED | Verbatim | `verify-evidence/check-libbox-abi.log` tail; `verify-evidence/gates.tsv:2` |
| §4 step 8 | `check-libbox-abi.sh` "UNWIRED; layer 5 is now GREEN" | CONFIRMED (value stale) | Constant is `40dd4879…` and equals the gitlink; still unwired | `check-libbox-abi.sh:155`; `git ls-tree HEAD clients/apple` |
| §5 step 4 | plan selects one successful run per product for this exact SHA, 90-min timeout | CONFIRMED and extended | Same, **plus** required exact-SHA evidence gates | `release-artifacts.sh:288-314`, `:553`; `release.yml:213-242`, `:316-321` |
| §5 step 6 | per platform binary + `.sha256` + BUILD-INFO, real `sha256sum -c`, platform from header, commit must match | CONFIRMED | Unchanged, and now also magic-byte and Apple-manifest aware | `release-artifacts.sh:1083-1411`, `:1190-1236` |
| §5 step 7 | "`create the release` … `gh release create … upload/*.tar.gz` … **Not draft, not prerelease**" | CORRECT | Draft-first, list derived from `publish-args`, then draft read-back, then `--draft=false --prerelease=false`, then a second read-back | `release.yml:481-486`, `:493-504`, `:519`, `:524-527` |
| §5 "live defect" | allowlist at `:575-592` is "four names per target"; `verify` dies with `unexpected file(s) in the release tree` whenever `RELEASE_EXTRA_PLATFORMS=true` | CORRECT — **defect is FIXED** | Allowlist is now at `:1374-1382`, has an Apple branch, and is scanned **per product payload directory**; `die` is at `:1396` with a different message | `release-artifacts.sh:1372-1399` |
| §5 producer table | Windows uploads the four files **plus** ZIP, `SHA256SUMS`, `build-info.json`; Android plus `libbox.provenance`, `SHA256SUMS.txt` | CORRECT | Windows uploads **exactly four** files; Android splits payload (4) and records (2) into two artifacts | `windows-core-amd64.yml:544-580`; `android-core-arm64.yml:432-452` |
| §5 | `test-release-artifacts.sh:532-533` asserts `SHA256SUMS` has exactly two rows | CORRECT | A full-contract fixture now asserts **7 public files / 6 entries** across five products; the suite is 108 cases | `test-release-artifacts.sh:1470`, `:1519`, `:1737`, `:1880-1892`; observed in-run `PASS full contract: public file count is 7` / `SHA256SUMS has exactly 6 entries` |
| §5 | header `:63-67` still says "Windows, Android and the Apple IPA/DMG are deliberately NOT here" | CORRECT | `:79-84` says that sentence *used to* say it, and explains why it was corrected. Apple (Libbox) is now a target too | `release-artifacts.sh:79-84`, `:91-111` |
| §5 | `:594` still prints "both platforms verified" | CORRECT | `:1410` prints `verified N platform(s) …` naming each `platform/arch` | `release-artifacts.sh:1401-1410` |
| §5 | `release.yml:69` sets `RELEASE_EXTRA_PLATFORMS: true`; `release-artifacts.sh:77-85` accepts true/false | CORRECT (lines moved) | Now `release.yml:91` and `release-artifacts.sh:101-111` | same |
| §5 | `target_field` table `:110-122` carries Windows and Android rows | CONFIRMED | Now `:134-229`, and carries a fifth (Apple) target plus `kind`, `payload_artifact`, `evidence_artifact` | `release-artifacts.sh:134-229` |
| §5 "out of scope" | acceptance default set is verify/interop-xray/server-linux; `client-apple.yml` not in it | CONFIRMED | Unchanged, and `verify-release-acceptance.sh` is no longer the release authority at all | `verify-release-acceptance.sh:99-100`, `:151`; `release-artifacts.sh:555-558` |
| §5 | "`verify.yml` does not run the Apple ABI gate" | CONFIRMED | verify.yml contains no Apple/ABI/libbox step; the only Apple-ish step is cronet provenance | `verify.yml:359`; grep of verify.yml for `abi|libbox|xcframework` returns comments only |
| §5 | Apple pin: "`release/apple-client-refs.env` declares the `clients/apple` gitlink as the sole authority" | CORRECT — **model replaced** | The refs file now declares the **client repository tip** the authority and says the gitlink is explicitly *not* the build authority; `APPLE_CLIENT_REQUIRE_GITLINK=1` restores the old equality check | `release/apple-client-refs.env:33-52`; `client-apple.yml:234-292`; `apple-client-source.sh:37-42`, `:479`, `:502`; checker `PASS the gitlink is no longer a hard equality by default` |
| §5 | refs file forbidden from recording a SHA (`check-apple-source-selection.sh:124-136`) | CONFIRMED | Rule still enforced; only its stated justification is superseded | `check-apple-source-selection.sh:124-128`; observed `PASS: a refs file that puts a per-platform SHA back is refused` |
| §5 | workflow body forbidden from hardcoding a SHA (`:209-210`) | CONFIRMED | Still asserts no 40-hex literal in the ios/macos jobs | `check-apple-source-selection.sh:209-217` |
| §5 | `VERIFIED_APPLE_SHA` agrees with the gitlink at `5cab4ec5`, layer 5 green | CONFIRMED structurally, CORRECT in value | Constant and gitlink both now `40dd48795579718008518ac51af320b1d61377ac` | `check-libbox-abi.sh:155`; `git ls-tree HEAD clients/apple` |
| §5 | "nothing runs the script" (`check-libbox-abi.sh`) | CONFIRMED | No referrer | as above |
| §5 | `check-apple-source-selection.sh` "could not have caught it **by design** — every check is self-referential to the gitlink" | CORRECT | Rewritten: it now *proves the opposite* — moving or dropping the gitlink does not move the resolved source, and an explicit ref does | `check-apple-source-selection.sh:412`, `:436-442`, `:723`; observed `PASS: moving the gitlink does NOT move the iOS source` |
| §5 | `check-apple-source-selection.sh` reachability | CORRECT (unstated) | It **is** wired, and now passes | `client-apple.yml:231`; observed `72 passed, 0 failed` |
| §5 | v0.1.6 Apple revision `a267e99e0` verified absent from every tracked non-`.git` file | CORRECT (narrowly) | It now appears in tracked files — a workflow comment about the desktop installer, and in `KNOWLEDGE/` | `.github/workflows/client-desktop-windows.yml:69`; `KNOWLEDGE/BASELINE.md:25` |
| §5 refs guard | `apple-client-source.sh` tested env not file; `refs_declares_platform_source()` replaced it | CONFIRMED | Present and exercised | observed `PASS: a missing refs file is refused`, `PASS: sha macos refuses the planted per-platform source` |
| §6 | three profiles; `OTHERS` = `DEFAULT` minus `with_naive_outbound`; Windows = `DEFAULT` + `with_purego` | CONFIRMED | Exactly one tag differs between DEFAULT and OTHERS; WINDOWS adds `with_purego` | `release/DEFAULT_BUILD_TAGS{,_OTHERS,_WINDOWS}` |
| §6 | `docs/BUILD-PROFILES.md:43-44` falsely says the client profile adds `with_utls` | CONFIRMED | `with_utls` is in **both** files | `release/DEFAULT_BUILD_TAGS`; `release/DEFAULT_BUILD_TAGS_OTHERS`; `docs/BUILD-PROFILES.md:43-44` |
| §6 | `verify.yml:214-215` states `DEFAULT_BUILD_TAGS` is deliberately unused | CONFIRMED (lines moved) | Now `verify.yml:277-279` | `verify.yml:277-279` |
| §7 caution 1 | no claim may be `VERIFIED_CI`; no workflow-run record exists in the tree | CONFIRMED | No run record, run id, or artifact digest exists as data; the only `run_id` occurrences are code that *writes* one into a manifest at publish time | grep of `verify-evidence/**`, `*.md`, `scripts/ci/*`, workflows for `actions/runs/<n>`, `GITHUB_RUN_ID=`, `"run_id"` — all hits are producers; `apple-libbox-artifact.sh:94`, `:206` |
| §7 caution 9 | do not describe `release.yml` as covering all platforms | CONFIRMED and sharpened | It covers five targets under `RELEASE_EXTRA_PLATFORMS=true`, incl. Apple Libbox; the IPA/DMG is still not a release asset | `release-artifacts.sh:101-111`, `:163-197` |

## Per-topic verdicts

| topic | verdict | current behaviour | owner (workflow/script) | module coverage | evidence |
|---|---|---|---|---|---|
| workflow DAG ownership | CHANGED | 12 workflows. Automatic: verify, RCV, mobile-contract, android-core (all push-with-filter), desktop runtime audit (`workflow_run`). Manual: release, Apple, macOS, desktop build, Linux, Windows core, interop, runtime audit dispatch | — | lists 9; omits 3; calls verify "the only automatic gate" | `verify.yml:35-79`; `release-contract-verify.yml:30-75`; `mobile-contract.yml:41-80`; `android-core-arm64.yml:29-51`; `client-desktop-windows-runtime-audit.yml:57-62` |
| Verify responsibility | NARROWED | Source gate: format, toolchain single-source, module integrity, vet, tag policy, upstream tripwires, untagged/tagged build, matrix, cronet provenance, 2 race legs. No release-helper suite, no acceptance self-test, no mobile contract | `verify.yml` | table rows 1–2 wrong | `verify.yml:112-150`, `:361-418` |
| Release Contract Verify responsibility | NEW | Owns release-tooling behaviour: 108-case helper suite, canonical-source PRESENT_INVALID suite, acceptance self-test, desktop version/source-identity suites, `bash -n` of release scripts, workflow structure, desktop client root, mock-`gh` assertion. Read-only (`contents: read`) | `release-contract-verify.yml` | absent | `release-contract-verify.yml:76-77`, `:100-293` |
| Release responsibility | NARROWED | Consumes evidence, never regenerates it: identity binding, exact-SHA evidence, tag+version, publish flag, plan, download, desktop staging, verify, flatten, check-upload, draft create, draft read-back, publish, public read-back | `release.yml` | §5 design list is the superseded version | `release.yml:150-527` |
| duplicate gate removal | YES | The 108-case suite ran 3× per release; now runs once in RCV and Release *requires* RCV's exact-SHA success instead of re-running it. Acceptance self-test moved out of verify to stop verify declaring a gate the release path never called | RCV + Release | not documented | `release.yml:190-212`; `verify.yml:112-126`, `:139-150`; `release-contract-verify.yml:209-222` |
| single source of release truth | YES | `release/JIEJIE_VERSION` in the tagged commit; cross-checked `tag == v<version>`; passed down via step outputs | `release-artifacts.sh cmd_release_version` | not documented | `release-artifacts.sh:373-411`; `release.yml:266-283`; `check-desktop-source-identity.sh:161` |
| artifact selection | IDENTITY, NOT SEARCH | Each target declares `payload_artifact` = `<stem>-<sha>`; `download` fetches by exact name; `find -name` replaced by plan-derived paths | `release-artifacts.sh:199-215`, `:236-241`, `:690-834` | not documented | `release-artifacts.sh:199-215`; `release.yml:326-345` |
| exact-SHA provenance | YES | Workflow resolved by API and asserted `path`+`active`; runs filtered `head_sha=<sha>` server-side; run's own `head_sha`/`workflow_id` re-asserted; newest exact-SHA run wins, never an older green | `release-artifacts.sh:269-314`, `:553` | partially | `release-artifacts.sh:269-280`, `:288-314`, `:624-629` |
| artifact allowlist | FIXED | Per product payload directory, `-maxdepth 1`, derived from the target table; Apple contributes its own four names; `die` message is now "unexpected file(s) in a product payload directory" | `release-artifacts.sh:1360-1399` | describes the defect as live | `release-artifacts.sh:1372-1399` |
| checksum validation | YES | `sha256sum -c` on each platform's `.sha256`; `SHA256SUMS` proven with the same command a downloader runs; manifest entry count pinned to the product count | `release-artifacts.sh:1083+`, `:1580-1588` | §5 step 6 | `release-artifacts.sh:1580-1588` |
| read-back validation | YES, TWICE, BY NAME | Draft read-back and public read-back both compare the API asset list **by name** against `public_asset_names`; a count-only check is explicitly rejected as insufficient | `release-artifacts.sh cmd_verify_public_release` | not documented | `release-artifacts.sh:1758-1793`; `release.yml:493-504`, `:521-527` |
| draft-first publishing | YES | `gh release create --draft`, verify, `gh release edit --draft=false --prerelease=false`, verify again. `concurrency` keyed on the tag with `cancel-in-progress: false` | `release.yml` | documents the opposite | `release.yml:76-78`, `:481-486`, `:519` |
| tag / release identity | YES | Tag shape checked, resolved, and proven equal to the checked-out commit; tooling pinned to the tag so `PRODUCT_SHA == TOOLING_SHA` is asserted at run time | `release-artifacts.sh cmd_check_tag`; `release.yml:150-188` | partially | `release.yml:114-188`; `release-artifacts.sh:327-380` |
| release asset completeness | YES | 7 public files for a release: 4 product tarballs + `Libbox.xcframework.tar.gz` + installer + `SHA256SUMS`; each exactly once; `SHA256SUMS` must name exactly the 6 binaries | `release-artifacts.sh:1671-1681`, `:1535-1599` | not documented | `release-artifacts.sh:1523-1533`, `:1585-1599`; `publish-args` refuses a short list at `:1746-1750` |
| Apple artifact source authority | **REPLACED** | Authority is `APPLE_CLIENT_REPOSITORY` tip of `APPLE_CLIENT_BRANCH`, resolved once per run to an exact SHA, published as a job output, consumed by libbox/ios/macos and seeded into the resolver state file; the gitlink is a local convenience only | `client-apple.yml:81-105`, `apple-client-source.sh` | module states the gitlink model as current | `release/apple-client-refs.env:33-52`; `client-apple.yml:234-292`; `apple-client-source.sh:37-42`, `:479`, `:502` |
| Android AAR provenance | YES | Producer writes `libbox.provenance` naming the commit, asserts it equals `GITHUB_SHA`, and publishes payload (4 files) and records (2 files) as **separate artifacts** | `android-core-arm64.yml:257-280`, `:432-452` | describes the old single-artifact shape | `android-core-arm64.yml:276-280`, `:432-452` |
| Windows artifact provenance | YES | Uploaded set is exactly the four contract files; ZIP, `SHA256SUMS`, `build-info.json` are verified in-job and deliberately **not** uploaded | `windows-core-amd64.yml:544-580` | describes the old shape | `windows-core-amd64.yml:548-579` |
| Unix/macOS/Linux release authority | YES | `server-linux-amd64.yml` and `client-macos.yml` are the two default release targets; Linux+macOS remain required even when extra platforms are off | `release-artifacts.sh:86-89` | partially | same |
| SIGPIPE / pipefail false-negative handling | YES, EXPLICIT | Documented, deliberate handling in 5 places: `od -N` instead of `head -c`; `set +o pipefail` around the failure-annotation `grep … | head`; `PIPESTATUS[0]` capture in RCV desktop steps; comments in Apple provenance, Apple signing, Windows tag assertion | RCV, `release-artifacts.sh`, Apple/Windows scripts | not documented | `release-artifacts.sh:1029-1032`; `release-contract-verify.yml:165-176`, `:192`, `:204`, `:229`, `:279`; `apple-abi-provenance.sh:339`; `test-apple-signing.sh:355`; `windows-core-amd64.yml:362` |
| dry-run behavior | YES | `dry_run` is normalised to an explicit publish flag; anything not exactly `true`/`false` dies. A dry run exercises everything except the four publishing steps | `release-artifacts.sh cmd_publish_flag`; `release.yml:288-300` | §5 step 3 | `release-artifacts.sh:1613-1626`; `release.yml:437-444` |
| canonical Release asset behavior | YES | `auto` prefers the canonical Release asset; PRESENT+INVALID is a HARD FAIL with no fallback; asset name is exact, never globbed | `scripts/publish-apple-beta.sh` | not documented | `publish-apple-beta.sh:34-36`, `:207`, `:220-277`, `:302-304` |
| Actions fallback behavior | YES | Reached only when the canonical asset is ABSENT (under `auto`) or explicitly requested; a fallback never runs after the canonical path succeeded | `scripts/publish-apple-beta.sh` | not documented | `publish-apple-beta.sh:241-254`, `:287-311` |
| fail-closed semantics | YES, PERVASIVE | Unreadable API answer is not a green one; skipped run refused as a gate; every job must be `completed`+`success`/`neutral`; missing payload artifact dies; `publish-args` is all-or-nothing; tag/version character sets whitelisted before filename interpolation | `release-artifacts.sh`, `require-executed-coverage.sh` | partially | `release-artifacts.sh:300`, `:640-663`, `:680-681`, `:1734-1750`; `require-executed-coverage.sh:1-37` |

## New behaviour the module does not document

1. **`release-artifacts.sh evidence` + `REQUIRED_EVIDENCE`** — `release-artifacts.sh:113-127`, `:580-684`;
   `release.yml:213-242`. The release refuses to publish unless `verify.yml` **and**
   `release-contract-verify.yml` have a completed, successful, newest-at-exact-SHA run, with **every
   job** successful. This is the strongest new trust boundary in the release path: source and tooling
   evidence is now a precondition, not an assumption. Module §5 step list omits it entirely.
2. **`public_asset_names()` as the single derivation** — `release-artifacts.sh:1644-1681`. Answers the
   "which workflow is the single authority" question: this function is. `check-upload` (`:1813-1821`),
   `expected-assets` (`:1706`), `publish-args` (`:1744`) and `verify-public-release` (`:1778`) all call
   it. The v0.1.8 six-asset release is recorded as the reason (`:1650-1661`).
3. **Draft → verify → publish → re-verify** — `release.yml:481-486`, `:493-504`, `:519`, `:524-527`.
   What stops a release being publicised with a missing asset is the draft: the public release is
   created by an `edit` that runs only after a name-by-name read-back of the draft has passed, and the
   same read-back runs again afterwards.
4. **`publish-args` all-or-nothing** — `release-artifacts.sh:1734-1750`. It validates every name exists
   before printing any, so a caller that ignores the exit status still cannot hand `gh` a short list.
5. **Apple is a fifth release target** — `release-artifacts.sh:91-111`, `:163-197`. `TARGET_WORKFLOWS`
   under `RELEASE_EXTRA_PLATFORMS=true` is linux, macos, windows-core, android-core, **client-apple**.
   The Apple target has `kind=xcframework-manifest`, a directory as `binary`, `manifest.json` as
   `buildinfo`, and deliberately **no** `tarball`.
6. **Payload/evidence split enforced at the producer** — `android-core-arm64.yml:432-452`;
   `windows-core-amd64.yml:544-580`. "Consumer does not clean up after producer; producer does not hand
   over what it does not ship" (`release.yml:326-345`). The old sixty-line Android cleanup step is gone.
7. **`require-executed-coverage.sh`** — `require-executed-coverage.sh:1-37`; wired at
   `server-linux-amd64.yml:810`, `:832`, `:860` with its self-test in `verify.yml:128-137`. It ends the
   vacuous-coverage failure where `grep -c '^--- (PASS|FAIL|SKIP):'` counted SKIPs as coverage.
8. **`check-release-workflows.py`** — 1254 lines, run by RCV (`release-contract-verify.yml:259-264`).
   Structural YAML/shell/permission assertions: the only writer holds `contents: write`, every
   `steps.<id>.outputs` reference resolves, every API-reaching release step authenticates, the RCV
   paths filter and the PR filter assert the same file set, and `verify.yml` no longer runs the helper
   suite. Measured at HEAD: **123 passed, 0 failed**.
9. **`client-desktop-windows-runtime-audit.yml`** — a `workflow_run`-triggered automatic workflow
   (`:57-62`) that audits the built installer on real Windows. Key semantics: it is explicitly **not**
   release-blocking (`:37-44`) but *is* an ordinary-step gate that fails its own workflow; the audit
   tooling is pinned to the audited SHA and a mismatch is a hard failure, not a warning (`:21-35`,
   `:98-140`).
10. **`mobile-contract.yml` as the sole owner of the mobile tag contract** —
    `mobile-contract.yml:91-123`; moved out of `server-linux-amd64.yml`. The checker asserts exactly one
    owner and no `continue-on-error`.
11. **Apple source-authority inversion** — `release/apple-client-refs.env:33-52` plus
    `client-apple.yml:234-292`. The gitlink is no longer the build authority; the client repository tip
    is, resolved once per run into a job output and seeded into the resolver state file.
    `APPLE_CLIENT_REQUIRE_GITLINK=1` is the opt-in legacy equality check. **This is the single largest
    superseded design in the module.**
12. **`resolve-apple-source` as a one-shot authority job** — `client-apple.yml:81-105`. `libbox`, `ios`
    and `macos` all `needs:` it and consume its outputs; the checker asserts no job re-resolves.
13. **Desktop source identity as a script** — `check-desktop-source-identity.sh:1-32`, called at
    `client-desktop-windows.yml:201`, tested at `test-desktop-source-identity.sh`. `TOOLING_SHA ==
    CORE_SHA` is required, asserted on the values before either directory is read.
14. **`inject-desktop-release-version.py`** — `inject-desktop-release-version.py:1-34`; the retired
    23-rule `apply-desktop-branding-overlay.py` was deleted (confirmed in the delta) because branding
    moved into the pinned fork. The only remaining parent-side overlay is one field in `version.json`.
15. **A newly unwired script** — `scripts/ci/check-apple-resolver-posix.py` (188 lines) has **no
    referrer** at HEAD. Its own header says the real gate is bash and cannot run where it was needed.
16. **`verify.yml` `paths-ignore: ["*.md"]`** — `verify.yml:38-77`. A root-markdown push no longer runs
    the source gate, and `docs/**` is deliberately *not* ignored because `verify-fork-handoff.sh` parses
    three machine-read documents there.

## Proposed module edit list

Do **not** rewrite the module wholesale; these are the edits.

1. **§5 heading "A live defect in the release path, measured at the release baseline"** — delete the
   whole section. It says: *"the release-tree allowlist at `:575-592` is still built as four names per
   target"* and *"the `verify` step dies with `unexpected file(s) in the release tree` whenever
   `RELEASE_EXTRA_PLATFORMS=true`"*. Replace with: **"The v0.1.6 release-tree allowlist defect is
   FIXED, at both ends."** The allowlist moved to `release-artifacts.sh:1372-1399`, gained an Apple
   branch, and now scans each product payload directory at `-maxdepth 1`; the producers were changed so
   Windows uploads exactly four files (`windows-core-amd64.yml:548-579`) and Android splits payload and
   records into two artifacts (`android-core-arm64.yml:432-452`). The `die` is at `:1396` and its
   message is now *"unexpected file(s) in a product payload directory"*. Evidence: those lines, plus the
   in-run `PASS full contract: the android payload is exactly the four public files`.
2. **§5 "Platform coverage, measured at the release baseline"** — it says *"only the Apple IPA/DMG is
   still excluded"* and quotes `release-artifacts.sh:63-67`. Should say: Apple **Libbox** is now a
   fifth release target (`:91-111`, `:163-197`); only the IPA/DMG packaging remains outside the release
   page. The header comment quoted is now corrected in place at `:79-84` and *says* it used to say
   that — quoting the old sentence as current is the defect.
3. **§5 item 7 of the numbered design list** — it says *"`create the release` — `gh release create …
   upload/*.tar.gz` … Not draft, not prerelease."* Replace with a four-step order: **draft create with a
   list derived from `publish-args` (`release.yml:474-486`) → draft read-back by name
   (`:493-504`) → `gh release edit --draft=false --prerelease=false` (`:519`) → public read-back
   (`:524-527`)**. Note that the asset list is deliberately not written in the workflow.
4. **§5 — add a new subsection "The single authority for the release asset list"** (currently absent).
   State that `public_asset_names` (`release-artifacts.sh:1644-1681`) is the only derivation, that
   `check-upload`/`expected-assets`/`publish-args`/`verify-public-release` all call it, and that the
   contract is **7 files** for a release. Mention the residual: `flatten` restates the same derivation
   locally at `:1523-1533`; the two agree today because both read `TARGET_WORKFLOWS` and `kind`.
5. **§5 "So `:594` still prints *'both platforms verified'*"** — delete. `:1410` prints
   `verified N platform(s) …` listing each `platform/arch`, and `:1401-1405` explains why.
6. **§5 "The Apple pin has no wired verifier"** — the *conclusion* survives, the *model* does not.
   Replace *"the fork adopted a deliberate single-authority pin design: `release/apple-client-refs.env`
   declares the `clients/apple` gitlink as the sole authority"* with: **the authority is the Apple client
   repository tip resolved once per run** (`release/apple-client-refs.env:33-52`;
   `client-apple.yml:81-105`, `:234-292`; `apple-client-source.sh:37-42`). Keep the finding that
   `check-libbox-abi.sh` is unwired and that its `VERIFIED_APPLE_SHA` therefore agrees with a gitlink
   that **no longer selects the build** — the agreement is now weaker evidence than the module claims.
   Update the constant value to `40dd48795579718008518ac51af320b1d61377ac` (`check-libbox-abi.sh:155`)
   and the gitlink to the same (`git ls-tree HEAD clients/apple`).
7. **§5 "`check-apple-source-selection.sh` could not have caught it by design — every one of its checks
   is self-referential to the gitlink"** — **refute and replace**. It was rewritten for the repo-tip
   model, is wired at `client-apple.yml:231`, and passes 72/0. It now proves *"moving the gitlink does
   NOT move the iOS source"* and *"dropping the gitlink does not change the resolved source"*
   (`check-apple-source-selection.sh:412`, `:436-442`, `:723`). The correct residual statement is
   narrower: it proves iOS and macOS **agree**, and that the gitlink does not select — it still cannot
   say the resolved revision is the *intended* one.
8. **§5 "The gate's own self-test does not cover it: `test-release-artifacts.sh:532-533` asserts
   `SHA256SUMS` has exactly two rows"** — delete. There is a full-contract fixture:
   `test-release-artifacts.sh:1470`, `:1519`, `:1737`, asserting **7 public files / 6 entries** across
   five products. Also correct every occurrence of **"92-case suite"** to **108 cases**
   (`verify.yml:112`; `release-contract-verify.yml:100`; observed in-run `108 cases`).
9. **§1 table** — replace the block. It must list 12 workflows, and the "Blocking?" column must show
   that **five** workflows have automatic triggers: `verify.yml` (push `testing` + PR, `paths-ignore:
   ["*.md"]`), `release-contract-verify.yml` (push `testing`/`fix/release-asset-contract`/`release/**`,
   PR, path-filtered), `mobile-contract.yml` (push `testing`, path-filtered), `android-core-arm64.yml`
   (push `testing`, path-filtered), `client-desktop-windows-runtime-audit.yml` (`workflow_run` after
   "Windows desktop installer"). Delete *"the only automatic gate"*.
10. **§1 verify.yml step table** — rows 1 and 2 no longer run there. State that the release-helper suite
    moved to RCV (`verify.yml:112-126`) and the acceptance self-test moved to RCV (`:139-150`), and that
    verify.yml is the **source** gate while RCV is the **release-tooling** gate.
11. **§2 "Comment-only mentions"** — remove `check-macos-app-name.sh` from this list; it is invoked at
    `build-macos-testflight.sh:210`. Fix the path of the masque acceptance script to
    `scripts/acceptance/jiejie-masque-vps.sh`. Add `scripts/ci/check-apple-resolver-posix.py` as newly
    unwired.
12. **§3** — update *"returns **15 modified + 1 added** files"* to **49 files (34 modified, 14 added,
    1 deleted)** for the same path set, and restate the distance as **287 commits behind HEAD**
    (101 behind `a267e99`). Say **13 logs** (not 14) plus `gates.tsv` and `README.md`.
13. **§5 "What is out of scope"** — keep the `client-apple.yml`-not-required finding, but add that
    `release-artifacts.sh:555-558` records `verify-release-acceptance.sh` as a **retired second
    authority** whose selection semantics (first admissible green) contradict the release's
    newest-exact-SHA rule — so the engine is not merely unreachable, it was superseded on policy.
14. **§5 — add a subsection on fail-closed / evidence semantics**, because the module has none and it is
    the release path's most load-bearing property: unreadable API ≠ green (`:300`, `:575`), a skipped
    run is not a pass (`:642-643`), every job of an evidence run must succeed (`:648-663`), and a plan
    with fewer rows than required gates dies (`:678-681`).
15. **§7** — add a caution: **do not cite a line number in `release-artifacts.sh`.** The file grew from
    ~600 to 1903 lines in this delta; every `:NNN` in the old §5 pointed at unrelated code. Cite the
    function name (`public_asset_names`, `cmd_verify`, `cmd_evidence`) instead.
16. **New stale-comment findings worth recording** (comments that assert superseded designs, in files
    that *do* run): `apple-libbox-artifact.sh:29-34` still says *"the two Apple products are built from
    two different Apple branches (iOS from hako-ui, macOS from the pinned dev commit)"*, a model the
    refs file explicitly retires; `release-artifacts.sh:1163-1174` still says
    `test-apple-canonical-source.sh` *"is NOT passing and is NOT wired into any workflow"*, while
    `release-contract-verify.yml:136-146` runs it and `check-release-workflows.py` asserts that it does;
    `check-apple-source-selection.sh:124` still justifies the no-SHA rule with *"the gitlink is the only
    authority"*.
17. **Checklist against the concurrent 537-line revision** (grep-verified absent from it): it has **0**
    occurrences of `REQUIRED_EVIDENCE` (edit 1's subject — the release path's strongest new trust
    boundary), `payload_artifact`, `require-executed-coverage`, `APPLE_CLIENT_REQUIRE_GITLINK`,
    `40dd4879` and `paths-ignore`. It has **1** occurrence of `public_asset_names` and **0** of `108`.
    Its §5 Apple section at `:406-451` is byte-identical to the stale text quoted in edits 6 and 7.

## Claims I could not verify

- **UNKNOWN — is `release-contract-verify.yml` a *blocking* gate?** RCV is a workflow, and `release.yml`
  consumes its run through `evidence` (`release-artifacts.sh:124-127`, `release.yml:213-242`), so it
  blocks **releases**. Whether it blocks **merges** depends on branch protection, which is not in the
  repository. Settle with the GitHub API, not the tree:
  `gh api repos/Piggy-Cat-bit-shadow/sing-box/branches/testing/protection` (or `.github` settings UI).
- **BLOCKED_BY_ENVIRONMENT — the true verdict of `test-release-artifacts.sh`.** Executed here:
  108 cases, 29 failed. Every failure names a missing host tool, not repository logic —
  `release-artifacts.sh:1035: od: command not found`, `:514: sleep: command not found`,
  `mock-gh: no jq on this machine`, `FileNotFoundError: '-c'` from the `python3` shim. The structural
  cases that do not need those tools pass, including all eleven `full contract:` cases.
  Settle with, on a real Linux runner: `./scripts/ci/test-release-artifacts.sh` (expect exit 0).
- **BLOCKED_BY_ENVIRONMENT — `test-apple-canonical-source.sh` and `verify-release-acceptance-test.sh`.**
  Both need a working `python3 -c` and a mock `gh`; this host has no real `gh` and only a stub `python3`.
  Settle with: `./scripts/ci/test-apple-canonical-source.sh` and
  `./scripts/ci/verify-release-acceptance-test.sh` on ubuntu-latest.
- **BLOCKED_BY_ENVIRONMENT — `check-release-workflows.py` is 123/0, not 125/0.** Two checks SKIP with
  *"git not on PATH for this process"* (line endings, executable bit). Settle by re-running with git on
  `PATH`: `python3 scripts/ci/check-release-workflows.py` → expect 125 passed.
- **BLOCKED_BY_ENVIRONMENT — `check-libbox-abi.sh` layers 3–4.** It needs `gobind` and client
  checkouts; the recorded log shows `PARTIAL` with a SKIP. Settle with
  `bash scripts/ci/check-libbox-abi.sh --client-root <dir>` on a host with `gobind`.
- **UNKNOWN — the "eight test functions" count** in the orphan-tag files (§2). I confirmed all four
  tags run in no declared configuration but did not re-count functions. Settle with
  `grep -c '^func Test' common/trafficsched/adaptive_research_test.go experimental/libbox/p2ios_*.go
  common/tlsfragment/integration_test.go`.
- **UNKNOWN — whether the Apple pipeline has ever produced a published manifest carrying a `run_id`.**
  `apple-libbox-artifact.sh:94`, `:206` writes `run_id` from `GITHUB_RUN_ID` into `manifest.json`, so a
  *published* release asset would be a genuine CI run record. No such manifest is in the tree, so the
  `VERIFIED_CI` ceiling still holds today. Settle with
  `gh release download <tag> --pattern manifest.json` once an Apple release exists.
