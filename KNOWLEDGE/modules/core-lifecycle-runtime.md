# Core lifecycle and runtime coordination

**Scope:** `adapter.Scope`, `Box` start/close ordering, `common/runtimecoord`, `route.ReferenceManager`,
`common/power`'s interaction with them.

**Provenance:** `common/runtimecoord` and `common/power` are **fork-created packages** (absent at
`e4b7e692`). `adapter/lifecycle.go`, `box.go`, `box_lifecycle.go` and `route/reference.go` are
heavily modified. Fork commits touching these paths: **85** (`adapter` 77, `common/runtimecoord` 8).

**Load this module when changing:** start/close ordering, teardown, network generations, rebuild
leases, idle retirement, memory trim, or anything that reacts to a network transition.

---

## 1. `adapter.Scope` — the ownership primitive

`adapter/lifecycle.go` (297 lines). Everything that must be torn down is registered here; nothing
else runs a component's `Close`.

| Element | Anchor | Rule |
|---|---|---|
| State ladder | `lifecycle.go:70-80` | `scopeOpen` → `scopeClosing` → `scopeClosed` |
| `Scope.Add(cleanup)` | `lifecycle.go:112-138` | **If the scope is already closing, the cleanup runs synchronously on the *caller's* goroutine.** Registration is never silently dropped. |
| `Scope.Start(name, component, stage)` | `lifecycle.go:147-183` | Creates a child scope keyed by the **component**, registers the child's `Close` as a cleanup, runs `component.Start` **outside the lock**, then re-reads the child context and returns `E.Cause(ctxErr, …)` so a start cancelled mid-flight **reports failure, not success**. |
| `Scope.Close` | `lifecycle.go:186-217`, drain at `:251` | First caller takes the queue and drains in **reverse registration order** (`slices.Backward`); later callers **join** via `closeDone`; the result filters `E.IsClosed`/`E.IsCanceled` but logs them. **Not reentrant on the same Scope** (`:214-217`). |
| Stages | `lifecycle.go:21-35` | `StartStateInitialize → StartStateStart → StartStatePostStart → StartStateStarted` |

### The completion boundary — state it correctly

`Scope.Close()` returning means **exactly** this and nothing more:

* every cleanup already in the queue has run and `closeErr` is final;
* it does **not** wait for a `component.Start()` that is running and has not yet reached `Add()` —
  nothing bounds how long a `Start` may take, so waiting would make `Close` unbounded;
* a cleanup registered after that point runs synchronously inside `Add()`, on the registering
  goroutine — a leak is prevented, but the late resource is **not** part of what `Close` reported;
* an error from such a late cleanup is logged by `Add()` and folded into nothing; `closeErr` is
  already final, so a later `Close` does not pick it up either.

Pinned by `adapter/lifecycle_close_boundary_test.go` (`TestScopeCloseDoesNotWaitForAnInFlightStart`,
`TestScopeCloseDoesNotFoldInALateCleanupError`, `TestScopeCloseJoinsAnAlreadyStartedDrain`,
`TestScopeCloseDoesNotWaitForAStartThatNeverRegistered`).

Registration inventory: **219 production `scope.Add` sites**. Exactly **five** close a `*Scope`, and
every one closes a *nested child* the component created for itself (`protocol/openvpn`,
`protocol/openconnect`, `protocol/tailscale`, `service/resolved`, `dns/transport/local`). No
production cleanup closes the scope it was registered on — that would deadlock.

---

## 2. `Box` start and close ordering

### Start (`box.go:1022-1170`)

`preStart()`:
1. `logFactory.Start()` (`:1025`)
2. `scope.Add(runtimeCoordinator.Close)`, then `scope.Add(logFactory.Close)` (`:1033-1034`) —
   **registered first ⇒ closed last**
3. owned `HistoryStorage.Close` if `Box` created it (`:1042-1048`)
4. `applyDebugOptions`, debug HTTP server (`:1049-1056`)
5. `startComponents(Initialize)` — **httpClient → network → dns-transport → dns-router →
   connection → router → outbound → endpoint → certificate-provider → inbound → service**
   (`:1061-1073`)
6. `startComponents(Start)` — **outbound → dns-transport → network → connection → httpClient →
   router → dns-router** (`:1077-1085`)

