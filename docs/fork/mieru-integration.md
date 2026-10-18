# Mieru integration

Status: integration complete and interop-tested on Windows; this document is the
stable fact sheet for the next round (architecture/performance and deep debug).

Baseline: `testing` at `42a7b58c26f` when the work started. The commits that follow
on `testing` are the integration itself.

## Pinned versions

| What | Version / commit | Why |
| --- | --- | --- |
| Protocol SDK | `github.com/enfein/mieru/v3 v3.38.1`, commit `a2208ee7a38fb41d843a13ecdde858e67ef2f485` | The latest public release at integration time. `v3.38.2` exists as a module tag but differs from `v3.38.1` only in `.github/workflows/release.yaml` (verified with `git diff --no-index` on the module zips); pinning the released version keeps the SDK and its test evidence aligned. |
| License | GPL-3.0 | The SDK is linked as a Go module, not copied. |
| Go | 1.26.8 toolchain, `go 1.25.5` directive | Same toolchain the fork already builds with. |
| Transitive delta | `github.com/enfein/mieru/v3` only; `github.com/google/btree` was already an indirect dependency | `go mod tidy` produced exactly one new `require` line and two `go.sum` lines. No unrelated dependency moved. |

## Reference matrix

All reference commits were read at the SHA below; no code was copied.

| Source | Commit | What was learned | Decision |
| --- | --- | --- | --- |
| `enfein/mieru` | `v3.38.1` / `a2208ee7` | The protocol semantics, `apis/client` and `apis/server`, `Mux`, listener factories, UDP-over-stream framing, traffic pattern, user hint. | KEEP as the only protocol implementation. |
| `enfein/mbox` | `1c5b39718ebcd05e6adb31f95246fbf056be9bd7` | The first sing-box skeleton: option field names, client/server config building, SOCKS5 request/response handling. | KEEP the schema names (`server`, `server_port`, `server_ports`, `transport`, `username`, `password`, `multiplexing`, `traffic_pattern`, inbound `users` / `user_hint_is_mandatory`). REJECT its lifecycle: client started in the constructor, `common.Close(o.client)` as cleanup (the SDK client implements neither `io.Closer` nor `Upstream`, so this is a no-op), no listener factory, no connection draining. |
| `hiddify/hiddify-sing-box` | `v5` / `3bbbd61ee4d76cc6ff433b111fed4187ad2f4b4f` | `Accept()` error backoff (100 ms 鈫?5 s), handshake/multiplexing enum maps, port binding schema. | PARTIAL. Enum mapping ideas were kept (case-insensitive, official enum names accepted). The unconditional backoff was rejected: `server.Accept()` reports per-connection handshake failures as well as a dead Mux, so a blanket delay lets one bad client slow every legitimate connection behind it. The Mux-level failure (`io.ErrClosedPipe`) is terminal and stops the loop; per-connection errors continue immediately. |
| `shtorm-7/sing-box-extended` | `extended` / `5956780ba3f140feff771d003914a1b82ea9d5d0` | Starting the client in `Start` rather than the constructor, MTU/handshake fields. | KEEP the Start-stage lifecycle. REJECT its startup DNS pre-resolution (`addresses[0]`): it discards the fork's dual-stack strategy, fallback and rotation. |
| `MetaCubeX/mihomo` | `Alpha` / `94b13aed49b8dc41973e4bb3f721c5fa2c5c5c1f` | Explicit `Stop`, lazy start, `StreamListenerFactory`/`PacketListenerFactory`, a resolver and packet dialer. | KEEP explicit `Stop()` and the listener factories. REJECT lazy start on first use: the fork has one lifecycle authority, and a node that starts inside `DialContext` is a second one. REJECT its resolver's single-IP answer for the UDP underlay for the same reason as shtorm. |

## Data path and ownership

