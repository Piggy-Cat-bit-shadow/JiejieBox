# Upstream review baseline 鈥?2026-11-15 (`-s ours`)

Status: **recorded, tree unchanged.** This document exists so that "`behind = 0`" is a review statement with a per-commit ledger behind it, not a claim that 68 upstream commits landed in this tree.

Machine-readable twin: `docs/fork/UPSTREAM-BASELINE-2026-11-15.json` (same rows, same fields).

## 0. What was measured, and with what

| Item | Value |
|---|---|
| Fork branch / tip at recording | `testing` @ `162f730196d609910c834bed6925aaaa46ed6a2e` |
| Upstream branch / tip | `SagerNet/sing-box:testing` @ `3dd8aa48d403f50cced084f46b4b0d5758bd4bd1` |
| Local upstream ref used | `refs/upstream/sing-box-testing` (fetched in an earlier round; no remote was added) |
| Commits upstream had and this fork did not | **68** (`git rev-list <upstream> --not testing`) |
| Tree after the merge | identical to the pre-merge tree, plus this document and its JSON |

Procedure, in the order it was run:

```bash
git rev-list 3dd8aa48d403f50cced084f46b4b0d5758bd4bd1 --not testing          # 68
git merge -s ours refs/upstream/sing-box-testing \
  -m "merge(upstream): record the reviewed upstream baseline (ours)"
git diff testing <merge^2>   # must be empty except this ledger
```

## 1. Why the graph said 68 while the content is mostly here already

The number is real, but it is not 68 pieces of missing work. Two mechanisms explain it:

1. **Upstream re-committed (rewrote) its own line.** 56 of the 68 carry a committer timestamp of
   `2026-11-14T08:20Z` while their author timestamps are from September and October:
   "Add go TUN stack" is authored `2026-09-13` and committed `2026-11-14T08:20:14Z`. The same
   subject appears up to 24 times in this clone's object store. The fork's own history note
   (`KNOWLEDGE/BASELINE.md` 搂provenance, AGENTS.md) says one logical upstream change can appear up
   to 11 times; measured here it is 24.
2. **This fork already absorbed the pre-rewrite revisions.** 45 of the 68 have a subject that is
   already in `testing`'s ancestry 鈥?the versions that came in with the 2026-11-11 absorb
   (`303fbb47d`, parents `4810371684` + upstream `a13dbe1f785f`), which the round-05 document
   records at ahead/behind **3737 / 0**. Upstream's rewrite gave those changes new SHAs.

What actually remains is the third group: **23 commits whose subject is not in our line.** Two of
them are implemented fork-side already (see below), five are documentation wording, and 16 are
behaviour that has not been adopted here.

## 2. Counts

| Disposition | Count | Meaning |
|---|---|---|
| ABSORBED | 45 | same change already in our line under its pre-rewrite SHA |
| FORK_EQUIVALENT | 2 | implemented here independently; the upstream commit is not needed for the behaviour |
| DOCS_ONLY | 5 | documentation text; no product behaviour attached |
| PENDING_REVIEW | 16 | not adopted and not yet compared line by line 鈥?the real backlog |
| **Total** | **68** | |

## 3. The 68 commits, one row each

`rewrite` = the upstream commit's committer timestamp differs from its author timestamp.

