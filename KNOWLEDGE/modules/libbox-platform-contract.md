# libbox and the platform contract

## Apple source and single-runner delta (2026-10-29)

At the current source target the Apple gitlink and ABI check constant both identify
`044e6d5447eb1285c950bd12cee10effab641f5a`; Android is `c7837107dc0126fb704fdf08cf203e10b30c8a22`.
`check-libbox-abi.sh --pin-only` passed locally for those parent gitlinks. It is not a full ABI or
Swift call-site scan. The two local client checkouts were older than those gitlinks and were not
altered. Windows symlink materialization and a missing desktop checkout still limit local client
inspection. For source selection, optional dispatch SHA, single-runner mode, and frozen release
requirements see `ci-release.md`'s 2026-10-29 section; the historical v0.1.31 observations below
remain scoped to that revision.

## Post-baseline TUN reset handoff (testing, 2026-10-28)

**VERIFIED_SOURCE** at `011f3fdcd99173acd3e80483b50cb76b07bf6ff4`:
`protocol/tun/inbound.go` implements `InterfaceUpdated`, `stackDidStart`,
`consumePendingReset` and `resetNetwork`. Before stack start an update sets a pending flag;
the post-store start recheck and atomic pending swap close the start/update handoff. Multiple
pre-start events can coalesce. Once started, each accepted update resets independently, and a
pending startup reset is consumed separately. Closed inbounds do not reset. Regression cases are
in `protocol/tun/reset_before_start_test.go`; their presence does not establish a test run.
The post-start behavior includes `1ff0af39110`, which is after `v0.1.30`; do not describe it
as released in v0.1.30. The later v0.1.31 release tag points to a descendant containing that change;
this ancestry does not establish device network-switch behavior.

### IPv6 entry presentment

`route/network_family_entry.go` publishes the box-level entry decision once; the TUN consumes it
before opening the interface (`protocol/tun/entry_ipv6.go`). A network change can alter per-dial
capability without silently rewriting the open TUN's addressing; changing the entry decision requires
a box restart. The box pre-check is intended to ensure all configured TUNs can apply suppression;
if the final reduction nevertheless refuses, the inbound keeps IPv6 and logs a disagreement without
rolling the shared decision back. Note that an older comment in `route/network.go` still describes
a later correction write; the current store and inbound code, not that comment, define the behavior.
This is `VERIFIED_SOURCE` for the current source, not a real-device transition test.

### TUN DNS hijack and fast-path ordering

`protocol/tun/inbound.go` configures the explicit or derived DNS hijack addresses and separately
tracks port-53 hijack mode. `JudgeFlow` checks DNS hijack before direct bypass or route exclusions;
UDP and TCP do not have identical resulting actions, so preserve the exact branches. The
`dns_hijack_configuration_test.go` and `direct_fast_path_dns_test.go` tests cover configuration and
ordering. The local TUN package suite passed on Windows, but it is not a real-device leak test.

### Command-server shutdown

`experimental/libbox/command_server.go` now calls `CloseService` from `CommandServer.Close` before
closing the `StartedService` subscribers and managers. An embedder that closes the server without
first issuing the separate close-service RPC therefore still requests Box shutdown. The method
discards the `CloseService` error, so its void return is not evidence that Box cleanup succeeded.
See the `p2ios`-tagged `TestCommandServerCloseClosesTheBox`; no device-level result is implied.

### Platform default-interface monitor ordering

At the testing target, `experimental/libbox/monitor.go` refreshes the interface list first when
the manager supports `RefreshInterfaces`, then publishes the default-interface and pause changes.
If the default interface remains the same, it invokes `UpdateNetworkEnvironment` when available so
gateway or other environment changes still get an observation. Older implementations without the
split interface use `UpdateInterfaces` instead. The monitor rechecks its closed flag under the
default-interface lock after the potentially slow refresh, preventing an update from writing the
default interface when close has already landed during that refresh. `monitor_ordering_test.go` and
`monitor_close_fence_test.go` exercise these boundaries with test doubles; they do not prove a
physical Android or Apple network transition.

