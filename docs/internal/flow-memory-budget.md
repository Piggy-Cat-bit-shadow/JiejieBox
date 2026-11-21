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
| D burst buffer budget | **not done** | A global borrow budget would be a second budget next to ResourceGuard's admission, and the geometry is already 16 KiB on iOS with a bulk-grow path. Needs a burst measurement first. |
| E reload double peak | **not done** | Needs a real reload at 1000/10000-node scale. Measurable on this host, but it changes load ordering and rule completeness is the thing it must not break; left whole rather than half-done. |
| F attribution | **half shipped** | The per-flow half is §2; the node half predates this. |

The honest ranking of what is left is A first - it is the largest single win in the recorded
inventory - and it is blocked on an environment that can run a Network Extension, not on analysis.

## 5. Reproduce

```sh
go test -count=1 -run TestAHeldConnectionStaysWithinItsBudget -v .
go test -tags with_low_memory -count=1 -run TestAHeldConnectionStaysWithinItsBudget -v .
```
