# Eight-protocol performance asset inventory

> Stage 1 inventory. Every path below was re-read at the baseline commit
> recorded in `docs/perf/BASELINE.md`; nothing here is carried over from an
> older report. The right-hand column says what the laboratory added, not what
> the repository already had.

## How to read this table

- **Existing measurement** means a `Benchmark*` function, a load harness, or a
  script that moves bytes and records a number.
- **In-process** means the protocol object is driven directly with a fake or
  loopback wire. It answers "what does this function cost", not "what does this
  protocol cost".
- **Gap** is the difference between what exists and stage 1's question: real
  kernels, real sockets, CPU/GiB, concurrency, weak networks.

## Summary

| protocol | existing measurement | strongest asset | stage-1 gap | lab scenario |
|---|---|---|---|---|
| MASQUE / CONNECT-IP | in-process dataplane/ingress/route benches; a VPS acceptance script with a `--stress` RSS sample | `test/masque_test.go` starts two real box instances | no real-QUIC throughput/PPS, no CPU/GiB, no netem | `masque-connect-ip` |
| VLESS XHTTP | `test/xhttpbench/cmd/xnettool` (real A/B vs Xray, CPU, integrity, impairments) + 4 in-process benches | `xnettool` | fork has **no XHTTP server transport**; external Xray server required; CPU accounting is Windows-only | not in the default matrix (see below) |
| VLESS REALITY / Vision | none for throughput; correctness and ABI tests only | `test/reality_test.go`, `test/interop` REALITY matrix | no cold/warm handshake or steady-state numbers | `vless-reality-vision` |
| AnyTLS | admission-cost micro benches, session burst/census/soak pressure tests | `protocol/anytls/session_burst_test.go` | no throughput, no latency, no UoT rate | `anytls-tcp` |
| Naive / Cronet | two Darwin-only A/B shell scripts + in-process codec and H2 benches | `test/jiejie/naive-client-it` (manual, official client) | no Linux-capable end-to-end harness | `naive-h2`, `naive-h3` |
| Mieru | complete PowerShell e2e harness (`scripts/mieru-bench`), official-SDK comparison | `scripts/mieru-bench/` | Windows-only, loopback-only, fixed concurrency | `mieru-tcp`, `mieru-udp` |
| TrustTunnel | **none** | `transport/trusttunnel` server tests with real listeners | no throughput, CPU, PPS or netem | `trusttunnel-h2`, `trusttunnel-h3` |
| AWG / WireGuard | packet-framing byte measurement on real devices; MTU budget tests | `transport/wireguard/wg_framing_measurement_test.go` | no throughput, PPS, CPU or weak-network cell | `wireguard-endpoint`, `awg-endpoint` |

## Protocol detail

### MASQUE / CONNECT-IP

Existing: `transport/masque/dataplane_bench_test.go` (17 benchmarks driving the
real client dataplane against a fake datagram sink), `ingress_bench_test.go`,
`route_bench_test.go`, `config_bench_test.go`; `protocol/masque/*_bench_test.go`
for bootstrap and inner resolver. The only load-bearing end-to-end asset is
`scripts/acceptance/jiejie-masque-vps.sh`, which needs a VPS and samples
`VmRSS/VmPeak/Threads/fds` only in its `--stress` section.

Gap: every benchmark fakes the wire, so none of them measures QUIC, congestion
control, DATAGRAM batching or the userspace IP stack. There is no PPS number,
no CPU per byte, and no loss/latency cell.

Lab: `masque-connect-ip` runs a `masque-server` endpoint and a `masque-client`
endpoint as separate processes over real QUIC, `system=false` so no TUN or root
is needed. TCP patterns cross the tunnel as IP packets; the `udp` pattern is
QUIC DATAGRAM with capsule fallback. The UDP cell is never compared with a
reliable-stream UDP cell.

### VLESS XHTTP

Existing: `test/xhttpbench/cmd/xnettool` is a real A/B stand (fork client vs
reference Xray client against one Xray server) with throughput, ping-pong,
connect latency, duplex, payload integrity, per-process CPU on Windows, and an
application-layer impairment shim. `transport/v2rayxhttp/xhttp_bench_test.go`
has four in-process uplink benches that explicitly exclude TLS, HTTP/2 and the
kernel.

