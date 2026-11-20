# Quadruple A — QUIC, NE, OOM and NAT: four-way timing

First window of the four-system round. The assigned rows were Q02 (network change × NetworkManager ×
QUIC pool × stream), Q03 (OOMKiller × ResourceGuard × UDP NAT × outbound `PacketConn`), Q07 (MASQUE
NAT64 × DNS bootstrap × QUIC path × network change) and Q08 (iOS NE × Cronet/native × memory pressure
× speed-test cancellation).

- Repository: `Piggy-Cat-bit-shadow/JiejieBox`, branch **`testing`**, no new branch, HEAD `5661fc9e5`
- Platform: Windows amd64, Go **1.27.4**; no Administrator token, no Linux netns/nftables, no
  NAT64/PREF64 network, no iOS device or NE
- Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` / `NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`

## 0. Result in one paragraph

**This window fixed nothing, because it found nothing it could execute that was still broken** - and
that sentence has a price: two of the four rows cannot be executed on this host at all, and one of
them only in half. What it did do is run the suites that own the executable arms of Q02 and the
desktop half of Q08, and state per arm which of the four systems was actually present. The honest
summary is: the dangerous four-way interactions in this area were already found and fixed in the
preceding rounds (the gate's re-entrancy, the post-teardown client creation, the double-executed
event), and what remains is blocked by the environment rather than by the code - Linux's NAT table, a
NAT64 fabric, and an iOS device.

## 1. Q02 - the executable arms hold

The four systems are (1) the platform's interface event, (2) `runtimecoord.GenerationGate` with its
once-per-epoch dedupe, (3) the QUIC carrier (the `familyCarrier` and the single-client path), (4) the
in-flight dial or stream. The suites below were run here, with the general tag profile:

| Behaviour | Test | Result |
| --- | --- | --- |
| the listener-phase notification is routed *through* the gate and the action is the reset, not the gated listener | `TestTheListenerTransitionIsNotReentrant` | green |
| a second notification for the same generation is deduplicated (one event must not execute the destructive action twice) | `TestTheSecondNotificationIsStillDeduplicated` | green |
| `InterfaceUpdated` reaches the gate (the push entry is not bypassed by a shortcut) | `TestInterfaceUpdatedRoutesThroughTheGenerationGate` | green |
| after the Box teardown a dial is refused as a teardown and creates **no** client (P9) | `TestAShutdownCarrierNeverCreatesAClientAgain` | green |
| after a *network change* the carrier must remain able to build a client (the reset is not a shutdown) | `TestANetworkChangeLeavesTheCarrierReusable` | green |
| two families under concurrent demand | `family_concurrency_test.go`, `family_lifecycle_test.go` | green |

The arm that is **not** covered is the fourth one: a dial or stream **in flight** when the event
arrives, with a real NAT rebinding in front of a real hysteria2 server. Building it needs a
controlled UDP proxy that changes its upstream source port mid-handshake plus a local QUIC server;
this window chose not to start it rather than to fake it with a blackholed handshake whose "failure"
would prove nothing about rebinding.

Note also that the client side does not migrate QUIC connections in this fork
(`docs/fork/interface-update-lifecycle.md` records the audit), so "migration" here can only mean the
server seeing a new 4-tuple - which is exactly the proxy experiment above.

## 2. Q03 - the NAT half is not observable here

The four systems are (1) the OOM killer's pressure fact, (2) the guard's admission plan, (3) the
`sing-tun` NAT table and its per-entry queue, (4) the real UDP `PacketConn` and its goroutines. The
third one does not exist without a TUN device, which needs an Administrator token this session does
not have. What could be run - the guard and the OOM killer - was run and is green, but a green guard
unit test is not the four-way claim and is not presented as one.

The UDP admission question itself (the NAT gate is not on the UDP path) is window A's R-4 from the
pairwise round: a **product decision** about whether unadmitted UDP flows are acceptable, with the
4096-entry table as the only bound. It is not re-counted here.

## 3. Q07 - NAT64 fabric missing

`transport/masque` builds and its suite is green (9.4 s), but nothing in it synthesizes a PREF64
discovery result, a delayed bootstrap answer and an epoch change at once. Without a NAT64 network the
four-way timeline cannot be produced, so the row is `NOT_OBSERVABLE`, not "probably fine".

## 4. Q08 - split, as the brief requires

- **Desktop half, executed:** the Go-side cancel chain. The Clash API's per-proxy and per-group delay
  handlers return when the request context is cancelled (`TestClashDelayIsCancelledWithTheRequest`,
  `TestGenericGroupDelayIsCancelledWithTheRequest`), and the URL-test coordinator survives cancel at
  the grant boundary with every waiter leaving holding either a slot or its own cancellation
  (`TestCoordinatorSurvivesCancelAtTheGrantBoundary` plus the lifecycle stress). Green here.
- **Device half, `NOT_OBSERVABLE`:** `phys_footprint` time series, Jetsam/NE termination, native
  Cronet session ownership after a cancelled test. No Windows RSS number is offered as a substitute,
  because the quantity is the NE process budget, not the Go heap.

## 5. What this window says about the four-system method

The four-way rows in this area are not equally attackable, and the honest ranking is:

1. **Q02's in-flight arm** is the one worth building next: it needs a UDP proxy, a local QUIC server
   and two streams, all of which exist in this environment - only the harness is missing.
2. **Q03's NAT arm** and **Q07** need privileges/networks that the session does not have.
3. **Q08** needs a device for its actual question; the Go half is already clean.

## 6. Gates

Seven suites green with the general tag profile (hysteria2 1.16 s, tuic 1.10 s, masque 9.42 s, urltest
8.36 s, clashapi 0.58 s, resourceguard 1.07 s, oomkiller 0.88 s), and the four concurrency-heavy
owners green under `-race` (hysteria2 2.55 s, urltest 9.85 s, resourceguard 2.15 s, clashapi
1.69 s). No production code changed, no
dependency pin moved; `release/source-lock.json` and `release/pin-runs.json` are untouched.
