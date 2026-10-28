# Commit navigation

## Post-v0.1.9 navigation anchors (testing target 011f3fdcd99)

These commits are navigation aids, not substitutes for current source, and do not by themselves
establish external provenance or test results.

| Commit | Subject and owner |
|---|---|
| `01be4b9d42d` | per-flow candidate admission → `modules/dns-demand.md` |
| `c10ff9186cf` | empty DNS family logging → `modules/dns-demand.md` |
| `c78edf21084` | remove ineffective AI tag fallback → `modules/fast-path-group-policy.md` |
| `cd2316a704a` | consumer detour capability warnings → `modules/path-selection-validation.md` |
| `0ef38b8ff91`, `98387b223d8` | Naive H3/H2 retry and budget → `modules/naive-http-transport.md` |
| `20482fa067d` | FakeIP sniff, mapped address and kTLS boundaries → `modules/sniff.md` |
| `3145d7dbdfc`, `1ff0af39110` | TUN handoff and post-start resets → `modules/libbox-platform-contract.md` |

**This is an evolution index, not a changelog.** Entries name commits that shaped behavior at their
review points, in order, so a reader can investigate why the code looks the way it does. Recheck the
current source before assuming a historical behavior still survives.

## How to use it

1. Find the behaviour you are about to change.
2. Read the **Final behaviour** line as the behavior at that entry's review point; compare current
   source and `BASELINE.md` before treating it as current.
3. Read the SHAs in order only if you need the reasoning. `git show <sha>` on each is enough; you do not
   need the full diff of the range.
4. If a **Superseded** line is present, the earlier attempt is worth reading precisely because it is
   wrong — it is the shape a future change is most likely to repeat.

## Two things to know before reading any SHA here

* Reachability from current upstream refs is an ancestry check, not proof of authorship. Upstream
  history has been rewritten; compare patches, blobs and historical evidence before assigning an
  origin, including for entries that carry an older provenance label.
* **Do not count upstream work by SHA.** Upstream rebases `testing`, so one logical upstream change can
  appear up to 11 times in this range under different SHAs. See `BASELINE.md` §provenance.

## Provenance vocabulary

| Label | Means |
|---|---|
| `OWN` | authored on the fork's own line |
| `UPSTREAM-INHERITED` | an upstream commit present below `e4b7e692` |
| `UPSTREAM-ABSORBED` | an upstream change the fork re-applied; **its content is already in the baseline** |
| `EXTERNAL-ADAPTED` | derived from a named external source, quoted in the commit or the module |
| `SUPERSEDED` | the change is no longer in the tree; kept for the reasoning |
| `ARCHIVE-ONLY` | a reverted change, deliberately preserved |

Type vocabulary: `FEATURE`, `FIX`, `HARDENING`, `PERF`, `MEMORY`, `ZERO-COPY`, `COMPAT`, `LIFECYCLE`,
`ROUTING`, `DNS`, `SNIFF`, `CHAIN-PROXY`, `MASQUE`, `NAIVE`, `PLATFORM`, `LIBBOX`, `REFACTOR`, `TEST`,
`INTEROP`, `CI`, `BUILD`, `RELEASE`, `DOC`, `REPOSITORY-HYGIENE`.

---

## 1. Start-time proof and the path model → `path-selection-validation.md`

**Initial problem:** a configuration could declare a chain that silently could not carry the traffic it
routed to it. The failure surfaced at dial time, on a user's connection.

