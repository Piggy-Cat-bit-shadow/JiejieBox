# Phase 08 — final ancestry and branch closeout

```text
STAGE = 08_FINAL_CLOSEOUT
RESULT = DONE_WITH_VERIFIED_HOLDS
MAIN_TESTING_HEAD = 173957534f1c01e0e26e080d98756efd84d1f51e (code; docs commit follows)
UPSTREAM_SINGBOX_TESTING_HEAD = a6952ccf6907c18e18917fcb763a433e8e36937b
MAIN_BEHIND_BY = 0
MAIN_OPEN_SEMANTIC = 0
MAIN_OPEN_DEPENDENCY_BLOCKERS = 0
OOM_SEMANTIC = PRESERVED_AND_TESTED
CI_CLOSEOUT_BRANCH = MERGED_AND_REMOVED
PENDING_DEVICE_BRANCH = PRESERVED 0477e327e5daa6a6fc757820154b13a628f1ed74
QUIC_SAGERNET_BRANCH = f0f7ab2d6f609a3a1a26ea9bc12b9f834c994c85, github.com/sagernet/quic-go
QUIC_METACUBEX_BRANCH = DELETED per explicit user instruction (was beb42da71a55); default is now sagernet
SING_TUN_UPSTREAM = SagerNet/sing-tun:dev 611d83b7a57b4719be45581eb674942ff561de6c
SING_TUN_FORK = ade2434649adc4e62c3fffa91a18ca5bda5b65f4; BEHIND=0; AHEAD=80; SEMANTIC_MATRIX=phase-01 report + 33 patch-equivalent + 8 mapped ports
SING_UPSTREAM = SagerNet/sing:dev 03a6cce64bf744023af9e2691fb11afd405b9061
SING_FORK = 4200a04a089a8271a2243971790f15c262f7bcfe; BEHIND=17; AHEAD=48; SEMANTIC_MATRIX=HOLD: bufio/Windows/stagger fixes not all ported
SING_QUIC_UPSTREAM = SagerNet/sing-quic:main 7e0a5869a5f49ed0a685143345dfda0448bbc708
SING_QUIC_FORK = 1ef2597cc1298146fcdddfeaa0384999f890d0c3; BEHIND=0; AHEAD=7
CRONET_UPSTREAM = SagerNet/cronet-go:main 049f2909701ca088c9569f6b303bba5376a2b2ff
CRONET_FORK = 8aa139e9e140d5737f82deaa934b81b68054712b; BEHIND=0; AHEAD=14; NATIVE_ABI=29 libs 8c68ce8, 37 assets
QUIC_UPSTREAM_RELEASE_LINE = v0.61.0-sing-box-mod.12 @ 80c132feb2112b5d0d88d02f941b1e68e56a2722
QUIC_UPSTREAM_DEV_COMPARISON = DEV_NONTARGET; sagernet is based on the mod.9 16930172 release line, not upstream/dev
EFFECTIVE_MODULE_PINS = sing-tun ade2434; sing 4200a04; quic-go f0f7ab2; sing-quic 1ef2597; cronet 8aa139e/8c68ce8; tailscale 1688b52
GVISOR_PERSONAL_FORK_CONSUMERS = NONE_VERIFIED (no replace; sing-tun's with_gvisor code uses github.com/sagernet/gvisor)
GVISOR_ACTION = ARCHIVED_WITH_EVIDENCE (Piggy-Cat-bit-shadow/gvisor)
REMOTE_BRANCH_COUNTS = sing-box 3->2; sing-tun 2->2; sing 1->1; sing-quic 1->1; quic-go 2->1; cronet-go 3->3
DELETED_BRANCHES = sing-box ci/actions-release-closeout-20261024 8b0737c8362 (merged); quic-go metacubex beb42da71a55 (user-approved removal)
HELD_BRANCHES = sing-tun fix/acceptloop-selfheal 1cd9bc2216df (old 28-ahead/81-behind history, Go stack/accept-loop work not proven superseded); cronet-go go 0d28acc44093 (old native build branch, diff too large); cronet-go jiejie-naive-zero-copy 520c2873df45 (docs/bench/test-only); sing dev behind 17 upstream commits
GO_TEST = module integrity PASS; sing-tun targeted PASS; quic-go ./... PASS; sing-quic ./... PASS; main build/TUN/device/MASQUE/Hysteria2/TUIC PASS
RACE = OOM and DNS -race PASS (phase-07 tree; OOM code unchanged by pin-only commits)
CI_FINAL_CODE_SHA = 173957534f1: Verify 40120061388, Android 40120061404, Mobile 40120061389 (statuses filled after completion); docs Verify follows
WORKTREES = _p01_main clean; _ci_closeout and OOM worktrees removed; pre-existing detached worktrees untouched
TAGS_RELEASES_ASSETS = UNCHANGED
FORCE_PUSH = NONE
```

