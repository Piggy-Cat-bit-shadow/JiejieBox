# XHTTP client transport

## VLESS encryption boundary after v0.1.9

**VERIFIED_SOURCE**, testing target `011f3fdcd99`: `option/vless.go` exposes optional VLESS
`encryption`; empty or `none` leaves it off. `protocol/vless/outbound.go` parses and initializes
the client encryption layer and wraps a dialed transport before VLESS uses it. The
`protocol/vless/encryption` package implements the `mlkem768x25519plus` client handshake,
independently of TLS; `client.go` records adaptation from starifly via Leadaxe and states that the
server/decryption half was not taken. `protocol/vless/encryption.go` guards the blocking handshake
against context cancellation. Tests include `protocol/vless/encryption_test.go`,
`encryption_guard_test.go` and package `encryption` tests. Do not infer server support or wire
interoperability from client parsing or unit tests alone; some ABI tests are excluded under `-race`.

> Status: `VERIFIED_SOURCE`
>
> Reviewed against repository `Piggy-Cat-bit-shadow/sing-box`, branch `testing`,
> HEAD `db8814ced6edf7a266fd2a73b03711332473dad9` on 2026-10-13.
>
> This module documents the fork's **client-side XHTTP / SplitHTTP transport configuration contract**.
> It is intended to answer "how do I write an XHTTP node?" without requiring a source-code audit.
> If this document conflicts with current source or tests, **source and tests win**.

---

## 1. Scope

This module covers:

- where XHTTP is configured in a VLESS outbound;
- all currently exposed `V2RayXHTTPOptions` fields;
- the four transport modes and their defaults;
- session / sequence metadata placement;
- packet-up uplink tuning;
- X-Padding behaviour;
- XMUX connection reuse and its non-obvious default rules;
- HTTP/1.1 / HTTP/2 / HTTP/3 selection;
- interaction with TLS, Reality, Host, path and VLESS XUDP;
- accepted-but-ignored and server-only fields;
- recommended configuration patterns and common failure modes;
- fork-specific lifecycle integration for XHTTP connection reuse.

Primary source anchors:

```text
option/v2ray_xhttp.go
option/v2ray_transport.go
option/vless.go
transport/v2rayxhttp/client.go
transport/v2rayxhttp/meta.go
transport/v2rayxhttp/xmux.go
option/v2ray_transport_xhttp_test.go
protocol/vless/outbound.go
```

The comments in `option/v2ray_xhttp.go` still reference old `SPECS/TASKS/...` paths.
Those paths are not required to configure XHTTP and may not exist in this repository.
This module is the knowledge-base replacement for that missing configuration guide.

---

## 2. Configuration model: XHTTP is a V2Ray transport

XHTTP is **not** a standalone outbound type.

For VLESS, it is selected under the outbound's `transport` object:

```json
{
  "type": "vless",
  "tag": "example-xhttp",
  "server": "example.com",
  "server_port": 443,
  "uuid": "YOUR-UUID",
  "tls": {
    "enabled": true,
    "server_name": "example.com"
  },
  "transport": {
    "type": "xhttp"
  }
}
```

The important distinction is:

```text
outbound.type       = vless
transport.type      = xhttp
```

Do **not** write:

```json
{
  "type": "xhttp"
}
```

and do **not** write:

```json
{
  "network": "xhttp"
}
```

`network` is the normal sing-box TCP/UDP network selector. XHTTP belongs to the V2Ray transport layer.

---

## 3. Smallest practical configuration

A practical TLS XHTTP outbound can be written as:

```json
{
  "type": "vless",
  "tag": "XHTTP",
  "server": "example.com",
  "server_port": 443,
  "uuid": "YOUR-UUID",
  "tls": {
    "enabled": true,
    "server_name": "example.com",
    "alpn": [
      "h2"
    ]
  },
  "transport": {
    "type": "xhttp",
    "mode": "auto",
    "path": "/xhttp",
    "x_padding_bytes": "100-1000"
  },
  "packet_encoding": "xudp"
}
```

For subscription/config conversion, this is a good default philosophy:

```text
preserve explicitly configured source fields;
do not invent advanced XHTTP tuning;
let omitted fields use the implementation defaults.
```

---

## 4. Modes

Supported `mode` values:

