# XHTTP benchmark harness (`xnettool`)

This directory holds the measurement side of the XHTTP performance project: a
reproducible A/B stand that runs **this fork's XHTTP client** and the **reference
Xray client** against the **same reference Xray server**, under the same XHTTP
settings, and reports throughput, latency, connection setup and CPU.

It is deliberately separate from `test/interop`. The interop stand answers "do
the bytes arrive and is the wire shape right"; this one answers "what does it
cost". A cell here is only interesting if the interop stand already passes it.

## What is measured

| dimension | how |
|---|---|
| upload throughput | one proxied TCP stream, pattern payload, measured window |
| download throughput | same connection, downlink direction |
| ping-pong latency | `k`-byte request/response round trips on one stream (p50/p90/p99) |
| connection setup | a fresh proxied connection per iteration: SOCKS dial → one byte → close |
| duplex | both directions at once on one stream |
| CPU | `GetProcessTimes` deltas for the client and the server, sampled around the measured phase only |
| integrity | every payload byte is a function of its stream offset and is verified at the far end |

CPU is charged to the processes that paid it — the tunnel client and the shared
reference server — and never includes process startup or teardown. The load
generator's own CPU is reported by the OS, not charged to either side.

## Shape of a run

```
xnettool run
  -xray   <path to xray.exe>          reference server AND reference client
  -sing-box <path to sing-box.exe>    this fork's client
  -work   <dir for generated configs and logs>
  -out    <dir for results>
  -modes        packet-up,stream-up,stream-one,auto
  -security     tls,reality
  -versions     h1,h2                 (+ -h3 for HTTP/3 cells)
  -xmux         default,single,rotate
  -impair       rtt50,rtt200,lossy    (empty = clean path)
  -patterns     upload,download,pingpong,connect,duplex
  -concurrency  1                     (comma-separated levels)
  -reps         3
  -duration-ms  4000                  measured window for throughput patterns
```

Each environment gets **one** reference server process, shared by both clients,
and both clients run against it at the same time. Repetitions alternate which
client is measured first, so a thermal or background drift lands on both sides
instead of on one. Results are medians over repetitions, with the raw runs kept:

- `<out>/<timestamp>/runs.json` — every run, including per-process CPU
- `<out>/<timestamp>/summary.md` — the comparison tables
- `<out>/<timestamp>/summary.json` — the same numbers, machine-readable
- `<out>/<timestamp>/cells/<env>/` — the generated configs and both logs

## Subcommands

| command | purpose |
|---|---|
| `target` | the destination behind the tunnel: deterministic pattern source, sink, echo and duplex |
| `loadgen` | the load generator; drives one shaped workload through a SOCKS5 inbound |
| `shim` | impairment box: one-way delay, jitter, stalls and injected resets |
| `udpshim` | UDP counting relay: datagrams and wire bytes per direction, per client, in front of the reference server; optional loss, delay and outage injection |
| `tlscover` | the camouflage TLS endpoint a REALITY server forwards unauthenticated handshakes to |
| `procstat` | per-process CPU seconds and working set |
| `clockprobe` | the measured resolution of the clocks the harness can use |
| `run` | the matrix runner described above |

## Fairness rules the harness enforces

1. **Both clients get the same XHTTP settings**, each in its own schema: same
   mode, same path, same XMUX section, same padding defaults, same ALPN.
2. **One shared reference server per environment.** A server restart between the
   two sides would move the server-side cost too.
3. **The same trust decision on both sides.** The harness mints its own CA and
   server certificate; our client verifies it with `certificate_path`, the
   reference with `certificates: [{usage: "verify"}]` plus `disableSystemRoot`.
   Neither side skips verification, so neither side skips the work.
4. **The same reference version for the whole run**, recorded by the reference's
   own startup banner in each cell's log.
5. **Payloads are verified.** A faster run that corrupted a byte is a failure,
   and the report says so in its integrity section.

## Environment limits, stated rather than implied

- **No packet loss.** This host has no Linux, no container runtime and no WSL
  distribution, so `tc netem` is unavailable, and a Windows host without a
  packet-filter driver cannot drop a TCP segment without acknowledging it. The
  `lossy` impairment therefore injects what can be produced honestly at the
  application layer — extra one-way delay, jitter, bursty stalls and aborted
  connections — and every result records which impairment was in force. A true
  light-loss cell is `BLOCKED_BY_ENVIRONMENT` here.
- **H3 cells are not impaired.** The shim forwards TCP; a QUIC path would need a
  UDP impairment this host cannot produce.
- **The clock is coarse on this host.** `xnettool clockprobe` measures Go's
  monotonic clock at roughly 500 µs granularity here (against ~300 ns for
  `QueryPerformanceCounter`). The load generator therefore times per-operation
  latency with QPC where it exists, and no transport behaviour is allowed to
  depend on sub-millisecond timing.
- **QUIC traffic is counted by a relay, not by the OS.** Windows per-process IO
  counters do not count Winsock sends, and a packet capture needs privileges this
  host lacks, so `xnettool udpshim` measures datagrams and wire bytes per
  direction. It is a NAT (one upstream socket per client address), because the
  client may open a second QUIC connection from a new source port. CPU measured
  through the relay is not comparable with a direct run — one extra hop copies
  every datagram through userspace — but the ratios are.
- **The relay also injects faults**, which is how the HTTP/3 failure paths are
  exercised on a host with no packet filter: `-drop-prob` (datagram loss),
  `-delay-ms` (added one-way delay, as a queue of release times — never a sleep
  per datagram, which would make the path stop-and-wait and cap throughput at
  `datagram/delay`), `-drop-after-ms` (a permanent outage) and
  `-blackhole-every-ms`/`-blackhole-for-ms` (recurring outages). It counts what it
  dropped, so a run can prove the impairment happened instead of assuming it did.
  Reordering is not available: the relay forwards in order.
- **TCP cells take `-impair rtt<ms>`** (e.g. `rtt30,rtt50,rtt100,rtt200`), which
  sets the shim's one-way delay to half the round trip; a latency question is a
  curve, so the value is parsed rather than enumerated. `lossy` remains the only
  named compound impairment. The shim is a delay line, so added latency does not
  cost bandwidth — a high-RTT cell measures the client, not the shim.
- **Affinity and priority are best effort.** Roles are pinned to disjoint cores
  and raised to high priority; a failure to do so is ignored, because an
  unpinned measurement is noisier, not wrong.

## Reproducing

```powershell
# 1. reference binaries
#    Xray v26.9.30 (windows-64) and the fork's own client
go build -tags "$(Get-Content release\DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()" `
    -o ..\..\xhttp-outperform\tools\sing-box.exe .\cmd\sing-box

# 2. the harness
go build -o ..\..\xhttp-outperform\tools\xnettool.exe .\xhttpbench\cmd\xnettool

# 3. one environment, three repetitions
..\..\xhttp-outperform\tools\xnettool.exe run `
    -xray <xray.exe> -sing-box <sing-box.exe> -work <work> -out <out> `
    -modes packet-up -security tls -versions h2 `
    -patterns upload,download,pingpong,connect -reps 3
```

The harness binary must be built from this directory (the `test` module) so that
`go build` resolves the same module graph the transport is tested with.
