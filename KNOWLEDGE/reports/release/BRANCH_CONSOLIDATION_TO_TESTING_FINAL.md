# BRANCH_CONSOLIDATION_TO_TESTING_FINAL

Consolidation of every remaining 0.1.6 branch in
`Piggy-Cat-bit-shadow/sing-box` into `testing`, with combination-state
regression and cleanup of the absorbed remote branches.

---

## 1. Before

```text
TESTING_BEFORE              = 6cc45d3f49a3908ff150045d3a113f98920eaaea
REMOTE_BRANCH_COUNT_BEFORE  = 14   (13 feature/closure branches + testing)
WORKTREE_CLEAN              = YES  (clean clone at <WORKSPACE_ROOT>\singbox-consolidation)
```

The remote branch list matched the task brief exactly, tip for tip, and
`testing` was still `6cc45d3f`, so no opening drift had to be reconciled.

Branch inventory taken at `git fetch --all --prune`:

| branch | tip | last commit subject | date |
|---|---|---|---|
| `audit/upstream-absorb-0.1.6-91ca1652d` | `c209ddd9e8ca` | docs(internal): record the window-1 artefact the blob comparison recovered | 2026-10-11 18:50 |
| `closure/0.1.6-consumer-dns-parity` | `ba3cab01bd20` | docs(internal): consumer / DNS / resolver parity closure | 2026-10-11 20:44 |
| `closure/0.1.6-group-crosskind` | `d95e256b7626` | chore(physicalpath): drop the two dead helpers the reverted narrowing left behind | 2026-10-11 20:49 |
| `closure/0.1.6-static-validation-final` | `44f667c6712e` | docs(internal): consumer demand is closed; the gate now hangs on cross-kind alone | 2026-10-11 21:26 |
| `closure/0.1.6-static-validation-final-wire` | `6ab42a0cd5f5` | docs(internal): align static-validation closure provenance with final code | 2026-10-12 00:06 |
| `closure/0.1.6-static-validation-true-final` | `d6a8a833d009` | docs(internal): true-final closure report, with the graph gap named | 2026-10-11 22:30 |
| `debug/static-validation-false-deny-20261012` | `140e3aca345c` | docs(internal): record round 2 - the audit findings checked one by one | 2026-10-11 19:27 |
| `fix/route-aware-outbound-capability-validation` | `0dfedb382f6e` | fix(routeflow): consume a protocol only where the runtime router actually stops | 2026-10-11 18:50 |
| `fix/0.1.6-apple-pin-gates` | `75f093c2ca75` | docs: two text fixes — stale row count, orphaned docstring fragment | 2026-10-11 21:36 |
| `integration/0.1.6-final-freeze` | `96a94f584d30` | docs(internal): the 0.1.6 final core closure report | 2026-10-11 20:03 |
| `jiejiebox/apple-finalize` | `96c30f79100a` | docs(apple): resolve the branch tip by command, not by literal | 2026-10-11 12:05 |
| `jiejiebox/p1c-iphone-device-closure` | `e65d5d4f1d11` | docs(fork): record the P1-C iPhone device closure evidence | 2026-10-11 19:07 |
| `jiejiebox/v0.1.6-hardening` | `91ca1652d9de` | fix(hysteria2): a hard path ceiling is its own field, applied after ChromeParrot | 2026-10-11 11:44 |
| `testing` | `6cc45d3f49a3` | fix(libbox): write the platform device level into the reload mirror before the nil instance | 2026-10-11 17:02 |

---

## 2. The ancestry graph, as measured

Read with `git merge-base`, `git rev-list --left-right --count`,
`git merge-base --is-ancestor` and `git cherry`. Nothing was taken from the
brief's guesses.

Every branch was **ahead** of `testing` and **none** was an ancestor of it:

```text
audit/upstream-absorb-0.1.6-91ca1652d            ahead=34  behind=88  unique=34
closure/0.1.6-consumer-dns-parity                ahead=55  behind=25  unique=53
closure/0.1.6-group-crosskind                    ahead=54  behind=25  unique=52
closure/0.1.6-static-validation-final            ahead=64  behind=25  unique=60
closure/0.1.6-static-validation-final-wire       ahead=72  behind=25  unique=68
closure/0.1.6-static-validation-true-final       ahead=67  behind=25  unique=63
debug/static-validation-false-deny-20261012      ahead=36  behind=88  unique=36
fix/route-aware-outbound-capability-validation   ahead=31  behind=88  unique=31
fix/0.1.6-apple-pin-gates                        ahead=58  behind=25  unique=56
integration/0.1.6-final-freeze                   ahead=53  behind=25  unique=51
jiejiebox/apple-finalize                         ahead=3   behind=11  unique=3
jiejiebox/p1c-iphone-device-closure              ahead=7   behind=25  unique=7
jiejiebox/v0.1.6-hardening                       ahead=29  behind=88  unique=29
```