| Value | Behaviour |
|---|---|
| `auto` | Reality → `stream-one`; otherwise → `packet-up` |
| `packet-up` | one GET download stream + sequential POST upload packets |
| `stream-up` | one streamed POST upload + a separate GET download stream |
| `stream-one` | one bidirectional HTTP stream; request body uploads, response body downloads |

If `mode` is omitted, it defaults to:

```text
auto
```

Unknown mode values are rejected.

### 4.1 `auto`

Current implementation:

```text
Reality enabled     -> stream-one
Reality not enabled -> packet-up
```

The client does not currently implement Xray's asymmetric `downloadSettings` transport selection,
so the decision is intentionally simpler than every possible Xray topology.

### 4.2 `packet-up`

Characteristics:

- download is a long-lived GET response;
- upload is split into sequential requests;
- each upload packet receives a sequence number;
- the packet size and inter-POST delay can be tuned;
- header/cookie uplink payload placement is valid only here;
- `uplink_http_method: "GET"` is meaningful only here.

### 4.3 `stream-up`

Characteristics:

- upload uses one streamed request body;
- download uses a separate GET;
- streamed-body requests normally carry `Content-Type: application/grpc`.

### 4.4 `stream-one`

Characteristics:

- one request carries both directions;
- closest transport shape to an HTTP-upgrade-style bidirectional stream;
- streamed-body request normally carries `Content-Type: application/grpc`;
- Reality + `mode: auto` resolves to this mode.

---

## 5. Full field reference

All JSON field names below are the actual current option-layer keys.

### 5.1 Basic fields

| Field | Type | Default | Meaning |
|---|---|---|---|
| `host` | string | TLS SNI, otherwise server address | HTTP Host override |
| `path` | string | effectively `/` after normalization | XHTTP request base path |
| `mode` | string | `auto` | `auto`, `packet-up`, `stream-up`, `stream-one` |
| `headers` | object | none | extra HTTP request headers |
| `x_padding_bytes` | range string | `100-1000` | inclusive random X-Padding byte-length range |
| `xmux` | object | see XMUX section | HTTP connection reuse policy |
| `no_grpc_header` | bool | `false` | suppress streamed-mode `Content-Type: application/grpc` |

Range strings accept either:

```text
"100-1000"
```

or a single value:

```text
"4000"
```

which means:

```text
4000-4000
```

If a reversed range is supplied, the parser normalizes the endpoints.

---

## 6. Host, SNI and path

### 6.1 `host`

Host resolution order is:

```text
transport.host
    -> tls.server_name
        -> server address
```

Therefore these are related but are **not the same setting**:

```text
tls.server_name = TLS SNI / certificate-facing server name
transport.host  = HTTP Host
```

It is valid to explicitly keep both when converting a node even if their values are identical.

Example:

```json
"tls": {
  "enabled": true,
  "server_name": "cdn.example.com"
},
"transport": {
  "type": "xhttp",
  "host": "cdn.example.com"
}
```

### 6.2 `path`

The client guarantees a leading slash.

For example:

```text
abc
```

is normalized to:

```text
/abc
```

The configured trailing slash is otherwise preserved.

This matters with reverse proxies. For example, an nginx location such as:

```nginx
location /upload/ {
    ...
}
```

may redirect:

```text
/upload -> /upload/
```

The XHTTP download `RoundTrip` does not rely on ordinary browser-style redirect following.
A 301/302 in front of the endpoint can therefore surface as a dial failure.

If the server path is `/upload/`, configure `/upload/` exactly.

---

## 7. HTTP version selection

There is **no** XHTTP JSON field such as:

```json
"http_version": 2
```

The client decides the HTTP transport from TLS/ALPN/Reality.

Current behaviour:

| Situation | HTTP transport |
|---|---|
| no TLS | HTTP/1.1 |
| TLS with `alpn: ["http/1.1"]` | HTTP/1.1 |
| TLS with `alpn: ["h3"]` | HTTP/3 |
| normal TLS with no explicit special ALPN | HTTP/2 |
| Reality requiring H2 path | HTTP/2, with H2 ALPN enforced as needed |

Typical H2 configuration:

```json
"tls": {
  "enabled": true,
  "server_name": "cdn.example.com",
  "alpn": [
    "h2"
  ]
}
```

Typical H3 selection:

```json
"tls": {
  "enabled": true,
  "server_name": "cdn.example.com",
  "alpn": [
    "h3"
  ]
}
```

Do not invent a `transport.http_version` key.

---

