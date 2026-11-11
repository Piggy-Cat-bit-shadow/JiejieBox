# Closure report (2026-11-11, round 3.5)

This is the targeted closure round after the three-stage programme. It is not a
fourth phase: it fixes what could be fixed in this environment, re-verifies the
earlier fixes, and states plainly what remains.

## Start and end state

- Start: `work/closure-2026-11-11` created from the stage-3 head
  `2e0003012e1`, then merged with the current `testing` tip (`1ce3aa3a825`,
  15+ commits: hidden-group, routing-dns, daemon/libbox, release docs). Merge
  commit `fa256e89afd`; no conflicts with the programme's files.
- End: this branch, commits `699258bfa95` (schema + wireguard pin) and the
  commit carrying this directory. Worktree clean. Not merged, not pushed to
  `testing`.
- Dependency fork: `Piggy-Cat-bit-shadow/wireguard-go` branch
  `fix/outbound-wake-2026-11-11` = `7ece3d66d79d`, one commit on the pinned
  `a765d957fd81`. Root and test `go.mod` now pin
  `v0.0.0-20261111081811-7ece3d66d79d`; `go.sum`, `release/source-lock.json`
  and `release/dependency-final-shas.json` agree.

## P1 outcomes

| area | status | evidence |
|---|---|---|
| B7 WireGuard/AWG stall | `FIXED_AND_VERIFIED` | fork wake fix + 36/36 two-process cells pass (WG and AWG, 2/4 streams, 0/200 ms/1 s, 3 reps) |
| Run schema / data truth | `FIXED_AND_VERIFIED` | 48/48 historical files validate under v1.1; 4 valid + 3 invalid fixtures fail closed |
| Binary provenance | `DEFERRED` | design complete, implementation not done; current `cgo_enabled`/tags are still host-derived or null (see `PROVENANCE-AND-SCHEMA.md`) |
| UDP measurement / B8 | `DEFERRED` (B8 is `UNRESOLVED`) | audit proves the current cell cannot measure loss (no pacing, no sequence, no drain, target count discarded); paced mode designed, not implemented |
| S3-9 Mieru recovery race | `UNRESOLVED` | 1/20 failures under `-race` (5 %), 0/20 without; test expectation is correct, pick-to-dial is in the SDK |
| Earlier four fixes | `PREVIOUSLY_FIXED_AND_REVERIFIED` | XHTTP close, rate clock, REALITY handoff, Mieru generation tests still pass |

## B7: root cause and fix

The pinned fork had exactly one wake site for `waitOutboundSpace`:
`Peer.Stop`. Normal progress (staged flush after the handshake, eviction,
give-up drop, sequential sender release) decremented `queuedOutboundPackets`
without waking anyone, and the space check ran before the wake channel was
registered. A writer that parked at the 1024-packet cap therefore slept forever
even after the backlog drained; keepalives bypass the staged path, so the tunnel
still looked alive while every TCP flow stalled. The counter was **not** leaking
— 1024 is a legitimate full backlog of batched containers.

`fix/outbound-wake-2026-11-11` adds `releaseOutboundPackets`, wakes after
`Peer.Start` resets, and registers the wake channel before the final check. It
carries three internal tests (park→release wakes; release above the cap keeps
parked; Stop wakes). The two-process matrix with the new pin:

```
wireguard-endpoint 0ms/200ms/1s  streams 2,4  18/18 pass (42.9-60.5 MiB/s)
awg-endpoint       0ms/200ms/1s  streams 2,4  18/18 pass (15.4-56.2 MiB/s)
```

Before the fix the same cell failed 2/2 at 0 ms and 200 ms spacing. The 1 s
AWG 4-stream cells show a lower goodput (15-24 MiB/s) but no stall; recorded,
not treated as a regression (same-pin before/after is impossible because the old
pin stalls).

## Schema: the data was right, the schema was wrong

