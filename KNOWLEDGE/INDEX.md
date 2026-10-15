# Knowledge index — route first, read narrowly

**This file answers one question: "I need to know X — where do I read?"** It is a router, not a
second knowledge base. Entries are pointers; the truth lives in the module.

> **Never bulk-read `KNOWLEDGE/`.** Route here first, then open the one or two sections the task
> needs. Every anchor below was verified to exist at the knowledge revision.

## Entry points, by load level

| File | Load when | Why |
|---|---|---|
| `../AGENTS.md` | **ALWAYS** | the three things that most often go wrong here |
| `README.md` | **ALWAYS** | baseline pointer, status labels, evidence rules |
| `INDEX.md` (this file) | **ALWAYS** | routing |
| `BASELINE.md` | **ALWAYS** before trusting any module | the SHA the modules describe, and the delta since |
| `modules/README.md` | TASK_MATCH | task → module map, fork vocabulary, coverage gaps |
| `PROJECT.md` | TASK_MATCH | what the fork is and deliberately did not change |
| `ARCHITECTURE.md` | TASK_MATCH | the eleven boundaries worth preserving |
| `INVARIANTS.md` | **LOAD_BEARING_ONLY** | A1–A20 hard invariants vs conventions |
| `ACCEPTANCE.md` | **VERIFICATION_ONLY** | requirement → evidence → re-check command |
| `COMMIT-INDEX.md` | **PROVENANCE_ONLY** | the SHAs behind a behaviour, grouped by theme |
| `REFRESH.md` | **REFRESH_ONLY** | the refresh protocol; not a task mode |
| `docs/archive/**` | **HISTORY_ONLY** | how we got here; never current truth |

## Route by task

