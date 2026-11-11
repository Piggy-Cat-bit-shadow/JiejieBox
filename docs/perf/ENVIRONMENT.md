# Environment: what was verified, and what could not be

Baseline commit and branch are recorded in `docs/perf/BASELINE.md`. This file
records the machine, the toolchain and every capability the laboratory relies
on, with the command that proves it.

## Code and module state

| item | value |
|---|---|
| repository | `Piggy-Cat-bit-shadow/JiejieBox` |
| base branch | `origin/integration/0.1.45-final` (equal to `origin/testing` at fetch time) |
| base commit | `4ec0decd9f2dd7e3116ab130b3af56291f73988b` |
| work branch | `work/perf-lab-2026-11-11` |
| worktree | `C:\Deepseek\_perf\kernel` (independent worktree; the original checkout was not touched) |
| worktree state | clean at branch creation; the lab is the only addition |

Module pins that change protocol behaviour or performance (read from `go.mod`):

| module | pin |
|---|---|
| `github.com/sagernet/sing` | `v0.9.7-0.20261110095940-27b73774b240` (fork `Piggy-Cat-bit-shadow/sing`, 2026-11-10) |
| `github.com/sagernet/quic-go` | `v0.61.0-sing-box-mod.13` (fork, 2026-11-10) |
| `github.com/sagernet/sing-quic` | `v0.7.2-0.20261109110824-5253224b67cf` (fork, `jiejie-masque-mod9`) |
| `github.com/sagernet/sing-tun` | `v0.9.7-0.20261109145802-79ed0653fdc6` (fork, 2026-11-10) |
| `github.com/enfein/mieru/v3` | `v3.39.0` |
| `github.com/sagernet/cronet-go` | `v0.0.1-143.0.7499.109-2.0.20261105062326-362e63b14ce2` (fork), Cronet library version `154.0.8037.49` |

## Host

| item | value |
|---|---|
| platform | WSL2, Ubuntu 24.04.5 LTS |
| kernel | `6.18.33.2-microsoft-standard-WSL2` (x86_64) |
| CPU | AMD Ryzen 7 4800H, 16 vCPU visible |
| memory | 7.5 GiB |
| glibc | 2.39 |
| filesystem | source on `drvfs` (`/mnt/c`), module cache and build cache on ext4 |

WSL2 is a VM: loopback numbers are not bare-metal numbers, and the vCPU count is
a host-visible topology rather than a dedicated allocation. Every run records
the host section so a cell is never compared with a cell from another machine by
accident.

## Toolchain

| item | value |
|---|---|
| Go | `go1.25.5 linux/amd64` (matches `go 1.25.5` in `go.mod`; CI uses `go-version-file: go.mod`) |
| CGO | enabled for the default lab binary, disabled for the purego Naive binary — recorded per run |
| CC | gcc 13.3.0 (Ubuntu 13.3.0-6ubuntu2~24.04.1) |
| module cache | 4.0 GB warm cache; cold build of `./cmd/sing-box` took 2m02s, warm build 0m29s |

Build profiles:

| profile | tags | used for |
|---|---|---|
| default (`release/DEFAULT_BUILD_TAGS_OTHERS`) | `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname` | VLESS, AnyTLS, Mieru |
| lab | default + `with_trusttunnel,with_awg` | TrustTunnel, WireGuard, AWG |
| lab purego | lab + `with_naive_outbound,with_purego`, `CGO_ENABLED=0` | Naive, and the matrix binary |

`release/DEFAULT_BUILD_TAGS` is the Darwin profile (it carries
`with_naive_outbound` but not `with_trusttunnel`/`with_awg`); it was not used as
a Linux recipe.

## Capability verification

### User and network namespaces (required by `netlab`)

```
$ unshare --user --map-root-user --net --mount --fork -- bash -c 'id -u; ip -o link show'
0
1: lo: <LOOPBACK> ...
```

Unprivileged user namespaces are enabled on this WSL2 kernel. No sudo is
required, and `sudo -n true` correctly fails — the lab never asks for root.

### Two namespaces joined by a veth, with `tc netem`

