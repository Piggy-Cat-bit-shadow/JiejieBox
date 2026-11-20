# The WSL2 execution log (quadruple C correction)

This directory is the correction of an audit claim, not a product change. The quadruple windows
recorded several rows as `NOT_OBSERVABLE` "no Linux netns/TUN/nftables". That was wrong for this host:
WSL2 is installed, and as root it provides every one of those capabilities. Everything in this directory
was executed on it.

## Host capability, measured

```
$ wsl -l -v
  NAME            STATE    VERSION
* Ubuntu-24.04   Stopped  2
$ wsl --version
WSL version 2.7.14.0, kernel 6.18.33.2-2
$ wsl -d Ubuntu-24.04 -u root -- bash -c 'id; ip netns add p; ip netns list; ip netns del p'
uid=0(root) gid=0(root) groups=0(root)
p
$ ip tuntap add dev probe0 mode tun        # exit 0, link appears
$ nft add table inet probe                 # exit 0, table listed and deleted
$ curl -sI https://go.dev                  # HTTP/2 200
$ ls /usr/local/bin/xtool                  # xtool 1.24.0, with a full Swift/LLVM toolchain
```

`setup.sh` installs Go 1.27.4 (linux-amd64) and stages the repository on ext4.

## Linux-only test suites, executed

`linux-suites-1.sh` .. `linux-suites-3.sh`, all as root, all green (2026-11-20):

| Package | Time |
| --- | --- |
| `route` (incl. fd disposal, family-route enumerate/filter) | 443.8 s |
| `common/trafficsched` | 105.8 s |
| `transport/http` | 72.9 s |
| `transport/v2rayquic` (netns dual-path) | 12.6 s |
| `experimental/libbox` (incl. the redirect judge) | 11.4 s |
| `protocol/tun` (real device reset) | 4.9 s |
| `common/tlsspoof` (after `apt-get install tcpdump`) | 3.9 s |
| `common/listener` | 2.0 s |
| `common/netns` | 0.2 s |
| `protocol/bridge`, `common/process`, `common/ktls`, `common/netx`, `common/netcap` | < 0.5 s each |
| **`sing-tun` fork at `f23ebd6` (the exact lock pin)** | **262.4 s** |

## Q01 end to end: fake-IP × TUN × router × real socket

`q01-capture.sh` runs inside a private netns: sing-box with a TUN inbound (gVisor stack, auto_route), a
fake-IP DNS server, a route rule for `alpha.test` (direct, override to `127.0.0.1:18081`) and a reject
rule for `beta.test`, with an echo server as the witness.

```
resolve alpha.test -> 198.18.0.2        (fake-IP store)
dial 198.18.0.2:18080 -> "echo"         (TUN captured, domain restored, rule routed, real socket)
echo accept #1
resolve beta.test  -> 198.18.0.3
dial 198.18.0.3:18080 -> connection refused   (reject rule)
echo accepts: 1                          (no second socket)
```

## The retired-generation arms

`q01-restart-same-range.sh`: after a **graceful** restart with the same range and cache file, dialling
the old placeholder **without re-resolving** is restored from the durable ledger and routed
(`inbound connection to 198.18.0.2:18080` -> `outbound connection to 127.0.0.1:18081`, echo accept #2).

`q01-retired-range.sh`: after a restart with a **different** range over the same cache file, the old
placeholder is refused and the echo sees no connection
(`router: missing fakeip record, try enable experimental.cache_file`).

## The harness error this exposed, recorded deliberately

The first version of the retirement arm killed sing-box **0.3 s** after the dial. The cache file writes
its fake-IP mappings asynchronously, so the record was still in the pending batch when the process was
terminated, and the next start legitimately found no record. The result looked like a durability defect;
it was the harness racing the writer. With a 3 s settle the placeholder is restored. Any future attempt
at this arm must wait past the flush window, and the log line "missing fakeip record, try enable
`experimental.cache_file`" is logged when the mapping is absent for any reason - including a retired
range - so it must not be read as "cache_file is disabled".

## Prerequisites

- WSL2 with a recent kernel, run as root (`wsl -d Ubuntu-24.04 -u root`);
- a Linux Go toolchain (the setup script installs 1.27.4). The client is stored as `q01client/q01client.go.txt`: a `.go` file here would be a Go package outside the module's checked-in source roots, which `cmd/internal/modlayout` correctly refuses; copy it to a `.go` path outside the repository to build it, exactly as `q01-capture.sh` does;
- `tcpdump` for `common/tlsspoof` (`linux-suites-3.sh` installs it);
- nothing listens on 18080/18081 in the namespace, and the scripts delete the namespace on exit.
