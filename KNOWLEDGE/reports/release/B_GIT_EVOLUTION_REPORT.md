# Agent B — Git Evolution / Provenance (Android) · `satelite-one`

Repo `C:\Deepseek\jiejiebox-work\satelite-one`, branch `main`, read-only audit.
All SHAs below are abbreviated in prose but were resolved to full 40-hex with `git rev-parse`; the
full values are in §B5 and in the commit inventory.

> **Deviation from repo guidance (declared).** `AGENTS.md` at HEAD tells a reader to open
> `KNOWLEDGE/README.md` first. My brief forbids reading `KNOWLEDGE/**` and
> `KNOWLEDGE_STAGE1_REPORT.md`, so I did not. Every statement below comes from Git objects,
> working-tree source, `docs/**`, `README.md`, `AGENTS.md`, `version.properties` and
> `.github/**`. Where a claim can only be checked by reading `KNOWLEDGE/**`, I mark it
> **UNKNOWN** instead of guessing.

---

## B0. History shape

| Property | Value | Status |
|---|---|---|
| `git rev-list --count HEAD` | **199** | VERIFIED_GIT |
| `git log --oneline \| Measure-Object -Line` | **199** (identical ⇒ no truncation, no graft) | VERIFIED_GIT |
| `git rev-parse --is-shallow-repository` | **false** — full history | VERIFIED_GIT |
| Oldest reachable commit | `3b7192425421ca6639cc22af869a99e1f3130688` — "Initial commit", 2026-08-27, **LICENSE only (21 lines)** | VERIFIED_GIT |
| Root commit parent | `3b71924` has **no parents** (root) | VERIFIED_GIT |
| Second commit | `db3365638e96b8d41c0f631ee77f57303d78089d` (tag `0.2.0`), parent `3b71924`, 139 files / 11480 insertions | VERIFIED_GIT |
| Merge commits | **zero** (`git log --merges` empty) — history is linear | VERIFIED_GIT |
| Branches (`git branch -a`) | `main`, `remotes/origin/HEAD → origin/main`, `remotes/origin/main` | VERIFIED_GIT |
| `git for-each-ref` | 18 refs = 15 tags + `main` + `origin/main` + `origin/HEAD` | VERIFIED_GIT |
| Tags | 15: `0.2.0`, `v0.3.0`, `v0.4.0`, `v0.4.1`, `v0.5.0`…`v0.5.10` | VERIFIED_GIT |
| Annotated tags | **only `v0.4.0` and `v0.4.1`** (`cat-file -t` = `tag`); the other 13 are lightweight (`commit`) | VERIFIED_GIT |
| Submodules / gitlinks | **none** — no `160000` entry in `ls-tree HEAD` or `ls-tree -r`; no `.gitmodules` | VERIFIED_GIT |
| Upstream lineage | **none.** Single remote `origin = https://github.com/Piggy-Cat-bit-shadow/satelite-one.git`. No second remote, no merge base with any upstream, no vendored upstream history | VERIFIED_GIT |
| Worktrees | **2**: `satelite-one` @ `942b6fe [main]` and `baseline-r5` @ `2171167 (detached HEAD)` | VERIFIED_GIT |
| Stash | empty | VERIFIED_GIT |
| Working tree | `git status --porcelain -uall` → empty | VERIFIED_GIT |
| `git describe HEAD` | `v0.5.10-88-g942b6fe` | VERIFIED_GIT |
| Authors | `zn0wii <lightlfyan@gmail.com>` 108; `Piggy-Cat-bit-shadow <1284533224@qq.com>` 88; `Ryan Yan <lightlfyan@gmail.com>` 3 | VERIFIED_GIT |
| Commits per month | 2026-08: 3 · 2026-09: 108 · 2026-10: 88 | VERIFIED_GIT |

**Two eras, cleanly separated by the last tag:**

* **Multi-core era** — root…`v0.5.10` = **111 commits** (`git rev-list --count v0.5.10`).
  Identity 星际穿越 / Interstellar, applicationId `com.interstellar.proxy`.
* **Single-core era** — `v0.5.10..HEAD` = **88 commits**. Identity Jiejiebox, one engine
  (`Piggy-Cat-bit-shadow/sing-box`), CI-provenanced release gates.

**Hidden branch archaeology (reflog, not refs).** `git reflog --all` still names five deleted
topic branches, and their tips are still live objects (`merge-base --is-ancestor` = **1**, i.e.
**not** ancestors of `main`):

| Deleted branch | Recovered tip | Fate |
|---|---|---|
| `jiejiebox/v0.1.6-ui-client-lifetime` | FF-merged into `main` (reflog `main@{3}`) | commits survive on `main` |
| `jiejiebox/v0.1.6-final-guard-integration` | FF-merged into `main` (reflog `main@{2}`) | commits survive on `main` |
| `jiejiebox/v0.1.6-release-abi-guard` | `47911014350246e7d7362f11f3ab43bee1e5283b` | **orphan**, superseded by `25ac148` |
| `jiejiebox/v0.1.6-startproxy-lifetime-guard` | `e413c5948b1393cd6d93458b8e41697127f227e2` | **orphan**, superseded by `d10c9a5` |
| `jiejiebox/v0.1.6-foreground-ordering-guard` | `baa9958e8cdaebeffd5767c8be1e8d42fa8cb364` | **orphan**, superseded by `15b5ff1` |

`git fsck --dangling` reports exactly **three** dangling commits, all foreign-agent or rebased
work: `5445f1b`, `2334bad`, `e0fe962` (details in §B2/§B6).

---

## B1. Evolution clusters

### Cluster: Multi-core / sidecar architecture and its removal

- **Problem.** v0.3.0–v0.5.10 shipped a *three-engine* client (sing-box in-process via `libbox`,
  mihomo and Xray as out-of-process sidecars with a `hev-socks5-tunnel` JNI bridge). That
  three-engine surface carried an entire class of bugs: fd inheritance across `ProcessBuilder`,
  a compiled-TUN fork (`cmfa` tag), geodata bootstrap ("鸡生蛋"), a fake-IP base address, a
  control port, three config generators and three per-core UI branches. The client was also
  shipping an engine it built from a *mutable* upstream `testing` HEAD, so an APK could not be
  reproduced.

- **Evolution (ordered):**

  1. `9212584` (2026-09-12) — *multi-core Phase 1*: `CoreKind` / `ProxyCore` abstraction,
     `BoxService` hands `libbox` to `SingBoxCore` losslessly, mihomo binary pipeline
     (`jniLibs`, 4 official ABIs), settings entry (sing-box only). FEATURE.
  2. `cfcda17` — *Phase 2*: mihomo sidecar full integration — `MihomoConfigBuilder` (YAML emitter),
     `SidecarProcess` (exec from `nativeLibraryDir`, log rotation, watchdog), `ClashApiClient`,
     sidecar TUN with fd inheritance + node-IP route exclusion, neutral `CoreGroup` DTO. FEATURE.
  3. `2e3c560`, `7772dca`, `1ddae75`, `dfd685c`, `8818a53`, `926d6cd`, `1eee16d`, `47d1b77`,
     `7dc43a9` — the mihomo fix-chain: geodata bootstrap, `proxy-server-nameserver` DNS
     chicken-and-egg, `cmfa` self-compiled tag, pivot to a **hev** JNI bridge
     (`dfd685c`, because `ProcessBuilder` children do not inherit the fd), two JNI-abort fixes
     (`8818a53`: `FindClass` on an IO thread with no classloader → preload on main in
     `Application.onCreate`; `926d6cd`: `RegisterNatives` needs all four native methods),
     `network_security_config` loopback plaintext (`47d1b77` — Android 9+ blocked the readiness
     probe). FIX/HARDENING.
  4. `b26d590` — *Phase 3*: Xray core, `XrayConfigBuilder`, v26.3.27 pinned with sha256 + mirror
     fallback. FEATURE. Then `6ee8dba` (hysteria2), `380a0c6` (restart on running core switch),
     `b8d58e7` (three-core egress probe).
  5. `e75d6eb` (tag **v0.4.0**), `5349689` (**v0.4.1**), `0607e82` (**v0.5.0**),
     `f316c30` (**v0.5.1**) … `ac94df1` (**v0.5.8**), `2db9aeb` (**v0.5.9**),
     `6f4124c` (**v0.5.10**) — the multi-core product line.
  6. `c68fa18` (**v0.5.3**) — `DirectPing` added so TCP ping bypasses the app's own TUN;
     `c68fa18` also fixed mihomo node-domain fake-ip exclusion.
  7. `d2a2e98` (2026-10-08, author `Piggy-Cat-bit-shadow`) — **单内核化 / single-core-ization.**
     Deletes `MihomoCore`, `XrayCore`, `ClashApiClient`, `SidecarProcess`, `TProxyService`,
     `CoreKind`, `MihomoConfigBuilder`, `XrayConfigBuilder`, `tools/fetch_cores.py`,
     `assets/geodata/*` (geosite.dat / geoip.dat / geoip.metadb ≈ 32 MB),
     `VPNService.establishSidecarTun` + route-exclusion helpers, `ProxyCore.SidecarTunSpec`,
     `CoreHost.openSidecarTun`, `RawConfigApplier.applyClash/applyXray`,
     `Settings.coreKind/apiSecret/serviceMode`, and every per-core UI branch. Adds
     `usePlatformAutoRedirect()/createAutoRedirect()` to `PlatformInterfaceWrapper` returning
     `false` / `error("not supported")` — **VERIFIED in diff**. ABI set collapses to
     arm64-v8a (release) + x86_64 (debug). NEW `verifyCoreProvenance`-style build metadata
     (`CORE_REPO`/`CORE_BRANCH`/`CORE_COMMIT`/…). Rewrites CI into `android-ci.yml` (new file)
     + `release-apk.yml`. SUPERSEDED (the whole v0.4–v0.5.10 architecture).
  8. `d628ef0` — post-removal dead-code audit: deletes `Yaml.kt` (only `MihomoConfigBuilder`
     consumed it; `YamlToJson.kt` is the Clash *parser* and is kept — message is explicit),
     `RawConfigApplier` leftovers, stale comments. REPOSITORY-HYGIENE.
  9. `1a0b506` — deletes the app's own `SmartSwitchEngine` (375 lines) and `DirectPing.kt`
     (133 lines): "latency has exactly one source now — sing-box's own urltest". Also deletes
     `Settings.smartActiveTag`, `ConfigBuilder.SMART_TAG`, 17 `smart_*` strings, 7 `ping_*`
     strings. Feeds `2f0bcbf`. SUPERSEDED, then ARCHIVE-ONLY.
  10. `22d9ab4` — brand consolidation to Jiejiebox + UI/functionality consistency audit
     (deletes `CustomRulesStore`, `CustomRouteRule`, shrinks `ConfigBuilder` −65, `AppViewModel`
     −86). Then `8d8bb9f` polishes `DnsOverridesPage` (+46) — and `ebd2f79`, **one commit
     later**, deletes that whole page (−318 lines). See §B2.
  11. `2b2d225`, `38074fa`, `997cd8f` — CI dry-run so the release path could be *executed*
     rather than reasoned about; `38074fa` corrects a real false negative (`git ls-remote`
     lists only ref tips, so a pinned ancestor SHA never matched → replaced by an explicit
     `fetch` of the SHA). FIX/CI.

- **Rejected / superseded attempts:**
  - The whole sidecar architecture (`cfcda17`…`ac94df1`) — **rejected by `d2a2e98`**.
  - mihomo `ProcessBuilder` TUN fd inheritance — **rejected by `dfd685c`** in favour of an
    in-process hev JNI bridge.
  - App-authored node selection/latency (`SmartSwitchEngine`) and `DirectPing` — **rejected by
    `1a0b506`**.
  - Building `libbox.aar` from mutable upstream `testing` for releases — **rejected by
    `89951ae`** (pin in `version.properties`).
  - The `assets/geodata` payload — deleted by `d2a2e98`.

- **Final rule (as implemented today).** One engine, one config generator:
  `app/src/main/java/com/interstellar/proxy/core/ProxyCore.kt`
  (`PlatformFactSink` at :14, `ProxyCore : PlatformFactSink` at :32, `closePlatformEvents()` at :63)
  and `core/SingBoxCore.kt` (:65–83 — raw fact forwarding, exactly one `PlatformEvents` per
  `CommandServer`). README states it at the top: "Android 上的**单内核** sing-box 客户端".
- **Locking evidence.** `.github/workflows/android-ci.yml` (added by `d2a2e98`) and
  `release-apk.yml` contain **no** mihomo/Xray/hev step; `app/build.gradle.kts` `splits` (:196);
  `MinimalConfigBuilderTest.kt:133-142` forbids `gvisor/stack/outbound_mode/bypass_cn/overseas`
  in generated config.
- **Tags:** FEATURE, COMPAT, LIFECYCLE, CORE-INTEGRATION, BUILD, CI, REPOSITORY-HYGIENE |
  OWN, SUPERSEDED, ARCHIVE-ONLY | VERIFIED_GIT.

---

### Cluster: Raw config pass-through vs hidden policy injection

- **Problem.** A subscription body can be (a) a share-link / Base64 node list, (b) Clash YAML,
  or (c) a *complete* core config. If the app "helpfully" rewrites (c) — injecting rules, DNS,
  region groups, ad-blocking, a fallback outbound — the user's stated intent is silently
  replaced, and bug reports become unfalsifiable. The project flip-flopped on this and the git
  record contains **both directions**.