## 8. Session and sequence placement

The XHTTP client creates per-dial session metadata.
`packet-up` additionally has a per-upload sequence number.

### 8.1 Session fields

| Field | Accepted values | Default |
|---|---|---|
| `session_placement` | `path`, `query`, `header`, `cookie` | `path` |
| `session_key` | arbitrary name | placement-dependent |
| `session_table` | predefined table name or literal ASCII alphabet | dashed UUID when pair omitted |
| `session_length` | range string | dashed UUID when pair omitted |

Default key when `session_placement` is:

```text
path   -> no key
header -> X-Session
query  -> x_session
cookie -> x_session
```

### 8.2 Sequence fields

| Field | Accepted values | Default |
|---|---|---|
| `seq_placement` | `path`, `query`, `header`, `cookie` | `path` |
| `seq_key` | arbitrary name | placement-dependent |

Default key:

```text
path   -> no key
header -> X-Seq
query  -> x_seq
cookie -> x_seq
```

The sequence number is relevant to `packet-up`.

### 8.3 Custom session IDs

`session_table` and `session_length` must be configured **together**.

Setting only one is rejected.

Supported predefined `session_table` names are case-sensitive:

```text
hex
HEX
number
alphabet
Alphabet
ALPHABET
base36
BASE36
Base62
```

A literal ASCII alphabet may also be supplied.

Example:

```json
"session_table": "Base62",
"session_length": "12-16"
```

The implementation validates the resulting ID space. The minimum-length alphabet space must be large
enough to avoid unsafe cross-client session collision risk.

Unless compatibility or obfuscation requirements demand otherwise, leaving both fields omitted is the
simplest and safest configuration.

---

## 9. Uplink data fields

These options primarily affect `packet-up`.

| Field | Values / format | Default |
|---|---|---|
| `uplink_data_placement` | `body`, `auto`, `header`, `cookie` | `auto` |
| `uplink_data_key` | string | placement-dependent |
| `uplink_chunk_size` | range string | placement-dependent |
| `uplink_http_method` | HTTP method | `POST` |

### 9.1 `uplink_data_placement`

`header` and `cookie` are valid only in:

```text
packet-up
```

Using them with stream modes is rejected.

Default key:

```text
header -> X-Data
auto   -> X-Data
cookie -> x_data
body   -> no key
```

### 9.2 `uplink_chunk_size`

Default:

```text
cookie -> 2048-3072
header -> 3000-4000
other  -> sc_max_each_post_bytes
```

The implementation enforces a minimum normalized chunk floor of 64 base64 characters.

### 9.3 `uplink_http_method`

Default:

```text
POST
```

`GET` is only valid for `packet-up`.

If `GET` is configured on a non-`packet-up` mode, current code warns and falls back to:

```text
POST
```

instead of rejecting the entire configuration.

---

## 10. X-Padding

### 10.1 Legacy/default mode

With:

```json
"x_padding_obfs_mode": false
```

or with the field omitted, the client keeps the legacy Xray-compatible X-Padding behaviour.

Default padding size:

```json
"x_padding_bytes": "100-1000"
```

### 10.2 Configurable obfuscation mode

Fields:

| Field | Accepted values | Default |
|---|---|---|
| `x_padding_obfs_mode` | bool | `false` |
| `x_padding_key` | string | `x_padding` |
| `x_padding_header` | string | `X-Padding` |
| `x_padding_placement` | `cookie`, `header`, `query`, `queryInHeader` | `queryInHeader` |
| `x_padding_method` | `repeat-x`, `tokenish` | `repeat-x` |

Example:

```json
{
  "x_padding_obfs_mode": true,
  "x_padding_placement": "header",
  "x_padding_header": "X-Padding",
  "x_padding_method": "tokenish",
  "x_padding_bytes": "100-1000"
}
```

Unless a peer/server profile explicitly requires these values, subscription conversion should generally
preserve the source settings rather than inventing them.

---

## 11. `no_grpc_header`

For streamed-body modes:

```text
stream-one
stream-up
```

the client normally sets:

```http
Content-Type: application/grpc
```

This header can be operationally important with CDNs/reverse proxies that use the content type to choose
streaming/no-buffering behaviour.

Therefore:

```json
"no_grpc_header": true
```

is **not** a generic optimization.

Do not enable it by default.

In some reverse-proxy/CDN paths, suppressing the header can turn a working streamed XHTTP session into a
connection that buffers or stalls until timeout.

