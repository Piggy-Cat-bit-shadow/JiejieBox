# Resource ownership matrix

One row per protocol, one column per question stage 2 asks. "Admission" means the
process-wide creation budget in `common/resourceguard`; "generation" means
`common/runtimecoord`'s network epoch; "reuse" is a pooled session/connection
handed to a new stream.

Paths are repo-relative; every cell was read from source in this stage.

## Matrix

| protocol | owned resources | creation admission | reuse admission | network generation | old-generation reuse after change | idle / close owner |
|---|---|---|---|---|---|---|
| **MASQUE (CONNECT-IP)** | 1 HTTP/3 QUIC conn + 1 CONNECT-IP stream per session; userspace TUN stack; UDP socket | **none** (`transport/http/client_h3.go:424,428`) | n/a — one session, no pool | no epoch read; `InterfaceUpdated`→`RestartSession` (`protocol/masque/client.go:598-600`) | old session cancelled by `RestartSession` (`transport/masque/client.go:455-465`) | endpoint has **no Close**; scope closes `tunnelDevice` and `client` (`protocol/masque/client.go:332,337,355`) |
| **XHTTP (H1/H2/H3)** | per-physical-conn transport (`http1.Transport`/`http2.Transport`/own `http3.Transport`), xmux pool, logical streams | H1/H2 **yes** (TLS seam); H3 **no** (`transport/v2rayxhttp/http3.go:153-157`) | no — a pooled conn is handed out without TLS dial | pool-local generation; no epoch read | `Reset`→`RetireSuspect` drains, no new streams (`client.go:333-345`, `xmux.go:579-604`) | `Client.Close`/`CloseIdleConnections`/`RetireSuspect`; `SetKeepIdle(true)` no-op; **no closed flag** until this stage's fix |
| **VLESS REALITY / Vision** | TCP+TLS(uTLS) conn, optional mux session, Vision wrapper over the same socket | **yes**, one TLS permit (`common/tls/client.go:178-205`) | mux reuse: no handshake, no permit | `InterfaceUpdated` resets mux (`protocol/vless/outbound.go:166-186`) | mux pool not reachable by `RetireSuspect` (`:227-230`) — deliberate | outbound forwarder `Close`/`CloseIdleConnections`; mux session `Reset` |
| **AnyTLS** | external `sing-anytls` session pool; TLS conn per session; UoT router | **yes** per dial (`protocol/anytls/admission.go:55-66`) | **yes — reuse is charged** (permit precedes the pool check) | `InterfaceUpdated`→`Reset` (`outbound.go:154-156`) | `Reset` closes all sessions; a creation that crosses it publishes unconditionally in the external client (`dep/client.go:156-163`) | external pool + `CloseIdleConnections`; no completion signal |
| **Mieru** | SDK client/mux/underlays (TCP or UDP socket), per-stream sessions | per **socket** only (`protocol/mieru/dialer.go:85-120`) | no (reused underlay never reaches the seam) | `InterfaceUpdated` drains in-flight, swaps runtime, `generation++` (`outbound.go:377-442`) | non-fallback path rechecks nothing (`:512-519`); fallback path rejects stale (`:590-611`) | SDK `Mux` cleaner every 5 s; SDK owns close |
| **TrustTunnel** | one shared session (HTTP/2 conn or QUIC conn), `_udp2` stream per PacketConn | **yes**, once per shared creation with waiter join (`transport/trusttunnel/client.go:601-625`) | no | `Reset` discards pending and closes session (`client.go:315-327`); publish rechecks `discarded` (`:640-662`) | late creation closed, not published | per-session idle watcher; `CloseIdleConnections` skips `streams>0`; `RetireSuspect` drains |
| **Naive / Cronet** | per-outbound Cronet engine set (concurrency engines), H2 fallback engine, native threads/sockets | **none** | n/a — engine owns its pools | no engine rebuild; only H3 fallback window reset + `CloseAllConnections` (`protocol/naive/outbound.go:344-355`) | engine and its QUIC/H2 pools survive the change; only connections are closed | `scope.Add(h.client.Close)` / `h2Fallback.Close`; `Close` not nil-idempotent |
| **AWG / WireGuard** | per-endpoint userspace device + Go stack, bind/UDP socket, peers, egress pool | **none** | n/a | **only generation-aware owner**: `runtimecoord.Register` + rebind lease (`transport/wireguard/endpoint.go:153-157`, `recovery.go:426,616`) | bind reopened, stack kept; in-flight rebind not cancelled; dial never reads generation | `Endpoint.Close` → device swap→Down→Device.Close→bind close; pause via `SetKeepIdleConnections`→`Down/Up` |

