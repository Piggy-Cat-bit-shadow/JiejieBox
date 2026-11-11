# Current performance baseline

> A baseline is a claim about a specific build on a specific path. This one is
> dated 2026-11-11 and was taken on the WSL2 host described in
> [ENVIRONMENT.md](ENVIRONMENT.md). Every table in [matrix.md](matrix.md) is
> generated from the raw `run.json` files; nothing below is copied from an older
> report.

## What was run

| item | value |
|---|---|
| kernel under test | `4ec0decd9f2dd7e3116ab130b3af56291f73988b` (integration/0.1.45-final) |
| lab tooling | `bf0e0bec560` (`scripts/perf`); the worktree HEAD differs from the kernel commit only by lab and docs files |
| binary | `go build -tags "<DEFAULT_BUILD_TAGS_OTHERS>,with_trusttunnel,with_awg,with_naive_outbound,with_purego"`, `CGO_ENABLED=0` |
| load model | 1 s warmup, 5 s measured window, 5 repetitions per cell, 1 and 4 concurrent streams |
| payload | 4 KiB pattern page, verified block by block at the receiving end |
| path | loopback, unless the cell name says otherwise |
| failures | a repetition that moves no payload is a failure, not a zero |

Loopback numbers answer "what does the data path cost when the network is not
the constraint". They are useful for comparing protocol and transport overhead;
they are not predictions of a real link. The shaped cells at the end are the
counterweight.

## Headline, loopback

Goodput is payload MiB/s at 1 / 4 streams. CPU is client seconds per GiB of
payload (the same number for the server is in `matrix.md`). Bolder differences
matter more than small ones, and a cell that failed is written as `failed`, not
as a low number.

| scenario | transport | up MiB/s 1→4 | down MiB/s 1→4 | CPU s/GiB (client) | connect p50 |
|---|---|---|---|---|---|
| vless-vision-tcp | TCP+TLS | 243.7 → 608.6 | 532.5 → 1372.8 | 4.4 / 2.1 | — |
| vless-reality-vision | TCP+REALITY | 255.2 → 782.8 | 542.8 → 1365.1 | 4.2 / 2.0 | 4.09 ms |
| anytls-tcp | TCP+TLS | 255.4 → 750.3 | 557.2 → 1198.2 | 4.2 / 2.8 | — |
| trusttunnel-h2 | HTTP/2 | 176.1 → 208.5 | 134.8 → 137.8 | 9.9 / 10.0 | — |
| naive-h2 | HTTP/2, Cronet | 89.5 → 198.0 | 219.7 → 302.9 | 14.5 / 6.7 | 4.24 ms |
| trusttunnel-h3 | HTTP/3 | 119.1 → 196.5 | 195.0 → 202.2 | 16.1 / 11.0 | — |
| naive-h3 | HTTP/3, Cronet | 47.3 → 70.5 | 70.1 → 60.9 | 24.0 / 19.5 | 3.64 ms |
| masque-connect-ip | QUIC CONNECT-IP | 64.2 → 72.4 | 64.0 → 70.1 | 37.5 / 41.9 | — |
| mieru-tcp | TCP, SDK server | 126.5 → 438.5 | 124.5 → 570.9 | 8.8 / 7.1 | 2.43 ms |
| mieru-udp | UDP underlay | 29.0 → 71.4 | 28.5 → 34.3 | 53.6 / 60.1 | 2.80 ms |
| wireguard-endpoint | WireGuard, userspace | 44.8 → failed | failed | 61.7 | — |
| awg-endpoint | AWG, userspace | 49.8 (n=2) → failed | failed | 55.3 | — |

Datagram cells are listed separately because they are not the same question:

| scenario | pattern | goodput MiB/s 1→4 | client CPU s/GiB |
|---|---|---|---|
| masque-connect-ip | QUIC DATAGRAM | 45.6 → 46.9 | 93.7 / 88.3 |
| trusttunnel-h3 | UDP over HTTP/3 stream | 32.0 → 28.4 | 90.5 / 100.5 |
| anytls-tcp | UoT over TLS stream | 23.9 → 22.3 | 102.1 / 108.6 |
| trusttunnel-h2 | UDP over HTTP/2 stream | 9.6 → 8.9 | 217.2 / 233.6 |