`git cherry origin/testing <branch>` reported `+` for **every** non-merge
commit on **every** branch, and `-` for none. There is therefore **no
patch-equivalent branch at all** in this set: nothing was already present as an
equivalent patch, and the `PATCH_EQUIVALENT_ABSORBED` classification is empty.

The measured ancestry pairs (A IS_ANCESTOR_OF B) collapse the thirteen branches
into **three independent heads** plus one long chain:

```text
jiejiebox/v0.1.6-hardening (29)
  └─ fix/route-aware-outbound-capability-validation (31)
       └─ debug/static-validation-false-deny-20261012 (36)
            ├─ audit/upstream-absorb-0.1.6-91ca1652d (34)
            └─ integration/0.1.6-final-freeze (53)
                 ├─ jiejiebox/p1c-iphone-device-closure (7)
                 ├─ closure/0.1.6-consumer-dns-parity (55)
                 └─ closure/0.1.6-group-crosskind (54)
                      └─ closure/0.1.6-static-validation-final (64)
                           └─ closure/0.1.6-static-validation-true-final (67)
                                └─ closure/0.1.6-static-validation-final-wire (72)  ← HEAD A
                 closure/0.1.6-consumer-dns-parity ─┐
                 closure/0.1.6-group-crosskind ──────┤
                 integration/0.1.6-final-freeze ─────┼→ static-validation-final
                 fix/route-aware-… ─────────────────┤
                 debug/… ───────────────────────────┤
                 audit/… ───────────────────────────┤
                 jiejiebox/p1c-… ───────────────────┘
fix/0.1.6-apple-pin-gates (58)                                                    ← HEAD B
jiejiebox/apple-finalize (3)                                                      ← HEAD C
```

`fix/0.1.6-apple-pin-gates` shares merge-base `96a94f58` with
`static-validation-final-wire`: 51 of its 56 commits were therefore already
absorbed by chain A, leaving 5 genuinely unique commits. `apple-finalize` is
disjoint from both.

**This is why only three merges were made.** The other ten branches were
ancestors of one of those three heads and are absorbed by ancestry, not by a
merge of their own — mechanically merging them would have added ten
no-op merge commits and made the graph unreadable.

---

## 3. Staging

```text
git switch --detach origin/testing
git switch -c integration/0.1.6-testing-consolidation
INTEGRATION_START = 6cc45d3f49a3908ff150045d3a113f98920eaaea  ==  TESTING_BEFORE
```

All merges, conflict resolutions and tests were done on that branch.
`testing` was not touched until the very end.

---

## 4. Merge graph

```text
REAL_MERGES = 3

  a85ba40e  chore(consolidation): absorb closure/0.1.6-static-validation-final-wire
            parents: 6cc45d3f (testing) + 6ab42a0c (final-wire)     160 files  +17171/-629
  a2abd572  chore(consolidation): absorb fix/0.1.6-apple-pin-gates
            parents: a85ba40e + 75f093c2                             40 files  +3958/-1216
  de715829  chore(consolidation): absorb jiejiebox/apple-finalize
            parents: a2abd572 + 96c30f79                              2 files  +355/-14
  9ced64e0  fix(runtimecoord): restore the source/test pair the merge split
            (a follow-up to the merges, see §6)                       1 file   +2/-104

ANCESTOR_SKIPS        = 10
PATCH_EQUIVALENT_SKIPS = 0   (none of the 13 is patch-equivalent to testing)
SUPERSEDED_SKIPS       = 0   (no branch was dropped as superseded)
CHERRY_PICKS           = 0
```

Branch-by-branch final status on the integration tree (all verified with
`git merge-base --is-ancestor <branch> HEAD`, `not_in_head = 0` for every one):