## What the matrix makes visible

1. **Admission is not a runtime property, it is a per-protocol decision.** Four
   protocols admit something, four admit nothing, and the four that do admit at
   three different layers (per stream, per socket, per shared session). The
   shared runtime has no notion of "a creation happened".
2. **Reuse is charged exactly once in the tree, and it is the wrong protocol.**
   AnyTLS charges the creation permit before asking the pool, so a stream that
   creates nothing can queue or be refused. TrustTunnel charges the shared
   creation and lets waiters join free — the semantics the other seams describe
   in their comments but do not implement.
3. **Only WireGuard reads a generation.** Every other protocol either relies on
   `InterfaceUpdated` fan-out or does nothing. `Coordinator.Stale()` exists but
   has no production caller (`common/runtimecoord/coordinator.go:499-536`).
4. **Only TrustTunnel re-checks a publish against a reset.** Mieru does it on its
   fallback path only; AnyTLS not at all (in the external module).
5. **On-demand tunnels are reachable by the eligibility pass, not by the idle
   walks.** That asymmetry is deliberate (`route/reusable_owner_inventory_test.go`
   pins it) and it is also where a live-traffic suspend can happen.
6. **Close has three different meanings.** `CloseIdleConnections` = release what
   carries nothing; `ReuseSuspect` = refuse new work, drain the rest; endpoint
   `SetKeepIdleConnections(false)` = suspend the tunnel. Only the first is
   idempotent by contract; only the third can cut live traffic.

## Per-protocol call graphs (abridged)

Each arrow is a concrete call. A resource is named at the point it is created,
and the closer is named at the point it runs.

**MASQUE** `ClientEndpoint.Start` → `http.NewClient`(H3) → `masque.NewClient`
→ `newSession` → extended CONNECT stream. Close: `Box` scope → `client.Close`
(twice: scope and `ctx.Done` goroutine) → `httpClient.Close` → H3 conn close.
Network: `InterfaceUpdated` → `RestartSession` → cancel stream + reset H3.

**XHTTP** `Outbound.DialContext` → `Client.DialContext` → `xmux.getContext`
→ pooled conn or `newConn` (lazy transport) → `dialMode` → upload/download
streams. Close: `Client.Close` → `xmux.Close` (detach busy, close idle).
Reuse boundary: `RetireSuspect` → detach + drain.

**REALITY/Vision** `Outbound.DialContext` → `tls.NewDialer.DialTLSContext`
→ `handshakeWithAdmission` → `aTLS.ClientHandshake` → `RealityClientConfig.
ClientHandshake` → `uTLS` handshake; Vision wraps the same conn after the
handshake. Close: Vision `Close` closes `netConn` when XTLS direct-write is
engaged, else the vless conn.

**AnyTLS** `Outbound.DialContext` → `dialAdmitted` (permit) → `sing-anytls
Client.DialContext` → idle session pop or `createSession` → TLS dial. Close:
`Client.Close` (external) + `CloseIdleConnections` hook.

**Mieru** `Outbound.DialContext` → runtime selector → `MieruClient.DialContext`
→ `Mux.DialContext` → reuse underlay or `newUnderlay` (admitted socket) →
new session. Close: `transportRuntime.stop` → SDK close mux.

**TrustTunnel** `Outbound.DialContext` → `client.openStream` → `getSession`
→ live session, pending offer waiter, or install offer → `completeOffer`
(`AdmitCreation` in production) → `dialSession` (H2 or QUIC) → `publishOffer`
with discard check. Close: `Reset` (network) or `Close`; idle watcher per
session.

**Naive** `Outbound.Start` → `cronet.NewNaiveClient` (engines) + H2 fallback
engine → CONNECT per flow via native stack. Close: scope → `Close` →
native Shutdown+Destroy.

**WireGuard** `Endpoint.Start` → `device.NewDevice` (userspace stack, peer
router) → `bind.Start` → handshake; dials go `Endpoint.DialContext` →
`stackDevice.DialContext` → `Go.DialTCP`. Close: `Endpoint.Close` → recovery
close → `device.Swap(nil)` → `Down` (bind close, peers stop) → `Device.Close`
(stack/tun close).
