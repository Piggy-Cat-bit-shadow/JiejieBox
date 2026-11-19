# Dependency vulnerabilities: 13 reachable at the release round, 5 fixed, 8 blocked by a measured trade-off

`govulncheck ./...` run on 2026-11-19 at `eb6f777d7` reported **13 vulnerabilities in 5 modules that this
code calls**. This page records what was done about each and why the remainder is a decision rather than
an oversight.

## The scan

```
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
Your code is affected by 13 vulnerabilities from 5 modules.
This scan also found 5 vulnerabilities in packages you import and 6 in modules you require,
but your code doesn't appear to call these.
```

## Fixed in this round (`eb6f777d7` → the commit after it)

Every one of these is reachable from `daemon`, `service/api`, `transport/v2raygrpc`,
`transport/v2rayxhttp` or the HTTP transports, and every one was bumped to the fixed release **without
moving the module's `go` line**:

| # | Module | Was | Now | Advisory |
| --- | --- | --- | --- | --- |
| 1 | `github.com/andybalholm/brotli` | v1.1.0 | **v1.2.7** | GO-2026-6888 — excessive memory when decompressing crafted streams |
| 2 | `github.com/klauspost/compress` | v1.19.1 | **v1.20.2** | GO-2026-6865 |
| 9 | `google.golang.org/grpc` | v1.79.3 | **v1.83.2** | GO-2026-6443 |
| 12 | `google.golang.org/grpc` | v1.79.3 | **v1.83.2** | GO-2026-6348 — heap OOM via HTTP/2 DATA frame fragmentation |
| 13 | `google.golang.org/grpc` | v1.79.3 | **v1.83.2** | GO-2026-6061 — xDS RBAC and HTTP/2 server |

`google.golang.org/genproto/googleapis/rpc` moved as grpc's own requirement, and grpc's requirements moved
`golang.org/x/net` 0.57.0 → 0.58.0 and `golang.org/x/crypto` 0.54.0 → 0.55.0 — still below their fixed
releases, so those eight advisories remain (below). `release-freeze.py verify` still passes: those modules
are not among the ten revisions the source lock pins, so no fork pin moved.

The rescan after the bump reports **8 vulnerabilities from 2 modules**, down from 13 from 5.

Verification: `go build ./...`, `go vet` on the grpc and compression consumers, `release-freeze.py verify`
PASS, the shipped-profile tests over `. ./daemon ./service/api ./transport/v2raygrpc
./transport/v2rayxhttp ./transport/v2rayhttp ./common/httpclient ./protocol/masque ./protocol/vless
./protocol/vmess` (root 27.5 s, daemon 43.8 s, xhttp 22.4 s — all green), and the ClientHello probe run
again after the bump:

```
go run ./scripts/ci/clienthello-probe
go=go1.27.4 groups=[4588 29 23 24 25] key_shares=[4588 29]      <- unchanged by the dependency bump
```

## Blocked: the eight `golang.org/x/*` advisories

| # | Module | Was | Fixed in | Blocked because |
| --- | --- | --- | --- | --- |
| 3 | `golang.org/x/net` | v0.58.0 | v0.62.0 | that release's `go.mod` says **`go 1.26.0`** |
| 4 | `golang.org/x/net` | v0.58.0 | v0.60.0 | same |
| 5 | `golang.org/x/net` | v0.58.0 | v0.60.0 | same |
| 6 | `golang.org/x/net` | v0.58.0 | v0.60.0 | GO-2026-6611, excessive CPU from repeated HTTP/2 initial-window changes — same |
| 7 | `golang.org/x/net` | v0.58.0 | v0.60.0 | same |
| 8 | `golang.org/x/net` | v0.58.0 | v0.60.0 | same |
| 10 | `golang.org/x/crypto` | v0.55.0 | v0.56.0 | that release's `go.mod` says **`go 1.26.0`** |
| 11 | `golang.org/x/crypto` | v0.55.0 | v0.56.0 | same |

A Go module whose `go.mod` declares a higher `go` version may only be required by a module whose own `go`
line is at least that high: `go get`/`go mod tidy` therefore **rewrites this module's `go` line to
1.26.0**. That line is not cosmetic here. It is what selects Go's GODEBUG defaults, and two of them
change the TLS ClientHello this product uses for camouflage.

**Measured today, in this repository, with the toolchain that ships (go1.27.4)**, using
`scripts/ci/clienthello-probe`:

```
go 1.25.5, toolchain go1.27.4:  groups=[4588 29 23 24 25]            (X25519MLKEM768, X25519, P-256/384/521)
go 1.26.0, toolchain go1.27.4:  groups=[4588 4587 4589 29 23 24 25]   (+ SecP256r1MLKEM768, SecP384r1MLKEM1024)
```

So raising the `go` line to fix those eight advisories **adds two key-exchange groups to every TLS
handshake the product makes** — a fingerprint change for a product whose camouflage is the point. That is
a product decision, not a debugging step, so it is recorded here instead of taken.

### What the owner needs to decide

1. **Stay on `go 1.25.5`** (current): five of thirteen fixed, the eight `x/*` advisories remain. Exposure
   depends on whether the affected code paths handle attacker-controlled input: the `x/net` HTTP/2 items
   are server-side window/frame handling that this product exposes through `service/api`'s web bridge and
   the clash API, and the `x/crypto` items are in primitives whose call sites would need checking.
2. **Move the `go` line to 1.26** and accept the ClientHello change: all thirteen fixed, and the group
   set becomes the probe's second line. If the camouflage is validated against a uTLS/forged profile
   (which does not use the stdlib group list), the actual exposure may be limited to whatever uses the
   stdlib `crypto/tls` — that is the question to answer before deciding, with
   `scripts/ci/clienthello-probe` and the REALITY/utls paths as the instruments.
3. **Vendor a targeted patch**: not recommended; the advisories are in the standard library's HTTP/2
   implementation and its crypto primitives, where a local patch is a fork of `x/net`.

The command that would perform option 2, for whoever decides it:

```
go get golang.org/x/net@v0.62.0 github.com/andybalholm/brotli@v1.2.7 \
       github.com/klauspost/compress@v1.20.2 google.golang.org/grpc@v1.83.1
go mod tidy        # this will raise `go 1.25.5` to `go 1.26.0`; do not revert that by hand afterwards
go run ./scripts/ci/clienthello-probe   # confirm the new group set is the intended one
```

## Also checked

`go mod verify` → *all modules verified* (412 modules in the graph). The four `x/*` modules' advisories do
not change the source-lock gate: the lock pins ten fork revisions, none of which is an `x/*` module.
