# Native Naive and the HTTP transport stack

## Naive outbound H3 fallback after v0.1.9

**VERIFIED_SOURCE**, testing target `011f3fdcd99`: `protocol/naive/outbound.go` is gated by
`with_naive_outbound`. Its `quic_fallback_to_h2` option is opt-in and requires `quic: true`;
otherwise construction rejects the combination. `protocol/naive/h3_fallback.go` retries eligible
H3 transport failures over H2 and keeps a per-outbound, network-resettable 30-second failure window;
attempt ordering prevents an older failure from overwriting a newer result. HTTP status responses and
caller cancellation do not establish an H3 transport failure. The retry includes the CONNECT
handshake, and reserves a retry budget. An internally bounded probe that expires is ambiguous: that
flow may retry over H2, but it does not arm the failure window. The Cronet outbound rejects
certificate pin options and uTLS rather than silently treating them as supported. See
`option/naive.go` and the fallback tests. This
does not assert interoperability with every server.

At the `cb71cf80d72` pin, the clean local Cronet fork source `c9c0e6ec8845` declares
`NaiveClient.Start() (err error)` and assigns a recovered startup panic to that return on its cleanup
path. This supersedes the historical audit's unnamed-return finding; no native startup-panic or
target-device run was performed here, and a panic during cleanup is a separate condition.

**Scope:** `protocol/naive/*` (inbound + tag-gated outbound), `transport/http/*` (the fork's largest
single addition), `common/httpclient/*`, `route/packet_destination_guard.go`.

**Provenance — the earlier knowledge base got this wrong.** `protocol/naive` had **4 files** at the
upstream ancestor `e4b7e692`; at `a267e99` it has **31**, across **64 fork commits**. The fork's own
`docs/FORK-DIFF.md` row 15 states it plainly: *"**Native Naive** — A NaiveProxy server inbound, with
UoT, padding, masquerade and pre-authentication resource controls."* This is a **fork subsystem**, not
a dependency bump.

`go.mod` carries `caddyserver/certmagic` and `caddyserver/zerossl` for certificate management;
that does not mean the Naive server is implemented with Caddy. The HTTP engines are
`golang.org/x/net/http2` + `.../http2/h2c`
(`protocol/naive/inbound.go:32-33`) and the fork's `github.com/sagernet/quic-go` (replaced at
`go.mod` replacements). The client integration is in `protocol/naive/outbound.go`, while its
Cronet engine lives in the separate `cronet-go` dependency. A server-side codec test passing proves
nothing by itself about the client.

**Load this module when changing:** the Naive inbound/outbound, HTTP/2 or HTTP/3 flow-control bounds,
`transport/http`, `http_clients`, the UoT data path, or the packet-destination guard.

---

## 1. Two implementations, one protocol

| Side | Where | Notes |
|---|---|---|
| **Server inbound** | `protocol/naive/` in this repository | registered in the **minimal server registry** as a production protocol (`include/registry.go:70 naive.RegisterInbound`) — the inbound is **always** registered |
| **Client outbound** | the fork's `cronet-go` | selected by `//go:build with_naive_outbound` (`include/naive_outbound.go:1`) vs a stub (`include/naive_outbound_stub.go`) wired at `include/registry.go:101` |

> **`DEFAULT_BUILD_TAGS_OTHERS` deliberately omits `with_naive_outbound`.** The Linux server therefore
> profile includes a Naive **inbound and no Naive outbound**. At the current Cronet fork pin,
> `cronet-go/all` has Linux and Windows platform files as well as Apple files; the Windows files
> require `with_purego`, which the Windows profile adds. A local test using exact-pinned Cronet
> source and purego passed, but does not prove native library loading or artifact support.

Reference-implementation boundary: the two custom codecs are independent, and the two agreeing with
each other does not substitute for comparison with the reference. Machine-readable padding vectors
shared across both repositories are in `test/jiejie/reference/naive_padding_vectors.json`.

---

## 2. Server inbound behaviour

### Authentication and CONNECT

Users authenticate with the configured credentials. An authenticated CONNECT establishes the tunnel;
authentication failure and unauthenticated traffic are handled by the **masquerade** path when one is
configured. The order is **parse → authenticate → tunnel**.