```
Config 鈫?registry 鈫?option.Mieru*Options 鈫?protocol/mieru
  Outbound: NewOutbound 鈫?dialer.New (fork dialer; resolves the server through
            the fork's DNS policy) 鈫?buildClientConfig 鈫?client.Store
            Start(stage=Start) 鈫?client.Start 鈫?Mux
            DialContext 鈫?client.DialContext 鈫?Mux 鈫?serverDialer 鈫?fork N.Dialer
  Inbound:  NewInbound 鈫?buildServerConfig (+ listener factories) 鈫?server.Store
            Start(stage=Start) 鈫?server.Start
              鈫?streamListenerFactory  鈫?listener.ListenTCPWithOptions
              鈫?packetListenerFactory  鈫?listener.ListenUDPWithOptions
            acceptLoop 鈫?server.Accept 鈫?metadata 鈫?RouteConnectionEx /
                         RoutePacketConnectionEx
```

Ownership rules:

- **Sockets** belong to the fork. `protocol/mieru` never calls `net.Listen` or
  `net.Dial` itself; the SDK's factories and dialers are wired to
  `common/listener` and `common/dialer`. `bind_interface`, `routing_mark`,
  `reuse_addr`, `netns`, TCP keepalive/fast-open/multipath and UDP fragment are
  therefore applied by the same code that applies them to every other inbound.
- **DNS** has one owner. The stream underlay uses `BypassDialerDNS` and hands
  the configured server name to the fork's dialer. The UDP underlay cannot
  bypass the SDK's pre-resolution through the public API, so a bridge asks the
  fork's DNS router (with `dialer.TargetQueryOptions`) for the SDK's endpoint;
  the actual socket still dials the configured name through the fork's dialer,
  so the single-address answer never becomes the dial decision. The SDK's
  source validation is satisfied by reporting the address it resolved.
- **Connections** are tracked by the route layer after `RouteConnectionEx`
  returns. The inbound therefore does **not** close an accepted connection on
  that return; it tracks a wrapper so `Close()` can terminate sessions that are
  still open. This was a real defect found during this integration: an earlier
  version closed the connection as soon as `RouteConnectionEx` returned, and
  the echo-router test did not catch it because that router blocks. The
  `handoffRouter` regression test now pins the route contract.
- **Lifecycle**: outbound creates the SDK client in the constructor (no
  goroutine), starts it at `StartStateStart`, and registers `Close` on the
  scope. `Close` is an explicit `client.Stop()`, preceded by a bounded drain of
  in-flight dials. The SDK cannot be restarted after `Stop`, so
  `InterfaceUpdated` builds a new client from the same config, swaps it, then
  stops the old one; the old client's Mux closes its underlays and sessions.
  Before the first dial there is no underlay to reset, so the startup network
  observation is a no-op rather than a client restart; after a dial the rebuild
  still runs. The `dialed` flag is the only added state, and it is cleared on
  every successful swap.
- **Metrics** have no Mieru-specific plane. Connection and byte counters belong
  to the shared route/connection manager, and the SDK's internal metrics are not
  re-exported. This is deliberate: a second per-protocol metrics surface would
  duplicate ownership and drift from the shared one.

| Layer | Owner | Mieru adapter role |
| --- | --- | --- |
| Config / schema | `option`, `include/registry`, `protocol/mieru/config.go` | decodes Mieru fields, validates, builds SDK client/server configs; no second config source |
| Routing / connections | `route` (`RouteConnectionEx`, `RoutePacketConnectionEx`, connection manager) | hands accepted conn/packet-conn to the router; never owns the flow after handoff |
| DNS | `dns` + `common/dialer` (`TargetQueryOptions`, `BypassDialerDNS`) | passes the configured server name to the fork dialer; the UDP bridge answers only the SDK's source-validation query |
| Dialer / sockets | `common/dialer`, `common/listener` | supplies SDK `Dialer`/`PacketDialer`/listener factories; no direct `net.Dial`/`net.Listen` |
| SDK / Mux / underlay | `github.com/enfein/mieru/v3` | configures and starts/stops; does not fork or patch the SDK |
| Packet / UoT | `protocol/mieru/uot.go`, `packetconn.go` | owns the per-datagram frame and packet-conn wrappers; no separate packet scheduler |
| Lifecycle | `adapter.StartStage`/`Scope`, `Outbound`/`Inbound` | explicit Start at StartStateStart, explicit Stop, bounded drain, pre-dial no-op update |
| Metrics | shared route/connection manager | none; SDK metrics are not re-exported |

