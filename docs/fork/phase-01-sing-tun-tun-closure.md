# Phase 01 — sing-tun convergence and TUN/MASQUE closure

```text
PHASE = 01/06
RESULT = PARTIAL_WITH_SPECIFIC_BLOCKER
SING_TUN_DEV = dcf84577d0eb28b2fa35bcf0a76736836f45a99d (merge 17a6a699750980284cae5d4e04a30720c543ccc2 of sync/upstream-fixes-2026-10-24 @ 88e6d8160cf5, port commit 1559fbd, style 17db765/4c4e5a2, privileged test + workflow dcf8457)
SING_TUN_SYNC_BRANCH = deleted (dev contains 88e6d81 as an ancestor; no open PRs; main no longer references it)
MAIN_TESTING = f94600ab3bb293af4dbd9734ae4536151379ac9e (code); ledger commit follows
EFFECTIVE_SING_TUN_ROOT_AND_TEST = same revision in both modules: v0.0.0-20261024025840-dcf84577d0eb, same Replace.Dir
DEP_TUN_0_1_2_3 = COMPLETED / COMPLETED / COMPLETED / COMPLETED
MASQUE_BACKPRESSURE = capsule-path park/drain/close/fallback tests pass; MemoryTun full/close/drop tests pass; live QUIC session NOT_VERIFIED_ON_DEVICE
CI_FINAL_CODE_SHA = f94600ab3bb — main Verify / Mobile contract verification / Android Core ARM64 all SUCCESS; sing-tun lint SUCCESS, test FAILURE with pre-existing failures (see below)
OPEN_SEMANTIC = 20 (was 22; 3454d6d was never open, it is PRESENT in the audit)
NEXT_PHASE_READY = YES (phase 02 starts from this ledger; DEP-SING-1 still blocks cc022098fd)
```

## What changed

**sing-tun/dev** (remote `Piggy-Cat-bit-shadow/sing-tun`)

- `17a6a69` merges `sync/upstream-fixes-2026-10-24` into `dev`. The sync branch was a rebase of
  dev's own commits onto a newer upstream base, so the resolved tree equals the sync tree. Before
  resolving, `git diff origin/dev origin/sync` was checked: the only differences were the sync-side
  fixes (owned-buffer staging, accepted-TCP removal, blocking inbound write, accept-loop self-heal,
  platform watchers, TPROXY reject). The fork contracts survive: `(handled, remove)`,
  `PacketBufferPort`, allocation-free return batching, NAT/fragment handling, Go-stack Start/Close,
  and the buffer ownership/wake rules.
- `1559fbd` ports the three remaining mechanisms:  - system-stack TCP NAT: RST or FIN-in-both-directions marks a session closed, it expires after
    `tcpClosingTimeout`, a retransmitted SYN gets a fresh mapping, and a concurrent sweep re-checks
    map identity before deleting. `stack_system_nat_test.go`.
  - no-auto-route IPv6: `::/0` is added to the instance's own table, the rule priority is clamped
    above `redirectRouteRulePriority`, and cleanup deletes by (family, output interface, table) and
    tolerates ENOENT. `tun_linux_test.go` (Linux build).
  - non-blocking judgement: at most 16 held packets per flow and 512 per stage, `JudgeFlow` on its
    own goroutine, verdict applied through a resume pump on System/Mixed/Go/gvisor and nfqueue.
    Close drops pending judgements; a verdict completing after Close installs nothing.
    `flow_dispatch_judge_test.go`.
- The memory TUN's blocking inbound write is covered by `tun_memory_wait_test.go` (full queue parks,
  slot wake, close wake, non-blocking drop at the bound).

**Main repo `testing`** (`2bef2ff6d08`)

- Root and `test/go.mod` replace pins moved to `v0.0.0-20261024012809-1559fbd506eb`; both `go.sum`
  files updated; `go mod tidy`/`verify` clean; `go list -m -json` shows the same `Replace.Dir`.
- `18aa2cdfe742`: `iproute2_table_index` defaults only when `auto_route` is on, and the system VPN
  device no longer claims table 2022 / the default rule priority. Unit tests pin both mappings.
- `ef0b632920ff`: auto-redirect `JudgeFlow` is bounded at 8 concurrent binder transactions, with the
  slot released by defer.
