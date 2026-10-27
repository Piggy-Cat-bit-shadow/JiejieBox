# MTU, packet size and buffer geometry

**Dependency path scope:** `common/buf/buffer_standard.go`, `common/buf/buffer_low_memory.go` and
`common/low_memory*.go` cited below are in the pinned `github.com/sagernet/sing` replacement,
not this repository root. Recheck its `go.mod` pin and source before relying on old geometry or line
numbers. Concrete MTU values below are historical unless reconfirmed for the target configuration.

## TUIC lower-tunnel ceiling after v0.1.9

**VERIFIED_SOURCE**, testing target `011f3fdcd99`: `protocol/tuic/outbound.go` derives a QUIC
payload ceiling from `dialer.DetourPathCapacity` when the lower tunnel proves one. It supplies or
clamps the initial packet size, disables path MTU discovery for that proven ceiling, and refuses a
ceiling below the QUIC Initial minimum. An unknown path does not assert a ceiling. The code passes
the result to the pinned sing-quic client; this is a path-capacity constraint, not a measurement of
the physical link MTU. See the TUIC tests and pinned fork before generalizing to another protocol.

For MASQUE, `protocol/masque/client.go` already computes `outerPayloadCeiling` from the detour
and clamps `HTTP3Options.InitialPacketSize` when the capacity is known. The 2026-10-28 historical
audit's statement that no outer ceiling is computed is therefore too broad. That constructor does
not assign `InitialPacketSizeCeiling` or disable path MTU discovery from this value, so the initial
clamp must not be presented as proof of a hard bound on later QUIC packets. This distinction is
`VERIFIED_SOURCE` at `011f3fdcd99`; no real underlay or peer measurement is implied.

**Scope:** `common/bufgeom` (fork-created), the MASQUE MTU/PTB path, the hysteria2 hard ceiling, the
WireGuard tunnel MTU, `common/physicalpath/status.go`'s MTU block, and the low-memory geometry profile.

**Load this module when changing:** any MTU computation, a packet-size bound, a buffer size, the
low-memory profile, or a PTB handling path.

---

## 1. Three MTU quantities, not one

```text
outer link MTU          what the platform path carries        ← NOT KNOWN anywhere in this codebase
        │
outer QUIC packet size  what the tunnel's own QUIC layer may put on that path
        │
inner tunnel MTU        what the tunnel promises the payload it carries
```

They are computed, bounded and proven **separately**, and the correct answer for one is not the
correct answer for another. Conflating them is the failure mode this section exists to prevent.

| # | Quantity | Computed at | Bounded at | At the shipped config |
|---|---|---|---|---|
| 3 | **inner tunnel MTU** | `protocol/masque/client.go:617-619` (`c.mtu`), seeded `:119-121` | `DefaultMTU = 1280` (`transport/masque/session.go:18`); device `protocol/masque/endpoint.go:83` | **1280** — and it is **configured, not measured** (`client.go:612-616`) |
| 2 | **outer QUIC initial packet setting** | `client.go` → `min(mtu + 51, MaxUint16)`; server mirror `protocol/masque/server.go` | detour capacity clamps the initial setting; values below 1200 are refused. This is not a hard ceiling for all later packets | **1331** before a lower detour cap |
| 1 | **outer link MTU** | **nowhere** | **nowhere** | **NOT KNOWN** |

**The 51 is itemised, not magic:** `QUICPacketOverhead = 51` (`session.go:19`) = 1 context ID + 8
quarter-stream-ID varint + 16 AEAD + 1 header + 1 packet number + 24 frame, itemised at
`outer_quic_mtu_test.go:31-46`.

**Asserted inequalities, not comments:** outer > inner (`outer_quic_mtu_test.go:73-77`, `:172-176`),
and `1280 + 51 − 1232 = 99` exactly (`:181-185`).

**Why the outer link MTU is genuinely unknown, not merely unrecorded:** `client.go:146-147` says so,
`PathCapacity` has **no link-MTU field at all** (`path_mtu.go:127-155`), and the ceiling that *is*
carried is a **UDP-payload** number (`path_mtu.go:111-120` = inner − 20/40 − 8 − encapsulation), which
is one level *below* a link MTU. `outer_quic_mtu_test.go:222-237` pins that the option surface has no
third knob.

