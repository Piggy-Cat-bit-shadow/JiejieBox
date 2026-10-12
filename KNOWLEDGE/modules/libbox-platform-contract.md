# libbox and the platform contract

**Scope:** `experimental/libbox/*`, `daemon/*`, `include/*` as they define the client-facing surface,
`cmd/internal/{mobilebuildtags,applebuildtags,appletags}`, `release/apple-client-refs.env`, and the
Apple/Android artifact pins.

**This module records the CORE side of the contract only.** `clients/apple`, `clients/android` and
`clients/desktop` are git submodules, empty in a clean worktree, and are **not auditable from this
repository**. Where a statement depends on client source, this module says so.

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
(`UnderNetworkExtension :15`, `IncludeAllNetworks :16`, `UseProcFS :24`, `UsePlatformShell :25`,
`UsePlatformBridge :31`, `UsePlatformAutoRedirect :33`).

> **Accurate wording: the core owns lifecycle and memory classification and the actions they earn; the
> platform owns network cost/type classification and its own capabilities.** Saying "the platform only
> reports facts" as a global invariant of `PlatformInterface` is false.

Two of the platform's judgements are **reconciled** rather than trusted:
`handover_coalescing_test.go:234` (a metered source must agree with `UpdateDefaultInterface`) and
`:276` (a metering change alone must not claim a network generation).

**One core-side admission of a *client-side* double-driver, quoted and `UNVERIFIED` here:**
`experimental/libbox/platform_events.go:253-260` records that Android also drives `Pause`/`Wake` from
`ACTION_DEVICE_IDLE_MODE_CHANGED`, and that mixing the two *"would let a Doze wake lift a pause the
screen fact still asserts, with neither path knowing."* `clients/android` is empty here, so this
cannot be checked from this repository.

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

### The design is deliberate and good

`release/apple-client-refs.env` declares the `clients/apple` **gitlink** as the **single authority**:

* the refs file is **forbidden** from recording a SHA (`check-apple-source-selection.sh:124-136`);
* the workflow body is **forbidden** from hardcoding one (`:209-210`);
* `APPLE_CLIENT_BRANCH=main` is **informational only** — *"It never selects which commit is built: the
  gitlink does that, so a branch that moves cannot move a release."*
* `scripts/ci/apple-client-source.sh sha ios` and `sha macos` both return the gitlink, and
  `assert-shared-source` fails the build if they ever differ (both jobs call it:
  `client-apple.yml:238-239`, `:503-504`);
* the rationale is in the file's own header: the two Apple products come from **one** source tree at
  **one** commit with **one** Libbox framework, and the parent decides **only** which commit that is.
  Presentation routing (iPhone → the fork's Hako presentation, iPad and macOS → upstream's) is a
  runtime property of the Apple source, so **the parent must not select a commit per platform**. An
  earlier revision pinned macOS to an independent `dev` commit; that model is gone.

`check-apple-source-selection.sh` **is wired** — `client-apple.yml:134`.

### The gap

`check-apple-source-selection.sh` **cannot** detect the pin moving to an unintended revision. Every one
of its checks is **self-referential to the gitlink**: they prove iOS and macOS *agree with each other*
and forbid a second copy. They cannot say the revision is the *intended* one.

The last cross-check against an intended revision was the `VERIFIED_APPLE_SHA` constant in
`scripts/ci/check-libbox-abi.sh`, and:

* the constant no longer matches the gitlink (`90a0e9ae…` vs `d0524912…`), so the script's own layer 5
  would fail;
* **nothing invokes that script** — not a workflow, not another script, not a `Makefile` target;
* `c7014ddc1` ("fix(apple): align CI branding and iOS target") is proof by demonstration: it moved the
  gitlink 33 minutes before the release commit and touched only `client-apple.yml` + `clients/apple`.
  **Nothing objected.**
* the released revision `d052491215144f209b4726bbcf374649881defa3` appears in **no** tracked non-`.git`
  file, so the tree the v0.1.6 Apple products were built from is recorded nowhere;
* `client-apple.yml` is **not** in `verify-release-acceptance.sh`'s required set (`verify.yml`,
  `interop-xray.yml`, `server-linux-amd64.yml`), and `verify.yml` does not run the ABI gate — so release
  acceptance is **structurally incapable** of noticing the drift.