**Scope:** `experimental/libbox/*`, `daemon/*`, `include/*` as they define the client-facing surface,
`cmd/internal/{mobilebuildtags,applebuildtags,appletags}`, `release/apple-client-refs.env`, and the
Apple/Android artifact pins.

**This module primarily records the core side of the contract.** The clients are separate Git
submodules and their availability depends on the checkout. In this calibration, Apple and Android
are initialized at `3aedd7417488` and `9d15717a85c5`; desktop is uninitialized. Local Windows Git
has `core.symlinks=false` for Apple, so link entries are materialized as text files, not followed as
real links. Client-source findings below are scoped to those revisions and this checkout.

**Load this module when changing:** any exported libbox symbol, `SetupOptions`, the CommandServer API,
the ABI version, a platform-facts API, or the Apple/Android pin.

---

## 1. There is no `libbox.go`

The gomobile surface is spread across ~85 files in `experimental/libbox/`.

### Top-level entry points

| Symbol | Anchor |
|---|---|
| `Setup(*SetupOptions) error` | `setup.go:162` |
| `ReloadSetupOptions` | `setup.go:90` |
| `SetLocale` | `setup.go:171` |
| `Version()`, `GoVersion()` | `setup.go:178`, `:182` |
| `FormatBytes` / `FormatMemoryBytes` / `FormatDuration` / `FormatBitrate` / `FormatNATMapping` / `FormatNATFiltering` / `FormatFQDN` / `ProxyDisplayType` | `setup.go:186-252` |
| `CheckConfig(string) error` | `config.go:51` |
| `GenerateConfigSchema()`, `FormatConfig`, `HasTunInbound` | `config.go:259`, `:267`, `:282` |
| `AvailablePort(int32)`, `RandomHex(int32)` | `service.go:476`, `:496` |
| `ReadAndroidVPNType` | `build_info.go:32`, android-only |

### `SetupOptions` — the platform handshake

`setup.go:49-66`. **Sixteen fields**, all strings/ints, so the call is ABI-stable by construction:

`BasePath`, `WorkingPath`, `TempPath`, `FixAndroidStack`, `CommandServerListenPort`,
`CommandServerSecret`, `LogMaxLines`, `Debug`, `CrashReportSource`, `AppVersion`,
`AppMarketingVersion`, `OomKillerEnabled`, `OomKillerDisabled`, `OomMemoryLimit`,
`PowerReportEnabled`, `PlatformMetadata`.

**Three properties of this handshake that a client must know:**

* **`CommandServerListenPort` is an `int32` narrowed to `uint16` with no range check** (`setup.go:80`).
  A value above 65535 wraps silently.
* **`CommandServerSecret` empty means no authentication.**
* **`PlatformMetadata` that is not valid JSON is silently dropped to `nil`** (`setup.go:95-99`).
* **`ReloadSetupOptions` (`:90`) covers only 5 of the 16 fields.** After start, use it — never `Setup`
  again.

The declared surface is large: `gomobile_surface_test.go:348 TestGomobileMethodResultSurface` counts
**452 bound declarations, 23 of which have a pointer-bearing result** — a registered debt, because a Go
pointer in gomobile's packed result frame is the `bulkBarrierPreWrite` crash (§8).

---

## 2. The policy boundary: the core decides, the platform reports

**This is real, and it is enforced by where the classification lives.**

| Concern | Owner | Anchor |
|---|---|---|
| Reporting a platform observation | **the platform**, through `experimental/libbox/platform_events.go` — a **thin forwarder**: `MemoryTrim(level int32)` `:234`, `SetAppForeground(bool)` `:243`, `SetScreenOn(bool)` `:261`, `ReportDeviceWake()` `:269`, `Close()` `:277` | `:92` builds `boxPlatformTarget`; `:194 NewPlatformEvents` routes into `runtimecoord.PlatformEvents` |
| Deciding what a level **means** | **the core** — `TrimLevel` constants and `TrimLevel.Action()` | `common/runtimecoord/platform_events.go:152-220` |
| Coalescing windows | **the core** — `trimCoalesceWindow = time.Second` (`:223`), `releaseCoalesceWindow = adapter.RecoveryWindow` (`:231`) | as cited |
| The device state machine | **the core** — `common/power.Governor` | `power-mobile-lifecycle.md` |