| SHA | Type | Origin | What it did |
|---|---|---|---|
| `01262510` | FEATURE | OWN | implements the `PhysicalPath`/`ControlPath` model and the start-time reachable-leaf dry run |
| `524ea41e` | FIX | OWN | makes `Hops` **packet order**, derived from the dial edge. **Nine tests went red**; two were renamed because they asserted the opposite of their names. |
| `d46717dbd` | FIX | OWN | `ControlPath` is the **decision order**, not the packet order — 51 minutes after `e201b5add` documented the opposite |
| `e3399187` | FIX | OWN | per-walk resolver state; a diamond is validated per route |
| `fc4bdd416` | FIX | OWN | the three requirement facts; records that `RequiredNetworks` is **write-only** |
| `8299f30e` | FIX | OWN | the exported dry run refuses a truncated route instead of leaving it to a lint |
| `8ae8f10d` | FIX | OWN | a truncated route cannot hide behind a sibling |
| `91f595906` | FIX | OWN | `destination_dns_ownership` is vacuous for a local dialer |
| `cf5a25bbe` | FIX | OWN | ownership parity for **every** proven local dialer |
| `33eba5bf7` | FIX | OWN | validate capability against **reachable flow types**, not entry capability |
| `0dfedb382` | FIX | OWN | consume a protocol only where the runtime router actually stops |
| `04591f8` | FIX | OWN | one resolver source of truth, and the false deny it removes |
| `4e228f53` | FIX | OWN | **the default-resolver edge is over-charged, not under-charged** — reverses an earlier decision |
| `31f250ff` | FIX | OWN | settles the three contracts: provable-only demand, group union, graph edges |
| `ff004007` | FIX | OWN | publish resolver facts on the adapters the graph actually walks |
| `5ad4c032` | FIX | OWN | populate the server-domain fact at every constructor site |
| `d95e256b7` | REFACTOR | OWN | drops two dead helpers — **and adds the orphan comment that contradicts the code** |

**Final behaviour:** validation is read-only, consumer-first and provable-only; `Hops` is packet order,
`ControlPath` is descent order; a control group is never a hop; `route.final` is charged the residual as
proven; `RequiredNetworks` is write-only.

**Superseded:** `KNOWLEDGE/reports/release/ROUTE_AWARE_OUTBOUND_CAPABILITY_VALIDATION_FINAL.md` §B/§H (its
`TestFinalProvesNothingAboutDelivery` no longer exists); `KNOWLEDGE/reports/release/GROUP_CROSSKIND_BRIDGE_CLOSURE.md` §4/§9 (all
three `= NO` verdicts were later closed); two `docs/fork/v016-*` reports still declaring J-1/J-2/J-3
"NOT IMPLEMENTED" when all three exist.

**Evidence:** 10 root `box_static_validation_*_test.go` files, 6 `box_physicalpath_*_test.go`,
`box_cross_kind_cycle_test.go`, 16 `common/physicalpath` test files, and four recorded **mutation**
controls.

---

## 2. Core lifecycle and runtime coordination → `core-lifecycle-runtime.md`

**Initial problem:** teardown was per-component and unreliable, and a network transition caused every
resource to rebuild at once.

| SHA | Type | Origin | What it did |
|---|---|---|---|
| `8c7b230d` | LIFECYCLE | OWN | adds the resource lifecycle coordinator |
| `5f56f02c` | MEMORY | OWN | trim idle resources when memory is elevated but not critical |
| `09964183` | FIX | OWN | stops the memory trim from building a second transport |
| `379b21161` | FIX | OWN | the generation-based rebind budget |
| `93823c3d` | MEMORY | OWN | retires the post-wake pools that were **unreachable**; drains the ones that can |
| `8735b1d` | MEMORY | OWN | reaches the HTTP client pools from the retire walks, which never did |
| `1331e8567` | FIX | OWN | `TrimMemory` does not advance the generation; `ReleaseMemory` does |
| `1f85dbc94` | FIX | OWN | all **four** pools reachable from the boundary walk |
| `7e13f461` | FIX | **OWN, not upstream** | clears the process-global interface getter **its owner registered** — the commit body says the fork's own refactor dropped it |

**Final behaviour:** one monotonic epoch; a rebuild is a **lease identity**, not a flag; retirement
asserts `IdleConnectionReleaser`, not `IdleConnectionKeeper`; a late `scope.Add` runs synchronously on
the registering goroutine.

**Superseded:** nothing. **The wording that was wrong:** "`7e13f461` absorbs an upstream
interface-getter reset" — it fixes a fork-introduced leak.

**Evidence:** `adapter/lifecycle*_test.go`, `common/runtimecoord/*_test.go`,
`route/reference_coverage_stress_test.go`, `route/reusable_owner_inventory_test.go`.

---

## 3. Sniff → `sniff.md`

**Initial problem:** sniffing copied whole datagrams and could be driven to unbounded memory growth.