**The ceiling comes from the path model, not from a constant.** `feat(mtu): cap the MASQUE outer QUIC
packet by a proven detour capacity` (`2118ee40`) is exactly two files: `client.go` +51 lines and
`outer_quic_mtu_test.go` +237. WireGuard's `validateTunnelMTU`
(`transport/wireguard/endpoint.go:442-488`) refuses only IPv6-on-sub-1280 — a 1210-byte detour yields
1130 nested while the detour itself needs 1360 (`:469-471`).

**`sing-quic` is load-bearing for that cap.** `scripts/ci/check-go-module-integrity.sh:105-115`
records why it is one of the four `REQUIRED_FORK_REPLACES`:

> *"It became load-bearing when the hard packet-size ceiling was given its own field: the ceiling
> reaches the wire only if this layer forwards it, so a nested module resolving upstream sing-quic
> would build a product whose proven ceiling silently disappears."*

---

## 2. PTB (packet too big)

The exact path, from `transport/masque/session.go`:

1. a send fails too-large, and the available size is
   `MaxPayloadSize = MaxDatagramPayloadSize − VarintLen(StreamID/4)` — **the context ID is still
   counted at this point** (`transport/http/client_h3.go:481-483`);
2. the error is classified (`session.go:401-411`): **nil-check first, then a direct type assertion**,
   and `errors.As` only as the fallback at `:417-426` — because `errors.As` **hoists its target to the
   heap at function entry**, which would cost one allocation per *successful* packet
   (`session.go:358-403`, guarded by `session_datagram_error_test.go:151-171`, whose budget is
   **exactly 1** allocation per single-packet write);
3. `mtu := maxPayloadSize − 1`;
4. **if `mtu >= minimumLinkMTU` (1280)** the context ID is undone with `datagram.Advance(1)` and
   `handlePacketTooBig(datagram, mtu)` runs (`:504-517`); otherwise it is a **hard error** —
   *"unable to carry 1280 byte packets"*;
5. the client handles it at `protocol/masque/client.go:686-697`; the server at
   `transport/masque/server.go:579-592`, addressed to the **owning peer** (`firstPeerAddress :596-605`)
   and queued on that session; ICMP errors are built in `icmp.go:73-85`, `:99-134`, and the destination
   rewrite **recomputes checksums**.

**The invariant, exactly stated:** a too-large datagram is **never silently dropped and never promoted
to a capsule**. PTB advertises `MaxDatagramPayloadSize − VarintLen(StreamID/4) − 1`, **and only when
that value is ≥ 1280**; otherwise the sender gets the explicit error above. The MTU is additionally
clamped by `sing-tun/icmp_error.go:113`, `:155`.

Tests: `packet_too_big_test.go` (`:111`, `:175`, `:234`, `:271`, `:294`, `:374`),
`packet_too_big_session_test.go` (`:150`, `:212`, `:270`, `:295`), `packet_size_bound_test.go:90-186`
(`maxPacketSize = 40 + 65535`, `session.go:56-61`).

**Server-side headroom:** `server.go:428-430` reports
`max(PacketHeadroom, handler.FrontHeadroom())` — the transport's own reservation, or the handler's if
it needs more.

---

## 3. The hysteria2 "hard path ceiling"

Commit `91ca1652d` — *"fix(hysteria2): a hard path ceiling is its own field, applied after
ChromeParrot"*.

**The problem it solved:** the ChromeParrot fingerprint override forces
`initialPacketSize = 1250` (`config.go:105-125`), and **no other config field could lower the first
flight while MTU discovery is inactive**. So a proven 1232-byte ceiling had no effect with the
fingerprint on.

**The fix:** give the ceiling its own field (`Config.InitialPacketSizeCeiling` in the pinned quic-go
fork) and apply it **after** the ChromeParrot override. A proven 1232 now reaches the wire with the
fingerprint on, and the same field caps what MTU discovery may raise the packet size to.

**Measurements and pins:** `protocol/hysteria2/hard_ceiling_wire_test.go`; the pin is in `go.mod`; the
decision record is `docs/fork/v016-final-closure-ledger.md:4-13`.