`start()` (`:1092-1170`) then: `startInternalServices(Start)`, NTP, `startComponents(Start)` =
**endpoint → cert-provider → inbound → service** (`:1110-1115`), `PostStart` over **all** components
(`:1119-1130`), `startInternalServices(PostStart)`, `Started` over all (`:1138-1149`), and finally
`s.network.EstablishInitialNetworkEnvironment()` (`:1166-1168`).

> **The Box is not "up" until the network manager has observed its network.** `Start` does not return
> before that call has run **synchronously**. State it precisely: `Start` returns only after the manager
> has synchronously observed its network **or established that there is nothing to observe** — the call
> is guarded by `s.network != nil` and is a no-op when the transition does not apply or when nothing
> changed (already observed, or no default interface). A future change that moves this earlier makes the
> first routed flow race the network state it should have seen.

Two orderings that look arbitrary and are not:

* **Initialize and Start use different orders.** `httpClient` is initialized *first* and started
  *fifth*; `outbound` is initialized *seventh* and started *first*. Validation needs the graph,
  runtime needs the transports.
* **`router.Initialize` only *compiles*.** A rule's `Start()` — the first point at which a rule may
  open a rule-set file or start a process reader — happens at **PostStart** (`route/router.go:237-250`).
  Every start-time validation therefore runs before any rule has been started.

### Close (`box.go:1172-1225`)

`closeOnce.Do` → `statusView.Disconnect()` (`:1210`) → unregister pause callback (`:1213-1216`) →
**close the power governor** (`:1217-1220`) → `scope.Close()` (`:1224`).

> The governor is closed **before** the scope because `ReferenceManager` reads it. Reversing the two
> closes a resource whose observer is already gone.

---

## 3. `common/runtimecoord` — one generation, identity-based leases

`common/runtimecoord/coordinator.go` (631 lines) plus `platform_events.go` (507 lines).

### The objects

| Object | Anchor | Meaning |
|---|---|---|
| `Coordinator` | `coordinator.go:36-46` | `parent`, `access`, `epoch` (starts 0 = "no reset yet"), `changed chan struct{}`, `closed`, `registrated` |
| `Advance(epoch)` | `:80-104` | **Monotone**: `epoch <= c.epoch` is a silent no-op (`:85`). Closes and *replaces* the change channel, snapshots registrations, then notifies **outside the lock** (`:98-103`). |
| `GenerationChanged()` | `:142-149` | Returns `(epoch, channel)` under one lock, so a caller cannot miss a transition between reading the number and waiting on the channel. |
| `Register(label)` | `:156-185` | A registration taken on an already-moved generation records the current epoch **and** `observed = true` (`:170-173`). |
| `Registration` | `:191-223` | Holds `epoch`, `observed`, `lease *RebindLease`, `nextLease`, `lastRebind`, `rebindCount`, `window`. |
| `RebindLease` | `:230-238` | `id`, `generation`, `reason`, `ctx`, `cancel`, `expired`. |
| `Expired()` | `:282-289` | True if expired, **or the registration is closed, or the lease is no longer the current owner**. |
| `Complete()` | `:295-310` | A **no-op for a superseded lease**, but it **always cancels**. |
| `observeEpoch` | `:355-381` | Moves epoch, clears `observed`, supersedes + cancels the in-flight lease, and **clears `lastRebind`** so a new network gets an immediate retry (`:375`). |
| `ScheduleRebind`/`BeginRebind` | `:409-465` | Coalescing window defaults to `adapter.RecoveryWindow` (`:437-440`), armed at **grant** time, not completion (`:444-446`). |
| `Stale()` | `:499-518` | True when closed, when `!observed`, or when the coordinator epoch > this epoch. |
| `Acknowledge()` | `:526-536` | The only thing that turns "epoch moved" into "this resource is current". |
| Probe origin | `:583-631` | `ProbeForeground` vs `ProbeAutomatic`, and `MeasurementContext` — decides whether a measurement may **wake a suspended resource**. |

### The invariant that the wording must not overstate

> **"One logical recovery at a time" is a lease *identity*, not a boolean.**

`Registration.lease` is a `*RebindLease` precisely so that a *superseded* lease cannot complete or
release a newer one. A future change that replaces it with a `bool` reintroduces the bug the pointer
exists to prevent. Rationale is written at `coordinator.go:202-215`.