48 historical `run.json` files produced 2,056 validation errors, all
`wire/latency/udp: null` where the runner correctly did not measure. The schema's
own rule says null never means zero, and `goodput` was already nullable. v1.1
makes exactly those three blocks object-or-null and restricts summary units to
what the runner emits; the payload version stays `perf-run/v1`.
`validate_runs.py` validates history (48/48 pass) and a fixture corpus (4 valid
pass, 3 invalid fail), and `run-baseline.sh` validates every run it produces,
reporting a missing validator as unavailable rather than passing.

## UDP / B8

The audit found fourteen ways the old cell manufactures loss: no pacing, no
sequence, reader teardown at the send deadline, drain absent (≈1.6 % at 80 ms
RTT), target ingress counts discarded, one SOCKS association shared by N
labelled "streams", MTU blackhole below 1500, no reset/quiesce between
warmup and measurement. With no pacing, "sent" means the local socket accepted
a datagram, so `lost = sent - received` cannot separate link loss from sender
overload. A paced+sequenced+drained design exists in `UDP-RETEST.md`; it was not
implemented in this round, so B8 is **UNRESOLVED**, not reclassified by
assertion. The earlier `CONFIRMED_NOT_A_BUG` is withdrawn.

## Provenance

`source.Collect` still records host `go version`, may record tags from a sidecar
that does not exist, and records `go.mod` require pins. This environment cannot
get a VCS revision into the binary: the worktree's `.git` is a file, and Go's
`buildvcs` requires a `.git` directory, so `mod (devel)` with no `vcs.revision`
is the honest result. The design in `PROVENANCE-AND-SCHEMA.md` uses
`debug/buildinfo.ReadFile`, binary sha256, a builder sidecar, and fail-closed
comparison; it is `DEFERRED` with concrete code paths and tests.

## Mieru S3-9

`-race -count=20`: 19 pass, 1 fail (5 %), same SOCKS EOF at the post-close dial;
`-count=20` without race: 20/20 pass. The test proxy is not the artifact: the
failure is the client picking the underlay the server just closed. Fix needs a
bounded, error-classified retry or an SDK change; neither was safe to land
without a product decision. `UNRESOLVED` with the repro.

## What was not re-measured, and why

No production data path changed except the wireguard-go pin, so the stage-1/2
matrices are not rerun. The B7 matrix is the affected path and was rerun on the
new pin. The schema/report fixes change no measurement. This supports
"no credible regression in the measured scope", not "no regression anywhere".

## The ten required answers

1. Start `2e0003012e1` on `work/closure-2026-11-11`, merged with `testing`
   `1ce3aa3a825`; end at this branch's last commit, clean.
2. Changed states: B7 → FIXED_AND_VERIFIED; schema → FIXED_AND_VERIFIED;
   B8 → UNRESOLVED (was CONFIRMED_NOT_A_BUG); S3-9 stays UNRESOLVED with a
   measured 1/20 distribution; provenance, UDP paced mode and the other
   deferred items unchanged.
3. B8: the old experiment cannot answer the question; no protocol defect is
   claimed. The old conclusion is withdrawn, not replaced by another guess.
4. Schema: yes, automatically — 48/48 historical files, 7 fixtures with
   fail-closed invalid cases, and a per-matrix hook in `run-baseline.sh`.
5. Binary/Fork pin: `go.sum`, `go.mod`, `test/go.mod`, `source-lock.json` and
   `dependency-final-shas.json` agree on `7ece3d66d79d`; BuildInfo extraction is
   designed but not implemented, so the report still relies on the tree pin.
6. B7: fixed in the fork and verified by the 36-cell two-process matrix.
7. S3-9: not fixed; 1/20 under race, 0/20 without; a retry would change
   connection semantics and needs a product decision.
8. No credible regression in the measured scope; only WSL2/loopback and the
   userspace data plane were measured.
9. Found beyond the list: the schema contradiction (2,056 false failures), the
   provenance drift (require vs replace; sidecar/tags), the `-buildvcs`
   worktree limitation, and the UDP instrument's false-loss mechanisms.
10. Grade: **READY_WITH_EXPLICIT_BLOCKERS**. The user needs to decide/own:
    wireguard-go fork merge and pin consumption; UDP paced-mode implementation
    and B8 rerun; provenance BuildInfo implementation; Mieru retry policy or SDK
    upstream fix; platform runs.