| SHA | Type | Origin | What it did |
|---|---|---|---|
| `e053cc733` | PERF + REFACTOR | OWN | explicit plan lists; `payloadReader`; the TLS header gate and its sentinel; the packet-sniffer indirection |
| `300b167b4` | PERF | OWN | retry driven by the previous round's failures |
| `912778dd6` | FIX | OWN | adds the **WireGuard** sniffer and moves it before UTP (UTP was stealing handshakes) |
| `91577eaa2` | PERF | OWN | the HTTP gate + replay reader |
| `4dff25c90` | PERF | OWN | `takePacketBytes` returns **views** |
| `9167595c7` | PERF | OWN | `frameSummary` replaces a frame-type slice |
| `bfe721454` | PERF + HARDENING | OWN | packet-bounded copy **and** the `Length` lower-bound guard — a port of `1b0cedf40` |
| `578663e0` | HARDENING + FIX | OWN | `maxSniffRounds = 64`, release-on-abandon, and the `matchRule` error-path buffer release |
| `1292a85a5` | TEST | OWN | the `wireguard.go` comment block (0 non-comment lines) |
| `a05a6954f` | **UPSTREAM-ABSORBED** | — | "Fix DNS sniffer matching non-DNS packets": `dns.go` is byte-identical at both ends |
| `b93c30f2a` | **UPSTREAM-ABSORBED** | — | "Fix protocol input validation": the CRYPTO length guard is present at `e4b7e692` |
| `dd4f62edc` | **UPSTREAM-ABSORBED** | — | "Rewrite HTTP proxy protocol": `http.go:14`, `:95` are present at `e4b7e692` |

**Final behaviour:** TLS sniffing runs no server handshake; QUIC is inspected as datagram views bounded
to the current packet; the fragmented loop continues only on `ErrNeedMoreData` and is capped at 64.

**Evidence:** 22 added test files, two fuzz targets, address-range and datagram-poisoning ownership
proofs. **The cap and the release-on-abandon are `VERIFIED_SOURCE` only** — no test covers them.

---

## 4. DNS → `dns-demand.md`

| SHA | Type | Origin | What it did |
|---|---|---|---|
| `5b0a33c` | FEATURE | OWN | `dns/transport/group` — stable / fastest / parallel transport groups |
| `a123864b` | FIX | OWN | the policy epoch owns the **cache commit**, not only the check |
| `ffec0df1` | FIX | OWN | a DNS-only environment change gets its **own** generation |
| `8fad3e9b` | FIX | OWN | a FakeIP server must not answer a destination lookup, and `domain_resolver` could name one |
| `296bb6f94` → `3711437d4` | FIX/MEMORY | OWN | the DNS cache, pooling and generation family (17 commits) |
| `1b405d865` → `c443a8df7` | FEATURE/REFACTOR | OWN | DNS family streaming; the collector was later dropped as dead (`721e6c4db`) |

**Current behavior:** a settled-start DNS query may deliver its dated answer after a transition but
must not commit it under the changed generation; a query started during transition is not delivered
as a stable result. A FakeIP server cannot resolve a destination; the TCP leg of a DNS transport's
requirement is `Conditional` because the retry only fires on truncation.

**Known defect — FIXED, and the document that says otherwise is stale:** the
`dns/transport/local.(*DBusResolvedResolver).loopUpdateStatus` goroutine leak recorded in
`docs/RELEASE-CERTIFICATE.md:70-85` was fixed by **`6b495134c`** (`git log -S'signalLoopStop'
e4b7e692..a267e99` returns exactly that commit). `VERIFIED_SOURCE`; execute needs Linux + systemd-resolved.

---

## 5. MASQUE / CONNECT-IP → `masque-connectip.md`

