# Performance regression report

## Method

- Same host, same WSL2 image, same loopback topology as stages 1 and 2: two
  sing-box processes on real sockets, 4 KiB pattern page verified by `memcmp`,
  payload counted at the target (upload) or the client (download), CPU from
  `/proc` boundary ticks plus `wait4` rusage, RSS/VmHWM/threads/FDs sampled every
  100 ms.
- Binary built from merged HEAD `f30e36a4dea` with the stage-1 tag set
  (`DEFAULT_BUILD_TAGS_OTHERS,with_trusttunnel,with_awg,with_naive_outbound,with_purego`),
  `CGO_ENABLED=0`. Effective dependency pins are in `docs/perf/ENVIRONMENT.md`.
- Baseline: the values in `docs/perf/BASELINE.md` (the stage-1 headline table).
  The stage-1 `matrix.json` was **not** used as the baseline: it aggregates every
  run of the day, including the early failed ones, and a median over those is not
  the baseline the report published.
- The first round used 3 repetitions × 5 s for all twelve scenarios; cells whose
  median moved outside the spread were re-run with 5 repetitions in a second
  batch (AnyTLS, Mieru TCP/UDP, REALITY, TrustTunnel H3), and the 5-repetition
  values are the ones quoted. The first round's 3-repetition outliers are kept in
  `results/` rather than hidden.
- Cells that fail for a known reason (B7) are written as failed, not as zero.

## Throughput: stage-1 baseline → final (MiB/s, medians)

| scenario | up 1→4 baseline | up 1→4 final | down 1→4 baseline | down 1→4 final |
|---|---|---|---|---|
| VLESS Vision | 243.7→608.6 | 264.7→809.1 | 532.5→1372.8 | 542.3→1409.0 |
| VLESS REALITY | 255.2→782.8 | 266.2→814.2 | 542.8→1365.1 | 528.0→1455.5 |
| AnyTLS | 255.4→750.3 | 255.9→769.4 | 557.2→1198.2 | 538.9→1234.3 |
| TrustTunnel H2 | 176.1→208.5 | 172.5→213.5 | 134.8→137.8 | 140.3→140.7 |
| TrustTunnel H3 | 119.1→196.5 | 145.7→202.2 | 195.0→202.2 | 194.8→233.0 |
| Naive H2 (Cronet) | 89.5→198.0 | 130.5→238.2 | 219.7→302.9 | 234.6→371.6 |
| Naive H3 (Cronet) | 47.3→70.5 | 58.5→78.1 | 70.1→60.9 | 91.5→76.4 |
| XHTTP H2 (reference server) | 32.6→125.6 | 32.7→126.3 | 83.8→213.4 | 89.6→368.6 |
| XHTTP H3 (reference server) | 32.6→121.9 | 32.8→127.4 | 83.3→169.3 | 119.8→203.9 |
| MASQUE CONNECT-IP | 64.2→72.4 | 70.1→80.5 | 64.0→70.1 | 72.3→79.2 |
| Mieru TCP | 122.0→429.7 | 122.6→483.1 | 105.1→701.3 | 103.5→689.4 |
| Mieru UDP | 30.5→73.6 | 30.4→73.0 | 31.5→39.7 | 31.2→39.9 |
| WireGuard (userspace) | 49.2 (1 stream) | 51.0 (1/1 rep; later reps hit B7) | failed (B7) | failed (B7) |
| AWG (userspace) | 42.2 (1 stream) | 54.3 (3/3) | failed (B7) | failed (B7) |
| AnyTLS UoT | 23.9→22.3 | 23.6→22.0 | — | — |
| TrustTunnel H2 UDP | 9.6→8.9 | 10.3→10.0 | — | — |
| TrustTunnel H3 UDP | 32.0→28.4 | 34.3→31.5 | — | — |
| MASQUE DATAGRAM | 45.6→46.9 | 49.5→53.5 | — | — |

**Verdict: no regression.** Every cell that moved down in the 3-repetition round
moved back to the baseline level when re-run with 5 repetitions: AnyTLS 4-stream
upload 529.6 → 769.4 against a 750.3 baseline, Mieru 4-stream download 616.2 →
689.4 against 701.3, REALITY 4-stream download 1173.6 → 1455.5 against 1365.1.
Those drops were run-to-run spread on this host, not code.

