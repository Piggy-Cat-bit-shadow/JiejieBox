# Performance laboratory

This directory is the measurement side of the kernel: one runner, one result
format, one shaped-link harness, and a scenario per protocol. It exists so a
number can be reproduced, traced to a commit and compared with another number
taken under the same conditions — and so that a cell that cannot be measured
says so instead of producing a plausible-looking figure.

It answers questions the unit tests cannot: what the real data path costs in
throughput, CPU per byte, memory, connections per second and latency; what
happens when the path has latency, loss or a bandwidth cap; and where a
protocol's cost actually comes from (transport, TLS/QUIC, native engine, stack).

## Quick start

The lab runs on Linux, or on WSL2 where the network-namespace harness lives. It
has been exercised on Ubuntu 24.04 under WSL2 (kernel 6.18, glibc 2.39).

```sh
# 1. build the kernel binary with the tags the scenarios need
GOFLAGS=-mod=mod go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS),with_trusttunnel,with_awg,with_naive_outbound,with_purego" \
  -o "$HOME/perf/bin/sing-box" ./cmd/sing-box
printf '%s' "$(...tags...)" > "$HOME/perf/bin/sing-box.tags"   # recorded in every run

# 2. build the runner (separate module, standard library only)
cd scripts/perf && go build -o "$HOME/perf/bin/perflab" ./cmd/perflab

# 3. check what the machine will record
"$HOME/perf/bin/perflab" env -binary "$HOME/perf/bin/sing-box"

# 4. one cell, one repetition: a wiring check, not a result
"$HOME/perf/bin/perflab" run \
  -scenario scripts/perf/scenarios/vless-vision-tcp \
  -out /tmp/perf -bin-lab "$HOME/perf/bin/sing-box" \
  -pattern upload -streams 1 -duration 2s -warmup 300ms -reps 1

# 5. the matrix (add --profile remote for the shaped path)
scripts/perf/run-baseline.sh --bin "$HOME/perf/bin/sing-box" --out "$HOME/perf/results" \
  --reps 5 --streams 1,4 --duration 5s

# 6. one document out of many runs
scripts/perf/summarize.py "$HOME/perf/results" --out matrix.md --json matrix.json
```

A run directory contains `run.json` (the raw record), `summary.md` (the human
view), the exact rendered `configs/`, per-process `logs/`, and the throwaway
`certs/` the run minted.

To attach runs to a report, export them with paths that the document can point
at:

```sh
scripts/perf/export-results.sh "$HOME/perf/results" docs/perf/results --with-logs
scripts/perf/summarize.py docs/perf/results --out docs/perf/matrix.md --json docs/perf/matrix.json
```

Logs are gzipped on export: a cell's log is mostly one repeated INFO line per
connection, so 95 MB of raw logs compresses to a few hundred kilobytes and can
be kept as evidence instead of discarded.

## Layout

| path | role |
|---|---|
| `cmd/perflab` | the runner: `env`, `run`, `target`, `reserve` |
| `internal/schema` | the result format, in Go |
| `internal/sampler` | `/proc` sampling: CPU ticks, RSS, VmHWM, threads, FDs |
| `internal/netmeter` | veth byte/packet counters (wire overhead) |
| `internal/loadgen` | SOCKS5 load generator: upload, download, duplex, ping-pong, connect, UDP |
| `internal/target` | the far end behind the proxy, plus the REALITY cover server |
| `internal/runner` | process lifecycle, repetition loop, summaries, reports |
| `netlab/netlab.sh` | user+network namespace, veth, netem/HTB, guaranteed teardown |
| `scenarios/<id>/` | `scenario.json` + `server.json.tmpl` + `client.json.tmpl` |
| `schema/perf-run.v1.schema.json` | JSON Schema for `run.json` |
| `run-baseline.sh` | the matrix runner |
| `summarize.py` | matrix aggregation into Markdown/JSON |
| `export-results.sh` | copy raw runs into the repository tree (logs gzipped) |

`scripts/perf` is its own Go module. The kernel build must not depend on the
lab, and the lab must be buildable without the kernel; both directions are
enforced by the module boundary.

## What a cell actually measures

A cell is **two real processes**: `sing-box` as the server and `sing-box` as the
client, configured by the scenario's templates, exchanging real sockets. The
runner does not call protocol functions directly, so what is measured is what
ships.