| SHA | Type | Origin | What it did |
|---|---|---|---|
| `b83089ec` | — | **UPSTREAM-INHERITED** | "Add MASQUE support", 世界, 2026-09-21 — **present below `e4b7e692`**, not a fork feature |
| `d53c867b7` | FEATURE | OWN | **the fork's first fork-authored commit** — the inbound masquerade handler |
| `7e57fe8c7` | HARDENING | OWN | the limiter actually bounds; a 429 decoy with no `WWW-Authenticate` |
| `b83695603` → `1511c1c6a` | FEATURE/FIX | OWN | the H3 pool rotates within the **tied-minimum** set (before, slot 0 served every tunnel) |
| `0271b145a` | HARDENING | OWN | CONNECT gated on `HandshakeComplete` (0-RTT replay) |
| `9491008a9` | FIX | OWN | `http3_fallback` was dead config |
| `e4f847ecf` | COMPAT | **EXTERNAL-ADAPTED** | QUIC defaults taken from `quic-go/masque-go` + `connect-ip-go` |
| `969909bc1` → `b656b062f` | HARDENING → PERF | OWN | cross-protocol overlap; the *correct* quadratic version was an **800× CPU DoS** (13.5 s vs 16 ms) |
| `a529b68c8` | HARDENING | **EXTERNAL-ADAPTED** | the 8192-entry capsule bound (was 149,796 entries / 17.65 MiB); constant from `connect-ip-go` |
| `e4d2f6c1a` | FIX | OWN | `Contains() == lookup()` on the server's own address |
| `6d94da584` → `b5a25edd8` | PERF/ZERO-COPY | OWN | no per-packet alloc; `errors.As(&tooLarge)` escaped to the heap **even when `err == nil`** |
| `8870623cf` | FEATURE | OWN | the bootstrap cache |
| `8f1ba42a9` → `1d87f0cf7`, `e93e297aa` | FEATURE/REFACTOR | OWN | DNS roles made explicit; immutable snapshots replacing the deleted `assigned_dns_*` model |
| `2118ee40` | FEATURE | OWN | caps the MASQUE outer QUIC packet by a **proven detour capacity** |
| `0d28c26d` | FIX | OWN | makes the H3 fallback reachable; lets the tunnel leg fragment |
| `a367bbe3` | FIX/HARDENING | OWN | closes the readiness window; serialises every DNS invalidation |
| `a00a2fe6` | FIX | OWN | the H3 acquisition is part of the **close decision** |
| `9235f90` | FIX | OWN | the server endpoint's teardown goes to the `Scope` that closes it |
| `c3b4f567` | FIX | OWN | releases resources acquired **after** `Scope.Close` returned |

**Current behavior:** each batch chunk is all-or-nothing; an accepted prefix transfers ownership and
only the remaining buffers may fall back. A context-ID substitution refuses the affected chunk.
The outer QUIC initial size is capped by a proven detour capacity, without proving a hard cap on
later packets; see `modules/mtu-and-geometry.md`.

---

## 6. Native Naive → `naive-http-transport.md`

**Initial problem:** the fork needed a NaiveProxy **server** inbound with the fork's resource controls,
and the padding/framing contract had to match a client it does not own.

| SHA | Type | Origin | What it did |
|---|---|---|---|
| `45f6686a6` | FEATURE | **EXTERNAL-ADAPTED** | the native Naive inbound (reference: `klzgrad/forwardproxy@naive d62c80d3…`, quoted at lines 320/352) |
| `23af897bf` | HARDENING | OWN | `-connect-authority` removed |
| `593f634a1` | FIX | UPSTREAM-ABSORBED-style | hijack buffered bytes |
| `fb90272a5` | FIX | OWN | an `io.Writer` short write caused **frame desync** |
| `b16660ed1` | HARDENING | OWN | a timing signal was removed |
| `3c6e0aad3` → `fe5789539` | FIX → FIX | OWN | a clamp was added and then **undone**; the full 0..255 padding range is correct. The "panic fix" **was itself a protocol change** — a test misused `buf.Resize(start, end)` where arg2 is a **length**. |
| `e9d329380` | COMPAT | UPSTREAM-ABSORBED-style | the response `Padding` header became unconditional |
| `d4ead1498` | HARDENING | OWN | `packetDestinationGuard` introduced |
| `7029722bf` | HARDENING | OWN | the single conjunctive TCP-only self-SSH:2222 exception |
| `1dc734f75`, `3bbf17907`, `10e1f2a95`, `d1f3798a6`, `2ec06e451` | HARDENING | OWN | guard order and IPv4-mapped normalisation |
| `724035c76` | REFACTOR | OWN | `server_limits.idle_timeout` was resolved and **never read** — removed with the reasoning |
| `bbfc31582` → `968395304` | REFACTOR | OWN | the Clash API removed (−1908) and then **restored** (+2254) |

