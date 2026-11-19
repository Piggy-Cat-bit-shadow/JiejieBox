# Dependency vulnerabilities: 13 reachable at the release round, **all 13 fixed**, ClientHello unchanged

`govulncheck ./...` on 2026-11-19 found **13 vulnerabilities in 5 modules this code calls**, reachable
from `daemon`, `service/api`, `transport/v2raygrpc`, `transport/v2rayxhttp` and the HTTP transports. An
audit of this bundle pointed out that the first version of this page treated "upgrade" and "keep the TLS
fingerprint" as a choice. It is not a choice, and the audit was right: the current state is **0 reachable
vulnerabilities with the pre-1.26 ClientHello preserved**, verified by the probe.

```
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
No vulnerabilities found.
Your code is affected by 0 vulnerabilities.
```

Raw outputs: `govulncheck-after-upgrade.txt` (summary) and `govulncheck-after-upgrade-verbose.txt`
(the full per-finding list, including the 3 imported-package and 2 required-module findings this code
does not call).

## How both halves were satisfied

Raising the module's `go` line is unavoidable: the fixed `x/net` (v0.62.0) and `x/crypto` (v0.58.0)
declare `go 1.26.0` themselves, and a module cannot require one whose language version is higher than
its own. That line also selects Go's GODEBUG defaults, and `tlssecpmlkem` became on in 1.26 — it adds
`SecP256r1MLKEM768` and `SecP384r1MLKEM1024` to every ClientHello.

The fix is to pin that one default back in the main module's build context:

```
go 1.26.0
toolchain go1.27.4
godebug tlssecpmlkem=0
```

Measured with the repository's own `scripts/ci/clienthello-probe`, same toolchain (go1.27.4) throughout:

| `go` line / override | supported_groups | key_shares |
| --- | --- | --- |
| 1.25.5 (before this round) | `[4588 29 23 24 25]` | `[4588 29]` |
| 1.26.0, no override | `[4588 4587 4589 29 23 24 25]` | `[4588 29]` |
| **1.26.0 + `godebug tlssecpmlkem=0`** | **`[4588 29 23 24 25]`** | **`[4588 29]`** |

So the upgraded dependency set emits the same `supported_groups` and `key_share` as the release did
before. The audit's independent reproduction (environment `GODEBUG=tlssecpmlkem=0`, and `godebug` in
`go.mod`) matched these numbers.

## The thirteen

| # | Advisory | Module | Was → now | What it is |
| --- | --- | --- | --- | --- |
| 1 | GO-2026-6888 | `github.com/andybalholm/brotli` | v1.1.0 → **v1.2.7** | excessive memory when decompressing crafted streams |
| 2 | GO-2026-6865 | `github.com/klauspost/compress` | v1.19.1 → **v1.20.2** | decompression memory |
| 3 | GO-2026-6860 | `golang.org/x/net` | v0.58.0 → **v0.62.0** | HTTP/2 handling |
| 4 | GO-2026-6617 | `golang.org/x/net` | v0.58.0 → **v0.62.0** | HTTP/2 handling |
| 5 | GO-2026-6612 | `golang.org/x/net` | v0.58.0 → **v0.62.0** | HTTP/2 handling |
| 6 | GO-2026-6611 | `golang.org/x/net` | v0.58.0 → **v0.62.0** | excessive CPU from repeated initial-window changes — **a malicious HTTP/2 peer against client or server** |
| 7 | GO-2026-6610 | `golang.org/x/net` | v0.58.0 → **v0.62.0** | HTTP/2 handling |
| 8 | GO-2026-6603 | `golang.org/x/net` | v0.58.0 → **v0.62.0** | HTTP/2 handling |
| 9 | GO-2026-6443 | `google.golang.org/grpc` | v1.79.3 → **v1.83.2** | xDS/authorization |
| 10 | GO-2026-6355 | `golang.org/x/crypto` | v0.55.0 → **v0.58.0** | **SSH** message causes the connection to deadlock (`ssh.NewClientConn` path) |
| 11 | GO-2026-6354 | `golang.org/x/crypto` | v0.55.0 → **v0.58.0** | **SSH** message handling |
| 12 | GO-2026-6348 | `google.golang.org/grpc` | v1.79.3 → **v1.83.2** | heap OOM via HTTP/2 DATA-frame fragmentation |
| 13 | GO-2026-6061 | `google.golang.org/grpc` | v1.79.3 → **v1.83.2** | xDS RBAC and HTTP/2 server |

Corrections against the earlier draft of this page, all from the audit: the two `x/crypto` advisories are
**SSH** paths, not "crypto primitives"; `GO-2026-6611` affects a malicious peer against **client or
server**, so the `x/net` set was not purely server-side; and the `x/net` advisories are not one issue —
each is its own advisory, which is why they are listed individually here rather than as "#3/#4".

## Verification at the final state

| Check | Result |
| --- | --- |
| `go build ./...` | exit 0 |
| `go vet` on the grpc/compression consumers | exit 0 |
| shipped-profile tests over 21 touched packages | all green — `release-profile-tests-after-upgrade.txt` (root 28.9 s, daemon 45.4 s, trusttunnel 53.5 s) |
| `go run ./scripts/ci/clienthello-probe` | `groups=[4588 29 23 24 25] key_shares=[4588 29]` — identical to the pre-upgrade release |
| `python scripts/ci/release-freeze.py verify --lock release/source-lock.json` | PASS — none of these modules is one of the ten pinned fork revisions |
| `govulncheck ./...` | **0 reachable** |
| `go test -race ./transport/trusttunnel` | ok 35.4 s (the historical hang did not reproduce; see `trusttunnel-race-full.txt`) |

## What is still not proven about the handshake

The probe measures the default `crypto/tls` client's `supported_groups` and `key_share`. It does not
measure the whole ClientHello (ALPN, extension order, GREASE), and it does not exercise the real
outbound paths. `common/tls/client.go` selects between the Apple/Windows system engine, REALITY, uTLS
(metacubex/utls) and stdlib TLS depending on configuration, and Naive has its own Cronet engine — a
build tag proves the capability exists, not that a given connection uses it. The audit's point stands:
after this upgrade, the actual handshake of each client artifact should be captured against a controlled
server and compared structurally, with random fields excluded. That needs the device/Apple toolchain
this environment does not have, so it is listed as an artifact-level follow-up rather than claimed.

Scope of `godebug`: it is the **main module's** build context. The CLI, the daemon and gomobile/libbox
built from this module inherit it. An embedder that builds its own main module (or a client repository
that vendors these packages under its own `go.mod`) does **not** — it needs the same `godebug` line, or
an explicit `tls.Config.CurvePreferences`, of its own. That is recorded here because the audit asked for
it to be, and because a silent fingerprint change in a client repo would be exactly the class of bug
this session kept finding.