The stock NaiveProxy path is TCP; UoT is a separate extension. Actual interoperability with a stock
client is not established by the local codec tests in this calibration; see the evidence table.

### UoT v1/v2 — the distinction that matters

| Mode | How the destination is carried |
|---|---|
| **v2 Connect mode** | the session header carries a **fixed destination**, checked when the session is established |
| **v1 and v2 non-Connect** | the address in the session header is only a **session identifier**; **every subsequent datagram carries its own destination** |

An authenticated CONNECT to a magic address selects the UoT decoder —
`sp.v2.udp-over-tcp.arpa` for v2, `sp.udp-over-tcp.arpa` for v1. **The magic address is never a real
destination.**

**Two address encodings coexist and must not be mixed:** the v2 request header uses the SOCKS5
serializer, while per-datagram addresses use the UoT address parser. Confusing them yields
`unknown address family: 0`. The v2 request header travels **inside** the tunnel, so when padding was
negotiated it must itself be wrapped in a padding frame.

Unpacked datagrams are handed to the router as a packet connection with the authenticated username
preserved, so ordinary routing, DNS, outbound selection and access control all apply.

> A previous conclusion that "v2 non-Connect is a P0 product defect" was a **test-fixture error**, not
> a product bug. The transport matrix is pinned by
> `test/jiejie/jiejie_naive_uot_transport_matrix_test.go`.

### Padding and framing

```text
padding FIRST, then the payload budget:
    payload_budget = 65536 - 3 - padding
    3 + payload + padding <= 65536
    writerMTU = 65278, front headroom = 3, rear headroom = 255
```

Frame layout: `[2-byte big-endian data size][1-byte padding size][data][zero padding]`. **Both sizes
have fixed widths, so a hostile frame cannot drive a large allocation.**

The rules that must not be relaxed:

* **Framing is enabled solely by the request `Padding` header.** A CONNECT without it is a valid plain
  HTTP proxy request and gets byte-for-byte I/O. (The *response* `Padding` header is sent for every
  authenticated CONNECT regardless — an independent concern.)
* Padding applies to the **first 8 frames** in each direction, then is dropped entirely.
* In-place framing is skipped above `maxFrameSize - 3 - 255`. **The padding range 0..255 is
  deliberately never clamped to the buffer.**
* **A short write is a failure**: a `Write` returning fewer bytes than requested with a nil error is
  reported as `io.ErrShortWrite` and never silently truncated. The frame counter advances **only after
  the whole frame was written**, so server and peer framing cannot diverge.
* A frame carrying padding but no data is consumed and the read continues, so a zero-byte read with a
  nil error is never handed to the caller.
* Data-path failures return an error rather than panicking on a normal failure mode.

**HTTP/1 CONNECT payload is raw bytes: it is NOT framed even when a `Padding` request header is
present.** The header is consulted only on HTTP/2 and HTTP/3. On HTTP/2 the tunnel writer flushes
through a response controller, and a failed flush is propagated as an error and tears the tunnel down
rather than being silently discarded.

Pinned by `protocol/naive/audit_write_contract_test.go`.

> **A historical "panic fix" was itself a protocol change**: a test misused `buf.Resize(start, end)`
> where the second argument is a **length**, not an end offset. The fix was reverted
> (`3c6e0aad3` → `fe5789539`) and the full 0..255 padding range restored.

### Masquerade

`masquerade` serves requests that are **not** authenticated Naive proxy requests — ordinary browser
traffic, probes, and proxy attempts with no or wrong credentials. It uses the same schema and semantics
as the Hysteria2 and HTTP inbound masquerade, so a normal website can share the port.

* **A web response only.** It can never establish a proxy tunnel: the tunnel path requires successful
  authentication first, and the masquerade handler has no access to the proxy or UoT data paths.
* Unset means the upstream behaviour: a proxy authentication challenge or a bad-request status.
* It does not make the service undetectable.

The current connection-count limiter refuses over-limit peers in `limitedListener.Accept` by closing
the accepted socket before HTTP request handling. It does not generate an HTTP 429 response there;
the old 429-decoy claim is stale. Neither a masquerade nor a particular refusal shape proves the
service cannot be fingerprinted. See `server_limited_listener.go` and `server_limiter_test.go`.

