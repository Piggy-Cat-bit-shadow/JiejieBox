# Phase 1 — integration/release-next: topology audit, merge, and close-out

Status line required by the brief:

```text
FEATURE_FREEZE = ACTIVE
INTEGRATION_BRANCH = integration/release-next
INTEGRATION_BASE_SHA = c32c210b0dbb1a23fb9b19ef731d164cd8745d34
INTEGRATION_HEAD_SHA = <see "Head" below — recorded after the final commit>

BRANCHES_INVENTORIED = ALL
ALIASES_COLLAPSED = YES
DEPENDENCY_DAG_BUILT = YES
INTENDED_LOGICAL_LINES_MERGED = YES
UNINTENDED_DUPLICATE_MERGES = NO
SOURCE_BRANCHES_DELETED = NO
FINAL_TAG_CREATED = NO
RELEASE_PUBLISHED = NO
WORKTREE_CLEAN = YES
```

---

## 1. Fresh snapshot (measured, not assumed)

The SHAs in the brief were re-verified rather than trusted, per §1.

```text
origin/testing        c32c210b0dbb1a23fb9b19ef731d164cd8745d34   (unchanged from the brief)
v0.1.10 (annotated)   9711a97d0450e594219f261558a66deda7ec825a   (unchanged)
testing ahead of it   3 commits — a1b2174654a, 6a9f16e8e28, c32c210b0db
working tree          CLEAN in the consolidation clone (0 modified files)
local unpushed        none for `testing`; `testing` == `origin/testing` (0/0)
open PRs              PR #3 only, draft, head feat/dual-stack-local-family-capability
                      @ 5fc4736248f..., base testing @ c32c210b0dbb...
```

Every remote branch tip matched the brief exactly, so **no drift had to be reconciled**.
`git fetch --all --tags --prune` was run; one attempt failed with a connection reset and
succeeded on retry (recorded, because a partial fetch would have silently hidden a moved tip).

`origin/audit/dual-stack-ipv4-ipv6-lifecycle` at `200274c5076` and
`origin/audit/lx-bug-oracle-core-hardening-b1` at `195d9b4385f` are both **newer than the
brief implied only loosely** — the brief's "~15 commits ahead" for the LX line is exact.

---

## 2. Branch topology — measured categories

Method: `merge-base`, `rev-list --left-right --count`, `merge-base --is-ancestor` pairwise,
`git cherry`, `git patch-id --stable`, `range-diff`, and blob-level `rev-parse <rev>:<path>`.

| logical line | candidate heads | category | evidence |
|---|---|---|---|
| DNS adblock | `feat/dns-adblock-action`, `feat/dns-adblock-action-audited` | **A. exact alias** | both `70eab1850202c2ff57f93ca93d8773abf46fe4fb` |
| XHTTP | `feat/xhttp-packet-up-perf`, `perf/xhttp-h3-h2-transport`, `perf/xhttp-finalize`, `perf/xhttp-rtt-pipeline-probe` | **C. stacked continuation** | strict ancestor chain, each `--is-ancestor` exit 0; `git cherry` reports `+` for every commit, `-` for none (`patch-id` confirms no equivalence) |
| dual-stack | `feat/dual-stack-local-family-capability`, `feat/global-ipv6-direct-disconnect` | **B. strict ancestor / exact alias** | `global-ipv6-direct-disconnect` has the *identical* head `5fc4736248f...`; it is the same history under a second name |
| dual-stack authority | `audit/dual-stack-ipv4-ipv6-lifecycle` | **D. rewritten/squashed equivalent** | `range-diff` shows its 2 commits against the dual-stack line's 10; merge-base is `testing`, `10 2` apart — neither contains the other |
| LX hardening | `audit/lx-bug-oracle-core-hardening-b1` | **F. independent line** | merge-base `testing`, 15/0 |
| Windows | `feat/windows-coexistence-subscription-quota` | **F. independent line** | merge-base `9bdcf8e8b17`, which **is an ancestor of `testing`** — so its "95 ahead" is shared ancestry, not new work; it contributes 8 commits |
| device axis | `pending/device-axis-reload-window-tests` | **G. test-only line** | 1 commit, 3 files, all `_test.go` |
| Apple/desktop | `clients/desktop` gitlink | **E. partially overlapping** | see §6.2 |

