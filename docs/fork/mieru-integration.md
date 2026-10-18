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
| `hiddify/hiddify-sing-box` | `v5` / `3bbbd61ee4d76cc6ff433b111fed4187ad2f4b4f` | `Accept()` error backoff (100 ms → 5 s), handshake/multiplexing enum maps, port binding schema. | PARTIAL. Enum mapping ideas were kept (case-insensitive, official enum names accepted). The unconditional backoff was rejected: `server.Accept()` reports per-connection handshake failures as well as a dead Mux, so a blanket delay lets one bad client slow every legitimate connection behind it. The Mux-level failure (`io.ErrClosedPipe`) is terminal and stops the loop; per-connection errors continue immediately. |
| `shtorm-7/sing-box-extended` | `extended` / `5956780ba3f140feff771d003914a1b82ea9d5d0` | Starting the client in `Start` rather than the constructor, MTU/handshake fields. | KEEP the Start-stage lifecycle. REJECT its startup DNS pre-resolution (`addresses[0]`): it discards the fork's dual-stack strategy, fallback and rotation. |
| `MetaCubeX/mihomo` | `Alpha` / `94b13aed49b8dc41973e4bb3f721c5fa2c5c5c1f` | Explicit `Stop`, lazy start, `StreamListenerFactory`/`PacketListenerFactory`, a resolver and packet dialer. | KEEP explicit `Stop()` and the listener factories. REJECT lazy start on first use: the fork has one lifecycle authority, and a node that starts inside `DialContext` is a second one. REJECT its resolver's single-IP answer for the UDP underlay for the same reason as shtorm. |

## Data path and ownership

```
Config → registry → option.Mieru*Options → protocol/mieru
  Outbound: NewOutbound → dialer.New (fork dialer; resolves the server through
            the fork's DNS policy) → buildClientConfig → client.Store
            Start(stage=Start) → client.Start → Mux
            DialContext → client.DialContext → Mux → serverDialer → fork N.Dialer
  Inbound:  NewInbound → buildServerConfig (+ listener factories) → server.Store
            Start(stage=Start) → server.Start
              → streamListenerFactory  → listener.ListenTCPWithOptions
              → packetListenerFactory  → listener.ListenUDPWithOptions
            acceptLoop → server.Accept → metadata → RouteConnectionEx /
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

The official `mita` server binary is not published for Windows, so the reverse
direction was driven against the official `apis/server` entry point that `mita`
uses, compiled from the same `v3.38.1` module. The forward direction used the
official `mieru.exe` 3.38.1 Windows client.

| Direction | Peer | Transport | Handshake / mux | Result |
| --- | --- | --- | --- | --- |
| Official client → this inbound | `mieru.exe` 3.38.1 | TCP + UDP | `HANDSHAKE_DEFAULT`, `MULTIPLEXING_HIGH` | TCP PASS, UDP associate PASS |
| Official client → this inbound | `mieru.exe` 3.38.1 | UDP only | `HANDSHAKE_NO_WAIT`, `MULTIPLEXING_OFF` | TCP PASS, UDP associate PASS |
| This outbound → official server | `apis/server` 3.38.1 | TCP + UDP | `default`, `high` | TCP PASS, UDP associate PASS |
| This outbound → official server | `apis/server` 3.38.1 | UDP only | `no_wait`, `off` | TCP PASS, UDP associate PASS |
| Official client → this inbound | `mieru.exe` 3.38.1 | UDP only | `no_wait`, `off`, explicit traffic pattern (seed 20261018) | TCP PASS, UDP associate PASS |
| This outbound → official server | `apis/server` 3.38.1 | UDP only | `no_wait`, `off`, explicit traffic pattern (same seed) | TCP PASS, UDP associate PASS |

The probe (`tester.exe`, stdlib only) starts a TCP echo server and a UDP echo
server and drives both through a real SOCKS5 client. The full path in each case
is: SOCKS5 client → sing-box mixed inbound → Mieru outbound → Mieru inbound →
sing-box direct outbound → echo server. Ports and credentials in the run were
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
entry; 4 ABBA rounds, 4 TCP connections × 64 MiB, 5000 UDP datagrams, 1000
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
- mbox, extended, Hiddify, Mihomo and the real `mita` binary are
  `BLOCKED_BY_ENVIRONMENT` here: this Windows host has no WSL distribution, no
  Docker, and no scriptable Windows build of those references. E6 in the
  evidence records each blocker; no numbers were invented.

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
  one listener per port — intentional, matching mita.
- IPv6 is live-tested for the TCP underlay on `::1`; the UDP underlay was
  live-tested on IPv4 only.
- `InterfaceUpdated` rebuilds the client only after the first dial; before that
  the SDK has no underlay to reset. A real Wi-Fi ↔ cellular hand-off was not
  reproduced on this machine.

NOT_TESTED

- Official `mita` binary against this outbound: no Windows build exists. The
  official client and the official server SDK were measured instead; a Linux
  host with the real `mita` binary is the next step.
- mbox, extended, Hiddify and Mihomo reference adapters: unavailable on this
  host (no WSL/Docker, no scriptable Windows build). The phase-2 matrix covers
  the official client and the official server entry point only.
- iOS/Android device runs, long-duration soak, packet-loss/latency matrix, and
  Wi-Fi/cellular path changes. These require other hardware.
- `go test -race` on Linux/macOS: the Windows run used `CGO_ENABLED=1` with the
  mingw toolchain. CI's race suite is the cross-platform check.

## Next round

1. Run the official `mita` binary against this outbound on Linux, including
   auth failure, duplicate user names, port ranges, and `traffic_pattern`
   round-trip.
2. Re-run the phase-2 matrix on Linux against `mita`, then add mbox, extended,
   Hiddify and Mihomo as reference clients. The Windows numbers are product-
   level and cannot be compared to their SDK builds directly.
3. Investigate the 6% TCP-upload gap at `tcp-low-default`: per the profile the
   cost is in the shared copy pipeline (`copyExtendedWithPool`), not in the
   Mieru adapter, so establish whether it is an inbound/copy-buffer effect
   before changing either.
4. Add a Linux CI interop job that builds `mita` from the pinned tag and runs
   the official `test/deploy/singbox` scripts against this fork.