| branch | tip | classification | absorption method | proof | safe to delete |
|---|---|---|---|---|---|
| `closure/0.1.6-static-validation-final-wire` | `6ab42a0cd5f5` | MERGED | real merge `a85ba40e` | merge commit parent | YES |
| `fix/0.1.6-apple-pin-gates` | `75f093c2ca75` | MERGED | real merge `a2abd572` | merge commit parent | YES |
| `jiejiebox/apple-finalize` | `96c30f79100a` | MERGED | real merge `de715829` | merge commit parent | YES |
| `closure/0.1.6-static-validation-true-final` | `d6a8a833d009` | ANCESTOR_ABSORBED | ancestor of final-wire | `--is-ancestor` on `origin/testing` | YES |
| `closure/0.1.6-static-validation-final` | `44f667c6712e` | ANCESTOR_ABSORBED | ancestor of final-wire | `--is-ancestor` | YES |
| `closure/0.1.6-consumer-dns-parity` | `ba3cab01bd20` | ANCESTOR_ABSORBED | ancestor of final-wire | `--is-ancestor` | YES |
| `closure/0.1.6-group-crosskind` | `d95e256b7626` | ANCESTOR_ABSORBED | ancestor of final-wire | `--is-ancestor` | YES |
| `integration/0.1.6-final-freeze` | `96a94f584d30` | ANCESTOR_ABSORBED | ancestor of final-wire | `--is-ancestor` | YES |
| `debug/static-validation-false-deny-20261012` | `140e3aca345c` | ANCESTOR_ABSORBED | ancestor of final-wire | `--is-ancestor` | YES |
| `fix/route-aware-outbound-capability-validation` | `0dfedb382f6e` | ANCESTOR_ABSORBED | ancestor of final-wire | `--is-ancestor` | YES |
| `audit/upstream-absorb-0.1.6-91ca1652d` | `c209ddd9e8ca` | ANCESTOR_ABSORBED | ancestor of final-wire | `--is-ancestor` | YES |
| `jiejiebox/p1c-iphone-device-closure` | `e65d5d4f1d11` | ANCESTOR_ABSORBED | ancestor of final-wire | `--is-ancestor` | YES |
| `jiejiebox/v0.1.6-hardening` | `91ca1652d9de` | ANCESTOR_ABSORBED | ancestor of final-wire | `--is-ancestor` | YES |

`KEPT_UNRESOLVED = 0`, `PROTECTED = 0`. Nothing in the 0.1.6 set was left
behind.

---

## 5. Conflicts

21 files conflicted across the three merges. Every one was resolved by reading
the base, both sides' intents and the surrounding, non-conflicting text — no
`--ours`/`--theirs` in bulk, no `git checkout --ours .`.

### Merge 1 — `closure/0.1.6-static-validation-final-wire` (8 files)

| file | ours intent | theirs intent | resolution | why no semantic loss |
|---|---|---|---|---|
| `common/runtimecoord/platform_events.go` | comment on the screen-off clear | comment on the same statement | ours | conflict hunks were comment text only for identical code lines; verified by a comment-stripped diff |
| `common/runtimecoord/platform_events_test.go` | comment | comment | ours | same |
| `common/sniff/quic_boundary_test.go` | deliberately avoids quoting a source line number ("the line moved when the bounded copy landed") | hard-codes `quic.go:191` | ours | the incoming text is the stale reference the other side removed |
| `route/splice_diagnostics.go` | `spliceReasonHandoverAccepted` / `Accepted` / `AcceptanceRatio()` / `"… accepted=%d acceptance_ratio=%.3f"` | `spliceReasonSuccess` / `Successes` / `Ratio()` / `"splice diagnostics: … successes=%d ratio=%.3f"` | ours | see §5.1 — our side is the fix, theirs is its revert |
| `route/splice_diagnostics_test.go` | source-level guard forbidding `Successes`, `successes=`, `splice diagnostics`, `splice success`, `splice ratio` | tests for the retired naming | ours | same |
| `route/tcp_splice_diagnostics_test.go` | same guard | same | ours | same |
| `scripts/ci/build-apple-libbox.sh` | normalises the built framework to the repository root | keeps it at `_libbox_build/Libbox.xcframework` via a `$framework` variable | theirs, plus our unique stale-root `rm -rf Libbox.xcframework` | the incoming design is what the rest of the tree assumes: `release-apple.sh:234` states an artifact install "unpacks into the iOS client and never writes the repository root", and `install_into`'s default source is the build-output path |
| `go.sum`, `test/go.sum` (and `go.mod`, auto-resolved) | see §5.2 | | union, then `go mod tidy` | |

`clients/apple` (gitlink) did not conflict here but was resolved deliberately:
see §5.3.

### Merge 2 — `fix/0.1.6-apple-pin-gates` (10 files)