> **Trap: that ledger's own body contradicts its banner.** Section `:465` still says *"HY2 ChromeParrot
> is BLOCKED on the pinned quic-go fork"*, while the header at `:4-13` retracts exactly that. **The
> banner is current; the body is a record.** Read banners first.

**MASQUE does not share this mechanism — and that is currently untested.** `InitialPacketSizeCeiling`
exists only in `protocol/hysteria2` (four references). MASQUE instead **clamps the preference eagerly**
(`client.go:167-192`) and has **no fingerprint field at all** (`option/masque.go:24-52`). The eager
clamp is safe *only* because nothing overwrites the value afterwards — so **adding a fingerprint or any
post-construction size override to the MASQUE client would silently void its physical ceiling, and no
existing test would fail.** If you are about to add one, add the test first.

The hysteria2 distinction is worth stating precisely, because the three fields have different meanings:

| Field | Kind | Applied |
|---|---|---|
| `InitialPacketSize` | **preference** | first, and overwritten by the fingerprint |
| `InitialPacketSizeCeiling` | **constraint** | **last** (`quic-go config.go:127-140`), so it survives the override and also caps PMTUD growth (1232 → 1438 measured, `config.go:184-198`) |
| `DisablePathMTUDiscovery` | **policy** | `hysteria2/outbound.go:148-156`, `:208-217`; `quicPathSizing :233-273` |

`config.go:109-125` is why the order matters: ChromeParrot **overwrites the size with 1250 and never
reads a ceiling**, so an early ceiling is discarded for the default configuration (Parrot is on by
default, `outbound.go:220`).

---

## 4. `common/bufgeom` — the geometry contract

A fork-created package (**1,157 lines**: 75 non-test + 1,082 test across 4 test files). It owns the
rule that a buffer acquired for a packet path is **exactly poolable** and survives `Advance`/`Prepend`
**without reallocating its payload**.

**A "geometry" is a compile-time pair in the replaced `sing` fork, not a runtime switch:**

| Profile | Source | Sizes |
|---|---|---|
| default | `common/buf/buffer_standard.go:5-8` (`//go:build !with_low_memory`) | **32 KiB / 16 KiB** |
| low memory | `common/buf/buffer_low_memory.go:5-8` (`with_low_memory`) | **16 KiB / 8 KiB** |

plus `common/low_memory_stub.go:7` / `low_memory.go:7` (`LowMemory` false/true).

**Measured, both profiles** (real runs, exit 0):

| | BufferSize / UDPBufferSize | `LowMemory` | per-flow live heap | per-acquire alloc (live/idle/bulk) |
|---|---|---|---|---|
| default | 32768 / 16384 | false | **64.1 KiB** | 1.75 / 0.93 / 1.75 B |
| `,with_low_memory` | 16384 / 8192 | true | **32.1 KiB** | 0.94 / 0.53 / 0.93 B |

Exactly 2×, and **bulk per-byte allocation is geometry-independent** (6.01 vs 5.48 B/KiB) because
`copyExtended` grows to 65535 after ~512 KiB (`geometry_bench_test.go:656-724`).

| Rule | Anchor | What breaks |
|---|---|---|
| Buffers are exactly poolable | `geometry_test.go:63 TestGeometryBuffersAreExactlyPoolable` | a non-power-of-two size **panics on first release** — `Buffer.Release()` ends in `common.Must(Put(…))` (`buffer.go:307`, `cond.go:294-300`), and above `MaxPooledBufferSize = 73728` (`alloc.go:11`, `:41-44`) `Get` returns nil and the failure is a nil-slice panic |
| `Acquire` never reallocates the payload on `Advance`/`Prepend` | `geometry_test.go:99 TestBufferAcquireNeverReallocatesThePayload` | an extra allocation per packet on the hot path |
| The **allocation ceiling** is asserted, not just benchmarked | the `!race` half | a regression reaches review as a number instead of a failure |
| The ceiling is **`//go:build !race`**; under `-race` only the structural half runs | `race_detector_test.go:1`, `:25` vs `race_detector_stub_test.go:1`, `:7`; consumer `geometry_test.go:147-151` | the ceiling is meaningless under `-race`: a pooled `Get`/`Put` hand-off measured **8,251 B/acquire under `-race` against 1.7 without it**, and loosening the 1024-byte ceiling to accommodate that would erase the pooled-versus-reallocated distinction the assertion exists to make |
| `AllocsPerRun` for `buf.New`/`Release` is **1.00, not 0.00**, unless the call is inlined | `geometry_test.go:84-98` | asserting `== 0` is a false expectation (0.00 inlined vs 1.00 = 64 B not inlined) |
| `UDPBufferSize == BufferSize/2` | `geometry_test.go:48-52` | the packet path and the stream path disagree about the pool |

