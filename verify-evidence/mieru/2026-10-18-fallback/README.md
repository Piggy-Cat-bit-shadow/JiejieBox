# Mieru TCP/UDP fallback 鈥?implementation and evidence

Baseline: `f6daabb9d5c` (phase-3 evidence at `../2026-10-18-84a38ca85f7`).
Feature code: `e1efe4cafdc621473442302d88897b2e476bbf0a`; Linux UDP-EOF fix:
`c603a023d329925dcfad4042c64c0bdb28411fb1`.
Date: 2026-10-18. Host: Windows 10 LTSC 19044, Go 1.26.8, AMD Ryzen 7 4800H.
SDK: `github.com/enfein/mieru/v3 v3.38.1` (unchanged).

## Contract

One `type: "mieru"` outbound, one tag, optional:

```json
{
  "type": "mieru",
  "tag": "mieru-out",
  "server": "example.com",
  "server_port": 8964,
  "transport": "tcp",
  "username": "alice",
  "password": "password",
  "fallback": {
    "enabled": true,
    "transport": "udp",
    "server": "udp.example.com",
    "server_port": 8965
  }
}
```

- Absent / `enabled: false`: the legacy single-client path is used. No second
  SDK client, socket, timer or goroutine is created.
- Enabled: `fallback.transport` is required and must be the opposite of an
  explicit `tcp`/`udp` primary. `both` and `handshake_mode: "no_wait"` are
  rejected. The backup inherits `server`/`server_port` when omitted; a primary
  configured only with `server_ports` must set the backup port explicitly.
- Credentials, Mux level, MTU and Traffic Pattern are shared. The server only
  needs the corresponding TCP/UDP listener; it does not parse `fallback`.
- Only **new sessions** are selected. A normal preference change never calls
  `Stop()` on either SDK client, never migrates an established session and does
  not replay application payload.

## Architecture

| Object | Owner | Responsibility |
| --- | --- | --- |
| `option.MieruFallbackOptions` | config | parse/validate the optional endpoint |
| `resolveFallbackOptions` | `protocol/mieru/config.go` | pure contract validation and endpoint inheritance |
| `transportSpec` | `Outbound` | immutable raw fork dialer + DNS bridge + options for one side |
| `transportRuntime` | `Outbound` | one official SDK Client, ClientConfig, serverDialer and UDP registry per generation |
| `fallbackSelector` | `Outbound` | O(1) preference, cooldown, generation, single-flight probe; no goroutine/timer |
| `Outbound.Start/Close/InterfaceUpdated` | `Outbound` | starts/stops both generations, drains in-flight dials, bumps the local generation |
| official SDK | vendor | Mieru Mux/Session/auth/crypto/underlay; unchanged |

The two runtimes share no client, Mux, PacketConn registry, in-flight count or
generation. The disabled path has exactly one transportRuntime and never enters
the selector. `InterfaceUpdated` is the existing network authority; the local
generation is derived from it and only exists to reject a late N-generation
result after N+1 was installed. No Mieru health state is written to
`runtimecoord`, and there is no Power Governor registration because there is no
background probe to gate.

## Preference and error policy

- A successful standard handshake or a valid SOCKS reply proves the transport
  path; a target error reply (e.g. code 2) does **not** switch.
- A socket/dial error, request-write error, handshake read timeout or reset is a
  transport failure. It starts a 30 s cooldown and this dial tries the secondary
  once.
- While the secondary is preferred, the first new dial after cooldown becomes
  the single recovery probe; every other concurrent dial stays on the secondary.
  A failed probe doubles the cooldown up to 5 min.
- A bare EOF is ambiguous on a TCP primary: the SDK returns the same plain
  error for wrong credentials, server close and underlay death. It is not used
  to publish a durable secondary preference. On a UDP primary, Linux reports an
  unreachable peer as a bare EOF; that is transport (the TCP server/target-close
  interpretation does not apply), which is the Linux blackhole path. If both
  transports fail silently, the selector returns to primary preference instead
  of leaving the outbound stuck on the secondary. **Limitation:** the official
  API gives no stable discriminator, so a wrong-credential attempt can still pay
  one secondary attempt; it cannot
  produce a successful connection without the secondary also authenticating.
- Local DNS/address/permission/cancel errors are not retried on the other
  transport.

Full row-by-row decisions: [ERROR-CLASSIFICATION.md](ERROR-CLASSIFICATION.md).

## Verification summary

- `go test ./protocol/mieru ./option ./include`: PASS (82.6 s; fallback subset 59.4 s).
- `CGO_ENABLED=1 go test -race ./protocol/mieru`: PASS, 150.3 s.
- tagged full build, `go vet`, `gofmt`: PASS.
- Real loopback matrices: TCP refused鈫扷DP, UDP blackhole鈫扵CP, TCP
  accept-but-silent鈫扷DP, UDP associate both directions, target refusal, existing
  primary/secondary sessions, 50 concurrent failovers, one-probe-per-window,
  Close/InterfaceUpdated/socket ownership.
- Real `mita` 3.38.1: healthy fallback config TCP+UDP PASS; primary TCP refused
  鈫?real mita UDP PASS; primary UDP bad 鈫?real mita TCP PASS (10.0 s SDK
  handshake wait); restricted user target reply code 2 did **not** trigger
  fallback (TCP failed in 3 ms).
- Disabled old/new: TCP download 1.00-1.07x, upload 1.00-1.02x; UDP echo
  0.92-0.95x across two 4-round pairs. There is no per-datagram change on the
  disabled path; the UDP-echo difference is recorded as run variance, not a
  claimed improvement.
- Enabled healthy-primary vs disabled (real mita, 4 rounds): TCP download
  1.024x, upload 1.021x, UDP echo 1.053x, first-byte P50 equal (1.034 ms).
- Idle enabled vs disabled: RSS 31.61 vs 31.36 MiB, no extra threads/sockets.
  Selector `choose`+`report`: 26.6 ns/op, 0 allocs.
- 3000 short sessions through fallback (primary TCP refused, secondary UDP
  healthy): 3000/3000 success in 3.29 s, P50 1.025 ms / P95 1.559 ms; 77 idle
  UDP endpoints immediately after, 0 after 4 min, no TCP established leak.
  See `performance/fallback-soak/metrics.txt`.
- Local failover: TCP-refused→UDP dial 1.5 ms; recovery probe 0.5 ms. UDP
  blackhole / TCP silent accept pay the official SDK's fixed 10 s handshake
  wait; this is bounded by the SDK, not an added selector timeout.

Raw data, logs, mutation reproductions, schema and interop are linked in
[TEST-MATRIX.md](TEST-MATRIX.md). Bugs/hardening are in
[BUG-LEDGER.md](BUG-LEDGER.md); ownership and ordering are in
[LIFECYCLE-OWNERSHIP.md](LIFECYCLE-OWNERSHIP.md); performance decisions and
negative results are in [PERFORMANCE-DECISIONS.md](PERFORMANCE-DECISIONS.md).

## Deliberately not done

- No active-session migration and no seamless TCP鈫擴DP handover.
- No per-transport credentials, Mux, MTU or Traffic Pattern in the backup.
- No lazy secondary start: measured idle cost of eager start is +0.25 MiB and
  no extra thread/socket, so lazy start would add lifecycle complexity for no
  measurable benefit (see PERFORMANCE-DECISIONS).
- No background recovery timer/probe. Recovery is business-driven only.
- `mita`/our loopback success is not a claim about real GFW behavior, iOS
  NetworkExtension sleep or mobile NAT. Those remain NOT_TESTED.

