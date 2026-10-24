# Upstream dependency integration — 2026-10-24 (phase 05 state)

**Scope.** This is the dependency companion to `UPSTREAM-INTEGRATION-STATUS.json`, which is the
authoritative ledger. It records the *effective* pins after phases 01–04; the `require` lines are not
the effective versions, because four modules are redirected to `Piggy-Cat-bit-shadow/*` forks by
`replace`.

## Effective pins

| module | effective pin | state |
|---|---|---|
| `github.com/sagernet/sing-tun` | `Piggy/sing-tun v0.0.0-20261024025840-dcf84577d0eb` | **COMPLETED** phase 01 (DEP-TUN-0..3); dev carries the privileged dual-TUN test |
| `github.com/sagernet/sing` | `Piggy/sing v0.9.6-0.20261024041103-4200a04a089a` | **COMPLETED** phase 02 (DEP-SING-1); pause.Manager WaitActiveContext |
| `github.com/sagernet/sing-quic` | `Piggy/sing-quic v0.7.2-0.20261024074641-5e5a0274bd6c` | **COMPLETED** phase 03 (DEP-QUIC-2); upstream 7e0a5869 + ceiling/owned path |
| `github.com/sagernet/quic-go` | `Piggy/quic-go v0.61.1-0.20261024063732-ce02ee5cc210` | **COMPLETED** phase 03 (DEP-QUIC-1); Sagernet line mod.12, not the MetaCubeX stable line |
| `github.com/sagernet/cronet-go` + `all` + 29 `lib/*` | root/all `8aa139e9e140`; libs `v0.0.0-20260929202119-8c68ce89873c` | **COMPLETED** phase 04 (DEP-CRONET-1); byte-identical native generation, Go-only binding above it |
| `github.com/sagernet/tailscale` | `v1.102.1-sing-box-1.14-mod.5.0.20261022222424-1688b52f8204` (official dev module) | **COMPLETED** phase 05 (DEP-TAILSCALE-1); netmon/tsnet default-route hooks present, no fork |
| `github.com/sagernet/sing-cloudflared` | same | **SYNCED_UPSTREAM** |
| `github.com/sagernet/sing-openvpn` | same | **SYNCED_UPSTREAM** |
| `github.com/sagernet/sing-mux` | same | **FORK_EQUIVALENT_OR_BETTER** |
| Go directive | `go 1.25.5` | **SYNCED_UPSTREAM** |

## Cronet native matrix (phase 04)

- `cronet-go/main` = `8aa139e9e140d5737f82deaa934b81b68054712b`.
- Root and `all` are `8aa139e`; all 29 `lib/<os>_<arch>` modules are `8c68ce8`.
- 37 native archives/shared libraries (`.a`, `.so`, `.dll`) are **byte-identical** between the
  native build commit `df0c319` and `8c68ce8`.
- No file under `include/` and no cgo file changed between `df0c319` and `8aa139e`; the only changes
  are Go-only naive transport code and tests.
- Root and `test` modules replace every one of those 29 paths; `check-go-module-integrity.sh` passes.
- Native archives were **not rebuilt** in this window (no Chromium toolchain); the ABI claim rests on
  the byte-identical blobs and unchanged cgo/include surface, plus the real native naive self-tests.

## Why no row is "done with a note"

A dependency is only marked completed when the default branch of the fork is reachable, the main-repo
`replace` resolves to it in both root and `test`, and executable evidence exists. Rows that still fail
that bar are left OPEN with the concrete missing condition, not hidden behind a version bump.