| If you are changing / asking about… | Module | Exact section |
|---|---|---|
| `Scope` teardown, `scope.Add` cleanup | `modules/core-lifecycle-runtime.md` | §1 `adapter.Scope` — the ownership primitive |
| Box start order | `modules/core-lifecycle-runtime.md` | §2 → Start (`box.go:1024-1217`; `PreStart` 1024, `Start` 1034, `preStart` 1069, `start` 1139) |
| Box close order, teardown | `modules/core-lifecycle-runtime.md` | §2 → Close (`box.go:1219-1253`; `Close` 1219, `close` 1233) |
| network generation, rebuild lease, `runtimecoord` | `modules/core-lifecycle-runtime.md` | §3 one generation, identity-based leases |
| `TrimMemory` vs `ReleaseMemory` | `modules/core-lifecycle-runtime.md` | §3 `TrimMemory` and `ReleaseMemory` are different |
| idle retirement inventory, `route/reference.go` | `modules/core-lifecycle-runtime.md` | §4 `route.ReferenceManager` |
| `TrimIdleResources` / `CloseIdleConnections` / `RetireSuspect` | `modules/core-lifecycle-runtime.md` | §4 that trio |
| start-time validation, `PhysicalPath`, `ControlPath` | `modules/path-selection-validation.md` | §1 the three things that are not the same |
| `ControlPath` direction | `modules/path-selection-validation.md` | §1 why `ControlPath` is NOT reversed |
| what validation actually checks, in order | `modules/path-selection-validation.md` | §2 start-time validation |
| `common/routeflow`, provable demand | `modules/path-selection-validation.md` | §3 |
| selector/urltest groups, preview vs committed | `modules/path-selection-validation.md` | §4 selection is a preview until committed |
| cycles, read-only guards | `modules/path-selection-validation.md` | §6 cycle detection |
| `RequiredNetworks` — **not** a contract | `modules/path-selection-validation.md` | §5 an implementation detail, NOT a contract |
| `fast_path` on a group | `modules/fast-path-group-policy.md` | §4 three-state semantics |
| which outbound types may carry `fast_path` | `modules/fast-path-group-policy.md` | §5 |
| what `fast_path: true` does **not** bypass | `modules/fast-path-group-policy.md` | §6 |
| nested group precedence | `modules/fast-path-group-policy.md` | §8 |
| DNS router, generations, cache commit | `modules/dns-demand.md` | §2 the cache-commit invariant |
| FakeIP | `modules/dns-demand.md` | §3 |
| consumer demand | `modules/dns-demand.md` | §4 |
| DNS leaks | `modules/dns-demand.md` | §5 DNS leaks and the reach of DNS |
| the `adblock` DNS action, blocking ad names with a local NXDOMAIN | `modules/dns-adblock-action.md` | §2 syntax, §3 behaviour, §8 placement |
| `systemd-resolved` goroutine leak | `modules/dns-demand.md` | §6 — fixed; the doc saying otherwise is stale |
| any sniffer, peek/read ownership | `modules/sniff.md` | §2 ownership |
| sniff loops, fragmented QUIC | `modules/sniff.md` | §3 the two loops → Packet |
| sniff → routing metadata contract | `modules/sniff.md` | §4 |
| sniff detection weaknesses **not** fixed | `modules/sniff.md` | §6 |
| MASQUE dataplane, capsules, sessions | `modules/masque-connectip.md` | §1 `transport/masque` |
| MASQUE endpoint | `modules/masque-connectip.md` | §2 `protocol/masque` |
| the two MASQUE resolvers | `modules/masque-connectip.md` | §3 not interchangeable |
| MASQUE MTU / PTB | `modules/masque-connectip.md` | §4 |
| Native Naive inbound | `modules/naive-http-transport.md` | §2 server inbound behaviour |
| UoT v1/v2 | `modules/naive-http-transport.md` | §2 → UoT v1/v2 — the distinction that matters |
| Naive target ACL / SSRF | `modules/naive-http-transport.md` | §3 the highest-risk area |
| `transport/http`, `common/httpclient` | `modules/naive-http-transport.md` | §4 |
| XHTTP modes | `modules/xhttp-client-transport.md` | §4 modes |
| XHTTP field reference | `modules/xhttp-client-transport.md` | §5 full field reference |
| XMUX reuse, all-or-nothing default | `modules/xhttp-client-transport.md` | §13.4 the important all-or-nothing default rule |
| XHTTP fork lifecycle / trim / reuse | `modules/xhttp-client-transport.md` | §14 fork-specific XMUX lifecycle behaviour |
| packet-up POST cadence, high-RTT throughput, response pipeline | `modules/xhttp-client-transport.md` | §12.3 post cadence, and why response completion is not pipelined |
| Direct Offload | `modules/data-path-optimization.md` | Part A |
| traffic class + upload scheduler | `modules/data-path-optimization.md` | Part B |
| dial governor | `modules/data-path-optimization.md` | Part C |
| buffer rules, borrowed views, batches | `modules/buffer-ownership.md` | §1 the rules, each with what breaks |
| **the recorded unfixed splice defect** | `modules/buffer-ownership.md` | §2 the splice path |
| MTU quantities (three, not one) | `modules/mtu-and-geometry.md` | §1 |
| PTB | `modules/mtu-and-geometry.md` | §2 |
| `common/bufgeom` geometry | `modules/mtu-and-geometry.md` | §4 |
| low-memory geometry profile | `modules/mtu-and-geometry.md` | §5 |
| sleep/wake/screen/lock | `modules/power-mobile-lifecycle.md` | §2 level/edge/snapshot vocabularies |
| device axis, `ReuseSuspect` | `modules/power-mobile-lifecycle.md` | §7 |
| timer census | `modules/power-mobile-lifecycle.md` | §6 |
| Android trim ladder | `modules/power-mobile-lifecycle.md` | §3 |
| libbox ABI, `SetupOptions` | `modules/libbox-platform-contract.md` | §1, §5 ABI versioning |
| CommandServer / CommandClient | `modules/libbox-platform-contract.md` | §4 |
| Apple pin and its gap | `modules/libbox-platform-contract.md` | §6 the Apple pin — and the gap |
| the two deliberate upstream interface breaks | `modules/libbox-platform-contract.md` | §8 |
| build tags, pruning, tag-polar pairs | `modules/build-pruning-release.md` | §2 tag-polar file pairs with `_stub` twins |
| release profiles | `modules/build-pruning-release.md` | §3 |
| pinned dependency prefix rule | `modules/build-pruning-release.md` | §5 a prefix problem, not a list problem |
| generators, `cmd/internal/*` | `modules/build-pruning-release.md` | §4 |
| what CI actually runs | `modules/ci-release.md` | §1 what actually runs |
| **unwired gates — the systemic risk** | `modules/ci-release.md` | §2 |
| `verify-evidence/` staleness | `modules/ci-release.md` | §3 |
| re-check commands to run | `modules/ci-release.md` | §4 the commands a re-checker must actually run |
| release provenance | `modules/ci-release.md` | §5 release provenance |

## Route by keyword / alias