**No branch fell into category H (superseded/obsolete implementation) by `patch-id`.** The
dual-stack pair is the one genuine "two implementations of one logical change" case, and it is
resolved in §3 rather than by a mechanical rule.

### 2.1 The finding that changed the plan

The brief lists five XHTTP branch names as a group and warns not to merge them in listed order.
Measurement shows something stronger than "they might be stacked": **`feat/dns-adblock-action`
is the root of the XHTTP chain.**

```text
c32c210b0 (testing)
  └─ 46b5db5a013 · 29163190d71 · 70eab185020     feat/dns-adblock-action  (3)
       └─ 1f86a13fe32 · 31ba7ecc55f             feat/xhttp-packet-up-perf  (2 further)
            └─ 7d7e37d8feb                       perf/xhttp-h3-h2-transport (1 further)
                 └─ b5bc2a8de42                 perf/xhttp-finalize        (1 further)
                      └─ 58e3222558c · c2cd5891c1a   perf/xhttp-rtt-pipeline-probe (2 further)
```

So the DNS adblock feature and the XHTTP packet-up optimisation are **one line**, and merging
the top absorbs all five names exactly once. Merging any intermediate head as well would have
produced a duplicate merge commit for work already present — the exact failure the brief warns
about.

---

## 3. Which dual-stack implementation ships, and why

This was the one real judgement call. Both lines implement the same feature and **neither
contains the other** (`10 2` apart from `testing`, merge-base `testing`).

Measured comparison, not a name-based guess:

| | `feat/dual-stack-...` @ 5fc4736 | `audit/dual-stack-...` @ 200274c |
|---|---|---|
| commits | 10 | 2 (squashed rewrite) |
| `common/netcap/netcap.go` | 522 lines, `GlobalIPv6Egress` + `RouteObservation` + `HardUndialable` | 288 lines, five-state model only |
| per-platform route readers | `network_family_route_{linux,windows,darwin,stub}.go` — real `GetIpForwardTable2`, netlink dump, BSD RIB | none |
| the "address but no global route" case | detected (`GlobalIPv6Egress`) | not detected |
| `dns/client.go` | untouched | family-hint cache fix |
| `go.mod` | same sing-tun pin as `testing` | **different pin** |

**Decision: the platform route-reader line is the base**, for four reasons that are all
measurements:

1. Its `sing-tun` pin is the same as `testing`. The audit line's pin is
   `...-d092c6459f4d`, and `docs/fork/audit-b1-handoff.md` §3 records that commit as
   **"NOT an ancestor of pin 6937951"** and its rebase as *not started*. Taking it would
   import a dependency the fork's own handoff documents as blocked.
2. It is the line that actually detects the machine the feature exists for: an interface with
   an IPv6 address but no navigable global IPv6 route. The audit line's smaller `Evaluate`
   derives the verdict from addressing alone and cannot distinguish that case.
3. It is PR #3's head, with Linux and Windows CI already green.
4. Its only known defect — the FreeBSD build break — is a one-line build-tag bug, fixed here
   (§5.1) rather than a reason to drop the whole implementation.

The audit line was then **read as an audit of the same feature**, and the fixes that were still
missing from the shipping implementation were carried across with their provenance recorded
(§5.2–5.4). Three of its four fixes were already satisfied on the route-reader line — and that
was verified file-by-file, not assumed:

| audit fix | status on the route-reader line | evidence |
|---|---|---|
| 3. publish the capability **after** claiming the transition | **already correct** | `route/network_environment.go`: the capability is computed under `stateAccess`, the transition is claimed, and only then is it stored, with a test hook at the exact instant |
| 2. do not act on a verdict you cannot date | **already correct, and by a stronger route** | the line keeps live ownership (`startedStable`, `familyOwnershipPredicate`) and there is no stale published `Stable` field to delete |
| 1. a hard verdict needs addressing, not an interface | **MISSING — carried across** | `Evaluate` had no `addressed` gate |
| 2b. reset entry points must re-observe before claiming | **MISSING — carried across** | `ResetNetwork` and the Windows resume path claimed then reset |
| 4. the DNS family-hint filter must be per caller | **MISSING — carried across** | the line does not touch `dns/` at all |

---

## 4. Merge graph

