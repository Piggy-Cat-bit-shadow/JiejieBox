# PAIRWISE A — Resource / Memory / Connection lifecycle: pair matrix

Window A of the JiejieBox cross-system pairwise debug. This file is the routing table: which pairs
were attacked, who owns each side, what state boundary they share, what was actually executed, and
where the evidence is. The conclusions are in `PAIRWISE_A_FINDINGS.md`; the mechanism chain and the
mutation controls are in `PAIRWISE_A_EFFECTIVENESS.md`; commands, counts and limits are in
`PAIRWISE_A_VERIFICATION.md`.

- Repository: `Piggy-Cat-bit-shadow/JiejieBox`, development branch **`testing`**
- Starting HEAD: `7654f1a77` (`testing` == `origin/testing` at the start; nothing else was modified)
- Toolchain: Go **1.27.4** (the `toolchain` line of `go.mod`), `CGO_ENABLED=1` with the bundled
  mingw64 gcc for `-race`, tags as noted per package
- Labels used: `已确认` (confirmed with source + execution), `已排除` (checked, no defect found),
  `未触发` (candidate exists, no evidence it fires), `无法观测` (cannot measure here), `未覆盖`
  (not attacked in this round)

## 0. Why these pairs

The question was not "does each module work" but "do two correct modules own the same resource
correctly". Everything below starts from a shared object with one owner too many or one owner too
few: the admission gate has a reclaim-hook slot that a protocol can fill; the OOMKiller has a
pressure atomic that a guard copies; a pool has a close path that a second subsystem can call.

## 1. Owner map (as of `7654f1a77`)

| Resource / state | Owner (authority) | Where | Reachable in which build |
| --- | --- | --- | --- |
| Process memory-pressure state | `service/oomkiller` (`Service.pressure atomic.Uint32`) | `service/oomkiller/service.go:34,120-129` | any build with `oomkiller` service; guard only in Network Extension mode |
| Admission policy / level → gate plan | `common/resourceguard.Guard` | `common/resourceguard/{guard,gate,admission,rate,growth}.go` | installed only when `policyModeNetworkExtension` |
| Expensive-creation seams | `common/tls` (TLS handshake), AnyTLS session hook, QUIC clients, naive native creation, Mieru underlay, XHTTP/H3 | `common/tls/client.go:185-216`, `protocol/*/admission.go`, `protocol/naive/native_creation.go:269-288` | per protocol build tag |
| Reclaim hooks on Critical | `Guard.runShed` → protocol `CloseIdleConnections` | `common/resourceguard/guard.go:264-277,397-416` | anytls, trusttunnel, naive (before this round) |
| Progressive trim / aggressive release | `oomkiller` timer → `NetworkManager.TrimMemory/ReleaseMemory` → `route/ReferenceManager` walk | `service/oomkiller/timer.go:817-1036`, `route/reference.go:601-625,658-677` | all |
| Idle-only releaser capability | `adapter.IdleConnectionReleaser` (`CloseIdleConnections`) | `adapter/outbound.go:324-338` | implemented by anytls, hy2, tuic, trojan, vless, vmess, hysteria, ssh, snell, shadowsocks, trusttunnel |
| Physical pool lifetime per protocol | each protocol client (SDK or in-tree) | e.g. `transport/trusttunnel/client.go`, `transport/v2rayxhttp/xmux.go`, `protocol/hysteria2/family.go` | per protocol |
| UDP NAT entries | `sing-tun` `UDPNat` (+ `route` per-flow sessions) | `sing-tun/udp_nat.go`, `route/conn.go:794-960` | TUN builds |
| Build reservation | `common/buildgate` (its own `resourceguard.Guard`, build-count dimension) | `common/buildgate/buildgate.go:160-171` | all |

Two authorities that look duplicated but are not: `buildgate`'s guard polices *builds* (provider /
rule-set compilation) with its own numbers and never installs reload hooks; the dial guard polices
*expensive creations*. They share the level source, not the ledger — checked, `已排除` as a
duplicate authority.

## 2. The pairs

### A01〔P0〕ResourceGuard × OOMKiller — authority, ordering, double reclaim

| | |
| --- | --- |
| Shared boundary | the pressure atomic (`Service.pressure`) and the guard's level→plan publication (`appliedVersion`, `transitions`) |
| Authority | OOMKiller publishes; the guard may only copy and map (`guardLevel`) |
| Attacked | ① does a published `Critical` actually stop a real creation *before* it allocates; ② does the recovery edge (Critical→Normal) hand capacity back; ③ is a `Critical` episode able to reclaim twice (guard shed + OOMKiller pass); ④ can a stale `observe`/`apply` publish an older plan |
| Executed | new pairing test `service/oomkiller/guard_reaches_creation_test.go`; new recovery-edge tests `common/resourceguard/gate_recovery_edge_test.go`; existing `guard_publish_race_test.go`, `guard_cycles_test.go`, `reclaim_bound_test.go`, `reclaim_overlap_test.go`, `poll_ordering_test.go` re-run |
| Conclusion | ④ `已排除` (existing pins hold). ③ `已排除` as *harmful* for anytls/trusttunnel (their hook is idle-only and internally locked — see A02 evidence) — the hedge is bounded duplicate work, not corruption. ① `已确认` with mutation control. ② `已确认` and **fixed**: a raised plan never woke the queue. |
| Evidence | `PAIRWISE_A_FINDINGS.md` F-2; `PAIRWISE_A_EFFECTIVENESS.md` EC-1, EC-2 |