**Final behaviour:** parse → authenticate → tunnel; padding negotiated and applied to the first 8
frames; the target ACL resolves before it checks; the per-datagram UoT guard intercepts on the **read**
side and permits when no rule matches.

**Test-integrity wart:** a previous conclusion that "v2 non-Connect is a P0 defect" was a
**test-fixture error**.

---

## 7. Data path: offload, class, scheduler → `data-path-optimization.md`

| SHA | Type | Origin | What it did |
|---|---|---|---|
| `d2b34f1cf` | FEATURE | OWN | the bypass/socket-semantics profile |
| `3dea03fd5` | FEATURE | OWN | **answer the bypass question for the flow, not the configuration** |
| `8a6e5f3cc` | FEATURE | OWN | `BypassVerdict` + the cost benchmarks |
| `143f869a9` | TEST | OWN | the `ActionBypass` trace |
| `ff80ddea6` | FIX | OWN | a bypass carrying the ICMP `Port` was rewritten to `ActionFlow` |
| `497d37727` | FIX | OWN | DNS hijack ordering before the route address sets (`8.8.8.8:53` had been leaving sing-box) |
| `386e95a70` | DOC | OWN | records the TUN-mode limitation |
| `e27d58711` | PERF | OWN | gates the debug line that cost 5 allocs / ~95 ns per eligible flow |
| `544c1ab5e` | FEATURE | OWN | the traffic class |
| `94df98f92` | FEATURE | OWN | the scheduler and the gate |
| `4e34d0091` | FEATURE | OWN | `route.traffic_scheduler.upload_rate` |
| `bd7b1ed19` | FIX | OWN | `writePeriod` saturates instead of converting `float64` ns to `Duration` |
| `bb21a854c` | TEST | OWN | **the `trafficresearch` tag** — the prototype fails deterministically on Linux |
| `4f999d461` | TEST | OWN | the HIGH-versus-HIGH gate rewritten off p99 onto the queue and the median |
| `bcc715a72`, `5ab49bdcb` | FEATURE/FIX | OWN | the dial governor, then wait-instead-of-poll |
| `58050da3c` | DOC | OWN | **quotes the bogus fixture benchmark** |
| `22d46966c` | FIX | OWN | fixes the benchmark fixture two days later — the doc was never updated |

**Final behaviour:** semantic equivalence, not config emptiness; an unclassified option refuses; the
verdict is a real bypass **only** in Linux `auto_redirect`; the scheduler is inert when unset and
continuous when configured.

**Known-bogus:** `direct-offload.md:210`'s `2856 B / 26 allocs`. **Stale:** `direct-offload.md:106`'s
sing-tun anchor (`d769a7080ca2`) is two pins out of date.

---

## 8. Power and mobile lifecycle → `power-mobile-lifecycle.md`

**Initial problem:** the sleep duration was measured wrong, and a resume re-anchored it.

| SHA | Type | Origin | What it did |
|---|---|---|---|
| `aac340808` | LIFECYCLE | OWN | the sleep authority |
| `d93b68560`, `89b4b2c25`, `6ebb5799a` | FIX | OWN | pool release at `DEEP_IDLE`; the unreachable `DEEP_IDLE` branch; a stale deadline |
| `0ff90482` | FIX | OWN | spends the device resume edge on an **edge latch**, not on the screen level |
| `e4327dca` | LIFECYCLE | OWN | gives the Apple device axis a real publisher and a real release |
| `4c7d96c0` | FIX | OWN | carries the Apple device axis on the **release line**, not only on a branch |
| `645d96b4` | FIX | OWN | a current-state screen read **reconciles the level**, it does not start a sleep |
| `60681f27` | FIX | OWN | a resume does not end a sleep episode, so it must not spend its boundary |
| `e20875359` | FEATURE | **SUPERSEDED** | gated the sleep edge on `IsDevicePaused()` |
| `e661258a6` | **REVERT** | **ARCHIVE-ONLY** | reverts `e20875359` on device evidence: **0 boundaries** over a ten-minute lock, strictly worse than the bug. Both are kept. |
| `12aa6db064f` | FEATURE | OWN | the first **consumer**: the optimistic DNS refresh asks the authority instead of waking the radio — refused in `QUIESCENT`/`DEEP_IDLE`/closed, released through `Allow().NetworkProbe` in the `WAKING` band. Tests in `dns/optimistic_refresh_power_test.go` |
| `dbf8de8b2a5` | FEATURE | OWN | the second consumer: the dashboard download **waits** on `WaitProviderRefresh` rather than fetching through a sleep, and its first pass is deliberately not deferred. Tests in `service/api/dashboard_power_test.go` |
| `ff897d35772` | FIX | OWN | resolves the DNS governor at construction instead of in `Start`, so the field the refresh goroutine reads is immutable rather than ordered. See `reports/core/HAKO_SCALE_DEBUG_AUDIT_2026-10-17.md` |

