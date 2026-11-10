# Upstream integration ledger

Living status of the upstream sync rounds. The 2026-11-11 audit
(`UPSTREAM-SYNC-2026-11-11-AUDIT.md`) is the snapshot; this file is where each repo's
result is closed.

## Round 2026-11-11

| repo | correct upstream | fork branch | before | after | state | evidence |
|---|---|---|---|---|---|---|
| sing | `SagerNet/sing:dev` | `dev` | behind 27 | **behind 0** | DONE (stage 02) | merge `4abfaed41727`, compare ahead_by 0 |
| sing-tun | `SagerNet/sing-tun:dev` | `dev` | behind 42 | **behind 0** | DONE (stage 02) | merge `99abf3c27666`, compare ahead_by 0 |
| sing-quic | `SagerNet/sing-quic:main` | `main` | behind 4 | **behind 0** | DONE (stage 03) | merge `aa6d98964049`, compare ahead_by 0 |
| quic-go | `SagerNet/quic-go:dev` | `sagernet` / consumed `integration/0.1.45` | behind 1 | **behind 0** | DONE (stage 03) | `sagernet` `f864f2f1`, consumed `158445cd`, compare ahead_by 0 |
| utls | `MetaCubeX/utls:v1.9.0-mod-meta` | `main` | behind 1 | **behind 0** | DONE (stage 03) | merge `e14b88879d80`, compare ahead_by 0 (1.9 line, not the v1.7 default) |
| cronet-go | `SagerNet/cronet-go:main` | `main` | behind 0 | behind 0 | DONE (no work) | audit row |
| wireguard-go | `sagernet/wireguard-go:main` | `awg3-inject` | behind 0 | behind 0 | DONE (no work, stage 03 re-verified) | ancestry + `go test ./...` device ok |
| apple | `SagerNet/sing-box-for-apple:dev` | `main` | behind 0 | behind 0 | DONE (pin still blocked on Xcode) | audit row |
| android | `SagerNet/sing-box-for-android:dev` | `main` | behind 5 | behind 5 | TODO (stage 04) | audit row |
| desktop | `SagerNet/sing-box-for-desktop:main` | `main` | behind 0 | behind 0 | DONE (no work) | audit row |
| dashboard | `SagerNet/sing-box-dashboard:main` | `main` | behind 95 (no merge base) | behind 95 | TODO (stage 04, merge `--allow-unrelated-histories`) | trial merge 13 conflicts |
| sing-box (main) | `SagerNet/sing-box:testing` | `integration/0.1.45-final` | behind 85 | behind 85 | TODO (stage 05, 108 trial conflicts) | audit row |

## Stage 02 rows

- **sing** `4abfaed41727e43d1d98f36385a86114925f8484` (parents `1729220c46c4` +
  `27b73774b240`), 20 files +670/−447. 19 of 27 upstream commits carried new
  content; 8 were already patch-equivalent in the fork. The `CachedConn` CAS was
  replaced by the upstream mutex ownership because the CAS never ordered `Read`
  against `Close`; a barrier test fails on the pre-merge code and passes after.
- **sing-tun** `99abf3c2766646ccd74c77f7b14c197204506121` (parents `57e9d17fa852`
  + `79ed0653fdc6`), 20 files +484/−150. 31 conflicts: 18 resolved to the fork
  (it already had the stronger budget/waiter/port design), 6 to upstream where
  the fork matched the upstream parent, 13 fused. Go-stack ENOBUFS,
  auto-redirect close/return paths, GSO segment threading and synthesized UDP
  reply IPv4 IDs were absorbed inside the fork's budgets. Four new regression
  tests, all mutation-checked.
- **Kernel consumption** `33d6e4c62a4`: replaces bumped to
  `sing v0.9.6-0.20261110222322-4abfaed41727` and
  `sing-tun v0.9.7-0.20261110222329-99abf3c27666`; `ReadRetry.Wait()` signature
  and the `ErrTooManySegments` recoverability contract adapted.

Stage 03 rows:

- **sing-quic** `aa6d98964049` (merge `7075cd7`, Salamander + all-sender pacing tests): Salamander
  runt reads, XPlus plaintext write length / removal of the in-place
  vectorised writers, ACK-only pacing in CUBIC/BBR/Brutal. Compare ahead_by 0.
- **quic-go** `sagernet` `f864f2f1` (merge `7fde0eb4` + CUBIC pacing test),
  consumed line `158445cdda77` after merging `sagernet`; compare ahead_by 0.
- **utls** `e14b88879d80` (merge of the v1.9 line): the upstream preset
  transport-parameter fix and the fork's marshal-time helper resolve to the
  same behaviour; the helper stays because it also covers the
  SetTransportParameters-before-ApplyPreset order. Compare ahead_by 0.
- **WireGuard**: no merge needed; `awg3-inject` already contains
  `sagernet/wireguard-go:main`, device tests pass.

See `02_SING_SINGTUN_UPSTREAM_INTEGRATION.md` and
`03_QUIC_TLS_WIREGUARD_UPSTREAM_INTEGRATION.md` for the reports, and
`UPSTREAM-DEBUG-AND-VALIDATION.md` for the exact commands.
