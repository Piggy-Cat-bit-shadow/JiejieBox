# Server-chain stall audit

> Companion to `ACTIVE_TCP_STALL_INVESTIGATION.md`. That document establishes a client-side
> mechanism. This one asks the question the brief's §13 makes primary: **which layer is the FIRST to
> stop making forward progress**, as distinct from the last one observed to be blocked.

```text
SERVER_CHAIN_AUDITED      = YES (from repository material)
SERVER_CHAIN_CONTRIBUTES  = YES (by architecture; not measured on a live VPS)
ROOT_CAUSE_LAYER          = UNDETERMINED (client and server are structurally equivalent here)
FIRST_STALL_LAYER         = UNDETERMINED
OBSERVED_BACKPRESSURE_LAYER = client ConnectionManager connectionCopy (upload)
EVIDENCE_LEVEL            = VERIFIED_SOURCE for the topology and the code path; INFERRED for the
                            propagation chain; NOT MEASURED on a live VPS
```

---

## 1. The real server path

Read from `docs/JIEJIE-SERVER.md` and `release/jiejie-production-topology.json`, not assumed from the
description. The residential hop is **server-side**, as the brief states, and the topology fixture
confirms it:

```text
inbound types : http, http, anytls, naive, shadowtls, shadowsocks
outbound types: direct, socks          (tags: `direct`, `residential-socks`)
```

```text
ChatGPT app
  -> client mixed-in                     (SOCKS/HTTP, userspace)
  -> client ConnectionManager / copy     ← the mechanism this investigation proved
  -> client outbound: AnyTLS / VLESS
  -> Internet
  -> VPS front door: Nginx TCP/443 ssl_preread by SNI+ALPN   (UDP/443 = sing-box MASQUE directly)
  -> VPS inbound: Residential-AnyTLS on loopback
  -> VPS route: resolve action, strategy ipv4_only, ordered BEFORE the routing rule
  -> VPS outbound: `residential-socks`   (SOCKS5, IPv4 TCP only, UDP rejected)
  -> residential exit (IPv4)
  -> IPv4 target (ws.chatgpt.com)
```

| Field | Value | Source |
|---|---|---|
| `VPS_INBOUND` | AnyTLS (also MASQUE H2/H3, Native Naive, ShadowTLS v3 + SS2022) | topology fixture |
| `VPS_ROUTING` | `resolve` action (ipv4_only) then route; ordered, pinned by `TestProductionResidentialChainOrdering` | JIEJIE-SERVER.md |
| `VPS_OUTBOUND` | `residential-socks`, type `socks` (SOCKS5) | topology fixture |
| `RESIDENTIAL_HOP` | the residential exit, IPv4 **TCP only** | JIEJIE-SERVER.md |
| `TARGET` | `ws.chatgpt.com:443` | report |
| `SERVER_SIDE_MUX` | **none configured** — two outbounds, no detour chain, no mux options | topology fixture |
| `SERVER_SIDE_BUFFERING` | sing-box's own copy path only; no relay wrapper, rate limiter or scheduler is configured | topology fixture |
| `SERVER_SIDE_KEEPALIVE` | **not configured**, so the fork defaults apply: `TCPKeepAliveInitial = 5 min`, `TCPKeepAliveInterval = 75 s` | topology has 0 occurrences of any `tcp_keep_alive*` key |
| `SERVER_SIDE_CONNECTION_MANAGER` | the same `route.ConnectionManager` and the same `connectionCopy` — identical source | see §2 |

Note on DNS: resolution happens **on the VPS**, deliberately. The residential SOCKS server is handed
an IPv4 address, never a hostname, and there is no SOCKS5h / remote-DNS mode. So DNS is not on the
stall path at either end — consistent with `DNS_NOT_IN_STALL_PATH` in the main investigation.

---

## 2. The server runs the same core, so it has the same gap

`SERVER_SIDE_CONNECTION_MANAGER` is not a guess. The server binary is built from the same `testing`
branch with a different feature profile (`release/DEFAULT_BUILD_TAGS_OTHERS`), and the connection
manager is not behind a build tag:

- `route/route.go` constructs the `ConnectionManager`; `route/conn.go` implements it.
- `connectionCopy` is the same function, with the same two goroutines.
- `ConnectionManager.Reclaim` still has exactly one production call site, on the network-reset path.
- There is still no steady-state sweep and no read deadline on the established path.

Therefore the VPS's own `VPS inbound socket ↔ residential-socks socket` flow is a proxied userspace
copy with **the same absent liveness path**. If the residential leg goes silent, the VPS behaves
exactly as the client does on a silent peer: the read parks, the write blocks, nothing tears it down.

```text
CLIENT_AND_SERVER_SHARE_SAME_CORE_BUG = YES   (VERIFIED_SOURCE: same code, no build tag)
```

---

## 3. Why the client is the LAST layer, not the first

This is the brief's §13 point, and the evidence makes it concrete rather than rhetorical.

In backpressure, the writer blocks **because the bytes it already wrote were accepted and are not
being consumed**. The client's upload copy blocks writing to the outbound socket, which means the
client's bytes reached the VPS and the VPS stopped taking more. The chain is:

```text
residential exit stops reading
  -> VPS write to residential-socks blocks        (VPS send buffer full)
  -> VPS stops reading from its inbound socket     (its copy upstream is blocked)
  -> client outbound send buffer fills
  -> CLIENT upload copy blocks in Write            ← what the investigation measured
  -> client stops reading from mixed-in
  -> the ChatGPT app's write blocks
  -> the app waits, sees nothing, and eventually gives up
```

So the client's blocked writer is **downstream backpressure arriving late**, not a client defect.
The main investigation proved the client *also* has the gap — the client would stall on its own if
its peer went silent — but proving the client *can* stall is not proving the client *did*.

```text
OBSERVED_BACKPRESSURE_LAYER = client ConnectionManager connectionCopy (upload goroutine)
FIRST_STALL_LAYER           = UNDETERMINED
```

And the discriminator between "client is the first layer" and "client is the last layer" is
structural, not statistical:

| Client's own path to the VPS | Client's write blocked? | Where the first stall is |
|---|---|---|
| healthy, VPS is not reading | yes | **downstream of the client** (VPS or beyond) |
| itself silently dead | yes | **the client** |

Both produce the identical client-side signature — which is exactly the same ambiguity the
healthy-backpressure result exposed, one layer up.

---

## 4. What ends a server-side stall, unaided

The server's own reclaim trigger is the same one the client has: a network change. A VPS does not
experience Wi-Fi roams or sleep/wake, so **on the server the trigger essentially never fires.** That
is worse than either phone.

What remains is TCP keepalive on the VPS→residential socket, with the fork's defaults:

```text
5 min   until the first keepalive probe
75 s    between probes
        x probe count (OS default; Linux commonly 9)
```

The decisive question is *what answers the probe*:

| Residential proxy state | Keepalive result | Outcome |
|---|---|---|
| OS gone (host dead, route blackholed) | probes unanswered | kernel closes the socket after the probe budget; the VPS read fails; the client is unwound |
| **OS alive, session stale** | **probes ACKed by the peer kernel** | **keepalive never fires; the stall is unbounded** |

The second row is the dangerous one and it mirrors the client-side conclusion exactly. It is also
the more likely one for a residential provider: the exit host is up, the NAT or provider session
that carried the flow may not be.

`TestBackpressure_KernelGiveUpWindow` measured the same property locally and found **no kernel
give-up in 6 minutes** with both stacks alive. So the unbounded case is real, not hypothetical.

---

## 5. Why the server chain could be the root cause even with a perfect client

If the first stall is at the VPS→residential hop, then **no client-side change can fix it.** The
client would still receive no data and would still block; the best a client-side change can do is
give up sooner and let the application reconnect — which is containment, not repair.

This is the brief's §21/§22 split, and the evidence supports keeping it split:

