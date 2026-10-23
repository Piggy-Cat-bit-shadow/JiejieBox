# Upstream dependency integration — 2026-10-24

**Scope.** Every module whose version moved between the fork's effective pins and
`SagerNet/sing-box:testing` at `824dbcfb4dd0a95921840b1737b950cde590241e` (2026-10-23 22:39 +0800).
This is the dependency ledger required by the 2026-10-24 upstream task, not a `go.sum` refresh.

**The fork's `require` lines are not its effective versions.** Five modules are redirected by
`replace` to `Piggy-Cat-bit-shadow/*` forks, and the `require` version is only the upstream pin the
fork forked from. Every row below names the *effective* module (`go list -m`), not the require line.

## Effective pins

| module | upstream effective | fork effective | delta | classification |
|---|---|---|---|---|
| `github.com/sagernet/sing-tun` | `v0.9.7-0.20261023115453-611d83b7a57b` | `Piggy/sing-tun v0.0.0-20261020085711-109186a15f3b` | 3 days, includes the flow-table/system-stack retention fixes (`03ab009ea1c`, `cc2f91b77a6`) and `WaitWritePackets` | **BLOCKED_WITH_EVIDENCE** — the Piggy fork must merge upstream first; `3454d6d2411` also needs the new wait API |
| `github.com/sagernet/sing` | `v0.9.7-0.20261023115510-03a6cce64bf7` | `Piggy/sing v0.9.6-0.20261019051552-baed8c3b4a34` | 4 days, includes the pause/default-route/namespace work the fork still lacks | **BLOCKED_WITH_EVIDENCE** — the fork's `pause.Manager` still exposes only `WaitActive`; `cc022098fdec` needs the context-aware wait first |
| `github.com/sagernet/sing-quic` | `v0.7.2-0.20261020082049-7e0a5869a5f4` | `Piggy/sing-quic v0.7.2-0.20261019060901-d0deb0359c06` | 1 day | **BLOCKED_WITH_EVIDENCE** — fork merge required; no API change identified yet |
| `github.com/sagernet/quic-go` | `v0.61.0-sing-box-mod.12` | `Piggy/quic-go v0.61.1-0.20261019061042-7e08ecaba383` | upstream tag `.12` vs a fork pseudo-version; the fork is not a descendant of the tag | **BLOCKED_WITH_EVIDENCE** — the fork carries the buffer-pinning/race changes; rebasing onto `.12` is a fork-side operation |
| `github.com/sagernet/cronet-go` (+ `all`, 31 `lib/*`) | `v0.0.0-20261023060337-b3c2ed82ac97`; libs `v0.0.0-20261023055542-d5437a0df01a` | `v0.0.1-143.0.7499.109-2.0.20261020055023-fd2f25825e4f`; libs `v0.0.0-20260929202119-8c68ce89873c` | root 3 days, libs 24 days | **BLOCKED_WITH_EVIDENCE** — native archives and Go bindings must move in one batch; no macOS/iOS device here to verify ABI or TestFlight |
| `github.com/sagernet/tailscale` | `v1.102.1-sing-box-1.14-mod.5.0.20261022222424-1688b52f8204` | `…20260925112514-35e61219dedd` | 27 days | **BLOCKED_WITH_EVIDENCE** — `1cc885e0400e` needs `netmon.SetDefaultRouteFunc`, absent from the old module; code change precedes the bump |
| `github.com/sagernet/sing-cloudflared` | `v0.1.4-0.20261021152903-2d40d788fcf6` | **same** | none | **SYNCED_UPSTREAM** — `go get`, then `go build ./protocol/cloudflare` and the package tests pass |
| `github.com/sagernet/sing-openvpn` | `v0.1.1-0.20261020061844-7d24ecd92837` | **same** | none | **SYNCED_UPSTREAM** — `go get`, then `go build ./protocol/openvpn` and `go test ./protocol/openvpn` pass |
| `github.com/sagernet/sing-mux` | `v0.3.10-0.20260929204512-caf09fe32475` | same | none | **FORK_EQUIVALENT_OR_BETTER** — already aligned |
| Go directive | `go 1.25.5` | `go 1.25.5` | none | **SYNCED_UPSTREAM** — `2ca6ab970f34` updates the CI toolchain, not the module directive |

The two synced modules were actually downloaded, compiled and tested; the pins above are the
resulting `go.mod` state, not a plan. Everything marked `BLOCKED_WITH_EVIDENCE` is a fork-repository
operation: upstream's changes must be merged into the `Piggy-Cat-bit-shadow/*` fork, the fork's own
patches (Go TUN stack, buffer pinning, race fixes) preserved, and only then may the main-repo
`replace` move. Doing it by editing the `replace` line alone would either fail to resolve or silently
drop the fork's own fixes, which is the "伪升级" the brief forbids.

## Why the blocked rows are not "done with a note"

- `sing-tun` carries the actual implementation of `03ab009ea1c` (TUN flow table retaining accepted TCP)
  and `cc2f91b77a6` (system stack retaining closed TCP sessions). The sing-box commits are one-line
  pin bumps; nothing in the main tree fixes either defect. The fork's current pin `109186a` predates
  both.
- `sing` carries `cc022098fdec`'s pause API and `1cc885e0400e`'s `netmon` hook. Neither exists in
  `baed8c3b4a34`.
- `cronet-go`'s native libraries are ABI-coupled to the Go bindings; moving only the root module is
  the exact failure the brief names. The fork's lib pins are from 2026-09-29.

## Reproduce

```sh
git show upstream/testing:go.mod | grep -E 'sing-tun|sing-quic|quic-go|sagernet/sing |cronet-go|tailscale'
git -C C:/Deepseek/integration-release-next diff -- go.mod
```

The fork's `go.sum` diff for this round contains only the two synced modules and their transitive
dependencies; `go mod verify` and `go build -tags <DEFAULT_BUILD_TAGS_OTHERS> ./...` pass.

## Residual risk

Every `BLOCKED_WITH_EVIDENCE` row is a real missing fix, not a cosmetic lag. In particular the two
TUN retention fixes and the WireGuard pause fix remain absent until the fork repositories move.
This round therefore cannot claim "dependencies are current"; it claims two synced, one already
aligned, and six blocked with a named upstream commit each.
