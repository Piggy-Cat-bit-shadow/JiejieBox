# PAIRWISE C — Discovery catalog

What this window actually scanned, in what form, and what came out of it. Sources are split by
availability, because a source that could not be read is a limit, not a result.

## 1. Sources: what was read, what was not

| Source | Access | What was used |
| --- | --- | --- |
| `AGENTS.md` (repo root) | read | the knowledge-base reading order, the evidence rules, the three recurring mistakes (provenance, evidence, tags) |
| `KNOWLEDGE/BASELINE.md` | read (head) | that the knowledge baseline is `v0.1.34`/`0067b03e` with scoped refreshes up to `v0.1.46` — i.e. **well behind `HEAD`**, so every KNOWLEDGE claim was treated as a hint, never as current fact |
| `KNOWLEDGE/INDEX.md` | routed (grep) | the module router entries for admission, `runtimecoord`, generations |
| `KNOWLEDGE/modules/resource-admission.md` | read | the admission/OOMKiller contract, the historical defects list, the "AI cautions" |
| `KNOWLEDGE/INVARIANTS.md`, `ACCEPTANCE.md` | **not read** | time-boxed out; the invariants used in this window are derived from source comments and the A/B audits instead |
| `docs/fork/*` (listing) | names only | `ios-resource-guard`, `resourceguard-protocol-coverage`, `resourceguard-adversarial-debug`, `go-1274-regressions`, `interface-update-lifecycle`, `post-wake-reuse`, `dual-stack-audit-2026-10-27` exist; only the ones quoted by source comments were relied on, and only as citation, not as evidence |
| A window audit (`docs/audits/pairwise-a-resource-lifecycle-2026-11-19/`) | read (own output) | R-1…R-11, the capability matrix, the "no job runs it" census |
| B window audit (`docs/audits/pairwise-b-network-dns-tun-2026-11-19/`) | read (own output) | R-B1…R-B10, the interaction map, the mutation controls |
| Upstream checkout `C:\Deepseek\sing-box` | present, **not diffed** | the fork is heavily diverged (~1951 changed paths at the last calibration); a full diff was not going to be read in this window, and an unread diff is not evidence |
| `.github/workflows/*` | read (excerpts) | `verify.yml`, `resource-guard.yml`, `linux-kernel-verify.yml`, `mobile-contract.yml`, `specialist-verification.yml`, `server-linux-amd64.yml` — used for the execution census (which package is run by which job) |
| `scripts/ci/clienthello-probe/main.go` | read | the toolchain/ClientHello measurement that made B-1's class of defect visible |
| **Upstream Issues / PRs** | **web search only, blocked** | one query (`sing-box segfault/deadlock/goroutine leak` on GitHub) returned **documentation pages only** — [migration.zh.md](https://github.com/sagernet/sing-box/blob/1ac1a339cb1223e9c70eae14c44411c75033c02d/docs/migration.zh.md), [rule_action.zh.md](https://github.com/sagernet/sing-box/blob/0b8995879f29a9b98ee027bc17b75e101445b238/docs/configuration/dns/rule_action.zh.md), [changelog.md](https://github.com/sagernet/sing-box/blob/253b41936ecd6ae17948d49d9c510d7100830927/docs/changelog.md) — no issue list. `git fetch`/`push` to the origin also needed retries against `Recv failure: Connection was reset`. **No Issue/PR claim is made in this window.** |
| Fork scans (Anywhere, `shtorm-7/sing-box-extended`, Clash/Mihomo, Hiddify, FlClash) | **not accessed** | no network path to them in this session |
| `C:\Deepseek\cronet-go`, `sing-tun`, `sing-anytls`, `mieru`, `quic-go` | read where a finding needed them (A/B windows) | dependency evidence for the naive/TrustTunnel/QUIC findings |

**Conclusion for the catalog:** this window's discoveries are source-and-execution driven. The
Issue/PR circle of the brief is recorded as `BLOCKED` for network reasons rather than replaced with
plausible-sounding references.

## 2. Candidate table

Seeds from the brief's §4.1 pool plus the "spread the A/B failure patterns" sweep. Weight is the
brief's own scoring, compressed: **H**igh / **M**edium / **L**ow, and "dedup" says what already covers
it.

| # | Candidate combination | Shared state / resource | Weight | Dedup vs A/B | Verdict |
| --- | --- | --- | --- | --- | --- |
| C-1 | **Generation gate listener half** × push notification | the gate ledger `resetEpoch`/`resetDone` vs the listener phase's direct call | **H** | B recorded it as R-B1 with the diagnosis, **not** fixed | **CONFIRMED_BUG, FIXED** (this window) |
| C-2 | **Fake-IP generation cursor** × hot reload (no `store_fakeip`) | the in-process issuance ledger vs the restarted allocator cursor | **H** | B recorded it as R-B8, **not** fixed | **CONFIRMED_BUG, FIXED** (this window) |
| C-3 | Health probe / URL test × selector × recovery state | `urltest` coordinator queue, probe budget, group selection | H | B did not attack; A did not touch | NOT ATTACKED — needs a group fixture plus a probe server; left as the next-round first candidate |
| C-4 | Sniff / ClientHello parse × payload rewind | the first packet's ownership across sniff attempts | M | `common/sniff` has 20+ tests; B's A09 was not attacked either | NOT ATTACKED |
| C-5 | Config validation × client import / hot reload | `option` decode vs `box.New` semantic validation | H | B's R-B9 (stop-then-build, no preservation) already records the ordering defect | **ALREADY_RECORDED** (R-B9) — a fix is a lifecycle redesign |
| C-6 | Android root TUN × auto_route / VPNService double entry | kernel rules + service entry | M | platform-only | **NOT_APPLICABLE** in this environment |
| C-7 | Apple On-Demand × config switch | NE provider parameters vs client config | M | platform-only | **NOT_APPLICABLE** |
| C-8 | Cronet native callbacks × Go ctx cancel / OOM limit | native engine lifetime vs Go task lifetime | M | A window touched naive's creation seam and its shed hook | **ALREADY_COVERED** for the Go side; the native side is unobservable here |
| C-9 | Diagnostics × OOM Critical | `guard.Stats()` side effect from the report path | M | A recorded it as R-8 (documented decision, pinned) | **ALREADY_RECORDED** |
| C-10 | NAT/UDP timers × sleep/background wake | NAT cleanup heap vs suspend | M | A's R-6 (download-only flows expire) and sing-tun's timer docs | **ALREADY_RECORDED** (cross-repo) |
| C-11 | Rule-set / MRS update × active routing / reload | rule-set artifacts vs the reload swap | M | `route`'s rule-set tests; the Windows temp-dir flake found in B is in this area | NOT ATTACKED |
| C-12 | System proxy / SOCKS port × TUN recapture | the local listener's own traffic vs TUN include rules | M | B's B08 was not attacked | NOT ATTACKED |
| C-13 | H3-only policy × fallback / health | the configured promise vs the demotion path | H | B's R-B3 (silent demotion) records the observability gap; R-B4 the naive window | **ALREADY_RECORDED** |
| C-14 | Proxy chain detour × admission | one logical dial charged per hop | M | A's A03 checked `HeldPermit`; the fork has `detour_wire_order_test.go` | **ALREADY_COVERED** by existing tests |
| C-15 | GOMEMLIMIT / GC × native footprint | Go heap vs RSS | M | A's A10 was not attacked | NOT ATTACKED (needs a device) |
| C-16 | DNS negative cache × NAT64/PREF64 / family racing | NXDOMAIN persistence vs the family plan | M | B's B06 was not attacked | NOT ATTACKED |
| C-17 | TLS/uTLS fingerprint × session reuse / hot reload | the fingerprint config vs a live session | M | B-1's class (toolchain defaults) is the adjacent, now-fixed case | NOT ATTACKED |
| C-18 | TUN packet buffers × close / error return | reusable buffers vs a returning write | M | `sing-tun` + `protocol/tun` tests | NOT ATTACKED |
| C-19 | Metrics × real business path | counters vs native/QUIC/UDP branches | M | A's A08 partly; the report keys are additive | NOT ATTACKED |
| C-20 | Background workers × Stop/Restart | watcher/goroutine lifetime across a reload | H | A's A02 (pool teardown) and B's R-B1 (double transition) | **PARTIALLY** — C-1 is a member of this family |

Weighting reflects *reachability × shared-owner depth × observability*, not severity alone: C-1 and C-2
won because both are on the production path for every user, both have a measurable wrong outcome
(session destroyed / wrong destination), and both are provable here without a device.

## 3. What the catalog says about the scan itself

- The A/B audits were the highest-yield input: two of the three candidates that were provable here came
  from their "recorded, not fixed" sections. A recorded finding with a file:line and a test sketch is
  a **work item**, not a note.
- The external circle was unusable (`BLOCKED`), so the "Issue/PR-driven" half of the brief is honestly
  absent. What replaced it was the *pattern-driven* sweep: take the failure shapes A/B confirmed (an
  unrouted listener; a state that survives where its evidence does not) and search the tree for the
  same shape. Both fixes below came from that sweep.
- The largest untouched area by this three-window effort is the **group/health-probe** family (C-3,
  C-13) and **reload atomicity** (C-5): both interact with everything else and neither has been
  attacked end-to-end.