| # | upstream SHA | subject | authored | committed | rewrite | disposition | evidence |
|---|---|---|---|---|---|---|---|
| 1 | `5e613f94fc3a` | Remove unused sniff override destination | 2026-11-13 | 2026-11-14 | yes | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 2 | `dfc2204298cc` | Fix sniffers clearing reverse mapped domain | 2026-11-13 | 2026-11-14 | yes | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 3 | `761e59fc9fcd` | Fix default interface detection in OpenVZ containers | 2026-11-14 | 2026-11-14 | no | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 4 | `2d6b038ce945` | Fix broken migration links in deprecation notes | 2026-11-13 | 2026-11-14 | yes | `DOCS_ONLY` | documentation text only; no product behaviour in this tree to compare |
| 5 | `6420f53fc666` | Fix broken documentation anchors | 2026-11-13 | 2026-11-14 | yes | `DOCS_ONLY` | documentation text only; no product behaviour in this tree to compare |
| 6 | `3c9d5de6f6df` | Fix field references in DNS documentation | 2026-11-13 | 2026-11-14 | yes | `DOCS_ONLY` | documentation text only; no product behaviour in this tree to compare |
| 7 | `c98968c58323` | Remove references to removed inbound fields from docs | 2026-11-13 | 2026-11-14 | yes | `DOCS_ONLY` | documentation text only; no product behaviour in this tree to compare |
| 8 | `6db3f11263bb` | Fix neighbor table going stale after netlink receive overrun | 2026-11-14 | 2026-11-14 | yes | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 9 | `61977994c3ab` | Record HTTPS address hints in DNS reverse mapping | 2026-11-13 | 2026-11-14 | yes | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 10 | `f2816c25820e` | Fix sniffed ECH public name overriding DNS mapped domain | 2026-11-13 | 2026-11-14 | yes | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 11 | `0c0a655136cf` | Fix auto-redirect overwriting marks of other programs | 2026-11-14 | 2026-11-14 | no | `FORK_EQUIVALENT` | fork-side equivalent: sing-tun 24e6bfd keeps other programs' packet marks (masked nftables compare/store + nfqueue verdict); integrated by PR #8 |
| 12 | `2b45ea59bd30` | Fix missing handshake timeout in SSH outbound | 2026-11-14 | 2026-11-14 | yes | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 13 | `53440b357b6b` | Fix buffer sizes of sockets hidden from quic-go | 2026-11-14 | 2026-11-14 | no | `FORK_EQUIVALENT` | fork-side equivalent: quic-go a99a3c4 SetDesiredBufferSizes + sing-quic 08c6abd size the socket at the four entry points; integrated by PR #8 |
| 14 | `0a03261e7922` | Send reality client version 1.8.2 | 2026-11-14 | 2026-11-14 | no | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 15 | `92724ee548d8` | Drop Edge 85 and iOS 14 from random uTLS fingerprint | 2026-11-14 | 2026-11-14 | no | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 16 | `08eaa2878c3e` | Send reality fallback request before returning | 2026-11-14 | 2026-11-14 | no | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 17 | `25d6a21f751c` | Use configured time and root pool in reality client | 2026-11-14 | 2026-11-14 | no | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 18 | `39501357a5c3` | Stop removing X25519MLKEM768 from reality ClientHello | 2026-11-14 | 2026-11-14 | no | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 19 | `c6770bf150f1` | Fix ShadowTLS v2 client authentication with X25519MLKEM768 | 2026-11-14 | 2026-11-14 | no | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 20 | `3e34bc176536` | Update uTLS to v1.9.0-alpha.1 | 2026-11-14 | 2026-11-14 | no | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 21 | `2cd3e2f84827` | Remove ineffective ALPN override from reality client | 2026-11-14 | 2026-11-14 | no | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 22 | `56bf27b23dbc` | Fix auto-redirect on older Linux kernels | 2026-11-14 | 2026-11-14 | no | `PENDING_REVIEW` | not compared line by line yet; adopt-or-explain read required before the next sync |
| 23 | `e6e78f75e4b5` | Migrate anytls into our own library | 2026-09-02 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 4234d4cc8393 (2026-09-02) |
| 24 | `b678b4e731b4` | Load rule-sets through mmap on iOS | 2026-09-02 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 290f73792454 (2026-09-02) |
| 25 | `df93115b031b` | Format plural list options as arrays | 2026-09-28 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: aac7dabaa046 (2026-09-28) |
| 26 | `f3bf0b59c794` | Implement fully functional auto redirect for Android | 2026-09-01 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 39545f00a08e (2026-09-01) |
| 27 | `4cad1bebd853` | Use screen state to end device pause on iOS | 2026-09-08 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: a875fe93ff8e (2026-09-08) |
| 28 | `e8517132be10` | Report every process sharing a socket in Linux process search | 2026-09-06 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: d9e14409f591 (2026-09-06) |
| 29 | `bb3e3b1328a2` | Remove dependency on gVisor | 2026-09-16 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: c1cc12b3fde1 (2026-09-16) |
| 30 | `eda056283ebc` | Add go TUN stack | 2026-09-13 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: fdc0299d8898 (2026-09-13) |
| 31 | `6eb171cba71d` | Close idle connections of unreferenced outbounds and DNS servers | 2026-09-02 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 31aacbfb4ab0 (2026-09-02) |
| 32 | `5a79bf7c9257` | Buffer cache file writes | 2026-09-03 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: e8b8d1029c7d (2026-09-03) |
| 33 | `8085439526d2` | Improve process search | 2026-09-04 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 570212580eb7 (2026-09-04) |
| 34 | `805852a33d6d` | Improve idle connection management | 2026-09-04 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 729230af1cd4 (2026-09-04) |
| 35 | `bcb605417757` | Add tailcat support | 2026-09-16 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 1ec54398db16 (2026-09-16) |
| 36 | `f30cfe8db25c` | Rewrite HTTP proxy protocol | 2026-09-21 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 7880f4cc807a (2026-09-21) |
| 37 | `ee6aed87204b` | Fix Android auto-redirect routing | 2026-09-19 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 0acfbb23a909 (2026-09-19) |
| 38 | `3dde0fd05400` | Add MASQUE support | 2026-09-21 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 1b5ff60a4208 (2026-09-21) |
| 39 | `cf061a80690a` | Fix FakeIP storage consistency with buffered cache file writes | 2026-09-21 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: b6dc339ebf61 (2026-09-21) |
| 40 | `a1b39346177a` | Resolve Windows connection owners without scanning the TCP table | 2026-09-17 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: bc3345f04e7d (2026-09-17) |
| 41 | `be8f3f2a655e` | Fix auto redirect DNS hijack to local address with iptables | 2026-09-17 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 1634ed68bf89 (2026-09-17) |
| 42 | `6409f3e84273` | Fix DNS sniffer matching non-DNS packets | 2026-09-18 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: f15590247901 (2026-09-18) |
| 43 | `8c69b7b96575` | Validate IP addresses and prefixes in JSON schema | 2026-09-18 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 3683a74fcfeb (2026-09-18) |
| 44 | `f388efb1cc29` | Fix on-demand endpoint resume | 2026-09-24 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 307a9ba812b4 (2026-09-24) |
| 45 | `d27f4b9f50d2` | Add dns_server_address and dns_search_domain rule items | 2026-09-24 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: e90e0d07e618 (2026-09-24) |
| 46 | `c61f04c24f0f` | Remove shared field defaults from MASQUE docs | 2026-09-25 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: ab4f96726a85 (2026-09-25) |
| 47 | `fa3defc037ea` | Fix protocol input validation | 2026-09-28 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 3cd77ab49352 (2026-09-28) |
| 48 | `2bebcb1d1a09` | Fix per-destination backpressure to endpoints | 2026-09-25 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 471910941536 (2026-09-25) |
| 49 | `40f41693eddf` | Fix HTTP/2 extended CONNECT with Go 1.27 | 2026-09-23 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 92934796cd95 (2026-09-23) |
| 50 | `72815f8ab6c3` | Add full certificate SHA-256 pinning for TLS | 2026-09-22 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: b8026930e26f (2026-09-22) |
| 51 | `6bd19c6299a4` | Fix AnyTLS splitting UDP packets into multiple frames | 2026-09-24 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 3a8c595beec5 (2026-09-24) |
| 52 | `d93be091a6be` | Use Prefixable for ip_cidr rule options | 2026-09-24 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: ed6c1689ec96 (2026-09-24) |
| 53 | `2a8116efc188` | Fix bridge forwarding with auto-redirect | 2026-09-24 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: d7324e4824d7 (2026-09-24) |
| 54 | `203e36746899` | Fix Tailscale peer relay server socket control | 2026-10-23 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 495d9fe45823 (2026-10-23) |
| 55 | `be59a1c5590b` | Remove local replace from test module | 2026-10-23 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: f10d31b17dfa (2026-10-23) |
| 56 | `603c46d88d8e` | Stagger connection attempts in parallel dial | 2026-10-23 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 2b974d181075 (2026-10-23) |
| 57 | `77d023f4b217` | Remove stale workflow | 2026-10-24 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 4e0917774cc9 (2026-10-24) |
| 58 | `e8acef2d5557` | Bump version | 2026-10-23 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 009669e7d6ab (2026-10-23) |
| 59 | `bc12d808a7ec` | Run systemd service as root | 2026-10-22 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 626964e7bcef (2026-10-22) |
| 60 | `4464491bc498` | Rework forward NAT with UDP mapping and fragment support | 2026-10-06 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 075fc22f2799 (2026-10-06) |
| 61 | `1cec967333e1` | Fix Xcode build cache reuse across package updates | 2026-10-02 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 1e8736edc61f (2026-10-02) |
| 62 | `fd7fe60f4dbe` | Remove unused DNS search expansion | 2026-10-20 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 75fb8f18315b (2026-10-20) |
| 63 | `3e42429777ff` | Limit concurrent auto-redirect flow judgements | 2026-10-18 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 4c36359423af (2026-10-18) |
| 64 | `79181950c0bb` | Apply backpressure to masque inbound packets | 2026-10-18 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 0c343d88b378 (2026-10-18) |
| 65 | `fb54d9ea4802` | Fix platform auto-redirect close order | 2026-11-08 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: a13dbe1f785f (2026-11-08) |
| 66 | `3dd8aa48d403` | Fix broken documentation anchors in testing pages | 2026-11-13 | 2026-11-14 | yes | `DOCS_ONLY` | documentation text only; no product behaviour in this tree to compare |
| 67 | `7047f9b8408d` | Forward endpoint local flows to tun | 2026-10-28 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: 9feaa2315431 (2026-10-28) |
| 68 | `c5aa74237697` | Close UDP sockets when device addresses change | 2026-11-03 | 2026-11-14 | yes | `ABSORBED` | pre-rewrite revision already in our line: dd897389b236 (2026-11-03) |