### `TrimMemory` and `ReleaseMemory` are different

`TrimMemory` does **not** advance the network generation. `ReleaseMemory` **does**. The coordinator's
`Trim` path (`5f56f02c`, `09964183`, `93823c3d`) and the release/reset path are separate entry points
by design.

---

## 4. `route.ReferenceManager` — the retirement inventory

`route/reference.go` (`ReferenceManager` at `:21-42`, built at `box.go:641`).

### What it does, in order

`Start` runs only at `StartStateStarted` (`:98-101`): creates the `observable.Subscriber` (`:102`),
hooks `urltest.HistoryStorage` and `clashmode.Manager` (`:104-111`), seeds `devicePaused` (`:112-114`),
runs `update()`, starts `m.loop()` (`:115-116`), registers the pause callback (`:117-133`), registers
`powerGovernor.AddReuseObserver(m.onReuseBoundary)` (`:142-144`), and **last** sets
`m.closed.Store(true)` so it is the **first** cleanup to run (`:145-150`).

Reference closure (`update()`, `:211-361`):

* static seeds: NTP detour (`:49-51`), every outbound/endpoint `domain_resolver` (`:52-57`);
* DNS-rule references (`collectDNSRuleReferences`, `route/reference_rule.go:140-168`); if **no rule is
  terminal**, the manager's default transport is added (`:228-232`);
* route-rule references (`collectRuleReferences`, `route/reference_rule.go:105-138`);
* on-demand endpoints are separated and kept out of the outbound queue (`:240-248`);
* `adapter.Referrer` is walked **transitively** for inbounds, services, the HTTP client manager, DNS
  transports and outbounds (`:249-302`);
* then the `IdleConnectionKeeper` decisions (`:306-358`) and the unreferenced-transport reset
  (`:333-358`).

### The rule that cost four review passes

`retireIdleResources()` (`:440-494`) covers outbounds, endpoints, DNS transports **and the HTTP
client manager**. The assertion is **`adapter.IdleConnectionReleaser`**, *not* the bundled
`IdleConnectionKeeper`. The comment at `:479-484` records why: asserting the bundled interface
silently failed at run time on **every** walk, so the HTTP client pools were unreachable through four
passes of review. Commits `93823c3d` and `8735b1d` are the fix.

> **When adding a pool to the retirement walk, implement and assert `IdleConnectionReleaser` — a
> full `IdleConnectionKeeper` is not required and its absence is not a defect.**

That is deliberate, not a gap: some transports have no safe "no new stream" state, so they cannot be
reuse-drained locally and only offer a destructive close.

### `TrimIdleResources` vs `CloseIdleConnections` vs `RetireSuspect`

| Entry point | Anchor | What it may do |
|---|---|---|
| `TrimIdleResources()` | `:412-414` | Identical body to `CloseIdleConnections()` today; the **distinction belongs to the caller** (`:394-411`) |
| `CloseIdleConnections()` | `:416-418` | Same body |
| `retireSuspectResources()` | `:512-537` | Calls `adapter.ReuseSuspect.RetireSuspect()` **first** (`:496-511`), then `CloseIdleConnections` (`:547-556`) |
| `onReuseBoundary` | `:154-197` | **Does not reset the network, does not terminate a stream, does not touch on-demand tunnels, does not dial.** |

`route/reusable_owner_inventory_test.go:49,96,117` forbids a **second** `ReuseSuspect` pool, so
"add a new reusable pool" is not an available move without changing that test.

---

## 5. Evidence, provenance, cautions

### Evidence