```
$ unshare --user --map-root-user --net --mount --fork -- bash nsprobe.sh
none   udp echo rtt ms: min=0.1  p50=0.1   max=1.7
client udp echo rtt ms: min=20.3 p50=20.4  max=41.5     # netem on one end
server udp echo rtt ms: min=20.3 p50=20.4  max=41.2
both   udp echo rtt ms: min=40.4 p50=40.5  max=81.7     # netem on both ends
```

This confirms both that `netem` works and what its unit means: one `delay 20ms`
qdisc adds 20 ms per crossing, so a profile's RTT is split in half and applied to
each direction.

The same probe was used to catch a real defect: with **both veth ends in one
namespace**, the kernel's local route table delivers traffic through loopback
and the qdiscs never see it (RTT stayed at 3.6 ms for an 80 ms profile). The
harness now creates a second namespace for the server side, and every shaped run
records the installed qdiscs in `run.json`.

### HTB rate limiting

A 100 Mbit/s HTB class with a netem child limits a VLESS upload to
~12.5 MiB/s (≈105 Mbit/s on the wire including framing), against ~160 MiB/s
unshaped. `tc` warns `quantum of class 10001 is big`; the rate still holds, and
the warning is recorded as an artefact of the class parameters rather than
hidden.

### Cronet on Linux amd64

The pinned `cronet-go` fork publishes a `lib/linux_amd64` module containing
`libcronet.a` (60 MB), `libcronet.so` (12 MB), a cgo binding
(`libcronet_cgo.go`, gated on `linux && !android && amd64 && !with_musl && !with_purego`)
and a purego binding (`libcronet_purego.go`, `with_purego`).

- **purego build: works.** `CGO_ENABLED=0 go build -tags "...,with_naive_outbound,with_purego"`
  succeeds. `libcronet.so` is copied next to the binary; the purego loader
  searches the executable's directory first
  (`internal/cronet/loader_unix.go:findLibrary`). A Naive H2 upload completes at
  107.6 MiB/s and an H3 upload at 56.1 MiB/s, so the native engine is really
  running.
- **cgo build: does not link on this host.** GNU ld reports
  `skipping incompatible .../libcronet.a` although the archive is a valid x86-64
  ar archive with ELF64 relocatable members. The purego path is the supported
  one here; the failure is recorded rather than worked around.

### AWG

`transport/wireguard/device_awg.go` is gated by `with_awg`, which no release
profile contains, and the endpoint is only registered by `with_wireguard`. The
lab binary carries both. An AWG upload at the same load as plain WireGuard
completes (46.3 vs 46.7 MiB/s on loopback), so the graft is active; the
obfuscation cost is expected to appear under loss and MTU pressure, which is
stage-2 work.

## Tools that are absent

| tool | state | consequence |
|---|---|---|
| `iperf3` | not installed | the lab uses its own Go load generator; no cross-check against iperf3 yet |
| `jq` | not installed | scripts use `sed`/`python3`; JSON is parsed by Go or Python |
| `perf`, `pidstat` | not installed | no hardware counters or per-thread CPU; CPU is process-level |
| `tcpdump`/`tshark` | not installed | wire accounting uses veth statistics, not capture |
| `qlog` | not enabled | no RTT/PTO/cwnd extraction yet (stage 2) |

## What must run elsewhere

| target | why | what is blocked here |
|---|---|---|
| macOS | Cronet engine-count A/B, `bench-naive-*.sh`, Apple TLS engine benches, `common/tls` apple benchmarks | native engine memory/threads on Darwin, Apple TLS data path |
| Android / iOS | libbox, Cronet mobile, mobile memory pressure | per-device RSS and battery-relevant costs |
| Windows | the shipping desktop client; `xnettool` CPU accounting | Windows process CPU and Winsock costs |
| Real device with TUN | `system=true` WireGuard/MASQUE, route/idle-cleanup accounting | privileged TUN data path, on-demand tunnels |

Everything in the matrix that is marked as measured was measured on this WSL2
host, with the binary and tags recorded in the run.
