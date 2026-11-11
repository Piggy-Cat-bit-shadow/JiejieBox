# Upstream debug and validation

Reproducible checks for the upstream sync rounds. Everything below was run on
Windows/amd64 with Go 1.25.5 unless marked otherwise; nothing here is a claim
about CI or devices.

## Environment

```sh
export PATH="/c/Deepseek/_gotool/go/bin:$PATH"        # Windows: C:\Deepseek\_gotool\go\bin
export GOPRIVATE=github.com/Piggy-Cat-bit-shadow/*
export GOFLAGS=-mod=mod
# race needs cgo on this host:
export CGO_ENABLED=1
export CC=C:\msys64\mingw64\bin\gcc.exe
```

## sing (stage 02)

Worktree `C:\Deepseek\sing-fork`, branch `dev`, merge commit `4abfaed`.

```sh
go build ./...
go test ./common/bufio/... ./common/network/... ./protocol/socks/... ./common/control/... ./common/domain/...
go test ./...
go test -race -count=1 -skip '^TestWriteVectorisedPacket$' ./common/bufio/... ./common/network/... ./protocol/socks/...
```

Results: build 0; targeted 0; `./...` 0; race 0 with that one test skipped. The
skipped test fails on the untouched `vectorised_windows.go` with a pre-existing
`checkptr: converted pointer straddles multiple allocations`; it is not caused
by the merge and is tracked separately.

**Pre-merge proof** (`common/bufio/cache.go` restored from `HEAD~`):

```sh
go test -count=1 -run TestCached ./common/bufio
# FAIL: replayed cached payload; ReaderReplaceable stuck false
go test -race -count=1 -run TestCachedConnReadCloseRace ./common/bufio
# WARNING: DATA RACE (CachedConn.Read c.buffer vs Close write; Buffer.Release vs Buffer.Read)
```

The barrier test is `common/bufio/cache_test.go`; it drives 200 independent
Read/Close pairs and asserts the payload is delivered exactly once.

## sing-tun (stage 02)

Worktree `C:\Deepseek\sing-tun-fork`, branch `dev`, merge commit `99abf3c`.

```sh
go build ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go vet ./...
go test -count=1 -skip '^TestGoKernel' .
go test -race -count=1 -skip '^TestGoKernel' -run 'TestMemoryTun|TestUDPNat|TestGoUDP|TestDetach|TestGSOSplit|TestSystemUDP|TestQueueClose|TestWaiter' .
```

Results: builds 0; root suite (kernel tests skipped) ok, 112 top-level PASS;
race ok; `test/go.mod` untouched.

**New tests** (`tun_offload_gso_batch_test.go`, `stack_system_udp_reply_test.go`):

```sh
go test -count=1 -run '^(TestGSOSplit.*|TestSystemUDPReplyHeaders.*)$' .
```

Mutation checks: ignoring the `segment` argument makes `GSOSplit` run forever;
deleting `ipHdr.SetID` makes two replies share IPv4 ID 0.

**Blocked**: all 21 `TestGoKernel*` (TUN adapter creation needs Administrator)
and Linux-only privileged/netns/iptables tests. They are cross-compiled/vetted,
never executed; do not report them as PASS.

## Linux execution (WSL, stage 02)

The Windows-only limits were closed as far as this host allows:

```sh
# sing, native Linux
go build ./... && go test -count=1 ./... && go test -race -count=1 ./common/bufio ./common/network ./protocol/socks

# sing-tun, TUN tests inside an unprivileged user+net namespace
unshare -Urn bash -c 'ip link set lo up; go test -count=1 ./...'
```

sing: build/test/race all pass. sing-tun: the full suite passes except
`TestGoMemoryDial/ipv6=false/unreachable`, which also fails on the pre-merge
commit `57e9d17` in the same namespace (a userns does not deliver the ICMP
unreachable the test expects); it is an environment limit, not a merge
regression. Evidence: `audit-evidence/upstream/linux/`.

## Kernel consumption (stage 02)

After bumping `go.mod` and `test/go.mod`:

```sh
go build ./...
go build -tags <windows profile> ./...
go test -count=1 ./transport/device ./protocol/tun
go test -vet=off -count=1 -tags <windows profile> ./route
```

Full Windows suite (DLL on PATH, serialized packages):

```sh
go test -vet=off -count=1 -p 1 -tags <windows profile> -timeout 60m ./...
```

Result: `protocol/mixed`, `protocol/naive`, `protocol/mieru` now pass. Remaining
failures are the three known baseline packages (`common/tls`, `common/tlsspoof`,
`common/windivert`) and a flaky `common/artifact` lease test that passes three
consecutive standalone runs (test isolation under the full run, unrelated to the
pin bump).

`TestPinnedSourceIsTheReplacedFork` verifies the replace names a module path
and version; `TestSystemReadLoopSurvivesATransientReadError/too_many_segments`
pins the kernel's explicit `ErrTooManySegments` recoverability.