| Guarantee | Level | Re-check |
|---|---|---|
| Scope cleanup-after-close runs on the caller's goroutine | `VERIFIED_SOURCE` + `VERIFIED_UNIT` | `go test -count=1 ./adapter/...` |
| `Close` does not wait for an in-flight `Start` | `VERIFIED_UNIT` | `adapter/lifecycle_close_boundary_test.go` |
| Epoch monotonicity, lease supersession, `Stale`/`Acknowledge` | `VERIFIED_UNIT` | `go test -count=1 ./common/runtimecoord/...` |
| Retirement walk reaches all four pools | `VERIFIED_UNIT` | `route/reference_coverage_stress_test.go`, `route/reusable_owner_inventory_test.go` |
| Ordering pins (reuse verdict before level release; sleep edge before level pause) | `VERIFIED_UNIT` | `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run 'TestTheReuseVerdict\|TestTheSleepEdge' .` |
| 100 sleep/wake cycles, 100 network transitions, no resurrection after Close | `VERIFIED_UNIT` (no `-race`) | `-run 'TestOneHundred\|TestNoResurrectionAfterClose' .` |
| Race-freedom | **`BLOCKED_BY_ENVIRONMENT`** — `-race` needs cgo and this host has no C compiler | Linux/macOS or Windows+gcc |
| Device behaviour behind any of this | **`UNVERIFIED`** — no TUN, no real NetworkExtension | `docs/RELEASE-CERTIFICATE.md` §1 |

### Provenance

| Commit | Type | Origin | What it did |
|---|---|---|---|
| `8c7b230d` | LIFECYCLE | OWN | adds the resource lifecycle coordinator |
| `5f56f02c` | MEMORY | OWN | trim idle resources when memory is elevated but not critical |
| `09964183` | FIX | OWN | stops the memory trim from building a second transport |
| `93823c3d` | MEMORY | OWN | retires the post-wake pools that were unreachable; drains the ones that can |
| `8735b1d` | MEMORY | OWN | reaches the HTTP client pools from the retire walks, which never did |
| `e4327dca` | LIFECYCLE | OWN | gives the Apple device axis a real publisher and a real release |
| `ae197b92` | FIX | OWN | fences a superseded interface monitor; publishes outside the transition lock |
| `0ff90482` | FIX | OWN | spends the device resume edge on an edge latch, not on the screen level |
| `9dd91d51`, `482e4d3a` | HARDENING | OWN | scope ownership hardening |
| `7e13f461` | FIX | **OWN, not upstream** | clears the process-global interface getter **its owner registered**. The commit body states the fork's own refactor moved the registration into `newSystemBinding` and **dropped it**; this is a fix to a fork-introduced leak, not an upstream absorption. |
| `27d8bb59`, `12ee55eef` | REPOSITORY-HYGIENE | OWN | ancestry records made only to clear GitHub's stale "behind upstream" state. **Both are no-tree-change merges** (`12ee55eef` records `PRE_SYNC_TREE == post-merge tree`). |
| `813ddfa98`, `03de88adb`, `0d91c4547`, `16c898a82` | — | UPSTREAM-INHERITED | upstream objects the fork carried (see `BASELINE.md` §provenance) |

### AI cautions

1. **Do not "fix" `Scope.Add` running a late cleanup synchronously.** Making it asynchronous is how a
   resource ends up alive after `Close` returned with no error.
2. **Do not make `Scope.Close` wait for in-flight `Start`s.** It is unbounded by construction; the
   two components that needed it (`protocol/masque/server.go`, `protocol/tun/inbound.go`) got a
   non-blocking handoff (`startupGate`, `acquiredStillOwned`) instead. Copy that shape.
3. **Do not register a cleanup that closes the scope it was registered on.** Five cleanups close
   scopes; all five close a nested child.
4. **Do not replace `Registration.lease *RebindLease` with a boolean.** The pointer exists so a
   superseded lease cannot release a newer one.
5. **Do not treat "epoch advanced" as "resource rebuilt"** — `Stale()` and `Acknowledge()` are two
   different states.
6. **Do not assert `IdleConnectionKeeper` where only `IdleConnectionReleaser` is implemented.** That
   exact mistake silently disabled the whole retirement walk.
7. **Do not move `EstablishInitialNetworkEnvironment()` earlier** in `Start`.
8. **Do not reorder the close sequence** (status view → pause callback → governor → scope).
9. **Do not make `TrimMemory` advance the network generation**, and do not make `ReleaseMemory` stop
   doing so.
10. **Do not add a second `ReuseSuspect` pool** — `route/reusable_owner_inventory_test.go` forbids it.
11. **Do not treat `27d8bb59`/`12ee55eef` as code changes.** Both are ancestry-only merges; reading
    them as features inverts their purpose.
12. **Do not cite `verify-evidence/` for any of this.** Those logs name `cfc77dc5`, 101 commits behind
    the release baseline, and are stale by their own `README.md` rule. See `ci-release.md`.
