# Branch consolidation status — 2026-10-24

## Gate check

- Phase 06 ledger: open semantic **0**, dependency blockers **0**, ancestry behind **0**.
- Phase 06 final code: `c3c23d1059c`; docs `9b1f95e40a4`; Verify/Android/Mobile green.
- No open PR in the six repositories; no release or tag was modified in this phase.
- `pending/device-axis-reload-window-tests` is preserved at `0477e327e5daa6a6fc757820154b13a628f1ed74` exactly as required.

## OOM integration

Source branch: `oom-final-2026-10-24` at `66b2b00287fa41ba7f4457c76e8aed2424a6fc25`.

| source commit | semantics | integrated as |
|---|---|---|
| `489ed2c4c164` | remove armed-footprint thrash; triggered stop barrier | cherry-picked |
| `4cc985c84083` | single reclaim executor; L1 cache / L2 idle / L3 GC; stop and overlap tests | cherry-picked (context-only conflict, no semantic rejection) |
| `80b42c247a8e` | transient pressure intent stays inside the stopped instance | cherry-picked |
| `61a04df647dc` | reload re-decides crisis instead of inheriting Triggered | cherry-picked |
| `2dee4eb478d5` | libbox platform sink kept out of adapter | cherry-picked |
| `ed0686`/`66b2b0` | OOM reports | final report carried via `66b2b0` |

The five code commits were applied cleanly on phase-06 `testing`; integration tip `e19b74a3540`
was merged to `testing` as `8795ba09d35178b448a45acc8555c19fce781311`.

Evidence before merge:

- `go test ./service/oomkiller ./dns ./experimental/libbox` — pass.
- `go test -race ./service/oomkiller ./dns` — pass.
- full `./route` suite — `ok 441.594s` (includes the intentional six-minute kernel-window test).
- `go build ./...` with `DEFAULT_BUILD_TAGS_OTHERS` — pass.
- OOM branch runs at `e19b74a`: Verify `40100352194`, Android `40100355076`,
  Mobile `40100357686`, all success at `e19b74a`.
- Final testing SHA `8795ba09d35`: Verify `40101905117`, Android `40101905143`, Mobile `40101905187`,
  all success.

OOM source and integration branches were deleted only after those results. Remote evidence:
`oom-final-2026-10-24` and `integration/oom-final-2026-10-24` are absent; `testing` contains the
semantics and the report.

## Branch inventory and actions

Counts are remote branches for the fork repository, before → after.

| repo | before | after | target |
|---|---:|---:|---|
| sing-box | 4 | 3 | `testing` |
| sing-tun | 6 | 2 | `dev` |
| sing | 3 | 1 | `dev` |
| sing-quic | 5 | 1 | `main` |
| quic-go | 4 | 2 | `sagernet` + `metacubex` |
| cronet-go | 7 | 3 | `main` |

### sing-box

- `testing` `8795ba09d35` — permanent.
- `pending/device-axis-reload-window-tests` `0477e327e5d` — **preserved**, not modified, not merged.
- `ci/actions-release-closeout-20261024` `8b0737c8362` — retained for human review; the preceding
  instruction explicitly forbade auto-merging it to `testing`. It contains the validated Actions
  closeout (P0 mobile concurrency, Verify race split, checker/docs).
- `oom-final-2026-10-24` `66b2b00287f` and temporary integration `e19b74a3540` — deleted after merge.

### sing

- `dev` `4200a04a089a` — target.
- Deleted ancestors: `fix/socks-lazy-reply-race` `3af46fe99d3b`, `fix/splice-setup-failure-fallback-v2` `baed8c3b4a34`.

### sing-tun

- `dev` `dcf84577d0eb` — target and effective pin.
- `fix/acceptloop-selfheal` `1cd9bc2216df` — **UNRESOLVED_HOLD**: 28 ahead / 81 behind; it is an
  old branch (ahead 28, behind 81) whose current dev already has the accept-loop self-heal test and
  API; the branch carries broader historical Go-stack work not proven equivalent. Do not delete
  without a focused range-diff.
- Deleted: `fix/listenconfig-ipv6-only-selfheal` `109186a15f3b`, `sync/go-stack-plus-040-and-race-9ffa539`
  `2980fcaf8102` (ancestors), `sync/go-stack-plus-040` `46cfb7ff31fe`, `sync/go-stack-plus-040-and-race`
  `8dde9c8cbe27` (the accept-loop commit is present in current dev; `stack_go.go` already has the
  Start/Close handshake and the current test files pin it).

### sing-quic

- `main` `5e5a0274bd6c` — target and pin.
- Deleted: `dev` `75c3ac4fa12b`, `stable` `2419e1d7f7ae` (both ancestors); `jiejie-packet-ceiling`
  `d0deb0359c06` (its ceiling/owned-datagram code and tests are in `main`; `git diff` over quic.go,
  quic_ceiling_test.go and tuic/owned_datagram.go is empty); dependabot x/net-0.17 `d94371968817`
  (obsolete; main uses x/net v0.56).

### quic-go