| file | ours intent | theirs intent | resolution | why no semantic loss |
|---|---|---|---|---|
| `scripts/ci/apply-apple-branding-overlay.py` | brands `PRODUCT_NAME` "sing-box"→"JieJieBox" and rewrites the SFI scheme `BuildableName` to `JieJieBox.app` | writes the display name only; asserts `PRODUCT_NAME` stays upstream | theirs | §5.4 — the pinned Apple client's own audit forbids the rename |
| `scripts/ci/apply-apple-macos-branding-overlay.py` | same, per-configuration state machine that rewrites `PRODUCT_NAME` | display name only, `PROTECTED = {"PRODUCT_NAME": "sing-box"}` | theirs | §5.4; and the automatically merged, non-conflicting header of this very file already said display-only |
| `scripts/ci/test-branding-scope.py` | asserts the branded `PRODUCT_NAME` and branded buildable | asserts `PRODUCT_NAME == UPSTREAM` on every target | theirs | §5.4 |
| `scripts/ci/test-apple-macos-name.sh` | asserts `PRODUCT_NAME == brand` and 3× `BuildableName = "<brand>.app"` | asserts `PRODUCT_NAME = "sing-box"` and that the scheme is validated, never rewritten | theirs | §5.4 |
| `scripts/ci/check-apple-source-selection.sh` | 3 `assert-shared-source` checks | 9, plus negative controls (a re-introduced per-platform SHA is refused; the removed `assert-distinct` is refused) | theirs | superset — the one-source topology is present on both sides |
| `scripts/publish-apple-beta.sh` | prose | prose, plus reports each platform's working tree | theirs | cosmetic/reporting only |
| `scripts/release-apple.sh` | prose | prose | theirs | cosmetic only |
| `scripts/ci/apple-client-source.sh` | `checkout_macos` works when the submodule is absent (blobless clone from the declared repository, object-store alternates check) | fails hard when `clients/apple` is not checked out | ours | the macOS job deliberately checks out with `submodules: false` (`client-apple.yml:472`), so the incoming revision breaks that job |
| `.github/workflows/client-apple.yml` | prose describing the one-source topology; macOS job keeps `submodules: false` | the same topology in different words; `submodules: recursive` | ours | all six hunks are prose with no behavioural difference |
| `scripts/ci/test-apple-signing.sh` | runs `scripts/ci/check-no-tracked-private-keys.py` | an anchored `git grep` for PEM headers | ours | that Python gate exists **only** on this side (added by `76f2369a`); taking the incoming hunk would leave the check invoking a pattern the tree already outgrew |

### Merge 3 — `jiejiebox/apple-finalize` (3 files)

| file | ours intent | theirs intent | resolution | why no semantic loss |
|---|---|---|---|---|
| `clients/apple` (gitlink) | `90a0e9ae` | `bb5309b4a` | ours | §5.3 |
| `scripts/ci/check-libbox-abi.sh` | `VERIFIED_APPLE_SHA = 90a0e9ae` | `bb5309b4a`; a fuller comment block naming the consolidation to `main` and why the revision is the verified one | constant ours, comment block theirs | the constant must agree with the gitlink (layer 5 of that script asserts it); the incoming comment is what the adjacent non-conflicting paragraph already refers to as "the report above" |
| `release/apple-client-refs.env` | `APPLE_CLIENT_BRANCH=main`, `HAKO_DESIGN_BASELINE_TAG=archive/hako-design-v1` | identical values, different prose | ours | the file is byte-identical to the integration tree, so nothing from the incoming commit is dropped — the pin-gate line had already absorbed it |

### 5.1 `route/splice_diagnostics.go` — the incoming side was a revert

The two sides differ by an identifier rename, and the rename is the thing one
side had deliberately undone. Our revision carries a source-level guard in
`route/splice_diagnostics_test.go` that fails if the producer ever says
`Successes`, `successes=`, `splice diagnostics`, `splice success` or
`splice ratio`, because that wording reads as a rate of working kernel splices
while the counter only means "sing-tun accepted the handover". The incoming
revision is exactly that wording. A comment-stripped, identifier-normalised
diff of the two files is a rename and nothing else, so keeping our side loses
no functionality.

### 5.2 `go.mod` / `go.sum`

The automatic merge had already resolved `go.mod` correctly, and the result was
kept: `sing` at our newer
`v0.9.6-0.20261011105649-bde7ddda55ef`, plus `quic-go` at
`v0.61.1-0.20261011111900-4d740322a18a` and the `sing-quic` replace that only
the incoming side carries. The two sums were resolved as a union and then
regenerated with `go mod tidy`; the result is 4 lines away from our revision
and keeps every entry both sides need. `go mod tidy` was only run because the
repository's own toolchain already requires it here; the dependency tree was
not otherwise refreshed.

### 5.3 The Apple gitlink — settled on the branch's own machine-readable evidence

Three-way disagreement, and our own tree was internally inconsistent:

| | `clients/apple` gitlink | `check-libbox-abi.sh` `VERIFIED_APPLE_SHA` |
|---|---|---|
| `origin/testing` | `2a189686` | `2b23330d4` |
| merge 1 incoming (`final-wire`) | `90a0e9ae` | `90a0e9ae` |
| merge 3 incoming (`apple-finalize`) | `bb5309b4a` | `bb5309b4a` |