### UDP association

The per-datagram encoding is the SDK's documented format:

```
0x00 | uint16 length | 0x00 0x00 0x00 | SOCKS5 address | payload | 0xff
```

`protocol/mieru/uot.go` implements it directly. It writes one frame into one
pooled buffer (one copy of the payload) and reads into the caller's buffer with
a per-association scratch framer (zero allocations on the read path). A frame
that does not fit the caller's buffer is drained completely before
`io.ErrShortBuffer`, so one oversized datagram cannot desynchronise the
association. Conformance is pinned against the SDK's
`PacketOverStreamTunnel` + `UDPAssociateWrapper` in both directions.

## Interop evidence (2026-10-18, Windows 11, loopback)

The phase-1 reverse direction used the official `apis/server` entry point that `mita` uses,
compiled from the same `v3.38.1` module, because the phase-1 assumption was that `mita` had
no Windows build. Phase 3 disproved that: the official `cmd/mita` source builds on Windows
and runs in foreground; the real-mita matrix and both interop directions are in
`phase3-debug/mita/`. The forward direction used the official `mieru.exe` 3.38.1 Windows
client.

| Direction | Peer | Transport | Handshake / mux | Result |
| --- | --- | --- | --- | --- |
| Official client 鈫?this inbound | `mieru.exe` 3.38.1 | TCP + UDP | `HANDSHAKE_DEFAULT`, `MULTIPLEXING_HIGH` | TCP PASS, UDP associate PASS |
| Official client 鈫?this inbound | `mieru.exe` 3.38.1 | UDP only | `HANDSHAKE_NO_WAIT`, `MULTIPLEXING_OFF` | TCP PASS, UDP associate PASS |
| This outbound 鈫?official server | `apis/server` 3.38.1 | TCP + UDP | `default`, `high` | TCP PASS, UDP associate PASS |
| This outbound 鈫?official server | `apis/server` 3.38.1 | UDP only | `no_wait`, `off` | TCP PASS, UDP associate PASS |
| Official client 鈫?this inbound | `mieru.exe` 3.38.1 | UDP only | `no_wait`, `off`, explicit traffic pattern (seed 20261018) | TCP PASS, UDP associate PASS |
| This outbound 鈫?official server | `apis/server` 3.38.1 | UDP only | `no_wait`, `off`, explicit traffic pattern (same seed) | TCP PASS, UDP associate PASS |

The probe (`tester.exe`, stdlib only) starts a TCP echo server and a UDP echo
server and drives both through a real SOCKS5 client. The full path in each case
is: SOCKS5 client 鈫?sing-box mixed inbound 鈫?Mieru outbound 鈫?Mieru inbound 鈫?sing-box direct outbound 鈫?echo server. Ports and credentials in the run were
ephemeral loopback only.

A wrong-password negative control was also run: the peer saw EOF for TCP and
UDP, and the valid client's next TCP and UDP runs passed against the same
inbound. The SDK drops an undecryptable underlay inside the Mux, so
`server.Accept()` never surfaces that authentication failure; the accept loop
therefore saw no error to classify in this run.

The official client/server logs and the sing-box debug logs from the run are in
`docs/fork/mieru-interop-2026-10-18/`.

Commands that reproduce the in-repo half:

```sh
go test ./protocol/mieru -count=1
CGO_ENABLED=1 go test -race ./protocol/mieru -count=1
go test ./protocol/mieru -run '^$' -bench UOTFrame -benchmem
go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./...
```

## UoT framing baseline