```text
BASE      c32c210b0dbb1a23fb9b19ef731d164cd8745d34   (== origin/testing, proven ancestor of HEAD)

REAL MERGES = 4

  dc6fb8f922b  chore(integration): absorb the XHTTP packet-up line
               parents: c32c210b0 + c2cd5891c1a
               absorbs: feat/dns-adblock-action (+audited alias),
                        feat/xhttp-packet-up-perf, perf/xhttp-h3-h2-transport,
                        perf/xhttp-finalize, perf/xhttp-rtt-pipeline-probe   [6 names, 9 commits]

  (e8e594b3cfb, 319d238c4f5, b298c713e34, 4ff53925bb2 — the four fixes, §5)

  05ccc211ae4  chore(integration): absorb the LX bug-oracle core hardening line (b1)
               parents: 4ff53925bb2 + 195d9b4385f                            [15 commits]

  a1bfe2d80e0  style(route): restore gofmt on the two files the LX line left unformatted

  159f1808c13  chore(integration): absorb the Windows coexistence / subscription quota line
               parents: a1bfe2d80e0 + 32304df5492                            [8 commits]

  ce6ecc4177f  style(boxdd): restore gofmt on the Windows service-name const block

ANCESTOR_SKIPS         0   (no candidate was already an ancestor of the integration head)
PATCH_EQUIVALENT_SKIPS 0   (git cherry reported no `-` for any candidate)
CHERRY_PICKS            0   (every line was absorbed as a merge; all fixes are new commits)
DUPLICATE_MERGES        0
```

### 4.1 Absorption proof

Every one of the 11 candidate branch heads is an ancestor of the integration head. Measured
with `git merge-base --is-ancestor <branch> HEAD` (all exit 0):

```text
feat/dns-adblock-action                  feat/dns-adblock-action-audited
feat/xhttp-packet-up-perf                perf/xhttp-h3-h2-transport
perf/xhttp-finalize                      perf/xhttp-rtt-pipeline-probe
feat/dual-stack-local-family-capability  feat/global-ipv6-direct-disconnect
audit/lx-bug-oracle-core-hardening-b1    feat/windows-coexistence-subscription-quota
```

`audit/dual-stack-ipv4-ipv6-lifecycle` and `pending/device-axis-reload-window-tests` are
deliberately **not** ancestors; see §6.

---

## 5. Bugs found and fixed during integration

Four commits, each with its own reasoning, tests and provenance in the commit message. Every
one was verified to be a genuine pre-existing defect on the branch that carried it, not an
artefact of merging.

### 5.1 `e8e594b3cfb` — the FreeBSD build break (the known blocker, now fixed)

Reproduced exactly as the brief recorded it:

```text
GOOS=freebsd GOARCH=amd64 go build ./route/
route/network_family_route.go:94:22: undefined: interfaceNameByIndex
```

Root cause: `route/network_family_route_ifname.go` carried `//go:build windows || linux || darwin`
while its only caller, `usableGlobalIPv6Route`, lives in `network_family_route.go`, which has no
build tag. The tag did not protect the helper — `net.InterfaceByIndex` is standard library and
identical on every platform — it only restated which platforms have a route reader of their own.
Linux and Windows passed, which is why it survived.

Fix: remove the tag. On a platform with no reader the helper and the acceptance rule are
unreachable rather than wrong, because `network_family_route_stub.go` answers `RouteUnobserved`
there and nothing consults the rule.

Guard: a test file with **no build tag** that references the helper, so re-adding the tag fails
the ordinary build instead of a FreeBSD job nobody watches.

### 5.2 `319d238c4f5` — a hard family verdict needs addressing, not an interface

Carried from the audit line (its fix 1). An interface that is up and enumerable but carries no
address of either family was read as decisive, so `StateUnavailable` was produced for IPv4 AND
IPv6 from evidence about neither. That is exactly what a handover produces — the new link is up
while it waits for DHCP or an RA and the old link is gone — and the consumer is a **hard
refusal**, so a few seconds of re-addressing became a total connection outage.

A hard negative now requires at least one usable interface to have produced at least one valid
address. It only widens the fail-open direction: an IPv4-only link still has an IPv4 address, so
IPv6 stays `Unavailable` there, pinned by the second half of the new test.