```
        loadgen (perflab)                target (perflab)
             |                                 ^
   SOCKS5    v                                 |  plain TCP/UDP
        +---------+   protocol + TLS/QUIC  +---------+
        | client  | <===================> | server  |
        +---------+                       +---------+
             ^                                 ^
             | /proc sampling, boundary CPU reads, wait4 rusage, veth counters
```

- **Goodput** is payload bytes that arrived and were verified, divided by the
  measured wall time. Upload counts what the target received; download counts
  what the client verified. Bytes merely queued into a socket buffer are never
  counted.
- **Integrity** is a 4 KiB pattern page compared block by block at the far end.
  A cell that corrupts a byte fails its integrity field; the number is still
  recorded, with `ok=false`.
- **CPU** per repetition comes from exact `/proc/<pid>/stat` tick reads at the
  boundaries; the scenario total comes from `wait4` rusage. `cpu_source` says
  which. `cpu_seconds_per_gib` is the number to compare across protocols: it
  normalises away both throughput and payload size.
- **Memory** is RSS and `VmHWM` sampled every 100 ms, with start/end values from
  the boundary reads. This is the process's resident set, not the Go heap.
  `go_heap_peak_bytes` stays null: the production binary exposes no profile
  endpoint, and a Go heap number from an instrumented build is not the shipped
  binary's number.
- **Wire** bytes/packets come from the veth's netlink counters (`ip -j -s link`,
  because `sysfs` is not network-namespace aware and would show the host's
  interfaces instead of the namespace's). `overhead_ratio` is wire bytes divided
  by payload bytes; it is null on loopback because loopback has no comparable
  counter. The counters sit below segmentation offload, so they record GSO-sized
  packets: byte totals are comparable, packet counts are not raw wire packets.
- **Latency** is measured per operation for `pingpong` (one round trip per
  sample) and `connect` (SOCKS dial → first echoed byte, i.e. a fresh proxied
  connection). Percentiles are only emitted when the sample count justifies
  them: P95 at n ≥ 20, P99 at n ≥ 100.
- **Startup** for both processes is recorded in `scenario.extra` — for a native
  engine it is a large part of what a user pays.

Two different transports are never ranked against each other. QUIC DATAGRAM
(MASQUE) and a reliable stream carrying datagrams (TrustTunnel `udp`, AnyTLS
UoT) answer different questions; the summary groups cells by transport
guarantee, not by speed.

## Results format

`run.json` follows `schema/perf-run.v1.schema.json`. Two rules matter:

1. **A missing value is `null`, never `0`.** "0 MiB/s" and "not measured" are
   different facts.
2. **Units are in the field name** (`goodput_mib_per_s`, `cpu_seconds_per_gib`,
   `rss_peak_bytes`), so no reader has to guess.

`reps[]` is the raw data. `summary[]` is derived from it and carries the sample
count, median, MAD, min, max and — when there are enough samples — P95/P99.
A result is only comparable with another result whose `source`, `host`,
`network` and `scenario` sections agree.

## Scenarios

`scenario.json` declares the protocol, the two templates, an optional readiness
probe, optional extra facts, and the default load model:

```json
{
  "id": "vless-vision-tcp",
  "protocol": "vless", "mode": "vision", "transport": "tcp", "security": "tls",
  "server": {"config": "server.json.tmpl", "ready": {"tcp": "__SERVER_ADDR__:__SERVER_PORT__"}},
  "client": {"config": "client.json.tmpl", "ready": {"tcp": "__CLIENT_ADDR__:__CLIENT_PORT__"}},
  "defaults": {"patterns": ["upload", "download"], "streams": [1, 4],
               "duration": "5s", "warmup": "1s", "reps": 3}
}
```

Templates use the repository's existing `__NAME__` placeholder convention. The
runner supplies addresses, ports, a per-run CA/leaf certificate, a UUID, a
password, a REALITY keypair (generated by the binary under test), WireGuard
keypairs, and a local REALITY cover port. An unresolved placeholder is a hard
error, not a silent empty string.