**If a client sent "you should trim", policy would have moved into three client codebases.** Instead a
client sends the number its OS gave it and the core classifies it. That is the contract.

### The Android trim ladder (core-owned)

`common/runtimecoord/platform_events.go:152-179`; the action map is `TrimAction` at `:184-191`
(`TrimIgnore` = 0, `TrimProgressive` = 1, `TrimRelease` = 2).

| Constant | Value | Action |
|---|---|---|
| `TrimLevelUnspecified` | 0 | **`TrimIgnore`** — *"the zero value: no trim was reported. It is **not** Android's level 0."* (`:155-156`) |
| `TrimRunningModerate` | 5 | **`TrimIgnore`** — advisory (`:158-159`, `:212-213`) |
| `TrimRunningLow` | 10 | `TrimProgressive` (`:160-161`) |
| `TrimRunningCritical` | 15 | **`TrimRelease`** — *"the documented meaning is that a currently-running process will be killed if memory is not released. That is the only Android fact that justifies the reset-shaped pass"* (`:162-164`, `:148-151`) |
| `TrimUIHidden` | 20 | **`TrimIgnore`** (`:165-170`) |
| `TrimBackground` | 40 | `TrimProgressive` (`:171-172`) |
| `TrimModerate` | 60 | `TrimProgressive` (`:173-174`) |
| `TrimComplete` | 80 | `TrimProgressive` — *"A reset here would shed the transports of a process that is already going away"* (`:175-178`) |
| anything else | — | **`TrimIgnore`** (`default:` at `:211-215`) |

**Progressive = 10, 40, 60, 80. Release = 15. Ignore = 0, 5, 20, and every unknown value.**

The numbers are Android's own `ComponentCallbacks2` values carried through unchanged (`:131-136`) —
the core maps them, it does not renumber them. Pinned by
`experimental/libbox/platform_events_test.go:344-352`.

**Why an unknown level is ignored** (`:193-201`, quoted): *"Trimming is not free: it drops a DNS or HTTP
pool the next request then has to rebuild, so a trim issued for a level whose meaning we do not know is
a cost paid for a guess. Ignoring it is the safe direction — the consequence is only that a genuinely
tight process keeps its pools until the next level we do understand, while the alternative would make
every future platform level a silent behavioural change."*

**Why `TrimUIHidden` is not a memory signal** (`:165-170`): *"This is a LIFECYCLE fact, not a memory
reading, and it is deliberately NOT mapped to a trim… the trim callback is not a reliable delivery
channel for it."* Repeated on the client side at `experimental/libbox/platform_events.go:229-233`:
*"Do not map `TRIM_MEMORY_UI_HIDDEN` to a lifecycle fact here."*

**Coalescing:** `trimCoalesceWindow = time.Second` (`:223`), `releaseCoalesceWindow =
adapter.RecoveryWindow = **90 s**` (`:231`, `adapter/runtime.go:153`). A **different** level acts at once
— *"an escalation is a new fact"* (`:222`). The trim callback must be reported **off the main thread**
(`experimental/libbox/platform_events.go:222-227`).

### Three structural proofs that the core, not the platform, decides

Not comments — artifacts:

1. **The Android sink physically cannot answer back.** Every `PlatformEvents` method returns nothing
   (`experimental/libbox/platform_events.go:234-282`), and the header (`:11-17`) gives the reason: a
   `string`/`[]byte` result would put a Go pointer in the packed cgo result frame. **A design where the
   platform classified would need a return value.** Pinned by
   `platform_events_test.go:106 TestPlatformEventsAreScalarAtTheBoundary` and exhaustively by
   `gomobile_surface_test.go:348`.
2. **`writes == stats.Pauses + stats.Wakes`** — `platform_events_action_ownership_test.go:28`,
   `:108-110`: the target writes the device-level mirror **exactly once per *published action***, never
   per fact, and a **deferred** wake reaches neither the manager nor the mirror (`:74-80`).
3. **The sink cannot decorate a measurement** — `platform_events_test.go:66-99` parses the source and
   **forbids** it from importing `adapter` or calling `ContextWithProbeOrigin` / `MeasurementContext` /
   `WithValue` / `WithCancel` / `WithTimeout`.

