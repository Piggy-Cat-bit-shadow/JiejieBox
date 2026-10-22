# Mobile platforms: does the half-close release cover iOS and Android?

This is the platform audit of the half-close release (`route/conn_halfclose.go`, recorded in
`half-closed-flow-release.md` and attacked in `half-closed-flow-adversarial-audit.md`). It answers one
question per layer, with file and line evidence rather than "the Go code is shared":

1. which TCP stack and TUN path each mobile platform actually runs;
2. whether the release mechanism is reachable on that path;
3. whether the two clients consume a CLOSED event correctly, and whether their differences can leave a
   row Active.

**Status of the device evidence: `NOT_VERIFIED_ON_DEVICE` for BOTH platforms.** This host has no iOS
device and no Android device. Everything below is source audit plus tests that run on this host, and
the two platforms are marked separately rather than one standing in for the other. The one field
observation this work is based on is iOS.

## 1. The path each platform actually runs

| Layer | iOS | Android | Desktop (control) |
| --- | --- | --- | --- |
| TUN device | `NEPacketTunnelProvider` packet-flow fd, handed to the core (`Library/Network/ExtensionPlatformInterface.swift:217`, `:845`) | `VpnService.Builder` fd (`app/src/main/java/io/nekohasekai/sfa/bg/VPNService.kt:58`) | whatever the profile configures (TUN, Mixed, system proxy) |
| TUN stack selection | the core parses the profile; `protocol/tun/inbound.go:713` is the only `tun.NewStack` call in production code | same line | same line |
| Default stack | `""` → sing-tun `NewGo` | `""` → sing-tun `NewGo` | `""` → sing-tun `NewGo` |
| gVisor availability | not shipped (no `with_gvisor` in the mobile tag set, `cmd/internal/mobilebuildtags/tags.go:60`; the Apple lists do not add it) | not shipped, and CI asserts it: `.github/workflows/android-core-arm64.yml:132` | not shipped by default |
| Inbound TCP conn type | `*tun.GoConn` (userspace stack) | `*tun.GoConn` | `*tun.GoConn` for TUN; `*net.TCPConn` for Mixed / system proxy |
| Connection events | daemon gRPC `SubscribeConnections` → `experimental/libbox` → same `Connections` map as Android | same | same (`dashboard/src/api/daemon.ts:255`) |
| "Active" rule | `ConnectionListViewModel.swift:170` (`closedAt > 0` ⇒ closed) → `HakoConnectionView.swift:143` (`nil` ⇒ "Active") | `compose/model/Connection.kt:102` (`closedAt > 0` ⇒ closed), `:50` (`isActive`) | `dashboard/src/api/daemon.ts:294` (`closedAt` > 0 ⇒ closed, else `Date.now()`) |

Two platform differences exist and neither touches close semantics: the MTU default
(NetworkExtension 4064, Android 9000, desktop 65535 - `protocol/tun/inbound.go:288-296`) and
`EXP_MultiPendingPackets` on Darwin. The stack itself is chosen from the profile's `stack` field on
every platform, so "the platform" never selects a different TCP implementation.

**Consequence.** The conn type the mechanism sees is decided by `stack`, not by the OS. Both mobile
artifacts ship without gVisor, so `stack: "gvisor"`/`"mixed"` is an error at TUN startup (the gVisor
stub), and the reachable stacks are:

| `stack` | iOS | Android | Desktop | Inbound TCP conn | Half-close release |
| --- | --- | --- | --- | --- | --- |
| `""` / `"go"` (default) | yes | yes | yes | `*tun.GoConn` | **covered** |
| `"system"` | if a profile asks | if a profile asks | if a profile asks | kernel socket | **not covered** - counted `skipped (unobservable read side)` |
| `"gvisor"` / `"mixed"` | unavailable (not shipped) | unavailable (not shipped) | unavailable by default | - | - |

That is exactly the boundary `route/conn_halfclose.go` states, now with the platform facts behind it.

### The DIRECT splice path, per platform

The half-close observer is started by a copy direction; a handed-over DIRECT TCP stream has no copy
direction. Which platforms can hand over is therefore a coverage question, not a detail:

| Platform | TUN | `supportsSockets()` | DIRECT TCP handover | Proxy handover |
| --- | --- | --- | --- | --- |
| iOS | NetworkExtension fd → `*tun.NativeTun` | true (`stack_go_io_darwin.go`) | **yes** (tracker's `SyscallConn` resolves to `*net.TCPConn`) | never (proxy stream is not a `syscall.Conn`) |
| Android | VpnService fd → `*tun.NativeTun` | true (`stack_go_io_linux.go`) | **yes** | never |
| Desktop | TUN → `*tun.NativeTun` | true (windows/darwin/linux) | **yes** | never |

So the DIRECT-only symptom the field report describes is the same on all three platforms, and it is
the reason `route/splice.go` now arms the stack's keepalive right after a confirmed handover (idle 2m,
interval 30s, count 3 → 3.5 minutes; `liveness_armed` in the TCP handover line proves it ran). The
memory TUN used by the tests cannot hand over at all (`supportsSockets() == false`), which is why the
handover step is `BLOCKED_BY_ENVIRONMENT` here and the arm itself is tested on the real userspace
stack instead.

## 2. Answers

**Do iOS and Android run the same TCP stack, TUN path and close behaviour?**
Yes for the shipped configuration. Both hand a TUN fd to the same core, both resolve `stack` through
the same single `tun.NewStack` call, both default to the userspace stack (no gVisor in either
artifact), and both run the same copy loops, `trafficcontrol` manager and `route.ConnectionManager`.
The differences are MTU and a Darwin packet option, not close behaviour. The one way to run a
different stack is a profile that sets `stack: "system"` - identical risk on every platform, not a
mobile difference.

**Does `route/conn_halfclose.go` cover both platforms' real path?**
Yes for the path the phones actually run: the finished side is a `*tun.GoConn` on both, which is the
type the observability check accepts (measured: its `Read` reports the terminal state after EOF). It
is **not** a claim about every possible profile: a `stack: "system"` profile produces kernel sockets
and is skipped, and the skip is counted in the shutdown line so a field report cannot mistake it for
coverage. Nothing in the mechanism branches on `GOOS`; what it branches on is the connection's read
semantics.

**After `trafficcontrol` removes the row, do both clients consume CLOSED and update the list?**
They share the consumer: the daemon's `SubscribeConnections` stream feeds `experimental/libbox`'s
`Connections` map on both, and both render `ClosedAt == 0` as Active. So the answer is one
implementation, tested once (`experimental/libbox/connection_events_test.go`), plus the daemon-side
producer (`daemon/connection_events_test.go`). The clients' own layers only translate that struct.

**Mobile-specific subscription interruption, reconnect snapshot, ordering, revival, caching?**
All four exist as *mechanisms* on the phones, and none of them can leave a row permanently Active:

| Mechanism | iOS | Android | Why it cannot fabricate an Active row |
| --- | --- | --- | --- |
| subscription suspension | `CommandClient.suspend()` on background (remote only), `isActiveConnection()` drops events from the old client | coroutine scope of the ViewModel, `connectionsGeneration` drops stale deliveries | a dropped event can only *delay* the update; the 1s reconcile re-announces the close |
| reconnect snapshot | store persists; the new subscription's first message is sent with `Reset: true` (`daemon/started_service.go:985`) | store is nulled on `onDisconnected` and rebuilt | Reset clears the map, so rows the new snapshot does not carry are gone (`TestConnectionResetDropsPreResetIDs`, and the reconnect test below) |
| event ordering | events are applied on the main queue, so ordered | `writeConnectionEvents` launches a coroutine per batch on `Dispatchers.Default`; the mutex serialises but does not order them | CLOSED always wins and UPDATE never creates or reopens an entry (`TestConnectionUpdateAfterCloseCannotReactivate`), and the reconcile repairs a lost close within 1s |
| stale cache | the last list stays published while disconnected | the store is cleared on stop | a stale list shows *old states*, and the next Reset replaces it; it cannot turn a closed row into an active one |

One asymmetry found and deliberately not "fixed": `libbox`'s CLOSED branch trusts the event's
`ClosedAt`, while the desktop dashboard falls back to `Date.now()` when it is zero
(`dashboard/src/api/daemon.ts:294`). A zero timestamp would therefore produce an Active row in both
phone clients. It is **unreachable from this daemon**, which sets the timestamp at all three
producers: `trafficcontrol` writes it into the closed copy
(`common/trafficcontrol/manager.go:106`), the event translator falls back to `time.Now()` when the
event and its metadata both lack it (`daemon/started_service.go:1102`, asserted by
`TestStreamingClosedAfterReconcileNeverResurrects`, which drives a CLOSED event with no timestamp at
all), and the reconcile does the same
(`daemon/started_service.go:1194-1201`). Changing the client would be speculative; the finding is
recorded here so that a future producer change that starts sending zero is caught by review, and the
test suite already pins the producer's fallback.

