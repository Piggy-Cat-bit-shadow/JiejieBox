# The three release windows: what this session executed, and what it did not

The `JiejieBox_PR_Issue_Debug_…` prompt set asks for three release-stability windows: A (core control
plane: TUN/DNS/lifecycle), B (protocol transports: QUIC/H3/XHTTP/Naive), C (expanded autonomous
search). This page maps each window's **attack groups** — the tables at the top of each file — to what
was actually executed in this session, so the next round starts from the truth instead of a claim.

**Reading caveat, stated because the briefs demand it:** the three files were read at heading and
attack-group level. Their appendices hold 80 / 48 numbered research seeds, and those were *not* read
line by line (the shell renders the files in a mangled encoding). No per-seed id is claimed as
executed except where the group is named below. No external Issue/PR is cited: the search attempts in
this session returned documentation pages only and the git remotes reset repeatedly.

## Window A — core control plane

| Attack group (from the file) | Executed in this session | Result |
| --- | --- | --- |
| DNS cross-blocking (a stalled DNS must not stall unrelated TCP; queue must be bounded) | window A's DNS transport pairing (`PAIRWISE_A_MATRIX` rows for `dns/dialer`), plus `dns` package suites (8.7 s + 6.1 s) | no defect; the gate's bounded wait is pinned |
| DNS stream isolation (concurrent requests, truncated reads) | `dns` suite, including the generation/cache isolation family (12 named tests) | no defect |
| Fake-IP cold start (timer churn, kill/restart, interrupted writes) | **four defects confirmed and fixed**: out-of-range allocation (B-3), retired-generation placeholder bypass (B-4), durable record with no reader (B-5), reload re-issuing a held address (C-2) | **fixed** |
| UDP lifecycle (reject + sniff + Close + network change interleaved; PacketConn and flow map must be reclaimable) | window A's R-4/R-5 audit (`sing-tun` ownership) + triple A's T1 (a pool generation installed after close) | one fixed (T1), one recorded cross-repo |
| **URLTest/Selector race (hot probe, hot update, hot group switch; no data race, no deadlock, correct terminal state)** | **one defect confirmed and fixed this round**: a provider that replaces or removes a node left the URL-test measurement of the predecessor in place (`adapter/provider`, commit `29289af38`); the group's ordering and the dashboard then used evidence about an object that no longer exists | **fixed** |
| Dual-stack dial (A/AAAA delay, IPv6-only, frequent cancellation) | `common/dialer` (~45 files, 22 s; `-race` 23 s) | no defect |
| TUN recovery and rollback (VM/namespace interruption, nftables divergence) | `protocol/tun` suites; the seven TUN pairing tests from window B | partial: no nftables/netns in this environment |
| Resource return (many short TCP/UDP → idle → GC → repeat) | window A's resource-governance round (4 fixes) | **fixed** |

## Window B — protocol transports

| Attack group | Executed | Result |
| --- | --- | --- |
| H3 small-flow fairness | `transport/trusttunnel` (33 s), `transport/v2rayxhttp` (22 s), `common/httpclient` | no defect found; the fallback ladder is the recorded R-B3 concern |
| QUIC send queue | `transport/v2rayquic` has **no test files** (recorded); the vless/vmess generation tests cover the publish path | not attacked |
| PMTU black hole | no harness; needs a controllable path | not attacked |
| Connection migration | window B's version-negotiation error (B-?? recorded); trusttunnel/v2rayquic migration paths read | partial |
| Cancel and stream termination | window B's B-2 (pool unregistered on network change), the xhttp body/EOF findings | **fixed** |
| Mieru UDP | window A's R-3 (no idle releaser; SDK cannot restart) | recorded, not fixable at the pin |
| **XHTTP/H2 (SETTINGS frame size × concurrent streams × slow read)** | **window B's B-1**: Go 1.27's `defaultMaxReadFrameSize` moved the HTTP/2 preface away from Chrome's, pinned with a test; plus the delta-assertion repairs in the xhttp pool tests | **fixed** |
| Naive/Reality (half-finished handshake, network switch, forced disconnect → no leaked bridge/goroutine, no silent downgrade) | **window A's naive fix**: the outbound no longer offers `CloseAllConnections` as a pressure hook; the H3 fallback window is reset on a network change; the fallback window staying unarmed is recorded (R-B4) | **fixed** (one recorded) |

## Window C — expanded search

The file's five "wonder" patterns (a DNS stall explained by a shared queue; "memory never drops" having
three different causes; a Go 1.27 compatibility question revealing an assumed cancel contract; QUIC
data loss being a backpressure/ownership error; an already-fixed fork bug implying a neighbour) are
methods rather than rows. Each maps to something this session did: the first to the DNS environment
generation tests, the second to the honest "no measurement" classification for iOS rows, the third to
the Go 1.27 preface finding (B-1) and the toolchain-as-wire evidence, the fourth to the xhttp body
findings, the fifth to the P9 sweep in `TRIPLEWISE_C_PATTERNS.md` (a marker that outlives its owner).

## Release-blocker verdict

No new release blocker was found in these three windows. The confirmed defects this session found and
fixed are listed in `docs/audits/README-overview-2026-11-19.md` (now fourteen). The open items with a
release-risk line are the same eight recorded there, and the only one that can amplify at runtime
(the TUN UDP NAT path) is a dependency-layer fix blocked by the pin-evidence rule during a freeze.

## What is still unexecuted, by name

- `transport/v2rayquic` has no test files at all; its publish site is the last of the four the atomic
  publish fix did not reach (R-B2), and it is the first item of the next protocol round.
- PMTU/black-hole, connection-migration soak, and any `netem`/namespace-based test: needs Linux with
  privileges.
- Every iOS/NE, Cronet-native, `phys_footprint`, auto_redirect and system-proxy row: needs a device or
  two live capture paths.
- The per-seed appendix (80/48 numbered items): read at group level only, as stated above.