| Query or alias | Canonical topic | Where |
|---|---|---|
| direct, direct offload, L0, native bypass, fast path, fast_path, bypass | fast_path has **two** meanings — check which | group policy → `modules/fast-path-group-policy.md` §2; per-flow offload → `modules/data-path-optimization.md` Part A |
| sleep, screen off, lock, pause, wake, governor, reload window, device axis | power and mobile lifecycle | `modules/power-mobile-lifecycle.md` §2, §5 |
| epoch, generation, lease, rebuild, reuse band | runtime coordination | `modules/core-lifecycle-runtime.md` §3 |
| trim, low memory, memory pressure, ReleaseMemory | memory policy | `modules/core-lifecycle-runtime.md` §3; Android ladder `modules/power-mobile-lifecycle.md` §3 |
| zero-copy, splice, splice defect, buffer pool, batch | buffer ownership | `modules/buffer-ownership.md` §1–2 |
| FakeIP, cache commit, DNS leak, domain_resolver | DNS | `modules/dns-demand.md` §2–5 |
| adblock, ad blocking, advertising rule-set, NXDOMAIN verdict, negative caching of a blocked name, "why not reject" | the adblock action | `modules/dns-adblock-action.md` §1, §5, §7 |
| peek, read ownership, fragmented QUIC, unsniffable | sniff | `modules/sniff.md` §2–3 |
| CONNECT-IP, capsule, DATAGRAM, PREF64, PTB | MASQUE | `modules/masque-connectip.md` |
| UoT, masquerade, padding, `server_limits`, SSRF | Naive | `modules/naive-http-transport.md` §2–3 |
| XMUX, stream-one, packet-up, splitHTTP, X-Padding, H2/H3 | XHTTP | `modules/xhttp-client-transport.md` |
| traffic_class, traffic_scheduler, backpressure, pacing | data path | `modules/data-path-optimization.md` Part B |
| tag stub, pruning, registry, DEFAULT_BUILD_TAGS | build | `modules/build-pruning-release.md` §1–3 |
| schema, schema.json, option/schema.go, generated docs, stale generated artifact | the generated-schema chain and its re-check contract | `modules/build-pruning-release.md` §4; `modules/README.md` §Not covered (schema row) |
| release authority, release contract, canonical asset list, asset allowlist, artifact provenance, exact-SHA provenance, checksum, read-back, draft-first, dry-run, `publish-args`, `expected-assets`, `verify-public-release`, single source of release truth | CI and release provenance | `modules/ci-release.md` §1, §5 |
| pipeline DAG, unwired gate, duplicate gate, gate ownership, which workflow runs what | CI gate ownership | `modules/ci-release.md` §1–2 |
| SIGPIPE, pipefail, exit-code false negative in a gate | shell-gate correctness | `modules/ci-release.md` §5 |
| Apple source authority, Android AAR provenance, Windows artifact provenance, Unix/macOS release authority | per-platform artifact provenance | `modules/ci-release.md` §5; `modules/libbox-platform-contract.md` §6–7 |
| Windows daemon, boxdd, Named Pipe, peer authentication, service install, worker, executable identity, signer trust, Authenticode, certificate validity, code-signing EKU, same-file check | the Windows daemon trust boundary | `modules/windows-daemon-security.md` |
| unwired gate, stale evidence, VERIFIED_CI, SKIP | evidence discipline | `modules/ci-release.md` §2–3; `README.md` §the four status labels |
| orphan, provenance, patch-id, upstream twin | Git provenance | `BASELINE.md` §the provenance test |
| tag scheme, annotated tag, release identity | release identity | `BASELINE.md` §release-identity trap |
| baseline, delta, "is the KB current?" | baseline | `BASELINE.md` §post-baseline incremental reading |

## Route by source path

| Path or symbol | Primary module | Section |
|---|---|---|
| `box.go` | `modules/core-lifecycle-runtime.md` | §2 start/close ordering |
| `adapter/scope.go`, `adapter.Scope` | `modules/core-lifecycle-runtime.md` | §1 |
| `common/runtimecoord` | `modules/core-lifecycle-runtime.md` | §3 |
| `route/reference.go`, `ReferenceManager` | `modules/core-lifecycle-runtime.md` | §4 |
| `common/physicalpath`, `common/routeflow` | `modules/path-selection-validation.md` | §2–3 |
| `route/fast_path.go`, `option/fast_path.go` | `modules/fast-path-group-policy.md` | §2, §11 |
| `protocol/group/selector.go`, `protocol/group/urltest.go` | `modules/path-selection-validation.md` + `modules/fast-path-group-policy.md` | §4 / §9 |
| `common/trafficclass`, `common/trafficsched` | `modules/data-path-optimization.md` | Part B |
| `common/bufgeom` | `modules/mtu-and-geometry.md` §4 + `modules/buffer-ownership.md` §1.1 |
| `common/power` | `modules/power-mobile-lifecycle.md` | §1–2 |
| `internal/memmetrics` | `modules/power-mobile-lifecycle.md` | §4 |
| `dns/transport/group` | `modules/dns-demand.md` | §1, §4 |
| `route/rule/rule_action.go` → `RuleActionAdblock`, `dns/router.go` → `walkDNSRules`, `constant/dns.go` → `DefaultDNSNegativeTTL` | `modules/dns-adblock-action.md` | §8 placement, §13 TTL choice |
| `transport/masque`, `protocol/masque` | `modules/masque-connectip.md` | §1–2 |
| `protocol/naive`, `transport/http`, `common/httpclient` | `modules/naive-http-transport.md` | §2, §4 |
| `transport/v2rayxhttp` | `modules/xhttp-client-transport.md` | §1–22 |
| `experimental/libbox`, `daemon/` | `modules/libbox-platform-contract.md` | §1, §4 |
| `cmd/internal/*`, `release/*` | `modules/build-pruning-release.md` | §4, §6 |
| `option/schema.go`, `docs/schema.json`, `schema/generate.go` — the generated schema artifact | `modules/build-pruning-release.md` §4 (the generator) + `modules/README.md` §Not covered (its **stale status and re-check contract**) | §4 / the schema row |
| `.github/workflows/*`, `scripts/ci/*` | `modules/ci-release.md` | §1–5 |
| `protocol/vless/encryption` | **not covered** — see `modules/README.md` §Not covered | — |
| `experimental/boxdd/**` — the Windows daemon, service, worker, named pipe | `modules/windows-daemon-security.md` | §1 (roles), §4 (handshake), §5 (Authenticode), §6 (the pin) |
| `clients/{apple,android,desktop}` | separate repositories; only the core side is recorded | `modules/libbox-platform-contract.md` §6–7 |

