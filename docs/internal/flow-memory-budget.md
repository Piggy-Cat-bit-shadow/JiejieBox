# What a held flow costs, and why the budget is written in the build's geometry

This is the short record for the per-flow half of the memory census. The node half already lives in
`outbound_cold_cost_test.go`; this is the increment a Network Extension's budget is actually spent
on, and the half that decides whether forty held flows fit.

Companion gate: `held_connection_cost_test.go` (repo root), run from an existing step in
`.github/workflows/verify.yml` in both buffer geometries.

## 1. Method

* A real `box.New` with a SOCKS inbound and a direct outbound, started through `Start()`.
* 64 connections established through it to a listener that accepts and then says nothing, so both
  relay directions park in a read and nothing is moving at the sample.
* Sample = `/gc/heap/live:bytes` and `/memory/classes/heap/stacks:bytes` from `runtime/metrics`
  plus `runtime.NumGoroutine()`, taken after **three** forced collections (the relay's per-connection
  garbage must be gone; two is what the node census uses and it holds less churn).
* The figure reported is the **delta per connection**, not a total: a total is not comparable
  between processes, machines or builds.

## 2. Numbers

| build | stacks per flow | live heap per flow | goroutines per flow |
|---|---:|---:|---:|
| default geometry, Windows/amd64 | 16 384 B | 43 279 B | 2.00 |
| `with_low_memory` (what iOS/tvOS ship), Windows/amd64 | 15 872 B | 26 977 B | 2.00 |
| default geometry, Linux/amd64 (CI) | 8 192 B | 41 807 B | 2.00 |
| `with_low_memory`, Linux/amd64 (CI) | 8 704 B | 25 420 B | 2.00 |
| after every flow is closed | goroutines return to the baseline | — | — |

Repeat runs on one machine spread by about 90 bytes on the heap figure, so the number is the
relay's and not a sample of noise.

Two things follow from the split, and both are properties of the relay rather than of this test:

* A held and quiet flow keeps **one** `buf.BufferSize` buffer, not two: the direction with data
  pending holds its cached reader, and the other direction's cached writer allocates on first
  write. That is why the desktop figure is 43 KiB rather than 75 KiB.
* The stack figure is two parked goroutines at the platform's starting stack size. Linux starts at
  2 KiB and Windows at 8 KiB, which is why the same build measures 8 KiB on one runner and 16 KiB
  on the other. A per-flow stack budget written as a constant across platforms would be wrong on
  one of them.

## 3. The budget

```
per-flow live heap <= buf.BufferSize + 14 KiB
```

44 KiB in the default build, 28 KiB under `with_low_memory`, and the assertion is on the geometry
the artifact was actually compiled with. A constant budget is slack on one geometry and hides a
doubled buffer on the other; a doubled buffer is exactly the regression this exists for, and it
costs +16 KiB on the mobile geometry.

The 14 KiB is 12 KiB of measured bookkeeping plus room for the platform spread (the same build is
43.3 KiB on Windows against 41.8 KiB on Linux), because a 1.7 KiB headroom would fail on a platform
it has never been measured on for a reason that is not a regression.

`phys_footprint` is **not** this number and is not claimed anywhere: a host test cannot see a
Network Extension's footprint, and what is measured here is the relay, not the tunnel device.

## 4. What this round did and did not do

| package | decision | why |
|---|---|---|
| A node/instance separation | **not done** | The deferral's target is already named (wireguard 3×NumCPU+2 goroutines + a UDP socket per idle node, plus tailscale/openvpn/openconnect/masque). It moves `listen_port` binding and MTU validation from `Start` to first dial and needs a real TUN plus a signed Apple build to verify; the previous round refused it for the same reason. Changing it unverified would touch the semantics 40+ wireguard lifecycle tests protect. |
| B per-held-flow cost | **shipped** | §2-§3. |
| C probe-session release | **not done** | Needs a new adapter-level "probe finished" hook and session generation tracking; the judgement "this session has no user activity" can only be validated on a device, and a wrong one closes a flow the user is using. |
| D burst buffer budget | **not done, and the earlier reasoning was wrong** | This document used to say a global borrow budget would be a second budget beside ResourceGuard's. It would not: `common/resourceguard/growth.go` already implements exactly that (`GrowthBudget`, with per-pressure-level flow and reserve caps, geometry-required exemptions, and permits released when a copy direction ends), and `route/conn.go` already wires it into the real relay per direction. The honest statement is that the mechanism exists and this round did not review it against Clash's shape; the questions that review should answer are listed in §4.1. |
| E reload double peak | **not done** | Needs a real reload at 1000/10000-node scale. Measurable on this host, but it changes load ordering and rule completeness is the thing it must not break; left whole rather than half-done. |
| F attribution | **half shipped** | The per-flow half is §2; the node half predates this. |