### A02〔P0〕Connection pool × lifecycle — borrow, reuse, retire, close

| | |
| --- | --- |
| Shared boundary | per-protocol pooled physical objects and the two walks that may close them (`ReferenceManager.retireIdleResources`, `Guard.runShed`), plus Box `Scope` teardown |
| Attacked | borrow-after-close, exactly-once close, trim-vs-inflight, post-teardown creation, quiet-lie (a pool that claims a releaser it cannot honour) |
| Executed | full `go test` for `protocol/anytls`, `protocol/trusttunnel`, `protocol/hysteria2`, `protocol/tuic`, `protocol/mieru`, `transport/v2rayxhttp`, `route`; plus two new pins |
| Conclusion | **F-1 `已确认` + fixed** (naive's only close primitive terminates live CONNECT streams and was registered as an idle reclaim hook). **F-3 `已确认` + fixed** (hy2/tuic had no closed state: a dial after teardown installed a client no later close could reach). Recorded, not fixed: trusttunnel `getSession` does not reserve the session it hands out (P2); anytls has no `ReuseSuspect` so the post-sleep boundary degrades to idle-only (P2); mieru implements no `IdleConnectionReleaser` (P2 gap). Ruled out: xhttp pool borrow/release (no defect by reading) — but see the pre-existing red tests in `PAIRWISE_A_VERIFICATION.md` §5, which contradict the reading and are **not** explained by this round. |
| Evidence | `PAIRWISE_A_FINDINGS.md` F-1, F-3, R-1..R-3 |

### A03〔P0〕Physical admission × protocol reuse / expensive init

| | |
| --- | --- |
| Shared boundary | the creation seam of each protocol, and whether a *reuse* is charged |
| Attacked | AnyTLS (create vs reuse ordering), TLS seam coverage, naive (Cronet dialer callback), UoT reuse on AnyTLS, MASQUE (endpoint-level tunnel) |
| Executed | source verification + existing admission suites re-run (`protocol/anytls`, `common/tls`, `protocol/naive` with `with_naive_outbound,with_purego`) |
| Conclusion | AnyTLS `已排除`: admission moved to the SDK creation hook (`AdmitCreation`), so an idle-session reuse never queues (`protocol/anytls/admission.go:15-48`). TLS seam `已确认`: one seam covers trojan/vless/vmess/DoT and skips a nested dial via `HeldPermit` (`common/tls/client.go:185-216`). naive `已确认`: admission sits at the native socket callback, not at `DialEarly`, so a CONNECT that reuses a pooled session is not charged (`native_creation.go:19-60,269-288`). UoT on AnyTLS `已排除`: UoT calls the session dial, which does not touch the gate. MASQUE `已排除` as an A03 gap: one endpoint tunnel, not a per-flow creation. |
| Evidence | `PAIRWISE_A_EFFECTIVENESS.md` EC-0; capability table §3 |

### A04〔P0〕iOS NE budget × HTTP/3 speed test × TrustTunnel

`未覆盖` / `无法观测`. There is no iOS device or NE process in this environment; a Go/Linux
high-water measurement would not be evidence about `phys_footprint` or Jetsam. `PAIRWISE_A_VERIFICATION.md` §6
records what a device run must measure. No device numbers are claimed anywhere in this round.

### A05〔P0〕TUN UDP NAT × ResourceGuard / OOMKiller

| | |
| --- | --- |
| Shared boundary | per-UDP-flow objects (NAT entry, handler goroutine, queued-byte budget, outbound packet session, copy goroutines) and the process creation policy |
| Attacked | entry cap vs real cost, admission-before-allocation, eviction vs the creation it launched, download-only flow lifetime, per-NAT byte budget × NAT instances, double release on Close |
| Executed | static audit of `sing-tun` (`c0855e6`) + `protocol/tun` + `route/conn.go`, cross-read against the guard's seams |
| Conclusion | `已确认` (by reading, not yet by execution): **N-1** the guard is not consulted anywhere on the TUN UDP path, so the 4096-entry table is the only bound on the real per-flow cost; **N-2** evicting a NAT entry does not cancel the creation it launched, so the table bound does not bound concurrent dials; **N-3** NAT lifetime is refreshed only by client→peer datagrams, so a download-only flow is destroyed one `UDPTimeout` after its last upload; **N-4** the 8 MiB NAT-wide queued-byte budget multiplies by the number of NAT instances (multi-queue × address-and-port-dependent mapping). `已排除`: double release / double close of an entry, entry created after `Close`, cleanup goroutine leak, heap corruption. None of these were fixed this round: N-1/N-2 need a change in the `sing-tun` fork plus a re-pin, which is a cross-repository move that must not be done on reading alone. |
| Evidence | `PAIRWISE_A_FINDINGS.md` R-4..R-7; `PAIRWISE_A_VERIFICATION.md` §6 |

### A06〔P1〕Queue backpressure × priority/fairness × cancel

| | |
| --- | --- |
| Shared boundary | `Gate` queue, waiter budget, interactive reserve, aging guarantee |
| Attacked | can recovered capacity reach queued callers; does a shrink revoke anything; is a cancelled waiter's slot handed on |
| Executed | new `gate_recovery_edge_test.go` (both directions), existing `permit_wait_test.go`, `queue_bound_test.go`, `gate_reserve_test.go`, `rate_test.go` re-run |
| Conclusion | the recovery edge `已确认` + fixed (F-2). Shrink semantics `已排除` (new pin). Cancelled-waiter hand-on `已排除` (`withdraw` hands the granted slot on; existing pins). Fairness/aging `已排除` (existing `gate_reserve_test.go`). |
| Evidence | `PAIRWISE_A_FINDINGS.md` F-2, F-2b |

### A07〔P1〕OOM reclaim × pool trim × network generation × reload

| | |
| --- | --- |
| Shared boundary | `releaseAccess` (serialises trim/release), pool generation counters, the guard's shed path |
| Attacked | two passes closing the same pool concurrently; a late callback landing in a new generation |
| Executed | existing `reclaim_overlap_test.go`, `reclaim_rhythm_test.go`, `reset_serialization_test.go`, `wake_drain_test.go` re-run; cross-read of `Guard.runShed` vs `adaptiveTimer.reclaim` |
| Conclusion | the OOMKiller side `已排除` (one executor under `releaseAccess`; `runCrisisReclaim` re-checks the armed line before the destructive step). The **guard's shed hooks are a second, unsynchronised executor** — `已确认` as a fact, `已排除` as harmful for anytls/trusttunnel because the action they run is idle-only and takes the client's own lock, and identical to what the walk calls; the one hook that was NOT idle-only is F-1 and is fixed. |
| Evidence | `PAIRWISE_A_FINDINGS.md` F-1; `PAIRWISE_A_EFFECTIVENESS.md` EC-3 |

### A08〔P1〕Diagnostics / metrics × memory crisis

`未覆盖`. `service/oomkiller/recorder*` and the extra-metadata producers were read only far enough to
confirm they do not change governance state (`guardMetadata`/`reclaimMetadata` are read-only
renderers, `service.go:143-426`). Re-entrancy, low-memory profiling cost and interrupted report IO
were **not** attacked.

### A09〔P1〕Long-lived connection / HTTP body × cancel / Close

`未覆盖` in this round. Two pre-existing failures in `transport/v2rayxhttp` at HEAD are recorded as a
starting point for it (`PAIRWISE_A_VERIFICATION.md` §5), and the A02 audit ruled out the xhttp pool's
borrow/release path by reading — which those failures disagree with, so they need their own round.

### A10〔P2〕Go runtime / GC × native caches / platform budget

`未覆盖`. No time-series, high-water or footprint measurement was produced; none is claimed.

### A11〔free〕Findings from the resource graph that were not on the list

- The naive shed hook (F-1) is exactly this class: a mechanism that was *connected*, logged, and
  tested as "the outbound offers an idle reclaim", while the action it registered terminates live
  tunnels.
- A **third** guard exists (`common/buildgate`) and is legitimate; recorded in §1 so a later round
  does not "unify" it.

## 3. Capability table actually used by the matrix

`CloseIdleConnections` (trim reachability) vs a guard shed hook, per owner:

| Owner | `CloseIdleConnections` | Trim walk reaches it | Guard shed hook | Hook action idle-only |
| --- | --- | --- | --- | --- |
| anytls | yes (`outbound.go:169-171`) | yes | yes (`:116`) | yes (SDK skips sessions with streams) |
| trusttunnel | yes (`:341-342`) + `RetireSuspect` | yes | yes (`:153`) | yes (skips `streams>0`) |
| naive | **no** | **no** | was `CloseAllConnections` | **no** → removed (F-1) |
| hysteria2 / tuic | yes (carrier forward) | yes | no | — (shed not registered) |
| mieru | **no** | **no** | no | — (recorded gap R-3) |
| xhttp / vless | yes (`vless/outbound.go` → transport) + `RetireSuspect` | yes | no | — |
| masque endpoint | no (only `SetKeepIdleConnections`) | intentional (`route/reusable_owner_inventory_test.go`) | no | — |