```text
PRIMARY FIX (unknown until the first stall layer is identified)
  - if client-side:   error/cancel propagation, half-close, ownership, stalled teardown
  - if server-side:   the same mechanism, but on the residential outbound, plus correct
                      propagation of a stalled residential leg back to the inbound flow
SECONDARY CONTAINMENT (justified regardless)
  - a bounded fail at the relay layer, so a downstream silence cannot hold a flow forever
```

A client-side fail-safe is legitimate **only** as containment. Shipping it as the fix would hide the
server-side root cause, which is exactly what §21 forbids.

---

## 6. Experiments that would settle it, and what they need

Listed in the order that most cheaply closes the question. None were run: every one needs a live VPS
or residential proxy, and this host has neither (see §7).

| # | Experiment | What it discriminates |
|---|---|---|
| 1 | **`ss -tinp` on the VPS during a stall**, both sockets. Which `Send-Q` is growing and which `Recv-Q` is frozen? | the first stall layer, directly |
| 2 | VPS → `direct` egress instead of `residential-socks`, same target | whether the residential hop is required to reproduce |
| 3 | VPS → residential → **blackhole** target (no FIN, no RST) | whether the client ends up in the exact local-reproducer state |
| 4 | Stop the residential upstream reading, but do not close | how backpressure propagates layer by layer, and how fast |
| 5 | Flow correlation: client source port ↔ VPS inbound socket ↔ VPS outbound socket | that the three are one flow, so the layers can be compared at all |
| 6 | `ss -tino` retransmit/RTO columns on the VPS residential socket | whether the kernel is retransmitting (path silent) or idle (path healthy) — which decides whether keepalive can ever help |

Experiment 6 is the cheapest way to tell §4's two rows apart, and it needs only one `ss` invocation
during a stall.

---

## 7. Evidence boundaries, stated plainly

```text
REAL_SERVER_PATH            = VERIFIED_SOURCE (topology fixture + JIEJIE-SERVER.md)
SERVER_SIDE_BUFFERING       = VERIFIED_SOURCE (no relay/limiter/scheduler configured)
SERVER_SIDE_MUX             = VERIFIED_SOURCE (none configured)
SERVER_SIDE_KEEPALIVE       = VERIFIED_SOURCE (not configured; fork defaults apply)
SERVER_SIDE_CONNECTION_MANAGER = VERIFIED_SOURCE (same source, no build tag)
DIRECT_EGRESS_TEST          = NOT_RUN (needs a live VPS)
RESIDENTIAL_EGRESS_TEST     = NOT_RUN (needs a live residential proxy)
BLACKHOLE_TEST              = NOT_RUN against the server chain (needs a live VPS);
                              RUN and PASS against the client mechanism
CLIENT_TO_SERVER_CORRELATION = NOT_RUN (needs both ends at once)
```

No VPS credentials, no residential proxy endpoint and no server configuration beyond the repository's
own topology fixture are reachable from this host. The harness that would run experiments 1–6 does not
exist here and was not fabricated.

**What is established without a VPS** is architectural and sufficient to change the conclusion: the
server runs the same copy path with the same absent liveness mechanism, the client's observable
symptom is consistent with being the *last* layer rather than the first, and no client-side fix can
repair a stall that begins at the VPS→residential hop.

---

## 8. Consequence for the fix gate

```text
SERVER_CHAIN_AUDITED            = YES
SERVER_CHAIN_CONTRIBUTES        = YES (architecturally; unmeasured)
ROOT_CAUSE_LAYER_IDENTIFIED     = NO
FIRST_STALL_LAYER               = UNDETERMINED
ROOT_CAUSE_CONFIGURATION_BLOCKED = the layer identification needs a live VPS capture
```

The brief's gate (§19) requires `ROOT_CAUSE_LAYER_IDENTIFIED = YES` and
`ROOT_CAUSE_CONFIDENCE >= 90%` before a production fix. Neither is met, and the reason is now
specific rather than general: **the two candidate layers produce the same client-visible signature,
and separating them needs one `ss` invocation on the VPS during a stall.** That is the single
highest-value next action in this whole investigation, and it is cheaper than any experiment run so
far.