### Where the boundary does NOT hold — the scope limit

`PlatformInterface` (`experimental/libbox/platform.go:5-35`) also carries platform-authored
**judgements** the core consumes without re-deriving: `isExpensive` / `isConstrained` (`:123`),
`NetworkInterface.Type` / `Metered` (`:141`, `:145`), and capability declarations
(`UnderNetworkExtension`, `IncludeAllNetworks`, `UsePlatformShell`,
`UsePlatformBridge :31`, `UsePlatformAutoRedirect :33`).

`UseProcFS` is no longer a `PlatformInterface` method at the reviewed target. The wrapper delegates
owner lookup to `PlatformInterface.FindConnectionOwner`; Android also exposes a separate libbox
`FindConnectionOwner` implementation backed by procfs (`connection_owner_android.go`). Do not infer
that removal of the old wrapper branch removed the Android helper or that every client invokes it.

> **Accurate wording: the core owns lifecycle and memory classification and the actions they earn; the
> platform owns network cost/type classification and its own capabilities.** Saying "the platform only
> reports facts" as a global invariant of `PlatformInterface` is false.

Two of the platform's judgements are **reconciled** rather than trusted:
`handover_coalescing_test.go:234` (a metered source must agree with `UpdateDefaultInterface`) and
`:276` (a metering change alone must not claim a network generation).

**One core-side admission of a *client-side* double-driver, quoted and `UNVERIFIED` here:**
`experimental/libbox/platform_events.go:253-260` records that Android also drives `Pause`/`Wake` from
`ACTION_DEVICE_IDLE_MODE_CHANGED`, and that mixing the two *"would let a Doze wake lift a pause the
screen fact still asserts, with neither path knowing."* At Android client revision `9d15717a85c5`,
`BoxService.kt` receives `ACTION_DEVICE_IDLE_MODE_CHANGED`, queues `isDeviceIdleMode`, and calls
`commandServer.pause()` or `.wake()` in the consumer. This confirms that particular client source
path; it does not by itself establish the reported device-level race or all client configurations.

---

## 3. Level, edge and snapshot — four vocabularies

| Vocabulary | Level | Edge | Snapshot / reconcile |
|---|---|---|---|
| `common/power.Governor` | `DevicePaused` `:471`, `NetworkPaused` `:659` | `DeviceWake` `:490`, `SleepStarted` `:525`, `Resumed` `:563`, `NetworkWake` `:671` | `State` `:53`, `Policy` `:168`, `ReuseBoundary` `:141` |
| `pause.Manager` | its level getters | its edge callbacks | — |
| `box_lifecycle.go` (the Apple mapping) | `screenState(on)` `:173-179`, `lockState(locked)` `:193-199` | `slept()` `:94-101`, `resumed()` `:141`, `woke()` `:156-163` | `reconcileScreenState` / `reconcileLockState` `:228-252` — **level-only** |
| `runtimecoord/platform_events.go` | the trim ladder | screen/foreground edges | the event snapshot |

**Why the reconcile APIs must be level-only:** a current-state read is not a transition. If a re-read
published an edge, every poll would look like a fresh transition. Pinned by
`box_lifecycle_ordering_test.go` and by `645d96b4` / `60681f27`.

**Why a display turning on is not an unlock:** `box_lifecycle.go:23-36`, `:173-199` — display-on
publishes only a resume *edge*; only an **unlock** releases the level.

---

## 4. `CommandServer` / `CommandClient` — the vocabularies a client speaks

| Side | Anchor |
|---|---|
| `CommandServerHandler` | `command_server.go:51` |
| `NewCommandServer(handler, platformInterface)` | `:61` |
| gRPC auth interceptors | `:111`, `:129` |
| `Start` / `Close` | `:147`, `:201` |
| `StartOrReloadService(configContent, *OverrideOptions)` / `CloseService` | `:217`, `:236` |

**Lifecycle and control (the platform→core surface):**