### Pre-authentication resource controls (`server_limits`)

`server_limits` can constrain resources before authentication; connection counts also include
authenticated connections. Every field is optional and omitting the block leaves these explicit
limits disabled. Other transport, runtime or operating-system limits may still apply.

| Field | Bounds |
|---|---|
| `max_connections` | total concurrent connections on the inbound, authenticated or not — the only bound covering the whole listener |
| `max_connections_per_ip` | concurrent connections from one source address. **The key is the real transport peer, never a forwarded header** — a client that can name its own source address would trivially evade a per-IP limit |
| `header_timeout` | how long a connection may take to deliver a **complete** request once the handshake is done; request phase only |
| `max_tracked_ips` | ceiling on tracked source addresses |

Deliberate omissions, each with a reason:

* **No handshake timeout here.** `tls.handshake_timeout` already exists and covers a peer that stalls
  *during* the handshake; once the handshake succeeds the deadline is cancelled, so a peer that
  completes it and then goes silent is covered by `header_timeout` instead. A second control for the
  same phase would give an operator two overlapping fields that can disagree.
* **No idle timeout here.** `HTTP2Options.IdleTimeout` already provides one and is wired to
  `http2.Server.IdleTimeout`.
* **An established tunnel is never subject to `header_timeout`.** A tunnel legitimately idle for longer
  than the header timeout — which long-lived connections routinely are — must not be torn down.
* `server_limits.idle_timeout` was resolved and **never read**; it was removed (`724035c76`) with the
  reasoning recorded so it is not reintroduced.

Counting happens at **accept**, wrapping the outermost listener, so it covers both a peer that never
completes the handshake and one that never sends a request. **The slot is released exactly once,
however the connection ends, including a TLS handshake failure.** The limiter's tracked-IP map is
bounded, swept on an amortized cadence, and **fails closed at its cap** rather than evicting a tracked
source.

`HTTP2Options` bounds the HTTP/2 server: `max_concurrent_streams`, `idle_timeout`,
`stream_receive_window`, `connection_receive_window`. Leaving every field unset keeps the upstream
`net/http` and `x/net/http2` defaults. **The receive windows are server-side admission windows, not
the client's** — they bound how much a peer may have in flight *toward this server*. Values that would
wrap when narrowed are rejected. `http2InitialWindowSize = 65535` (`inbound.go:458`) with a table of
minimum accepted values (`:409-418`); the comment at `:350-364` records that the inbound previously
used a bare `&http2.Server{}` so every bound was unbounded.

### HTTP/3 availability — fatal vs non-fatal

HTTP/3 is served by an **injected constructor** `ConfigureHTTP3ListenerFunc` (`inbound.go:37`),
installed by `protocol/naive/quic/inbound_init.go:177` when the QUIC tag is on and by a **failing
stub** at `include/quic_stub.go:48` when it is not.

That polarity is the whole point: it lets `decideHTTP3Availability(constructorPresent bool, network []string)`
(`inbound.go:316-324`) and `decideHTTP3AvailabilityForOptions` (`:334`) distinguish three cases:

| Condition | Result |
|---|---|
| a constructor exists | `http3Available` |
| no constructor, **and the inbound also has TCP** | `http3UnavailableNonFatal` — *"A missing optional transport must not take down TCP CONNECT"* (`:220-223`) |
| no constructor, **and H3 was the only transport** | `http3UnavailableFatal` (`:212-219`) |

**The stub installs a failing constructor rather than leaving the field nil** so that "absent" and
"present but broken" are different states. Making the stub nil would collapse them.

### Reserved headers and ALPN

`:scheme` and `:path` are refused on HTTP/2 and HTTP/3 CONNECT (`inbound.go:507-521`). Reserved extra
headers are refused at `protocol/naive/outbound.go:367`. ALPN is built from `http2.NextProtoTLS` plus
an optional `"h3"` (`inbound.go:256-294`, `http3ALPN = "h3"` at `:245`).

---