---

## 12. Packet-up tuning

| Field | Format | Default |
|---|---|---|
| `sc_max_each_post_bytes` | range string | `1000000-1000000` |
| `sc_min_posts_interval_ms` | range string | `30-30` |

### 12.1 `sc_max_each_post_bytes`

Bounds the payload size of one packet-up upload POST before splitting.

Example:

```json
"sc_max_each_post_bytes": "1000000-1000000"
```

### 12.2 `sc_min_posts_interval_ms`

Minimum delay between successive packet-up upload POSTs.

Example:

```json
"sc_min_posts_interval_ms": "30-30"
```

Do not tune these merely because they are exposed. They alter packet-up request behaviour and can affect
latency, burstiness and peer compatibility.

### 12.3 Post cadence, and why response completion is not pipelined

A packet-up uplink keeps **one POST in flight**: the next POST starts after the previous POST's
response has been fully drained, so with a queue that never empties the cadence is

```text
one POST per max(round trip, sc_min_posts_interval_ms)
```

At `011f3fdcd99`, `packetUplink.sendPacket` reports a read or close error from
`drainPacketUpResponse`; an HTTP 200 whose body stalls or fails is not counted as successful data
flow and must not clear the connection's failure streak. The POST has its own bounded context.
See `transport/v2rayxhttp/conn.go`; this source contract does not establish a live-peer result.

Post size is unaffected by latency: gathering still fills every POST to `sc_max_each_post_bytes` at
every round trip measured (1,000,000 bytes from 0 to 200 ms).

The reference implements the obvious objection to that design — it starts the next POST on
`httptrace.WroteRequest` and drains the response in a goroutine, i.e. it pipelines response
completion (`Xray v26.9.30`, `transport/internet/splithttp/dialer.go:495-534`) — so the two were
compared directly (packet-up, HTTP/2, one stream, same reference server, three repetitions per
point, 30 runs, 0 payload mismatches):

| Round trip | 0 ms | 30 ms | 50 ms | 100 ms | 200 ms |
|---|---|---|---|---|---|
| fork upload | 26.93 MB/s | 24.25 MB/s | 15.57 MB/s | 7.88 MB/s | 3.92 MB/s |
| reference upload | 22.84 MB/s | 19.67 MB/s | 14.53 MB/s | 7.80 MB/s | 4.04 MB/s |
| ratio | 1.18x | 1.23x | 1.07x | 1.01x | 0.97x |
| fork ping-pong p50, 512 B | 30.0 ms | 32.2 ms | 52.0 ms | 102.0 ms | 202.0 ms |
| reference ping-pong p50 | 30.5 ms | 31.8 ms | 51.8 ms | 101.7 ms | 201.8 ms |

Both implementations fall together (about 27 → 4 MB/s) and the fork is never behind, including in the
small-post regime where a response wait would hurt most. **The synchronous response wait is therefore
not a fork-specific high-RTT cliff**: the collapse is "one POST per round trip", which the reference
shows too even though it pipelines. Pipelining response completion was considered and rejected — it
needs a bounded in-flight completion set and would change the failure semantics the seq-no-hole
argument rests on, for no measured gain.

Evidence: `VERIFIED_UNIT` for the invariants (at most one POST in flight, contiguous ascending seq,
cadence no faster than half the round trip — `TestPacketUpPostCadenceTracksTheRoundTrip` in
`transport/v2rayxhttp/packetup_pipeline_probe_test.go`); **historical record** for the round-trip
sweep, which is a manual harness measurement on one host (loopback plus a delay-line impairment shim,
so added latency costs no bandwidth) and cannot be re-run from CI. Beyond 200 ms there is no data.

Provenance: probe commit `58e3222558c` on branch `perf/xhttp-rtt-pipeline-probe`, on top of the
finalization branch `b5bc2a8de42`. Both are feature branches; **neither is merged to `testing`**, so
this subsection describes the fork's XHTTP work rather than the reviewed baseline above.

> XHTTP remains closed; no further optimization justified by current evidence.

---

## 13. XMUX

XMUX is **HTTP connection reuse**, not a separate wire protocol.

The server identifies XHTTP sessions by session metadata, not by the specific pooled HTTP connection.
Pooling therefore changes client-side connection reuse/rotation, not the logical XHTTP session format.