## Route by invariant / acceptance

| Question | Where |
|---|---|
| Is this rule load-bearing or just a convention? | `INVARIANTS.md` §A hard vs §B conventions vs §C strategies vs §D implementation details |
| Which invariant owns `Scope` teardown? | `INVARIANTS.md` A1 |
| Which owns leases / "one recovery at a time"? | `INVARIANTS.md` A2 |
| Which owns buffer ownership? | `INVARIANTS.md` A6, A7 |
| Which owns release re-verification? | `INVARIANTS.md` A12; `ACCEPTANCE.md` §12 |
| Which owns fail-closed release acceptance? | `INVARIANTS.md` A20 |
| Which owns the tag-stub rule? | `INVARIANTS.md` A16 |
| Which owns upstream/fork ownership boundary? | `INVARIANTS.md` A15 |
| How do I re-check a subsystem? | `ACCEPTANCE.md` — the numbered section matching the module |
| Known flaky / known-failing? | `ACCEPTANCE.md` §Known-failing and known-flaky set |
| Standing limits of the fork? | `ACCEPTANCE.md` §The fork's own standing limits |

## Historical topics → current truth

Archive reports are **clues, not current truth** (`REFRESH.md` §2). For a topic that has an archived
report, read the module first and the archive only for how the decision was reached.

| Historical topic | Current truth lives in |
|---|---|
| fast_path group policy | `modules/fast-path-group-policy.md` |
| L0 / direct coverage | `modules/data-path-optimization.md` Part A |
| physical path / route-aware capability validation | `modules/path-selection-validation.md` §1–3 |
| DNS parity / consumer demand | `modules/dns-demand.md` §4 |
| WireGuard lifecycle / census probes | `modules/core-lifecycle-runtime.md` §3; `COMMIT-INDEX.md` |
| Apple source authority / exact-SHA resolution | `modules/libbox-platform-contract.md` §6 |
| Android build / AAR pipeline | `modules/libbox-platform-contract.md` §7; `modules/build-pruning-release.md` |
| Windows runtime / capability gates | `modules/libbox-platform-contract.md` §8; `modules/ci-release.md` |
| CI DAG / duplicate gate removal | `modules/ci-release.md` §1–2 |
| release artifact trust boundary | `modules/ci-release.md` §5; `INVARIANTS.md` A12, A20 |
| tag handling security | `INVARIANTS.md` A16; `modules/ci-release.md` §5 |
| SIGPIPE / pipefail false-negative fixes | `modules/ci-release.md` §5; `COMMIT-INDEX.md` §10 |
| upstream absorbed changes | `BASELINE.md` §the provenance test; `COMMIT-INDEX.md` provenance vocabulary |
| sing-tun absorbed/fork fixes | `modules/build-pruning-release.md` §5; `modules/mtu-and-geometry.md` |
| release allowlist / checksum / dry-run / read-back | `modules/ci-release.md` §5; `ACCEPTANCE.md` §12 |

## Maintenance

`modules/README.md` is the **module registry and ownership map** (coarse: task → module).
This file is the **fine-grained router** (section anchors, aliases, reverse path/symbol index).
Do not merge them.

When you add or rename a section in a module, update the anchor here in the same change — a broken
anchor is a defect, not a missing document (`modules/README.md` §Not covered says the same about a
link that does not resolve). An index that points at a section which no longer exists is worse than no
index, because it reads as authoritative.

The refresh-time obligations that keep this file honest are `REFRESH.md` §13 (module structure),
§31 (cross-file consistency pass) and §33 (updating `BASELINE.md`). The numbering in that document
stops at 42; there is no section about this file, so do not cite one.