| Call | Anchor | Kind |
|---|---|---|
| `Pause()` | `:390` | edge |
| `Wake()` / `WakeNow()` | `:448`, `:476` | edge |
| `RecordScreenState(on bool)` | `:504` | edge |
| `RecordLockState(locked bool)` | `:533` | edge |
| `ReconcileScreenState` / `ReconcileLockState` | `:564`, `:584` | **snapshot (level)** |
| `ResetNetwork` | `:599` | action |
| `UpdateWIFIState` | `:607` | fact |
| `rememberDeviceLevel` / `restoreDeviceLevel` | `:295`, `:353` | internal level bookkeeping |
| `NeedWIFIState` / `NeedFindProcess` | `:248`, `:256` | capability query |

`platformHandler` implements `ServiceStop` / `ServiceReload` / `SystemProxyStatus` /
`SetSystemProxyEnabled` / `TriggerNativeCrash` / `WriteDebugMessage` / `ConnectSSHAgent`
(`:617-648`).

**Client side:** `CommandClient` (`command_client.go:25`), `NewCommandClient` (`:92`),
`NewStandaloneCommandClient` (`:83`), `SetXPCDialer` (`:79`), `Connect` (`:260`),
`ConnectWithFD` (`:264`), `Disconnect` (`:343`); streams `handleLogStream` (`:435`),
`handleStatusStream` (`:467`), `handleGroupStream` (`:488`), `handleClashModeStream` (`:505`),
`handleConnectionsStream` (`:547`), `handleOutboundsStream` (`:569`); actions `SelectOutbound`
(`:586`), `URLTest` (`:599`), `SetClashMode` (`:611`), `CloseConnection` (`:623`),
`CloseConnections` (`:635`), `GetSystemProxyStatus` (`:675`), `TriggerGoCrash` (`:697`),
`TriggerNativeCrash` (`:709`), `TriggerOOMReport` (`:721`), `GetStartedAt` (`:750`),
`StartNetworkQualityTest` (`:783`), `StartSTUNTest` (`:859`), plus per-endpoint subscriptions for
Tailscale (`:976-1180`), USBIP (`:989`, `:1298`), OpenConnect (`:1002`), OpenVPN (`:1058`),
Taildrop (`:1354`, `:1367`, `:1441`, `:1543`, `:1556`).

**`daemon`:** `StartedService` (`started_service.go:48`), `ServiceOptions` (`:89`),
`NewStartedService` (`:105`), `StartOrReloadService` (`:324`), `CloseService` (`:422`);
`Instance` (`instance.go:26`), `attachInstance` (`:155`); `ManagedService` (`managed_service.go:17`);
`NewServer` (`server.go:16`); `NewAttachedService` (`attached_service.go:11`).

---

## 5. ABI versioning

| Element | Anchor |
|---|---|
| the constant | **`daemon/started_service.go:39` — `const APIVersion = 4`** |
| served by | `StartedService.GetVersion` (`:208-213`, `ApiVersion: APIVersion` at `:211`) |
| the wire field | `daemon/started_service.pb.go:297` (`ApiVersion int32`), `GetApiVersion()` `:339` |
| consumed by | `experimental/libbox/command_client.go:760 GetAPIVersion()` → `version.ApiVersion` (`:766`) |

`daemon/started_service.pb.go`, `managed_service.pb.go` and their `_grpc.pb.go` are **checked-in
generated code**; `cmd/internal/protogen` regenerates them (`Makefile:57 proto`). **`protogen` execs a
`protoc` binary that the `Makefile` never installs**, so `make proto` needs `protoc` on `PATH`
separately.

### The ABI surface varies with the build tag set

`include/registry.go` is tag-polar per capability (see `build-pruning-release.md` §2), so **which
libbox symbols exist depends on the tag set**. That is why three `cmd/internal` packages exist with no
`main.go` — `applebuildtags`, `mobilebuildtags` — plus `appletags`, which is a printer over them.

**The Apple geometry tag** is selected by exactly one command, `go run ./cmd/internal/appletags
-mobile-only` (`appletags/main.go:33`), and the low-memory CI gate **fails rather than falling back**
if it returns empty (`verify.yml:252-258`). The doc comment states why it exists: the low-memory gate
and the memory benchmark previously read `DEFAULT_BUILD_TAGS_OTHERS`, which *"does not match what
libbox builds"*, so those checks *"were testing a configuration no Apple binary ships"*.