## 3. Target ACL / SSRF boundary — the highest-risk area

The client chooses the proxy destination, so the destination must be constrained. **The rule of
record: resolve the target, then check the resolved address, then dial exactly that address.**

**Resolution must happen before the address check.** `actionResolve` writes its results into
`metadata.DestinationAddresses`, and the connection manager passes those candidates to
`DialSerialNetwork`; its non-parallel fallback dials an already-resolved IP. A parallel-capable
outbound receives the original destination and the candidate list, so verify that implementation's
contract and any recovery path before claiming universal address pinning. Without a `resolve` action
a domain target can avoid an IP-based deny rule; the intended session-level policy requires it.

A DNS-rebinding test confirms this is not theoretical: a domain that first resolves to an allowed
address and then to loopback is rejected on the second resolution, with the query count proving a
genuine second lookup occurred (`TestJiejieTargetACLRebindingCannotBypass`).

### Per-datagram destination guard for UoT

For non-Connect UoT sessions **each datagram carries its own destination**, so a session that passed
the session-level check could otherwise send later datagrams somewhere else entirely.
`route/packet_destination_guard.go` checks destinations decoded from datagrams and drops rejected
ones. Exact session destinations reuse the session decision; other destinations are memoized for
the guard's lifetime, so rules and DNS are not re-evaluated on every subsequent packet to that key.

| Property | Value |
|---|---|
| Enabled for | flows marked `metadata.UoTDatagramDestinations`, used for non-Connect UoT sessions; the marker, not an inbound protocol name, controls the guard |
| Intercepts on | the **read** side — the write side has a batch fast path that cannot be guaranteed to see every packet, so the read side is the only position that cannot be bypassed |
| Cost | decisions are memoized per destination, so an allowed datagram costs a map lookup after the first |
| Normalisation | IPv4-mapped IPv6 is normalised to IPv4 first, so `::ffff:127.0.0.1` cannot smuggle a restricted destination past the rule |
| Rejection | a rejected datagram is **dropped without an error**, so other datagrams in the same session are unaffected |
| No rule matches | the guard **permits** — `common/uot/router.go` is shared by twelve protocol inbounds, so a blanket deny would be catastrophic |

**The guard does not re-route to a different outbound.** Its verdict is permit/deny only; a rule that
would select a different outbound based on the per-datagram destination does not take effect, and the
datagram still uses the outbound chosen when the session was established.
`TestPacketDestinationGuardOutboundSelectionPrecondition` fails if anyone adds such a rule.

For a domain carried by a datagram, `checkPacketDestination` resolves addresses for its rule decision,
but `ReadPacket` returns the original destination and the guard caches the decision by domain and port.
It does not itself pin a resolved address into the forwarded datagram. Therefore the session-level
resolve/check/dial guarantee above must not be generalized to this per-datagram path: downstream
resolution and DNS changes require separate analysis before claiming rebinding resistance.

Both the allowed and rejected memo maps retain distinct destination keys for the life of the
session without an eviction bound (`packet_destination_guard.go`). A peer supplying many different
destinations can therefore grow retained state. `TestPacketDestinationGuardMemoGrowsWithDistinctDestinations`
illustrates the map growth by inserting keys directly; it is not an end-to-end attack measurement.

**Rule order is the security property:** resolve → exception → reject (`d4ead1498` → `7029722bf` →
`1dc734f75`/`3bbf17907`/`d1f3798a6`).

### Why the deny list must include the host's own address

`<VPS_SELF_IP>:443` must stay permanently rejected. Production TCP/443 is held by Nginx, which forwards
by SNI into this inbound, so allowing a client to re-CONNECT the self address on 443 re-enters the same
front door and forms a self-proxy recursion:

```text
Naive -> <VPS_SELF_IP>:443 -> Nginx -> Naive -> <VPS_SELF_IP>:443 -> ...
```

**The CONNECT hostname and the TLS SNI inside the tunnel are not bound to each other**, so "allow self
443 for this one domain" is not a reliable recursion defence: a client can CONNECT an allowed domain and
then switch SNI inside the tunnel. A pre-dial rewrite to a constrained internal ingress is the
documented topology's mitigation; its safety depends on the actual ingress and route configuration.