Readiness is a functional probe, not a port check: the runner pushes a few bytes
through the whole chain (client inbound → protocol → server → target) before the
first repetition. A protocol whose listener is UDP only declares
`{"sleep_ms": ...}` instead of a TCP probe.

### Adding a protocol

1. Write `server.json.tmpl` and `client.json.tmpl` that pass `sing-box check`.
2. Write `scenario.json` with the load model the protocol can honour.
3. Run one upload repetition and read the logs under the run directory.
4. Only then add it to `run-baseline.sh`.

There is no per-protocol code in the runner. If a protocol needs runner code,
the scenario format is missing a concept, and that is the thing to fix.

## Network laboratory

`netlab/netlab.sh` runs a command inside two network namespaces joined by one
veth pair:

```
namespace A (client)                    namespace B (server)
 10.213.0.1/24  [veth a] <---> [veth b]  10.213.0.2/24
 netem/HTB on egress                     netem/HTB on egress
 perflab + client proxy                  server proxy + target
```

Two namespaces are not decoration. When both veth ends are addresses inside one
namespace, the kernel's local routing table delivers the traffic through
loopback and **no qdisc ever sees it** — a shaped run that silently measures an
unshaped path. The lab made exactly that mistake once; the fix is the second
namespace, and `run.json` records the qdiscs so it cannot hide again.

- Profiles: `loopback` (no namespace), `normal` = 20 ms RTT / 0 %,
  `remote` = 80 ms RTT / 1 %, `weak` = 160 ms RTT / 3 %. RTT is split across the
  two directions; `delay_ms` in the record is the one-way value.
- `--bandwidth-mbps` adds an HTB class with a netem child, so latency and rate
  layer instead of fighting: `htb rate 100mbit` → `netem delay 10ms`.
- `--rtt-ms`, `--loss-pct`, `--jitter-ms`, `--mtu` override the profile.
- `--dry-run` prints the topology, qdiscs and the command without running it.
- Cleanup is structural: the namespace dies with the process, and the inner
  script kills the server-side namespace holder on exit. No host interface,
  qdisc or address is ever created or removed. If the command crashes, the
  namespace takes the debris with it.
- No root. `unshare --user --map-root-user --net` supplies the capability.

Limits, stated rather than hidden: `netem` is a delay line, not a link
simulator — it does not model queue build-up the way a real bottleneck does.
Loss is applied per direction, so end-to-end loss is slightly higher than the
configured value. HTB's rate is approximate below ~10 Mbit/s because of timer
granularity.

## Windows

The runner builds on Windows (`sampler` uses `GetProcessTimes`), but the
namespace harness, `/proc` sampling and veth counters are Linux-only. On Windows
the sampler reports CPU from `GetProcessTimes`, RSS from
`GetProcessMemoryInfo`, and `go_heap_peak_bytes`/`FDs` as null. Windows cells
are for functional comparison; the primary measurements are taken on Linux/WSL2.

## Troubleshooting

| symptom | cause | what to do |
|---|---|---|
| `readiness probe failed`, log shows `started` but nothing else | client cannot reach the server address | check `PERF_CLIENT_ADDR`/`PERF_SERVER_ADDR`; on a shaped run, confirm the qdisc print showed both ends |
| `no free tcp+udp port` | reserving on an address the runner's namespace does not own | shaped runs reserve server-side ports inside the server namespace; check `PERF_SERVER_NS` |
| shaped run reports loopback-level latency | both veth ends in one namespace (the old bug) | confirm `PERF_NET_TOPOLOGY=netns-veth` in `run.json` and both qdiscs in `network.qdiscs` |
| `fixed placeholder __X__` error | a template uses a variable the runner does not supply | add it in `runner.prepare` or remove it from the template |
| Naive scenario fails immediately | `libcronet.so` not next to the binary | the purego build loads it from the executable's directory |
| `git` sources are empty in the record | WSL2 git cannot resolve a Windows `gitdir:` path | the runner translates it; if it still fails, pass `PERF_REPO` |

## What this stage does not do

- No production data path was modified to make a number look better.
- No Go heap, socket-buffer or kernel-memory split: the schema has the fields,
  the current binary exposes no endpoint to fill them, and they are null.
- No packet capture, qlog or PTO/cwnd extraction yet: that is stage 2 material.
- No macOS/Android/iOS cells: the native engines there need those hosts.