> **Correct statement: "the Apple pin has no wired verifier, and the one candidate verifier is both
> unwired and stale."** Do **not** write "the Apple ABI gate is broken" — that implies a gate that runs.
>
> **What is therefore `UNVERIFIED` for v0.1.6:** ABI call-site compatibility of the Swift/Kotlin clients
> against the libbox surface **at the pinned revision**. The recorded PASSes are at
> `bb5309b4a`/`90a0e9ae` — two pin generations earlier — and do not transfer. The repository says so
> itself: `docs/fork/apple-artifact-verification.md:609-616` records that whether the pinned revision
> contains the migrated call sites "is NOT verified here", and that the gate which would answer it
> "needs a client checkout".

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
(`:127-138`). It runs on `push` to `testing` **with a 19-path filter** — so a new top-level directory
can silently drop Android ABI verification.

The `StringBox` migration is why the Android submodule points at the **fork** rather than upstream:
`.gitmodules` explains that the ABI migration changes the generated Java signatures, so the client call
sites must move with the core, and *"the upstream repository is not writable from here"*. Without it a
clean checkout would call the old ABI against a rebuilt libbox and fail to compile.
`-checklinkname=0` is passed for that build via `cmd/internal/mobilebuildtags` (see
`build-pruning-release.md` §2).

## 8. The two deliberate upstream-interface breaks

| Interface | At `e4b7e692` | At `a267e99` | Commit | Why |
|---|---|---|---|---|
| `DeprecatedNote.Message()` / `MessageWithLink()` | `string` | `*StringBox` | `eef6bafff` | a bare `string` puts a Go pointer in gomobile's packed result frame → `fatal error: bulkBarrierPreWrite: unaligned arguments`, unrecoverable. Recorded in `docs/fork/libbox-abi-contract.tsv`. |
| `adapter.NewAdapterWithDialerOptions` | `(…)` | `(…, serverIsDomain ...bool)` | `ff0040079` | the server-domain fact has to reach the adapter |

**Upstream's libbox is not drop-in replaceable in this fork.** Everything else is additive: new
capabilities are **optional interfaces embedding `Outbound`** (`ConnectionCopyTuner`,
`CopyBufferGrowthTuner`, `BypassableOutbound`), so upstream implementations still satisfy the
originals.

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
  - The Apple client's gitlink as "just a pin": it is the release's only record of which
    revision the products were built from.
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
  - clients/apple   Piggy-Cat-bit-shadow/sing-box-for-apple   (gitlink is the single authority)
  - clients/android Piggy-Cat-bit-shadow/sing-box-for-android (fork-located because the ABI
                    migration moves the generated Java signatures)
  - clients/desktop SagerNet/sing-box-for-desktop             (upstream's)
  None is auditable from this repository; all three are empty in a clean worktree.
```

---

## 10. Evidence

| Guarantee | Level | Re-check |
|---|---|---|
| The exported surface and `SetupOptions` | VERIFIED_SOURCE + VERIFIED_UNIT | `go test -count=1 ./experimental/libbox/...` (measured: ok, 20.3 s) |
| The core owns trim classification and coalescing | VERIFIED_SOURCE | `common/runtimecoord/platform_events.go:152-231` |
| The trim ladder is complete and an unknown level is ignored | VERIFIED_SOURCE + VERIFIED_UNIT | `go test -count=1 ./common/runtimecoord/...` |
| `APIVersion = 4` is served and consumed | VERIFIED_SOURCE | `git grep -n APIVersion` |
| The Apple client is one source for iOS and macOS | VERIFIED_SOURCE (gate declaration) | `bash scripts/ci/check-apple-source-selection.sh` |
| The Apple **pin cannot move without objection** | **VERIFIED_GIT that no wired mechanism exists** | `git ls-tree HEAD clients/apple`; `git grep VERIFIED_APPLE_SHA= -- scripts/` — **KNOWN GAP** |
| ABI call-site compatibility at the pinned revision | **UNVERIFIED** | `bash scripts/ci/check-libbox-abi.sh` (needs a client checkout + `gobind`) |
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
10. **Do not add a second copy of the Apple client SHA.** The gitlink is the single authority by design
    — and note that this very design is why the pin has no verifier. If you want a verifier, **wire
    one**; do not reintroduce a duplicate constant that nothing reads.
11. **Do not claim the Apple ABI is verified at the pinned revision.** It is not.
12. **Do not describe `check-libbox-abi.sh` as a gate.** Nothing runs it.
13. **Do not assume `make proto` works.** `protogen` needs a `protoc` the `Makefile` never installs.
14. **Do not claim client-side behaviour.** The client repositories are not in this worktree.
15. **Do not forget that `include/*` has zero test files.** The registry and the tombstones are
    unguarded; a change there is caught only by a build.