There is exactly **one conjunctive TCP-only self-SSH:2222 exception** (`7029722bf`).

### Self-hosted web: rewrite the target, do not open the public port

A self-hosted web zone resolves to the host's own public address and is caught by the self-address
rule; the symptom is a client failing to reach its own site. The fix is **not** to open
`<VPS_SELF_IP>:443` but to rewrite the target to a loopback-only, web-only internal ingress, using
`override_address`/`override_port` on a `domain_suffix` rule scoped to `network: tcp` and `port: [443]`.

`override_address` clears `metadata.DestinationAddresses`, so the dial target is the loopback ingress
and never the resolved public address. **Even a client that CONNECTs a self-hosted domain and then
switches to an unrelated TLS SNI still terminates at the loopback ingress**, so the recursion path is
*cut* rather than merely discouraged.

`domain_suffix` is raw suffix matching: write the bare suffix, not a `*.` prefix. Within one rule's
destination group, `domain`/`domain_suffix` and `ip_cidr` are **OR rather than AND**, so "only when
this domain resolves to the self address" cannot be expressed by combining them.

### Server-internal connections are out of scope for this ACL

The target ACL constrains the **client-chosen proxy target**; it must not break connections the server
itself initiates. The Naive and MASQUE web masquerade backends, the AnyTLS fallback backend, the local
DNS resolver and the ShadowTLS handshake target are established by server components that do not pass
through the sing-box Router, so inbound-scoped rules do not match them.
`TestJiejieTargetACLDoesNotBreakMasqueradeBackend` pins that in the same instance where the rules
genuinely reject loopback, the masquerade still reaches its loopback backend while a client CONNECT to
loopback is still refused.

### The CONNECT status code is not a rejection signal

The inbound returns `200 OK` when it accepts a CONNECT, and a router-level `reject` closes the
connection **afterwards**. A CONNECT to `127.0.0.1:443`, to the host's own address, or to a proxy entry
name all return `200 OK` and then transfer no data. **A test must write into the tunnel and observe
whether the origin answers — the status code must not be used to judge whether a target was rejected.**

### Known limits (recorded, not resolved)

* **The host's own public address is not in the deny set.** The repository has no trustworthy record of
  it, so it was deliberately not guessed. Confirm on the host before calling this complete.
* Suggested CIDR deny lists are not validated against a production environment.
* Per-IP limiting has been exercised only from a single loopback address, including its IPv4-mapped
  form; behaviour under a genuinely distributed source is untested.
* The production topology deliberately configures **no** `server_limits`.
* The limiter's fail-closed path and amortized sweep are proven at unit-test level, not under load.
* The self-hosted-web runtime tests need a second loopback address, which macOS cannot do, so they
  **skip on macOS** and execute only on Linux.
* `test/jiejie/jiejie_naive_ipv6_udp_test.go:141,169` skip with *"no usable IPv6 address; **NOT a
  pass**"*. The fork's skip wording is deliberate throughout — treat `SKIP` as `UNVERIFIED`.

---

## 4. `transport/http` and `common/httpclient`

`transport/http` is the fork's **largest single addition**: 90 files at `a267e99` versus 20 at
`e4b7e692`, touched by **137 commits**. Note that `protocol/http/*` (the inbound) and
`transport/http/*` (the client transport) are different worlds.

**HTTP client transport requirement.** `common/httpclient/transport_requirement.go`
(`TransportRequirement{AnyOf, AllOf, Conditional}` at `:23-36`) computes what transports a configured
HTTP client needs:

| Configuration | Requirement |
|---|---|
| version 1 or 2 | `AllOf{tcp}` |
| version 3 **with** fallback | **`AnyOf{udp, tcp}`** with `udp` **`Conditional`** (`http3FallbackRequirement()` `:54-64`) |
| version 3, no fallback | `AllOf{udp}` |

> **HTTP/3-with-fallback requires `AnyOf{udp, tcp}`, not both.** Requiring both would refuse a
> configuration that works. This is the same shape as DNS's `udp ⇒ AllOf{udp} + Conditional{tcp}`
> (`dns/transport/requirement.go`), where the TCP retry only fires on a truncated response.