It avoids paying a completely fresh TCP+TLS(+Reality) setup for every stream.

### 13.1 Fields

```json
"xmux": {
  "max_concurrency": "16-32",
  "max_connections": "1-2",
  "c_max_reuse_times": "100",
  "h_max_request_times": "600-900",
  "h_max_reusable_secs": "1800-3000",
  "h_keep_alive_period": 0
}
```

| Field | Meaning |
|---|---|
| `max_concurrency` | maximum live streams sharing one HTTP connection |
| `max_connections` | pool connection-count bound |
| `c_max_reuse_times` | number of stream handouts before a connection is retired |
| `h_max_request_times` | number of HTTP requests before retirement |
| `h_max_reusable_secs` | reusable lifetime in seconds |
| `h_keep_alive_period` | HTTP/2 keep-alive/read-idle ping period |

`h_max_request_times` counts **HTTP requests**, not logical XHTTP streams.

This distinction is important in `packet-up`, because one logical stream can produce many upload POSTs.

### 13.2 Range syntax

XMUX range fields accept the fork's normal XHTTP range form:

```json
"max_concurrency": "16-32"
```

A single integer string is also valid:

```json
"max_concurrency": "16"
```

and represents:

```text
16-16
```

The option layer also accepts the compatible two-element array form where supported by the range type.

### 13.3 `max_concurrency` vs `max_connections`

They represent alternative pool bounding strategies and are documented as mutually exclusive.

For ordinary authored configuration, choose one strategy instead of setting both.

A decoder/round-trip test may intentionally contain both merely to prove that fields survive parsing;
that should not be copied as a recommendation.

### 13.4 The important all-or-nothing default rule

This rule is easy to miss.

If the entire `xmux` section is:

```text
absent
```

or is effectively completely empty, Xray-compatible defaults are selected:

```text
max_connections     = 3-3
h_max_request_times = 600-900
h_max_reusable_secs = 1800-3000
```

But if **any XMUX field is explicitly set**, then the section is treated as explicitly authored.

Other unspecified range fields remain zero, which means their normal explicit-section semantics
(e.g. unlimited/no corresponding retirement bound), rather than silently inheriting the complete
default set.

Therefore:

```json
"xmux": {
  "max_concurrency": "16-32"
}
```

is behaviourally different from omitting `xmux`.

This is intentional.

### 13.5 Keep-alive value

`h_keep_alive_period` is a plain integer rather than a range.

Its option contract states:

```text
0        -> implementation/default behaviour
negative -> disable keep-alive pings
positive -> configured HTTP/2 keep-alive period in seconds
```

---

## 14. Fork-specific XMUX lifecycle behaviour

This fork integrates XHTTP connection reuse with the runtime lifecycle.

The XHTTP client implements:

```text
adapter.IdleConnectionKeeper
adapter.ReuseSuspect
```

Important consequences:

### Memory trim

`CloseIdleConnections()` retires only pooled connections carrying no live stream.

A memory trim is deliberately not allowed to become an unnecessary reconnect trigger for active work.

### Reuse boundary / suspect connections

`RetireSuspect()` stops old/suspect pooled connections from accepting new streams.

A connection already carrying live traffic is drained rather than immediately destroyed.

### Full close / network change

`Close()` retires the entire XHTTP pool.

Existing live-use teardown is deferred according to pool ownership rules rather than blindly killing a
shared transport at an unsafe moment.

### No timer-driven prewarm

`SetKeepIdleConnections(true)` does not proactively dial/prewarm a connection.

This is deliberate: lifecycle policy should not generate network dials just to keep a pool warm.

These behaviours are fork-specific operational context and should be preserved when changing
`transport/v2rayxhttp`.

---

## 15. Accepted but ignored / client-irrelevant fields

The option layer accepts several fields for compatibility even though the client does not use them.

| Field | Current client behaviour |
|---|---|
| `sc_max_concurrent_posts` | legacy compatibility field; accepted but ignored |
| `server_max_header_bytes` | server-only; ignored by client |
| `no_sse_header` | server-only; ignored by client |
| `sc_max_buffered_posts` | server-only; ignored by client |
| `sc_stream_up_server_secs` | server-side behaviour; client does not configure it |

Their presence should not be interpreted as evidence that the client actively implements the
corresponding server-side feature.

---

## 16. VLESS UDP / XUDP interaction

XHTTP is the VLESS transport. VLESS packet handling is configured separately.