> **Stale cross-reference:** `geometry_bench_test.go:732` names `TestAllocsPerRunBufferAcquire`, which
> **does not exist**. Do not go looking for it.

> **Consequence: `-race` does not cover the byte ceilings, by design, anywhere.** On a host where
> `-race` works, the ceilings run in the non-race build only. On this audit host `-race` is
> `BLOCKED_BY_ENVIRONMENT` entirely, so the `race` half was exercised nowhere.

The MASQUE batch path adds its own geometry rule: packet **headroom is reserved by the producer** so
the 1-byte context ID fits (`transport/masque/session.go:561-565`), because otherwise every packet
takes the substitution path — which is the path with the documented silent-drop bug
(`masque-connectip.md` §1).

---

## 5. The low-memory geometry profile

The 16 KiB vs 32 KiB selection is a **mobile-only build tag**, read from exactly one source:

```sh
go run ./cmd/internal/appletags -mobile-only
```

* `cmd/internal/appletags/main.go:33` defines it; the doc comment states why the package exists: the
  low-memory CI gate and the memory benchmark previously read `DEFAULT_BUILD_TAGS_OTHERS`, which *"does
  not match what libbox builds"*, so those checks *"were testing a configuration no Apple binary
  ships"*.
* The tag reaches gomobile through **`-tags-not-macos`**, not `-tags`: `-tags` applies to every target,
  so a mobile-only tag placed there would reach macOS in a mixed build
  (`applebuildtags/tags.go:63`, `mobilebuildtags/tags.go:42`).
* `verify.yml:252-258` reads it and **fails rather than falling back** if the string is empty:
  *"Refusing to fall back to a guess: this step would compile the default geometry while claiming to
  exercise the low-memory one."*
* `client-macos.yml` asserts the tag set in **both directions** — required tags present, retired tags
  absent.

**The geometry tag is a real behaviour switch, not a size optimisation.** A build that claims
low-memory while compiling the default geometry is worse than one that does not claim it.

---

## 6. Status and what a consumer may conclude

`common/physicalpath/status.go` publishes an MTU block per hop, each with a `Generations` field. The
status surface is **read-only and republishes the model** (`status.go:394-398`, `:638-641`); values are
**copies, not views**.

**A status consumer may conclude:** the last proven value for a hop, and the generation it was proven
under. It must compare generations before treating a value as current.

**Where the code must report `UNKNOWN` with a reason:** wherever the kernel cannot know a value — the
status surface reports `UNKNOWN` with a named reason rather than a default, and
`Path.Exit()` reports `ok=false` while any hop is unknown, so a partly-verified path cannot be mistaken
for a verified one (`path-selection-validation.md` §2).

**`HopStatus` scalars are copies.** `agentf_hop_scalar_ownership_attack_test.go`
(`TestAgentFEveryHopScalarIsCopiedNotShared`) clobbers every `PathStatus.Hops` scalar — including the
nested MTU and `Generations` fields — and truncates the caller's slice, proving the status values are
copies. **This test is not about cross-kind cycles**; an earlier description of it as such was wrong.

---

## 7. Evidence