Enforced at `common/httpclient/transport_capability.go:30`, called from
`common/httpclient/manager.go:122`.

Five real defects were found in the wrapper/capability layer and fixed with red-first controls:

| Where | Defect | Consequence |
|---|---|---|
| `route/conn.go` | batch creation not forwarded | a batch-capable UDP written one datagram per syscall |
| `route/packet_destination_guard.go` | write side unpinned | one added method batches straight to the raw socket, skipping the check |
| `transport/http/stream_error.go` | `WriteBuffer`/`ReadBuffer` returned `nil` for a non-`ExtendedConn` | **success reported for a buffer never written** |
| `transport/v2rayhttp/conn.go` | `ReaderReplaceable` true while `responseCache` held bytes | **silent truncation of the response head** |
| `transport/trojan/protocol.go` | a load-bearing handshake invariant; the package has `protocol_replaceable_test.go`, but its existence alone does not establish coverage of every flag | an inverted flag can send the payload with no handshake |

Three audit leads were **disproved rather than fixed**: `v2raygrpc`'s `syscall.Conn` (byte-identical to
`grpc@v1.79.1`), `normalizingConn`'s advertised capabilities (the silent `nil` was the defect, not the
advertisement), and `trackedConn`'s activity tracking.

---

## 5. Evidence

| Guarantee | Level | Re-check |
|---|---|---|
| Padding/write contract, short writes, frame counter | `VERIFIED_UNIT` | `protocol/naive/audit_write_contract_test.go`, `padding_test.go`, `padding_segmentation_test.go`, `padding_zero_frame_test.go`, `audit_padding_test.go`, `fuzz_padding_test.go` |
| UoT v1/v2 transport matrix | `VERIFIED_UNIT` | `test/jiejie/jiejie_naive_uot_transport_matrix_test.go` (separate module) |
| Target ACL rebinding example | historical test evidence for `TestJiejieTargetACLRebindingCannotBypass`; not a universal or per-datagram guarantee | separate `test/jiejie` module |
| Per-datagram UoT guard | `VERIFIED_UNIT` | `TestJiejieTargetACLUoTV1MultiTargetChecksEachDatagram`, `TestJiejieTargetACLUoTV2NonConnectMultiTarget`, `TestPacketDestinationGuardPermitsWhenNoRuleMatches`, `TestPacketDestinationGuardExposesNoBypassReadPath`, `TestPacketDestinationGuardOutboundSelectionPrecondition` |
| The guard does not break server-internal backends | `VERIFIED_UNIT` | `TestJiejieTargetACLDoesNotBreakMasqueradeBackend` |
| HTTP/2 resource bounds, H3 availability decision | `VERIFIED_UNIT` (needs `with_quic`) | `protocol/naive`, `option/http_resource_bounds_test.go` |
| Batch-capability loss | `VERIFIED_UNIT` with a real red control | `route/udp_batch_capability_test.go` (3 tests) |
| Interop against the official NaiveProxy client and Caddy | **`UNVERIFIED` here** — the reference suite was not run in this calibration; Verify runs a different selected test in the separate `test/` module | `run-jiejie-suite.sh` (~20 min, needs bash + a reference binary) |
| Real-network STUN, IPv6 UDP origin, Caddy performance comparison | **`UNVERIFIED`** — explicitly recorded as not done | — |
| Device behaviour | **`UNVERIFIED`** | needs a device |

---

## 6. Provenance