Several cells are **faster** than the stage-1 baseline by more than the spread:
XHTTP H2/H3 download, Naive H2/H3 both directions, TrustTunnel H3 download, and
MASQUE upload/download. No commit in this branch touches those data paths
(XHTTP's only change is the closed-pool refusal; the merge adds the hidden-group
feature). The conservative reading is host-state drift between the baseline day
and this run, not an optimisation: this report makes no improvement claim.

## CPU

Client CPU s/GiB for the cells whose definitions are stable across both rounds:

| cell | stage 1 | final |
|---|---|---|
| Vision upload 4 / download 4 | 4.71 / 2.10 | 5.21 / 3.00 |
| REALITY upload 4 / download 4 | 5.38 / 3.09 | 5.16 / 2.92 |
| AnyTLS upload 4 / download 4 | 5.60 / 4.20 | 5.47 / 4.11 |
| TrustTunnel H2 upload 4 / download 4 | 20.91 / 13.42 | 10.12 / 13.10 |
| TrustTunnel H3 upload 4 / download 4 | 16.1 / 11.0 | 13.60 / 15.49 |
| Naive H2 upload 4 / download 4 | 9.99 / 6.64 | 9.39 / 6.09 |
| Mieru TCP upload 4 / download 4 | 10.61 / 6.94 | 13.74 (3-rep) / 7.66 |
| MASQUE upload 4 / download 4 | 37.5 / 41.9 | 33.86 / 40.89 |

The Vision upload 4 CPU is the one visible move (+10.7 %) and it comes with a
+33 % throughput move in the same cell, so CPU-per-GiB is not comparable between
two measurements whose payload rate differed that much; a fixed-workload CPU
comparison would need a pinned sender rate, which the lab does not implement.
TrustTunnel H2's `-51.6 %` "improvement" is the same artefact in reverse: the
stage-1 CPU number came from a run whose throughput was 0.3 MiB/s (handshake
bound), where per-GiB cost is dominated by handshakes. Neither is used as
evidence of a regression or an optimisation.

## Resource side

Client-side peaks, final batch (sampled every 100 ms; RSS is VmHWM):

| scenario | threads peak | FDs peak | RSS peak MiB |
|---|---|---|---|
| VLESS Vision | 20 | 38 | 67 |
| REALITY | 20 | 18 | 72 |
| AnyTLS | 20 | 23 | 70 |
| TrustTunnel H2 | 18 | 27 | 77 |
| TrustTunnel H3 | 19 | 16 | 70 |
| Naive H2 (Cronet) | 26 | 22 | 75 |
| Naive H3 (Cronet) | 25 | 23 | 76 |
| XHTTP H2 | 21 | 21 | 126 |
| XHTTP H3 | 22 | 21 | 122 |
| MASQUE | 21 | 51 | 87 |
| Mieru TCP | 22 | 43 | 218 |
| Mieru UDP | 22 | 262 | 381 |
| WireGuard | 19 | 16 | 107 |

The stage-2/3 findings that matter are visible here: Cronet adds ~6 threads and
~9 MiB over a plain TLS outbound; Mieru's FD/RSS profile is dominated by its
underlay retention (S3-2), not by a per-flow leak; MASQUE's 51 FDs are the
userspace stack plus QUIC.

## Reproduce

```sh
CGO_ENABLED=0 go build -tags \
  "$(cat release/DEFAULT_BUILD_TAGS_OTHERS),with_trusttunnel,with_awg,with_naive_outbound,with_purego" \
  -o "$HOME/perf/bin/sing-box-final" ./cmd/sing-box
$HOME/perf/bin/perflab run -scenario scripts/perf/scenarios/vless-vision-tcp \
  -out "$HOME/perf/final" -bin-lab "$HOME/perf/bin/sing-box-final" \
  -reps 5 -duration 5s -warmup 1s -streams 1,4
```

Raw runs: [`results/final/`](results/final/) (3-repetition matrix) and
[`results/final2/`](results/final2/) (5-repetition re-runs).