**Final behaviour:** level/edge/snapshot are three different things; a display-on publishes only a
resume edge and the reuse epoch; **only an unlock releases the level**; three reuse bands at 5 s and
15 s; the trim path never resets the network while the release path does. **Two scheduled outbound
fetches now defer to the authority** (the DNS optimistic refresh and the dashboard download); the
WireGuard recovery poll is deliberately left ungated, with the reason recorded in
`power-mobile-lifecycle.md` §6 and `reports/core/HAKO_SCALE_ARCHITECTURE_AUDIT_2026-10-17.md` §6.

**Device evidence (the only physical-device run recorded here):** iPhone 17 Pro Max, iOS 26.4.2 —
pre-fix 28 m 24 s → 12 boundaries → 28.29 s; post-fix 13 m 13 s → 1 boundary → 13 m 13.492448 s.

---

## 9. Build, pruning and release → `build-pruning-release.md`, `ci-release.md`

| SHA | Type | Origin | What it did |
|---|---|---|---|
| `f857f0814`, `d535f106f` | BUILD | UPSTREAM-ABSORBED | dropped `with_gvisor` |
| `0e0063530` | REFACTOR | OWN | dropped `with_clash_api` and the **whole LXD daemon** (−26,481 lines, the largest churn event, lifetime ~25 h) |
| `76cd68eaf` | FIX | OWN | restored the Clash API, claiming byte-identity with upstream |
| `379b21161` | BUILD | OWN | **added `with_xhttp` — the only fork-invented profile entry** |
| `b0e93f894` | BUILD | OWN | removed `tfogo_checklinkname0` from all three profile files (the tag travels with the flag now) |
| `3bab501db`, `dcd8b10cd`, `4c39a835b`, `b175f04dd`, `523db47e4` | REFACTOR | OWN | retired the whole **product-specific registry + capability allowlist** architecture |
| `108439963`, `2937cb71e`, `6c5fa3a20` | CI | OWN | the upstream-assumption tripwire gate |
| `e63ce659` | CI | OWN | the fail-closed release acceptance gate |
| `9aef595e` | CI | OWN | runs the acceptance gate's self-test on every push |
| `d660c979` | FIX | OWN | compares the release gate against the names the workflows declare |
| `a267e99e0` | RELEASE | OWN | v0.1.6: include Windows and Android artifacts — **and did not extend the release-tree allowlist** |
| `27d8bb599`, `12ee55eef` | REPOSITORY-HYGIENE | OWN | ancestry-only merges to clear GitHub's stale "behind upstream" state. **No tree change** (`12ee55eef` records `PRE_SYNC_TREE == post-merge tree`). |
| `7f31bfd5e` | RELEASE | OWN | advances the `clients/apple` gitlink `d0524912` → `5cab4ec5` and **nothing else** — `1 file changed, 1 insertion(+)`. The pin, not the code: no workflow, refs file or build script moves with it. |
| `ece0fb9d2` | FIX + CI | OWN | unblocks `release-apple.sh testflight`, twice over. The refs guard tested the caller's **environment** instead of the refs **file**, so `resolve -> export -> assert-shared-source` refused the values `resolve` had just printed; and `VERIFIED_APPLE_SHA` was a pin generation behind the gitlink, failing the ABI gate's layer 5. Cherry-picked from `24d55d64b`, with the constant re-derived to the *current* gitlink rather than the one the original named. |

**Final behaviour:** upstream's registries and profiles, one fork-invented tag, the version stamp and
the Apple pin metadata; release downloads and re-verifies rather than rebuilding.