## GitHub Compare

```sh
curl -s 'https://api.github.com/repos/SagerNet/sing/compare/Piggy-Cat-bit-shadow:dev...SagerNet:dev'   | jq '.ahead_by'
curl -s 'https://api.github.com/repos/SagerNet/sing-tun/compare/Piggy-Cat-bit-shadow:dev...SagerNet:dev' | jq '.ahead_by'
```

Both return 0 (`ahead_by` is the upstream-only count; the fork is `behind_by`
65/183, i.e. ahead).

## Evidence index

- `_int0145/audit-evidence/upstream/ahead-behind.tsv` — audit snapshot.
- `.../trial-merge-*.txt` — pre-merge conflict sizing.
- `.../sing-merge-msg.txt`, `singtun-merge-msg.txt`, `kernel-sync2-msg.txt` —
  commit rationale/commands.
- Merge commits: `4abfaed`, `99abf3c`; kernel `33d6e4c`.

## Transport layer (stage 03)

### sing-quic

```sh
cd _deps/sing-quic
go build ./...
go test -count=1 ./...
go test -count=1 -run 'TestSalamanderReadFromRunt' ./hysteria2
go test -count=1 ./congestion_meta1 ./congestion_meta2 ./hysteria/congestion
```

All pass. The Salamander test fails on the pre-merge `hysteria2/salamander.go`
(runt reads return 7/8 bytes instead of 0); the merged file passes.

### quic-go

```sh
cd _sync-quicgo
go test -count=1 ./internal/congestion
go test -count=1 ./...
```

Full suite passes on both sagernet and the consumed integration/0.1.45 line. Restoring the pre-merge CUBIC ordering makes
`TestCubicACKOnlyDoesNotConsumePacer` fail on its first assertion.

### utls

```sh
cd _dep_audit/utls
go test -count=1 -run TestUQUICPresetTransportParametersReachClientHello ./...
go test -count=1 ./...
```

Both pass. Correct upstream is `MetaCubeX/utls:v1.9.0-mod-meta`; the repository
default `v1.7.0-mod-meta` must not be used.

### WireGuard

```sh
cd _scratch/wgmerge
git merge-base --is-ancestor origin/main HEAD
go test -count=1 ./...
```

Ancestry passes; `device` tests pass. `go build ./...` at the repository root
reports `function main is undeclared` by design (platform-specific main), so use
`go test`/package builds instead.

### Linux kernel transport sweep (stage 03)

```sh
# in WSL, from a synced kernel tree
go build -tags <linux profile> ./...
go test -vet=off -count=1 -tags <linux profile>   ./transport/v2rayquic ./transport/masque ./protocol/hysteria2   ./protocol/tuic ./transport/wireguard ./protocol/masque
```

Both pass. This sweep caught the Linux-only bridge backend still calling
`ReadRetry.Wait(err)` after the stage-02 sing-tun bump; the fix (`8cc1dbcc63f`)
also keeps `ErrTooManySegments` recoverable and skips its backoff.

### Kernel consumers

```sh
go test -vet=off -count=1 -tags <windows profile> \
  ./transport/v2rayquic ./transport/v2rayxhttp ./transport/masque \
  ./protocol/masque ./protocol/hysteria2 ./protocol/tuic \
  ./transport/wireguard ./protocol/naive
```

All pass with the stage-03 pins. Full suite (`-p 1`, DLL on PATH): only the three known baseline packages (`common/tls`, `common/tlsspoof`, `common/windivert`) fail; everything else passes. Log: `kernel-windows-full-3.txt`.

## Client and protocol layer (stage 04)

```sh
# Android
JAVA_HOME=<jdk17> ANDROID_HOME=<sdk> ./gradlew --no-daemon :app:assembleDebug
JAVA_HOME=<jdk17> ANDROID_HOME=<sdk> ./gradlew --no-daemon :app:testOtherDebugUnitTest

# Dashboard (Node 26.7.0 / pnpm 11.13.0)
pnpm install --frozen-lockfile && pnpm generate && pnpm test && pnpm build

# Desktop
pnpm test && pnpm build

# Kernel consumers (DLL on PATH, windows profile tags)
go test -vet=off -count=1 -tags <profile> \
  ./protocol/naive ./protocol/masque ./transport/masque ./protocol/mieru
go test -race -count=1 -tags with_xhttp ./transport/v2rayxhttp
```

Results: Android build 8m28s + unit 27s; dashboard 97/97 (main) and 98/98
(integration), build 0; desktop 59/59 + build 0; naive 4.5s, MASQUE 8.6/7.1s,
mieru 94.4s, XHTTP race 21.5s. `pnpm lint:css` still reports four pre-existing
errors on the fork-only ProfileViews rules.

Dashboard's worktree needs the materialised `@shared/subscription.ts` and the
`vendor/iterm2-color-schemes` submodule before `pnpm generate`/tests; these are
build environment setup, not tracked changes.