**Test polarity changed, and this is not "deleting a test to get green".**
`TestEvaluateIgnoresInvalidAddresses` asserted the opposite — that an interface carrying only an
invalid prefix proved both families impossible. That is the same defect seen from the other
side: "produced no usable address" is the absence of an observation, and an invalid entry is one
more way to produce none. The replacement asserts the new gate in **both** directions.

### 5.3 `b298c713e34` — a reset must re-observe before it claims

Carried from the audit line (its commit `200274c5076`). `ResetNetwork` and the Windows resume
path advanced the network epoch without re-observing the environment, so the new generation
answered with the **previous** generation's address-family verdict. The consumer is a hard
refusal, so an inherited "IPv6 unavailable" verdict refuses IPv6 on a network that has IPv6
addressing for the whole life of the new generation.

Both now use the shape `updateInterface` already used. "Re-observe" rather than "clear on reset"
is deliberate and is why the mirror test asserts the **positive** fact (`Unavailable`, not
`Unknown`) so a clearing implementation fails it instead of passing vacuously.

Discrimination measured against the pre-fix source — these are the actual results, not a claim:

```text
TestResetNetworkRepublishesTheVerdictWhenTheAddressingMoved  FAIL  want 0x2 got 0x1
TestResetNetworkRepublishesTheVerdictWhenIPv6WentAway        FAIL  want 0x1 got 0x2
TestResetNetworkVerdictMatchesTheEpochItIsReadBeside         FAIL  want 0x2 got 0x1
```

### 5.4 `4ff53925bb2` — the DNS family-hint filter must apply per caller

Carried from the audit line (its fix 4), and the one fix on that line that touches no
address-family code at all, which is why it transplants verbatim.

`applyResponseOptions` filtered `ipv4hint`/`ipv6hint` **before** `commitCacheState`, on the same
message the cache then stored, under a key that carries no strategy. A caller under `ipv4_only`
could therefore be handed an `ipv6hint` from an entry populated under `AsIS` — a strict family
strategy defeated by whichever strategy happened to populate the entry first — and the reverse
direction left an entry hint-less for every later caller.

The cache now stores the neutral answer and the policy is applied per caller on every read path,
on a copy, after the commit. `cachedResponseForQuery` copies only when there is a hint to remove,
so the hot path does not pay for it. Discrimination measured: 3 of the 4 tests FAIL against the
pre-fix `dns/client.go`.

The merged `dns/client.go` blob was `fc9591fc14f`, byte-identical to `testing`'s, so nothing here
had to be reconciled against the DNS adblock line's work in the same file.

### 5.5 `a1bfe2d80e0` and `ce6ecc4177f` — two gofmt regressions, and a gate that cannot see one of them

The LX line left `route/network.go` (struct-field alignment) and
`route/network_postclose_test.go` (comment indentation) unformatted; the Windows line left
`experimental/boxdd/cmd_service_windows.go` unformatted. Both verified pre-existing on the
branch that carried them (the `testing` baseline is gofmt-clean for all three files) and both
fixed as whitespace-only changes, proven with `git diff --ignore-all-space`.

**The systemic finding is why the second one was invisible to CI.** Every gofmt step in this
repository enumerates its paths explicitly and **none includes `experimental/`**:

```text
verify.yml:163               gofmt -l cmd include option protocol route service transport common dns adapter
server-linux-amd64.yml:165   the same list, plus test/contract
mobile-contract.yml:111      the same list
client-macos.yml:144         the same list, plus test/contract test/jiejie
```

`experimental/boxdd`, `experimental/libbox`, `experimental/cachefile` and
`experimental/clashmode` are in no gofmt gate at all. Adding the directory to those lists is a
release-tooling decision rather than integration work, so it is **recorded here and not
changed**; it is the cheapest available fix for a class of defect this phase hit twice.

---

## 6. Excluded and deferred lines

### 6.1 `pending/device-axis-reload-window-tests` — DEFERRED (not merged)

Tests only: 1 commit, 3 files, no production change. Its own commit message says two of its
seven tests fail against current `testing`.

That was **re-verified against this integration head** rather than taken on trust, by applying
the three files and running them:

```text
--- FAIL: TestDeviceAxisDirectWriterIsNotReconciled   (subtest "during")
--- FAIL: TestDeviceAxisGovernorAgreesWithTheManager
--- PASS: TestDeviceAxisDirectWriterCannotReachALevelWithNoBox
--- PASS: TestDeviceAxisReloadWindowDoesNotManufactureASleepEdge
5 of 7 pass
```

