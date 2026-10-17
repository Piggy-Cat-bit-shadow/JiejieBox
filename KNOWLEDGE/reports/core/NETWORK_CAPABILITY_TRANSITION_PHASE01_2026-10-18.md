# Network Capability Transition — Phase 01 construction report

**Round:** `01 / 04`, Network Capability Transition Construction.
**Branch:** `testing` (worked in place; no feature, debug, temp branch or worktree).
**Start SHA:** `f868a8fb49efc66b8e0b9b97229a53171c50b053` (local `testing` was three commits behind
`origin/testing` at session start; fast-forwarded before any work with `git pull --ff-only origin testing`).

**Evidence levels used here** follow `KNOWLEDGE/README.md`:

| label | meaning in this report |
|---|---|
| `VERIFIED_SOURCE` | the statement was read in the tree at the SHA above, with the file/function named |
| `VERIFIED_UNIT` | a `go test` run on this host passed and the named test is what asserts it |
| `VERIFIED_MUTATION` | a deliberate source defect was introduced, the suite was run, and the named test failed |
| `BLOCKED_BY_ENVIRONMENT` | the command could not run on this host; what it would have proved is stated |

**Not `VERIFIED_CI`.** This tree contains no workflow-run record, so a CI job declaration is not
evidence that a job ran. **Device behaviour is not verified:** no TUN, no NetworkExtension, no real
Wi-Fi↔cellular handover was exercised here. Everything below about platforms is `VERIFIED_SOURCE`
plus the platform readers' unit tests.

**`-race` is `BLOCKED_BY_ENVIRONMENT`.** `go env CGO_ENABLED` is `0` and this host has no mingw-w64 C toolchain;
`go test -race` stops with `-race requires cgo`. What it would have proved: that the atomic
publication of `(epoch, settled, capability)` and the `sync.Mutex` discipline in `beginTransition` /
`commitTransition` are free of data races under concurrent readers. The functional equivalents
(`TestCapabilitySnapshotIsRaceFree`, `TestCapabilityReadIsConsistentUnderConcurrentTransitions`,
`TestConcurrentEnvironmentTransitionsAreSerialised`, `TestDNSEnvironmentChangeIsRaceFree`) ran
without `-race` and passed.

