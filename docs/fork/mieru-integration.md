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
| `BenchmarkWriteUOTFrame` (this integration) | 116.0 | 64 | 1 |
| `BenchmarkWriteUOTFrameSDK` (SDK wrapper) | 498.3 | 1280 | 1 |
| `BenchmarkReadUOTFrame` (this integration) | 77.60 | 0 | 0 |
| `BenchmarkReadUOTFrameSDK` (SDK wrapper) | 82.79 | 4 | 2 |

This is a framing microbenchmark, not a network throughput claim. It measures
the code that sits once per datagram on a UDP association; the numbers are the
reason the read path keeps a per-association framer.

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
- Explicit `Stop()` lifecycle, close of established sessions, bounded drain,
  network-change rebuild, listener options applied through the fork's listener
  helpers.
- Unit tests, SDK conformance tests, loopback end-to-end tests, `-race`,
  full-tag build, `sing-box check` for valid and invalid configurations.

PARTIAL

- Inbound port ranges are implemented (`listen_port_ranges`); outbound port
  ranges are implemented (`server_ports`). A range is expanded by the SDK, so a
  very large range creates one listener per port — intentional, matching mita.
- `InterfaceUpdated` rebuilds the client. It is exercised by the startup
  network observation in the interop logs and by a unit test; a real Wi-Fi ↔
  cellular hand-off was not reproduced on this machine.

NOT_TESTED

- Official `mita` binary against this outbound: no Windows build exists. The
  official server SDK path (the same API and the same protocol implementation)
  was used instead. A Linux host with the real `mita` binary is the next step.
- iOS/Android device runs, long-duration soak, packet-loss/latency matrix,
  and throughput comparison against other protocols. These belong to the
  architecture/performance round.
- `go test -race` on Linux/macOS: the Windows run used `CGO_ENABLED=1` with the
  mingw toolchain. CI's race suite is the cross-platform check.

## Next round

1. Run the official `mita` binary against this outbound on Linux, including
   auth failure, duplicate user names, port ranges, and `traffic_pattern`
   round-trip.
2. Measure first-packet latency and throughput for TCP vs UDP underlay,
   `default` vs `no_wait`, and `off`/`low`/`high` mux with `mita` + a real
   destination; compare against the SDK's own client on the same host.
3. Profile the per-datagram path on a high-packet-rate UDP flow; the remaining
   write allocation (one pooled buffer) and the `Session` encryption cost are
   the two candidates.
4. Add a Linux CI interop job that builds `mita` from the pinned tag and runs
   the official `test/deploy/singbox` scripts against this fork.