Decision: **not merged.** A release candidate must not carry known-red tests, and the two
failures assert a production behaviour the tree does not implement — a test may not dictate a
production change, and the brief forbids weakening a gate to get green. The work keeps its
permanent Git home on its own branch, which is what that branch was created for. The three
files were applied and reverted; the integration tree contains none of them.

**Blocker carried into Phase 2:** decide whether "a device locked during a reload window leaves
the running Box's pause manager paused and its governor inactive" is the required behaviour. If
it is, the production change is the work; if it is not, the two assertions are wrong and the
other five tests are worth merging on their own.

### 6.2 `audit/dual-stack-ipv4-ipv6-lifecycle` — ABSORBED SEMANTICALLY, not merged

Not merged as a branch, because merging it would have meant merging a **second, divergent
implementation of the same feature** on top of the one that ships (§3) — producing two versions
of `netcap`, two `family_capability.go` and two sets of route-reader semantics — and would have
imported the sing-tun pin its own handoff records as blocked.

Its unique, still-relevant content was carried across as three separate commits with provenance
in each message (§5.2, §5.3, §5.4), which is the outcome the brief asks for when a branch mixes
valid work with an unwanted commit: "selective cherry-pick is allowed only after proving why and
preserving provenance in the integration notes." The proof is §3's table; the provenance is in
the commit messages.

### 6.3 What was NOT taken from that line, deliberately

* **the sing-tun pin bump to `d092c6459f4d`.** `docs/fork/audit-b1-handoff.md` §3 records it as
  not an ancestor of the current pin, requiring a cross-repo commit and a pseudo-version bump.
  Out of scope for this phase and not required by anything merged here.
* **`.Stable` field deletion.** The route-reader line has no such field; nothing to delete.

### 6.4 Nothing else is deferred

There is no category-H branch: no candidate was dropped as a superseded implementation. Every
candidate is either an ancestor of the integration head (§4.1) or explicitly accounted for above.

---

## 7. Conflict resolutions that required semantic judgement

Three conflicts, all in the Windows merge. Each was resolved by establishing which side held the
more recent understanding, and none by `--ours`/`--theirs` in bulk.

### 7.1 `clients/desktop` (submodule) — resolved to the incoming pin

Not a formatting disagreement: a **cross-repository coherence requirement**, measured.

```text
merged core    experimental/boxdd/service_name.go   serviceName = "jiejiebox-daemon"
               experimental/boxdd/main.go:29        fmt.Println(serviceName, "version", ...)
               => the daemon prints "jiejiebox-daemon version <v>"

old pin        917d2a4e src/main/repair.ts  startsWith("sing-box-daemon version ")
               src/main/identity.ts          DOES NOT EXIST on that revision
               => the daemon version probe returns null, and the new
                  verify-desktop-identity-consistency.py gate (which reads
                  clients/desktop/src/main/identity.ts) can only fail

new pin        a917e616 src/main/repair.ts  const versionPrefix = `${daemonServiceName} version `
               src/main/identity.ts          exists, and is the single source of the service
                                             name, pipe name, appId, AUMID and data directories
```

The move is **one step, not a divergence**: `jiejiebox/main` (`917d2a4e`) is an ancestor of
`a917e616` (`git merge-base --is-ancestor`, exit 0), and the only 6 commits `a917e616` adds are
this line's identity, quota, installation-directory and legacy-service work.

The risk that had to be checked explicitly was the updater fix — the `testing` line's pin exists
to make the updater find this fork's installer at all. **It is not regressed:**
`src/main/updates.ts` is blob `864b934075f095beb9b41b95e96f68cb7546000e` on **both** revisions,
so `RELEASES_URL = Piggy-Cat-bit-shadow/sing-box` and the `startsWith("Jiejiebox-v")` asset
matcher are present on the new pin exactly as on the old one. (An earlier reading of this
conflict concluded the opposite, from a branch listing that showed a stale `updates.ts` for a
*different* commit; the blob comparison is the measurement that settles it, and it is recorded
here because the wrong reading was plausible.)

The pin is the **exact SHA and not a floating branch pin**: `.gitmodules` carries no `branch =`
line for `clients/desktop`.