The **mobile-only** tag must never enter the shared set: `-tags` applies to every target, so a
mobile-only tag placed there reaches macOS in a mixed build. `applebuildtags/tags.go:63` and
`mobilebuildtags/tags.go:42` both say so, and the tag travels via gomobile's `-tags-not-macos`.

---

## 6. The Apple pin — and the gap

### One resolved source per build, and a separate gitlink check

At `cb71cf80d72`, `release/apple-client-refs.env` chooses the Apple client repository's `main` tip
by default; `APPLE_CLIENT_REF` or `APPLE_CLIENT_SHA` can override it. `client-apple.yml` resolves one
exact SHA in its `resolve-apple-source` job and passes that result to the downstream jobs. This is the
build selection; the parent `clients/apple` gitlink is a local checkout default, not the default build
authority. `assert-shared-source` requires iOS and macOS to agree, reports a gitlink mismatch, and
only makes that mismatch fatal when `APPLE_CLIENT_REQUIRE_GITLINK=1`. Re-resolving a moving branch
independently in each job would defeat the one-SHA boundary.

The gitlink still matters for a different check: `verify.yml` runs `check-libbox-abi.sh --pin-only`,
which compares both client gitlinks to the script's `VERIFIED_*_SHA` constants. The full check is
declared in `client-apple.yml` and includes a call-site scan as well as the gitlink check. A matching
gitlink and constant **does not prove the independently resolved Apple source is the same commit**;
inspect the resolved build SHA and actual run, especially if the branch moved. This host ran only the
pin-only check, not the full client ABI sweep or a device build.

Direct local `probe-accessors.py` scans at the pinned client revisions returned 309 Kotlin files and
423 Swift/Objective-C files with zero reported violations and three interface methods each. The
Windows Apple checkout materializes tracked symlinks as text; the `SubscriptionQuotaCaption`
`SubscriptionInfo.swift` link points to a missing target. Thus the nominal Swift scan's zero exit
must not be promoted to a complete Swift call-site sweep. The Android result is a static scan, not a
compiled client, ABI compatibility proof or device test.

The v0.1.31 [Apple workflow run](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40606786576)
resolved `3aedd7417488` and its ABI step reported **PARTIAL**: binding checks and gitlink checks
passed, but the Swift accessor probe skipped because a referenced source file was missing. The step's
successful exit is not evidence that the client-call-site sweep passed. Signing preflight also skipped
in unsigned mode; this was not a physical-device validation.

The source-selection checker is wired in the Apple workflow. The separate
`verify-release-acceptance.sh` checker is not invoked by the release path; see `ci-release.md` §5 for
the actual release gates, source-selection history and their distinct evidence. `load_refs` checks
per-platform declarations in the refs file itself before sourcing it; treating caller environment as
the file's declarations previously broke the release helper (`ece0fb9d2`).

### What IS wired for Apple

Packaging/install validity (`apple-libbox-artifact.sh validate`/`install`, `client-apple.yml:282-284`,
`:577-579`), the signing environment (`check-apple-signing-environment.sh` `:160`, `:300`, `:562`),
branding, macOS naming, and source selection. **Signing assertions skip unless
`APPLE_SIGNING_MODE=development`**, and `runtime_validation: NOT EXECUTED` is written unconditionally
(`:378`).

---

## 7. Android

`android-core-arm64.yml` builds the AAR and asserts, in CI: provenance `commit=` == `GITHUB_SHA`
(`:269-273`); `jni/arm64-v8a/libbox.so` present, **> 1 MB**, and `file` reports aarch64 (`:286-301`);
`go version -m` shows no gVisor (soft-fails to a NOTE if unreadable, `:313-315`); the NDK and gomobile
pins are **derived, never copied** (`:167-171`, `:209-242`); the resolved tags are asserted
(`:127-138`). It runs on `push` to `testing` **with a path filter** — so a new top-level directory
can silently drop Android ABI verification.

