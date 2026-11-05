# Resource governance — protocol coverage audit

> **阶段矩阵说明。** §1 的“exactly one consumer”描述共享 TLS 接入前；§3 已记录扩展，后续又修复类别和多跳许可。当前行为以 [资源准入模块](../../KNOWLEDGE/modules/resource-admission.md) 为入口，不把这份阶段矩阵当作最新覆盖结论。

Round 05. Work done on the product line `testing` at `d45fa8a4404`. This report covers what was
inspected, what was changed, and what remains; it is not a claim that every protocol was
measured under pressure.

## 1. What exists today (verified in source)

| Component | Fact | Evidence |
|---|---|---|
| Admission policy | `common/resourceguard`: levels Normal/Armed/Critical, a gate with limit/queue/wait, an interactive reserve, starvation aging, shed hooks, stats | `common/resourceguard/guard.go`, `gate.go` |
| Pressure source | `service/oomkiller` creates the guard **only** in `policyModeNetworkExtension` and feeds it its own atomic pressure reading | `service/oomkiller/service.go:61`, `guardLevel()` reads `s.pressure.Load()` |
| Consumers | exactly one: `protocol/anytls`, wrapping TCP and UoT dials through `dialWithAdmission` | `protocol/anytls/admission.go`, `outbound.go` (`uot.Client{Dialer: h.dialAdmitted}`) |
| Non-NE platforms | no guard in the context; `Acquire` on a nil guard returns `(nil, nil)`, so every dial path is unchanged | `guard.go` `Acquire` |

The pressure read is an atomic load, not a lock: `guardLevel()` switches on `s.pressure.Load()`,
which is the same value the TUN stack consumes. There is no second pressure state machine.

## 2. Coverage matrix (compiled outbounds)

Classification is by *when* the expensive creation happens, read from each dial path. `admitted`
means a `resourceguard` permit is taken before it.

| Protocol / transport | Expensive creation | Per-flow or per-outbound | Admitted before this round |
|---|---|---|---|
| AnyTLS (TCP + UoT) | TCP + TLS handshake + session | per flow, but skipped when an idle session is reused | **yes** |
| Trojan | TCP + TLS per stream | per flow | no |
| VLESS / VMess (no multiplex) | TCP + TLS per stream | per flow | no |
| VLESS/VMess with XHTTP/WS/gRPC transport | same, through the transport dialer | per flow | no |
| ShadowTLS | TCP + ShadowTLS handshake | per flow | no |
| Shadowsocks / Snell / SS2022 | TCP (and UDP) per flow, encryption only | per flow | no (cheap: no handshake) |
| Hysteria2 | one QUIC connection per outbound; streams ride it | **per outbound** | not needed on the stream path |
| TUIC | one QUIC connection per outbound; streams ride it | **per outbound** | not needed on the stream path |
| Naive (cronet) | `NaiveClient` created once; CONNECT per dial rides its pool | per outbound, pool miss per flow | not needed |
| MASQUE | H3 tunnel initialisation per outbound | per outbound | not needed |
| Mieru | SDK client per outbound | per outbound | not needed |
| SSH | one SSH client, kept on the outbound | per outbound | not needed |
| Tor | SOCKS client, one process | per outbound | not needed |
| HTTP (painless) | inbound-side only | — | n/a |

Two claims matter and are checkable in source: `hysteria2.NewClient` is called once in
`Start()` and `DialContext` calls `client.DialConn` (`protocol/hysteria2/outbound.go:199,302`);
TUIC is the same shape (`protocol/tuic/outbound.go:182` calls the shared client). So a
200-stream burst through either is **one** QUIC handshake, and adding an admission gate on their
stream path would queue streams that cost nothing.

## 3. What changed this round

**The shared TLS dialer now admits the handshake.**

Every TLS-based outbound and HTTP transport builds `tls.NewDialer` — Trojan, VLESS, VMess, the
gRPC-lite/HTTP/WebSocket/HTTPUpgrade/XHTTP transports, and the DoT/DoH DNS transports
(`grep -n 'tls.NewDialer('` finds 12 call sites). Before this change only AnyTLS was admitted, so
a memory storm driven by any of those protocols was unbounded even though each flow costs the
same TCP connect plus a full TLS handshake.

The seam is `defaultDialer.DialTLSContext` → `handshakeWithAdmission` (`common/tls/client.go`),
which sits **before** the handshake — the point where the handshake buffers, the key exchange and
the per-connection goroutine begin.