Resolved to **`90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07`**, on evidence rather
than recency:

* `fix/0.1.6-apple-pin-gates/verify-evidence/pin-identity.log` requires
  `APPLE_GITLINK == VERIFIED_APPLE_SHA == SUBMODULE_HEAD == APPLE_ORIGIN_MAIN`,
  and all four are `90a0e9ae` there, on `integration/0.1.6-final-freeze` and on
  `closure/0.1.6-static-validation-final-wire`;
* `verify-evidence/no-stale-pins.log` names `bb5309b4` and `5911580a` as
  **retired**;
* in `sing-box-for-apple`, `90a0e9ae` is the newest candidate (2026-10-11
  19:14, "Merge branch 'jiejiebox/p1c-iphone-device-closure' into main"), and
  `bb5309b4a` (2026-10-11 09:55) is its ancestor — confirmed against the API —
  so `apple-finalize` recorded `main` *before* the P1-C closure landed on it;
* `refs/heads/main` now points at `90a0e9ae` exactly.

`git ls-tree HEAD clients/apple` and the `VERIFIED_APPLE_SHA` constant therefore
agree, which is the condition the ABI gate checks.

### 5.4 Why the branding overlays went to the incoming side

The deciding authority is the pinned client's own boundary audit. At
`90a0e9ae`, `scripts/dev/audit_apple_ui_boundary.py` states:

```python
#: The one brand override this fork is allowed to make. INFOPLIST_KEY_CFBundleDisplayName is the
#: smallest safe lever: it changes what SpringBoard, the Dock and Spotlight show and nothing else.
BRAND_DISPLAY_NAME = "Jiejiebox"

#: Settings that carry a user-visible name but must keep their upstream value, because they are
#: identity, protocol or signing surface rather than presentation.
PROTECTED_NAME_SETTINGS = (
    "PRODUCT_NAME",
    "PRODUCT_BUNDLE_IDENTIFIER",
    "BASE_PACKAGE_IDENTIFIER",
)
```