**Do both platforms have a reproducible "half-closed, peer vanished, row stays Active" path?**
The *mechanism* is identical and was present on both before this work: both run the userspace stack by
default, both leave the finished side unobserved, and both render `ClosedAt == 0` as Active. The field
report is iOS only; that is a difference in who observed the symptom, not in the code path. Android's
lack of a screenshot is not evidence either way, and is recorded as unverified.

**If only one platform had it, what would the root cause be?**
Not applicable here: the shared path is the same. The difference that *could* produce a single-platform
outcome is the profile's `stack` (a `system`-stack profile on one platform and a Go-stack profile on
the other), which is a configuration difference, not a platform TCP-stack difference. That is why the
skip counter is part of the device instructions: it separates "the mechanism did not fire" from "the
mechanism was never armed for this inbound".

## 3. Tests added or reused for this audit

| Test | Layer | What it proves for the mobile question |
| --- | --- | --- |
| `TestConnectionReconnectSnapshotDropsRowsAndStaleUpdatesCannotRevive` (new) | libbox (shared by both clients) | a reconnect Reset snapshot drops rows it no longer carries, and a buffered UPDATE or a late CLOSED from the old subscription cannot recreate or reopen them |
| `TestConnectionResetDropsPreResetIDs` | libbox | the Reset snapshot is the whole truth |
| `TestConnectionUpdateAfterCloseCannotReactivate`, `TestConnectionClosedEventBeatsEveryEarlierState`, `TestConnectionClosedEventNeverProducesAnActiveRow` | libbox | CLOSED wins over every earlier state; an UPDATE can never reopen |
| `TestConnectionFilterByStateNeverShowsAClosedRowAsActive` | libbox | the default Active filter is exactly "no ClosedAt" - the rule both clients render |
| `TestInitialConnectionStateCarriesActiveAndClosed`, `TestReconcileEmitsClosedForAGoneConnection`, `TestStreamingClosedAfterReconcileNeverResurrects` | daemon | the producer's half: the snapshot carries ClosedAt for history, the reconcile announces closes, a CLOSED event always has a usable timestamp |
| `TestTUNHalfClosedPeerGoneClosesTheTransport`, `TestTUNHalfClosedReleaseIsReportedAtShutdown` | route, real userspace stack | the mechanism fires and the peer-visible socket closes on the stack both phones run |
| `TestHalfCloseKernelSocketIsSkipped` | route | the `stack: "system"` path is skipped and counted, not silently "covered" |
| `TestTrafficTrackerHalfClosedKernelPeerIsNotObservable` | e2e | the kernel-socket limit at the traffic table, i.e. the desktop Mixed / system-proxy shape |

Commands (tags `<DEFAULT_BUILD_TAGS_OTHERS>`, and `-race` where noted):

```
go test -run TestConnection ./experimental/libbox -count=1        ok
go test ./daemon -count=1                                         ok
go test ./route -run TestHalfClose -count=1                       ok
go test ./route ./protocol/tun ./e2e ./daemon ./experimental/libbox -count=1   ok
go test -race ./route ./daemon ./experimental/libbox -count=1     ok
```

## 4. Not verified on device

`NOT_VERIFIED_ON_DEVICE` for **both** platforms, separately:

- **iOS**: the same shutdown line as the core record, read in the extension's log after the tunnel
  stops. It proves the mechanism fires on the userspace stack the phone runs. Not done here: no device.
- **Android**: the same line, from the app's log after stopping the tunnel. It proves the same on the
  Android build of the same core. Not done here: no device.
- Per platform, the line to read is
  `half-closed flows: N observed, released N (peer reset N, keepalive timeout N), stopped N, pending 0; skipped N (no probe capability N, unobservable read side N)`,
  with `observed == released + stopped + pending`, `pending 0` at shutdown, and
  `skipped (unobservable read side)` non-zero only if a `stack: "system"` profile was in use.

## 5. Residual risks

1. A profile that sets `stack: "system"` (any platform) is outside the mechanism. Counted, not hidden.
2. Android's event application is not order-preserving by construction; correctness rests on CLOSED
   winning plus the 1s reconcile, both tested. A future change that makes UPDATE authoritative would
   break it silently for a second or two, not permanently.
3. The iOS store persists across a teardown by design; it relies on the daemon always sending the
   first message of a subscription with `Reset`. That is one line
   (`daemon/started_service.go:985`) with a libbox-side test, but no daemon-side stream test (the
   function needs a gRPC server to drive).
4. Nothing here was observed on a phone; the field evidence for Android remains absent.