Measured with `go test ./protocol/mieru -run '^$' -bench UOTFrame -benchmem`
(AMD Ryzen 7 4800H, Windows, Go 1.26.8), 1200-byte payload:

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkWriteUOTFrame` (this integration) | 118.8 | 64 | 1 |
| `BenchmarkWriteUOTFrameSDK` (SDK wrapper) | 666.1 | 1280 | 1 |
| `BenchmarkReadUOTFrame` (this integration) | 79.83 | 0 | 0 |
| `BenchmarkReadUOTFrameSDK` (SDK wrapper) | 86.93 | 4 | 2 |

This is a framing microbenchmark, not a network throughput claim. It measures
the code that sits once per datagram on a UDP association; the numbers are the
reason the read path keeps a per-association framer.

## Performance evidence (2026-10-18)

The phase-2 matrix is in
[`verify-evidence/mieru/2026-10-18-84a38ca85f7`](../../verify-evidence/mieru/2026-10-18-84a38ca85f7/README.md).
Both clients used the same official `apis/server` harness and local SOCKS5
entry; 4 ABBA rounds, 4 TCP connections 脳 64 MiB, 5000 UDP datagrams, 1000
latency samples.

| Cell | Target vs official |
| --- | ---: |
| TCP download, tcp transport, low mux | +10.2% |
| TCP download, tcp transport, high mux + no_wait | +7.0% |
| TCP download, udp transport | +3.2% |
| TCP upload, tcp transport, low mux | **-6.1%** |
| TCP upload, udp transport | +16.0% |
| UDP echo, udp transport | +47.4% (P50 0.61x) |
| TCP P95 latency | 0.98-1.01x |

- The UDP-echo win is the UoT framing above, not the cipher or the server; the
  server and target are identical for both variants.
- The 6% TCP-upload gap with the TCP underlay is consistent across rounds and is
  the one repeatable regression. It is recorded, not hidden; CPU/GiB on that
  cell is 32% lower, so it is a throughput trade-off.
- Startup to SOCKS-ready is unchanged (253-255 ms vs 248-258 ms). RSS is not
  comparable as adapter memory: the target process also contains the sing-box
  core and a mixed inbound. The only adapter-memory claim is the UoT microbench.
- A 6 s CPU profile of an 8 GiB download puts 36% in the SDK underlay/socket
  loop, 23% in the shared route copy, 24% in GC and 12% in ChaCha20; the Mieru
  adapter is not a flat hotspot. No speculative pool or goroutine work was
  added.
- `InterfaceUpdated` now returns before the first dial: the startup network
  observation used to stop and restart a client with no underlay. A/B over 12
  cold starts shows no startup or first-byte change, so this is kept as a
  lifecycle simplification, not sold as a speedup.
- Mihomo v1.19.32 (Mieru SDK v3.38.0) ran as a product comparison: target/Mihomo
  throughput is 1.07x on low-mux TCP download, 0.95x on low-mux TCP upload,
  0.85x on high-mux TCP upload, 1.27x on UDP-transport upload, and 0.93-1.05x
  on UDP echo. The target is not uniformly fastest; Mihomo's high-mux TCP upload
  is the strongest reference result. This is not a unified-SDK comparison.
- Real `mita` 3.38.1 was built from the official source and run on Windows (foreground
  `mita run`; `MITA_INSECURE_UDS=1` only for its Unix-socket chown). Official client and
  target both reach it TCP+UDP. Against real mita the target is 1.15x / 1.14x faster on TCP
  download and 1.05-1.46x on UDP echo, but first-byte P50 is ~2x slower (1.0 ms vs 0.52 ms;
  P95 equal) and UDP-transport TCP upload is 0.739x. These negatives are recorded in
  `phase3-debug/mita/README.md`; this matrix replaces the phase-2 "mita unavailable"
  limitation.
- mbox, extended and Hiddify are `BLOCKED_BY_ENVIRONMENT` here: this Windows host has no WSL
  distribution, no Docker, and no scriptable Windows build of those references. E6 in the
  evidence records each blocker; no numbers were invented for them.
- Fallback disabled old/new (two 4-round pairs, same binary shape): TCP download 1.00-1.07x,
  TCP upload 1.00-1.02x; UDP echo 0.92-0.95x. No per-datagram code changed on the disabled
  path, so the UDP-echo difference is treated as run variance, not a claimed result.
- Fallback enabled with a healthy primary vs disabled, against real mita: TCP download
  1.024x, TCP upload 1.021x, UDP echo 1.053x, first-byte P50 identical (1.034 ms). Idle
  (no dials): +0.25 MiB RSS, no extra threads or sockets; selector `choose`+`report` is
  26.6 ns/op with 0 allocs.
- Local failover timing: TCP connection-refused→UDP completed in 1.5 ms; the first recovery
  probe completed in 0.5 ms after the fake clock crossed the cooldown. A UDP blackhole and
  a TCP accept-but-silent primary each take the SDK's fixed 10 s handshake wait before
  fallback; that is bounded by the official SDK, not an extra selector timeout.

## Boundary and common faults (phase 3)

The phase-3 debug round fixed three P1 and three P2 defects; the full evidence is in
[`verify-evidence/mieru/2026-10-18-84a38ca85f7/phase3-debug`](../../verify-evidence/mieru/2026-10-18-84a38ca85f7/phase3-debug/README.md).

- **The SDK does not own the packet socket.** `PacketUnderlay.Close` only wakes its read
  loop; it does not close the `PacketConn`. The inbound now wraps every listener factory
  result in a registry and closes it on `Close`, partial start and failed-start retry, so the
  UDP port is released and reload can rebind. The outbound does the same for its UDP
  underlay sockets; relying on `Stop` alone could leave the read loop blocked after it
  overwrote the wake-up deadline.
- **One framer, one reader.** `ReadPacket`/`ReadFrom` may be called concurrently by the
  `net.PacketConn` contract. The framer's scratch arrays are now protected by a per-
  association read mutex; the pre-fix race and the post-fix race test are in
  `phase3-debug/RACE-REPRO.md`.
- **Failure replies, not false success.** An unsupported command now gets SOCKS5 reply 7 and
  an unparseable destination reply 1 before the connection is closed; CONNECT/UDP ASSOCIATE
  still get the synthetic success mita sends.
- **Start on a closing scope is an error.** `scope.Add` can run the cleanup synchronously;
  both inbound and outbound now report that closure instead of returning nil for a closed
  object.
- **Authenticated user survives the wrapper.** `trackedConn` forwards `UserName()`; the
  router metadata and any downstream capability check see the same user.
- **Oversized UoT frames validate the suffix** before `io.ErrShortBuffer`; a malformed
  frame is not reported as a valid short one.

Performance after the fixes was re-measured with 4-round old/new A/B: TCP within 卤2%, UDP
echo 卤5% in opposite directions, latency and startup unchanged. The only measurable cost is
the UoT read mutex (+9 ns/datagram microbenchmark), accepted for a real data-race fix.

## Optional TCP/UDP fallback

The Mieru outbound can optionally keep one TCP and one UDP SDK generation for
the same `tag`. It is disabled by default; the legacy single-client path is
still selected when `fallback` is absent or `enabled: false`.

```json
{
  "type": "mieru",
  "tag": "mieru-out",
  "server": "example.com",
  "server_port": 8964,
  "transport": "tcp",
  "username": "alice",
  "password": "password",
  "fallback": {"enabled": true, "transport": "udp"}
}
```

`fallback.server` and `fallback.server_port` inherit the primary endpoint.
`fallback.transport` must be the opposite of the primary. `both` and
`handshake_mode: "no_wait"` are rejected when fallback is enabled; the server
only needs corresponding TCP/UDP listeners and does not understand `fallback`.
The docs page is `docs/configuration/outbound/mieru.md`, and the generated schema
contains `MieruFallbackOptions`.

Ownership is deliberately literal: `primarySpec`/`secondarySpec` hold the raw
fork dialer and DNS bridge; each generation creates its own official
`Client`, `ClientConfig`, `serverDialer` and UDP socket registry. The two
runtimes never share a mutable client, Mux, packet registry or in-flight count.
`fallbackSelector` holds only O(1) preference/cooldown/generation state; it has
no goroutine, timer or background probe. `Outbound.Start`, `Close` and
`InterfaceUpdated` start/stop both generations under the same Scope and bump a
local generation; a late result from N cannot update N+1.

The selector only chooses a new session. A normal preference change never calls
`Stop()` on either SDK client and never migrates an established session.
A primary transport failure starts a 30 s cooldown; the first business dial
after it may become the single recovery probe, and every other concurrent dial
stays on the secondary. A target SOCKS reply (for example connection refused)
is target-class: it proves the proxy path is healthy and does not trigger
fallback. Local DNS/cancel/permission errors are caller-class and are not
retried on the other transport. A bare EOF (possibly wrong credentials or a
server close) is ambiguous; two silent failures are not published as a durable
secondary preference. The ambiguous classification is a documented SDK API
limit: the official `PostDialHandshake` returns a plain error for auth failure,
server close and underlay death alike.

## What is deliberately NOT done

- No second DNS resolver, socket factory, health model, or sleep/wake state.
  The Microsoft/Apple lifecycle model is untouched.
- No sing-box multiplex over Mieru: Mieru's own multiplexer is the only one.
- No `DestinationDNSOwnership` / `Direct Offload` / `CanBypass` capability:
  Mieru is a proxy and none of its paths are equivalent to a local connect.
- No silent acceptance of unsupported fields: unknown JSON keys are rejected by
  the existing schema, invalid transport/credentials/MTU/port ranges/traffic
  patterns fail construction with a message naming the field.

## DONE / PARTIAL / NOT_TESTED

DONE

- `type: mieru` inbound and outbound registered and constructible; schema and
  docs generated.
- TCP data path and UDP associate (UoT) in both directions, including both
  underlay transports, `HANDSHAKE_NO_WAIT`, all mux levels, multi-user, and
  `user_hint_is_mandatory` plumbing.
- Address-shape coverage in the loopback suite: an FQDN destination survives
  the request and reaches the router; an IPv6 listener and an IPv6 destination
  work over `::1`; an additional port from `listen_port_ranges` is really
  bound and served; a domain server name is resolved by the fork's registered
  DNS router, not by the SDK.
- An explicit traffic pattern (seed `20261018`) interoperates with the
  official client and the official server in both directions.
- Phase-2 performance matrix against the official client and official server
  entry point, with raw cells, CPU/RSS, profile and A/B evidence under
  `verify-evidence/mieru/2026-10-18-84a38ca85f7`.
- `InterfaceUpdated` no longer stops/restarts the SDK client before its first
  dial; after a dial it still rebuilds.
- Phase-3 debug: listener and UDP-underlay registries that close the sockets the
  SDK leaves open; a per-association UoT read mutex for the `net.PacketConn`
  concurrent-reader contract; SOCKS5 failure replies for unsupported
  commands/invalid destinations; explicit error on Start-on-closing-scope;
  `UserName()` preserved through `trackedConn`; oversized-frame suffix
  validation. See `phase3-debug/BUG-LEDGER.md`.
- 4-round old/new performance A/B after the fixes: TCP within 卤2%, UDP echo
  卤5% in opposite directions, latency/startup unchanged.
- 4-round target/official matrix against real `mita` 3.38.1 on Windows: TCP download
  +15%/+14%, UDP echo +5%/+9%/+46%, but first-byte P50 ~2x slower and UDP-transport upload
  0.739x; recorded in `phase3-debug/mita/`.
- 3000 short connections (two runs, 6000 total): 0 failures, P50 1.02 ms / P95 1.07 ms;
  the SDK's idle underlays settle after ~4 min (1 socket, 46 MiB, 329 handles). See
  `phase3-debug/soak/`.
- Optional `fallback` for one Mieru outbound: explicit primary/secondary TCP↔UDP,
  two independent SDK generations, protocol-confirmed preference changes, only
  new sessions, no active-session migration. Disabled by default; `both` and
  `no_wait` combinations are rejected. See the section above and
  `verify-evidence/mieru/2026-10-18-fallback/`.
- Fallback tests: config contract, pure selector/generation/cooldown/backoff,
  real SDK loopback TCP-refused→UDP, UDP blackhole→TCP, TCP accept blackhole→UDP,
  UDP associate both directions, target refusal no switch, existing primary and
  secondary sessions surviving a preference change, concurrent failover,
  single-flight recovery, Close/InterfaceUpdated/socket ownership.
- Real mita 3.38.1 fallback: healthy primary and TCP-refused→UDP / UDP-blackhole→TCP
  both reach the real server; restricted-user target refusal (SOCKS code 2) does
  not trigger fallback.
- 4-round disabled old/new A/B: TCP download 1.00-1.07x, upload 1.00-1.02x;
  UDP echo 0.92-0.95x (no mechanism in the per-datagram path; treated as run
  variance, not a claimed speedup). Enabled no-failure vs disabled: TCP
  download 1.024x, upload 1.021x, UDP echo 1.053x, first-byte identical.
- Idle fallback overhead: +0.25 MiB RSS, no extra threads/sockets; selector
  hot path 26.6 ns/op, 0 allocs.
- Explicit `Stop()` lifecycle, close of established sessions, bounded drain,
  network-change rebuild, listener options applied through the fork's listener
  helpers.
- Unit tests, SDK conformance tests, loopback end-to-end tests, `-race`,
  full-tag build, `sing-box check` for valid and invalid configurations.
- Cross-compilation with `CGO_ENABLED=0`: `./protocol/mieru` for
  linux/amd64, darwin/arm64, freebsd/amd64 and android/arm64, and the full
  `./cmd/sing-box` tag profile for linux/amd64 and darwin/arm64.
- The nested `test` module was retidied and `go vet`ed with the production
  tags; the added SDK is in its graph as an indirect requirement.

PARTIAL

- Inbound port ranges are implemented and live-tested (`listen_port_ranges`).
  Outbound port ranges are translated into SDK endpoints (`server_ports`) and
  covered by config tests, but the SDK's random endpoint choice was not forced
  in a live test. A range is expanded by the SDK, so a very large range creates
  one listener per port 鈥?intentional, matching mita.
- IPv6 is live-tested for the TCP underlay on `::1`; the UDP underlay was
  live-tested on IPv4 only.
- `InterfaceUpdated` rebuilds the client only after the first dial; before that
  the SDK has no underlay to reset. A real Wi-Fi 鈫?cellular hand-off was not
  reproduced on this machine.
- Fallback is explicitly **not** an active-session migration: normal preference
  changes only affect new sessions; `InterfaceUpdated` keeps its original
  network-change semantics. The fallback endpoints were live-tested on IPv4
  loopback; a different-host/secondary-FQDN and IPv6 fallback path is covered by
  configuration/dialer ownership, not a separate live matrix.
- The SDK returns a plain error for authentication failure, server close and a
  dead underlay. A bare EOF is kept ambiguous; two silent handshakes are not
  published as a durable secondary preference, but a wrong-credential attempt
  can still pay one secondary attempt (the SDK offers no discriminator, and the
  UDP-blackhole path must not be treated as healthy).

NOT_TESTED

- mbox, extended and Hiddify reference adapters: unavailable on this host (no WSL/Docker,
  no scriptable Windows build). Real `mita` 3.38.1 did run, and both official-client and
  target directions reached it over TCP+UDP. Mihomo did run, but with SDK v3.38.0, so its
  ratios are product-level and are kept out of the unified-SDK matrix.
- A multi-hour soak, packet-loss/latency matrix, TUN, iOS/Android devices, and
  Wi-Fi/cellular path changes. These require other hardware, a longer run, or a
  privileged runner. The 3000-short-connection check is done (`phase3-debug/soak/`);
  fallback has a real-mita loopback matrix but no mobile/real-network failover run.
- `go test -race` on Linux/macOS: the Windows run used `CGO_ENABLED=1` with the
  mingw toolchain. CI's race suite is the cross-platform check.

## Next round

1. Real `mita` 3.38.1 now runs on Windows; the 4-round target/official matrix is in
   `phase3-debug/mita/`. Move the same matrix to Linux to validate the Windows socket and
   scheduling differences, including auth failure, duplicate user names, port ranges, and
   `traffic_pattern` round-trip.
2. Re-run the matrix on Linux against the real mita binary, then add mbox, extended and
   Hiddify as reference clients. The Windows Mihomo result is a product comparison at SDK
   v3.38.0, not a unified-SDK cell.
3. Investigate the 6% TCP-upload gap at `tcp-low-default`: per the profile the
   cost is in the shared copy pipeline (`copyExtendedWithPool`), not in the
   Mieru adapter, so establish whether it is an inbound/copy-buffer effect
   before changing either.
4. Add a Linux CI interop job that builds `mita` from the pinned tag and runs
   the official `test/deploy/singbox` scripts against this fork.