| Commit | Type | What it did |
|---|---|---|
| `d53c867b7` | FEATURE | **the fork's first fork-authored commit** — the HTTP inbound masquerade handler |
| `45f6686a6` | FEATURE | **EXTERNAL-ADAPTED** the native Naive inbound (reference: `klzgrad/forwardproxy@naive d62c80d3dd2c706b6b87579844d2397bddd18317`, cited at lines 320/352 and in the bufReader comment) |
| `77607290f`, `ec1e7ab29` | FEATURE | production registration of the inbound |
| `7454a8add` | FEATURE | padding negotiation and the parse→authenticate→tunnel order |
| `23af897bf` | HARDENING | `-connect-authority` removed |
| `593f634a1` | FIX | hijack buffered bytes |
| `fb90272a5` | FIX | an `io.Writer` short write caused **frame desync** |
| `b16660ed1` | HARDENING | a timing signal was removed |
| `3c6e0aad3` → `fe5789539` | FIX → FIX | the clamp was added and then **undone**; the full 0..255 padding range is correct |
| `e9d329380` | COMPAT | the `Padding` header became unconditional on the response |
| `d4ead1498` | HARDENING | `packetDestinationGuard` introduced |
| `7029722bf` | HARDENING | the single TCP-only self-SSH:2222 exception |
| `1dc734f75`, `3bbf17907`, `10e1f2a95`, `d1f3798a6`, `2ec06e451` | HARDENING | guard order and normalisation |
| `58985a375` … `f64638a98` | INTEROP | the Naive interop family (Caddy, official client, H3 vs Caddy) |
| `724035c76` | REFACTOR | dead config removed (`server_limits.idle_timeout`) |
| `bbfc31582` → `968395304` | REFACTOR | the Clash API removed and then restored |
| `4ebc00414`, `7f7c7ae11a38`, `8f1ba42a9` | FEATURE | AnyTLS fallback after TLS termination |
| `b5a25edd8` | ZERO-COPY + HYGIENE | the `errors.As` heap escape, and a stray 21 MB `masque.test` binary (removed `9e7c85955`, ignored `bcad6b4d6`) |

The `EXTERNAL-ADAPTED` label is used only where a source was quoted — here the `klzgrad/forwardproxy`
and Caddy references in the fork's own commits and docs.

---

## 7. AI cautions

1. **Do not describe Native Naive as "a dependency bump".** It is a fork subsystem with 64 commits.
2. **Do not add a Caddy dependency.** The fork uses `x/net/http2` + `h2c` + its own quic-go fork.
3. **Do not test the server codec and conclude the client is correct.** The client outbound is in
   another repository.
4. **Do not frame HTTP/1 CONNECT.** The `Padding` header is consulted only on HTTP/2 and HTTP/3.
5. **Do not clamp the 0..255 padding range to the buffer.** That was tried and reverted.
6. **Do not silently truncate or "repair" a short write.** It is `io.ErrShortWrite` by design, and the
   frame counter must not advance before the whole frame is written.
7. **Do not make `include/quic_stub.go` install a nil constructor.** The failing stub is what lets
   `decideHTTP3Availability` tell "absent" from "broken".
8. **Do not make a missing HTTP/3 transport fatal when the inbound also has TCP.**
9. **Do not add a `server_limits` handshake or idle timeout.** `tls.handshake_timeout` and
   `HTTP2Options.IdleTimeout` already own those phases, and two overlapping fields can disagree.
10. **Do not apply `header_timeout` to an established tunnel.**
11. **Do not key `max_connections_per_ip` on a forwarded header.** It must be the real transport peer.
12. **Do not resolve the target after checking it.** `resolve` must run first; the address checked must
    be the address dialed.
13. **Do not move the per-datagram guard to the write side.** The batch fast path cannot be guaranteed
    to see every packet.
14. **Do not make the per-datagram guard deny when no rule matches**, and do not make it re-route to a
    different outbound.
15. **Do not open `<VPS_SELF_IP>:443` to fix a self-hosted site.** Rewrite the target to a loopback-only
    ingress.
16. **Do not judge a target rejection by the CONNECT status code.** It is `200 OK` either way; write
    into the tunnel and watch the origin.
17. **Do not combine `domain_suffix` with `ip_cidr` to mean "only when it resolves to the self
    address".** They are OR, not AND, within one destination group.
18. **Do not require both UDP and TCP for HTTP/3-with-fallback.** It is `AnyOf` with UDP
    `Conditional`.
19. **Select the target's build profile and verify its dependencies.** OTHERS omits Naive outbound;
    the Windows profile includes `with_purego`. The existence of platform source files alone does
    not prove that a particular native library or release artifact is usable.
20. **Do not cite `test/jiejie/**` results from this worktree.** The `test` module is separate, needs a
    reference binary and network, and push CI deliberately does not run it.
