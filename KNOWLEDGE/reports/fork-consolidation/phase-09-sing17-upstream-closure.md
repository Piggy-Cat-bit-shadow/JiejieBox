# Phase 09 — sing 17-commit upstream fusion and branch purge

```text
STAGE = 09_SING_UPSTREAM_AND_BRANCH_PURGE
RESULT = DONE_WITH_DEVICE_LIMITS
UPSTREAM_SINGBOX_TESTING_SHA = a6952ccf6907c18e18917fcb763a433e8e36937b
UPSTREAM_SING_PIN_SHA = 03a6cce64bf744023af9e2691fb11afd405b9061
SING_FORK_DEV_FINAL_SHA = e70473d8f2fbcda5506c31e04d1828b2fc770a5e
SING_UPSTREAM_TARGET_IS_ANCESTOR = YES
SING_BEHIND_TARGET = 0
SING_17_ITEM_AUDIT = 17/17
SING_CODE_REGRESSIONS = 0
SING_PERFORMANCE_REGRESSIONS = NOT_MEASURED (no reproducible before/after bench; all package tests pass)
SING_BOX_TESTING_FINAL_SHA = 234bc542d5615502aea3562f17300aadc0e24f31 (code; docs commit follows)
ROOT_TEST_SING_EFFECTIVE_PIN = v0.9.6-0.20261024191059-e70473d8f2fb (both modules)
SING_TUN_ACCEPTLOOP_BRANCH = DELETED (was 1cd9bc2216df)
CRONET_GO_BRANCH = DELETED (was 0d28acc44093)
CRONET_NAIVE_BRANCH = DELETED (was 520c2873df45)
MAIN_PROTECTED_PENDING = 0477e327e5daa6a6fc757820154b13a628f1ed74 UNCHANGED
QUIC_SAGERNET = f0f7ab2d6f609a3a1a26ea9bc12b9f834c994c85 (unchanged this phase)
GVISOR_ARCHIVED = UNCHANGED
CI_VERIFY = Verify 40124382403, SHA 234bc542d561, SUCCESS
CI_ANDROID = Android Core ARM64 40124382415, SHA 234bc542d561, SUCCESS
CI_MOBILE = Mobile contract verification 40124382395, SHA 234bc542d561, SUCCESS
FORCE_PUSH = NONE
TAG_RELEASE_ASSETS = UNCHANGED
REMOTE_BRANCH_INVENTORY = sing-box testing/pending; sing dev; sing-tun dev; sing-quic main; quic-go sagernet; cronet-go main
WORKTREES = _p01_main clean; sing integration worktree removed; pre-existing detached release/bench worktrees untouched
```

## Merge and audit

`sing` fork `dev` (was `4200a04`) merged `SagerNet/sing/dev` at the sing-box 1.15
`go.mod` pin `03a6cce6`; the merge commit is `e70473d`. The upstream target is now a real ancestor
and `git rev-list --count HEAD..upstream/dev` is 0.

### 17-item matrix

| upstream | classification | fork implementation / evidence |
|---|---|---|
| `ff74c537` IPv6 original dst port | DIRECT_PORT | `common/control/redirect_linux.go`; Linux test |
| `749ff464` IP_PMTUDISC_DO | DIRECT_PORT | `common/control/frag_linux.go` |
| `226df743` oversized UDP | ADAPTED_PORT | fallback batch writer now leaks the rejected buffer, continues, returns the last `errMessageTooLong`; fork scratch retained |
| `237e23f2` Windows dual-stack | DIRECT_PORT | `common/control/bind_windows.go` |
| `9faf3c29` ABX truncated input | DIRECT_PORT | `common/abx/reader.go` |
| `0678b219` JSON format | DIRECT_PORT | `common/json/...`; format tests |
| `9779f53a` NTP wake | DIRECT_PORT | `common/ntp/service.go` |
| `5dc452e5` pause wait | ALREADY_EQUIVALENT | phase-02 `WaitActiveContext`; `service/pause` race test |
| `399dc04f` domain mmap | DIRECT_PORT | `common/domain` constructors |
| `575784b8` Windows socket owner | DIRECT_PORT | `common/winiphlpapi` |
| `a8c04917` UDP socket buffer | DIRECT_PORT | `common/control` |
| `33c35f12` SOCKS UDP batch | ADAPTED_PORT | upstream protocol/socks merged; fork lazy/preconnect and packet ownership retained |
| `f28a8521` UDP buffer soft-fail | DIRECT_PORT | combined with a8c |
| `979c0526` memory Limit | DIRECT_PORT | `common/memory` |
| `3a1dcc10` Windows TCP owner key | DIRECT_PORT | `common/winiphlpapi` |
| `5a314a42` parallel dial stagger | DIRECT_PORT | `common/network/multi.go` |
| `03a6cce6` batch allocation reduction | ADAPTED_PORT | fork scratch/offload packet batch retained; upstream changes merged where non-conflicting |

The nine bufio/pause conflicts were resolved explicitly: the fork scratch/offload and Darwin vector
paths win where they encode stronger ownership; the upstream fallback `errMessageTooLong` behavior
was ported; pause conflict was comments only. No blanket `--ours/--theirs`.

## Verification

- `go build ./...`, `go test ./...` in `sing`: PASS.
- `go test -race` on `common/bufio`, `protocol/socks`, `service/pause`: PASS with
  `-skip TestWriteVectorisedPacket` (the pre-existing Windows `vectorised_windows.go` checkptr panic;
  the same test fails in the old tree and is not a merge regression).
- Cross builds: Windows amd64, Linux amd64, Darwin arm64: PASS.
- `sing-box` root/test `go mod tidy`/`verify`: PASS; module integrity 3 modules PASS.
- Product regression: SOCKS, WireGuard, TUN, direct, DNS, MASQUE: PASS; build with
  `DEFAULT_BUILD_TAGS_OTHERS`: PASS.
- Final code CI at `234bc542d56`: Verify/Android/Mobile all SUCCESS.

## Branch purge

- `sing-tun/fix/acceptloop-selfheal` (`1cd9bc`) deleted: current dev has the accept-loop self-heal
  and updated API/tests; the old branch's Go-stack work is superseded by the phase-01 merge and
  `ade2434` ancestry.
- `cronet-go/go` (`0d28acc`) deleted: old native build branch; current main has the 37-asset,
  29-lib generation (`8aa139e`/`8c68ce8`) and no reference.
- `cronet-go/jiejie-naive-zero-copy` (`520c287`) deleted: only old test/bench/docs scaffolding, no
  production code and no consumer.
- Remaining branches: sing-box `testing` + `pending`; sing `dev`; sing-tun `dev`; sing-quic `main`;
  quic-go `sagernet`; cronet-go `main`.

## Known limits

- No real iOS/Android device; native/SDK behavior is CI/build evidence only.
- No stable before/after performance bench was available for the packet-batch/SOCKS/parallel-dial
  changes; the fork's existing implementations were retained and all tests passed. This is recorded
  as `NOT_MEASURED`, not as a performance PASS.
- The Windows `vectorised_windows.go` race/checkptr test is a pre-existing host limitation and is
  excluded only from the local race probe; the code is unchanged by this merge.