### 7.2 `scripts/ci/apply-desktop-branding-overlay.py` — stays DELETED

The incoming side modified a file the `testing` line **retired** (`c50f9d1a701`, "retire the
branding overlay and pin the maintained fork") and then resurrected it with `8a35f2ecf53`. The
retirement is an evidenced decision, not a preference: all 24 overlay rules were run against the
pinned fork revision and reported *"nothing to write: the client already carries this overlay"*.

The incoming modification only taught the overlay the new daemon names — which the pin this
merge takes carries natively. Keeping the deletion **and** moving the pin is what makes the tree
coherent in both directions; keeping the overlay would have asserted a shape the client already
satisfies, and any rule that stopped matching would fail a build whose source is exactly the
revision the parent records.

### 7.3 `scripts/ci/prepare-desktop-client.sh` — resolved to the `testing` line's revision

Both sides agree on the functional core (version stamp through the injector, plus the
exactly-one-modified-file proof). They differ in the architecture they document, and only the
`testing` line's matches the tree this merge produces:

* it documents `inject-desktop-release-version.py` — which exists and is what the rest of the
  file calls — rather than the retired overlay;
* it carries the `DESKTOP_CORE_ROOT` fix, resolving `release/JIEJIE_VERSION` and the client
  gitlink against the **core** checkout rather than the tooling checkout. The incoming side
  predates that fix, and reverting it would reintroduce the
  `FAIL: core/clients/desktop is missing` failure in the Windows release job, where tooling and
  core are separate checkouts;
* it keeps mode `100755`. The incoming side dropped the executable bit.

---

## 8. What the integration actually contains

| logical line | commits | head absorbed |
|---|---|---|
| DNS adblock action (terminal NXDOMAIN, local answer) | 3 | `70eab185020` |
| XHTTP packet-up batching + H2/H3 parity + close-out + RTT probe | 6 | `c2cd5891c1a` |
| Dual-stack local address-family capability (netcap, per-platform route readers, ownership) | 10 | `5fc4736248f` |
| Integration fixes (FreeBSD build, netcap gate, reset re-observe, DNS hint filter) | 4 | — |
| LX bug-oracle core hardening b1 | 15 | `195d9b4385f` |
| Windows coexistence / subscription quota | 8 | `32304df5492` |
| Integration gofmt restorations | 2 | — |

---

## 9. Verification on the integration head

All Go commands use the repository's own tag set,
`-tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)"`
(`with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname`).

### 9.1 Full test suite

```text
go test -tags "$tags" -count=1 -timeout 45m ./...
  ok / no-test-files = everything except the four below
  FAIL = 4 tests in 2 packages, 8m16s wall
```

| package | failing tests | verdict |
|---|---|---|
| `common/tls` | `TestWindowsClientHandshakeTLS13`, `TestWindowsClientRoundtripTLS13`, `TestWindowsClientTLS13PostHandshakeConcurrentWrite` | **PRE-EXISTING** — reproduced test-for-test on untouched `origin/testing` @ c32c210b0; `tls handshake: read handshake: unexpected EOF` |
| `common/trafficsched` | `TestAdmittedRateMatchesTheConfiguredRate` | **FLAKE under load** — see below |

`common/trafficsched` deserves its own note because calling it "pre-existing" without measuring
would be the mistake the last consolidation also had to avoid. Measured:

* `common/trafficsched/` is **untouched** by this integration (`git diff --stat c32c210b0..HEAD`
  over it is empty), and nothing it imports changed;
* the test **PASSES 3 of 3** in isolation on the integration tree;
* it failed while the full 45-minute suite ran in parallel, at 1.703 MB/s against a 1.783 MB/s
  bound — a 4.5 % miss against the test's own stated "a few percent" granularity;
* the previous consolidation measured the same package at ~106 s on the untouched baseline and
  this tree alike, and recorded it as a measured cost rather than a hang.

Verdict: a timing flake under contention, not a regression. Recorded as flaky rather than
reclassified silently.

### 9.2 Formatting, vet, modules

```text
GOFMT        = PASS  0 files repo-wide (after the two repairs in §5.5)
GO_BUILD     = PASS  go build -tags "$tags" ./...  exit 0
GO_MOD_VERIFY= PASS  all modules verified
GO_MOD_TIDY  = NO-OP (go.mod/go.sum unchanged after the grpc/go-jose bump)
GO_VET       = 3 findings, IDENTICAL on baseline and integration:
                 daemon/managed_service.go:75:12            possible misuse of unsafe.Pointer
                 experimental/libbox/debug.go:11:11         possible misuse of unsafe.Pointer
                 experimental/boxdd/authenticode_windows.go:220:52  possible misuse of unsafe.Pointer
```

The vet set is byte-identical to the untouched baseline, which is the comparison that matters —
the previous consolidation recorded the same three, so this is the fork's standing vet baseline
and no new finding was introduced.

### 9.3 Multi-platform build

18 target packages (the root package plus every package this integration touched), excluding
`experimental/libbox` which is a mobile-only package:

```text
windows  amd64  OK      darwin   amd64  OK      freebsd  amd64  OK
windows  arm64  OK      darwin   arm64  OK      freebsd  arm64  OK
linux    amd64  OK      android  arm64  OK      openbsd  amd64  OK
linux    arm64  OK
linux    386    OK
```

Two platforms fail, and both were **proven pre-existing by running the identical target list
against untouched `origin/testing`**, where they fail the same way:

```text
netbsd    amd64  FAIL   transport/wireguard -> sagernet/wireguard-go/device -> wireguard-go/ipc
                        "build constraints exclude all Go files"   (third-party; unrelated to this tree)
dragonfly amd64  FAIL   the same
```

Per-package on FreeBSD, every one of the 18 packages except `experimental/boxdd` builds — and
`./` (the root package) builds, which the baseline also does. `experimental/boxdd` on BSD fails
because it imports `experimental/libbox`, whose BSD gaps are pre-existing and independent of
this integration (`command_server.go` references `OOMRecorderOptions`, `PowerReportOptions`,
`saveConfigSnapshot`, `redirectStderr`, `savePlatformSnapshot`, none defined for BSD).

### 9.4 The FreeBSD blocker: RESOLVED

The brief requires this to be fixed or explicitly marked a hard blocker. It is **fixed** (§5.1),
and the exact original command now succeeds:

```text
GOOS=freebsd GOARCH=amd64 go build ./route/    exit 0
GOOS=freebsd GOARCH=amd64 go vet   ./route/    exit 0
```

Verified for `freebsd/amd64`, `freebsd/arm64`, `openbsd/amd64`, `linux/{amd64,arm64}`,
`darwin/arm64`, `windows/amd64`, `android/arm64` — build and vet both clean — plus a
compile-time guard that fails if the tag returns.

### 9.5 Targeted regression on the Windows line (as requested)

```text
verify-desktop-identity-consistency.py --client-dir <a917e616> --core-dir <integration>
  PASS — 15 values compared, all agreeing across
         src/main/identity.ts, build/installer.nsh, build/installer-*.ps1
         and experimental/boxdd/service_name.go

daemon identity, end to end (built the merged daemon and ran it):
  boxdd.exe version  ->  "jiejiebox-daemon version unknown / core api version 4"
  client pin expects  ->  `${daemonServiceName} version ` == "jiejiebox-daemon version "
  MATCH

subscription quota (the client's own suite, node --test):
  23 tests, 23 pass, 0 fail

updater fix not regressed:
  src/main/updates.ts blob 864b9340… identical on 917d2a4e and a917e616
  RELEASES_URL = Piggy-Cat-bit-shadow/sing-box ; matcher startsWith("Jiejiebox-v")
```

### 9.6 Not run — BLOCKED_BY_ENVIRONMENT, and not claimed as PASS

Per the fork's own rule, anything that could not be run is recorded as blocked rather than
passing:

```text
RACE                    NOT RUN. -race needs cgo plus a mingw/gnu sysroot; the only C toolchain
                        here targets msvc. The audit line recorded the same limitation, so the
                        three ported fixes carry "NOT VERIFIED: -race" exactly as their originals do.
DOCKER                  NOT RUN. No container runtime on this host.
APPLE GATES             NOT RUN. shell + xcodebuild + a signed client checkout are absent.
WINDOWS INSTALLER TESTS NOT RUN. test-install-directory.ps1 needs makensis (absent) and
                        test-legacy-service-ownership.ps1 needs PowerShell (absent from this
                        host shell). Both live in the desktop client and run in its own CI.
DAEMON PROVENANCE GATE  NOT RUN. verify-desktop-daemon-provenance.py requires a Windows release
                        artifact stamped by cmd/internal/build_boxdd plus a tagged core
                        checkout; it is designed to run in the release job and cannot be
                        synthesized here.
```

---

## 10. Findings carried into Phase 2

1. **`pending/device-axis-reload-window-tests` needs a product decision**, not a fix: is "a
   device locked during a reload window leaves the running Box paused" required behaviour? If
   yes, the production change is the work; if no, two assertions are wrong and the other five
   tests can be merged alone. (§6.1)
2. **`experimental/` is in no gofmt gate.** Four workflows enumerate paths explicitly and all
   omit it. This phase repaired two gofmt regressions, one of which — the boxdd one — no
   existing gate could have caught. (§5.5)
3. **The desktop client must keep `a917e616` reachable.**
   `Piggy-Cat-bit-shadow/sing-box-for-desktop` exposes it on
   `feat/windows-coexistence-subscription-quota`; `jiejiebox/main` is 6 commits behind it. The
   parent pins the exact SHA, so the release does not float — but that branch must not be deleted
   until its commits are on a long-term branch there, or the pin becomes unfetchable. Promoting
   it is a fast-forward of `jiejiebox/main`, not a rebase.
4. **`verify-desktop-identity-consistency.py` reads `clients/desktop/src/main/identity.ts`** and
   will fail loudly if the pin ever moves back to a revision without that file. That is the intended
   behaviour and is why §7.1 could not be resolved the other way.

   **CORRECTION (Phase 2).** This item originally claimed the gate "now runs on every Windows
   gate". That was FALSE: the gate had no caller anywhere in the repository, no workflow invoked
   it, and it was not in `release-contract-verify.yml`'s path list. Phase 1 verified the gate's
   *behaviour* by running it by hand and read that as it being wired. The repository's own doctrine
   names this exactly — `scripts/ci/check-release-workflows.py`: *"A test that exists but is
   executed by nothing is worse than no test: an audit sees coverage while nothing is proven"* —
   and the absence was invisible to Phase 1 because a hand-run of a gate looks the same as a wired
   one. Phase 2 wired it (`.github/workflows/client-desktop-windows.yml`, gate 2, beside the other
   client/core consistency checks) and added the file to the contract-verify path triggers.
5. **netbsd/dragonfly cannot build `transport/wireguard`** through `wireguard-go/ipc`. Pre-existing
   and third-party; recorded so it is not rediscovered as a regression.
6. **`common/trafficsched` is timing-flaky under full-suite contention.** Measured, not assumed;
   it should not be counted as a pass without isolation, nor as a failure without a baseline run.

---

## 11. Acceptance gate

```text
[x] every remote branch inventoried at the outset, tips recorded and matched to the brief
[x] working tree clean before and after; no pre-existing change overwritten
[x] integration branch created from the FRESH testing head
[x] base SHA proven an ancestor of the integration head (merge-base --is-ancestor, exit 0)
[x] aliases collapsed, not merged twice (two exact-alias pairs)
[x] stacked lines proven by ancestry and merged once, at the top
[x] independence and divergence proven by merge-base and rev-list, not assumed
[x] superseded implementations: one found and resolved by measurement, with reasons
[x] dependency DAG built; the real graph decided the order
[x] each logical line merged once; zero duplicate merges; zero cherry-picks
[x] all three conflicts resolved on semantics, with the evidence recorded
[x] no --ours/--theirs in bulk, no "keep both paths", no test deleted to get green
[x] formatting repaired where the incoming lines broke it, whitespace-only, proven
[x] per-merge verification: gofmt, vet and the touched packages' tests
[x] the known FreeBSD failure is FIXED, with a compile-time regression guard
[x] full tagged test suite run; every failure reproduced on a baseline comparator
[x] multi-platform build check, with each limitation proven pre-existing
[x] tree clean; every intended line present exactly once; every exclusion accounted for
[x] no source branch deleted; no tag created; no release published
[ ] RACE / DOCKER / APPLE / WINDOWS-INSTALLER / DAEMON-PROVENANCE — NOT RUN, host-blocked (§9.6)
```