## Actual upstream target (sing-box 1.15 testing)

The target is the dependency set declared by `SagerNet/sing-box/testing` at `a6952ccf6907`, not the
latest dev branch of each library:

| module | declared version | commit |
|---|---|---|
| sing-tun | 611d83b7a57b | `611d83b7a57b4719be45581eb674942ff561de6c` |
| sing | 03a6cce64bf7 | `03a6cce64bf744023af9e2691fb11afd405b9061` |
| sing-quic | 7e0a5869a5f4 | `7e0a5869a5f49ed0a685143345dfda0448bbc708` |
| quic-go | mod.12 | `80c132feb2112b5d0d88d02f941b1e68e56a2722` |
| cronet root/all | b3c2ed82ac97 | `b3c2ed82ac97` |
| cronet lib | d5437a0df01a | `d5437a0df01a` |

## What changed

### CI closeout branch

`ci/actions-release-closeout-20261024` (`8b0737c`) was merged into `testing` as `6fc9c59`; all five
files are now on `testing`. Checker 133/133, YAML parse, Release Contract 107/107 and the branch's
Verify/Mobile evidence were green. The remote branch and its worktree were removed after the merge.

### quic-go

- `sagernet` now records `80c132f` (mod.12) as an ancestor. The four target commits were audited:
  `f061` EMSGSIZE, `dbae` trust_anchors and `80c` application-limited are patch-equivalent;
  `a032` Chrome IPv6 packet size was ported (`cf36655`). The merge is `f0f7ab2`.
- The old `verify.yml` trigger was changed from `jiejie-masque-mod9` to `sagernet`; the repository
  default is now `sagernet` and `metacubex` was deleted after the user confirmed it is not needed.
- `sing-quic/main` follows at `1ef2597`; root and nested test module both resolve to
  `f0f7ab2`/`1ef2597` effective replacements.

### sing-tun

- The 41 upstream-only commits contain 33 patch-equivalent commits and eight ports already recorded
  in phase 01 (IPv6 no-auto-route, accepted-TCP removal, closed-session NAT, forward-flow close,
  Android auto-redirect, Go stack, non-blocking judgement, forward-close rework). `ade2434` records
  `upstream/dev 611d83b` as a parent with `-s ours` after that audit; the tree keeps `(handled,remove)`,
  `PacketBufferPort`, NAT/fragment and Go-stack contracts.
- Root and nested test pins now resolve to the same `ade2434` pseudo-version. TUN/device tests pass.

### sing

- Upstream `dev` is `03a6cce`; fork `4200a04` remains **17 behind / 48 ahead**. The remaining
  upstream-only commits are not all patch-equivalent: `common/bufio` allocation and oversized-UDP
  handling, Windows dual-stack binding, SOCKS batch writes, parallel-dial staggering, abx/NTP/JSON
  fixes. A normal merge was attempted and conflicts in nine bufio/pause files could not be resolved
  without rewriting the fork's custom scratch/offload paths; it was aborted rather than forcing a
  lossy resolution. This is the one verified hold.
- No pin was changed for `sing`; consumers remain on `4200a04`.

### cronet-go

`main` is ahead 14 / behind 0 of upstream; root/all 8aa and 29 libs 8c68ce with 37 native assets are
unchanged. `go` and `jiejie-naive-zero-copy` are retained with reasons; no consumer references them.

### gVisor

No shipped tag and no `replace` uses the personal fork; `github.com/sagernet/gvisor` remains only as
sing-tun's optional transitive requirement. `Piggy-Cat-bit-shadow/gvisor` was archived rather than
deleted, with the `with_gvisor` source and module untouched.

## Verification

- `scripts/ci/check-go-module-integrity.sh`: 3 modules, 36/36 root replaces and 31/31 Cronet paths
  consistent, PASS.
- `go mod verify` root/test: PASS; `go build` with `DEFAULT_BUILD_TAGS_OTHERS`: PASS.
- sing-tun targeted system accept/NAT/memory/flow tests: PASS; build PASS.
- quic-go `go test ./...` and `go vet ./...`: PASS.
- sing-quic `go test ./...`: PASS.
- main TUN/device/MASQUE/Hysteria2/TUIC: PASS; MASQUE runs end to end.
- Final main code CI runs are recorded in the report header; the docs-only commit gets its own
  Verify run rather than reusing the code SHA's result.

## Remaining work (one explicit next step)

For `sing`, port the 17 upstream-only commits in the next window, starting with the small
`ff74/749/9faf/0678/9779` fixes and deciding whether the fork's `common/bufio` scratch/offload
implementation supersedes upstream `226/33c/03a6`. Do not merge until each is mapped and tested.
This is why the result is `DONE_WITH_VERIFIED_HOLDS`, not `DONE`.