The `StringBox` migration is why the Android submodule points at the **fork** rather than upstream:
`.gitmodules` explains that the ABI migration changes the generated Java signatures, so the client call
sites must move with the core, and *"the upstream repository is not writable from here"*. Without it a
clean checkout would call the old ABI against a rebuilt libbox and fail to compile.
`-checklinkname=0` is passed for that build via `cmd/internal/mobilebuildtags` (see
`build-pruning-release.md` §2).

## 8. Two interface changes in the historical comparison

| Interface | At `e4b7e692` | At `a267e99` | Commit | Why |
|---|---|---|---|---|
| `DeprecatedNote.Message()` / `MessageWithLink()` | `string` | `*StringBox` | `eef6bafff` | a bare `string` puts a Go pointer in gomobile's packed result frame → `fatal error: bulkBarrierPreWrite: unaligned arguments`, unrecoverable. Recorded in `docs/fork/libbox-abi-contract.tsv`. |
| `adapter.NewAdapterWithDialerOptions` | `(…)` | `(…, serverIsDomain ...bool)` | `ff0040079` | the server-domain fact has to reach the adapter |

**Do not assume upstream's libbox is a drop-in replacement.** The table is not exhaustive for later
revisions: `UseProcFS` was subsequently removed from `PlatformInterface`, with a separate Android
helper retained. Some outbound capabilities are optional interfaces (`ConnectionCopyTuner`,
`CopyBufferGrowthTuner`, `BypassableOutbound`); that does not establish compatibility of every API.

---

## 9. Core guarantees / client obligations

```text
Core guarantees
  - The core classifies platform levels and owns all lifecycle policy. A client reports the
    number its OS gave it; the core decides what it means.
  - A scope-owned resource is torn down by the Scope; a cleanup registered after close runs on
    the registering goroutine.
  - `APIVersion` (currently 4) is served on GetVersion and must be checked by the client.
  - Trim (progressive) never resets the network, dials, wakes suspended work or kills active
    flows. Release (critical) does reset it.
  - A reuse verdict is published before the device level is released.

Client must provide
  - SetupOptions: BasePath, WorkingPath, TempPath, CommandServerListenPort,
    CommandServerSecret, AppVersion, and the platform flags it actually supports.
  - Platform facts, at the granularity the OS gives them: memory trim level, screen on/off,
    lock state, device wake, network change.
  - A pause/resume edge when it observes one — never a level read presented as an edge.

Client must NOT reinterpret
  - A raw trim level's meaning: do not send "please trim"; send the level.
  - A display-on as an unlock.
  - A snapshot read as an edge (`ReconcileScreenState` is not `RecordScreenState`).
  - `libbox.DeprecatedNote.Message()` as a `string`: it returns `*StringBox` so gomobile's
    packed result frame stays aligned.
  - The Apple gitlink as the source used by every build: the workflow resolves and records
    its Apple source separately; the gitlink is also checked against the ABI contract constant.
  - The build-tag set as fixed: which libbox symbols exist depends on it.

Version / ABI coordination
  - `APIVersion = 4` (daemon/started_service.go:39), served by StartedService.GetVersion and
    read by command_client.go:760. A client that does not check it can call a surface that
    does not exist.
  - The Apple geometry tag is read from ONE source: `go run ./cmd/internal/appletags
    -mobile-only`. It must never be hardcoded.
  - The tag-polar registry means the ABI surface is a function of the tag set; a client built
    against one profile is not interchangeable with another.

External repositories
  - clients/apple   Piggy-Cat-bit-shadow/sing-box-for-apple   (build source is resolved separately)
  - clients/android Piggy-Cat-bit-shadow/sing-box-for-android (fork-located because the ABI
                    migration moves the generated Java signatures)
  - clients/desktop SagerNet/sing-box-for-desktop             (upstream's)
  Checkout status varies; see the scope note and record the actual submodule revision.
```

---

## 10. Evidence