- `sagernet` `ce02ee5cc210` (module `github.com/sagernet/quic-go`) — production line.
- `metacubex` `beb42da71a55` (module `github.com/metacubex/quic-go`) — retained independent line and
  **default branch**.
- Old names `jiejie-masque-mod9` / `jiejie-masque-stable` are absent from `git ls-remote`.
- Deleted: `jiejie-masque-convergence` `d29e441af4d8`, `jiejie-zero-copy-datagram` `393ae9ae9df7`;
  the ownership/batch DATAGRAM API and tests are in `sagernet`, which is additionally ahead with the
  packet-ceiling and application-limited fixes.

### cronet-go

- `main` `8aa139e9e140` — target, root/all/lib pins unchanged.
- `go` `0d28acc44093` — **UNRESOLVED_HOLD**: old native build branch (`049f2909` assets) whose
  diff-too-large comparison cannot prove equivalence; main has the current 37-asset set.
- `jiejie-naive-zero-copy` `520c2873df45` — **UNRESOLVED_HOLD**: four unique commits are docs,
  benchmarks and test-only framing/callback coverage, not production code; not merged.
- Deleted: `feat/per-dial-quic` `fd2f25825e4f`, `dev` `1159fbf94b5f`, `go_dev` `c3902ec13951`,
  `fix/naive-codec-parity` `d83f1a1c6e7f` (all ancestors of main).

## Effective pins after the changes

```
quic-go    => Piggy-Cat-bit-shadow/quic-go v0.61.1-0.20261024063732-ce02ee5cc210 (sagernet line)
sing-quic  => Piggy-Cat-bit-shadow/sing-quic v0.7.2-0.20261024074641-5e5a0274bd6c (main)
sing-tun   => Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261024025840-dcf84577d0eb (dev)
sing       => Piggy-Cat-bit-shadow/sing v0.9.6-0.20261024041103-4200a04a089a (dev)
cronet     => root/all 8aa139e9e140; libs 8c68ce89873c
tailscale  => official v1.102.1-sing-box-1.14-mod.5.0.20261022222424-1688b52f8204
```

Root and nested `test` modules resolve to the same effective versions; `go mod verify` and builds
pass. No branch deletion changed a consumed commit SHA.

## Known reference and hold

`Piggy-Cat-bit-shadow/quic-go`'s `sagernet` branch still has `.github/workflows/verify.yml` with
`branches: [jiejie-masque-mod9]`. Updating that file would create a new commit on `sagernet` and
change the branch tip away from the immutable `ce02ee5` pin, so it was deliberately **not** changed;
the workflow remains dispatchable and the production build is unaffected. This is the only known
stale-name reference; historical docs may keep the old name as provenance.

## Final remote state

```
sing-box: testing 8795ba09d35, pending 0477e327e5d, ci/actions 8b0737c8362
sing-tun: dev dcf84577d0eb, fix/acceptloop-selfheal 1cd9bc2216df (hold)
sing:     dev 4200a04a089a
sing-quic: main 5e5a0274bd6c
quic-go:  sagernet ce02ee5cc210, metacubex beb42da71a55 (default)
cronet:   main 8aa139e9e140, go 0d28acc44093 (hold), jiejie-naive-zero-copy 520c2873df45 (hold)
```

`_p01_main` is clean at `8795ba09d35`; pre-existing detached release/bench worktrees were not touched.

## Phase 08 update

- CI closeout branch `8b0737c` merged into `testing` (`6fc9c59`) and removed; main repo now has only
  `testing` and `pending/device-axis-reload-window-tests` (`0477e327e5d`, unchanged).
- `quic-go`: `sagernet` is `f0f7ab2` and records `80c132f` (mod.12) as an ancestor; the old
  `verify.yml` trigger now names `sagernet`. Per explicit user instruction the independent
  `metacubex` line was deleted and `sagernet` is the repository default. `sing-quic/main` follows at
  `1ef2597`.
- `sing-tun/dev` is `ade2434` and records upstream `dev 611d83b` as an ancestor (behind 0).
- `sing/dev` remains `4200a04`, behind 17 upstream `dev` commits; this is the verified hold.
- `cronet-go/main` remains ahead 14 / behind 0; `go` and `jiejie-naive-zero-copy` are retained holds.
- Personal `gvisor` has no consumer and was archived; `sing-tun`'s `with_gvisor` source and
  `github.com/sagernet/gvisor` are untouched.
## Phase 09 final update

- `sing/dev` merged `SagerNet/sing/dev` `03a6cce6` at `e70473d`; all 17 upstream commits are
  accounted for and the target is an ancestor (`behind=0`). Root and nested test pins are
  `v0.9.6-0.20261024191059-e70473d8f2fb`.
- `sing-tun/fix/acceptloop-selfheal` (`1cd9bc`), `cronet-go/go` (`0d28acc`) and
  `cronet-go/jiejie-naive-zero-copy` (`520c287`) were deleted after source/consumer audit.
- Final remote branches: sing-box `testing` + `pending`; sing `dev`; sing-tun `dev`; sing-quic
  `main`; quic-go `sagernet`; cronet-go `main`.
- Final code SHA `234bc542d56`: Verify `40124382403`, Android `40124382415`, Mobile `40124382395`,
  all success.