| Guarantee | Level | Re-check |
|---|---|---|
| Geometry poolability and no-realloc on `Advance`/`Prepend` | `VERIFIED_UNIT` | `go test -count=1 ./common/bufgeom/...` |
| The allocation ceiling | `VERIFIED_UNIT` in the **`!race`** build only | same |
| MASQUE PTB correction and the outer-QUIC cap | `VERIFIED_UNIT` | `go test -count=1 -timeout 8m -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./transport/masque/... ./protocol/masque/...` |
| The hysteria2 hard ceiling reaches the wire with ChromeParrot on | `VERIFIED_UNIT` | `go test -count=1 -run HardCeiling ./protocol/hysteria2/` |
| The low-memory geometry is read from one source | `VERIFIED_SOURCE` | `go run ./cmd/internal/appletags -mobile-only` |
| Status values are copies, not views | `VERIFIED_UNIT` | `go test -count=1 -run TestAgentFEveryHopScalarIsCopiedNotShared ./common/physicalpath/` |
| A real NIC's MTU behaviour | **`UNVERIFIED`** — needs a real TUN and root; a release blocker | `docs/fork/real-tun-validation.md` |
| Race-freedom | **`BLOCKED_BY_ENVIRONMENT`** | needs cgo |

**One withdrawn finding, recorded so it is not re-derived:**
`docs/fork/v016-path-mtu-audit.md`'s first PTB argument **used the wrong path and was withdrawn** by
`docs/fork/v016-path-mtu-audit-correction.md:11-32` — `flow_dispatch.go` is the *ForwardDispatcher*
branch, while hysteria2's QUIC socket goes through `stack_go_udp_socket.go`'s `GoUDPConn.transmit`.
**Read the correction, not the audit.**

---

## 8. AI cautions

1. **Do not collapse the three MTU quantities into one.**
2. **Do not cap the MASQUE outer QUIC packet with a constant.** It is capped by a *proven* detour
   capacity.
3. **Do not unpin `sing-quic`.** The proven ceiling reaches the wire only if that layer forwards it.
4. **Do not silently drop a too-large datagram.** It is PTB with a corrected MTU.
5. **Do not correct an MTU below `minimumLinkMTU`.**
6. **Do not apply the hysteria2 ceiling before the ChromeParrot override.** It must come after, or the
   fingerprint wins and the ceiling is inert.
7. **Do not read `docs/fork/v016-path-mtu-audit.md` without its correction document.**
8. **Do not assume `-race` covers the allocation ceilings.** They are `!race`-gated on purpose.
9. **Do not make a buffer size non-power-of-two.** It cannot be pooled.
10. **Do not remove the producer-side headroom reservation.** Every packet would take the substitution
    path — the one with the historical silent-drop bug.
11. **Do not hardcode the low-memory geometry tag.** It is read from `appletags -mobile-only`, and the
    CI step fails rather than guessing.
12. **Do not put the mobile-only tag in the shared Apple tag set.**
13. **Do not treat a status MTU value as current without its generation.** `status.go:816-823` reads
    `Generations` **before** the error and stamps it at `:831`, so a failure from an older generation
    is not shown as current.
14. **Do not describe `agentf_hop_scalar_ownership_attack_test.go` as a cross-kind test.** It is a
    status-view aliasing test.
15. **Do not update `docs/fork/physicalpath-controlpath-model.md`'s claims without checking
    `d46717dbd`.** It is stale on `ControlPath` ordering as well.
16. **Do not add a fingerprint or any post-construction size override to the MASQUE client** without
    first adding a test that catches the voided ceiling. See §3.
17. **Do not clamp a sub-1200 ceiling.** It is refused, not clamped (`path_mtu.go:36-41`,
    `client.go:168-179`, `hysteria2/outbound.go:181-190`).
18. **Do not size an upper protocol from `PortMTU()` alone** — that overstates the budget by
    WireGuard's 32 bytes (`path_mtu.go:169-193`; `mtu_budget_test.go:234-267`).
19. **Do not assign an inner MTU to `initial_packet_size` in either direction.** The 99-byte gap
    (`1280 + 51 − 1232`) is asserted at `outer_quic_mtu_test.go:181-185`.
20. **Do not read `PortMTU()` through `c.device` again.** The nil-device panic window is exactly when
    it is asked (`client.go:598-619`; `port_mtu_lifecycle_test.go:26-40`, `:78-108`).
21. **Do not advertise a PTB MTU below 1280**, and do not lower `maxPacketSize` below 65575
    (`session.go:56-61`; `packet_size_bound_test.go:80-150`).