## 4. What this merge does NOT do

- It does not take a single upstream file. The merge strategy is `ours`: the resulting tree is
  `testing`'s tree plus this ledger.
- It does not move a dependency pin, a gitlink, `go.mod`, `go.sum`, `release/source-lock.json`,
  `release/pin-runs.json` or the release version. None of them is touched by this branch.
- It does not adopt the `PENDING_REVIEW` commits, and it does not make them unreachable: they are
  reachable from upstream, and the SHAs are recorded here.

## 5. Consequence, stated so nobody has to rediscover it

After this merge, upstream `testing` is an ancestor of this branch. That has two effects:

1. A later `git merge upstream` brings **only commits upstream adds from now on**. The 16
   `PENDING_REVIEW` commits will never arrive by merge; adopting one means a deliberate
   cherry-pick/patch with its own build, ABI and pin-runs consequences. The uTLS/reality cluster
   in that group also needs a uTLS version move, which is a pin and release-lock change, not a
   free ride.
2. `behind = 0` is now a graph fact **and** a reviewed statement: every one of the 68 has a row
   above. It stops being true the moment upstream pushes again, which is the point of recording
   the tip SHAs in section 0.

## 6. How to re-derive this file

```bash
git fetch --no-tags https://github.com/SagerNet/sing-box.git \
  +refs/heads/testing:refs/upstream/sing-box-testing
git rev-list refs/upstream/sing-box-testing --not testing      # list to classify
```

Classification is mechanical for `ABSORBED` (subject present in `git log testing`) and for
`FORK_EQUIVALENT` (checked against the pinned dependency forks); `DOCS_ONLY` and
`PENDING_REVIEW` are the two labels a human has to defend in review.
