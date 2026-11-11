# Failures, gaps and environment blocks

> Stage 1. Everything here is a fact about this host or this build at the
> baseline commit, with the command or path that shows it. A gap is not a
> silent skip: it is listed so stage 2 starts from a known state.

## Blocked

### B1 — XHTTP has no server transport in this fork

`transport/v2rayxhttp/register.go:22` calls only `v2ray.RegisterClient`, and
`transport/v2ray/transport.go:24` (`NewServerTransport`) has no `xhttp` case.
A VLESS XHTTP cell therefore needs an external Xray server binary.

Consequence: XHTTP is not in the default matrix. The existing
`test/xhttpbench/cmd/xnettool` remains the right harness for the A/B and needs an
Xray server plus — for CPU accounting — a Windows host
(`procstat_other.go` returns "implemented for Windows only").

This is an environment/product boundary, not a measurement result.

### B2 — Cronet cgo link fails on this host

```
/usr/bin/ld: skipping incompatible .../cronet-go/lib/linux_amd64@.../libcronet.a
             when searching for -l:libcronet.a
/usr/bin/ld: cannot find -l:libcronet.a
```

The archive is a valid x86-64 ar archive with ELF64 relocatable members, so the
mismatch is in the archive (or its index) as published, not in the build flags.
The purego binding loads the same `libcronet.so` at runtime and works, so the
Naive cells are measured; the cgo path is untested here.

### B3 — Go heap and native memory cannot be split from outside the process

The production binary exposes no profile endpoint (`option/experimental.go` has
cache, clash_api, v2ray_api and debug, but no pprof). `go_heap_peak_bytes` is
therefore `null` in every run. RSS and `VmHWM` are real but aggregate: for
Cronet they include the Chromium network stack, for QUIC they include quic-go's
buffers, and the lab cannot attribute them without a profile endpoint.

### B4 — No privileged network operations

`sudo -n true` fails (password required) and the lab never asks. Everything runs
inside an unprivileged user+network namespace, which is enough for veth, netem,
HTB and two namespaces. It is not enough for: host-wide `mpstat`/`perf`, a
system-level TUN device, `system=true` WireGuard/MASQUE endpoints, or capturing
on the host's physical interface.

Consequence: `system=false` is used for MASQUE and WireGuard. The privileged TUN
data path is untested in this stage.

### B5 — Tooling absent from the host

`iperf3`, `jq`, `pidstat`, `perf`, `sysbench` and `tcpdump` are not installed and
cannot be installed without egress to the distribution mirrors (which works) but
also without root for the package manager. The lab's own Go load generator and
`/proc` sampler replace them; there is no independent cross-check against
iperf3, and no hardware-counter CPU breakdown.

### B6 — Platform cells not run

macOS, Android, iOS, Windows and real-device TUN cells are out of scope for this
host. `docs/perf/ENVIRONMENT.md` lists which claim each platform would add.

## Gaps that are stage-2 work, not blocks

| gap | why it matters | where it starts |
|---|---|---|
| no qlog / packet capture | RTT, PTO, cwnd and loss are inferred from throughput; a congestion-control question cannot be answered | `sing-quic` qlog hooks, `udpshim` counters |
| no cold/warm handshake separation | `connect` measures a whole proxied connection, not DNS/TCP/TLS/Session phases | split the probe in `loadgen`, record per-phase |
| no per-stream fairness or tail | one aggregate goodput hides a starved stream | per-stream byte counters in `loadgen` |
| no A/B interleaving | a single-code baseline cannot attribute a change | add a two-binary mode to `run-baseline.sh`, alternate repetitions |
| no memory-per-connection sweep | `measure-client-baseline.sh` covers 100 held connections but not 1/10/100/1000 with the proxies running | port it to Linux and to a matrix cell |
| no reuse benefit measurement | `connect` and `upload` use different connection lifetimes | add an explicit `reuse` pattern with N requests per connection |
| no network-change cell | recovery is covered behaviourally, not under measurement | `runtimecoord` generation switch during load |
| no micro/e2e join | `allocs/op` is missing from e2e rows | `perflab bench` ingesting `go test -bench` output under the same `run_id` |

## Defects the laboratory found in itself

These are worth keeping because each one produced plausible numbers before it
was caught.

1. **One namespace, no shaping.** With both veth ends as local addresses in one
   namespace, the kernel's local route table short-circuits traffic through
   loopback. The qdiscs were installed, `tc` showed them, and the measured RTT
   stayed at 3.6 ms for an 80 ms profile. Throughput at a 100 Mbit/s cap came out
   at 1.36 Gbit/s. Fixed by a second namespace for the server side; every run now
   records the installed qdiscs (`network.qdiscs`) and the topology
   (`network.topology=netns-veth`).
2. **Unbounded writes.** A stalled tunnel made `loadgen` block in `Write`
   forever, so a broken cell looked like a slow one. Fixed with per-phase socket
   deadlines and a short `IOGrace` on the readiness probe.
3. **Port reservation in the wrong namespace.** Ports are per-namespace; the
   first shaped run asked the client namespace for the server's port and failed
   with `no free tcp+udp port on 10.213.0.2`. Fixed with a namespace-aware
   reservation (`perflab reserve` executed through `nsenter`).
4. **Template key generation read the wrong directory.** The REALITY keypair was
   only generated when a template was found relative to the process working
   directory. Fixed: both templates are scanned in the scenario directory before
   rendering, and an unresolved placeholder is a hard error.
5. **`sysfs` is not network-namespace aware.** `/sys/class/net` inside a new
   netns still lists the host's interfaces, so the veth statistics file did not
   exist and every shaped run recorded `wire: null` while its traffic really
   crossed the link. Fixed by reading the counters through `ip -j -s link`
   (netlink); `sysfs` remains only as a fallback for unshaped runs.
6. **A scenario field was not rendered.** `mieru-tcp/scenario.json` used
   `__TRANSPORT__` as its `transport` label; template substitution applies to the
   config templates, not to the scenario metadata, so the run recorded the
   placeholder. The label is fixed; the two baseline `run.json` files still carry
   the literal string because the data is not rewritten after the fact.

Each is recorded in the code comments at the point of the fix, not only here.