- **Evolution (ordered):**

  1. `ac94df1` (**v0.5.8**, 2026-09-20) — introduces raw pass-through **with injection**.
     New `RawConfigApplier.kt` (+428), `RawConfigFormat.kt` (+94), `SubscriptionFetcher` (+72),
     `Settings` +12. The message says it plainly: "全局'使用原始配置'开关直供匹配内核
     (**兼容性修正+内置规则注入**, 不重写结构; Mix 互斥置灰)". Three formats: clash / sing-box /
     Xray. Also: control port `9090 → 19090`, Content-Disposition RFC5987 subscription names.
     FEATURE/COMPAT. → **SUPERSEDED**.
  2. `d2a2e98` — restricts pass-through to `RawConfigFormat.SINGBOX` and deletes
     `applyClash`/`applyXray` ("不再有能匹配它们的核心"). FEATURE/COMPAT.
  3. `ebd2f79` (2026-10-08) — **the reversal.** The app stops having a policy layer at all:
     - `ConfigBuilder` → minimal generator; `BuildOptions` reduced to
       `selectedNodeTag / mixedPortEnabled / mixedPort / includeTun`; `OutboundMode`,
       `SMART_TAG`, `BLOCK_TAG`, `DNS_HOSTS_TAG`, `deriveRegionGroups`, `ruleSetJson`,
       `customGroups` deleted.
     - **Raw path becomes byte-for-byte pass-through** ("Raw config 侧同时改为逐字节透传").
     - Deletes `RulesStore`, `GeoRuleUpdater`, `SimpleRulesStore`, `DnsOverridesStore`,
       `DnsOverrideEntry`, `NetProbe`, `NodeMatcher`, `RawConfigApplier` **entirely**, and
       `assets/rules/*.srs` (`geoip-cn.srs`, `geosite-cn.srs`, `geosite-geolocation-!cn.srs`,
       `category-ads-all.srs`).
     - Deletes `ProxySettingsPage`, `CustomRulesPage`, `DnsOverridesPage`; `SettingsSubPage`
       → `{ Settings, PerApp, Connections, Logs }`.
     - **Verified counts:** `Settings.kt` fields **30 → 22** (matches the message exactly);
       `strings.xml` keys **423 → 304** = **−119 per language** (matches "每种语言删 119 条"
       exactly; measured at `1a0b506` vs `ebd2f79`).
  4. `d8b3093` — fixes the UI text that still *claimed* injection. Message: "Raw 路径已改为
     逐字节透传, 但启用开关的说明仍写着'自动注入内置规则'… 与实现不符". This is a
     **message/UI-vs-code disagreement that was found and corrected** — 4 lines in each
     `strings.xml`.
  5. `ad91f44` — removes `SettingsPage.PrefToggleRow` (orphaned), renames the "分流 / Routing"
     section to "Android 平台 / Android" because "路由策略还在" is no longer true; re-fixes the
     raw-mode copy. Notes that `CommandClient`'s `ClashMode` transport enum is deliberately kept.
  6. `2f0bcbf` — drops the now-unused `org.json` **test** dependency (only `SmartSwitchEngine`
     needed it; `2f0bcbf`'s own message says main-side `org.json` use is gone).
  7. `aec3893` — removes the user switch completely: raw vs generated becomes **automatic**
     detection (`Libbox.checkConfig` on a full sing-box body → pass through verbatim; otherwise
     generate). Deletes `Settings.useRawConfigEnabled`, `AppViewModel.useRawConfig/
     setUseRawConfig`, the whole "配置模式 / 使用原始配置" block, and changes the *subsidy badge*
     to report "a detected fact" rather than a switch state. `RawConfigFormat` / 3-format
     detection is retained for *labelling and Mix eligibility* only.
  8. `48923e0` — docs commit that writes the kernel evidence into the generator comment so
     nobody "helpfully" re-adds `stack=mixed` (sing-tun `NewStack` maps `""` → Go netstack;
     sing-box marks `tun.stack` deprecated).

- **Rejected / superseded attempts:**
  - "Pass the user's config through **but** add our built-in rules" (`ac94df1`) → **rejected by
    `d2a2e98` + `ebd2f79` + `d8b3093`**.
  - "Let the user choose raw vs rewrite" (`ac94df1`'s global switch) → **rejected by `aec3893`**
    (user must not have to understand raw/rewrite).
  - "Mix + raw together" → resolved as a *data-model* rule: a raw sing-box subscription is
    `standalone`, never mix-capable (`aec3893`).
  - "App pins `tun.stack = mixed/gvisor` and `mtu = 9000`" → **rejected by `aec3893`**, with the
    reason recorded by `48923e0`.

- **Final rule (as implemented today).**
  - `app/src/main/java/com/interstellar/proxy/data/SubscriptionRepository.kt:265-279`
    `applyRawConfigIfMatching()` — pass-through **only** when
    `RawConfigFormat.from(sub.configFormat) == SINGBOX` (:270), the raw file exists (:271), and
    `Libbox.checkConfig(raw)` passes (:273); then `ConfigStore.writeActiveConfig(raw)` — the body
    is written out unchanged. Called automatically from `regenerateActiveConfig()` (:205-216)
    with the comment "no injected rules, no DNS rewrite, no outbound rebuild, no route.final
    override".
  - `SubscriptionRepository.kt:166-169` `isRawConfig()` = sing-box format **and** raw file present;
    `:178` / `:186` exclude raw subs from the Mix pool.
  - `RawConfigFormat.kt` still enumerates `CLASH / SINGBOX / XRAY` — **detection and labelling
    only**; the *gate* is the `SINGBOX` comparison in the repository. (This is the one nuance
    where a naive reading of `d2a2e98`'s message — "只接受 sing-box 格式" — looks like it
    contradicts the surviving enum. It does not: the enum was never pruned, the acceptance gate
    was.)
  - Generated-config side: `data/config/MinimalConfigBuilder.kt:74-75` (route + experimental),
    `:79-86` (explicit "NO clash_api here, not even an empty one"), `:102-104` (no `stack`),
    `:121-124` (`auto_route: true` + LAN `route_exclude_address`), `:216/:222/:237`
    (`dns-local` / `dns-remote` / `final = dns-remote`), `:265` (`route.final = GROUP_TAG`).

- **Locking evidence.**
  - `data/config/MinimalConfigBuilderTest.kt:103-104` — `check("stack" !in tun)`,
    `check("mtu" !in tun)`.
  - `MinimalConfigBuilderTest.kt:133-142` — forbidden-substring sweep (`gvisor`, `stack`,
    `outbound_mode`, `bypass_cn`, `overseas`, …).
  - `MinimalConfigBuilderTest.kt:168-171` — second sweep for `clash_api`, `external_controller`,
    `external_ui`, `19090`, `secret`.
  - `data/subscription/RawConfigDetectorTest.kt` (added `ac94df1`, never touched since).
  - `data/net/SubscriptionFetcherTest.kt` (added `ac94df1`, last touched `d2a2e98`).

- **Tags:** FEATURE, COMPAT, UI, DOC, REPOSITORY-HYGIENE | OWN, SUPERSEDED (the injection
  design), ARCHIVE-ONLY (the switch UI) | VERIFIED_GIT.

---

### Cluster: PlatformFacts — "Android reports facts, the core decides actions"

- **Problem.** The app had its own device-policy state machine
  (`ACTION_DEVICE_IDLE_MODE_CHANGED → core.pause()/wake()`, plus screen handling). Two writers of
  one device state fight each other: a Doze exit would wake a core whose screen is still off.
  Additionally, the bridge that carried Android facts to `libbox` had **no owner**, so a dying
  service instance could unbind a live core's bridge.

- **Evolution (ordered):**

  1. `a3d2321` (2026-10-09) — `feat: wire platform memory and lifecycle facts into libbox`.
     New `bg/PlatformFacts.kt` (+161), new `core/PlatformFactSink` interface
     (`ProxyCore : PlatformFactSink`), `SingBoxCore` gains exactly one `PlatformEvents` per
     `CommandServer` (`Libbox.newPlatformEvents(server)` right after `server.start()`),
     `InterstellarApplication.onTrimMemory` forwards the **raw** level.
     `ProxyCore.pause()`/`wake()` are **kept but documented as no longer driven by Android**
     ("the old `ACTION_DEVICE_IDLE_MODE_CHANGED → pause()/wake()` path was retired … two writers
     of the same device state fight each other"). `PlatformFactsTest.kt` added (186 lines).
     FEATURE/CORE-INTEGRATION/LIFECYCLE.
  2. `1a86247` — *P0-2/P0-3/P0-4.* Introduces the `Session` token and the single-consumer
     `Channel` delivery lane; `attach()` reads state and enqueues **under one lock**;
     `appForeground` initial value changed to **`false`** (a process can start from a tile or a
     background service; "we have not seen onStart" is not foreground evidence); extracts the
     idempotent `releaseCore()` with a fixed order; fixes the Activity counter drift
     (old code `return`ed on `isChangingConfigurations` **without decrementing** — measured
     drift to 4, after which the app reported foreground forever). Adds
     `ActivityForegroundTrackerTest.kt`. FIX/HARDENING.
  3. `51cb7f5` — *P0-B/P0-C.* Makes the session token **non-null/required** (a null token used
     to mean "detach whatever is attached", i.e. an implicit force-detach reachable from ordinary
     teardown); splits *unbind* (only if still current) from *drain* (**unconditional**);
     bounds the drain with a 5 s timeout that reports **false** rather than pretending;
     documents the queue as **`Channel.UNLIMITED`** and explicitly *refuses* to describe it as
     bounded. Introduces **`CoreLifecycle`** as the publish linearization point, splits
     `closePlatformEvents()` out of `ProxyCore.shutdown()`, and makes `onDestroy` route through
     the same idempotent `releaseCore()`. Adds `CoreLifecycleTest.kt`. FIX/HARDENING.
  4. `4d15e3b` — *P1-B.* Makes the foreground fact's platform limit explicit: a configuration
     change whose replacement Activity never reaches `onStart` keeps the last known fact, with a
     **documented platform limitation** instead of a timer; adds `debugFact()` (debug-only
     logcat line, compiled out of release). FIX/HARDENING/DOC.
  5. `a5a6783` — *P1-D, test-only.* Proves every trim level is forwarded verbatim
     (`5/10/15/20/40/60/80/0/999/-1`), and that nothing is delivered after detach. TEST.
  6. `32cc6cc` — *P0-C, test-only.* Pins the fact-bridge handover and the "unprovable drain"
     answer. TEST.
  7. `1efaa8f` — *P1-E, test-only.* Asserts the network path **cannot** reach a wake fact:
     `DefaultNetworkMonitor` / `DefaultNetworkListener` / `PlatformInterfaceWrapper` /
     `InterfaceResolutionEpoch` must contain **zero** occurrences of `reportDeviceWake`,
     `setScreenOn`, `ACTION_SCREEN`, `USER_PRESENT`, `onUserPresent`; `onUserPresent()` is the
     **only** producer of a wake fact. TEST/AUDIT.
  8. `18320df` — *P0-C (teardown).* `startup()` stages `commandServer` **before** `start()` so a
     partial start is never ownerless; `closeReportingFailure()` refuses to swallow
     `CancellationException`; adds `TeardownFailureTest.kt`. FIX/HARDENING.

- **Rejected / superseded attempts:**
  - App-side device policy (`pause()`/`wake()` from Doze broadcasts) — **rejected by `a3d2321`**;
    `pause()`/`wake()` survive as an unused-by-Android interface + explicit comment.
  - Nullable session token / implicit force-detach — **rejected by `51cb7f5`**.
  - "Set the field to null" as a teardown barrier — **rejected by `51cb7f5`** in favour of a
    queue barrier ("proves nothing about a call that has already started").
  - A bounded queue — **explicitly rejected** in `51cb7f5` (bounding would drop/merge one-shot
    facts or reorder an ON/OFF pair).
  - A timer to detect "the replacement Activity never came" — **rejected by `4d15e3b`** as the
    polling the design exists to avoid.
  - A second wake-fact producer anywhere on the network path — **rejected by `1efaa8f`** as a
    pinned assertion.

- **Final rule (as implemented today).**
  - `bg/PlatformFacts.kt:20-53` — class contract: "**This class contains no policy on purpose** …
    no Kotlin branch that maps a level to an action, no threshold, no timer, no RSS polling, no
    reconnect, and no pause."
  - `:289-291` `onMemoryTrim` forwards the level unchanged; `:471-473` `onUserPresent` is the
    only wake producer; `:217-228` `attach()` reads screen+foreground and enqueues under one lock;
    `:253-284` `detachAndDrain(expected)` — non-null token, unbind-only-if-current, unconditional
    barrier, 5 s bound (`:177`).
  - `core/ProxyCore.kt:14-24` — `PlatformFactSink` with the four fact calls and the "no
    implementation may reinterpret a level" rule.
  - `InterstellarApplication.kt:68-69` installs; `:82` forwards `onTrimMemory`.
- **Locking evidence.** `bg/PlatformFactsTest.kt` (added `a3d2321`, last touched `a5a6783`),
  `bg/ActivityForegroundTrackerTest.kt` (added `1a86247`, last touched `4d15e3b`),
  `bg/CoreLifecycleTest.kt` (added `51cb7f5`, last touched `7d4218a`),
  `core/TeardownFailureTest.kt` (added `18320df`), and CI's `:app:testDebugUnitTest` step in
  `.github/workflows/android-ci.yml`.
- **Tags:** FEATURE, LIFECYCLE, HARDENING, CORE-INTEGRATION, TEST | OWN, CORE-COORDINATED |
  VERIFIED_GIT.

---

### Cluster: Screen seed / observation epoch

- **Problem.** `PlatformFacts.install()` registered the screen receiver and then seeded the
  initial value from `PowerManager.isInteractive`. That is a **second, unlocked writer** of the
  same fact, and the correctness of the whole class rested on a looper-affinity argument that
  lived in a *different file* and was checked nowhere.

- **Evolution (ordered):**

  1. `7275c0f` (2026-10-10) — first fix: introduce `ScreenFactState` so both writers go through
     it, plus a version counter. Message is unusually honest about reachability: the overwrite
     is "**在今天的调用路径上不可达**" (no `Handler` is passed to `registerReceiver`, so the
     broadcast queues behind `Application.onCreate` on the main looper) — the change is for
     *shape*, not for a reproduced bug. It also admits the **first fix was incomplete and the
     test caught it**: "最初只把两处写入都收进锁内, 于是 `onChanged` 仍是后写者胜 —— 落在后面的
     旧快照照样赢。`A01` 直接断言失败。加入版本号后才通过。" Adds `ScreenFactOrderingTest.kt`
     and `InstallGuard` (one-way latch, no reset). FIX/HARDENING.
     → the *transition-counting* version guard is **SUPERSEDED** by (2).
  2. `481e370` (2026-10-10, "Round 8 P0, found by reading the source rather than the report") —
     **two independent defects in (1)**, both fixed:
     - the key was sampled **after** the platform read, so it already contained the observation
       it was meant to detect;
     - the key counted level **transitions**, so a repeated same-value broadcast could not
       invalidate an older reading.
     Fix: count **observations**, take the epoch **before** the read, and move the production
     algorithm into one place, `PlatformFacts.seedInitialScreen`. Again reachability is not
     overstated ("not reachable on today's call path… a latent contract defect").
     Reports RED→GREEN: 5 named failures (`S02`, `S10`, `S03`, `S07`, `A04b`) at 210 tests,
     then 0 failed. FIX.
  3. `15c432d` — new `ScreenSeedWiringTest` (S01–S12, 426 lines) driving the **real**
     `seedInitialScreen`; `S12a`/`S12b` deliberately re-create *the removed shapes* and show they
     accept a stale reading; `ScreenFactOrderingTest.A04b` is rewritten because "its old form
     asserted the opposite of the contract the fix depends on". TEST.
  4. `ae89388` — `S08` was **flaky**: it fired one observer + one seeder at a latch and asserted
     *both* outcomes occurred over 200 rounds; on a fast runner the observer always won. Pinned CI
     run on `54a3eb0` failed with `IllegalStateException: no snapshot was ever applied in 200
     rounds`. Rewritten as an explicit handshake, both paths 200/200. TEST/FIX.

- **Rejected / superseded attempts:**
  - Lock-the-second-writer-only (`7275c0f` first attempt) — **rejected by its own test**.
  - Transition-count guard keyed after the read (`7275c0f` as shipped) — **rejected by `481e370`**.
  - Relying on the looper-affinity argument — **rejected by `7275c0f`** ("靠线程论证" is replaced
    by a structural refusal).
  - A timing/latch-based concurrency test — **rejected by `ae89388`** ("it made a race a
    coin-flip that decided whether the test may pass").

- **Final rule (as implemented today).**
  - `bg/ScreenFactState.kt:69` class; `:84` `observationEpoch` (counts **observations**, not
    transitions); `:95` `observationEpoch()`; `:108-113` `onChanged` bumps every time and returns
    "did the level change?" (two different questions, deliberately separated at `:100-107`);
    `:134-139` `seedSnapshot(value, observedBefore)` applies **only** when the epoch is unchanged;
    `:155-167` `InstallGuard`.
  - `bg/PlatformFacts.kt:526-531` `seedInitialScreen` — epoch **first**, `screenSeedHook?.invoke()`,
    then `readInteractive()`, then `seedSnapshot`; `:506` `screenSeedHook` is the test seam
    (null in production); `:590` the single production call site; `:545-553` idempotent `install()`.
- **Locking evidence.** `bg/ScreenSeedWiringTest.kt` (added `15c432d`, last touched `ae89388`),
  `bg/ScreenFactOrderingTest.kt` (added `7275c0f`, last touched `15c432d`).
- **Tags:** FIX, HARDENING, LIFECYCLE, TEST | OWN | VERIFIED_GIT.

---

### Cluster: Foreground fact ordering & reentrancy

- **Problem.** `onForegroundChanged` was an **expression body** (`= synchronized(lock) { … }`), so
  listener notification ran **while holding the lock**. A listener is not a leaf: the UI's
  listener reaches `connect()`/`disconnect()`, which call `addForegroundListener` /
  `removeForegroundListener` back into the same object. The JVM lock is reentrant, so this never
  deadlocked — it did something worse: a nested `addForegroundListener` replayed the canonical
  value *from inside the outer notification*, so one edge could produce a second one, depending
  on whether a client happened to be subscribed. The core side had already banned this shape
  (`runtimecoord.PlatformEvents`: "It is NEVER held across a target call").

- **Evolution (ordered):**

  1. `3c65418` (2026-10-11) — `feat(ui)`: `PlatformFacts` notifies registered foreground listeners
     from the same place it updates `appForeground`, over a **copy** of the listener list;
     `addForegroundListener` delivers the canonical value immediately. Message: "Committed on a
     **new branch** rather than main, deliberately: main is left at `e1ec9f8`, untouched"
     (branch = `jiejiebox/v0.1.6-ui-client-lifetime`, recovered from reflog). FEATURE.
  2. `3813a23` — *P0-A + P0-B.* P0-A: `subscribeForeground()`'s synchronous `false` replay called
     `suspendUiClient()`, and `connect()` then called `commandClient.connect()`
     **unconditionally** — so the canonical state said "background, no client" and `connect()`
     built one anyway. P0-B: the lock-held dispatch above. Fix: record under the lock, dispatch
     **after releasing**, register under the lock and replay outside it. Adds
     `PlatformFactsListenerLockBoundaryTest.kt`. FIX/HARDENING.
  3. `36dee73` — "adversarial debug pass over this round's **own** diff. Both are defects
     introduced or left by the previous commit, so they are fixed here rather than reported."
     Releasing the lock **gave up the serialisation the lock was providing for free**: two facts
     recorded in order on two threads could reach listeners in the opposite order, so the *last*
     value a listener acted on was one already superseded. Reproduced deterministically: listener
     handed `[false, true]` for facts recorded `true` then `false`. Fix: each notification takes
     a sequence number under `lock` and delivers in turn; `removeForegroundListener` is documented
     as **not a barrier**. Also: the wiring test could be walked around (it compared positions in
     the function body; inserting two lines before the early return restored the P0-A defect while
     all 196 tests stayed green). FIX/HARDENING.
  4. `56eb255` (2026-10-12) — `foregroundDelivered` was initialised to `0L` while the wait was
     `foregroundDelivered < ticket - 1`. Ticket `1` needed predecessor `0`, which the watermark
     already held **before anything had been delivered** — so ticket 1 ran its listener while
     ticket 0's listener was still on the stack. Reachable on **the first pair after every process
     start**. Fix: initialise to `-1L` ("nothing delivered yet") and make the wait an **equality**
     against `ticket - 1`. Measured: before, the second `onForegroundChanged` returned in 0 ms;
     after, ~350 ms. FIX.
  5. `15b5ff1` (2026-10-12) — a **reentrant** notification was also given a ticket. The nested
     frame had to publish something, and it published `ticket - 1`; the outer frame then published
     its own **lower** ticket, moving the watermark **backwards**, so ticket 2 spun forever in
     `awaitForegroundTurn` (`Thread.yield()`). In the app that is not a wrong value but a **silent
     stop**: the foreground listener never runs again, so `resumeUiClient()`/`suspendUiClient()`
     stop being driven. Fix: only outermost frames take a ticket; reentrant ones deliver inline;
     the publish is monotonic (`maxOf`). FIX.
  6. `2cb6eb9` (2026-10-12) — composition test: the `startProxy` lifetime guard **and** the
     foreground ordering guard, driven end to end through the real objects. TEST.

- **Rejected / superseded attempts:**
  - Holding the lock across listeners (`3c65418` shape) — **rejected by `3813a23`**.
  - `foregroundDelivered = 0L` + `<` comparison — **rejected by `56eb255`**.
  - Giving reentrant notifications a ticket — **rejected by `15b5ff1`**.
  - "Move `commandClient.connect()` above `subscribeForeground()`" — **explicitly rejected by
    `3813a23`**: "It narrows the window and then suspends a client that was created a moment
    earlier; the verdict has to decide, not the ordering."
  - Source-position assertions in the wiring test — **rejected by `36dee73`** in favour of
    asserting what is *inside* a braced gate block, counting establishes.

- **Final rule (as implemented today).**
  - `bg/PlatformFacts.kt:99` listener list; `:110` `foregroundSequence`; `:126`
    `foregroundDelivered = -1L` with the full rationale at `:112-124`; `:135` `insideListener`
    (`ThreadLocal`); `:302-323` `onForegroundChanged` — braces, not an expression body, with the
    notification **outside** the `synchronized` block; `:380-421` `dispatchForegroundListeners`
    (reentrancy at `:396-407`, ticket at `:404`, monotonic publish at `:419`);
    `:436-440` `awaitForegroundTurn` (equality, `Thread.yield()`); `:459-465`
    `addForegroundListener` (register under lock, replay outside).
- **Locking evidence.** `bg/PlatformFactsListenerLockBoundaryTest.kt` (added `3813a23`, last
  touched `15b5ff1`), `ui/AppViewModelWiringTest.kt` (added `3c65418`, last touched `d10c9a5`),
  `ui/UiClientLifetimeTest.kt` (added `3c65418`, last touched `2cb6eb9`).
- **Tags:** FIX, HARDENING, UI, LIFECYCLE, TEST | OWN | VERIFIED_GIT.

---

### Cluster: UI command-client lifetime

- **Problem.** Backgrounding the app has to switch off the main UI `CommandClient` (1 Hz `Status`
  tick, `Groups`/`Outbounds` control plane, 2 s reconnect poll, chart sampling) while the VPN, TUN,
  core, command server and connections keep running. The trap: `CommandClient.disconnect()`
  notifies handlers on the **deliberate** path too, and `AppViewModel` reacts by calling
  `applySocketDrop()`, which for `Status.Started` arms a **400 ms watchdog** that calls
  `markStopped()` **and `BoxService.stop()`**. So a naive `onBackground { commandClient.disconnect() }`
  tears the running tunnel down ~400 ms later.

- **Evolution (ordered):**

  1. `3c65418` — introduces `ui/UiClientLifetime.kt` (196 lines) as the single decision object
     ("three decisions are one decision set"), fed from `PlatformFacts`, testable on a plain JVM
     (no Compose). `AppViewModel` keeps the side effects; the lifetime keeps the rules — the same
     split the project already used for `NotificationPublishGate`. FEATURE.
  2. `3813a23` — P0-A above; `connect()` now reads `uiLifetime.wantsClient()` **after** the
     subscription and the verdict gates both the establish call and the reconnect poll.
  3. `d10c9a5` — `startProxy()` also called `commandClient.connect()` **directly**, and
     `commandClient` is **one** socket — the same instance the guarded `connect()` hands to the
     UI. Reachable while suspended: tap start, background the app inside the startup window
     (everything after `delay(250)` runs on `Dispatchers.IO`) → `onBackground → true`,
     `wantsClient → false`, then the unguarded call rebuilt the client after the disconnect with
     nothing later to correct it (`mayReconnect()` gates new polls and never closes an existing
     client). Fix: `if (uiLifetime.wantsClient())` at the last moment before the establish; the
     call is kept because `onConnected()` + the Status tick are what promote `Starting → Started`
     and cancel `startingWatchdog`. FIX.

- **Rejected / superseded attempts:**
  - Deriving the foreground edge from a Compose `ON_STOP` effect — **rejected by `3c65418`**: it
    would treat a theme switch / UI-mode / display-size / font-scale change as backgrounding, i.e.
    a **second writer of one fact**.
  - A bare `onBackground { disconnect() }` — **rejected by `3c65418`** (400 ms tunnel teardown).
  - Removing the startup establish — **rejected by `d10c9a5`** (load-bearing for `Starting →
    Started`).
  - `UiClientLifetime` documented as main-thread-only — **later corrected in the same file**: the
    url-test's `finally` runs on `Dispatchers.IO` and calls back through `reapplyOwedSuspend`, so
    two threads raced a read-modify-write; the methods are now synchronized (`UiClientLifetime.kt:44-51`).

- **Final rule (as implemented today).**
  - `ui/UiClientLifetime.kt:53` class; `:64` `foreground = true` (cold start behaves as before);
    `:86` `owesSuspend`; `:101-113` `onBackground(headlessProbe)` (a suspend is never silently
    consumed by an in-flight probe); `:123-129` `onForeground` (idempotent → stops the duplicate
    `connect()` → `onDisconnected()` → `applySocketDrop()` chain); `:139-140`
    `mayReportSocketLoss`; `:157` `onTearingDown` / `:160` `onClientConnected`; `:168`
    `mayReconnect`; `:171` `wantsClient`; `:181-188` `snapshot()` (all four facts under **one**
    lock acquisition).
  - Wiring: `ui/AppViewModel.kt:448` (listener → resume/suspend), `:463` subscribe, `:493-494`
    verdict gates the establish, `:510` / `:519` `mayReconnect`, `:535` `onTearingDown`, `:553`
    `suspendUiClient`, `:562` `onBackground`, `:575-577` `reapplyOwedSuspend`, `:586-588`
    `resumeUiClient`, `:722` `wantsClient()` before the `startProxy` establish, `:970`
    `reapplyOwedSuspend()` in the url-test `finally`.
  - The `onTearingDown` docstring records the sibling defect reached without backgrounding:
    `disconnect()` cancels `startingWatchdog` **and then** fires `onDisconnected()` →
    `applySocketDrop()` → **re-arms** the watchdog it just cancelled.
- **Locking evidence.** `ui/UiClientLifetimeTest.kt`, `ui/AppViewModelWiringTest.kt` (both
  `3c65418`-born), `bg/PlatformFactsListenerLockBoundaryTest.kt`, `bg/PlatformFactsTest.kt`
  (composition test `2cb6eb9`).
- **Tags:** FEATURE, FIX, HARDENING, UI, LIFECYCLE, TEST | OWN | VERIFIED_GIT.

---

### Cluster: Service lifecycle, start-attempt ownership, stale generation

- **Problem.** `BoxService` owns single slots (`core`, `factSession`, `fileDescriptor`) and a
  foreground notification that is a **process-wide** slot. A start that was still inside
  `startup()` could write its core into slots a teardown was clearing; a stale stop could write
  `Stopped` + `stopSelf()` over a **newer** generation; and a destroyed `Service` instance could
  still start a core.

- **Evolution (ordered):**

  1. `51cb7f5` — introduces **`CoreLifecycle`** (generation + publish token). `startCore` takes a
     token via `beginStart()`; `releaseCore` calls `invalidate()` first; `publish(token)` refuses
     a superseded attempt; the `publish` block runs **inside** the critical section so "core
     without its bridge" is impossible. `CoreLifecycleTest.kt` added (6 cases).
  2. `5d9f4f2` — *P0-A/P0-D.* `onDestroy()` only released when `core`/`factSession`/
     `fileDescriptor` were already non-null — but the dangerous window is exactly the one where
     **all three are still null** inside `created.startup()`. Adds a **terminal `closed`** state;
     `beginStart()` returns null when closed (refusing *before* any native object exists);
     `close()` invalidates in-flight attempts **and** refuses not-yet-started ones. Explicitly
     **deletes** a `closed` check inside `publish()` as "按构造不可达" after fault injection showed
     it changed nothing. Also introduces `CoreStartResult{PUBLISHED/SUPERSEDED}`. And fixes a
     **cross-instance** defect: `DefaultNetworkMonitor` used the singleton itself as the
     `DefaultNetworkListener` key, so an old instance's teardown removed the successor's
     registration → key changed to the owner. FIX/HARDENING.
  3. `bff3ce6` — `CoreLifecycle.published` was set only in `publish()` and **never cleared**, so
     `isPublished` kept claiming a live core after release. Fix: `invalidate()` and `close()` both
     clear it. FIX.
  4. `74ff3ce` (**round 5**) — `stopAndAlert` is reachable from `serviceReload0()` while
     `status == Status.Stopped` (a one-shot `standaloneClient().serviceReload()` never consults the
     Service's status), so during the IO release a user tap could start generation **B**, and the
     alert's finalizer then wrote `Stopped` on B and `stopSelf()`-ed. Adds a `startAttempts`
     watermark moved **only by `beginStart`**, plus `startsSince(watermark)` — because `generation`
     could not serve as the watermark (`releaseCore()`'s own `invalidate()` would make every stop
     look superseded by itself). Adds `StopConvergenceTest.kt`. FIX/HARDENING.
  5. `bdb4ced` (**round 6, P0**) — two gaps the round-5 watermark missed, both "实际验证出来的,
     不是推测": (i) `stopAndAlert` did `unregisterReceiver` + `notification.close()` +
     broadcast the alert **before** asking `startsSince` — and those first two harm a successor
     because the notification slot and the registration are process-wide; (ii) the start token was
     claimed in the IO coroutine's `startCore()`, i.e. **too late** — between `Status.Starting`
     (main) and `beginStart()` (IO) a stop saw "no newer start". Fix: claim the token in
     `onStartCommand()` on the main thread **before** `status.value = Status.Starting`; take **one**
     decision after the release and then do **all** convergence effects or **none**; keep the alert
     broadcast before the ownership check (reporting is its only job, no resource side effects);
     **delete `pendingRestart`** (a boolean that a stale generation could clear had to encode both
     "a start was requested during teardown" and "whose request is it"). FIX/HARDENING.
  6. `7d4218a` (**round 7 / R7-P0**) — round 6's isolation left the mirror half: A's
     `releaseCore()` → `lifecycle.invalidate()` unconditionally advanced the generation, so B's
     `publish(B)` was refused, while A's finalizer withheld its convergence because B *was*
     accepted. **Two halves each "correct", together a deadlock**: `Status.Starting` with no core
     behind it, spinning until the process died. Window = `releaseCore()` duration = up to two 5 s
     drain timeouts + native teardown. Fix: split "who may publish" into three independent rules,
     each moved by the event that actually moves it — (1) newer start: `attempt == startAttempts`
     (**equality**, not a boundary: with a boundary, take token 2, publish, take token 3 → both
     publish and the first core is silently replaced); (2) a stop's teardown:
     `attempt > stopStaleUpTo`, pinned by `endStop()`; (3) destroy: `closed`, checked first. The
     second counter (`generation`) is **removed**. Adds `RestartOwnershipTest.kt`. FIX/HARDENING.
  7. `039bf63` — writes down the invariant that makes "close the notification only at convergence"
     safe, plus the bounded cost (a user pressing Stop while a fact call is wedged waits ~10 s).
  8. `cf3dc09` — *P0-E.* `VpnService.onRevoke()` did
     `runBlocking { withContext(Dispatchers.Main) { … } }` **from the main thread** — a
     self-deadlock (ANR) the moment the dispatch is actually needed. Fix: call it directly;
     `BoxService.onRevoke()` only sets `Stopping` and hands teardown to its own IO coroutine.
     Explicitly records: revoke needs a system-level trigger, this was **not** device-tested.
     FIX.

- **Rejected / superseded attempts:**
  - Releasing on `onDestroy` only when fields are non-null — **rejected by `5d9f4f2`**.
  - A `closed` check inside `publish()` — **deleted by `5d9f4f2`** as structurally unreachable
    (healthy: the message records the fault injection that proved it).
  - `pendingRestart` boolean — **deleted by `bdb4ced`**.
  - Claiming the start token inside `startCore()` — **rejected by `bdb4ced`**.
  - `invalidate()` deciding successors' fate — **rejected by `7d4218a`**.
  - `generation` as the stop watermark — **rejected by `74ff3ce`** (a stop would supersede itself).
  - A boundary comparison instead of equality in `ownsServiceLocked` — **rejected by `7d4218a`**.
  - Doing the convergence side effects *before* the ownership check (`stopAndAlert`, round ≤5) —
    **rejected by `bdb4ced`**.
  - `runBlocking { withContext(Main) }` in `onRevoke` — **rejected by `cf3dc09`**.

- **Final rule (as implemented today).** `bg/BoxService.kt`:
  - `:151-190` `startCore(attempt)` (token already claimed by the caller at `:555`);
    `:177-182` `lifecycle.publish(attempt) { core = created; factSession = PlatformFacts.attach(created) }`
    — publish and attach are one indivisible step.
  - `:212-288` `releaseCore()` — `beginStop()` → `invalidate()` → `detachAndDrain` (one retry,
    `:234-247`) → **close the native bridge only if the drain is proven** (`:252-273`, with the
    recorded tradeoff that an unproven drain leaves one unreachable Go object rather than risking
    a use-after-close, and still shuts the server down because "an unstoppable tunnel is worse") →
    fd → `DefaultNetworkMonitor.stop(this)` (`:281`) → `running?.shutdown()` → `finally { endStop() }`.
  - `:436-473` `stopService()` — watermark at `:439` **before** the release; **one** decision at
    `:457`, then all-or-none convergence.
  - `:475-519` `stopAndAlert()` — alert broadcast first (`:495-497`), ownership check at `:503`.
  - `:523-628` `onStartCommand()` — `isClosed` refusal at `:527`, `beginStart()` at `:555`
    **before** `status.value = Status.Starting` (`:561`), bounded `MAX_START_SUPERSEDED_RETRIES`
    loop (`:56`, `:591-613`) that distinguishes a **transient** teardown refusal from a **newer
    start** (`isSupersededByNewerStart`), then `releaseCore()` if a stop landed (`:621-624`).
  - `:633-655` `onDestroy()` — **unconditional** `lifecycle.close()` (`:641`) + unconditional
    `releaseCore()` on `GlobalScope` (deliberate, documented at `:651-653`).
  - `:738-1026` `CoreLifecycle` — `startAttempts` (:760, only `beginStart` moves it :783-796),
    `stopStaleUpTo` (:771, pinned by `endStop` :846-850), `teardownsInFlight` (:774),
    `invalidate` (:806), `startsSince` (:871-873, `closed || startAttempts != watermark`),
    `PUBLISH_TEARDOWN_WAIT_MS = 12_000` (:884), `close` (:892-903), `publish` (:958-983, waits on
    the monitor and **re-checks ownership on every wakeup, and again immediately before the
    write**), `ownsServiceLocked` (:1012-1015 — three conditions, documented at :985-1011).
- **Locking evidence.** `bg/CoreLifecycleTest.kt`, `bg/StopConvergenceTest.kt`,
  `bg/RestartOwnershipTest.kt`; CI `:app:testDebugUnitTest`.
- **Tags:** FIX, HARDENING, LIFECYCLE, TEST, DOC | OWN | VERIFIED_GIT.

---

### Cluster: Notification publish gate & generations

- **Problem.** `ServiceNotification` is both the notification *target* and a `CommandClient.Handler`,
  and it operates a **process-wide** FGS notification slot.

- **Evolution (ordered):**

  1. `e8bd186` — *P0-D.* (i) a `scope` cancelled by `close()` could not be revived on the same
     instance, but `pendingRestart`'s Stop→Start called `show()` again → `released = false` while
     `connect()` could only launch onto a cancelled scope: the dynamic rate notification died
     silently. Fix: a per-generation `Session` (scope + client). (ii) Explicitly records
     "normal `stopService` does not close the notification" as **NOT REPRODUCED** (three paths do
     close it). (iii) The real bug: `if (released) return` then `notify()` is a two-step
     check-then-act; `close()` could cancel between them and a stale update re-posted a
     notification belonging to no foreground service (surviving `stopSelf`). Fix: extract the pure
     `NotificationPublishGate` so "check the generation + publish" is one critical section.
     Adds `NotificationPublishGateTest.kt` (6 cases). FIX/HARDENING.
  2. `24c8372` — *P0-B*, three defects: (1) every client got the **same** handler, and the callback
     read the **current** generation, so an old client's callback after Stop→Restart was authorised
     **under the new session's identity**; fix: `GenerationHandler(token)` per client, and every
     publish point carries the caller's own token. (2) `show()` unconditionally called
     `gate.open()`, so a `Starting → Started` text refresh minted a new generation and made the
     previous client's token stale; fix: `beginOrRefreshSession()` — reuse the current generation
     unless it was closed. (3) `PlatformInterfaceWrapper.onCoreTraffic` is a native callback with
     no client token and called the public `updateTraffic`; fix: `updateCoreTraffic()` resolves
     against the **live** generation and refuses once closed, and the token-taking method became
     private. FIX/HARDENING.
  3. `87b3358` — *P1-4.* (1) the receiver could be registered **after** `close()` (check +
     `registerReceiver` were two steps); fix: one lock for register/unregister with "check released
     + register" inside the same critical section. (2) `stopAndAlert`'s failure path left
     `pendingRestart = true`, so a later unrelated `stopService` completion triggered an unwanted
     auto-restart; fix: clear it explicitly (a failure is not a "switch"). FIX.
  4. `67a6df0` — binds the notification's status client to its **own** `CoroutineScope`
     (SupervisorJob + IO) instead of `GlobalScope`, cancelling **last** (disconnect →
     stopForeground → cancel notification → unregister receiver → then cancel). Explicitly keeps
     the notification's independent `Status` stream: it belongs to the foreground Service and must
     work with no Activity/ViewModel, and its own `SCREEN_ON/OFF` watching is a **different
     responsibility** from the core's `PlatformEvents` screen axis. REFACTOR/LIFECYCLE.
  5. `bff3ce6` / `0a299ce` — `SessionGate.commitConnected` so the connections "connected" flag is
     committed through the same lock and generation as the list (`stopSession` clears the list
     **and** marks offline inside one critical section).

- **Final rule.** `bg/ServiceNotification.kt:367` `NotificationPublishGate`; `:389`
  `beginOrRefreshSession()`; `:120` `generation = gate.beginOrRefreshSession()`; `:174`
  `gate.isOpen(generation)`; `:256` refuse to install a receiver on a closed generation;
  `:282-283` publish under the **caller's** token; `:300` publish and close inside one lock;
  `:326-342` `close()` closes the generation first, then explicitly cancels.
- **Locking evidence.** `bg/NotificationPublishGateTest.kt` (added `e8bd186`, last touched
  `24c8372`).
- **Tags:** FIX, HARDENING, LIFECYCLE, UI, TEST | OWN | VERIFIED_GIT.

---

### Cluster: Connections session gate, page-scoped monitoring, logs

- **Problem.** Connections and logs were **eager, process-lifetime** streams: `Libbox.newConnections()`
  + `CommandClient(Connections)` created in the ViewModel constructor and connected unconditionally
  from `AppRoot`, even if the user never opened the page — plus a 1.5 s reconnect poll, a full
  connection-list rebuild + sort + `StateFlow` publish per event. Logs did `buffer.toList()` on
  **every** arriving line.

- **Evolution (ordered):**

  1. `060b009` — connections become a **page-scoped session**: `setActive(Boolean)` idempotent,
     store/client created only while visible; leaving bumps `sessionGeneration` first, then
     cancels the poll, disconnects, drops store/client and clears the snapshot; every entry is a
     fresh session (the kernel replays full initial state to a new subscriber, so there is no
     stale closed-connection residue). Also removes the home page's connections dependency and
     replaces the egress-IP card with real runtime status. Explicitly **keeps** the synchronous
     `runBlocking` consumption and says why: `CommandClient.writeConnectionEvents` passes the
     native `ConnectionEvents` object through **directly**, and events are only valid for the
     duration of the callback, so async consumption would need a deep copy and would introduce
     reorder/drop/close-race risk; what was skipped instead is "all the work nobody is looking at".
     PERF/MEMORY/UI.
  2. `d3af634` — *P1-3.* `sessionGeneration/store/client` were plain unsynchronised fields and the
     final publish had one `isCurrent()` check, so `callback checks current → page closes and
     clears → callback writes its expired snapshot` resurrected a closed page's list. Fix: extract
     the pure `SessionGate`, make the generation `@Volatile`, do the generation check **inside the
     lock** shared with the clear, fix the `stopSession` order (stop the source first, then bump +
     clear atomically), mark store/client `@Volatile`. Adds `SessionGateTest.kt` (7 cases,
     including a real 2-thread 200-round race whose invariant fails under the old
     "check-then-write" shape). FIX/HARDENING.
  3. `0a299ce` — *P1-A.* `commitConnected(candidate, connected)` so the flag shares the lock and
     generation with the list; `stop()` clears the list **and** marks offline in one critical
     section; `stopSession()` no longer writes `_connected` itself. FIX/HARDENING.
  4. `7416c73` — logs: `core/LogRingBuffer.kt` (fixed capacity 3000, O(1) append with overwrite,
     `snapshot()` in time order, `clear()` nulls the slots to actually release references, one
     monitor for serialisation) replaces `ArrayDeque` + per-line `toList()`; the UI is decoupled
     with a 75 ms coalescing ticker gated by a tiny pure `LogUiPublishGate`; invisible ⇒ no
     snapshot generated at all; auto-scroll follows **only** when the user is already at the
     bottom, using `scrollToItem` rather than animation. PERF/MEMORY/UI.
  5. `fb606bf` — runtime acceptance found that "only follow at the bottom" left the first entry
     sitting on the **oldest** line; fix: position at the newest on entry, then follow only at the
     bottom. FIX/UI.
  6. `7b6aeb3` — *P1-1/P1-2.* (1) `LaunchedEffect(logs.size)` stops firing once the ring is full
     (size no longer grows) — exactly when following matters most; fix: a monotonic ring
     **version**, with `snapshotWithVersion()` returning (snapshot, version) **under one lock**.
     (2) `markPublished()` marked "the current newest version", so an append landing between
     `ring.snapshot()` and `markPublished()` was marked published without being in the list —
     if no further line arrived it would never be displayed; fix: `markPublished(version, now)`
     must mark the **snapshot's** version, `isDue(version, now)` takes the version explicitly, the
     gate is lock-protected, and the duplicate `appendVersion` `AtomicLong` is removed (the ring's
     version is the single authority). FIX/HARDENING.

- **Rejected / superseded attempts:**
  - Eager process-lifetime connections/logs streams — **rejected by `060b009`/`7416c73`**.
  - Async consumption of `writeConnectionEvents` — **rejected by `060b009`** (native event validity).
  - `LaunchedEffect(logs.size)` as the follow key — **rejected by `7b6aeb3`**.
  - Content-position-based assertions in tests — **rejected by `36dee73`** (see that cluster).

- **Final rule.** `ui/ConnectionsViewModel.kt:81` `setActive`, `:132` `stopSession`, `:181`
  `gate.commit(generation, list)`, `:218` `SessionGate`, `:229` `start()`, `:235` `commit()`.
  `core/LogRingBuffer.kt:20` class, `:40` `version`, `:74` `snapshotWithVersion()`.
  `ui/LogsViewModel.kt:30` `LogUiPublishGate`, `:43` `isDue(version, now)`, `:57`
  `markPublished(version, now)`.
- **Locking evidence.** `ui/SessionGateTest.kt` (added `d3af634`, last touched `bff3ce6`),
  `core/LogRingBufferTest.kt` + `ui/LogUiPublishGateTest.kt` (both added `7416c73`, last touched
  `7b6aeb3`).
- **Tags:** PERF, MEMORY, FIX, HARDENING, UI, TEST | OWN | VERIFIED_GIT.

---

### Cluster: Traffic / status data sourcing (a real revert)

- **Problem.** After `ebd2f79` removed `experimental.clash_api` from the generated config, the
  home page's cumulative traffic, the notification's rate, and per-connection bytes read **0**.
  A wrong diagnosis was committed, then reverted one commit later.

- **Evolution (ordered):**

  1. `919089d` — diagnosis: `box.go`'s `trafficcontrol.Manager` is created only when
     `needClashAPI || needAPIService || PlatformLogWriter != nil`, and libbox does not set
     `PlatformLogWriter`, so removing `clash_api` made `TrafficAvailable = false` and every byte
     counter zero. Fix: add an **empty** `"clash_api": {}` object, arguing from kernel source that
     an empty object creates the manager while `external_controller == ""` means **no listener**.
     **Explicitly rejects** restoring `127.0.0.1:19090` or any unauthenticated controller.
     Independently valuable and **kept**: the "unknown ≠ 0" semantics — `SpeedState.trafficAvailable`,
     `trafficDisplay(running, available) → Idle/Unavailable/Live`, `bytesOrUnknown(...)`, and the
     home card showing "—" + "统计不可用" instead of a fake `0 kB/s`. Adds `TrafficDisplayTest.kt`.
  2. `fb5a361` — **the premise was wrong; this reverts it.** Verified against the *pinned* core
     (`c35faabf4`) and the *old* pin (`a8fdc36c`): the creation condition is identical in both, and
     `daemon/instance.go`'s `StartedService.newInstance()` sets `PlatformLogWriter: s`
     **unconditionally**, and libbox goes through `StartedService`/`AttachedService` — so
     `PlatformLogWriter != nil` is **always true for libbox** and the traffic manager is always
     created, regardless of `clash_api`. Runtime check: a raw sing-box JSON **without** `clash_api`
     still reported non-zero cumulative traffic. So `clash_api: {}` was inert surplus config that
     invited back a block the project had deliberately removed. **Removed.** The real explanation
     of the observed `Σ 0`: the measurement was wrong — the generated tun has
     `route_exclude_address` for `10/8`, `172.16/12`, `192.168/16` (throw routes), so private-range
     traffic never enters the tunnel, and `adb shell` (including ping/ICMP) does not use VPN routes
     at all; neither can be counted. Correct validation is traffic that actually traverses the
     core (measured: a rate-limited subscription download moved `Σ 1.1 MB → 2.1 MB`, the
     notification showed `197 kB/s ↓` matching a ~185 KiB/s server limit, and one connection
     showed `↑0.1 kB ↓5.2 MB`). The `TrafficAvailable` semantics are **kept but re-described** as
     "respecting the kernel's authoritative report", not as a functional switch.

- **Rejected / superseded attempts:**
  - `clash_api: {}` as a statistics enabler — **REVERTED by `fb5a361`**.
  - `external_controller` / port 19090 — rejected twice (`919089d` explicitly, `fb5a361` by test).
  - Treating `TrafficAvailable == false` as `0 B/s` — rejected by `919089d`'s three-state model.

- **Final rule.** `data/config/MinimalConfigBuilder.kt:79-86` — "NO `clash_api` here, not even an
  empty one"; `ui/AppViewModel.kt:66` `enum class TrafficDisplay { Idle, Unavailable, Live }`,
  `:69-72` `trafficDisplay`, `:81` `bytesOrUnknown`. README documents the exact kernel condition.
- **Locking evidence.** `data/config/MinimalConfigBuilderTest.kt:161-171` — the comment states the
  real kernel mechanism and the test locks out `clash_api`, `external_controller`, `external_ui`,
  `19090`, `secret`; `ui/TrafficDisplayTest.kt` (7 cases: three states, "unavailable is not
  written as 0 B", "a real 0 is displayed as 0").
- **Tags:** FIX, HARDENING, UI, CORE-INTEGRATION, TEST, DOC | OWN, SUPERSEDED |
  VERIFIED_GIT (the revert) — the commit message of `919089d` **disagrees with its own later
  correction**, which is recorded in `fb5a361`.

---

### Cluster: Core provenance & release gates

- **Problem 1 — identity drift.** The APK's `BuildConfig` recorded core commit `aaaefb00` while the
  packaged `libbox.so` contained `constant.Version = aac61e52`. Root cause: two different sources —
  `constant.Version` comes from the core's own git HEAD at build time, while `CORE_COMMIT` was read
  from the sing-box checkout **at APK build time**. The AAR was built at 15:58 (HEAD `aac61e52`);
  `aaaefb00` appeared at 16:23; the APK was built at 16:25 with a checkout that had moved on.
  Verified with `strings`: the binary contains `aac61e52` and not `aaaefb00`, plus 7330 gVisor
  symbols (so it is genuinely the post-gVisor build, not a stale AAR).

- **Evolution (ordered):**
  1. `82f0763` — CI: push a `v*` tag → build + publish a release APK; unify the version into
     `version.properties`. BUILD/CI/RELEASE.
  2. `234b0ef`, `9c9f8c8`, `ff85db8` — the first three real CI failures, each a different class:
     `sdkmanager` needed the full `cmdline-tools` path (runner `PATH`); `app/libs` must be created
     after `checkout` (the directory only ever held the gitignored `libbox.aar`); **`gradlew` was
     committed mode `100644` and had to become `100755`** plus a defensive `chmod` in the build step
     (this is the commit that tag `v0.3.0` points at). CI/BUILD.
  3. `89951ae` — **identity fix.** One source of truth: the core's `build_libbox` emits
     `libbox.provenance` (`commit=` / `version=`) next to the artifact; the app reads **that** and
     uses the git checkout only as an explicit-warning fallback. New Gradle task
     `verifyCoreProvenance` (depended on by `assembleDebug/Release`) byte-searches `libbox.so` for
     the declared commit/version and **fails** on mismatch — with four enumerated cases (A pass,
     B commit mismatch, C bare-SHA version mismatch, D tag-form version containing the real SHA),
     and unverifiable combinations also **fail** rather than degrade to a warning. Adds
     `.github/scripts/check_core_provenance.py` (112 lines). Release pins the core via a new
     `version.properties` `coreCommit=<40-hex>`; the release workflow validates the format, that
     upstream serves the commit, and that HEAD matches after checkout, and emits `build-info.json`.
     About page becomes 版本 / 内核来源 / 内核分支 / 内核版本. FIX/BUILD/CI/RELEASE.
  4. `f6a91df` — *P0-A: evidence attribution, not a missing gate.* The project already had a strict
     pin path; the defect was that the previous round cited a dev-CI run
     (`37913585896`) as evidence for `fb5a361` when that run's `headSha` was `5445f1b` on branch
     `fix/release-core-pin` — a different branch's commit. Also: even the right run used the
     `testing` tip, never the pin, so **dev CI can never be evidence for a pinned combination**.
     Adds: an explicit "the checked-out app revision == `GITHUB_SHA`" assertion exporting
     `APP_COMMIT_FULL`; `.github/scripts/test_check_core_provenance.py` with 8 negative cases
     (AAR from another revision = cache collision / wrong pin rebuild, relabelled provenance,
     version mismatch, binary not containing the declared revision, missing provenance, missing
     AAR, empty expected SHA, no `libbox.so`) — "a gate that has never been seen to fail is not
     evidence"; `build-info.json` gains `app_commit_full` and **measured** `signature_state`.
     CI/TEST/AUDIT.
  5. `72beebc` — runs the Gradle gate **and** the Python gate in both workflows, and states why
     they are not duplicates: Python checks the artifact **before** the build (files + packaged
     `libbox.so`); Gradle re-checks the AAR **AGP actually packaged after `assemble`**. Running
     only one leaves a gap between "the file on disk is right" and "the APK really contains it".
     Also notes `verifyCoreProvenance` **skips** when the AAR is absent, so it cannot be the release
     gate alone. CI/AUDIT.
  6. `fcafdd5` — docs correction of what each gate actually proves. DOC/AUDIT.
  7. `a0e2722` — *P0-E*, "定点移植, 不是合并分支" (a targeted port, not a branch merge). First
     **corrects its own previous round's factual error**: the previous dry-run's `build-info.json`
     claimed `"signature_state": "debug-signed"`, but a release variant with no
     `signing.properties` has **no signing config at all** and `assembleRelease` produces an
     **UNSIGNED** package; there is no "fall back to debug signing" for release. Then:
     **absorbs** the work of `ci/unsigned-dryrun-min-permissions-rebased` (3 commits, see §B2);
     top-level and build job get `contents: read`, only `publish` gets `contents: write`; a
     dry-run path never reads or restores production signing secrets and asserts that explicitly;
     signature state is **measured** with `apksigner verify --verbose --print-certs` and written to
     `build-info.json` + `apksigner-verify.txt`; artifact naming carries the measured state
     (`unsigned` / `debug-signed` / `signed-by-user`). CI/RELEASE/HARDENING.
  8. `c7cc03d` — **fail-open release gate found by static reading.** The `publish` job downloaded
     the artifact and then looked for APKs under `app/build/outputs/apk/release/*.apk` — a
     **different directory** from the build job's. `shopt -s nullglob` made the glob expand to an
     empty array; the `-eq 0` count check was guarding the same wrong path, so the loop body ran
     zero times and the step printed "all 0 APKs verify as signed with a non-debug certificate"
     and **exited 0** — unverified bytes headed for a Release. Reproduced locally with real bash
     semantics + real `apksigner`. Adds `.github/scripts/measure_apk_signature.sh`. CI/FIX.
  9. `722d22b` — the verified bytes and the uploaded bytes were described by **two independent path
     expressions**. Fix: a flat `release-bundle/` (flat on purpose — nesting is where the old bug
     hid), self-checked with `sha256sum --check` at assembly time; new
     `.github/scripts/verify_release_bundle.py`; publish downloads into an isolated directory,
     runs preflight, re-measures signatures on **the same files** with the **same** script, and
     `files:` references only `release-bundle/`; release notes read the preflight-verified
     `build-info.json`. Explicitly: `CN != Android Debug` proves only "not the well-known debug
     subject", and the script reports measured state without ever claiming proven signature
     ownership. 12 negative cases (R01–R12) built as **real bundles on disk** through the **real**
     script. CI/FIX/TEST.
  10. `3c06b53` — first pinned dry-run (`37971211698` on `722d22b8`) failed on the new step:
      (i) `Generate checksums` ran **before** `Rename APKs`, so `SHA256SUMS.txt` described
      nonexistent names and `build-info.json`'s `sha_for` would have written **empty** ABI hashes;
      (ii) `sha256sum ./*.apk` writes `./name` entries, which `sha256sum --check` cannot match in a
      flat bundle. Fix: checksums move into the bundle step **after** renaming, with
      `sed 's|  \./|  |'`; `build-info.json` asserts a non-empty SHA per ABI; the **build** job now
      runs the **same** `verify_release_bundle.py` as publish; `--expect-version/--expect-kind`
      are passed explicitly because the old greedy filename regex swallowed the ABI into a version
      containing `-`. CI/FIX.
  11. `19817c5` — second dry-run (`37972168650` on `3c06b532`) also failed, caught by the **new**
      assertion: `grep: SHA256SUMS.txt: No such file or directory` — `Write build-info.json` ran
      **before** the bundle step that creates the manifest. Fix: **remove the ordering entirely** —
      "copy → hash → describe → prove" become four segments of **one** step, and the two separate
      steps (`Generate checksums`, `Write build-info.json`) are deleted because their precondition
      was itself the bug. Records: "本轮 CI 两次失败都是**验证链在起作用** … 没有任何 artifact
      被上传、没有 Release 被创建." CI/FIX.
  12. `039bf63` — see the service-lifecycle cluster (the invariant that keeps the convergence gate
      leak-free).
  13. `61b807e` — *Round 7 / Gate 3.* The comment said "only the paths preflight verified are
      uploaded"; the code did not — `bundle-files.txt` was printed and grepped, while the upload
      site **enumerated a second time**. Worse, the verifier tolerated files it did not recognise,
      so `fail_on_unmatched_files: true` only checked "listed but missing", never "present but
      unlisted" — an extra `.so` or a keystore would be **silently excluded** from the Release. The
      failure mode is omission, not substitution. Fix: the verifier's step 7 fails on any entry
      outside `publishable`; `--list-to` writes **bare filenames**; publish reads the list into a
      step output, independently re-checks with `diff -u on-disk.txt verified.txt`, and uses
      `files: ${{ steps.verified.outputs.files }}` with `working-directory: release-bundle`, so the
      uploaded set no longer appears a second time anywhere in the YAML. Tests C13–C17, including a
      **positive control** (same fixture with the extra file removed) to prove the failures are
      caused by that file. CI/FIX/TEST.
  14. `58b8309` — for three hours on 2026-10-09, three different app SHAs (`7d4218a`, `8432caf`,
      `61b807e`) failed dev CI **identically** at `Build libbox.aar from …@testing`:
      `build_libbox` wrote `libbox.provenance` but produced **no `libbox.aar`**, so
      `cp libbox.aar` failed and every app step was skipped — an app-only change could get no CI
      verdict at all. (The one success, `039bf631`, only succeeded because its AAR **cache hit**,
      i.e. it never built the core.) Fix: `workflow_dispatch` gains an optional `core_ref`
      (must be 40 lowercase hex) that also enters the cache key; blank = unchanged, still follows
      `CORE_BRANCH` HEAD. Does **not** change the pin or the release path. CI/COMPAT.
  15. `f2cde01` — Android dev CI becomes **manual-dispatch only** (owner's request: self-funded
      Actions minutes; a broken upstream `testing` costs a core build and then skips every app
      step). Revert is one line, quoted in the message; `release-apk.yml` untouched and still
      tag-triggered. CI.
  16. `6442c95`, `f133dc9`, `25ac148` — the pin/ABI trilogy, see below.

- **Problem 2 — the pinned core drifted from the packaged AAR, and one ABI shipped with no engine.**
  1. `6442c95` (2026-10-11) — moves `coreCommit` from `c35faabf4` to `0ff90482b`, the core closeout
     revision carrying the Resume Edge fix this round exists to ship. The old pin was **328 commits
     behind `testing`**. States explicitly that this makes the tree **RED on purpose and that this
     is the safe direction**: `:app:verifyCoreProvenance` still passes because its expectation comes
     from `libbox.provenance` itself (it checks **self-consistency, not the pin** — "this is the gap
     that makes a relabelled AAR invisible to it"), while `check_core_provenance.py` — the pin
     check — **fails**, so the tree cannot produce a mislabelled APK.
  2. `6442c95` also **records, without fixing**, a defect found by inspecting the packaged
     artifacts: `app/libs/libbox.aar` contained exactly **one** `libbox.so`, for **x86_64**, so
     `satelite-one-arm64-v8a-debug.apk` had no `lib/libbox.so` at all — a UI with no engine. Both
     APKs build and both are produced by the then-current CI.
  3. `f133dc9` (2026-10-11) — the pin described an intent with no corresponding AAR, so the tree
     could not produce a shippable APK. `coreCommit` moves to `1965bbf35` (the then-current
     `testing` tip), which the packaged AAR **is** built from. Also records a provenance subtlety:
     the first rebuild used the **abbreviated** SHA, and because `build_shared.ReadCommit` and
     `SING_BOX_BUILD_VERSION` bake the SHA into the binary as a **string** while `release-apk.yml`
     rejects anything that is not 40 lowercase hex, a short SHA is "a provenance record that cannot
     pass the release gate even though the artifact is correct". End-to-end verification is
     enumerated (core SHA, AAR size 118160367 bytes, four ABIs each carrying the SHA, pin, APK) with
     per-ABI SHA-256 values and the statement that the packaged object is **byte-identical** between
     AAR and APK. The arm64 split is fixed by the same rebuild (now carries a 76.5 MB `libbox.so`).
  4. `25ac148` (2026-10-12) — **the root cause was a missing gate, not a missing AAR.** No release
     gate ever **opened** an APK: `verify_release_bundle.py` checked filenames, whole-file
     checksums and metadata; `check_core_provenance.py` asked "does **ANY** packaged `libbox.so`
     contain the pinned revision?", which a single-ABI AAR answers **YES** to; `measure_apk_signature.sh`
     only runs `apksigner`. So an arm64-v8a split with no `lib/arm64-v8a/libbox.so` — or holding
     another ABI's engine — assembles, installs, passes every gate and ships with no core; and
     because the earlier fix was a rebuilt AAR rather than a gate, the class could return silently.
     Fix: new `_zipcontent.py` (one shared ABI↔engine primitive so the gates and the workflow cannot
     disagree), `verify_release_bundle.py` requires **each APK to contain its own ABI's engine**
     (keyed on the ABI in its filename), digest equality against a `core_engine_sha256` map, and no
     smuggling a second ABI's engine; `--aar` compares the map against the real AAR and
     `--require-aar` makes the strongest check impossible to drop by a workflow edit;
     `check_core_provenance.py` requires `jni/<abi>/libbox.so` for **every** shipping ABI;
     `release-apk.yml` records the per-ABI digests while the AAR is still on disk (the publish job
     never receives the AAR). New `test_release_abi_guard.py` with N1–N10 including two positive
     controls ("what stops 'reject everything' from passing"). RELEASE/CI/FIX/HARDENING/TEST.

- **Rejected / superseded attempts:**
  - Deriving the core commit from the checkout at APK-build time — **rejected by `89951ae`**.
  - Degrading a provenance mismatch to a **warning** — **rejected by `89951ae`** (`fail`).
  - `git ls-remote` as a pin-existence check — **rejected by `38074fa`** (only lists ref tips).
  - Assuming "no secrets ⇒ debug-signed" — **rejected by `a0e2722`** (release is UNSIGNED).
  - Inferring signature state rather than measuring it — **rejected by `a0e2722`**.
  - Scanning a nonexistent directory and calling it verification — **rejected by `c7cc03d`**.
  - Two path expressions for "verified" and "uploaded" — **rejected by `722d22b`**, then the
    second enumeration that survived — **rejected by `61b807e`**.
  - Multiple steps whose ordering is a contract — **rejected by `19817c5`**.
  - A greedy filename regex to recover version/kind — **rejected by `3c06b53`**.
  - Dev CI as evidence for a pinned combination — **rejected by `f6a91df`**.
  - Dev CI on every push — **rejected by `f2cde01`** (cost).
  - Adopting `fix/release-core-pin`'s pin change — **rejected by `f6a91df`** ("那属于另一个 agent
    的提交, 按指令不得静默采纳 testing 尖端"). The orphan commit is `5445f1b` (§B2/§B6).
  - Pinning to a revision with no built AAR — **rejected by `f133dc9`**.
  - Short SHA as a provenance record — **rejected by `f133dc9`**.
  - "Any packaged `libbox.so`" as an ABI guard — **rejected by `25ac148`**.

- **Final rule.** `version.properties` (`versionCode=16`, `versionName=0.5.10`,
  `coreCommit=1965bbf351710b86722838ae61a91b62c97d29de`); `app/build.gradle.kts:91-92`
  (provenance file wins over `CORE_COMMIT_FULL` and any local checkout), `:246-375`
  (`verifyCoreProvenance`, skips when the AAR is absent at `:259`), `:196` `splits`;
  `.github/scripts/check_core_provenance.py` (133 lines), `verify_release_bundle.py` (399),
  `_zipcontent.py` (107), `measure_apk_signature.sh` (135),
  `test_check_core_provenance.py` (119), `test_release_publish_gate.py` (767),
  `test_release_abi_guard.py` (290); `.github/workflows/release-apk.yml` (tag-triggered,
  `contents: read` everywhere except the `publish` job), `.github/workflows/android-ci.yml`
  (manual dispatch, optional `core_ref`, `CORE_BRANCH: testing`).
- **Locking evidence.** The three Python negative-test suites run **inside** `android-ci.yml`
  (`test_check_core_provenance.py`, `test_release_publish_gate.py`) plus the Gradle
  `:app:verifyCoreProvenance` step in the same workflow.
- **Tags:** CI, RELEASE, BUILD, FIX, HARDENING, AUDIT, TEST | OWN, CORE-COORDINATED |
  VERIFIED_GIT. **Caveats:** every claim about the *core* repository (its `box.go` condition, its
  `daemon/instance.go`, the existence of `1965bbf35` / `2fd57543a`) is **UNVERIFIED** here — no
  core object exists in this clone (`git cat-file -t 1965bbf351…` → *could not get object info*).

---

### Cluster: Network monitor — listener ownership and resolution epoch

- **Problem.** Two independent defects on the "which interface is the default?" path:
  (a) the listener registration was keyed by a **process-level singleton**, so a stale teardown
  dropped a live successor's registration; (b) the retry loop's `epoch` was **advisory** (each
  writer captured and compared its own value), the "no network" branch returned **before** bumping
  it, and the whole resolve path ran on the **main thread** with up to nine 100 ms `Thread.sleep`
  retries.

- **Evolution (ordered):**
  1. `5d9f4f2` (2026-10-09) — `DefaultNetworkMonitor.start()/stop()` gain an **owner** parameter;
     the `DefaultNetworkListener` key becomes the owner (`BoxService` instance) instead of the
     singleton. Rationale in the diff: "With the singleton as the key the registration map held
     exactly one entry for the whole process, so a destroyed Service instance that ran its teardown
     after a successor had already started would remove that single entry and silently kill the
     successor's network tracking (`DefaultNetworkListener` unregisters only when its map empties)."
     FIX/HARDENING.
  2. `ed014c6` (**round 6, P1**) — **corrects an assumption from round 5**: "**不需要给内核接口加
     token**. libbox 的 `closeDefaultInterfaceMonitor(listener)` 本来就**把 listener 对象传进来
     了**, 那个对象的身份就是所有权证明. 第五轮把它当成'只能清空', 是因为 Android 侧把它存进了一个
     进程级可变字段." Adds `InterfaceListenerRegistry` (pure Kotlin, no Android types), identity
     based with explicit `===` (not `==`: gomobile-generated implementations do not override
     `equals` today, and the guarantee is kept **in the code**, not in an assumption about a
     generated class). `setListener(listener)/setListener(null)` → `addListener/removeListener`;
     dispatch takes a snapshot first and calls **outside** the lock (the event eventually reaches
     native; holding a Java monitor across a native call is the classic re-entrant-callback
     deadlock); `defaultNetwork` becomes `private set`. Also fixes a performance defect in the same
     place: `checkDefaultInterfaceUpdate` used `for (times in 0 until 10)` with a possible
     `updateDefaultInterface` **each round** — up to 10 JNI calls and 10 kernel-side interface
     writes per network change; now it returns on success and retries only while the interface
     genuinely cannot be found. Backed by a **counter-proof**: in an isolated worktree at round-5
     HEAD (`2171167b`) the removal side was reverted to the `setListener(null)` equivalent and the
     same test set went **6 red** (N01/N02/N03/N04/N09 + the race case). Adds
     `InterfaceListenerRegistryTest.kt` (322 lines, 11 cases). FIX/PERF/HARDENING.
  3. `8432caf` (**Round 7 / Gate 2**) — three gaps: (1) `mainHandler` is passed to
     `registerBestMatchingNetworkCallback` / `requestNetwork` / `registerDefaultNetworkCallback`
     (`DefaultNetworkListener.kt:141/148/158/164`), so the whole path **including up to nine 100 ms
     `Thread.sleep` retries** ran on the main looper — roughly a one-second main-thread block for a
     background bookkeeping job; (2) the lost-network branch returned before `++epoch`, so a loop
     still running for the previous network saw an unchanged epoch and announced an interface the
     device had already left; (3) the epoch was advisory. Fix: new `InterfaceResolutionEpoch`
     (pure Kotlin, fully synchronous, no dispatcher, no Android type); `beginAttempt()` is called
     by **every** event that changes the answer **including** a lost network and an explicit null;
     `isWinner(token)` is the **single** authority on whether a result may be published;
     `checkDefaultInterfaceUpdate` no longer blocks its caller — it snapshots listeners, claims the
     epoch, and launches on an injectable `resolveDispatcher` (production `Dispatchers.IO`);
     ownership is validated **before each retry** and **before publishing** (the second is the
     critical one: the lookup takes time and the device may have moved during it); `Thread.sleep`
     → `delay`. Adds `InterfaceResolutionEpochTest.kt` (249 lines). Two honest notes in the
     message: the original draft asserted "any instant has exactly one winner", which is **wrong**
     (`isWinner` is a point-in-time question; a thread can legitimately see true and lose it a
     microsecond later), so the test was changed to the property that actually holds; and N10
     asserts at the **bytecode** level that `DefaultNetworkMonitor`'s constant pool contains no
     `java/lang/Thread`, because a device test cannot distinguish "the main thread was busy for a
     second" from bad luck — with the discrimination checked (the class does not contain the
     reference; same-package `SingBoxCore.class` does). FIX/PERF/HARDENING.
  4. `1efaa8f` (P1-E, test-only) — pins that the four network files contain **zero** occurrences of
     the wake/screen facts, that `onUserPresent()` is the only wake producer, and that screen-fact
     handlers contain no `ResetNetwork` / `restartService` / `closeService(` / `rebuildVpn` /
     `forceReconnect`. The message also records three self-inflicted test bugs, one of which is a
     genuinely instructive one: the E02 filter excluded lines containing `sink.reportDeviceWake`,
     but the real enqueue line **is** `enqueue(live) { sink -> sink.reportDeviceWake() }`, so the
     result was empty and the assertion failed with "found 0 producers" — "**是测试抓出了自己的
     过滤 bug**, 不是产品问题. 教训: 排除'声明', 不要排除'调用形状'." TEST/AUDIT.

- **Rejected / superseded attempts:**
  - Singleton as the listener key — **rejected by `5d9f4f2`**.
  - "Set the listener to null" as a removal API — **rejected by `ed014c6`** (with a 6-red counter-proof).
  - `==` for listener identity — **rejected by `ed014c6`** (`===`).
  - Holding the monitor across dispatch — **rejected by `ed014c6`**.
  - Up to 10 interface writes per network change — **rejected by `ed014c6`**.
  - The lost-network branch not bumping the epoch — **rejected by `8432caf`**.
  - An advisory epoch — **rejected by `8432caf`**.
  - `Thread.sleep` on the main looper — **rejected by `8432caf`**, locked by a bytecode assertion.
  - Adding a token to the kernel interface — **explicitly unnecessary** per `ed014c6` (the listener
    object already is the proof).

- **Final rule.**
  - `bg/InterfaceResolutionEpoch.kt:34` class; `:45` `beginAttempt()` (`++current`), `:53`
    `isWinner(token)` = `token == current`, `:56` `currentAttempt`.
  - `bg/InterfaceListenerRegistry.kt:39` class; `:46-48` `add` (identity, idempotent),
    `:58-60` `remove` (removes **only** the passed object, `removeAll { it === listener }`),
    `:66` `contains`.
  - `bg/DefaultNetworkListener.kt:29` object; `:49` `listeners: MutableMap<Any, (Network?) -> Unit>`
    keyed by owner; `:55-57` register-on-first-listener + immediate replay of the known network;
    `:69-76` `Stop` unregisters only when the map **empties** (which is exactly why keying matters);
    `:99` `start(key, listener)`, `:114` `stop(key)`; `:141` `mainHandler` retained (the *callback*
    is still main-looper; only the resolve work moved off it).
  - `bg/DefaultNetworkMonitor` — `start(owner)` / `stop(owner)`; `defaultNetwork` `private set`.
- **Locking evidence.** `bg/InterfaceListenerRegistryTest.kt` (added `ed014c6`, untouched since),
  `bg/InterfaceResolutionEpochTest.kt` (added `8432caf`, untouched since),
  `bg/ScreenFactOrderingTest.kt:184+` (the P1-E block from `1efaa8f`).
- **Tags:** FIX, PERF, HARDENING, NETWORK, LIFECYCLE, TEST | OWN, ADAPTED (the file itself is a
  sing-box-for-android port) | VERIFIED_GIT.

---

### Cluster: Docs-only evidence series (rounds 7–8)

- **Problem.** Several rounds produced *claims* whose evidence was not in the repository: which CI
  run belongs to which SHA, what the candidate APK actually did on an AVD, which items of a
  previous report are still true. The project's answer was a long, deliberately non-authoritative
  documentation series plus a set of *self-correcting* docs commits.

- **Evolution.** Round-8 documentation commits in order:
  `ffbd6aa` (parity report A–G) → `50140a8` (screen-off device evidence + Doze blocker) →
  `c969126` (align the verdict with the evidence actually collected) → `fa5fb04` (P1-E evidence,
  candidate build, why D-0b cannot be measured here) → `cae13dd` (point the report at candidate
  builds on the exact final SHA) → `59b03f1` (candidate/baseline identity chain side by side) →
  `2b206cd` (Gate 4-B candidate regression on the same AVD, E-5c) → `017fff7` (T01–T12 matrix
  filled with what the candidate APK actually did) → `dc80505` (isolate the CI failure to upstream
  testing, E-6) → `bffb8ad` (T08 becomes PASS on real traffic evidence, plus a recorded measurement
  trap) → `54a3eb0` (reconcile the round-7 report with round-8 evidence, item by item) →
  `e0284cc` (verified CI run + device regression + round-8 report) → `6ea83f8` (state the tested
  product SHA and **how to prove the last commit is docs-only**) → `e1ec9f8` (finalize round-8
  evidence and references).
  Then `6864a1e` (establish the stage-1 knowledge baseline) and `942b6fe` (clarify the knowledge
  delta and release evidence), which create `KNOWLEDGE/**` — **not read by me**.

- **Verification of the "docs-only" claim.** I checked every one of these by name-only diff:
  - `ffbd6aa`, `50140a8`, `c969126`, `fa5fb04`, `cae13dd`, `59b03f1`, `2b206cd`, `017fff7`,
    `dc80505`, `bffb8ad`, `54a3eb0`, `e0284cc`, `6ea83f8`, `e1ec9f8` — **each touches exactly 1–2
    files, all of them `docs/**`**. VERIFIED_GIT. Four of them (`1efaa8f`, `15c432d`, `ae89388`,
    `32cc6cc`) are labelled `test(android)` and touch only `app/src/test/**`; they are **not**
    docs-only and I have assigned them to their functional clusters.
  - `e1ec9f8` touches 2 files (`docs/**`); `e0284cc` touches 2 (`docs/**`).
  - `docs/` at HEAD contains exactly `ROUND8-REPORT.md` and
    `android-screen-power-parity-final-report.md`; the first appearance of `docs/` in history is
    `ffbd6aa`.
  - The two reports are explicit about their own scope limits. `docs/ROUND8-REPORT.md` header
    records: opening SHA `bffb8ad`, product-code-freeze SHA `ae89388`, pinned core `c35faabf4`,
    version `versionCode=16 / versionName=0.5.10` **unchanged**, and the verdict string
    `ANDROID_CLIENT_CODE_CLOSED + PINNED_CI_VERIFIED + EMULATOR_REGRESSION_VERIFIED (仅已测项) +
    REAL_DEVICE_PENDING + RELEASE_NOT_TRIGGERED`, with an explicit list of what is **not** claimed
    (`READY_FOR_RELEASE` / `REAL_DEVICE_PASS` / `FULL_APPLE_PARITY`). It also records that its own
    first CI run on the final code failed (`run 38005659283`, `ScreenSeedWiringTest.S08`) and that
    this was a flaky **test** assertion, fixed by `ae89388`, with `run 38006795028` green — "这条
    记录保留在此, 因为'CI 有一次红'与'CI 一直绿'是两件事."
  - `docs/android-screen-power-parity-final-report.md` header names its start SHAs
    (`58b8309` for round 7, `bffb8ad` for round 8), records that the remote had **only `main`**,
    and states that a local `baseline-r5` worktree was a detached comparison that was never pushed.

- **Rejected / superseded attempts (documentation-level):** `c969126` and `bffb8ad` each
  **correct a previous entry of the same report**; `dc80505` attributes a CI failure to upstream
  rather than to the app; `6ea83f8` refuses to embed its own SHA in its own text and instead gives
  the *command* to produce it (because a report that contains its own final SHA needs one more
  commit). The pattern is consistent: supersede by adding a labelled correction rather than by
  rewriting history.

- **Final rule.** `docs/ROUND8-REPORT.md` + `docs/android-screen-power-parity-final-report.md`
  are the only in-repo evidence reports; both are self-describing about their scope tags.
- **Locking evidence.** The reports themselves cite CI run ids and SHA-level `git diff --name-only`
  commands; **none of that is verifiable from Git alone** (no CI artifacts, no run metadata in the
  repository). See §B6.
- **Tags:** DOC, AUDIT, TEST | OWN | VERIFIED_GIT (for "these commits are docs-only"),
  UNVERIFIED (for the CI/device evidence they cite).

---

### Cluster: Repository hygiene, brand and naming

- **Evolution.**
  - `cd9153d` (2026-09-14, `Ryan Yan`) — `Create FUNDING.yml`. Sole contribution from that author
    besides the root LICENSE. REPOSITORY-HYGIENE.
  - `2171167` (2026-10-10) — ignores `local-stub-aar/`: a machine without the pinned `libbox.aar`
    cannot compile `:app:compileDebugKotlin` because the source imports `io.nekohasekai.libbox.*`,
    so a **compile-only stub AAR** (all natives throw) was created locally. It was not ignored, so
    one `git add -A` could commit an AAR with **no `libbox.so`**, which "would make the provenance
    gate's warnings completely meaningless and could let a later build treat a fake core as real".
    Also adds `__pycache__/` for the new importable `_zipcontent` module. Verifies the pre-existing
    ignore behaviour of `app/libs/libbox.aar` / `libbox.provenance` / `local.properties` with
    `git check-ignore -v`. States "本轮没有改动任何产品代码、测试或 CI 逻辑" — **confirmed: the
    diff is `.gitignore` only.** REPOSITORY-HYGIENE.
  - `21c7f68` (2026-09-21) — renames the CI APK prefix `interstellar-` → `satelite-one-` in
    `build.gradle.kts` `outputFileName` and in the workflow's rename glob/artifact name;
    **`applicationId` and signing secrets deliberately unchanged**. RELEASE/BUILD.
  - `22d9ab4` (2026-10-08) — "品牌收口为 Jiejiebox + UI 功能一致性审计". Public brand becomes
    Jiejiebox (see `README.md:6` `# Jiejiebox`, `assets/android/ic_launcher-web.png`).
  - **Naming is now internally inconsistent, and no commit reconciles it.** At HEAD:
    repository/APK = `satelite-one`, README title/public brand = **Jiejiebox**, Kotlin package and
    `applicationId` = **`com.interstellar.proxy`**, and `core/ProxyCore.kt:18` still reads
    "**Jiejiebox's** own UI process is in the foreground". Four names for one product; the
    "收口" commit changed the public brand only. INFERRED (from the absence of any reconciling
    commit) + VERIFIED_GIT (each individual string).
  - `fa5fb04`…`e1ec9f8` documentation series — see the previous cluster.
  - Deleting `1a0b506`'s subject matter left two orphans that were cleaned up by `2f0bcbf`
    (`org.json` test dependency) and `ad91f44` (`PrefToggleRow`); this "delete a subsystem, then
    sweep the orphans" pattern recurs (`d628ef0` after `d2a2e98`; `ad91f44` after `ebd2f79`).
- **Tags:** REPOSITORY-HYGIENE, BUILD, RELEASE, DOC | OWN | VERIFIED_GIT.

---

## B2. Supersession / revert / absorption pairs

**Explicit reverts (a later commit undoes an earlier one).**

1. **`fb5a361` REVERTS the `clash_api` half of `919089d`.** `919089d` added an empty
   `"clash_api": {}` to the generated config on the stated premise that
   `trafficcontrol.Manager` is created only when `experimental.clash_api != nil`. `fb5a361`
   verifies the premise is **false** for libbox (`daemon/instance.go`'s
   `StartedService.newInstance()` sets `PlatformLogWriter: s` unconditionally, so the manager is
   always created) and removes it. It **keeps** `919089d`'s `TrafficAvailable` / `TrafficDisplay`
   semantics, re-described as "respecting the kernel's authoritative report". The generator now
   *forbids* `clash_api` (`MinimalConfigBuilder.kt:79-86`, test at `:168-171`).
   → Topic: NETWORK? no — **FIX/UI/CORE-INTEGRATION**; provenance **SUPERSEDED**.

2. **`481e370` SUPERSEDES part of `7275c0f`.** `7275c0f` introduced the version guard counting
   level **transitions** and (in its first attempt) only locked the two writers; `481e370` proves
   both halves wrong — the key was sampled **after** the read, and a transition counter cannot be
   invalidated by a same-value observation — and replaces them with an **observation** epoch taken
   **before** the read. `7275c0f`'s `InstallGuard`, `ScreenFactState` split and debug line survive.

3. **`7d4218a` SUPERSEDES the `generation`-based publish gate of `51cb7f5`/`5d9f4f2`/`74ff3ce`.**
   `7d4218a` removes the second counter entirely (`CoreLifecycle.startAttempts` is now the only
   counter; `stopStaleUpTo` states the teardown rule separately). `5d9f4f2`'s "`publish()` only has
   to check the generation" reasoning is thereby retired — the final `ownsServiceLocked` checks
   **three** conditions (`BoxService.kt:1012-1015`).

4. **`bdb4ced` SUPERSEDES parts of `5d9f4f2` and `74ff3ce`.** `5d9f4f2`'s `stop()`-as-`invalidate()`
   plus `pendingRestart` restart path is **deleted**; `74ff3ce`'s watermark is kept but its scope
   is widened from "the `Stopped` write" to **every** convergence side effect, and the token claim
   moves from `startCore()` (IO) to `onStartCommand()` (main).

5. **`24c8372` SUPERSEDES parts of `e8bd186`.** `e8bd186` introduced `NotificationPublishGate` with
   `open()`/`close()`; `24c8372` (i) replaces the shared handler with a per-client
   `GenerationHandler(token)`, (ii) replaces unconditional `open()` on every `show()` with
   `beginOrRefreshSession()` (so a text refresh does not mint a generation), and (iii) makes the
   token-less native traffic path a separate, generation-resolved entry point.

6. **`7b6aeb3` SUPERSEDES part of `7416c73`.** `7416c73`'s `LogUiPublishGate` marked "the current
   newest version" and added a duplicate `appendVersion` `AtomicLong`; `7b6aeb3` makes the ring's
   version the single authority, requires `markPublished(version, …)` to name the **snapshot's**
   version, and deletes the duplicate counter. It also replaces `LaunchedEffect(logs.size)` with a
   version key (the size stops changing once the 3000-slot ring is full).

7. **`fb606bf` SUPERSEDES part of `7416c73`.** `7416c73`'s "follow only when already at the bottom"
   left the first entry of the logs page parked on the **oldest** line; `fb606bf` positions at the
   newest on entry and keeps the "follow only at the bottom" rule afterwards.

8. **`ed014c6` SUPERSEDES part of `5d9f4f2`.** `5d9f4f2` changed the `DefaultNetworkListener` key
   from the singleton to the owner; `ed014c6` states round 5 treated `closeDefaultInterfaceMonitor`
   as "can only clear", whereas libbox already passes the listener object and therefore **is** the
   ownership proof, and replaces the whole set/clear API with an identity registry.

9. **`d628ef0`, `ad91f44`, `2f0bcbf` SUPERSEDE leftovers of `d2a2e98`, `ebd2f79`, `1a0b506`.**
   Each is a *post-removal sweep*: `d628ef0` deletes `Yaml.kt` and stale references after
   `d2a2e98`; `ad91f44` deletes `PrefToggleRow` and misleading section labels after `ebd2f79`;
   `2f0bcbf` deletes the `org.json` test dependency after `1a0b506`.

**Absorption (a later commit folds in work that exists elsewhere).**

10. **`a0e2722` ABSORBS `ci/unsigned-dryrun-min-permissions-rebased` (3 commits).** Message:
    "吸收自 `ci/unsigned-dryrun-min-permissions-rebased`(3 个提交, 不落后于内核实务, 且**未改
    pin/版本**), 并做了三方合并". Both rebases of that branch are still present as objects and are
    **not** ancestors of `main`:
    - variant on base `fb5a361`: `be0c7b7` ("ci(android): keep the dry run unsigned and read-only")
      → `739cfb7` ("docs(android): state the real signing behaviour of each variant") →
      `e0fe962` ("ci(android): keep the release step shell portable and the artifact name clean");
      `739cfb7` is `fb5a361`'s direct child, and `e0fe962` is a **dangling commit**.
    - variant rebased onto `4d15e3b`: `547d363` → `b6ea2eb5` (`2334bad` is a cherry-pick of
      `e0fe962` on top, carrying "(cherry picked from commit e0fe9628…)" in its body).
    `git patch-id` says the **same-titled** commits across the two variants are **NOT**
    patch-equivalent (`be0c7b7` ≠ `547d363`, `739cfb7` ≠ `b6ea2eb5`) — they are genuinely different
    patches on different bases. `e0fe962` is a dangling commit. `a0e2722` is a **single** commit on
    `main` representing all three.

11. **`f6a91df` REFUSES to absorb `fix/release-core-pin`.** The orphan tip is
    `5445f1b80c923aa16ba348aaa531f43ee10a5fa0`, title "release: pin the core to the frozen v0.1.6
    revision", author `agent <agent@local>`, parent `fb5a361`, diff = **one line**:
    `coreCommit=c35faabf402a4da93b8c31cdfad941b8b1528ffc` → `coreCommit=2fd57543a0a3580d0733705608e49f0fb0420ce2`.
    `f6a91df` records: "不存在 `fix/release-core-pin` 分支上那个 `2fd57543` 的改动 —— 那属于另一个
    agent 的提交, 按指令不得静默采纳 testing 尖端." **`5445f1b` is a dangling commit and is not an
    ancestor of `HEAD`** (`merge-base --is-ancestor` exit 1). It is the single clearest
    "rejected attempt" in the repository.

**Patch-equivalent duplicates (topic branch → cherry-pick onto an integration branch).**

12. Three pairs, each proven equivalent with `git patch-id --stable`:

    | Original (deleted branch tip) | Cherry-pick on `main` | patch-id | parents | trees |
    |---|---|---|---|---|
    | `47911014350246e7d7362f11f3ab43bee1e5283b` | `25ac1482f4ee89a2e57f91523677fa382ef92cbf` | **equal** (`08010b21…`) | both `56eb255` | **identical** |
    | `e413c5948b1393cd6d93458b8e41697127f227e2` | `d10c9a56cd8d85a8df376fbf53220fdc7a774a5c` | **equal** (`70fb43f5…`) | `56eb255` / `25ac148` | different (base differs) |
    | `baa9958e8cdaebeffd5767c8be1e8d42fa8cb364` | `15b5ff178b3dc65463f39a239b1829f6da112767` | **equal** (`744d96f1…`) | `56eb255` / `d10c9a5` | different (base differs) |

    The reflog records the mechanism exactly: reflog entries
    `HEAD@{2026-10-12 04:30:29} cherry-pick: release(android): make an APK without its own ABI's
    engine fail the release gate`, then `…the startup establish needs the UI client verdict too`,
    then `…a reentrant foreground record must not strand the next notification`, followed by
    `checkout: moving from main to jiejiebox/v0.1.6-final-guard-integration` and
    `merge jiejiebox/v0.1.6-final-guard-integration: Fast-forward`. For the other two branches the
    originals were authored on their own branch (`HEAD@{…} commit: <same subject>`) and then
    cherry-picked.

**Silent deletions of recently-built work (no revert commit; the removal commit is the record).**

13. **`ebd2f79` deletes `DnsOverridesPage.kt` one commit after `8d8bb9f` polished it (+46 lines).**
    Verified: the file exists at `22d9ab4` and `8d8bb9f`, and is **absent** at `ebd2f79`.
14. **`d2a2e98` deletes `RawConfigApplier.applyClash/applyXray` and the whole sidecar tree**, of
    which `ac94df1` (v0.5.8, two weeks earlier) had just added 428 + 204 lines.
15. **`ebd2f79` deletes `assets/rules/*.srs` and `GeoRuleUpdater`**, which `d536cef` (v0.5.5) and
    `ac94df1` (v0.5.8) had just built the online-update path for.
16. **`1a0b506` deletes `SmartSwitchEngine.kt` (375 lines) and `DirectPing.kt` (133 lines)**, both
    added inside the previous three weeks (`b5a17ce` smart mode v1; `c68fa18` DirectPing).

---

## B3. Release boundaries & tag inventory

`git describe --tags --always <sha>` is quoted for every key SHA below.

| Tag | ref type | Tag → commit | Subject at tag | Date | Commits since previous tag | Version commit | Distance version→tag |
|---|---|---|---|---|---|---|---|
| `0.2.0` | **lightweight** | `db3365638e96b8d41c0f631ee77f57303d78089d` | 星际穿越 v0.2.0: Android sing-box 客户端初始提交 | 2026-08-27 | 2 (whole history) | `db33656` | **0** |
| `v0.3.0` | **lightweight** | `ff85db8ecc43729eedc4b9c3c2c5d518c7d335d7` | CI 修复: gradlew 补上 git 可执行位 (100644 → 100755) | 2026-09-12 | 32 | `270bf20` | **29** |
| `v0.4.0` | **annotated** (`tag` object `263fe37e…`, tagger zn0wii, msg "星际穿越 v0.4.0: 三内核切换(mihomo sidecar)") | `5cab12f528143c25ae56a3d1db5fb2751ec309de` | 首页仪表卡布局优化: InstrumentCard 双槽位 | 2026-09-12 | 21 | `e75d6eb` | **1** |
| `v0.4.1` | **annotated** (`tag` object `2fb0e90f…`, msg "星际穿越 v0.4.1") | `534968955dce02129862311cdab367e4922f4a7e` | v0.4.1: 分流信息架构… | 2026-09-12 | 11 | `5349689` | **0** |
| `v0.5.0` | lightweight | `0607e8281e4a3c7e94eddc442ff6475ef8396166` | v0.5.0: Xray 内核接入(三内核完成) | 2026-09-13 | 7 | `0607e82` | 0 |
| `v0.5.1` | lightweight | `f316c302759a39f9530ebe84969967c828c79442` | v0.5.1: 智能模式 + 测速/导入/分应用修复系列 | 2026-09-13 | 22 | `f316c30` | 0 |
| `v0.5.2` | lightweight | `1ba3b4a83e7214777ae5d6262a2077eba8b13a06` | v0.5.2: 智能行为日志… | 2026-09-13 | 3 | `1ba3b4a` | 0 |
| `v0.5.3` | lightweight | `9b781553c0bd184eb00feae06275ee81d1417e06` | CI: Release 正文改为自动更新摘要 | 2026-09-13 | 2 | `c68fa18` | **1** |
| `v0.5.4` | lightweight | `439523ac71d0817454833ad41369a52c6b3836c8` | v0.5.4: 三内核直连模式修复… | 2026-09-14 | 2 | `439523a` | 0 |
| `v0.5.5` | lightweight | `d536cefb6a54281fb06f0616dbc583de91a770e7` | v0.5.5: 海外网站走代理开关… | 2026-09-20 | 3 | `d536cef` | 0 |
| `v0.5.6` | lightweight | `c104c3ece4a05c1de7a99513557c3a3c275445c6` | v0.5.6: 修分应用代理读不到无图标系统组件 | 2026-09-20 | 1 | `c104c3e` | 0 |
| `v0.5.7` | lightweight | `b56111823ca4917d1f7c0f60766799decf0b193a` | v0.5.7: 应用内检查更新 | 2026-09-20 | 1 | `b561118` | 0 |
| `v0.5.8` | lightweight | `ac94df18bdfa31b9ba2b961ec1118f9d969a3ce6` | v0.5.8: 原始订阅配置直通内核 + 节点页分组重构 | 2026-09-20 | 1 | `ac94df1` | 0 |
| `v0.5.9` | lightweight | `2db9aebf3846f21fa1a0fe99814b9b701a6b8930` | v0.5.9: 修停止代理后通知栏残留'已连接' + 连接详情面板 | 2026-09-21 | 1 | `2db9aeb` | 0 |
| `v0.5.10` | lightweight | `6f4124c4cfab38fdade0c92c1c412a4ef4a7309e` | v0.5.10: 多语言支持(中文/English) | 2026-09-25 | 2 | `6f4124c` | 0 |

**Tag semantics verified against `version.properties`.** `versionCode` / `versionName` at every
tag: `0.2.0` has **no `version.properties`** (the file first appears at `82f0763`, 2026-09-12; at
`0.2.0` the version lived in `app/build.gradle.kts` as `versionCode = 2` / `versionName = "0.2.0"`).
From `v0.3.0` onward the tag commit's `versionName` equals the tag (`3/0.3.0`, `4/0.4.0`,
`5/0.4.1`, `6/0.5.0`, `7/0.5.1`, `8/0.5.2`, `9/0.5.3`, `10/0.5.4`, `11/0.5.5`, `12/0.5.6`,
`13/0.5.7`, `14/0.5.8`, `15/0.5.9`, `16/0.5.10`) — **VERIFIED, monotonically increasing, no
collisions.**

**Three tags are NOT on their version-bump commit** — `v0.3.0` (+29 commits later, a CI fix),
`v0.4.0` (+1), `v0.5.3` (+1). This is exactly the phenomenon `d2719ab` fixed in CI
("tag 落点可能在版本提交之后"), and it is real in this history, not hypothetical.

**Release boundaries (what falls where).**
- `0.2.0` … `v0.5.10` = 111 commits = **the multi-core era**. Feature content per tag is in the
  tag table above; the whole era ends at `6f4124c`.
- `v0.5.10..HEAD` = **88 commits**, none tagged. `git describe HEAD` = `v0.5.10-88-g942b6fe`.
  This is the entire single-core + facts + lifecycle + provenance era. **The repository has
  released nothing since the architecture was replaced.** The round-8 report confirms this
  explicitly: `RELEASE_NOT_TRIGGERED`.
- `version.properties` is now the single version authority (its own header comment says so:
  "全局唯一的版本定义处"); it has been touched by 15 commits (the 14 version bumps + `89951ae`'s
  `coreCommit` introduction, plus `a3d2321`, `6442c95`, `f133dc9` re-pins).
- `coreCommit` history: introduced by `89951ae` = `a8fdc36caeb006a8f7b9a19aa6c4750dddb52e99`
  → `a3d2321` = `c35faabf402a4da93b8c31cdfad941b8b1528ffc` → `6442c95` =
  `0ff90482b90f82e92af605a073963e44f8751c00` → `f133dc9` =
  `1965bbf351710b86722838ae61a91b62c97d29de` (**current**). The rejected alternative
  (`5445f1b`, dangling) would have been `2fd57543a0a3580d0733705608e49f0fb0420ce2`.

**`describe` for the key audited SHAs:**
`d2a2e98` → `v0.5.10-1-gd2a2e98`; `89951ae` → `v0.5.10-2-g89951ae`;
`a3d2321` → `v0.5.10-17-ga3d2321`; `51cb7f5` → `v0.5.10-29-g51cb7f5`;
`3c65418` → `v0.5.10-77-g3c65418`; `f133dc9` → `v0.5.10-81-gf133dc9`;
`25ac148` → `v0.5.10-83-g25ac148`; `2cb6eb9` → `v0.5.10-86-g2cb6eb9`;
`942b6fe` (HEAD) → `v0.5.10-88-g942b6fe`.

---

## B4. Fork-owned vs upstream-inherited boundary

**There is no upstream Git lineage in this repository.** `git remote -v` shows exactly one remote,
`origin = https://github.com/Piggy-Cat-bit-shadow/satelite-one.git`; `git log --merges` is empty;
`git log --graph` shows a single linear chain `3b71924 → db33656 → … → 942b6fe`; there is no
second remote, no plugin submodule, no `160000` gitlink, no `.gitmodules`. **INFERRED**:
"fork-owned history begins at the root commit", because there is nothing before it to inherit from.

**The root commit is an empty-shell initialization, not an import:**
- `3b71924` ("Initial commit", `Ryan Yan <lightlfyan@gmail.com>`, 2026-08-27 07:48:13 +0800)
  contains **`LICENSE` only** — 21 lines, adding 21 insertions.
- `db33656` ("星际穿越 v0.2.0: Android sing-box 客户端初始提交", parent `3b71924`, 15 minutes
  later) is the first real content: **139 files, 11480 insertions**.
- `LICENSE` (at HEAD, unchanged since `3b71924`) is **MIT**, "Copyright (c) 2026 Ryan Yan".
  Neither `db33656` nor any later commit touched it.

**Upstream-inherited *code* — exactly one file.** `git grep -l -i "sing-box-for-android"` over
`app/src` at HEAD matches **only**
`app/src/main/java/com/interstellar/proxy/bg/DefaultNetworkListener.kt`, and the same grep for
`"Max Lv"` matches the same single file. Its header (`:1-8`) reads:

```
Copyright (C) 2019 by Max Lv <max.c.lv@gmail.com>
Copyright (C) 2019 by Mygod Studio <contact-shadowsocks-android@mygod.be>
Ported from sing-box-for-android (GPLv3) for interstellar-android.
```

That is the whole of the "UPSTREAM-INHERITED" surface. Note the provenance anomaly: the *file*
says **GPLv3** while the repository `LICENSE` says **MIT**, and no commit in this history
reconciles them. Flagged in §B6.

**Vendored-but-not-in-Git: the engine.** The single engine is **not** vendored as source or as an
object here:
- `app/src/main/jniLibs/` — `.gitignore`d ("sidecar cores (mihomo/xray/hev) — fetched via
  `tools/fetch_cores.py`"). Nothing in it at HEAD.
- `app/libs/libbox.aar` — `.gitignore`d, described by the ignore comment as "112MB sing-box core —
  rebuilt from Piggy-Cat-bit-shadow/sing-box (see README)".
- `app/libs/libbox.provenance` — `.gitignore`d ("emitted by `build_libbox` next to the AAR").
- `assets/` at HEAD contains **only** icon sets (`assets/android/**`, `assets/android-debug/**`),
  `assets/app.jpg`, `assets/face-id-svgrepo-com.svg` — the `assets/geodata/*` payload that
  `d2a2e98` deleted is gone and nothing replaced it. `tools/` at HEAD contains only
  `tools/make_icons.py`.

Consequence: **the binary that decides all runtime behaviour is absent from Git.** Every claim
about the core in commit messages (its `box.go` traffic-manager condition, its
`daemon/instance.go` `PlatformLogWriter`, its `constant.Version`, whether `1965bbf35` or
`2fd57543a` exists) is **UNVERIFIED** from this repository. `git cat-file -t` on all four core
SHAs returns *could not get object info*.

**CORE-COORDINATED surface (app ↔ fork coupling), all OWN code, no vendored upstream:**
`core/ProxyCore.kt` + `core/SingBoxCore.kt` (libbox `CommandServer`/`CommandClient`/`PlatformEvents`
/`OverrideOptions`/`SystemProxyStatus`), `bg/PlatformInterfaceWrapper.kt` (a
`PlatformInterface` implementation, including the `usePlatformAutoRedirect()=false` /
`createAutoRedirect()=error()` shim added by `d2a2e98` specifically to match the fork's own
`platformInterfaceStub`), `bg/DefaultNetworkMonitor.kt` + `DefaultNetworkListener.kt` +
`InterfaceListenerRegistry.kt` (libbox's `closeDefaultInterfaceMonitor(listener)` contract),
`utils/CommandClient.kt`, `utils/CommandTarget.kt`.

**Fork-owned (OWN) by construction:** CI, release gates, provenance, `PlatformFacts`/`ScreenFactState`/
`CoreLifecycle`/`UiClientLifetime`/`SessionGate`/`NotificationPublishGate`/
`InterfaceResolutionEpoch`/`LogRingBuffer`/`LogUiPublishGate`, `MinimalConfigBuilder`,
`RawConfigFormat`/`RawConfigDetector`/`SubscriptionParser`/`UriParser`/`ClashParser`/`YamlToJson`,
`SubscriptionRepository`, `Settings`/`ConfigStore`, the whole Compose UI, the icon sets and
`tools/make_icons.py`.

**Android manifest/permissions:** `app/src/main/AndroidManifest.xml` plus
`app/src/main/res/xml/network_security_config.xml` and a **separate debug variant**
`app/src/debug/res/xml/network_security_config.xml` — the debug-only `10.0.2.2` plaintext
allowance was added by `d2a2e98` (which also narrowed release to loopback-only plaintext, the
`47d1b77` fix made permanent).

---

## B5. SHA verification results

All 49 SHAs supplied in the brief were resolved with `git rev-parse --verify "<sha>^{commit}"` and
`git rev-parse --disambiguate=<sha>`; **every one exists, is a commit, and is unambiguous
(`ambig=1`)**. None is wrong, mis-abbreviated or misattributed. The "claimed role" column is my
reading of the brief; the "matches?" column is my verdict after reading the diffs.

| SHA (as claimed) | exists? | full SHA (12) | date | subject | matches the described role? |
|---|---|---|---|---|---|
| `d2a2e98` | yes | `d2a2e98fcb22` | 2026-10-08 | 单内核化: 只保留 Piggy-Cat-bit-shadow/sing-box | **YES** — it *is* the removal |
| `d628ef0` | yes | `d628ef084802` | 2026-10-08 | chore(android): 删除单内核化后遗留的死代码 | YES — the removal's aftermath sweep |
| `aec3893` | yes | `aec3893a05aa` | 2026-10-08 | refactor(android): 去掉 tun stack 依赖；原始配置改为自动识别，不再要用户开关 | YES, with nuance — it removes the *user switch*, not pass-through itself |
| `ebd2f79` | yes | `ebd2f79fa298` | 2026-10-08 | refactor(android): 配置收敛为 sing-box 最小集，App 不再自定路由/DNS/地区策略 | YES — this is where byte-for-byte pass-through lands |
| `ac94df1` | yes | `ac94df18bdfa` | 2026-09-20 | v0.5.8: 原始订阅配置直通内核 + 节点页分组重构 | **YES, but it is the OPPOSITE pole** — this commit *introduced* injection + the user switch |
| `a3d2321` | yes | `a3d2321b6d66` | 2026-10-09 | feat(android): wire platform memory and lifecycle facts into libbox | YES — PlatformFacts introduction |
| `a5a6783` | yes | `a5a678353c31` | 2026-10-10 | test(android): prove every trim level is forwarded verbatim (P1-D) | YES (test-only; "normalized platform facts" is the cluster, not this commit) |
| `51cb7f5` | yes | `51cb7f5d0443` | 2026-10-09 | fix(android): give the platform fact bridge a real owner and a bounded drain | YES — also introduces `CoreLifecycle` |
| `1a86247` | yes | `1a86247014e9` | 2026-10-09 | fix(android): bound the platform fact bridge to its own core session | YES — introduces the `Session` token |
| `7275c0f` | yes | `7275c0f2aa14` | 2026-10-10 | fix(android): make the initial screen reading unable to overwrite a newer screen fact | YES — the *first* (later superseded) guard |
| `481e370` | yes | `481e37041998` | 2026-10-10 | fix(android): invalidate stale screen snapshots by observation epoch, taken before the read | YES — the guard that survives |
| `15c432d` | yes | `15c432d6c8d1` | 2026-10-10 | test(android): pin the screen seed wiring, the same-value observation and the pre-read epoch | YES (test-only) |
| `36dee73` | yes | `36dee7364eed` | 2026-10-11 | fix(android): keep the foreground facts in record order, and make the gate test see its own guard | YES |
| `56eb255` | yes | `56eb255dd465` | 2026-10-12 | fix(android): order the FIRST foreground fact pair, and make the gate test see a stray establish | YES |
| `15b5ff1` | yes | `15b5ff178b3d` | 2026-10-12 | fix(android): a reentrant foreground record must not strand the next notification | YES |
| `3c65418` | yes | `3c65418f42ae` | 2026-10-11 | feat(ui): give the UI's command-socket client one owner, and feed it from the canonical foreground fact | YES |
| `3813a23` | yes | `3813a234218d` | 2026-10-11 | fix(android): the UI client is created only when the canonical lifetime says so | YES |
| `d10c9a5` | yes | `d10c9a56cd8d` | 2026-10-12 | fix(android): the startup establish needs the UI client verdict too | YES; **it is a cherry-pick of the orphan `e413c59`** |
| `2cb6eb9` | yes | `2cb6eb9b8e37` | 2026-10-12 | test(android): compose the startProxy guard with the foreground fact guard | YES (test-only) |
| `74ff3ce` | yes | `74ff3ce3fb8e` | 2026-10-10 | fix(android): stop a stale stop from converging over a newer generation | YES |
| `bdb4ced` | yes | `bdb4ced36b93` | 2026-10-10 | fix(android): claim the start token on main and gate every stop side effect | YES |
| `7d4218a` | yes | `7d4218ad2f90` | 2026-10-10 | fix(android): let a stop's teardown stop stranding the start that follows it | YES — removes the second counter |
| `5d9f4f2` | yes | `5d9f4f23369b` | 2026-10-09 | fix(android): make a destroyed Service instance terminal for core startup | YES — also the listener-key change |
| `24c8372` | yes | `24c8372e5117` | 2026-10-09 | fix(android): bind every notification callback to its own generation token | YES |
| `cf3dc09` | yes | `cf3dc0933e00` | 2026-10-09 | fix(android): stop blocking the main thread in VpnService.onRevoke | YES |
| `8432caf` | yes | `8432cafb6edd` | 2026-10-10 | fix(android): resolve the default interface off the main thread, and make the epoch authoritative | YES |
| `ed014c6` | yes | `ed014c61c701` | 2026-10-10 | fix(android): isolate the native default-network listener by owner | YES, with nuance — it *corrects* round 5 and adds an identity registry |
| `89951ae` | yes | `89951ae0162e` | 2026-10-08 | fix(android): 修正内核来源错位并加构建期校验 | YES |
| `f133dc9` | yes | `f133dc957802` | 2026-10-11 | release(android): pin the core to the SHA the AAR is actually built from | YES |
| `25ac148` | yes | `25ac1482f4ee` | 2026-10-12 | release(android): make an APK without its own ABI's engine fail the release gate | YES; **cherry-pick of the orphan `4791101`** |
| `a0e2722` | yes | `a0e272217727` | 2026-10-09 | ci: port the unsigned read-only dry run and measure signature state instead of assuming it | YES; also the **absorption** commit |
| `c7cc03d` | yes | `c7cc03d4c931` | 2026-10-10 | ci: make the publish gate fail closed instead of verifying nothing | YES — a real fail-open gate |
| `61b807e` | yes | `61b807ea4db3` | 2026-10-10 | fix(ci): upload the verified list itself, and refuse a bundle with files we cannot name | YES |
| `722d22b` | yes | `722d22b82668` | 2026-10-10 | fix(ci): publish exactly the verified bundle, not a parallel path expression | YES |
| `58b8309` | yes | `58b83094f9a1` | 2026-10-10 | ci: let a dispatch pick the core revision, so a broken upstream HEAD cannot blind the app | YES |
| `f2cde01` | yes | `f2cde016d931` | 2026-10-10 | ci: run the Android dev CI only on manual dispatch | YES |
| `919089d` | yes | `919089d660b1` | 2026-10-09 | fix(android): restore kernel traffic statistics without opening a port | YES — **but its diagnosis is wrong; see `fb5a361`** |
| `fb5a361` | yes | `fb5a3615aa78` | 2026-10-09 | fix(android): correct the traffic-statistics diagnosis; keep clash_api out | YES — the revert |
| `060b009` | yes | `060b009613dd` | 2026-10-09 | perf(android): make connections monitoring page-scoped; show runtime status at home | YES |
| `7416c73` | yes | `7416c736ded0` | 2026-10-09 | perf(android): retain full logs with coalesced UI snapshots | YES |
| `e1ec9f8` | yes | `e1ec9f8ce53c` | 2026-10-10 | docs(android): finalize round-8 evidence and references | YES — docs-only (verified by name-only diff) |
| `6ea83f8` | yes | `6ea83f884b16` | 2026-10-10 | docs(android): state the tested product SHA and how to prove the last commit is docs-only | YES — docs-only |
| `e0284cc` | yes | `e0284ccdead2` | 2026-10-10 | docs(android): record the verified CI run, the device regression and the round-8 report | YES — docs-only |
| `54a3eb0` | yes | `54a3eb06f591` | 2026-10-10 | docs(android): reconcile the round-7 report with round-8 evidence, item by item | YES — docs-only |
| `bffb8ad` | yes | `bffb8ad494fb` | 2026-10-10 | docs(android): T08 becomes a PASS on real traffic evidence, and record a measurement trap | YES — docs-only |
| `dc80505` | yes | `dc80505c8191` | 2026-10-10 | docs(android): isolate the CI failure to upstream testing (E-6) | YES — docs-only |
| `017fff7` | yes | `017fff7f47ae` | 2026-10-10 | docs(android): fill in the T01-T12 matrix with what the candidate APK actually did | YES — docs-only |
| `2b206cd` | yes | `2b206cdadd19` | 2026-10-10 | docs(android): Gate 4-B candidate regression on the same AVD (E-5c) | YES — docs-only |
| `59b03f1` | yes | `59b03f12fb6b` | 2026-10-10 | docs(android): record the candidate/baseline identity chain side by side | YES — docs-only |

**Misattributions found: none.** Two corrections worth stating explicitly:

* **The supplied list is a subset, not a map of the evolution.** ~40 further commits carry equal
  or greater weight and are absent from it: `9212584`, `cfcda17`, `2e3c560`, `dfd685c`, `8818a53`,
  `926d6cd`, `47d1b77`, `b26d590`, `e75d6eb`, `ac94df1`'s siblings, `1a0b506`, `22d9ab4`,
  `8d8bb9f`, `d8b3093`, `ad91f44`, `2f0bcbf`, `48923e0`, `9af5e46`, `2b2d225`, `38074fa`,
  `997cd8f`, `cd9153d`, `21c7f68`, `e8bd186`, `87b3358`, `18320df`, `bff3ce6`, `0a299ce`,
  `d3af634`, `67a6df0`, `fb606bf`, `7b6aeb3`, `4d15e3b`, `32cc6cc`, `1efaa8f`, `ae89388`,
  `3c06b53`, `19817c5`, `039bf63`, `2171167`, `6442c95`, `6864a1e`.
* **`d10c9a5` and `25ac148` (and `15b5ff1`) are cherry-picks, not first-class commits on `main`.**
  Their originals (`e413c59`, `4791101`, `baa9958`) are orphan objects. Anyone treating the
  SHA list as "the history" will miss the fact that three of the audited SHAs have patch-equivalent
  twins and that the twin branch names are only recoverable from the reflog.

**Commit-message vs diff disagreements (flagged).**

| Commit | Claim | Reality | Verdict |
|---|---|---|---|
| `919089d` | `clash_api: {}` is required for the traffic manager | premise false; `fb5a361` reverts the config change and explains the real cause (measurement, not the kernel) | **DISAGREES — corrected one commit later by `fb5a361`** |
| `d2a2e98` | "原始配置直通只接受 sing-box 格式（`RawConfigFormat.SINGBOX`）" | the acceptance *gate* is sing-box-only, but the `RawConfigFormat` **enum still enumerates CLASH/XRAY** for detection/labelling; the applier methods were deleted | Consistent on substance; **the message reads as if the enum were pruned** — it was not |
| `039bf63` | subject prefix `docs(android)` | the diff modifies `app/src/main/java/com/interstellar/proxy/bg/BoxService.kt` — but **only adds a KDoc block**, no code | **Technically a code-file change; content is comment-only.** Verified by reading the full diff |
| `1a0b506` | "主源码 16263 → 15359 行 (74 → 72 文件)" | file count **verified** (74 → 72 under `app/src/main/java`); my line count gives **14997 → 14152**, so neither absolute number nor the −904 delta reproduces (−845 measured) | **PARTIALLY VERIFIED — file count exact, absolute line counts UNVERIFIED** (counting scope/method unknown) |
| `ebd2f79` | "Settings 字段 30 → 22" and "字符串 423 → 304 (每种语言删 119 条)" | measured **exactly**: `Settings.kt` fields 30 (`1a0b506`) → 22 (`ebd2f79`); `values/strings.xml` string keys 423 → 304 = **−119**; `values-en` identical | **VERIFIED exactly** |
| `e8bd186` | reports a *suspected* "normal `stopService` does not close the notification" bug | message itself records it as **"NOT REPRODUCED … 与提示词不符"** and shows all three paths do close it | Honest self-refutation; no diff to check |
| `5d9f4f2` | says a `closed` check inside `publish()` would be needed | message records that the check was **deleted** as structurally unreachable, after fault injection showed it changed nothing | Message and diff agree (the deletion is the diff) |
| `51cb7f5` | "队列有界性: 文档改为真实表述 —— 非有界(UNLIMITED)" | code is `Channel<() -> Unit>(Channel.UNLIMITED)` at `PlatformFacts.kt:174` | **VERIFIED** (the message corrects an earlier *claim*, not the code) |
| `74ff3ce` | "`generation` 保持第四轮语义完全不变" | true at the time; `7d4218a` **later deletes the `generation` counter entirely** | Message was accurate for its commit; superseded afterwards |
| `6442c95` | deliberately ships a tree where the pin check **fails** | message states this openly and explains why it is the safe direction; `f133dc9` fixes it next | **Honest red build**, correctly self-disclosed |
| `3c65418` | "Committed on a new branch rather than main, deliberately: main is left at `e1ec9f8`, untouched" | true; the branch (`jiejiebox/v0.1.6-ui-client-lifetime`) was later fast-forward merged and **deleted**, so only the reflog names it | Message accurate; the *branch* is not recoverable from refs |
| `f6a91df` | refuses to adopt `fix/release-core-pin`'s change | the refused commit `5445f1b` exists as a **dangling** object with exactly the described one-line pin change to `2fd57543a` | **VERIFIED — including the refusal** |
| `fb5a361` | "旧 pin 与新 pin 这个条件完全相同(已 diff 确认)" | **UNVERIFIED** — no core objects exist in this clone | Cannot check |

---

## B6. Gaps & risky provenance (things no commit explains)

1. **The shipped engine is not in Git and cannot be audited here.** `app/libs/libbox.aar` and
   `app/libs/libbox.provenance` are `.gitignore`d (`.gitignore` at HEAD), and `app/src/main/jniLibs/`
   likewise. Every factual claim about the core — `box.go`'s traffic-manager condition
   (`919089d`, `fb5a361`), `daemon/instance.go`'s unconditional `PlatformLogWriter: s`,
   `constant.Version`, the existence and content of `c35faabf4` / `a8fdc36c` / `0ff90482b` /
   `1965bbf35` / `2fd57543a` / `ad824f609` / `47e08ed3` — is **UNVERIFIED**. `git cat-file -t` on
   each of those returns *could not get object info*. **The pin is a promise that this repository
   cannot check.**

2. **No CI evidence is in the repository.** The commit messages and `docs/**` cite run ids
   (`37912264064`, `37913585896`, `37953003446`, `37971211698`, `37972168650`, `38005659283`,
   `38006795028`, and counts like "176 PASS / 192 PASS / 195 PASS / 210 tests / 241 unit tests"),
   dry-run outcomes and device/AVD results. **None of it is verifiable from Git**: no artifacts,
   no logs, no run metadata, no `build-info.json`, no `SHA256SUMS.txt`. `f6a91df` itself documents
   that a previous round mis-cited a run (`37913585896` had `headSha=5445f1b` on branch
   `fix/release-core-pin`, not `fb5a361`) — i.e. the *one* documented instance of CI-evidence
   misattribution was caught by hand, not by tooling. Treat every CI number in a commit message
   as **UNVERIFIED**.

3. **Three commits exist only as orphans, reachable solely through the reflog.** `git fsck
   --dangling` reports exactly three: `5445f1b` (the refused core re-pin), `2334bad` and
   `e0fe962` (two parallel rebases of the same CI commit, one carrying
   "(cherry picked from commit e0fe9628…)" in its body). A `git gc` would delete them, and with
   them the only surviving evidence of (a) the rejected `2fd57543a` pin and (b) the absorbed
   `ci/unsigned-dryrun-min-permissions-rebased` branch. **Nothing in the repository references
   them** — `a0e2722` and `f6a91df` name the *branches*, but no ref, note or doc names the SHAs.
   This is the most fragile provenance in the whole audit.

4. **Five deleted topic branches are named only in the reflog**, and three of their tips
   (`4791101`, `e413c59`, `baa9958`) are **not** ancestors of `main` and are **not** dangling
   (the reflog keeps them alive). So the repository simultaneously contains (i) three
   patch-equivalent duplicate commit pairs and (ii) the *first-class* commits of those three
   changes only on `main`. Anyone reconstructing "what changed" from `main` alone gets the right
   content but the wrong authorship story; anyone reconstructing it from `fsck` alone misses them
   entirely. **INFERRED** from reflog + `patch-id`.

5. **A second worktree exists on disk**: `C:/Deepseek/jiejiebox-work/baseline-r5` at detached
   `2171167`. It is a local comparison worktree; `docs/android-screen-power-parity-final-report.md`
   states it was "本地 detached 对照, **未推送、未提交**". I did not modify or inspect its
   contents beyond `git worktree list` (read-only constraint).

6. **LICENSE vs the one ported file.** The repository `LICENSE` is **MIT**, "Copyright (c) 2026
   Ryan Yan", and has not changed since the root commit. The single ported file
   (`bg/DefaultNetworkListener.kt`) declares **GPLv3** provenance and two third-party copyrights
   (Max Lv, Mygod Studio). **No commit in this history reconciles MIT-licensed repository with a
   GPLv3-declared file**, and no commit adds a third-party notice file. **UNKNOWN** whether this
   is intentional; flagged as a real compliance question.

7. **Four product names for one product, no reconciling commit.** Repo/APK `satelite-one`
   (`21c7f68`), public brand Jiejiebox (`22d9ab4`, `README.md:6`), package/`applicationId`
   `com.interstellar.proxy` (`db33656`, explicitly held constant by `21c7f68`), and a live
   "Jiejiebox's own UI process" string in `core/ProxyCore.kt:18`. A stale
   `interstellar.jks` / `keystore.properties` / `signing.properties` ignore rule persists in
   `.gitignore` even though the APK prefix was renamed. Cosmetic, but it means any tooling keyed
   on the product name has four candidates.

8. **The "last commit is docs-only" invariant is asserted, not enforced.** `6ea83f8` tells the
   reader how to prove the last commit is docs-only and `docs/ROUND8-REPORT.md` gives
   `git diff --name-only ae89388..HEAD -- app/src/main` (expected empty). But `6864a1e` and
   `942b6fe` — the two commits *after* that instruction — touch `KNOWLEDGE/**` and
   `KNOWLEDGE_STAGE1_REPORT.md` (verified: 14 files and 5 files respectively), i.e. the invariant
   holds **only if `KNOWLEDGE/**` counts as docs**. Nothing in Git states which paths count.

9. **No commit explains why `v0.3.0`'s tag is 29 commits after its version commit.** `d2719ab`
   fixed the *CI* consequence ("tag 落点可能在版本提交之后") but no commit explains or corrects the
   actual misplacement of `v0.3.0`, `v0.4.0` and `v0.5.3`. Since the release workflow validates
   `tag == v<versionName>` from `version.properties`, those three tags would have shipped a build
   whose `versionName` still matches — but whose tree includes 1 resp. 29 commits of unreleased
   change. **UNKNOWN** whether that was intended.

10. **88 commits and a replaced architecture sit after the last tag.** The single-core client, the
    whole `PlatformFacts` axis, the lifecycle/ownership gates, the provenance/ABI release gates and
    the round-7/8 evidence work are all **unreleased** (`git describe` = `v0.5.10-88-g942b6fe`;
    `ROUND8-REPORT.md` states `RELEASE_NOT_TRIGGERED`). The two most safety-relevant gates
    (`25ac148`'s ABI guard, `61b807e`'s verified-list upload) have never run on a tag.

11. **The last three audited fixes were written on throwaway branches and cherry-picked**, which
    means `main`'s history was assembled by rewriting, not by merging, and **no merge commit
    records why**. The reflog is the only trace. `d10c9a5`'s and `15b5ff1`'s trees differ from
    their originals' trees solely because their parents differ — a fact invisible without
    `patch-id`.

12. **`DefaultNetworkListener.kt` retains a main-looper `Handler` (`:141`) after `8432caf` moved
    the *resolve* work off the main thread.** The commit is explicit that only the resolve path
    moved; the callback still dispatches on the main looper. No commit closes that remaining
    main-thread coupling — it is a known, documented boundary rather than an unexplained one, but
    it is the reason `8432caf`'s own test had to assert at the bytecode level.

13. **`ProxyCore.pause()` / `wake()` survive as interface members that Android no longer calls.**
    `a3d2321` documents them as retired ("the Android device axis no longer drives it") but no later
    commit removes them, and no test asserts that nothing calls them. A future edit could re-wire
    them and re-create the two-writers problem `a3d2321` removed. **Gap in the locking evidence**
    (contrast with `1efaa8f`, which *did* pin the analogous "one wake producer" rule).

14. **`git log` shows no branch, tag or note recording the two audit/design tags the brief mentions
    ("archive/design tags").** The only refs are the 15 release tags listed in §B3. If any archive
    or design tag ever existed it was deleted and left no reflog trace (`for-each-ref` = 18 refs,
    `reflog --all` = 165 entries, none for a non-release tag). **UNKNOWN / not present.**

---

### Verification legend used above

* **VERIFIED_GIT** — proved by a diff, tree, ref, reflog, `patch-id`, object type, or file:line read.
* **INFERRED** — stated with the basis in parentheses.
* **UNVERIFIED** — a claim made by a commit message or doc that cannot be checked from this
  repository (core-repo facts, CI run ids, device results).
* **UNKNOWN** — nothing in the repository bears on it.

Everything in §B0, §B1 (except core-repo claims), §B2, §B3, §B4 and §B5 is VERIFIED_GIT unless
explicitly marked otherwise.