- MASQUE inbound backpressure adds real capsule-path tests (park on full, drain one, close wake,
  non-blocking fallback), plus the sing-tun queue-level tests.

## Verification actually run

| Check | Result |
|---|---|
| `golangci-lint run` on windows/linux/darwin/android targets | PASS (0 issues) |
| `go build ./...` (sing-tun, Windows) | PASS |
| `go build -tags with_gvisor ./...` (Windows) | PASS |
| cross-builds: linux/amd64, linux/arm64, darwin/arm64, android/arm64, windows/arm64 (with and without `with_gvisor` where applicable) | PASS |
| `GOOS=linux go test -c` for sing-tun root and main `experimental/libbox` | PASS (compile only) |
| sing-tun `-race` on the new NAT, judgement and memory-TUN tests | PASS |
| sing-tun targeted suites (flow dispatch, system NAT, system accept/listenconfig, memory TUN) | PASS |
| sing-tun full root package minus `TestGoKernelTCPTransmitLifetime` | fails only `TestGoKernelPacketBatchIdle`, identically on the pre-merge sync tree (host issue) |
| sing-tun `linux-tun-verify` on GitHub's Ubuntu kernel | PASS: linux-only unit suite as non-root, and the committed dual real-TUN test as root (two TUNs, per-instance rule and `::/0`, Close removes only its own) |
| main Linux checks (`protocol/tun`, `transport/device`, `transport/masque`, libbox judgement) | PASS |
| main `go test ./protocol/tun ./transport/device ./transport/masque` | PASS |
| main same packages under `-race` | PASS |
| main `go build -tags DEFAULT_BUILD_TAGS_OTHERS ./...` | PASS |
| main `go mod verify` (root and test) | PASS |

## CI evidence

**sing-tun** (run on `dcf8457`):

- lint: SUCCESS (run 40056405709).
- test: FAILURE (run 40056405724; rerun failed the same way). Persistent failures:
  `TestGoConnKeepalive`, `TestGoKernelZeroWindowHalfClose`, `TestGoKernelPacketMapping` — all on the
  pre-merge `dev` 7539c98 baseline too (run 40050607262), which additionally failed
  `TestGoKernelFlowControl` and `TestGoKernelPacket`. `TestGoKernelPacketFragments` failed once and
  passed on rerun. No new persistent failure.
- `linux-tun-verify`: SUCCESS (run 40056412780), including the root dual real-TUN test.

**Main repo**: SUCCESS on `f94600ab3bb`: Verify (run 40057018200), Mobile contract verification
(40057018099), Android Core ARM64 (40057018028). The Linux package checks also pass separately
(run 40056761067: `protocol/tun`, `transport/device`, `transport/masque`, `experimental/libbox`).

Not run, and why:

- Real Linux rule/route tables are now covered (sing-tun run 40056412780) with two real TUN devices.
- Android device behaviour for the 8-slot semaphore is still `NOT_VERIFIED_ON_DEVICE`; the Linux
  runner only proves the channel bound and the defer release.
- A live QUIC client/server MASQUE datagram session (`NOT_VERIFIED_ON_DEVICE`): the capsule and
  device layers are covered; the transport session was not exercised against a peer here.
- `internal/winipcfg` tests fail with `ERROR_NOT_FOUND` (no suitable interface) on this host;
  `BLOCKED_BY_ENVIRONMENT`, unrelated to this phase.

## Ledger correction

The authoritative machine-readable ledger is `UPSTREAM-INTEGRATION-STATUS.json`. The previous 23
count came from the markdown table including `3454d6d2411d`, which the audit JSON classifies
`PRESENT`; the audit has 22 `MISSING_FIX`. After this phase: 20 open semantic rows and 5 open
dependency blockers (DEP-SING-1, DEP-QUIC-1/2, DEP-CRONET-1, DEP-TAILSCALE-1).

## Next

Phase 02 should start with `MAIN-42cae14c63d1` (Apple TLS engine leak). `MAIN-cc022098fdec`
(WireGuard pause/sleep) remains blocked on DEP-SING-1. The sing-tun `sync/upstream-fixes-2026-10-24`
branch is deleted; `dev` is the only long-term branch.