> **Correction, measured after the round: the `-race` blocker is not what this report first said.**
> The blocker above was recorded as "no C compiler". An MSVC-targeting `clang` **is** present, so that
> wording was wrong, and being wrong about a blocker is worse than being vague about one - it hides the
> fact that a single install would unblock the item. What was actually tested:
>
> | attempt | result |
> |---|---|
> | `CGO_ENABLED=1 CC=clang go build -race ./common/netcap` | **succeeds** - which is why the first probe looked like progress; it compiles but never links |
> | `CGO_ENABLED=1 CC=clang go test -race ...` | fails at `runtime/cgo`: `clang: error: unsupported option '-mthreads' for target 'x86_64-unknown-windows-msvc'` - Go's Windows cgo driver passes mingw-style flags this toolchain rejects |
> | `CC='clang --target=x86_64-w64-windows-gnu'` | fails at `stdlib.h: file not found` - no mingw sysroot or headers |
> | a `gcc.exe` anywhere on the host | none (searched) |
>
> So `-race` remains `BLOCKED_BY_ENVIRONMENT`, and the **minimum condition to resume** is: a mingw-w64 gcc
> (e.g. MSYS2's `mingw-w64-x86_64-gcc`) on `PATH`, then
> `CGO_ENABLED=1 CC=gcc go test -race ./route ./common/dialer`. Everything else this round needed is
> already in place. `VERIFIED_CI` still does not apply, and nothing in this report depends on `-race`
> having run.
---

## A. Branch / start SHA / final SHA

| | |
|---|---|
| branch | `testing` |
| start SHA | `f868a8fb49efc66b8e0b9b97229a53171c50b053` |
| last commit that changes code | `aa9ce1d2255` |
| final SHA | the `HEAD` of `origin/testing` immediately after the commit that adds this file. It is deliberately **not** written here: a commit cannot carry its own hash without being rewritten, and a hash that is rewritten after it is printed is worse than no hash. Read it with `git log -1 --oneline origin/testing`. |
| worktree | clean **except** `clients/desktop`, and the exception is created by the mandated sync rather than by this round. Diagnosed rather than waved at: before the pull the recorded gitlink was `edb4c6658eb`, which is exactly what the submodule is checked out at; the three commits pulled moved the pin to `d502365906a` and `git pull` does not recurse into submodules, so the parent now reports the submodule modified. The submodule itself has no uncommitted content. Left as it is — `git submodule update clients/desktop` would make the parent look clean while re-pointing a client source this round is forbidden to touch (§25), and the fork's branch tip is not lost either way. Reproduce: `git ls-tree HEAD clients/desktop` vs `git -C clients/desktop rev-parse HEAD`. |

The tree was up to date with `origin/testing` before work began (`0/0`), because the three commits it
was behind included release branding and a client pin — not this round's subject, but not something to
build a report on top of either.

---

## B. Existing network transition event graph

This is the graph as it exists, not as it was assumed. Every arrow was read in the tree.

```text
OS / platform event
│
├── networkUpdateMonitor (sing-tun)
│     Linux: netlink route/addr dump          Windows: NotifyRouteChange +
│     Darwin: PF_ROUTE                        NotifyInterfaceChange + DNS registry watch
│     emits SYNCHRONOUSLY, in registration order
│     │
│     ├─(1) NetworkManager.postUpdateNetworkEnvironment      registered FIRST  (network.go:392)
│     │        └─ updateNetworkEnvironment
│     │              └─ recomputeNetworkEnvironment                   [environmentUpdateAccess]
│     │                    ├─ fingerprint = gateways | SSID | gateway MAC   (network_environment.go:192-240)
│     │                    ├─ localFamilyCapability()  ──┐
│     │                    ├─ stateAccess.Lock()         │
│     │                    ├─ if fingerprint changed:    │
│     │                    │     publish fingerprint      │
│     │                    │     beginTransition()  ─────┼─ epoch++ , transitionStable=false ,
│     │                    │     storeFamilyCapability ◄──┘   runtimeCoordinator.Advance()
│     │                    │     publish capability
│     │                    └─ else  storeFamilyCapability only   (NO claim, NO epoch move)
│     │        └─ if changed: boundEnvironmentTransitionExported
│     │              └─ resetRunAccess.Lock(); resetNetworkLocked(); commitTransition(token)
│     │
│     └─(2) defaultInterfaceMonitor.delayCheckUpdate   registered SECOND
│              └─ time.AfterFunc(ONE SECOND, postCheckUpdate)      ← debounce lives HERE
│                    ├─ interfaceFinder.Update()
│                    ├─ checkUpdate() → defaultInterfaceChanged(old,new)?
│                    └─ if changed: emit → NetworkManager.notifyInterfaceUpdate
│                          ├─ observeNonPlatformInterfaces()   ← the interface list is refreshed HERE
│                          ├─ beginTransition()  (coalesced: one token per burst)
│                          └─ dispatchInterfaceUpdateLocked → go updateInterface(ctx, iface)
│                                └─ [resetRunAccess] observeNonPlatformInterfaces()
│                                     recomputeNetworkEnvironment()
│                                     decide ONE reset for {environment change, pending interface change}
│                                     resetNetworkLocked(); commitTransition(token)
│
├── platformInterface.NetworkInterfaces() → NetworkManager.UpdateInterfaces()
│     └─ store list; postUpdateNetworkEnvironmentWhenObserved(observed)
│        (an observation that FAILED publishes nothing — see the trap in §U)
│
├── WIFI monitor → onWIFIStateChanged → updateNetworkEnvironment  (SSID is in the fingerprint)
├── Windows power listener → notifyWindowsPowerEvent → ResetNetwork / recompute
├── ResetNetwork(ctx)   — libbox command server, ReleaseMemory, power suspend
└── Box.Start → EstablishInitialNetworkEnvironment()   (synchronous, before Start returns)
```

Two facts about that graph decide most of this report:

1. **The route view is read fresh at every event; the interface list is not.** `localFamilyCapability`
   loads `r.networkInterfaces` (an atomic) and reads `machineGlobalIPv6Route()` (a route-table dump, no
   cache) *in the same call*, but only `notifyInterfaceUpdate`, `updateInterface` and `UpdateInterfaces`
   ever **write** that atomic. On arrow (1) — a route-only change — the addressing the capability is
   built from is whatever the last writer left, up to one interface-monitor debounce (1 s) old.
   `VERIFIED_SOURCE`; consequence and the reason it is nonetheless the right shape are in §U.3.
2. **Only one of the arrows can claim a generation for a capability change.** Arrow (1) claims only
   when the *fingerprint* moves. A change to the capability alone — an address appearing, a route
   appearing — is published by `storeFamilyCapability` under the **incumbent** generation, with
   `transitionStable` still `true`. That is deliberate (`family_capability.go:83-90`, "a change to the
   capability is deliberately NOT a network transition") and it is what makes the whole design need
   only one clock (§D).

---

## C. Capability authority

**One authority exists, and it is `route.NetworkManager`.** Recorded because the brief ranks "more than
one source" as a high-priority risk; the answer came out clean and is worth pinning down.

```text
       r.networkInterfaces (atomic, platform or finder list, up-only)
       r.interfaceMonitor.MyInterfaces()          this process's own TUN / bridges
       machineGlobalIPv6Route()                   per-platform route reader, NO cache
                        │
                        ▼
        netcap.UsableInterfaces            drop !up, own interfaces, loopback-only
                        │
                        ▼
        netcap.Evaluate                    → FamilyCapability{IPv4, IPv6, GlobalIPv6Egress, reachable}
                        │
                        ▼
        storeFamilyCapability              ONE writer; atomic.Pointer[adapter.NetworkFamilyCapability]
                        │
                        ▼
        adapter.NetworkFamilyCapabilitySnapshotter.NetworkFamilyCapabilitySnapshot()
                        │                  (epoch, stable, capability) under transitionAccess
                        ▼
        common/dialer.networkObserver      resolved ONCE at construction (strongest of five interfaces)
                        │
                        ▼
        captureLocalFamilyPolicy()         → localFamilyPolicy{ownership, capability}
                        │
                        ▼
        localDialOwnership.owns()          began settled && settled now && same epoch
```

- **The model never guesses about the public Internet.** `netcap.go:1-31`: a HARD negative is only ever
  produced by a structural impossibility (no interface on the local dial path carries an address of that
  family). `StateValidated` and `StateDegraded` are reserved and nothing produces them.
- **No second source in the dial path.** `netcap.Evaluate` has exactly one production caller
  (`route/network_family_capability.go:122`) and `netcap.FamilyCapability` has exactly one production
  holder (`adapter.NetworkFamilyCapability`, consumed by `common/dialer`). No file under
  `common/dialer/protocol`/`route` enumerates interfaces or a route table to build a family verdict;
  `protocol/direct/outbound.go` `fetchMyAddresses` reads *this process's own* TUN addresses for sniffing
  and route purposes and feeds nothing into the capability. `VERIFIED_SOURCE` (repo-wide grep for
  `netcap.FamilyCapability`, `netcap.Evaluate`, `HardUndialable`, `HardUnavailable`).
- **The two TUN-exclusion rules are different and both belong to the producer.** `UsableInterfaces` drops
  `MyInterfaces()` from the *capability*; `machineGlobalIPv6Route` passes the same names and their
  indices into the route predicate so a TUN route is not mistaken for a way out. A client that creates
  its own TUN without registering it can still be counted as underlay addressing — the documented
  direction is a missed fast-fail, never a refused connection (`network_family_capability.go:73-86`).
- **Degradation is total.** Every field defaults to `StateUnknown`, so a manager that never published
  (not started, no interface monitor, a test double) refuses nothing and the dialer keeps its previous
  behaviour exactly.

---

## D. Generation semantics

**There is one counter, and it is the network reset epoch. No `CapabilityRevision`, `CapabilityEpoch` or
`RouteRevision` exists, and none was added** — see §X for why the existing generation is sufficient.

| element | where | semantics |
|---|---|---|
| `networkResetGeneration atomic.Uint64` | `network.go:1174` | monotone; advanced only inside `beginTransition` |
| `transitionOwner transitionToken` | `network.go:979` | the epoch of the transition that currently owns the settled state |
| `beginTransition()` | `network.go:997` | one atomic mutation: `transitionStable=false` → hook → `transitionOwner = ++generation` → publish to `runtimeCoordinator.Advance(epoch)` **after** releasing `transitionAccess` |
| `commitTransition(token)` | `network.go:1083` | settles **only if** `transitionOwner == token`; the check and the store are one critical section |
| `transitionOwns(token)` | `network.go:988` | the "am I still the live transition" query used before any body runs |
| `runtimeCoordinator.Advance(epoch)` | `common/runtimecoord/coordinator.go:80` | monotone; supersedes and cancels any in-flight `RebindLease` |

**Production claim sites (§30 Q3).** Exactly four, `VERIFIED_SOURCE`:

| # | site | when it claims |
|---|---|---|
| 1 | `recomputeNetworkEnvironment` (`network_environment.go:283`) | the environment **fingerprint** changed; claimed while `stateAccess` is still held, *before* the capability is stored |
| 2 | `notifyInterfaceUpdate` (`network.go:1397`) | a default-interface notification, when no transition is pending, or when the pending token has been superseded |
| 3 | `ResetNetwork` (`network.go:942`) | only when the recompute it runs first found no change — otherwise the recompute's token is reused |
| 4 | `notifyWindowsPowerEvent` (`network.go:1645`) | resume, same rule as (3) |

**What this means for the transitions the brief lists.** A capability change is *not* a generation change
unless the environment fingerprint moved with it:

| capability change | generation moves? | why |
|---|---|---|
| default interface changed | **yes** | new gateway set ⇒ new fingerprint; and (2) claims too |
| gateway changed | **yes** | gateway is a fingerprint term |
| Wi-Fi SSID changed | **yes** | SSID is a fingerprint term (BSSID is deliberately not: `TestBSSIDDoesNotParticipateInTheEnvironmentFingerprint`) |
| `network_strategy` interface swap, VPN/TUN reset | **yes** | via the fingerprint |
| IPv6 address added/removed on the default interface | **yes**, one epoch, ≈1 s later | `defaultInterfaceChanged` compares `interfaceNetworks` (IPv6 masked to /64) and emits; the notification claims |
| IPv6 address added/removed and the notification is superseded by another | **yes**, one epoch | coalesced on `networkResetPendingToken` |
| **IPv4/IPv6 global route added/removed on an unchanged interface** | **no** | routes are not a fingerprint term; the capability is republished under the incumbent epoch with `settled` untouched |
| explicit `ResetNetwork` | **yes** | always, by definition |
| release / resume | **yes** | `ReleaseMemory` and the resume path both claim |

The last row of that table is the answer to §30 Q4 and it is the one place this report has to be precise:
**a same-interface route change is fully covered for the capability, and deliberately not covered by a
generation bump.** It is safe because the consumer's gate is `(settled, epoch, capability value)` and the
value itself carries the new fact; a dial that captured the old value keeps using it, which the code
documents as safe (the address it produces is a real connection on a real socket, and the case the gate
exists for — no address of that family anywhere — fails instantly on its own if the structure is gone).

**Why no second epoch.** Each one of the six questions the brief requires before adding a counter
(§10) has an answer from the existing machinery, so the second counter would have no work to do:

1. *Who bumps it?* Nobody new would need to: the capability is a **value**, not a clock, and every
   consumer re-reads it through one atomic load.
2. *What is its atomic relation to the generation?* It would have to be published inside the same
   `stateAccess` critical section as the fingerprint, exactly as the capability already is.
3. *What would setup capture?* `dialSetupGate` captures a per-generation `context.Context`, not a
   number; a revision could only cancel setups it does not need to cancel — the capability change is
   exactly the case that must **not** cancel an in-flight setup.
4. *What would familyHealth key on?* The epoch already does, and using a revision would let a soft
   penalty survive the reset that was supposed to forget it (`dual_stack_scheduler.go:866-874`).
5. *What would an established connection stamp?* The epoch, already.
6. *Who wins on interleave?* Today: last claim wins, decided under `transitionAccess`. A second clock
   makes that a two-variable decision with no owner.

Measured cost of not having one: a route flap republishes the capability under a settled generation
(§U.3). A revision would let that be marked "settling" — at the price of a second clock on every hot
path, for a window that self-corrects in ≤ one interface-monitor debounce.

---

## E. Stability semantics

**Two states are published, and a third is deliberately kept out of the capability.**

| state | representation | meaning |
|---|---|---|
| `Settling` | `transitionStable == false` | a transition has claimed the network and its body has not finished (or a newer claim supersedes the one that finished) |
| `Stable` | `transitionStable == true` | the transition that owns the network has committed, and no newer one has claimed |
| `DeadPath` | **not** in the capability | `networkIsConfirmedOffline()` = the pause manager says paused **and** the onset has held ≥ `HardTransitionConfirm` (2 s). It selects `ReclaimDeadPath` for **established connections**; it does not produce a capability verdict |

**Why `DeadPath` is not a capability state.** A dead path is a statement about the *path*, not about the
*addressing*: the machine's interfaces can still carry both families while nothing on the far side
answers. Encoding it as `Unavailable` would hard-deny a family out of a transient platform report —
precisely the failure the `addressed == 0` guard exists to prevent one level down. So it stays where it
is provable (the reclaim policy) and the capability stays fail-open. `VERIFIED_SOURCE`, and pinned from
both sides by `TestConfirmedOfflineIsReclaimedAsADeadPath` (new), `TestAGapThatHasNotHeldIsStillADrain`
(new), `TestTransientGapIsNotConfirmedOffline`, `TestSustainedPauseIsConfirmedOffline`,
`TestGapOnsetSurvivesABurstOfNotifications`.

**The two ordering rules that make the pair safe**, `VERIFIED_SOURCE` at `network.go:1028-1066`:

- *unstable first, then the token.* The reverse leaves an instant in which a reader not holding
  `transitionAccess` — `NetworkTransitionStable` is lock-free on purpose — sees the new token beside a
  settled flag. The four combinations are enumerated in the source; only three are reachable, and the
  dangerous one is not among them.
- *one `defer`, unlocking before `runtimeCoordinator.Advance`.* It used to be two, and Go's LIFO ran the
  publication under `transitionAccess` — an ABBA hazard against any registrant that calls back into
  `NetworkTransitionSnapshot`.

`VERIFIED_UNIT` for the ordering, from both sides of the lock:
`TestPublishedEnvironmentCannotBeObservedAsStableBeforeTransitionClaim` (a reader parked at the
`environmentPublished` hook observes the new fingerprint while the claim is part of the same critical
section), `TestLaterTransitionCannotReportSettledBeforeItsBodyRuns` (a superseded body's commit is
ignored), and `TestCommitCannotRaceNewerBeginIntoStableState` (the check-and-settle TOCTOU).

**Settling never publishes a false hard verdict, and the guarantee lives in the consumer.** The
capability value IS published while unsettled (that is what makes it a value rather than a state
machine), and `localFamilyPolicy` cannot act on it: `localDialOwnership.owns()` requires the capture to
have *begun* settled, the network to *be* settled now, and the epoch to be unchanged —
`network_observation.go:190-217`. `VERIFIED_MUTATION`: deleting the "began settled" term fails
`TestUnstableThenSettledCaptureStillDeniesNothing`,
`TestSettledAgainAfterACommitStillDoesNotLaunderADuringCapture`,
`TestASettledFlagOnlyManagerCannotLaunderADuringCapture`,
`TestADialThatBeginsDuringATransitionIsRefusedAfterItCommits`,
`TestDialEpochPredicateRequiresASettledNetwork`, `TestEveryDialEntryPointSharesTheTransitionContract`.

---

## F. Capability snapshot

```go
// common/netcap
type FamilyCapability struct {
    IPv4             FamilyState   // Unknown | Unavailable | Provisioned  (+ reserved Validated, Degraded)
    IPv6             FamilyState
    GlobalIPv6Egress FamilyState   // the SEPARATE question about the public Internet
    reachable        int           // diagnostic: how many interfaces the evaluation saw
}

// adapter — the published value
type NetworkFamilyCapability struct { Capability netcap.FamilyCapability }

// adapter — the publication, one call, one observation
NetworkFamilyCapabilitySnapshot() (epoch uint64, stable bool, capability NetworkFamilyCapability)
```

**What it is not.** It is not "is the Internet reachable over this family". A campus presenting a
complete and healthy IPv6 structure while carrying no working public IPv6 is the motivating case; the
model refuses to turn "the structure exists" into "it works", because the only way to know would be a
probe, and a probe is both a new always-on cost and a new source of wrong answers. The second question is
learned from real traffic by the existing scheduler (`familyHealth`).

**`GlobalIPv6Egress` is separate on purpose.** `IPv6 = Provisioned` with
`GlobalIPv6Egress = Unavailable` is a Windows box with an IPv4 address and a `fe80::` link-local
address: a link-local destination is genuinely reachable, no global IPv6 destination is. One field could
not say both.

**Unknown vs Unavailable — the rule (§30 Q7, §12).**

| term | hard negative requires | anything unproven becomes |
|---|---|---|
| `IPv4` / `IPv6` | the machine has usable interfaces, **at least one of them carries a valid address**, and none carries this family | `Unknown` (fail open) |
| `GlobalIPv6Egress` | routes were actually read, and none was navigable | `Unknown` (gates nothing) |

`Unknown` never refuses. `Unavailable` refuses. There is no third spelling, and in particular "resolved to
nothing" cannot reach the model at all: `Evaluate` has no resolver-shaped parameter
(`netcap.go:377-383`). `VERIFIED_MUTATION`: removing the `addressed == 0` guard — so a link that is up but
not yet addressed hard-denies **both** families — fails `TestEvaluateAddresslessInterfaceIsNotEvidence`.

`HardUndialable` is the single decision point and takes the *destination*, not a family, so a caller
cannot forget the scope term:

```text
ScopeLocal  (loopback, link-local, unspecified)  → dialable, always
ScopeSite   (fc00::/7)                           → dialable, always
ScopeGlobal                                      → family must exist AND, for IPv6, egress must not be
                                                    provably absent
```

---

## G. Transition matrix

The brief's Phase 2 list, resolved against the code. "epoch" is the network generation; "settled?" is
whether the transition window is entered.

| # | transition | published capability | epoch | settled? | evidence |
|---|---|---|---|---|---|
| 1 | IPv4-only → dual-stack | `IPv4=Provisioned`, `IPv6=Unavailable` → `Provisioned` | moves (address set changes the monitor's view) | yes | `VERIFIED_SOURCE` + `TestCapabilityFollowsTheRefreshedList` |
| 2 | dual-stack → IPv4-only | `IPv6` → `Unavailable` | moves | yes | `VERIFIED_SOURCE` + `TestResetNetworkRepublishesTheVerdictWhenIPv6WentAway` |
| 3 | IPv6-only → dual-stack | `IPv4` → `Provisioned` | moves | yes | `VERIFIED_SOURCE`; **platform caveat in §H** |
| 4 | dual-stack → IPv6-only | `IPv4` → `Unavailable` | moves | yes | `VERIFIED_SOURCE`; **platform caveat in §H** |
| 5 | IPv4-only → IPv6-only | both families flip in one publish | moves | yes | `VERIFIED_SOURCE` |
| 6 | IPv6-only → IPv4-only | both families flip in one publish | moves | yes | `VERIFIED_SOURCE` |
| 7 | dual-stack → dead path | **both `Unknown`** (no usable interface) | moves (fingerprint → 0) | yes | `VERIFIED_UNIT`: `TestEnvironmentTransitionToZeroEstablishesABoundary`, `TestTransitionDuringAGapDrains` |
| 8 | dead path → IPv4-only | `IPv4=Provisioned`, `IPv6=Unavailable` | moves | yes | `VERIFIED_UNIT`: `TestEnvironmentZeroToNetworkEstablishesABoundary` |
| 9 | dead path → dual-stack | both `Provisioned` | moves | yes | `VERIFIED_SOURCE` |
| 10 | same-interface IPv6 **route** lost | `IPv6` stays `Provisioned`, `GlobalIPv6Egress` → `Unavailable` | **does not move** | **no** | `VERIFIED_UNIT`: `TestAGenerationWithoutAGlobalIPv6RoutePublishesTheEgressVerdict`, `TestTheEgressVerdictMovesOnlyWithItsGeneration` |
| 11 | same-interface IPv6 **route** restored | `GlobalIPv6Egress` → `Provisioned` | **does not move** | **no** | `VERIFIED_UNIT`: `TestAGenerationWithAGlobalIPv6RouteKeepsIPv6Dialable` |
| 12 | same-interface IPv6 **address** lost | `IPv6` → `Unavailable` | moves, ≈1 s later | yes | `VERIFIED_SOURCE` + `TestCapabilityIsRepublishedWhenOnlyTheAddressingChanges` |
| 13 | same-interface IPv6 **address** restored | `IPv6` → `Provisioned` | moves, ≈1 s later | yes | `VERIFIED_SOURCE` + `TestInterfaceObservationPrecedesTheVerdictOnTheEventPath` |

Rows 10 and 11 are the answer to §30 Q4 and the case the brief calls out as a "strange finding" to hunt
for ("same interface 下 route 变化根本没 bump generation"). It is real, it is by design, and the reason it
is safe is §D's last paragraph.

---

## H. IPv4 transitions

Symmetric by construction — `Evaluate` treats the two families identically (`stateFor(hasIPv4)`,
`stateFor(hasIPv6)`), and `HardUnavailable(ipv4 bool)` is the same predicate on either side. **The one
asymmetry in the whole model is a deliberate extra term for IPv6** (`GlobalIPv6Egress`), and there is no
IPv4 counterpart because no IPv4-side fact is missing when an IPv4 address exists.

Recorded because §14 asks for the audit and forbids turning it into a new probe:

- **`IPv4 address present` with no IPv4 default route** reads as `Provisioned`, and a global IPv4
  destination is therefore attempted. The kernel fails it immediately. This is the IPv4 mirror of the
  IPv6 case `GlobalIPv6Egress` was added for, and it is **not** modelled — an explicit decision, not an
  oversight: adding an IPv4-egress term would be a second hard gate built from a platform-dependent
  route read, for a failure whose user-visible cost is one immediate `ENETUNREACH` on a path that
  already races. `VERIFIED_SOURCE`.
- **gateway change / DHCP renewal**: covered, and this is the *strongest* of the IPv4 cases — the gateway
  set is the environment fingerprint, so the change moves the epoch and re-publishes
  (`TestGatewayChangeIsStillANetworkTransition`).
- **cellular transition**: covered as a fingerprint change, and as a drain rather than a teardown
  (`TestAnOnlineTransitionIsADrain`, new).
- **IPv4-only → IPv6-only** is the case where a *platform* limitation shows: see the Windows note in §U.5.

---

## I. IPv6 address transitions

```text
interface up, addressed, no IPv6 address   → IPv6 = Unavailable
interface up, addressed, fe80:: only       → IPv6 = Provisioned   (a link-local DESTINATION is reachable)
interface up, addressed, ULA only          → IPv6 = Provisioned   (ScopeSite destinations are reachable)
interface up, addressed, global /128 only  → IPv6 = Provisioned   (the address exists; egress is the other term)
interface up, NOT addressed                → both Unknown          (a handover in progress, not a verdict)
```

**`HasIPv6Address` alone would be wrong twice**, and both directions are pinned: a `fe80::`-only link is
*not* evidence of global reachability (that is why `GlobalIPv6Egress` exists), and a ULA-only link is
*not* unreachable (that is why `ScopeSite` exists as its own answer). `VERIFIED_UNIT`:
`TestNoInterfaceCarryingAFamilyIsStillUnavailable`, `TestDownInterfacesAreNotEvidence`,
`TestABoundDialToAnIPv6LessInterfaceIsTheUnsafeDirection`, `TestCapabilityStillSeesRealIPv6BehindTheExcludedTunnel`.

**A host route is not a way out.** An on-link `/128` is the machine reaching an address it already owns —
`usableGlobalIPv6Route` rejects it, because a stub address, a NAT64 interface or a CLAT interface would
otherwise report "global IPv6 works" on the strength of the machine's own address. `VERIFIED_UNIT`:
`TestAVirtualAdapterAloneNeverCreatesEgress`.

---

## J. IPv6 global route transitions

Three platform readers, one shared predicate (`usableGlobalIPv6Route`), and a fourth, tag-only stub:

| platform | primitive | notes |
|---|---|---|
| Linux | netlink route dump; `linuxRouteQualifies` accepts **any** table | a policy-routing setup can hold the only IPv6 route in a custom table; reading `RT_TABLE_MAIN` alone would publish `RouteAbsent` on a machine that routes global IPv6 perfectly well. `linuxTableUnspec`/`linuxTableMain` are written as numbers so the contract compiles and asserts everywhere |
| Windows | `GetIpForwardTable2(AF_INET6)` | **multicast is rejected before the scope test**: Windows installs an `ff00::/8` route on *every* IPv6-capable interface, so without that check the egress term is inert (measured on the affected host: 7 zone rows accepted, 0 with them excluded) |
| Darwin | BSD RIB | reported `::/0` as the unspecified address; the family check therefore precedes the `IsUnspecified` test — otherwise `0.0.0.0` reads as an IPv6 default route |
| stub | — | `RouteUnobserved`; contributes nothing |

**`RouteUnobserved` is the safety property.** A platform that cannot read a route table, or a read that
failed, must contribute nothing rather than a guess — the term then degrades to the address-only answer
the model had before the field existed. `VERIFIED_UNIT`: `TestTheRouteObservationFailureOfOneGenerationDoesNotOutliveIt`,
`TestInterfaceNameByIndexReportsInsteadOfGuessing`.

**Blackhole / unreachable / prohibit / throw are rejected**, so an explicitly unreachable configuration
cannot be read as "IPv6 is available". **An on-link global prefix shorter than /128 IS accepted** — a
host on a routed global /64 with no gateway can reach every address in that prefix, and rejecting
gateway-less routes would have refused global IPv6 on a directly-connected network.

---

## K. Same-interface changes

The full same-interface set, with the two mechanisms that carry it:

| change | mechanism | epoch | pinned by |
|---|---|---|---|
| IPv6 global route lost/restored | arrow (1) recompute; route read fresh | no | `TestTheEgressVerdictMovesOnlyWithItsGeneration` |
| IPv6 address lost/restored | arrow (2) → `defaultInterfaceChanged` (masked /64 set) → notify → epoch | yes | `TestCapabilityFollowsTheRefreshedList` |
| IPv6 address changed **within** the same /64 | `interfaceNetworks` masks to /64 ⇒ **no** notification | no | `VERIFIED_SOURCE`, `sing-tun/monitor_shared.go:171-176` |
| IPv4 address changed | same as above, unmasked | yes if the /24-or-longer prefix differs | `VERIFIED_SOURCE` |
| flags changed (up/down) | `defaultInterfaceChanged` compares `Flags` | yes | `VERIFIED_SOURCE` |
| MTU changed | compared | yes | `VERIFIED_SOURCE` |
| gateway changed | fingerprint term | yes | `TestGatewayChangeIsStillANetworkTransition` |
| DNS servers / search domains changed | **not** a network transition; the DNS layer advances its own generation | no | `TestDNSOnlyServerChangeIsNotANetworkTransition`, `TestDNSOnlyChangeAdvancesTheGeneration` |
| duplicate DNS notification | coalesced | no | `TestDuplicateDNSNotificationIsNotANetworkTransition` |
| temporary empty DNS list | not a transition | no | `TestTemporaryEmptyDNSListIsNotANetworkTransition` |

The within-/64 row is benign and worth naming: the family *presence* is what the capability reads, and an
address change inside a prefix cannot change presence. What it does mean is that `r.networkInterfaces`
can lag for an address change the capability would treat identically — a cost, not a defect.

---

## L. Wi-Fi / cellular changes

- **SSID is a fingerprint term; BSSID is not** (`TestBSSIDDoesNotParticipateInTheEnvironmentFingerprint`).
  Roaming between APs of one SSID is therefore not a transition, which is what stops a large office from
  generating an epoch per access point.
- **Gateway hardware address is a fallback identity** when no SSID is available
  (`systemNeighborHardwareAddresses`), so a wired handover between two networks with the same gateway IP
  is still distinguished.
- **A handover gap is a pause, not a verdict.** `dispatchInterfaceUpdateLocked` with no default interface
  records the onset and calls `NetworkPause()`; the capability publishes `Unknown` for both families
  because `UsableInterfaces` finds nothing. Only a pause that has **held** 2 s selects `ReclaimDeadPath`.
- **The burst problem is solved by coalescing, not by a timer.** `notifyInterfaceUpdate` claims once and
  reuses the pending token for a burst (`TestHandoverNotificationBurstClaimsOneGeneration`,
  `TestRepeatedInterfaceNotificationsCoalesceOwnership`). The environment side is coalesced by the
  fingerprint comparison itself — a repeated notification costs a hash compare and no teardown.
- **Cost is bounded and reversible.** `TestRepeatedInterfaceChangesCostBoundedWorkAndAreReversible`,
  `TestInterfaceUpdatesDoNotAccumulateGoroutines` (`VERIFIED_UNIT`) — the "one goroutine per network
  event" prohibition in §26 is pinned, not merely intended.

---

## M. Flow capture

**A flow consumes one published snapshot and re-probes nothing.**

```text
newNetworkObserver(manager)                  resolved ONCE, at construction
   NetworkFamilyCapabilitySnapshotter   generation + settled + verdict, ONE reading   ← production
   NetworkTransitionSnapshotter         generation + settled, one reading
   NetworkResetCounter + State          two readings (tearing admitted)
   NetworkResetCounter                  generation only
   NetworkTransitionState               settled only
   nothing                              no opinion
```

The five-way precedence exists because the same defect had been fixed twice, in two files, in the same
shape: *a manager that reports a fact and is then read through an accessor that cannot carry it silently
loses the answer*. One resolution means no call site can prefer a weaker source.

**Every decision goes through `localDialOwnership`, and there is no exported field that skips it.** The
consumers are:

| consumer | gate |
|---|---|
| `DefaultDialer.DialContext` / serial path | `hardUndialableError` |
| literal candidates | `hardUndialableError` |
| packet path | `hardUndialableError` |
| `candidateScheduler` plan filter | `localFamilyPolicy.gate()` — **one** ownership read for the filter, independent of the candidate count |
| `candidateScheduler` late arrival | `s.localFamily.hardUndialable(address)` — a **fresh** read, deliberately |
| recovery candidate filter | `withoutUndialable` / `refusedCandidateIn` |
| native bypass (`LocalFamilyDialer`) | same policy, **opposite polarity** (unsettled ⇒ *not blocked*) |

`VERIFIED_MUTATION`: making a superseded or unsettled capture still restrict fails
`TestOldVerdictCannotDenyNewGenerationScheduler{,LateStream,Literal,LiteralCaptureWindow,Serial,Bypass}`,
`TestDuringTransitionCannotHardDeny`, `TestOwnershipIsCapturedEvenWithoutACapability`,
`TestUnstableThenSettledCaptureStillDeniesNothing`.

**One plan, one ownership read.** The plan-level gate fixes ownership once and judges the whole candidate
list against it; a per-address read would let a transition landing mid-filter drop the head of the list
for a verdict that no longer applies while keeping the tail under "no opinion" — every variable correctly
synchronised, the plan composed from two generations. `VERIFIED_MUTATION`: reading the capability twice
from the manager inside one capture fails `TestOnePlanCostsOneOwnershipRead` and
`TestTheHealthNamespaceComesFromTheCapture`.

**The late-arrival path is deliberately *not* the plan's gate.** An arrival is a fresh decision made
seconds later; a gate built when the race started would apply a verdict captured then to an address
discovered now, and a transition in between would have the stale verdict drop a candidate the successor
generation could dial.

---

## N. FamilyHealth invalidation

`familyHealth` is `map[uint64]familyHealthState`, **keyed by the network reset epoch**, and the key is
read from the *same capture* the scheduler's verdict and ownership come from:

```go
policy := d.captureLocalFamilyPolicy()          // networkGeneration = policy.ownership.epoch
```

So "family health never crosses a generation" is a property of the key rather than of a clearing step.
The history matters here: the key used to be the network **environment fingerprint**, read as a second
call beside the family capture — so a transition landing between the two stored one network's identity
on a scheduler holding another's verdict. The consequence was bounded (a lost fast-fallback bias) but it
was the last place where one dial composed two readings of one instant.

| property | mechanism | pinned by |
|---|---|---|
| no inheritance across a transition | epoch-keyed | `TestFamilyHealthIsScopedToTheNetworkGeneration`, `TestProductionDialerHealthIsPerNetwork`, `TestAFamilyVerdictCannotCrossEnvironments` |
| strict, in the safe direction | a reset that does **not** move the fingerprint also starts a fresh bucket | `VERIFIED_SOURCE`, `dual_stack_scheduler.go:870-874` |
| bounded memory | clear-at-cap at 256 generations | `TestTheFamilyHealthTableStaysBounded` |
| expires | 15 s (`tcpTimeoutWindow`, = `C.TCPTimeout`) | `TestFamilyHealthExpires` |
| cleared by success | `recordSuccess` | `TestFamilyHealthRecoversOnSuccess` |
| protocol failures excluded | `isFamilyPathFailure` | `TestFamilyHealthIgnoresProtocolFailures` |
| **capability ≠ health** | `Unavailable` is a hard gate; a health entry is a *soft* start-both-families nudge that never removes a candidate | `TestFallbackStillProtectsAManagerWithoutAFamilyCapability`, `TestUnavailableFamilyDoesNotDelayTheHealthyOne`, `TestOwnershipStillGatesTheEgressVerdict` |

`VERIFIED_MUTATION`: keying the store and the lookup on one constant generation — so a Wi-Fi IPv6 penalty
follows the user onto cellular — fails `TestProductionDialerHealthIsPerNetwork`,
`TestFamilyHealthIsScopedToTheNetworkGeneration`, `TestFamilyHealthExpires`,
`TestFamilyHealthRecoversOnSuccess`, `TestAFamilyVerdictCannotCrossEnvironments`.

**Fixed in this round, after the audit found it:** `familyHealth.invalidateGeneration` and
`familyHealth.invalidateAll` were production methods with **no production caller** — only
`dual_stack_scheduler_test.go:422` called the first, and nothing at all called the second. They were
removed rather than left as a documented hazard, because the keying already makes them redundant and a
clearing method beside a generation-keyed map is a wrong signpost in the one place this type's scope is
decided: a reader reasonably infers that something calls it on a transition, then looks for the call site
instead of finding the key. "A second way to forget a verdict" is the parallel mechanism this line of work
exists to remove.

What the type does now has exactly three removal paths, and they are the three that read or overwrite an
entry: the expiry inside `fallbackImmediately`, `recordSuccess`, and the clear-at-cap in `storeLocked`.
The test that used the removed method was rewritten to assert the actual design — that the Wi-Fi entry is
*still present* — so the isolation it demonstrates is visibly the key rather than a purge.
`VERIFIED_SOURCE` + `VERIFIED_UNIT` (`go build ./...`, `go vet ./common/dialer`, and the seven
`familyHealth` tests).

---

## O. Recovery / late candidate transition

**The transition boundary for setup is a context per generation, not a lease per dial**
(`route/dial_setup.go`):

```text
setup context owns SETUP ONLY
  caller cancel            → cancels setup               (unchanged)
  network transition       → cancels setup for a stale generation
  successfully established → ownership TRANSFERS; the setup context has no authority left
```

| hazard the brief lists | mechanism | pinned by |
|---|---|---|
| old recovery producer outlives the transition | the setup context is cancelled by `advanceAndBump`; the recovery producer is cancelled and joined | `TestRecoveryProducerStopsOnRequest`, `TestRecoveryProducerIsJoined` |
| a DNS family result arriving late is used by the new generation's scheduler | the late stream is filtered by a **fresh** policy read at arrival | `TestOldVerdictCannotDenyNewGenerationSchedulerLateStream`, `TestLateCandidateCanJoinOnceItsFamilyBecomesAvailable` |
| a late candidate launched after setup cancel | `begin`'s release stops the generation watch **before** releasing the setup, so the watch cannot cancel an established connection; and a cancelled setup context fails the dial itself | `TestLiteralOriginalWinnerCancelsRecovery`, `TestRecoveryReturnsAtOnceWhenTheFirstFamilyIsUsable` |
| recovery offers a candidate the machine cannot dial | `withoutUndialable` on the recovered set | `TestRecoveryDoesNotOfferCandidatesTheMachineCannotDial`, `TestRecoveredUndialableFamilyIsDroppedBeforeTheRace` |
| the refusal reason is the machine's, not the resolver's | one policy, one error constructor | `TestTheRecoveryPathKeepsTheMachinesOwnRefusalReason`, `TestEmptyRecoveryReportsTheMachinesOwnVerdict` |

`VERIFIED_MUTATION`: removing the recovery filter, or making `begin` watch `context.Background()` so a
transition never cancels a setup, both fail the named tests.

---

## P. Setup cancel ordering

The order is stated in the code and every step is load-bearing:

```text
recomputeNetworkEnvironment
   ├─ publish fingerprint            (stateAccess held)
   ├─ beginTransition()              epoch++ , settled=false , coordinator.Advance after unlock
   └─ storeFamilyCapability          (stateAccess still held)   ← publish and claim are ONE observation

resetNetworkLocked                      [resetRunAccess held]
   ├─ connectionManager.Reclaim(reason)
   │     └─ dialSetups.advanceAndBump(func(){ staleGeneration = generation.Add(1) })
   │           └─ ONE critical section: bump + swap the generation context
   │           └─ cancels the previous context AFTER unlocking
   ├─ InterfaceUpdated on endpoints/inbounds/outbounds   (skipped while network is paused)
   └─ router.ResetNetwork()          ← DNS barrier: generation++ then reverse-mapping purge, together

commitTransition(token)                 settles only if this transition still owns the network
```

**Why the bump and the swap are one step.** They used to be two:

```go
staleGeneration = m.generation.Add(1)   // the counter the drain scan compares against
m.dialSetups.advance()                  // the context a setup derives from
```

A dial beginning between them captured the **old** context — so this transition cancelled it, treating it
as an old-path dial — while the connection it produced was stamped with the **new** counter, which *equals*
`staleGeneration`, so the drain scan classified it as current. **Killed as old, classified as new.**
Measured with a forced interleaving: 200 / 200 trials; unforced, 6 / 20 000. Reordering cannot fix it —
there are three instants in play (the gate's capture, the network epoch the predicate compares, the
manager stamp written when the connection is tracked), so moving two only changes which pair disagrees.
Making the bump part of the gate's own transition reduces it to one clock. `VERIFIED_UNIT`:
`TestATransitionNeverPublishesItsCounterBeforeItsGate`, `TestATransitionCancelsSetupsItBeganAfter`,
`TestNetworkTransitionCancelsAnInFlightBlackholedDial`, `TestNetworkTransitionSparesADialStartedAfterIt`,
`TestRepeatedTransitionsWithDialsInFlightLeakNothing`.

**Ordering of capability publication vs setup cancel.** The capability is published **before** the reset
body, while the network is already marked unsettled. That is the only order that works: publishing after
the body would let a flow capture the new epoch with the old verdict, and cancelling setups before the
claim would let a setup be cancelled by a transition that has not yet announced itself.

---

## Q. Established connection / Reclaim boundary

**A capability change never closes an established connection** (§19, §20, §30 Q11):

```text
ReclaimNetworkTransition   drain.  releaseFlows=false, closeAll=false, drain=true
                           → reclaim only what is PROVABLY idle for drainIdleGrace (20 s)
                           → mark the rest as belonging to the previous path
                           → keep sweeping as they fall silent (backoff to a max interval)
ReclaimDeadPath            closeAll=true, releaseFlows=true
ReclaimIdle                reclaim proven-idle only, no drain marking
```

- **"Proven idle" excludes what cannot be observed.** A kernel-owned (spliced) connection moves bytes
  between descriptors and never reaches `Read`/`Write`, so its silence is the absence of an instrument,
  not idleness. `TestTransitionProtectsAKernelOwnedConnection`,
  `TestNeverUsedObservableConnectionBecomesIdle`.
- **The drain ends.** `beginDrainSweep` re-asks the same question, so a stream that keeps transferring is
  never reclaimed however long ago the transition was, and one that falls silent is reclaimed promptly:
  `TestSweepReclaimsWhatFallsSilentAfterTheTransition`,
  `TestSweepSparesConnectionsDialledAfterTheTransition`,
  `TestSweepBackoffResetsOnProgressAndOnANewTransition`.
- **The host is told, but not killed.** `resetNetworkLocked` calls `InterfaceUpdated` on every listener
  **unless the device is paused** — a teardown while paused can only produce a certain-to-fail dial and
  throws away a session the notification did not break. The work is deferred to the wake, not skipped.
- **The classification is now pinned from both directions** — `TestConfirmedOfflineIsReclaimedAsADeadPath`
  and `TestAGapThatHasNotHeldIsStillADrain` (new this round; see §T).
- `VERIFIED_MUTATION`: replacing the draining reclaim with `CloseAll()` fails
  `TestTransitionDuringAGapDrains`.

**QUIC / migratable transports are not touched.** The boundary only says the capability cannot close an
established connection; what the connection does with a migrated path is the protocol's business. The
reason the boundary matters more for QUIC than for TCP is stated and left alone.

---

## R. DNS cache boundary

**A network transition is a DNS barrier, not a cache purge**, and §16's rule is implemented rather than
assumed:

```text
dns.Router.ResetNetwork                       [VERIFIED_SOURCE, dns/router.go:2255]
   ├─ dnsEnvironmentAccess.Lock()
   │    networkGeneration.Add(1)               ← the barrier, from the FIRST instruction
   │    dnsReverseMapping.Purge()              ← split-horizon: an address can mean a new name
   ├─ for each transport: Reset()              ← drop the pooled connection
   ├─ refreshTransportEnvironments()           ← re-pin to the environment the transport now serves
   └─ (the exact cache and the NXDOMAIN name cache are NOT touched)
```

- **Why the reverse mapping is purged and the exact cache is not.** The reverse mapping is a cache of
  what previous answers said an address *meant*; with split-horizon, corporate, captive-portal or VPN DNS
  the same address can mean a different name on the new network, so leaving entries lets a stale name
  participate in route-rule matching until its TTL expires. The exact cache holds "name → address", which
  a path change does not make wrong.
- **Why the advance and the purge are one critical section.** Advancing first is only half of it: while
  the two were separate steps, a capture taken between them read the NEW counter, matched it, and its
  answer — asked for under the network being left — landed after the purge that was supposed to retire
  it. `TestResetNetworkClosesTheWindowBeforePurging`, `TestResetNetworkIsABarrierFromItsFirstInstruction`,
  `TestResetNetworkOrderIsBarrierFirst`.
- **The namespace is the environment fingerprint, not the generation.** The generation restarts at zero
  with the process; the fingerprint is derived from the network itself. Entries belonging to the old
  environment *stay* and simply stop being reachable.
- **What was unpinned, and is now pinned (§T, M9/M19).** Nothing asserted either half of the boundary at
  the network layer. `dns/` pins what the **cache** does with a generation; nothing pinned what the
  **network layer** asks the cache to do, and nothing pinned the positive direction (a reset that does
  not move the environment leaves a correct answer being served). Both are now `VERIFIED_MUTATION`.

---

## S. Performance

**No new timer, no new goroutine, no new periodic work was added by this round** — which is why the
numbers below are a measurement of the existing design rather than of a change.

Per-event cost (network event → published capability):

| step | cost |
|---|---|
| fingerprint | one `DefaultNetworkInterface()` read, one `systemGateways` syscall when the list is empty, FNV-64a over ≤3 short strings |
| addressing | one atomic load (`networkInterfaces`) + one `MyInterfaces()` read; the *copy* happens on the monitor path, not here |
| route term | **one route-table read** per event (netlink dump on Linux, `GetIpForwardTable2` on Windows, RIB walk on Darwin). This is the only expensive step, and it is per **event**, not per flow |
| validation | `UsableInterfaces` + `Evaluate`: a handful of small switches over the usable interface list |
| publication | one atomic pointer store, one atomic bool store, one atomic add |

Per-flow cost — measured on this host, `go test -bench . -benchtime 200000x`:

| benchmark | ns/op | allocs |
|---|---|---|
| `BenchmarkLocalFamilyGate/global-ipv4/allowed` | 15.9 | 0 |
| `BenchmarkLocalFamilyGate/global-ipv6/refused` | 15.5 | 0 |
| `BenchmarkLocalFamilyGate/link-local/allowed` | 10.1 | 0 |
| `BenchmarkLocalFamilyFilter/single/no-capability` | 11.4 | 0 |
| `BenchmarkLocalFamilyFilter/single/both-present` | 11.4 | 0 |
| `BenchmarkLocalFamilyFilter/single/ipv6-refused` | 157.6 | 2 |
| `BenchmarkLocalFamilyPolicyCaptureOnly` (one observation) | 47.9 | 1 (64 B) |
| `BenchmarkLocalFamilyPolicySnapshot` (manager read) | 67.4 | 1 (64 B) |
| `BenchmarkSchedulerSingleCandidate` | 48.1 | 1 |

`VERIFIED_UNIT`: `TestTheCapabilityCaptureTakesNoLockAndNoSyscall` asserts the property the numbers
imply — a per-flow capture takes no lock and issues no syscall. `TestOnePlanCostsOneOwnershipRead`
bounds manager reads to one per plan rather than one per candidate. The prohibitions in §26 are
pinned, not merely intended: `TestCapabilityPublicationRegistersNoCallback`,
`TestInterfaceUpdatesDoNotAccumulateGoroutines`, `TestRepeatedInterfaceChangesCostBoundedWorkAndAreReversible`.

**Not measured here:** a real transition's wall-clock latency on a device, and the cost of a route-table
dump on a host with a very large routing table. Both are `BLOCKED_BY_ENVIRONMENT`; the second would need
a synthetic table the platform readers can be pointed at.

---

## T. Mutation

**Nineteen mutations across three batches.** Each is a minimal, semantically-targeted source edit; after
each the tree was restored with `git checkout --` and re-verified clean. The suite run per mutation was
`go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run
'Capability|Transition|Environment|Egress|Interface|Family|Generation|Recovery|Late|Ownership|Reset|Health|During|OldVerdict|Reclaim|Setup|Settle|Handover|Drain|DeadPath|Sweep|Unavailable'
./common/netcap ./common/dialer ./dns ./route`.

### Batch 1 — the brief's own §28 list

| id | mutation | result | test that killed it |
|---|---|---|---|
| M1 | same-interface IPv6 route loss does not invalidate the capability (`globalRoute := netcap.RouteUnobserved`) | KILLED | `TestAGenerationWithoutAGlobalIPv6RoutePublishesTheEgressVerdict`, `TestTheEgressVerdictMovesOnlyWithItsGeneration`, `TestAGenerationWithAGlobalIPv6RouteKeepsIPv6Dialable`, `TestAVirtualAdapterAloneNeverCreatesEgress`, `TestTheEgressVerdictIsNeverDerivedFromAddressType` |
| M2 | a superseded/unsettled capability is still honoured (`if true` instead of the nil-source check) | KILLED | `TestOldVerdictCannotDenyNewGenerationScheduler`, `TestOldVerdictCannotDenyNewGenerationSchedulerLateStream`, `TestOldVerdictCannotDenyNewGenerationLiteralCaptureWindow`, `TestDuringTransitionCannotHardDeny` |
| M3 | familyHealth crosses the network generation (store and lookup both keyed on `0`) | KILLED | `TestProductionDialerHealthIsPerNetwork`, `TestFamilyHealthIsScopedToTheNetworkGeneration`, `TestFamilyHealthExpires`, `TestFamilyHealthRecoversOnSuccess`, `TestAFamilyVerdictCannotCrossEnvironments` |
| M4 | a capture taken while settling may still hard-deny (drop the "began settled" term) | KILLED | `TestUnstableThenSettledCaptureStillDeniesNothing`, `TestSettledAgainAfterACommitStillDoesNotLaunderADuringCapture`, `TestADialThatBeginsDuringATransitionIsRefusedAfterItCommits`, `TestDialEpochPredicateRequiresASettledNetwork` |
| M5 | recovery offers candidates the machine cannot dial (`withoutUndialable` returns its input) | KILLED | `TestRecoveryDoesNotOfferCandidatesTheMachineCannotDial`, `TestRecoveredUndialableFamilyIsDroppedBeforeTheRace`, `TestRecoveryReportsNothingWhenEveryFamilyIsUndialable` |
| M6 | a setup is not cancelled by the transition that superseded it (`begin` watches `context.Background()`) | KILLED | `TestNetworkTransitionCancelsAnInFlightBlackholedDial`, `TestATransitionCancelsSetupsItBeganAfter`, `TestTransitionCancelledDialIsNotAPathFailure` |
| M7 | the capability is published before the generation is claimed | KILLED | `TestCapabilitySnapshotIsConsistentWithItsEpoch` |
| M8 | the capability is read a second time from the manager inside one capture | KILLED | `TestOnePlanCostsOneOwnershipRead`, `TestTheHealthNamespaceComesFromTheCapture` |
| **M9** | **a network transition flushes the whole DNS cache** (`r.dns.ClearCache()` in `route.Router.ResetNetwork`) | **SURVIVED** | — (see below) |
| M10 | new capability destroys old established connections (`CloseAll` instead of `Reclaim`) | KILLED | `TestTransitionDuringAGapDrains` |

### Batch 2 — invariants the brief's list does not reach

| id | mutation | result | test that killed it |
|---|---|---|---|
| M11 | a half-built network hard-denies **both** families (`if false` instead of `addressed == 0`) | KILLED | `TestEvaluateAddresslessInterfaceIsNotEvidence` |
| M16 | the verdict is not republished when only the addressing changed | KILLED | `TestCapabilityIsRepublishedWhenOnlyTheAddressingChanges`, `TestTheEgressVerdictMovesOnlyWithItsGeneration`, `TestInterfaceObservationPrecedesTheVerdictOnTheEventPath`, `TestCapabilityFollowsTheRefreshedList` |
| **M17** | **a confirmed dead path drains instead of closing** (`reason` pinned to `ReclaimNetworkTransition`) | **SURVIVED** | — (see below) |
| M18 | a network transition does not move the DNS generation barrier (`Add(1)` → `Add(0)`) | KILLED | `TestRouterAndClientShareOneGenerationTruth`, `TestCompleteLookupDoesNotMixNetworkGenerations`, `TestLookupFamiliesDoesNotMixGenerations`, `TestEnvironmentGenerationMatrix` |

### Batch 3 — the two survivors, after the fix

| id | mutation | result | test that kills it now |
|---|---|---|---|
| M9 | a network transition flushes the whole DNS cache | KILLED | `TestANetworkTransitionIsABarrierAndNotACacheFlush` (new) |
| M17 | a confirmed dead path drains instead of closing | KILLED | `TestConfirmedOfflineIsReclaimedAsADeadPath` (new) |
| M19 | the DNS router purges its own cache on a reset | KILLED | `TestResetNetworkStillServesACachedAnswerOnTheSameNetwork` (new) |

**The two survivors were the round's construction work, and they are both test gaps rather than live
defects.** Each is a decision inside a function that is otherwise thoroughly covered, and in each case the
existing tests exercised the *table* rather than the *choice of row*:

- **M9** — `dns/` pins what the cache does with a generation, and the reverse mapping has its own purge
  tests, but nothing asserted which of `ResetNetwork` / `ClearCache` the network layer asks for. Adding a
  flush is one plausible line and it is silent: it buys nothing (an A record is not wrong because the
  device moved) and costs a burst of upstream queries during re-addressing.
- **M17** — `offline_classification_test.go` asserts `networkIsConfirmedOffline()` itself and
  `conn_reclaim_test.go` asserts `Reclaim(ReclaimDeadPath)` directly, so both halves of the dead-path rule
  were tested and the arrow between them was not. Pinning it from **both** sides (confirmed offline ⇒
  close; a gap that has not held ⇒ drain) is what makes the 2-second window a measured decision rather
  than a field nobody reads.

**Summary: 19 mutations, 19 killed after the fix, 2 of them only after this round's tests were added.**

---

## U. New strange findings

### U.1 The two survivors above

Both described in §T. Recorded as findings rather than as trivia because each is a *decision* that a
future editor would make the reasonable-but-wrong way: a cache flush on a network change, and a dead path
that drains.

### U.2 `familyHealth`'s two invalidators were dead production code — removed

`invalidateGeneration` and `invalidateAll` had no production caller. The generation keying makes them
redundant, so they were not *wrong* — but a reader who finds an `invalidateGeneration` method beside a
generation-scoped map will reasonably infer that something calls it on a transition, and will look for the
call site instead of finding the key. That is a documentation hazard pointing in the wrong direction, and
it is exactly the "second clearing mechanism" §10 warns about.

Removed, with the reason left in their place, and with the one test that used them rewritten to assert the
opposite of what it had been implying: the Wi-Fi entry is *still present* after another generation has been
looked up, because the isolation is the key and nothing purges. See §N for the three removal paths that
remain. `VERIFIED_SOURCE` + `VERIFIED_UNIT`.

### U.3 The route view is fresher than the addressing on the route-only path

`localFamilyCapability` reads the route table **now** and loads the interface list from an atomic that
only three writers ever refresh. On the route-only arrow (B.1) those two facts are therefore from
different instants: a route that has just appeared can be published beside addressing that has not been
re-collected. The pair is still one **call**, and the code's comment is accurate about that; what is not
spelled out at that call site is the freshness asymmetry.

**Who corrects it, and how fast.** The default-interface monitor arms a one-second debounce
(`delayCheckUpdate` → `time.AfterFunc(time.Second, postCheckUpdate)`), then refreshes the finder and
re-emits if `defaultInterfaceChanged` — which compares the **masked prefix set**, so an address gain or
loss on the default interface does trigger it. The correction therefore arrives within ≈1 s, with its own
epoch, from the ordinary interface path.

**Why it is not a defect to fix here.** The alternative — reading the addressing at route-event time —
would either mean an interface enumeration per route event (the cost this design exists to avoid) or
freezing the route term to the last interface event, which would make a route-only change *never* update
the egress term, and the egress term exists precisely for the machine whose route appears before anything
else does. The asymmetry is the price of the egress term being real. Recorded as a residual risk (§V.1).

### U.4 `defaultInterfaceChanged` masks IPv6 to /64

`sing-tun/monitor_shared.go:171-176`. An address change **inside** the current /64 produces no
notification, so the manager's addressing list can lag an address change indefinitely. Benign for the
capability — family presence cannot change within a prefix — but it means no test should treat
`r.networkInterfaces` as a live view of the machine. `VERIFIED_SOURCE`.

### U.5 Windows picks the default interface from the IPv4 table only

`sing-tun/monitor_windows.go:157-204`: `checkUpdate` reads `GetIpForwardTable2(windows.AF_INET)` and
returns `ErrNoRoute` when no IPv4 default route wins. On an IPv6-only network that reads as "no default
interface" ⇒ `NetworkPause()` ⇒ after 2 s of continuous pause `ReclaimDeadPath` ⇒ every connection is
closed, on a network that works. This is **upstream sing-tun behaviour in a pinned dependency**, not this
fork's code, and §25 forbids widening into it. Recorded so that rows 3 and 4 of the transition matrix
(§G) are not read as more than they are, and so the next round does not spend a day rediscovering it.
`VERIFIED_SOURCE`.

### U.6 `Add(1)` on the DNS generation is a plain atomic, while the purge takes a lock

The advance and the purge were moved into one critical section precisely because the advance is a plain
atomic add that takes no lock — a capture between them read the NEW counter and matched it, so its answer
landed after the purge. The fix is correct and pinned (`TestResetNetworkClosesTheWindowBeforePurging`).
Recorded because the shape is a trap that recurs: *any* "generation++ then clean up" pair in this tree has
the same window unless the compare on the reader side is inside the same lock.

### U.7 The environment fingerprint is not the network identity

It is `gateways | SSID | gateway-MAC`. Two genuinely different networks can collide (same gateway IP, no
SSID, no neighbour MAC available), and one network can present two fingerprints (a gateway renumber).
The consequence is bounded in both directions — a collision misses a generation bump, a renumber spends
one — and the coalescing tests bound the cost of the second. Recorded because §30 Q3 asks "which events
bump the generation", and the honest answer is "the events that move these three values", not "the events
that change the network".

### U.8 `r.networkInterfaces` has three writers, and none of them watches a non-default interface

The capability ORs addressing over **every** usable interface, but the list it ORs is only rewritten by
`notifyInterfaceUpdate` (default-interface change), `updateInterface` (the same event's goroutine), and
`UpdateInterfaces` (platform push, or `protocol/tailscale`'s binding). The default-interface monitor
refreshes the *finder* on every network event but re-emits only when the **default** interface changed
(`sing-tun/monitor_shared.go:160-176`).

So on a host with more than one usable interface, an address change on a **non-default** interface does
not reach the capability until the default interface changes — a stale view, in both directions. It is
bounded by the model's own generosity: `Evaluate` ORs, so a non-default interface's IPv6 already makes
`IPv6 = Provisioned` even when every dial binds the default interface and will fail there. The staleness
therefore errs toward *fail-open* in the direction that costs an attempt, and the fail-closed direction
needs a non-default interface to **lose** addressing that the list still shows. `VERIFIED_SOURCE`;
recorded rather than fixed because the fix — refreshing the finder inside `updateNetworkEnvironment` —
would make several existing tests depend on the host's real interfaces, which is exactly why observation
is kept outside the recompute (§U.3).

---

## V. Residual risks

| # | risk | severity | why it is accepted |
|---|---|---|---|
| V.1 | **A route-only change publishes a mixed-instant snapshot** (fresh route, addressing up to one debounce old), for ≤1 s. The fail-open direction (stale `Provisioned`) costs a kernel-fast-failed attempt; the fail-closed direction can refuse a family for that second. | low | §U.3. Every alternative is worse: per-event enumeration, or an egress term that never updates on the event it exists for. |
| V.2 | **No producer-side quiescence.** A hard verdict is published from a single observation of the route table, with no dwell time. A route flap therefore flips `GlobalIPv6Egress` twice under an incumbent generation. | low | The gate for a *permanent* hard-deny is the family term, which needs an address-set change, which moves the epoch. Adding a settle timer would be the "high-frequency timer per transition" §26 forbids, for a cost that is one immediate `ENETUNREACH`. |
| V.3 | **`DeadPath` is not in the capability**, so a genuinely offline device publishes `Unknown`/`Unknown` and spends attempts that cannot succeed. | low | Encoding it would turn a *path* fact into an *addressing* fact and hard-deny a family on a transient platform report. The fast-fail loss is bounded by the caller's connect timeout; the misclassification loss would be an outage. |
| V.4 | **Platform asymmetry is unverified on device.** Darwin has no separate route-change callback for a platform-monitor client (`networkMonitor` is nil), so a same-interface IPv6 route change may never reach the producer there. | **medium** | `VERIFIED_SOURCE`; there is no Darwin device in this environment. On such a client the interface monitor is the only source, and a route-only change with no address change may be missed — the fail-open direction. Needs a device run; recorded rather than guessed at. |
| V.5 | **`-race` unverified** | medium | `BLOCKED_BY_ENVIRONMENT`, §header. |
| V.6 | ~~Two dead production invalidators invite a second clearing mechanism~~ | — | **Closed in this round.** Both were removed and the reason was left in their place; the one test that used them now asserts the entry is still present, so the isolation it demonstrates is visibly the key. §U.2, §N. |
| V.7 | **Windows IPv6-only networks read as dead paths** | low for this round, high if used on such a network | §U.5; upstream dependency, out of the round's scope. |
| V.8 | **A non-default interface's addressing change does not reach the capability** until the default interface changes, so `r.networkInterfaces` can be a stale view on a multi-interface host. | medium-low | §U.8. Bounded fail-open by the fact that `Evaluate` ORs across interfaces; the fix would make the recompute host-dependent, which is why observation lives outside it. |

---

## W. Commits

The round is `git log --oneline f868a8fb4..HEAD` on `testing`, pushed in order. The two commits whose
effect is a test are named with their hashes below; the remaining commits — the dead-code removal and this
report plus its corrections — are listed by subject, because a commit cannot contain its own hash and a
hash rewritten after publication is worse than a pointer to `git log`. A correction commit after a report
is this repository's normal shape, not an anomaly: `convergence/POST_REFACTOR_AUDIT_2026-10-17.md` opens by
recording that it corrected a construction report after finding a regression that report had introduced.

| SHA | subject | files |
|---|---|---|
| `ad5bbb7e1fa` | `test(netcap): pin the reclaim policy a transition actually asks for` | `route/network_transition_reclaim_reason_test.go` (+147) |
| `aa9ce1d2255` | `test(netcap): pin the DNS cache boundary of a network transition` | `dns/network_reset_cache_boundary_test.go` (+99), `route/network_transition_dns_cache_test.go` (+91) |
| *(hash in `git log` — `remove the familyHealth invalidators that nothing called`)* | `refactor(dialer): remove the familyHealth invalidators that nothing called` | `common/dialer/dual_stack_scheduler.go`, `common/dialer/dual_stack_scheduler_test.go` |
| *(hash in `git log` — the report and its corrections)* | `docs(knowledge): record the Phase 01 network capability transition audit` (+ corrections) | `KNOWLEDGE/reports/core/NETWORK_CAPABILITY_TRANSITION_PHASE01_2026-10-18.md`, `KNOWLEDGE/reports/README.md` |

**One production file changed, and only to delete dead code.** `common/dialer/dual_stack_scheduler.go`
lost `familyHealth.invalidateGeneration` / `invalidateAll`, which had no production caller (§U.2); the one
test that used the first was rewritten to assert the design instead of the method. No behaviour changed:
`go build ./...`, `go vet ./common/dialer` and the seven `familyHealth` tests pass, and the mutation that
proves the generation scoping (M3) still fails the same five tests.

That was the whole of the code change, and it is not an omission: the capability producer, its
publication, its generation and stability semantics, and its consumers already implement the contract this
phase was asked to construct, and the audits in §B–§R are the evidence. What was missing was that two of
the decisions holding that contract together would have survived being broken — commits 1 and 2 — plus one
signpost pointing the wrong way — commit 3.

**Verification run on this host at the final SHA:**

```text
go build -tags "$TAGS" ./...                      PASS
go vet -tags "$TAGS" ./route ./dns                PASS
go test -tags "$TAGS" ./common/netcap             ok
go test -tags "$TAGS" ./common/dialer             ok   14.1s
go test -tags "$TAGS" ./dns                       ok    7.9s
go test -tags "$TAGS" ./route                     ok  421.7s
gofmt -l .                                        clean
git diff --check                                  clean
go test -race ...                                 BLOCKED_BY_ENVIRONMENT (no cgo, no mingw-w64 C toolchain)
```

`TAGS` is `release/DEFAULT_BUILD_TAGS_OTHERS`. `DEFAULT_BUILD_TAGS` is the Darwin profile despite its
name and cannot build on Windows (`AGENTS.md`).

---

## X. Final verdict

1. **What is the authoritative source of capability?** `route.NetworkManager`, and nothing else. It is the
   only caller of `netcap.Evaluate` and the only producer of the value published through
   `adapter.NetworkFamilyCapabilitySnapshotter`. `netcap` is the model and never learns which platform it
   is on. No dial-path code enumerates interfaces or routes to build a family verdict (§C).

2. **Which events change the capability?** `postUpdateNetworkEnvironment` (route / address / DNS-registry
   events from the network monitor), `notifyInterfaceUpdate` (default-interface change, after the
   monitor's 1 s debounce), `UpdateInterfaces` (platform-supplied list), `onWIFIStateChanged`,
   `ResetNetwork`, the Windows resume path, and `EstablishInitialNetworkEnvironment` at Start. All of them
   funnel through one function, `recomputeNetworkEnvironment` (§B).

3. **Which events bump the generation?** The four claim sites in §D. Capability changes bump it **only**
   when the environment fingerprint moved with them; a same-interface route change republishes the
   capability under the incumbent epoch, and a same-interface address change bumps it ≈1 s later via the
   interface monitor. There is no capability-specific counter.

4. **Is a same-interface route/address change covered?** Yes for the capability, and deliberately not by a
   generation bump. Route changes update `GlobalIPv6Egress` immediately under the incumbent epoch; address
   changes update the family term and bump the epoch one debounce later. Rows 10–13 of §G; `VERIFIED_UNIT`
   for both directions, and `VERIFIED_MUTATION` (M1, M16) that the coverage is real.

5. **Can a Settling state be published as a Stable hard verdict?** **No.** The capability value is
   published while unsettled, but it cannot be *acted on*: `localDialOwnership.owns()` requires the capture
   to have begun settled, the network to be settled now, and the epoch unchanged. `VERIFIED_MUTATION` (M4):
   deleting the "began settled" term fails six tests. The producer's own ordering also makes the
   dangerous combination unobservable: `beginTransition` stores unstable **before** the token under one
   lock, and the capability is stored after the claim while `stateAccess` is still held.

6. **Does a flow consume only the published snapshot?** Yes. One `networkObserver` resolved at
   construction, one `NetworkFamilyCapabilitySnapshot` per observation, one ownership read per candidate
   plan, and no exported field that bypasses the ownership gate (§M).

7. **Can a flow still probe or re-capture the capability itself?** No. The dial path has no interface
   enumeration and no route read; `benchmark` + `TestTheCapabilityCaptureTakesNoLockAndNoSyscall` show a
   capture is an atomic load. `protocol/direct`'s `fetchMyAddresses` reads this process's *own* addresses
   for sniffing and route purposes and contributes nothing to the capability (§C).

8. **Is `familyHealth` strictly generation-scoped?** Yes, and structurally so: the key is the epoch, taken
   from the *same capture* as the verdict and ownership it accompanies. `VERIFIED_MUTATION` (M3): keying
   both sides on one constant fails five tests. **Caveat recorded in §N/U.2:** the two `invalidate*`
   methods have no production caller, so the scoping is a property of the key, not of a clearing step —
   do not read those methods as the mechanism.

9. **Can an old recovery result or a late candidate leak across generations?** Not into a decision. A late
   arrival is filtered by a **fresh** policy read, so a superseded verdict cannot drop it; a setup is
   cancelled by exactly one generation transition and released before establishment, so a transition
   cannot retroactively cancel an established connection. `VERIFIED_MUTATION` (M5, M6) and
   the six `TestOldVerdictCannotDenyNewGeneration*` tests.

10. **Is the DNS cache flushed unnecessarily?** No. `dns.Router.ResetNetwork` advances the generation,
    purges the reverse mapping, resets and re-pins the transports, and leaves both the exact cache and the
    NXDOMAIN cache alone; entries of the previous environment simply stop being reachable. **This was
    unpinned until this round** — M9 and M19 both survived — and is now pinned at both layers (§R, §T).

11. **Does the new capability wrongly affect an old established connection?** No. A capability change
    creates no reclaim call at all; a transition drains (`ReclaimNetworkTransition`) and only a confirmed
    2 s dead path closes (`ReclaimDeadPath`). `VERIFIED_MUTATION` (M10, M17).

12. **Is a second `CapabilityRevision` needed?** **No.**

13. **If not introduced, why is the existing generation sufficient?** Because the capability is a
    *value*, not a clock: consumers re-read it through one atomic load and the value itself carries every
    capability change, including the ones that do not move the epoch. The only fact a revision could add
    is "this value changed while the epoch did not" — which the consumer does not need, because a
    mid-dial capability change is safe by construction (the address it produces is a real connection on a
    real socket, and the case the gate exists for fails instantly on its own). Every one of the six
    questions §10 requires before adding a counter is answered by existing machinery (§D), and the
    alternatives are each worse: a revision cannot cancel a setup (the capability change is exactly the
    case that must **not**), must **not** key family health (a soft penalty would survive the reset that
    should forget it), and turns "last claim wins" into a two-variable decision with no owner.

14. **(n/a — nothing was introduced.)**

15. **Is the hot path still cheap?** Yes, and measured: 10–16 ns and zero allocations for the gate, 48 ns
    and one 64-byte allocation for one capture, no lock and no syscall per flow. One route-table read per
    **event**, no timer, no goroutine, no per-flow enumeration (§S).

16. **Which real new bugs were found?** **No live correctness defect in the capability producer.** The
    round's substantive finding is that two boundary decisions holding the contract together were
    unpinned — a network transition flushing the DNS cache (M9/M19) and a confirmed dead path draining
    instead of closing (M17) — and both would have survived a regression. The one finding that was acted
    on in code is the two dead `familyHealth` invalidators, removed (U.2). Three observations are recorded
    rather than fixed: the route/addressing
    freshness asymmetry with its ≤1 s self-correction (U.3), the absence of any watcher for a NON-default
    interface's addressing (U.8), and the Windows IPv4-table default-interface picker reading an
    IPv6-only network as a dead path (U.5, upstream). The medium residual risk is Darwin:
    a platform-monitor client has no separate route-change callback, so a same-interface route change may
    never reach the producer there — `VERIFIED_SOURCE`, needs a device run (V.4).

---

## PHASE 01 HANDOFF

```markdown
## PHASE 01 HANDOFF

- testing HEAD:
    aa9ce1d2255  test(netcap): pin the DNS cache boundary of a network transition
    ad5bbb7e1fa  test(netcap): pin the reclaim policy a transition actually asks for
    plus the report commit on top (hash = `git log -1 --oneline origin/testing`)
  Last commit that changes code:  aa9ce1d2255
  Start SHA for the round:        f868a8fb49efc66b8e0b9b97229a53171c50b053
  Production files changed in 01: none. The two commits are tests only.

- Capability authority:
    route.NetworkManager  (route/network_family_capability.go)
    Sole caller of netcap.Evaluate; sole writer of the published value.
    Inputs: r.networkInterfaces (atomic, up-only, platform-or-finder),
            interfaceMonitor.MyInterfaces() (this process's own TUN/bridges),
            machineGlobalIPv6Route() (netlink / GetIpForwardTable2 / BSD RIB; NO cache).
    Model: common/netcap (evaluate + scope + the single HardUndialable decision point).

- Snapshot type/API:
    adapter.NetworkFamilyCapabilitySnapshotter        (adapter/network.go:192)
      NetworkFamilyCapabilitySnapshot() (epoch uint64, stable bool, capability NetworkFamilyCapability)
    adapter.NetworkFamilyCapability{ Capability netcap.FamilyCapability }
    netcap.FamilyCapability{ IPv4, IPv6, GlobalIPv6Egress FamilyState; reachable int }
    FamilyState: Unknown(0) | Unavailable | Provisioned   ; Validated, Degraded RESERVED - nothing produces them
    02 MUST consume this call. It is one observation: epoch, settled flag and verdict under one lock.
    Do NOT add a getter that returns the verdict without the ownership pair.

- Generation source:
    NetworkManager.networkResetGeneration (monotone) -> transitionToken (network.go:979)
    claimed ONLY in NetworkManager.beginTransition() (network.go:997)
      - transitionStable=false FIRST, then the token, under transitionAccess
      - runtimeCoordinator.Advance(epoch) AFTER the lock is released (ABBA hazard)
    settled ONLY in commitTransition(token) (network.go:1083), and only by the owning token
    Four claim sites: recomputeNetworkEnvironment, notifyInterfaceUpdate, ResetNetwork,
    notifyWindowsPowerEvent (+ EstablishInitialNetworkEnvironment at Start).
    ONE clock. No CapabilityRevision, no second epoch. Do not add one - see report §D.

- Stability states:
    Settling  = transitionStable == false        (claimed, body or a successor's body not yet committed)
    Stable    = transitionStable == true
    DeadPath  = NOT a capability state. networkIsConfirmedOffline() (pause held >= HardTransitionConfirm,
                2 s) selects ReclaimDeadPath for ESTABLISHED connections only.
    Rule: the verdict may be published while Settling; it may not be ACTED ON while Settling.
    Enforcement lives in the consumer: localDialOwnership.owns() = began settled && settled now && same epoch.

- Events that invalidate/rebuild capability:
    postUpdateNetworkEnvironment   networkMonitor (route/addr/DNS-registry) -> recompute, claims only if
                                   the environment FINGERPRINT moved
    notifyInterfaceUpdate          default-interface change (>=1 s debounce in sing-tun) -> ALWAYS claims
    updateInterface                the single reset decision for {environment change, pending interface change}
    UpdateInterfaces               platform-supplied list; recompute ONLY if the observation succeeded
    onWIFIStateChanged             SSID is a fingerprint term
    ResetNetwork(ctx)              libbox RPC, ReleaseMemory, power suspend
    notifyWindowsPowerEvent        resume
    EstablishInitialNetworkEnvironment   synchronous at Start
    FINGERPRINT = default interface gateways | Wi-Fi SSID | gateway hardware address.
    A capability change that does not move the fingerprint is published under the INCUMBENT epoch
    with settled untouched (deliberate: a capability change is not a network transition).

- Publication ordering:
    recomputeNetworkEnvironment, all under stateAccess:
        fingerprint written
        [test hook environmentPublished]
        beginTransition()          <- THE LINEARIZATION POINT; marks unstable, then takes the token
        storeFamilyCapability()    <- verdict published AFTER the claim, still under stateAccess
        [test hook familyCapabilityPublished]
    then, outside stateAccess: resetRunAccess -> resetNetworkLocked -> commitTransition(token)
    DNS barrier order inside resetNetworkLocked's last statement:
        dns.Router.ResetNetwork: generation++ and reverse-mapping purge in ONE critical section,
        THEN transports Reset(), THEN re-pin environments. The exact cache is NOT touched.
    02 must not publish a verdict outside this section, and must not reorder the claim after the store.

- Unknown vs Unavailable rule:
    Unknown       = no opinion. FAIL OPEN. It is the zero value and the degradation for every missing
                    capability. It refuses nothing.
    Unavailable   = a structural impossibility, the ONLY hard negative:
                      family term  : usable interfaces exist, >=1 carries a valid address, none carries
                                     this family
                      egress term  : routes were READ and none was navigable
    An interface that is up but carries no address of either family is NOT evidence -> Unknown.
    "The resolver returned nothing" cannot reach the model; Evaluate has no resolver-shaped input.
    Scope rule (netcap.HardUndialable, the single decision point, takes the DESTINATION):
      local  (loopback, link-local, unspecified) -> always dialable
      site   (fc00::/7)                          -> always dialable
      global                                     -> family must exist; for IPv6 also GlobalIPv6Egress
    Never spell Unknown as Unavailable. Never refuse a local or site destination for a global reason.

- FamilyHealth generation relationship:
    familyHealth is map[uint64]familyHealthState KEYED BY THE RESET EPOCH, and the key is read from the
    SAME capture as the verdict (DefaultDialer.captureLocalFamilyPolicy().ownership.epoch).
    Not a clearing step. Nothing clears it on a transition; the new epoch is a new bucket.
    Bounded at familyHealthMaxGenerations = 256 (clear-at-cap); lazy 15 s expiry (= C.TCPTimeout);
    cleared by a success in that family.
    Soft only: it makes both families start together; it NEVER removes a candidate. Capability is hard
    fact, health is experience - do not merge them.
    NO invalidateGeneration / invalidateAll exists any more - they had no production caller and were
    removed in this round (see U.2). Nothing clears this map on a network change; the new epoch is a new
    bucket, and the only removal paths are the expiry in fallbackImmediately, recordSuccess and
    clear-at-cap in storeLocked. Do not re-add a clearing method without its caller.

- Settling flow behavior:
    A dial that begins during Settling must NOT hard-deny: localDialOwnership records startedStable and
    owns() returns false, so the gate refuses nothing (fail open). The capture is refused again at
    hand-over because the epoch moved.
    LocalFamilyDialer (native bypass) uses the SAME policy with OPPOSITE polarity: an unowned capture
    answers "not blocked", because a block only disables an optimisation.
    A late candidate is judged by a FRESH policy read, not the plan's gate: a verdict captured seconds
    ago must not drop an address the successor generation could dial.

- Explicitly NOT solved in 01:
    * Candidate Admission / FlowDecisionContext convergence. 02's job. 01 only guarantees ONE published
      snapshot and ONE ownership gate.
    * Any active probing (ICMP/HTTP reachability). `GlobalIPv6Egress=Unavailable` means "no route", never
      "no public IPv6 answered".
    * A producer-side quiescence/dwell window. A route flap flips the egress term under the incumbent
      epoch with no settle time (report §V.2). Deliberate: a per-transition timer is what §26 forbids.
    * DeadPath inside the capability. It stays a reclaim classification (report §E, §V.3).
    * Darwin: a platform-monitor client has no separate route-change callback, so a same-interface route
      change may not reach the producer at all (report §V.4, MEDIUM - needs a device run).
    * Windows: the default-interface picker reads only the IPv4 forward table, so an IPv6-only network
      reads as "no default interface" -> pause -> ReclaimDeadPath after 2 s (report §U.5; upstream
      sing-tun, out of scope).
    * The freshness of the capability's ADDRESSING view. It is refreshed by three writers and no other
      event, so on the route-only path it can be one interface-monitor debounce old (§U.3) and on a
      multi-interface host a non-default interface's change may not reach it at all (§U.8). Both are
      bounded fail-open-ish and both are recorded as residual risks V.1/V.8, not fixed.
    * The IPv4 mirror of GlobalIPv6Egress (no IPv4-egress term; an IPv4 destination with no IPv4 default
      route is attempted and fails in the kernel). Explicitly not modelled.

- Files/contracts 02 must consume:
    adapter/network.go        NetworkFamilyCapability, NetworkFamilyCapabilitySnapshotter
                              (+ NetworkTransitionSnapshotter, NetworkResetCounter, NetworkTransitionState
                               - narrower, kept for managers that cannot do better)
    common/netcap/netcap.go   FamilyCapability, FamilyState, DestinationScope/ClassifyScope,
                              Evaluate, UsableInterfaces, HardUndialable, HardUnavailable
    route/network_family_capability.go    localFamilyCapability, storeFamilyCapability, observation path
    route/network_environment.go          recomputeNetworkEnvironment (the ONLY publication point),
                                          boundEnvironmentTransition{Exported,Locked}
    route/network_family_route.go         usableGlobalIPv6Route (the shared platform predicate)
    route/network.go                      beginTransition / commitTransition / transitionOwns / noTransition,
                                          NetworkFamilyCapabilitySnapshot, resetNetworkLocked
    common/dialer/network_observation.go  networkObserver (ONE resolution), ownership()
    common/dialer/family_capability.go    localDialOwnership, localFamilyPolicy, localFamilyGate,
                                          captureLocalFamilyPolicy, newLocalFamilyPolicy
    route/dial_setup.go                   dialSetupGate (setup ownership), advanceAndBump (one clock)
    route/conn_reclaim.go                 ReclaimReason, reclaimPolicyFor, networkTransitionReclaimer
    common/dialer/dual_stack_scheduler.go familyHealth (generation-keyed)
    Tests to keep green (the contract, not the implementation):
      route/network_transition_dns_cache_test.go          TestANetworkTransitionIsABarrierAndNotACacheFlush
      route/network_transition_reclaim_reason_test.go     TestConfirmedOfflineIsReclaimedAsADeadPath,
                                                          TestAGapThatHasNotHeldIsStillADrain,
                                                          TestAnOnlineTransitionIsADrain
      dns/network_reset_cache_boundary_test.go            TestResetNetworkStillServesACachedAnswerOnTheSameNetwork
  02 must NOT re-derive a family verdict, must NOT read the manager twice for one decision, and must NOT
  introduce a second epoch.
```