| Guarantee | Level | Re-check |
|---|---|---|
| The exported surface and `SetupOptions` | VERIFIED_SOURCE + VERIFIED_UNIT | `go test -count=1 ./experimental/libbox/...` (measured: ok, 20.3 s) |
| The core owns trim classification and coalescing | VERIFIED_SOURCE | `common/runtimecoord/platform_events.go:152-231` |
| The trim ladder is complete and an unknown level is ignored | VERIFIED_SOURCE + VERIFIED_UNIT | `go test -count=1 ./common/runtimecoord/...` |
| `APIVersion = 4` is served and consumed | VERIFIED_SOURCE | `git grep -n APIVersion` |
| The Apple client is one source for iOS and macOS | source and local fixture checks; the selection script passed 72 checks under MSYS2 with bundled Python in this calibration | `bash scripts/ci/check-apple-source-selection.sh`; inspect the actual workflow run |
| Client gitlinks match ABI contract constants | local pin-only script passed; Verify declares the same gate. This does not prove the resolved Apple build SHA equals the gitlink | `./scripts/ci/check-libbox-abi.sh --pin-only`; inspect Apple workflow's resolved source |
| The refs file cannot silently reinstate a two-source pin | VERIFIED_SOURCE + VERIFIED_UNIT | `sed -n '88,105p' scripts/ci/apple-client-source.sh`; assert it greps the **file**, not the environment — `ece0fb9d2` |
| ABI call-site compatibility at the pinned revisions | Android static probe passed locally; Swift nominal scan is incomplete due to symlink materialization / missing target, and the exact-target CI ABI result was PARTIAL | `probe-accessors.py` per client; full gate needs `gobind` and a valid checkout |
| Android AAR provenance and architecture | **UNVERIFIED (declaration only)** | `android-core-arm64.yml` |
| Device behaviour on either platform | **UNVERIFIED** — no device, no SDK, no Xcode | — |

---

## 11. AI cautions

1. **Do not look for `libbox.go`.** The surface is ~85 files.
2. **Do not move level classification into a client.** The core owns the trim ladder, the coalescing
   windows and the edge/level distinction.
3. **Do not default an unknown trim level to a progressive action.** Unknown is ignored.
4. **Do not merge `RecordScreenState` with `ReconcileScreenState`.** One is an edge, one is a level,
   and merging them resets the lifecycle anchor.
5. **Do not treat a display-on as an unlock.**
6. **Do not change `DeprecatedNote.Message()` back to `string`.** It returns `*StringBox` to keep
   gomobile's packed result frame aligned; the alternative is an unrecoverable fatal error.
7. **Do not treat upstream's libbox as drop-in replaceable here.**
8. **Do not hardcode the Apple geometry tag.** Read it from `cmd/internal/appletags -mobile-only`.
9. **Do not put a mobile-only tag in the shared Apple set.** `-tags` reaches macOS in a mixed build.
10. **Do not confuse Apple source selection with the gitlink ABI check.** The workflow freezes the
    resolved source once per build; the separate gitlink/constant check does not select that source.
11. **Do not claim the Apple ABI is verified at the pinned revision.** It is not.
12. **Do not call the ABI gate unwired.** Verify declares its pin-only half and the Apple workflow
    declares the full check; inspect the actual run and resolved source before claiming coverage.
13. **Do not assume `make proto` works.** `protogen` needs a `protoc` the `Makefile` never installs.
14. **Scope client-side claims to a checked-out client SHA and the actual files inspected.** A
    superproject gitlink alone does not prove a client build or device behavior.
15. **Do not forget that `include/*` has zero test files.** The registry and the tombstones are
    unguarded; a change there is caught only by a build.
16. **If you move the Apple pin, the constant must move with it.** `clients/apple`'s gitlink is the
    recorded checkout, and `VERIFIED_APPLE_SHA` in `scripts/ci/check-libbox-abi.sh` is the cross-check.
    `verify.yml` now runs `--pin-only` and fails when the two diverge; `client-apple.yml` runs the full
    gate. The gate tells you they disagree, not which revision was intended: re-derive the
    justification rather than inheriting the previous one, and run layer 4 against the new revision
    (`probe-accessors.py`, or `check-libbox-abi.sh --client-root <checkout>` on a macOS host). The
    v0.1.28 move is the worked example: `6e559a4` -> `3aedd741` changed the quota caption, the profile
    title and the path monitor, no ABI call site, and the constant had to be re-derived to match.