## What the numbers say

1. **Direction matters more than protocol for the Go data paths.** Every
   stream-based protocol downloads faster than it uploads at the same stream
   count — VLESS Vision 2.2×, AnyTLS 2.2×, Mieru TCP ~1× at one stream but 1.3×
   at four, TrustTunnel H2 the reverse at one stream. The asymmetry is in the
   sender's copy and write path plus the receiver's read loop, not in the
   cipher. Any single-direction benchmark of these protocols is half a
   measurement.

2. **The strongest differentiator is CPU per byte, not peak throughput.** The
   TCP+TLS cells need 2–4 client seconds per GiB; TrustTunnel H2 needs ~10;
   Cronet-backed Naive 11–30; MASQUE's userspace IP stack 37–48; the userspace
   WireGuard/AWG endpoints 55–65. A 20% throughput change that doubles CPU per
   byte is a regression on a phone even if the benchmark chart goes up.

3. **H3 is not automatically better than H2.** TrustTunnel H3 sits at
   119–202 MiB/s against H2's 135–208; Cronet H2 is 90–303 against H3's 47–70.
   On this host, with no packet loss, QUIC's userspace crypto and congestion
   control cost more than H2 gains. This matches the direction of the historical
   Naive observation, but the numbers here are new and measured on this build.

4. **UDP-over-stream is the expensive way to carry datagrams.** The same
   1200-byte datagram load costs 90–234 CPU seconds per GiB over a reliable
   stream, against 88–94 for MASQUE's QUIC DATAGRAM, and H2 framing costs more
   than H3 framing (9.6 vs 32 MiB/s at one stream). A protocol that frames UDP
   over a stream should not be compared with a datagram transport on throughput
   alone.

5. **Concurrency does not scale linearly where one process does the copying.**
   VLESS Vision upload gains 2.5× from 1→4 streams while download gains 2.6×;
   MASQUE gains 1.1×; WG/AWG did not survive 4 concurrent flows at all in
   userspace mode. The 4-stream column is where process-level serialisation
   shows up.

6. **Connection setup is cheap on loopback and dominated by the protocol
   handshake.** p50 for a fresh proxied connection is 2.4–4.2 ms at one stream
   and 8–10 ms at four; REALITY costs ~0.1 ms over plain TLS at one stream,
   which is what a session-ticket-less handshake should cost when the network
   costs nothing.

## Shaped cells

Loopback answers "what does the code cost". These answer "what survives a
network". Both profiles use the same binary, load model and payload; the link is
one veth with `netem` on each direction, and the server and target live in the
far namespace.

**100 Mbit/s cap, 20 ms RTT, no loss** — confirms the harness enforces what it
claims:

| scenario | up MiB/s | down MiB/s | wire Gbit/s | overhead |
|---|---|---|---|---|
| vless-vision-tcp | 11.8 | 11.1 | 0.09–0.09 | 0.95–1.01 |
| trusttunnel-h2 | 11.8 | 11.2 | 0.10 | 0.97–1.02 |
| trusttunnel-h2 (UDP over stream) | — | — | 0.19 | 2.29 |

11.1–11.8 MiB/s is 93–99 Mbit/s of payload on a 100 Mbit/s class; the wire
counter agrees with the cap and the overhead ratio is ~1, which is the sanity
check the loopback runs cannot provide.

**80 ms RTT, 1% random loss, both directions** — the same cells:

| scenario | up MiB/s 1→4 | down MiB/s 1→4 | wire overhead | notes |
|---|---|---|---|---|
| vless-vision-tcp | 1.5 → 5.6 | 0.6 → 2.6 | 0.96–1.05 up, 1.05–4.14 down | single-flow TCP is RTO-bound; the download direction retransmits 4× its payload at one stream |
| trusttunnel-h2 | 0.3 → 0.3 | 0.0 (2 of 3 reps moved nothing) | 1.00 up, 35–178 down | the H2 tunnel does not recover; two download repetitions are recorded as failures, not zeros |
| masque-connect-ip | 0.3 → 0.2 | 0.1 → 0.1 | 1.14–1.28 up, 6.7–9.8 down | the userspace IP stack collapses; UDP datagrams returned nothing in 5 of 6 repetitions |

These are not protocol rankings. At 1% uniform loss every stream-based protocol
is dominated by TCP's loss recovery, and the spread between repetitions is
larger than the spread between protocols: the VLESS 4-stream upload moved 3.7 to
14.9 MiB/s across three repetitions. What the cells do establish is that the
laboratory can produce and detect the effect, and that TrustTunnel's and MASQUE's
loss behaviour deserves a dedicated diagnosis in stage 2 rather than a column in
a table.

The UDP-over-stream and QUIC-DATAGRAM cells did not survive this profile at all:
`udp` repetitions returned zero datagrams and are recorded in `failures[]` with
class `no-payload`. That is a result — a datagram path that drops every datagram
under 1% loss is a real defect, not a missing measurement — but three
repetitions cannot say whether the cause is the SOCKS UDP association, the
tunnel's framing, or the loss itself.

## Limits of this baseline

- Loopback has no wire counters: `wire_gbit_per_s` and `overhead_ratio` are
  `null` in every loopback cell, and are only populated in the shaped cells.
- The load generator and, on loopback, the target share the runner process with
  the sampling. Their CPU is recorded in `reps[].loadgen`, never charged to the
  client or the server, but at 4 streams the generator is close enough to a core
  that a wider sweep should re-check it.
- `startup_server_ms` for a UDP-only listener (MASQUE, WireGuard, AWG, Mieru UDP)
  is the scenario's configured settle delay, not a measurement: there is no TCP
  port to probe. `startup_client_ms` is when the client's SOCKS listener
  answered; for Cronet the engine starts on the first request, which is why
  `first_request_ms` is recorded separately in newer runs.
- CPU is process-level. There is no per-thread or hardware-counter attribution,
  and no Go-heap number (`go_heap_peak_bytes` is null everywhere: the production
  binary exposes no profile endpoint).
- A CPU-per-GiB value is only recorded when a repetition moved at least 1 MiB.
  Below that, one 10 ms scheduler tick over a few kilobytes reads as thousands of
  seconds per GiB; the shaped UDP cells show such artefacts and they must not be
  quoted.
- Wire counters sit below segmentation offload on the veth: byte totals are
  comparable across cells, packet counts are GSO super-packets, not wire
  packets.
- The WG/AWG cells are partial. One-stream upload works; four-stream upload,
  download and UDP through the userspace endpoint timed out (60 s connect
  timeout, `connect timeout: connection timed out` in the client log). The raw
  evidence is in the run directories under `docs/perf/results/`. This is
  recorded as a measured limitation of that configuration, not as a protocol
  result, and it is why WG/AWG are not compared with the other protocols.

## Reproducing

```sh
# build the binary (tags as in the table above), then the runner
cd scripts/perf && go build -o "$HOME/perf/bin/perflab" ./cmd/perflab && cd ../..

# loopback matrix, same load model
scripts/perf/run-baseline.sh --bin "$HOME/perf/bin/sing-box" --out "$HOME/perf/baseline" \
  --profile loopback --reps 5 --streams 1,4 --duration 5s --warmup 1s

# shaped cells
scripts/perf/netlab/netlab.sh --profile remote -- "$HOME/perf/bin/perflab" run \
  -scenario scripts/perf/scenarios/vless-vision-tcp -out "$HOME/perf/baseline/shaped-remote" \
  -bin-lab "$HOME/perf/bin/sing-box" -reps 3 -streams 1,4
```

The exact configurations, certificates and logs for each cell are in
`docs/perf/results/`; the run directories there are named after the scenario and
timestamp in `matrix.md`.
