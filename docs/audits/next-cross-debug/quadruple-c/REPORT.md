# Quadruple C — autonomous combinations: the artifact the product ships

Third and last window of the four-system round, with no assigned numbering.

- Repository: `Piggy-Cat-bit-shadow/JiejieBox`, branch **`testing`**, no new branch, HEAD `4f65b1f4b`
- Platform: Windows amd64, Go **1.27.4**; no Administrator token, no Linux netns/nftables, no device
- Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` / `NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`

## 0. Result in one paragraph

One self-invented four-system check was executed and it passes: **the binary the release recipe builds
really sends the camouflage ClientHello** — `groups=[4588 29 23 24 25]`, measured on the wire through a
real SOCKS → Trojan TLS handshake, with a recorder standing where the Trojan server would be. The
control that makes it evidence rather than ceremony is a third build: the same source linked with the
short `-X runtime.godebugDefault` list that this session fixed two rounds ago sends
`[4588 4587 4589 29 23 24 25]` — the post-quantum groups the product's camouflage exists to avoid. The
probe is committed so the question can be re-asked after any build change. No production defect was
found; the other seven seeds are recorded with their exact blockers, two of them as `SOURCE_ONLY` after
checking the code rather than guessing.

## 1. X-Q06 — from a string diff to what a censor sees

The previous rounds established that `-X runtime.godebugDefault=...` **replaces** the go.mod-computed
default instead of merging into it, and that the two build paths (`release/LDFLAGS` and
`cmd/internal/build_shared`) had drifted (fixed in `e8a74e41f`). That was a measurement of **strings**.
This window asked the only question that matters for a camouflage product: *what does the shipped
binary actually put on the wire?*

**Method** (`scripts/ci/clienthello-artifact-probe`):

1. build `./cmd/sing-box` the way the release recipe does -
   `-tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -trimpath -ldflags "$(cat release/LDFLAGS) -s -w -buildid="`;
2. start it with a minimal config: a SOCKS inbound, a Trojan outbound with TLS pointed at a local
   recorder, `route.final` on the Trojan outbound;
3. drive one SOCKS CONNECT to `example.com:443`; the Trojan outbound dials the recorder over TLS;
4. the recorder reads the first TLS record and parses the supported_groups and key_share - the
   handshake never completes, because the ClientHello is the measurement.

**Measured (2026-11-20, windows/amd64, Go 1.27.4):**

| Build | LDFLAGS | On the wire |
| --- | --- | --- |
| release recipe | `$(cat release/LDFLAGS)` (full list) | `groups=[4588 29 23 24 25] key_shares=[4588 29]` |
| development | none (go.mod `godebug tlssecpmlkem=0`) | `groups=[4588 29 23 24 25] key_shares=[4588 29]` |
| **defect control** | the old `build_shared` short list | **`groups=[4588 4587 4589 29 23 24 25] key_shares=[4588 29]`** |

The third row is the whole point: same source, same tags, same probe - only the linker string differs,
and the wire changes. That is what makes the first row a measurement instead of a restatement of the
flags.

**Necessity of the four systems.** The build flags decide what the default is; the linker applies it;
the TLS stack builds the ClientHello; and the remote peer sees it. A module-level probe (the existing
`scripts/ci/clienthello-probe`) drops the artifact, and the artifact is the only thing that ships. This
window's probe is the only member of the family that observes the fourth.

**Six-stage check.**

1. *Loaded*: the binary is built with the release tag profile and the release LDFLAGS, i.e. the recipe
   `windows-core-amd64.yml` uses.
2. *Reached*: the handshake is driven through the product's own inbound and outbound (`socks` →
   `trojan`), not through a wrapper.
3. *Decided*: the GOEXPERIMENT/GODEBUG default decides the key-share list; the defect control shows
   precisely where the decision comes from.
4. *Acted*: the process opens a real TCP connection and writes a real ClientHello.
5. *Observed*: the groups parsed out of that record.
6. *Settled*: the child is killed and reaped, the temp config and log are removed.

**Not claimed.** ALPN was empty for both builds because the probe's Trojan outbound configures no ALPN -
that is the configuration, not a finding. The probe measures the Trojan TLS client; a Hysteria/QUIC
outbound's ClientHello is a different path and was not measured here.

## 2. The other seeds

| Seed | Verdict | Why |
| --- | --- | --- |
| X-Q01 certificate store × reload × REALITY × pool | `NOT_COVERED` | the store's reload ordering was fixed in the 2x2 round; REALITY does not take key material from the store, and no REALITY-side reload exists to combine |
| X-Q02 rule-provider download × selector × DNS bootstrap × epoch | `NOT_COVERED` | the self-dependency (a provider whose download is routed through the group it feeds) has to be configured first, and no fixture builds that cycle |
| X-Q03 SOCKS UDP × TProxy/TUN × NAT expiry × outbound UDP | `NOT_OBSERVABLE` | TProxy/TUN needs Linux + root |
| X-Q04 Mieru underlay × Guard × OOM × reset | `SOURCE_ONLY` | `protocol/mieru` implements no `IdleConnectionReleaser` (window A's R-3), so the reclaim walk cannot reach the SDK's underlays; the attack needs an SDK harness that was not built |
| X-Q05 netns × bind × dual-stack × QUIC retry | `NOT_OBSERVABLE` | netns is Linux-only |
| X-Q07 status × governance × cancel/close × provider refresh | `SOURCE_ONLY` | the read path was checked: `SelectionStatus`/`Revision` take no selection and advance no cursor (`selector.go:533-609`), the delay handlers cancel with the request, and the physicalpath report reads the same read-only contract. No side effect found |
| X-Q08 DNS64 × fake-IP restore × rule action × return path | `NOT_COVERED` | needs a DNS64 fabric; the return-path repairs are inherited |

## 3. The instrument, and why it is not in a workflow

`scripts/ci/clienthello-artifact-probe` is committed with the measured table in its doc comment. It is
not wired into a workflow on purpose: it needs a locally built binary and a loopback listener, and the
release recipes already measure their own output. What it adds over them is reproducibility by anyone
who has the binary, which is what an audit needs.

## 4. Gates

No production code changed. The probe builds and runs (`go vet` clean); the three binaries above were
built with the release tag profile. `release/source-lock.json` and `release/pin-runs.json` are
untouched.

## 7. Correction: the environment was NOT blocked (WSL2 was there all along)

The quadruple windows repeatedly recorded Linux-only rows as `NOT_OBSERVABLE` "no netns/TUN/nftables".
That was wrong for this host. WSL2 is installed (`Ubuntu-24.04`, kernel 6.18.33.2), and as `root` it
provides everything the rows needed. Measured:

```
wsl -d Ubuntu-24.04 -u root -- bash -c 'id; ip netns add p; ip netns list; ip netns del p'   -> uid=0(root), netns created
ip tuntap add dev probe0 mode tun                                                             -> exit 0
nft add table inet probe                                                                      -> exit 0
curl -sI https://go.dev                                                                       -> HTTP/2 200
/usr/local/bin/xtool                                                                          -> xtool 1.24.0 (+ Swift/LLVM toolchain)
```

Go 1.27.4 (linux-amd64) was installed and the repository staged on ext4; the harness and the full
execution log are in `linux/README.md`. What was executed green (as root):

| Area | Time |
| --- | --- |
| `route` (fd disposal, family-route enumerate/filter) | 443.8 s |
| `common/trafficsched` / `transport/http` / `transport/v2rayquic` (netns dual-path) | 105.8 / 72.9 / 12.6 s |
| `experimental/libbox` (redirect judge) / `protocol/tun` (real device reset) | 11.4 / 4.9 s |
| `common/tlsspoof` (after `apt-get install tcpdump`) | 3.9 s |
| `common/netns`, `common/listener`, `common/process`, `common/ktls`, `common/netx`, `common/netcap`, `protocol/bridge` | all green |
| **`sing-tun` fork at `f23ebd6` - the exact lock pin** | **262.4 s** |

**And Q01 was executed end to end**, inside a private netns, with real packets:

1. `alpha.test` resolved through the hijacked DNS to a fake address (`198.18.0.2`);
2. dialling `198.18.0.2:18080` was captured by the TUN, the fake address was restored to `alpha.test`,
   the route rule fired and the **direct outbound opened a real socket** to the override target - the
   echo server accepted exactly one connection;
3. `beta.test` (`198.18.0.3`) was refused by the reject rule and the echo never saw a second
   connection;
4. after a graceful restart with the **same** range and cache file, dialling the old placeholder
   **without re-resolving** was restored from the durable ledger and routed (accept #2);
5. after a restart with a **different** range over the same cache file, the old placeholder was
   **refused** and nothing accepted.

So the rows below change from `NOT_OBSERVABLE` to either `NO_DEFECT` (executed) or `NOT_COVERED`
(capability exists, the specific attack harness was not built):

| Row | Was | Is now | Why |
| --- | --- | --- | --- |
| quadruple-b Q01 (fake-IP x TUN x router x return path) | `NOT_OBSERVABLE` | **`NO_DEFECT`** | the capture path, the reject path and the retired-placeholder arms ran on real packets |
| quadruple-a Q03 (TUN UDP NAT) | `NOT_OBSERVABLE` | `NOT_COVERED` | TUN/netns exist and `protocol/tun` + the fork's suite ran green; the UDP-NAT end-to-end burst harness was not built |
| quadruple-a Q06 live TUN flow | `NOT_OBSERVABLE` | `NOT_COVERED` | a live TUN flow is now possible; the reload-during-flow attack was not built |
| quadruple-a X-Q03 TProxy / X-Q05 netns | `NOT_OBSERVABLE` | `NOT_COVERED` | netns works; the TProxy rule set and the netns x QUIC attack were not built (the existing `transport/v2rayquic` netns suite is green) |
| quadruple-b X-Q08 DNS64 | `NOT_OBSERVABLE` | `NOT_COVERED` | a DNS64 fabric is buildable here (TUN + a NAT64 service), not built |
| quadruple-a Q08 iOS NE | `NOT_OBSERVABLE` | `NOT_OBSERVABLE` | xtool can build, but `phys_footprint`/Jetsam still need a device |

**The harness error this exposed, recorded deliberately.** The first retirement run killed sing-box 0.3 s
after the dial; the cache file writes fake-IP mappings asynchronously, so the record was still pending
and the next start legitimately found none. It looked like a durability defect and was the harness
racing the writer. With a 3 s settle the placeholder is restored. The log line
`missing fakeip record, try enable experimental.cache_file` is emitted when the mapping is absent for
any reason - including a retired range - and must not be read as "cache_file is disabled".