**It cannot double-charge AnyTLS.** AnyTLS admits at its own seam and its session handshake runs
through this same dialer; without a guard the same handshake would take two permits, halving the
effective limit and queueing one dial twice. `resourceguard.WithHeldPermit` / `HasHeldPermit`
(`common/resourceguard/held.go`) mark the admitted dial, and the TLS seam skips admission when
the marker is present: one handshake, one slot.

**Tests** (`common/tls/handshake_admission_test.go`, 4 cases):
an unmarked dial takes a permit and a failed handshake leaks none; a marked dial asks the gate
for nothing; no guard means no refusal; a full gate (limit 1, queue 0) refuses the second
handshake in constant time and does not run it.

Existing suites: `protocol/anytls`, `common/resourceguard` and `common/trafficclass` pass; the full tagged
suite keeps its three known environment failures (`common/tls` TLS 1.3 on this host,
`common/tlsspoof`, `common/windivert`).

## 4. Findings that were *not* changed, and why

1. **Reuse is decided after the permit is taken (AnyTLS).** `dialWithAdmission` acquires before
   the client knows whether it will reuse an idle session. Under saturation, reuse-only streams
   queue behind slots reserved for handshakes. Fixing it needs the client to answer "is an idle
   session available" before the dial — `sing-anytls` has `takeIdleSession` but no exported
   predicate. That is a dependency change with its own pin/freeze cycle, and shipping it without
   a device test would trade a measured queue for an unmeasured one.
2. **`ClassInteractive` was inert, and is now wired.** The gate reserves four slots beyond the
   limit (`InteractiveReserve`) for interactive traffic, but no caller passed `ClassInteractive`:
   the class was resolved at the route layer for the scheduler only and never reached the
   admission seam.

   Fixed this round: `route/conn.go` attaches the resolved class to the dial context
   (`trafficclass.WithClass`) at both the stream and the UDP dial sites; `protocol/anytls`'s
   admission maps a high-priority class (`interactive`, `realtime`) to
   `resourceguard.ClassInteractive` and everything else to `ClassNormal`. Tests: the class
   survives the context round-trip and "unclassified" is distinguishable from "classified
   default"; an interactive flow is admitted through the reserve while the normal pool is full,
   and a bulk flow is refused instead of borrowing it.
3. **UDP NAT is already bounded.** The diagnostic's ~200 UDP entries were not an unbounded map:
   `sing-tun`'s UDP NAT is an LRU with a lifetime and a size cap (4096 on iOS, 16384 elsewhere,
   `udp_nat.go` `MaxSize`). The 200 goroutines seen in the snapshot are the per-flow packet
   connection loops themselves, which end with their entries. A separate NAT admission would
   bound the same thing twice.
4. **`gvisor`/`mixed`/`system` loopback reflection** (from the loopback work) is untouched here.

## 5. Not covered (explicit)

* No pressure harness was built this round: no STUN-like UDP storm replay, no
  high-RTT/loss/blackhole matrix, no 50–100 reload cycles, no per-protocol concurrency sweep. The
  numbers in this report are source facts plus unit tests, not pressure measurements.
* No device or simulator run. `NEEDS_DEVICE` for anything about the iOS NE footprint.
* No race/fuzz run on the new seam; `Verify` runs the repository's race steps, which include
  `common/tls`.

## 6. Next steps that are worth an implementation round

1. **Reuse predicate**: add `HasIdleSession() bool` to `sing-anytls` and take the permit only
   when it is false; test with a saturated gate and a reusable session.
2. **Class propagation**: attach the traffic class to the dial context in the route layer and
   read it in `Acquire`, so `ClassInteractive` becomes a real priority rather than reserved
   capacity.
3. **Pressure harness**: a deterministic local harness (mock TLS/QUIC servers, injected memory
   levels) that measures per-protocol in-flight handshakes, queue depth and refusals — the
   evidence this round could not produce.

---

## After the audit: the shared TLS seam honours the class

The audit found the last asymmetry on this seam: `common/tls` acquired with `ClassNormal`
unconditionally, so a Trojan or VLESS flow a person was waiting on queued behind background work
exactly like a bulk transfer, while AnyTLS had mapped the class since the reserve existed. The seam
now maps `trafficclass` the same way AnyTLS does, and three tests state the property at the shared
entry: an interactive handshake may use the reserve while the normal pool is full, bulk and default
traffic may not, and both seams answer the same for the same class.

An earlier revision of this document said the interactive reserve applied to AnyTLS only. That was
true when written and is false now; the sentence above is kept rather than deleted so the change is
visible.