For this fork, an explicit subscription-style setting:

```json
"packet_encoding": "xudp"
```

maps cleanly to the VLESS XUDP path.

When translating a source subscription, preserving explicit `xudp` is useful because it records the
source node's intended packet behaviour even where the fork may have a compatible default.

There is no Mihomo-style:

```json
"udp": true
```

boolean required in the sing-box outbound.

Likewise, do not convert:

```yaml
network: xhttp
```

into sing-box `network`.

If normal TCP+UDP availability is desired, leaving the sing-box outbound network selector unset is
normally appropriate.

---

## 17. Mihomo / Clash-style XHTTP mapping

A common source node:

```yaml
- name: 🇺🇸 美国｜Vless XHTTP TLS
  type: vless
  server: example.com
  port: 443
  uuid: YOUR-UUID
  network: xhttp
  tls: true
  udp: true
  servername: cdn.example.com
  client-fingerprint: chrome
  alpn:
    - h2
  encryption: none
  xhttp-opts:
    path: /example
    host: cdn.example.com
    mode: stream-one
    reuse-settings:
      max-concurrency: 16-32
  packet-encoding: xudp
```

maps conceptually to:

```json
{
  "type": "vless",
  "tag": "🇺🇸 美国｜Vless XHTTP TLS",
  "server": "example.com",
  "server_port": 443,
  "uuid": "YOUR-UUID",
  "encryption": "none",
  "tls": {
    "enabled": true,
    "server_name": "cdn.example.com",
    "alpn": [
      "h2"
    ],
    "utls": {
      "enabled": true,
      "fingerprint": "chrome"
    }
  },
  "transport": {
    "type": "xhttp",
    "path": "/example",
    "host": "cdn.example.com",
    "mode": "stream-one",
    "xmux": {
      "max_concurrency": "16-32"
    }
  },
  "packet_encoding": "xudp"
}
```

Mapping:

| Source concept | sing-box fork |
|---|---|
| `name` | `tag` |
| `type: vless` | `type: vless` |
| `server` | `server` |
| `port` | `server_port` |
| `uuid` | `uuid` |
| `network: xhttp` | `transport.type: xhttp` |
| `tls: true` | `tls.enabled: true` |
| `servername` | `tls.server_name` |
| `client-fingerprint` | `tls.utls.fingerprint` + `tls.utls.enabled` |
| `alpn` | `tls.alpn` |
| `encryption` | `encryption` |
| `xhttp-opts.path` | `transport.path` |
| `xhttp-opts.host` | `transport.host` |
| `xhttp-opts.mode` | `transport.mode` |
| `reuse-settings.max-concurrency` | `transport.xmux.max_concurrency` |
| `packet-encoding: xudp` | `packet_encoding: xudp` |
| `udp: true` | no equivalent boolean required |
| `ip-version: dual` | do not invent `ip_version`; resolver/network family policy is separate |

---

## 18. Recommended profiles

These are **configuration templates**, not requirements.

### 18.1 Normal TLS, explicit H2, stream-one

```json
{
  "type": "vless",
  "tag": "VLESS XHTTP TLS",
  "server": "example.com",
  "server_port": 443,
  "uuid": "YOUR-UUID",
  "encryption": "none",
  "tls": {
    "enabled": true,
    "server_name": "cdn.example.com",
    "alpn": [
      "h2"
    ],
    "utls": {
      "enabled": true,
      "fingerprint": "chrome"
    }
  },
  "transport": {
    "type": "xhttp",
    "host": "cdn.example.com",
    "path": "/xhttp",
    "mode": "stream-one",
    "x_padding_bytes": "100-1000"
  },
  "packet_encoding": "xudp"
}
```

### 18.2 Reality with `auto`

```json
{
  "type": "vless",
  "tag": "VLESS Reality XHTTP",
  "server": "SERVER_IP_OR_DOMAIN",
  "server_port": 443,
  "uuid": "YOUR-UUID",
  "tls": {
    "enabled": true,
    "server_name": "REALITY_SNI",
    "utls": {
      "enabled": true,
      "fingerprint": "chrome"
    },
    "reality": {
      "enabled": true,
      "public_key": "YOUR_PUBLIC_KEY",
      "short_id": "YOUR_SHORT_ID"
    }
  },
  "transport": {
    "type": "xhttp",
    "path": "/xhttp",
    "mode": "auto",
    "x_padding_bytes": "100-1000"
  },
  "packet_encoding": "xudp"
}
```