That is a global constraint, not a per-target one. Our revision rewrites
`PRODUCT_NAME` to `JieJieBox` and the SFI/SFM scheme's `BuildableName` to
`JieJieBox.app`; the incoming commit `8a30bccf` ("brand display, not product
identity; and repair the Apple pin gates") is the repair of exactly that
over-reach, and its message records that the frozen SHA does not move.

Two further facts made this unambiguous:

* the automatically merged, non-conflicting header of
  `apply-apple-macos-branding-overlay.py` already reads "It does NOT touch
  `PRODUCT_NAME`… `PROTECTED = {"PRODUCT_NAME": "sing-box"}`", so keeping the
  other side would have produced a file contradicting its own docstring;
* `scripts/ci/check-macos-app-name.sh`, `build-macos-testflight.sh` and
  `prepare-apple-client.sh` auto-merged to the display-only model, so the
  incoming overlay is what makes the tree agree with itself. Our side would
  have needed eight more edits to become coherent.

The canonical brand name is `JieJieBox` (capital B); the Apple client commits
`Jiejiebox` (lower-case b) as an accepted starting state, which the overlays
normalise.

---

## 6. One real regression found after the merges, and fixed

`go test ./common/runtimecoord/` failed on the merged tree while passing on
**both** parents — the signature of a chimera:

| revision | `common/runtimecoord` |
|---|---|
| untouched `origin/testing` | ok |
| pre-consolidation `closure/…final-wire` | ok |
| this integration branch, before the fix | **FAIL (2 tests)** |

Cause: `platform_events.go` came out as our revision byte-for-byte (the
conflict resolved there), while `platform_events_test.go` came out as a
line-level union of both revisions. The two sides disagree about whether a
user-present edge may write the screen LEVEL — our `DeviceWake()` sets
`screenKnown`/`screenOn`, the incoming one deliberately does not, because
`recomputeDeviceLocked` derives `wantPaused = screenKnown && !screenOn`, so an
unlock that writes the level leaves the device axis believing the screen is on
and it never re-pauses.

Resolved to the our-side pair (`9ced64e0`): the source is already that
revision, so the test file was restored to it. The two incoming-only
assertions assert the opposite of the behaviour this tree has and cannot hold
here; the alternative reading is recorded in the incoming commit `645d96b4`,
which remains an ancestor of the merge.

This is the failure mode the brief warned about, and it was caught only because
the suite was run against the merged tree *and* both baselines rather than
against the merged tree alone.

---

## 7. Regression

All Go commands were run with the repository's own tag set,
`-tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)"`
(`with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname`).
An untagged run is a different configuration and produces false failures in
this tree (see §7.3).

### 7.1 Frozen static-validation invariants (regression, not a new audit)

```text
CROSS_KIND_GRAPH_CLOSED                = YES  still
STATIC_VALIDATION_FALSE_DENY_CLOSED    = YES  still
STATIC_VALIDATION_FALSE_ALLOW_CLOSED   = YES  still
STATIC_VALIDATION_REGRESSION           = PASS
```

Named focus points from the brief, all passing:

| invariant | test |
|---|---|
| endpoint explicit resolver cycle | `TestTheCrossKindGraphReadsPublishedAdapterFacts/endpoint_explicit_domain_resolver_closes_a_cycle` |
| default resolver domain-server cycle | `TestTheDefaultDomainResolverIsAGraphEdge`, `…/default_resolver_on_a_domain_server_closes_a_cycle` |
| literal-server positive twin | `TestAnEndpointTaggedDirectIsDiagnosedRatherThanCrashing`, `TestADistinctEndpointTagStarts` |
| H-PROVABLE consumer tests | `TestConsumerDemandContractIsProvableOnly`, `TestAnHTTPClientDetourMustCarryTheConfiguredTransports`, `TestADNSTransportDetourMustCarryTheQuery` |
| group advertised-only positive control | `TestAnAdvertisedOnlyRootDoesNotClaimAProof` |
| route-aware case-08 / case-10 / case-10b | `TestRouteAwareNetworkRequirementMatrix` (all 22 cases, incl. `case-08-group-really-delivers-udp-to-a-tcp-only-member`, `case-10-nested-groups-carry-the-correct-requirement`, `case-10b-nested-groups-keep-the-real-defect`) |
| `ResolveAuthority` nil-manager panic regression | `TestResolveAuthorityWithoutATransportManager` (common/dialer) |
| false-deny / false-allow matrix | `TestFalseDenyMatrix`, `TestCrossKindDNSCycleIsRefusedAtStartup` |

### 7.2 Build, format, vet, full test

```text
GO_BUILD_ALL   = PASS    go build ./...  exit 0
GOFMT          = PASS    0 of the 106 changed Go files need formatting; 0 repo-wide
GO_VET_ALL     = PRE_EXISTING_ONLY
GO_TEST_ALL    = PRE_EXISTING_ONLY
```

**`go vet`** — three `unsafe.Pointer` findings remain, and only those:

```text
daemon/managed_service.go:75:12:            possible misuse of unsafe.Pointer
experimental/libbox/debug.go:11:11:         possible misuse of unsafe.Pointer
experimental/boxdd/authenticode_windows.go:95:52: possible misuse of unsafe.Pointer
```

Identical with and without tags, and identical on the untouched
`origin/testing` baseline and on the pre-consolidation comparator, so they are
**pre-existing**.

One vet failure **was** introduced by the merge and is fixed: the incoming
`transport/http/stream_error_buffer_test.go` referenced `stubConn`, which lives
in the `with_quic`-gated `stream_error_test.go`, so `go vet ./...` and
`go test ./...` were red in the untagged configuration. This reproduced
identically on the pre-consolidation comparator, so it was pre-existing on the
incoming line rather than created by the resolution, and it was fixed the way
the repository already fixed the same defect in
`client_h3_strict_fallback_test.go` — add `//go:build with_quic`. Vet is now
clean both tagged and untagged.

**`go test ./...`** (single run, `-count=1 -timeout 25m`, all 172 packages,
7m10s):

```text
ok        = 82
no test files = 87
FAIL      = 3
```

The three failures are the brief's pre-existing host/environment set, and each
was reproduced test-for-test on the untouched baseline:

| package | failing tests | reason |
|---|---|---|
| `common/tls` | `TestWindowsClientHandshakeTLS13`, `TestWindowsClientRoundtripTLS13`, `TestWindowsClientTLS13PostHandshakeConcurrentWrite` | `tls handshake: read handshake: unexpected EOF` |
| `common/tlsspoof` | `TestIntegrationSpooferOpenClose`, `TestIntegrationConnInjectsThenForwardsRealCH`, `TestIntegrationSpooferInjectThenWrite` | `windivert: open SCM: Access is denied` (needs Administrator) |
| `common/windivert` | `TestIntegrationTamperedCacheRepaired`, `TestIntegrationOpenSendOnly`, `TestIntegrationCloseTwice`, `TestIntegrationRecvAbortsOnClose`, `TestIntegrationConcurrentOpen` | same |

No new failure was reclassified as pre-existing, and no failure was assumed to
be pre-existing without being reproduced on a comparator.

`protocol/masque` initially looked like a hang under a shorter timeout. It is
not: on the untouched baseline its `TestMasqueRestartCyclesDoNotLeakLoops`
alone passes in **330s**, and the full package passes within the 25m budget. It
was measured rather than guessed at.

### 7.2b Two slow packages, measured rather than called hangs

Both were chased down because a timeout is not evidence:

| package | single run, untouched baseline | single run, this tree | verdict |
|---|---|---|---|
| `common/trafficsched` | 106.206s | 106.417s | identical; not a regression |
| `protocol/masque` | 330s for one test | passes within budget | identical; not a regression |

`common/trafficsched` times its own sleeps (`TestContentionColdStart` 54s,
`TestContentionSteadyState` 29s, `TestAdmittedRateMatchesTheConfiguredRate`
4s), so its cost is mostly wall clock. An early stress plan used `-count=10`
there, which is roughly 18 minutes of sleeping and tripped the timeout; every
individual test still reported PASS. The plan was resized from measured cost,
which is what §7.4 records.

### 7.3 Why untagged runs are not the comparison

This tree's default configuration carries build tags. Running untagged produces
failures that are not regressions, e.g.:

```text
initialize endpoint[0] wireguard[wg-endpoint]: WireGuard is not included in this build,
  rebuild with -tags with_wireguard
```

Both configurations were run; the tagged one is the repository's own and is
what the results above use.

### 7.4 Stress

Run on the packages that several branch lines touched at once (30 Go packages
are touched by ≥2 of the 13 branches; the root package and
`experimental/libbox` are touched by 12).

<!--STRESS_RESULTS-->

### 7.5 Not run on this host

```text
RACE   = NOT_RUN_HOST_BLOCKED   (-race needs cgo and a C toolchain; none on this host)
DOCKER = NOT_RUN_HOST_BLOCKED   (docker is not installed; test/docker_test.go has no skip guard)
APPLE_GATE_REGRESSION = NOT_RUN_HOST_BLOCKED
```

The Apple gates are shell + Xcode + a signed client checkout:
`scripts/ci/*.sh`, `bash`, `python3` and `xcodebuild` are all absent here (
`python.exe` on this host is the 0-byte Microsoft Store alias, and the
`C:\src\PortableGit` tree is empty), so **no Apple gate was run and none is
recorded as PASS**. What was verified instead, without a shell, is recorded in
§5.3, §5.4 and §8.

---

## 8. What was verified about the Apple surface without running its gates

* `git ls-tree HEAD clients/apple` == `VERIFIED_APPLE_SHA` == `90a0e9ae`, the
  agreement `check-libbox-abi.sh` layer 5 asserts;
* `refs/heads/main` in `sing-box-for-apple` is `90a0e9ae`, so the pinned commit
  is the branch tip, not merely reachable;
* the branding overlays write only `INFOPLIST_KEY_CFBundleDisplayName`:
  `apply-apple-branding-overlay.py` and
  `apply-apple-macos-branding-overlay.py` both carry
  `PROTECTED = {"PRODUCT_NAME": "sing-box"}`, and
  `test-branding-scope.py` asserts `setting(block, "PRODUCT_NAME") == "sing-box"`
  with `SCHEME_BUILDABLE = 'BuildableName = "sing-box.app"'`;
* no private key block is tracked — `git grep -lIE '^-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----'`
  returns nothing, which is the rule
  `scripts/ci/check-no-tracked-private-keys.py` enforces;
* `scripts/ci/apple-client-source.sh` still exposes `assert-shared-source` and
  no longer exposes the removed `assert-distinct`.

The repository-wide, Go-side Apple-adjacent checks (`experimental/libbox`,
`cmd/*`, `release/`) run as part of `go build ./...` and `go test ./...` and
pass.

---

## 9. Publication

Publication was blocked for four goal rounds by an environment failure, and is
recorded here because it affected how the work was verified:

```text
spawn <LOCAL_HOME>\AppData\Local\Programs\DeepSeek Harness\DeepSeek Harness.exe EACCES
```

The harness could not create **any** child process: `pwsh` failed on spawn and
so did `glob` (ripgrep), while the in-process `read` tool kept working. It was
an execution-permission denial, not a missing file — `read` opened that same
`.exe` successfully. The likely cause was resource exhaustion from the
concurrent test processes this task itself started; it cleared after the
operator cleaned up leftover `go` / `*.test` processes and restarted the app.

The GitHub MCP channel stayed healthy throughout (`get_me`, `list_branches`),
but a file-level API cannot reproduce merge history, so publishing through it
was never a valid substitute — it would have lost exactly the structure this
task exists to produce.

During the outage the remote was re-verified twice as untouched:

```text
testing                = 6cc45d3f49a3908ff150045d3a113f98920eaaea   (== TESTING_BEFORE)
remote branch count    = 14
remote tips            = unchanged, all 13 at their inventory SHAs
```

### How it was published, once the shell returned

```text
git fetch --all --prune                     => origin/testing still 6cc45d3f (no drift)
git rm -r --cached _consolidation           => scratch untracked again
  + .gitignore entry, committed              => 9aad25a4
git add transport/http/stream_error_buffer_test.go + report, committed => 7c819f5d
report update, committed                     => 88ff6cfe
git switch testing && git reset --hard origin/testing
git merge --ff-only integration/0.1.6-testing-consolidation   => 6cc45d3f..88ff6cfe, ff
git push origin testing                      => 6cc45d3f..88ff6cfe  testing -> testing
```

No force push was used at any point.

### Branch deletion

Each of the 13 branches had its ancestry **re-proved against the pushed
`testing`** immediately before deletion, rather than trusting the §4 table:

```text
for b in <13 branches>; do
  git merge-base --is-ancestor "origin/$b" origin/testing || { REFUSE; continue; }
  git push origin --delete "$b"
done

DELETED = 13   (all guards passed)
REFUSED = 0
```

The temporary `integration/0.1.6-testing-consolidation` needed no deletion
because **it was never pushed** — it existed only in the working clone. An
earlier draft of this report said it had been deleted from the remote; that
was wrong and is corrected here.

---

## 10. After

```text
TESTING_BEFORE              = 6cc45d3f49a3908ff150045d3a113f98920eaaea
TESTING_AFTER               = 88ff6cfe66c22557d1b5f3eb6e66b1cfd24543f0

REMOTE_BRANCH_COUNT_BEFORE  = 14   (13 feature/closure branches + testing)
REMOTE_BRANCH_COUNT_AFTER   = 1    (testing only)

REAL_MERGES                 = 3    (a85ba40e, a2abd572, de715829)
CHERRY_PICKS                = 0
ANCESTOR_SKIPS              = 10
PATCH_EQUIVALENT_SKIPS      = 0
SUPERSEDED_SKIPS            = 0

DELETED_REMOTE_BRANCHES     = 13
KEPT_REMOTE_BRANCHES        = testing

origin/testing == local testing == integration tip   =>  88ff6cfe (verified after push)
TESTING_FORCE_PUSHED        = NO
TESTING_WORKTREE_CLEAN      = YES
```

Final remote inventory:

```text
origin/HEAD -> origin/testing
origin/testing
```

`testing` was advanced by `git merge --ff-only` only, after re-checking that
`origin/testing` still equalled `TESTING_BEFORE`.

---

## 11. Acceptance gate

```text
[x] every remote branch inventoried at the outset (13 + testing, tips recorded)
[x] TESTING_BEFORE recorded (6cc45d3f49a3908ff150045d3a113f98920eaaea)
[x] work done on a temporary integration branch, not on testing
[x] no mechanical re-merge of ancestor branches (10 absorbed by ancestry)
[x] every independent work line absorbed (3 heads merged; all 13 proven ancestors)
[x] patch-equivalent branches proven (there are none; cherry showed 0 of them)
[x] superseded branches proven (none dropped on that basis)
[x] Apple gate/core branches audited separately (fix/0.1.6-apple-pin-gates,
    jiejiebox/apple-finalize, and the branding-surface conflict, §5)
[x] hardening branch audited separately (jiejiebox/v0.1.6-hardening, §2)
[x] all conflicts resolved on semantics, file by file (21 files, §5)
[x] frozen static-validation invariants regressed (PASS, §7.1)
[x] full build/test run (go build PASS; 172 packages, 82 ok / 87 no-test / 3 FAIL)
[x] new failures not disguised as pre-existing (every failure reproduced on a
    comparator; two genuine defects were found and fixed instead, §6)
[x] testing updated by fast-forward only
[x] no force push to testing
[x] testing SHA re-verified after push
[x] every deleted branch had SAFE_TO_DELETE = YES, re-proved at delete time
[x] temporary integration branch removed (never published, so nothing to remove)
[x] old 0.1.6 branches maximally cleaned (13 deleted, 0 kept)
[x] final remote branch list recorded (1: testing)
```

Not satisfied, and not claimed:

```text
[ ] stress across every package several branches touched — a narrowed fast set
    was run (10 packages, 111 iterations, 0 failures); the two traffic-shaped
    packages were measured against the baseline instead of repeated (§7.2b, §7.4)
[ ] RACE / DOCKER / APPLE_GATES — NOT_RUN_HOST_BLOCKED (§7.5); no Apple gate is
    recorded as PASS
```

---

## 12. Closing statement

All 0.1.6 branch work has been absorbed into `testing`; redundant remote
branches have been removed. `testing` is now the sole authoritative development
line for this release state.