The honest ranking of what is left is A first - it is the largest single win in the recorded
inventory - and it is blocked on an environment that can run a Network Extension, not on analysis.

## 4.1 What the existing growth budget still needs reviewed

`common/resourceguard/growth.go` + `route/conn.go`'s `growthThreshold` is the global borrow budget
this document once claimed did not exist. It resolves per copy direction, refuses under pressure
instead of widening, exempts a direction whose writer needs the larger buffer for framing, and
releases the permit when the direction's copy ends. What that leaves open, in the order it is worth
measuring:

1. **The permit is taken before either copy starts**, so a held-and-quiet flow occupies one permit
   per direction for its whole life. At 64 held flows that is 128 permits; whether the normal-level
   `Flows + InteractiveReserve` cap reaches that with a real profile is unmeasured, and if it does,
   the refusal lands on flows that would have been quiet anyway.
2. **Growth is one-way.** `CopyWithIncreateBuffer` enlarges; nothing returns a grown connection to
   the small geometry when the traffic stops, so a burst leaves large buffers attached until the
   flow closes.
3. **The geometry-required exemption is uncapped by design.** A direction that asks for
   `CopyGrowthImmediate` is allowed and counted but not charged against the shared cap, so a
   protocol with framing reasons can add resident buffers that the cap does not see. Its real peak
   is unmeasured.
4. **The cap's capacity is not derived from the NE budget.** The numbers in the growth plan come
   from the tunnel's own sizing, not from anything that models a 50 MiB-class extension.

None of the four is established as a defect; each is a question the existing mechanism can answer
with the per-flow harness this round added, which is why they are written down rather than fixed.

## 5. Reproduce
```sh
go test -count=1 -run TestAHeldConnectionStaysWithinItsBudget -v .
go test -tags with_low_memory -count=1 -run TestAHeldConnectionStaysWithinItsBudget -v .
go test -count=1 -run TestAHalfClosedFlowStillDeliversTheOtherDirection -v .
```

## 6. The half-close gate, and why an idle budget cannot replace it

`held_connection_cost_test.go` measures a connection that is held and **quiet**. That is the cheap
state, and it is also the state in which a broken relay looks perfect: nothing is flowing, so nothing
can be lost. `half_close_flow_test.go` covers the state a proxy is actually in every time an HTTP
client finishes its request - the client half-closes, the server still has a response to send:

```
client writes a request, then CloseWrite  ->  destination sees the bytes and then EOF
destination writes a response, then closes ->  client sees the bytes and then EOF
```

A relay that turns `CloseWrite` into a full `Close` fails at the response; one that forgets to
propagate it fails at the EOF; one that leaks a copy fails the teardown check. None of the three
would move a single byte of the idle budget. It passes on this tree, so it is a gate rather than a
fix.

## 7. What was audited and came back clean

`common/httpclient/managed_transport.go` is the component the brief names for "closed must not
resurrect", and it was read line by line for the epoch/close/reset race before anything was written.
It already carries seven dedicated tests for exactly that invariant
(`TestARequestAfterCloseDoesNotResurrectTheTransport`,
`TestAResetAfterCloseDoesNotResurrectTheTransport`, `TestCloseIsIdempotentAndStillReportsOnce`, the
`Reset`/epoch pair and the `CloseIdleConnections` pair), including the `closed` flag that makes
"closed" a different answer from "not built yet" inside `rebuildAccess`. The `acquireEpoch` retry
loop and the `marked`-before-`active` ordering in `retireEpoch` are both the correct shape. No defect
found, and no test added: a second test for a covered invariant is maintenance, not evidence.