Gap: the fork registers **only a client transport** (`transport/v2rayxhttp/register.go`
calls `v2ray.RegisterClient`; `v2ray.NewServerTransport` has no `xhttp` case), so
an XHTTP cell needs an external Xray server binary. `xnettool`'s CPU accounting
is `procstat_windows.go`-only (`procstat_other.go` returns "implemented for
Windows only"), and its impairments are application-layer: loss and H3
impairment are declared blocked by its own README.

Stage 1 decision: the XHTTP cell is **not** in the default matrix, because a
cell that needs an external reference server is a different experiment from the
in-repo process pairs, and the stage-1 acceptance criteria are already met by
in-repo H2/H3 cells. `xnettool` is the right tool for the XHTTP A/B and is left
intact; porting its CPU accounting to Linux is stage-2 work. This is recorded as
a gap, not silently skipped.

### VLESS REALITY / Vision

Existing: correctness and compatibility only — `test/reality_test.go` (needs
`google.com:443` as the camouflage target), the REALITY matrix in `test/interop`,
`common/tls/reality_*_test.go` for ClientHello/SessionID/ShortID/key-share
behaviour, and `protocol/vless/encryption/vision_*_test.go` for the Vision ABI
and tripwire. There is no cold/warm handshake measurement and no steady-state
data path measurement.

Lab: `vless-reality-vision` starts a local TLS cover server with the run's own
certificate as the REALITY handshake target, so no public site is needed and the
camouflage path is exercised with a real certificate. The runner generates the
REALITY keypair with the binary under test (`sing-box generate reality-keypair`),
not from a fixture. Compare against `vless-vision-tcp`: same payload, same TLS
version, the only difference is REALITY.

### AnyTLS

Existing: `protocol/anytls/admission_test.go` and `admission_attack_test.go`
measure admission-guard cost against a fake dialer; `session_burst_test.go`,
`tls_amplification_test.go`, `soak_cycles_test.go`, `stall_attack_test.go` and
`burst_matrix_test.go` are pressure tests with a real TLS listener but no
sustained load. `session_burst_test.go:284` is a heap-per-session census.

Gap: no throughput or latency measurement at all, and no UoT rate test.

Lab: `anytls-tcp` measures upload/download/connect and the `udp` pattern over
AnyTLS UoT on the same stream. The UoT numbers are labelled as UDP-over-Stream;
they are not a datagram transport and are not compared with MASQUE.

### Naive / Cronet

Existing: `scripts/ci/bench-naive-cronet-engine.sh` (engine-count A/B: RSS,
threads, FDs, startup, first request, 1/4/8/16 streams, CPU) and
`bench-naive-receive-window.sh`. Both refuse to run off Darwin and both need a
real external Naive server. `protocol/naive/bench_test.go` and
`bench_h2_test.go` are codec/H2 in-process benches.

Gap: no Linux-capable end-to-end harness; the native engine's threads and RSS
were never measured on this side.

Lab: `naive-h2` and `naive-h3` pair the in-repo Naive inbound with the
Cronet-backed outbound. On Linux the outbound is built with `with_purego`: the
`linux_amd64` module ships `libcronet.a`, `libcronet.so` and a purego binding
(`libcronet_purego.go`), so the shared library is loaded from the binary's
directory and no cgo link step is needed. `CGO: disabled` is recorded in the run.
This is the native-dependency scenario the stage asks for: the engine's RSS,
thread count, FDs and startup are in the same record as its goodput.

The cgo path (`-l:libcronet.a`) does not link on this host: GNU ld reports the
in-tree archive as incompatible. That is recorded as an environment finding, not
worked around by disabling checks.

### Mieru

Existing: `scripts/mieru-bench/` is the only complete end-to-end harness in the
repository — a real official-SDK server (`server/main.go`) and a shaped client
(`client/main.go`) driven by `run.ps1`, with CPU seconds, working set,
CPU s/GiB, five modes and three scenarios. `protocol/mieru/bench_test.go`
measures 1200-byte UoT framing against the official SDK.

Gap: PowerShell/Windows only, loopback only, fixed concurrency; the framing
bench is not a data path.

Lab: `mieru-tcp` and `mieru-udp` run the in-repo `mieru` inbound (which embeds
the official mieru server SDK) against the in-repo outbound, on a real TCP or
UDP underlay. The PowerShell harness is left in place; porting its official-client
comparison to Linux is stage-2 work.

### TrustTunnel

Existing: **no benchmark of any kind.** `transport/trusttunnel/service_test.go`
runs real H2 and H3 listeners for behavioural assertions; the UDP tests count
associations and queued bytes; `owner_stress_test.go` asserts leak-freedom under
concurrent dial/close.

Gap: no throughput, CPU, PPS or latency number anywhere.

Lab: `trusttunnel-h2` and `trusttunnel-h3` are the H2 and H3 representatives in
the matrix. The `udp` pattern is TrustTunnel's UDP-over-HTTP-stream framing — a
reliable stream carrying datagrams, which may head-of-line block. It is grouped
and labelled separately from QUIC DATAGRAM.

Build note: the inbound is gated by `with_trusttunnel`, which is **not** in any
release tag profile; the lab binary adds it explicitly.

### AWG / WireGuard

Existing: `transport/wireguard/wg_framing_measurement_test.go` measures the
32-byte fixed overhead and 16-byte padding granularity with two real
wireguard-go devices and a real loopback UDP socket; `mtu_budget_test.go` and
`nested_mtu_test.go` check the MTU arithmetic. AWG has behavioural tests only
(`endpoint_awg_carrier_test.go`, `awg_reserved_test.go`,
`client_bind_awg_transparency_test.go`).

Gap: no throughput, PPS, CPU or weak-network cell for either protocol.

Lab: `wireguard-endpoint` and `awg-endpoint` use two userspace endpoints
(`system=false`, no TUN, no root) with a synthetic `198.18.17.0/24` tunnel
address pair. The client rewrites the SOCKS destination to the tunnel address
and the server rewrites it back to the lab target, so the encrypted tunnel — not
a loopback shortcut — is what is measured. The AWG cell is identical except for
the obfuscation parameters, which makes the pair a controlled comparison.

Build note: AWG needs `with_awg` **and** `with_wireguard`; the former is absent
from every release profile. `system=true` needs `/dev/net/tun` and CAP_NET_ADMIN
and is not used by the lab.

## Assets deliberately not reused

- `test/xhttpbench/cmd/xnettool`: excellent, but needs an external Xray server
  and has Windows-only CPU accounting. Left intact.
- `scripts/mieru-bench/*.ps1`: Windows orchestration. The Go client/server pair
  is portable and is the reference implementation the stage-2 port should use.
- `scripts/ci/bench-naive-*.sh`: Darwin-only by construction.
- `scripts/acceptance/jiejie-masque-vps.sh`: needs a public VPS; the lab is
  local-only by design.
