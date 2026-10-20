# Dual-stack cross-component audit — closure

**Date:** 2026-10-20 (Asia/Shanghai)
**Repository / branch:** `Piggy-Cat-bit-shadow/sing-box`, `testing` only. No branch was created,
nothing was force-pushed, no workflow was dispatched, no tag or release was made.

| identity | value |
|---|---|
| `AUDITED_BASELINE_SHA` | `b721b988fefec4a4da3b25ad320fa6a1cad8a7ee` (tag `v0.1.21`, the audit's snapshot) |
| `START_SHA` (this round) | `b153ed7fd2b59f922a883501bf48f8f573d1e67b` |
| `CLOSURE_CODE_SHA` | `0aa91173f0afdcb980806883e5a2bb544068a29f` (the DS-01 fix; earlier commits in §10) |
| `DOCUMENT_SHA` | the commit that adds this file; `git log -1 --format=%H` |

The original audit report is preserved unchanged at
`KNOWLEDGE/reports/core/DUAL_STACK_CROSS_COMPONENT_ARCHITECTURE_AUDIT_2026-10-20.md`. It remains a
point-in-time record of what the audit saw; nothing in it was rewritten to say "fixed".

---

## 1. What this round changed, in one table

| item | before this round | after | evidence |
|---|---|---|---|
| **DS-01** late family refusal shown as `no dial candidates` | reported `CONFIRMED_BUG` by the audit | **confirmed on HEAD and fixed here**: the late-arrival arm dropped a refused candidate without recording the refusal, so the empty-set conclusion could only say `no dial candidates` | red test `TestLateRefusalExplainsWhyNothingWasAttempted`, then green; §2 |
| **DS-02** group outbound serialises a resolved address list | `HIGH_CONFIDENCE_RISK`, no measurement | **measured**: direct shape 0 ms (races), group shape 300 ms (serial, and it consumes the shared deadline) | `TestGroupShapeDialsAddressesSerially` |
| **DS-03** recovery producers not joined | `HIGH_CONFIDENCE_RISK` | **already joined** at HEAD: `producers.Wait()` and `originalWorkers.Wait()` with termination comments; ownership tests pass | `common/dialer/resolve.go:467,486,593`; §5 |
| **ssh** `InterfaceUpdated` unlocked close | not in the audit | **real bug found and fixed**: unlocked read of `clientConn` racing with a dial, and it could close the session the dial had just installed | mutation run reports `DATA RACE`; fixed form passes `-race -count=3` |
| **DS-04** fallback amplification | `UNVERIFIED_HYPOTHESIS` | attempt counts recorded (2 serial vs 2 concurrent for a two-address list); no extra layer observed | §6 |
| **DS-05** entry presentment latch | `UNVERIFIED_HYPOTHESIS` | design boundary restated; device verification still `NOT_TESTED_DEVICE` | §7 |
| **F7** load-sensitive liveness assertion | one idle timeout with a 3× ceiling (flaked at 12.3 s under load) | ceilings raised to 5×/6× with the reasoning recorded; a real hang still fails | §8 |

Two production files changed in this round: `common/dialer/dual_stack_scheduler.go` (the DS-01 fix)
and `protocol/ssh/outbound.go` (the unlocked close), plus the tests that pin each and the DS-02
measurement. Nothing else.

---

## 2. DS-01 — confirmed on HEAD, fixed here

The audit said a late AAAA candidate, refused by the local IPv6 egress gate, produced
`no dial candidates` instead of the refusal reason. An interim read of this round concluded the
opposite, because the streaming conclusion *does* consult `refused` and the plan filter *does* record
refusals. That read was wrong: neither of those is the arm the late candidate travels through.

**The defect is in the late-arrival arm.** `dialWithLateCandidatesAdmitting` drops a candidate the
machine cannot dial with a bare `continue`:

```go
if s.localFamily.hardUndialable(candidate.address) {
    continue
}
```

A late candidate never passes through `filterLocalFamily`, so that drop never reached `refused`. The
stream then closed with `started == 0` and `refused.refused() == false`, and the conclusion answered
`no dial candidates` — the symptom, not the cause the machine had already established.

**Reproduction, red before the fix** (`TestLateRefusalExplainsWhyNothingWasAttempted`, empty plan, one
late global-IPv6 candidate, the reported machine's policy):

```
Error: Target error should be in err chain:
       in chain: "no dial candidates"
       Messages: the refusal is the reason nothing was attempted, so it must be the error
```

**The fix** records the first refusal in the late arm, before dropping the candidate:

```go
if s.localFamily.hardUndialable(candidate.address) {
    if !refused.refused() {
        refused = localFamilyRefusal{family: candidate.family, address: candidate.address}
    }
    continue
}
```

The first refusal is enough to explain an empty attempt set; a later one cannot be more informative
and must not overwrite the reason the first candidate was dropped. `localFamilyRefusal.error` re-asks
the same policy with that address, so if the machine state moved between the drop and the conclusion
the answer degrades to the general one rather than describing a state that no longer holds — which is
the same rule the plan-filtered arm already followed.

**Green after the fix**, `-count=3`: the error is now `errLocalIPv6EgressUnavailable` and the test
also asserts that no attempt was made (a refusal must not be dialed) and that the string
`no dial candidates` is absent.

**What this changes about the audit**: DS-01 was real, and its A2-7 note ("5 of 6 sites fixed, the
streaming late arm remains") is accurate against `b721b988` — the plan arm and the streaming
conclusion were fixed there, the late arm was not. No historical text was rewritten; the audit stays
as the point-in-time record, and the correction lives here.

| regression | asserts |
|---|---|
| `TestLateRefusalExplainsWhyNothingWasAttempted` (**new**) | the late refusal is the error, no attempt, no symptom string |
| `TestGlobalIPv6IsRefusedBeforeASocketExists` | the gate returns `errLocalIPv6EgressUnavailable` |
| `TestTheRefusalIsPerDestinationNotPerMachine` | ULA / link-local / LAN destinations are not refused |
| `dual_stack_parallel_network_test.go` (`ErrorIs`/`NotContains`) | the parallel path surfaces the refusal, not `no dial candidates` |
| `egress_real_world_attack_test.go` | the refusal survives the real policy wiring |

The whole `common/dialer` package passes, and passes under `-race`.

---
## 3. DS-02 — measured, and deliberately not changed

### The production path

When a flow's destination was resolved, `resolve.go:947` hands the address list to
`DialSerialNetwork(ctx, d.dialer, …)`. `direct` answers `ParallelNetworkDialer`, so the list races.
A group (`selector`, `urltest`, `loadbalance`) does not answer it, so the same list walks the serial
loop in `common/dialer/default_parallel_network.go`: address one, wait for it to finish or for the
shared context to expire, then address two.

### The measurement (`common/dialer/dual_stack_group_shape_test.go`)

Two doubles, a black-holed IPv6 candidate and a healthy IPv4 one, both driven through the real
`DialSerialNetwork`:

| shape | attempts | starts | elapsed |
|---|---|---|---|
| direct-shaped (answers `ParallelNetworkDialer`) | both | 0 ms, 0 ms | **0 ms** |
| group-shaped (`N.Dialer` only) | IPv6 then IPv4 | 0 ms, **300 ms** | **300 ms** |

The group's IPv4 attempt starts only after the IPv6 attempt has burned the shared deadline. With a
dial budget that is consumed by the first candidate, the healthy family can be attempted **after the
budget is already gone** — so the cost is not only latency; it can be the connection itself.

### Decision: `NO_CHANGE_SUPPORTED_BY_EVIDENCE`

Making a group answer `DialParallelNetwork` is not a mechanical interface addition:

- the member must be chosen **once** (selector's current pick, urltest's selected member,
  loadbalance's per-flow member) and delegated to, or a single connection could be retried across
  different members — the exact semantic the audit's own brief warns about;
- `loadbalance` has a failover path (`DialWithFailover`) that a delegated parallel call would bypass,
  changing retry behaviour on failure;
- the packet surface (`ListenSerialNetworkPacket`) has its own ownership rules, and the report's own
  negative guarantee — a UDP socket existing is not proof of end-to-end reachability — argues against
  inventing a parallel packet path to match;
- the address-list path is reached only for `actionResolve` flows whose outbound is a group, and this
  round has no field data on that frequency.

What is recorded instead is the measurement and the exact call site, so a future round with field
data can decide quickly. Both group semantics and the direct fast path are untouched, and the
measurement test remains as the regression that documents the difference.

---

## 4. DS-03 — already joined, and the three-resolver matrix

At HEAD the recovery producers are joined explicitly:

- `resolve.go:467` and `:486` — `producers.Wait()` after the racing/producer sections, with a comment
  that the winner path "can prove they have exited rather than merely assuming it";
- `resolve.go:593` — `originalWorkers.Wait()` for the original-literal workers.

Ownership is covered by `dual_stack_ownership_test.go`: the original winner leaves no unowned
recovered connection, the recovered winner is left open exactly once, parent cancellation leaves no
worker or connection, both-attempts-fail leaves no worker, and the race returns exactly one
connection. Those tests pass at `START_SHA` and at the closure SHA.

The three resolver shapes the brief asked for map onto that suite:

| resolver shape | behaviour under test | result |
|---|---|---|
| cooperative (returns on ctx cancel) | `TestParentCancellationRecordsNoVerdict`, `TestParentCancellationLeavesNoWorkerOrConnection` | cancellation propagates; no worker or connection survives |
| slow but cooperative | `TestSameFamilySlowCandidateRecordsNoVerdict`, `TestPendingInsideIntervalRecordsNoVerdict`, `TestFastRaceRecordsNoVerdict` | the fast winner returns; the slow loser records no family verdict |
| ignores cancellation | the same cancellation tests with a blackholed attempt: the loser unwinds with `context.Canceled` and is **not** recorded as a family failure (`dual_stack_stalled_test.go`) | bounded: the winner path does not wait for the loser; `Close` is not blocked by it |

No join was added this round, because the joins are already there and the tests demonstrate the
bounded behaviour. The audit's suggestion ("add a WaitGroup") would have been redundant at best and,
if written as an unconditional wait before returning the winner, harmful.

**Addendum (same day):** the joins above are the *outer* producers. The innermost
`LookupFamilies` goroutine inside `recoverCandidates` has no join, and a specialist round measured
what that does and does not cost for both chains — see
`DS03_RECOVERY_PRODUCER_CLOSURE_2026-10-20.md`. Its verdict is `NO_PRODUCTION_CHANGE`, with the
non-cooperative-resolver boundary stated as a measurement rather than assumed away.

---

## 5. The ssh bug (found while closing the 17-item matrix)

`protocol/ssh/outbound.go`'s `InterfaceUpdated` closed `s.clientConn` **without** `clientAccess`,
while `connect`, `CloseIdleConnections`, `releaseStream` and the `Wait` goroutine all read and write
that field under the lock. On an interface change a concurrent dial's freshly installed connection
could be the one closed — retiring the replacement instead of the old session — and the access is a
plain data race.

The fix takes the lock, snapshots and clears both `s.client` and `s.clientConn`, unlocks, and closes
outside the critical section — the shape the other retirement paths already use. Clearing
`s.client` also makes the old client's `Wait` goroutine a no-op, because it only clears the fields
while they still point at it.

Verification is a mutation pair, not a claim:

- old form (temporarily restored in the working tree): `WARNING: DATA RACE` in
  `TestInterfaceUpdatedRetiresTheConnectionUnderTheLock`;
- fixed form: `go test -race ./protocol/ssh -count=3` passes.

---

## 6. The 17 `InterfaceUpdated` implementations

The current code has 27 implementations in `protocol/` (5 inbounds, 1 internal node, 21
outbounds/endpoints), matching the audit's count. The 17 from the audit's list, with what each one
actually does:

| object | action | long-lived resource | verdict |
|---|---|---|---|
| `anytls` | `client.Reset()` | mux client | `CORRECT` |
| `group/urltest` | re-runs `CheckOutbounds` in a goroutine, guarded by pause and `ctx.Err` | member selection | `CORRECT` |
| `http` | `client.ResetConnections()` | HTTP transport pool | `CORRECT` |
| `hysteria` | `CloseWithError("network changed")` | QUIC connection | `CORRECT` |
| `hysteria2` | `CloseWithError("network changed")` | QUIC connection | `CORRECT` |
| `mieru` | under lock: drain in-flight, rebuild transport runtimes, guarded by started/closed/dialed | SDK client | `CORRECT` (most thorough of the set) |
| `openconnect` | `client.RestartSession()` | tunnel session | `CORRECT` |
| `openvpn` | `client.RestartSession()` | tunnel session | `CORRECT` |
| `shadowsocks` | `multiplexDialer.Reset()` when present | mux | `CORRECT` |
| `snell` | `client.Reset()` | mux | `CORRECT` |
| `ssh` | retire the session under the lock (**fixed this round**) | SSH connection | `FIXED` |
| `tailscale` endpoint | `netMon.InjectEvent()` when started | netmon | `CORRECT` |
| `tailcat_outbound` | `node.interfaceUpdated()` under lock | tailcat node | `CORRECT` |
| `trojan` | `transport.Close()` + `multiplexDialer.Reset()` | transport + mux | `CORRECT` |
| `tuic` | `CloseWithError("network changed")` | QUIC connection | `CORRECT` |
| `vless` | `transport.Close()` (+ mux reset when present) | transport + mux | `CORRECT` |
| `vmess` | same shape as vless | transport + mux | `CORRECT` |

Evidence level for the table: `VERIFIED_SOURCE` for every row; `MEASURED_LOCAL` only for ssh (race
pair). No per-protocol runtime test exists without a real server, and none is claimed: the columns
describe what the code does on an interface change, not that a device has been switched underneath
it. The five already-sampled objects (`direct`, `naive`, `masque`, `wireguard`, plus the
hysteria2 inbound) were re-read and are unchanged in shape.

Double-close risk was checked in each row: every path either nils the field under the lock
(`ssh`, `mieru`, `tailcat`) or closes a value it owns (`hysteria*`, `tuic`, transports). None closes
a value that another path may replace without a lock, which is precisely why the ssh row stood out.

---

## 7. DS-04 and DS-05

**DS-04 (amplification).** The controlled measurement gives the attempt counts for a two-address
list: the group shape performs exactly 2 sequential attempts, the direct shape 2 concurrent attempts,
and no layer observed here produces more than one attempt per address. That bounds *this* layer: it
says nothing about DNS qtype counts or protocol-level retries, which the audit already recorded as
unmeasured. No change.

**DS-05 (entry presentment).** The latch is a deliberate design trade: the entry's family capability
is published once per generation, so an IPv4-only start does not later advertise IPv6 without a
restart or a network transition event. Publishing AAAA while the TUN has no usable IPv6 would create
the split state the report warns about, so the latch stays. Device verification remains
`NOT_TESTED_DEVICE`; nothing here changed it.

---

## 8. F7 — the load-sensitive liveness assertion

`transport/http/TestActiveInboundStopsBecauseThePeerIsStarved` asserted that the connection dies
within 3 idle timeouts after the last inbound packet; under a concurrent build it once took 12.3 s
against a 9 s ceiling, while running at about 8 s alone. The invariant the test exists to protect is
"the wait is bounded, not hung", not a narrow timing budget, so the ceilings are now five and six
idle timeouts with that reasoning written next to them. A genuinely unbounded wait still fails: there
is no path in the test that returns successfully without the connection dying.

---

## 9. Tests actually run (this round)

| command | result |
|---|---|
| `go test ./common/dialer -run 'TestLateRefusalExplainsWhyNothingWasAttempted' -count=3` | PASS (red before the fix, green after) |
| `go test ./common/dialer -count=1` / `-race` | PASS (16.5 s / 17.7 s) |
| `go test ./common/dialer -run 'TestGlobalIPv6\|TestEgress\|TestLocalFamily\|TestGroupShape' -count=1` | PASS |
| `go test ./common/dialer -run 'TestLateFamilyAnswerDoesNotStrandTheProducer\|TestSchedulerClosesImmediatelyWithNoCandidates\|TestLateResolutionErrorDoesNotFailAWinner\|TestOwnership\|TestProducer' -count=1` | PASS |
| `go test ./protocol/ssh -count=1` | PASS |
| `go test -race ./protocol/ssh -count=3` | PASS; the mutated (old) form reports `DATA RACE` |
| `go test -tags with_quic ./transport/http -run 'TestTheIdleTimeoutBoundsASilentPath\|TestActiveInboundStopsBecauseThePeerIsStarved' -count=1` | PASS (11.9 s) |
| `go vet ./common/dialer ./protocol/ssh ./transport/http` | clean |
| `gofmt -l` on the four changed files | clean |
| `git diff --check` | clean |

Not run, and not claimed: real WAN, device network transitions (PMTU/ICMPv6/NAT64), any per-protocol
runtime lifecycle test. `route/TestBackpressure` is **not** in that list any more: it ran here and
passed (`TestBackpressure_HealthyBlockedWriterIsIdenticalToStalled` 23 s,
`TestBackpressure_KernelGiveUpWindow` 360 s), which is what the 420 s `./route` figure in the table
above is mostly made of.

---

## 10. Commits and rollback

| commit | purpose |
|---|---|
| `1c97c3a61` | ssh: retire the session under the lock, plus the mutation-verified race test |
| `a1705021f` | dialer: the DS-02 serial-cost measurement; transport/http: F7 ceilings |
| `df51e2dc6` | dialer: record both parallel launches before any goroutine runs (the measurement test failed on its own race under `-race` on CI, not on the behaviour) |
| `0aa91173f` | dialer: record a late family refusal before dropping it, plus the red→green regression |
| the docs commit | this report and the knowledge index row |

Rollback is one `git revert` per commit; no dependency pin, workflow, submodule pointer or release
artifact was touched.

---

## 11. Limits

- DS-01 was reproduced from its described shape and the real scheduler entry point, not from the
  audit's original probe output (which was not available here). The mechanism is proven; the exact
  probe binary is not. An interim read of this round first concluded "not reproducible" because it
  looked at the wrong arm — the plan filter and the streaming conclusion both consult `refused`, and
  the late-arrival arm between them did not. That correction is recorded here rather than hidden.
- DS-02 has a measurement, not field frequency data. That is why it stays `NO_CHANGE`.
- The 17 rows are source-verified; only ssh has a runtime test, and only for the field-access race.
- No device, no WAN, no macOS/Windows/Android runtime network switching.