With current mode selection:

```text
Reality + auto -> stream-one
```

### 18.3 Preserve an explicit source XMUX concurrency setting

```json
"transport": {
  "type": "xhttp",
  "path": "/xhttp",
  "mode": "stream-one",
  "xmux": {
    "max_concurrency": "16-32"
  }
}
```

Do not silently add other XMUX limits unless the source profile or an intentional tuning decision calls
for them.

---

## 19. Common mistakes

### Mistake 1 — treating XHTTP as an outbound

Wrong:

```json
"type": "xhttp"
```

Correct:

```json
"type": "vless",
"transport": {
  "type": "xhttp"
}
```

### Mistake 2 — writing `network: "xhttp"`

Wrong:

```json
"network": "xhttp"
```

Correct:

```json
"transport": {
  "type": "xhttp"
}
```

### Mistake 3 — inventing `http_version`

Wrong:

```json
"transport": {
  "type": "xhttp",
  "http_version": 2
}
```

Correct: select the intended HTTP path through TLS/ALPN.

### Mistake 4 — assuming Host and SNI are aliases

They serve different layers.

```text
transport.host  -> HTTP
tls.server_name -> TLS
```

### Mistake 5 — stripping a required trailing slash

If the reverse proxy is configured for `/upload/`, preserve `/upload/`.

### Mistake 6 — setting `no_grpc_header: true` as an optimization

This may break streaming behaviour in front of a buffering reverse proxy/CDN.

### Mistake 7 — setting one XMUX field and assuming all omitted XMUX defaults still apply

They do not. Explicit XMUX follows the all-or-nothing rule described above.

### Mistake 8 — tuning every exposed field

Most XHTTP fields exist for compatibility and advanced transport shaping.
A normal node does not need to specify every field.

---

## 20. Configuration decoder coverage

XHTTP must remain reachable from a real JSON configuration file, not only from direct Go construction.

`option/v2ray_transport_xhttp_test.go` exists as a regression guard for this contract.

The V2Ray transport option dispatcher must preserve:

```text
type = xhttp
```

through JSON decode/encode and map it to `V2RayXHTTPOptions`.

When changing option dispatch, schema generation, aliases or build tags, keep this regression category:
an implementation that compiles but cannot be selected from a config file is not functional XHTTP
support.

---

## 21. Maintenance rules

When XHTTP code changes, re-check at least:

```text
option/v2ray_xhttp.go
option/v2ray_transport.go
transport/v2rayxhttp/client.go
transport/v2rayxhttp/meta.go
transport/v2rayxhttp/xmux.go
protocol/vless/outbound.go
option/v2ray_transport_xhttp_test.go
```

Update this module when any of the following changes:

- a JSON field is added, removed or renamed;
- a default changes;
- an accepted value changes;
- mode resolution changes;
- HTTP version selection changes;
- metadata placement semantics change;
- XMUX defaulting/rotation semantics change;
- a compatibility field stops being ignored;
- VLESS/XUDP integration changes;
- lifecycle reuse/retirement semantics change.

Do not copy stale Xray or sing-box-lx documentation over current source behaviour.

Source-of-truth order:

```text
current source and tests
    > git history/diffs
        > this module
            > external/reference documentation
```

---

## 22. Quick reference

For a normal converted XHTTP node, usually think in this order:

```text
1. VLESS server / UUID
2. TLS / Reality
3. transport.type = xhttp
4. path
5. host if HTTP Host must be explicit
6. mode
7. ALPN if an HTTP version must be forced
8. packet_encoding = xudp if the source explicitly uses XUDP
9. XMUX only when the source or an intentional tuning profile specifies it
10. leave advanced placement/padding/tuning fields alone unless required
```

A compact normal form:

```json
{
  "type": "vless",
  "server": "example.com",
  "server_port": 443,
  "uuid": "YOUR-UUID",
  "tls": {
    "enabled": true,
    "server_name": "example.com",
    "alpn": [
      "h2"
    ]
  },
  "transport": {
    "type": "xhttp",
    "path": "/xhttp",
    "mode": "stream-one"
  },
  "packet_encoding": "xudp"
}
```

That is the starting point. Advanced XHTTP options should be added because a peer, proxy path or measured
behaviour requires them — not merely because the fields exist.