**Live defect:** `release-artifacts.sh`'s release-tree allowlist still encodes two products while
`release.yml:69` enables four → `verify` fails on the Windows and Android trees. See `ci-release.md` §5.

**Superseded:** the environment-based refs guard (`ece0fb9d2` replaces it). It is the shape a future
change is most likely to repeat, because testing a sourced file's variable names in the *environment*
looks equivalent to reading the file and is not — `resolve` and `release-apple.sh` legitimately carry
the same names. Also superseded: two `VERIFIED_APPLE_SHA` values, `90a0e9ae` and `d0524912`. Each was
correct for its own gitlink and became wrong the moment the pin moved without it — which is the whole
failure mode that constant has.

---

## 10. Superseded work, reverts and dead ends — the trap material

**Only three explicit reverts in 1,834 commits:**

| Revert | Reverts | Why |
|---|---|---|
| `e661258a6` | `e20875359` | **device evidence.** Gating the sleep edge on `IsDevicePaused()` produced 0 reuse boundaries over a 10-minute lock — strictly worse than the bug. Unfixable that round because the anchor and the per-sleep latch are the **same field**. |
| `4b75589b1` | `f1ea985d2` + `e92dff18b` | *"The defect they fixed does not exist."* The fix's mutable lifecycle exposed an **unlocked reader on the connection path**, and the author's own comment was false. |
| `eb3b09033` | `5096ac91a` | The nil-`SO_MARK` panic is unreachable (the build tags are exact complements), and the race had only been shown with an artificial concurrent `Register` loop. |

`ARCHIVE-ONLY`: `e20875359`, `f1ea985d2`, `e92dff18b`, `5096ac91a`.

**Removed then restored:** the Clash API; the MASQUE client transport; the Apple/Android/desktop client
lines; the desktop installer (`43f4048ac` → `7a46125de` "recover verbatim").

**Removed for good:** `lxd/`; the product registry and capability allowlist; `-connect-authority`;
`server_limits.idle_timeout`; the MASQUE PREF64 clear path; a MASQUE dead selection rule; the DNS
streaming collector; a stray 21 MB `masque.test` binary.

**Superseded pairs:** `3c6e0aad3` → `fe5789539` (the clamp); `969909bc1` → `b656b062f` (the quadratic
overlap check); `b51d3cbe5` ("set version 0.1.15") → `e70f3d2c8` ("correct version to v0.1.5");
the `splice_diagnostics` `Successes`/`Ratio` rename, reverted in consolidation and now guarded by a
source-level test.

**Fix-of-fix chains, each of which introduced a false result at least once:** the path model (5 rounds,
two of which introduced false denies); static validation (5 rounds); the power governor (7 steps ending
in a revert); the Naive padding fix, where **the fix was the bug**.

**Tests that could not fail** — a whole family, each later fixed: `48dd4469e`, `e5b0897f4`, `12c0a9e60`,
`3c3dd135b`, `f8b0af5f6`, `10f9e17d2`, `eac9cc83d`, `b5c0009d1`, `95159084c`, `666470cbe`,
`4cdb250b0`, `c2d93396e`, `da14572a7`, `b5c39a11e`, `675d72bc5`, `6140afcc5`.
The instructive one: a TUN test that called `inbound.Close()` **directly** — which the product never
does — so it stayed green while the product leaked.

**Scope oscillation:** `5ce3c4d67` ("refactor!: narrow the fork to VPS-only") removed the iOS/client
lines and the entire MASQUE client transport (−13,122 lines, +270). **All of it is back.** The narrowing
was undone by the next upstream sync, not by a decision — `transport/masque/server.go` was added by an
upstream commit, deleted an hour later, re-added by the next upstream commit, and absorbed. **A future
AI reading that commit message will believe a deliberate scope decision was made. It was not.**

---

## 11. Where to look next

| You want | Go to |
|---|---|
| the release identity and the provenance test | `BASELINE.md` |
| which module owns your task | `modules/README.md` |
| whether a rule is an invariant or a convention | `INVARIANTS.md` |
| how to re-check a requirement | `ACCEPTANCE.md` |
| why the code is shaped this way | the module's **Provenance** table |
| the "reasonable but wrong" edit you are about to make | the module's **AI cautions** |
