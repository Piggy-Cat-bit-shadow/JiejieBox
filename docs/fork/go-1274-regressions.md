# Go 1.25.5 → 1.27.4: regressions and candidates found by the wider sweep

This is the hunting record for the toolchain upgrade, separate from the two reported incidents
(`go-1274-compatibility-report-2026-11-17.md`). It lists what was found, what was excluded **with the
evidence**, and what is a behaviour change to watch rather than a bug.

The method was not "read the release notes and guess". Every row below is one of:

- a **differential run** through `scripts/ci/go-toolchain-diff.sh <test> <package>`, which runs the
  same test under both toolchains and reports whether they disagree;
- a **source comparison** between the two toolchains' own trees;
- an **empirical probe** compiled and run against both.

---

## R1 — a manual provider refresh no longer reaches the data plane under go1.27.4

**Status: confirmed, reproducible, mechanism NOT yet isolated. No fix attempted.**

```text
$ scripts/ci/go-toolchain-diff.sh 'TestARecoveryPinsARemoteSubscriptionSnapshot' ./daemon

== comparison ==
go1.25.5:PASS
go1.27.4:FAIL

RESULT: the toolchains DISAGREE.
```

The failure:

```text
--- FAIL: TestARecoveryPinsARemoteSubscriptionSnapshot (0.33s)
    config_recovery_provider_test.go:262:
        Error: Should be false
        Messages: the changed subscription is what the running Box has now
```

### What the test establishes when it passes

A remote provider serves a working SOCKS node. The subscription document is then changed to a **dead**
node (`server_port: 1`), and `StartedService.RefreshProviders` is called. After it returns, the traffic
must go through the dead node - i.e. the running Box must have adopted the new document.

### What happens under 1.27.4

- The provider reports `status=published` (instrumented run, both toolchains), so the download, the
  parse and the provider's own state change all happen.
- The data plane keeps serving through the **old** node: the probe still succeeds at 0, 100, … 900 ms
  after the refresh returned. So this is **not** a timing window and **not** a test race.
- It is **not** "same tag, new address": changing the node's tag as well (`node` → `node2`) does not
  help either. The update does not propagate at all.

### Excluded causes, each measured

| Cause | How it was excluded |
|---|---|
| `encoding/json` backed by v2 (1.27) | `GOEXPERIMENT=nojsonv2` on 1.27.4: still FAIL |
| Green Tea GC (1.26 default) | `GOEXPERIMENT=nogreenteagc`: still FAIL |
| Container-aware GOMAXPROCS (1.25) | `GOMAXPROCS=1` and `GODEBUG=updatemaxprocs=0`: still FAIL |
| A slow/racy convergence | 900 ms of polling after the refresh returned: no convergence |
| Provider fetch skipped or served from cache | The provider reports `published` with the new document in the same run |
| Reload bookkeeping (the test reloads before refreshing) | Not isolated - see next steps; this is the remaining suspect |

### Why CI did not catch it

**`./daemon` is compiled by every gate and never executed by one.** `verify.yml` runs
`go test -run '^$' ./...` (type-check and link) and executes a curated list of packages that does not
include `daemon`. The regression was therefore invisible on a green branch, at the baseline commit as
well as here: it reproduces on `4dc2593fb` with no local changes.

### Why it matters beyond the test

`RefreshProviders` is the manual subscription refresh the UI exposes. Under 1.27 a user who refreshes a
subscription and then immediately selects or measures a node is still on the **old** node list, while
the call reports success. For this fork's iOS client that is the "subscription updated but the nodes
did not change" shape, and it feeds the same user-visible surface as the `command.sock` incident.

### Next steps, in order

1. Print the group's membership and the `adapter.Outbound` instance behind the member tag immediately
   after the refresh, and again after the `providerUpdateCheckScheduler` goroutine has run
   (`protocol/group/provider_members.go`). That decides between "the scheduler never ran", "the
   manager's graph validation rejected the commit", and "the selector kept the old instance".
2. If nothing changes, add a third toolchain (1.26.x) to narrow the change to 1.25→1.26 or 1.26→1.27.
3. Fix, then re-run the differential command above; it must report agreement.
4. Wire `./daemon` into a gate (with `-skip` for this test until it is fixed) so the class is no longer
   invisible.

---

## E — candidates examined and excluded

Each row was a real hypothesis with a reachable code path; the evidence column is why it is not a bug
for this repository at this toolchain.

| # | Candidate (Go change) | Reachable here? | Evidence |
|---|---|---|---|
| E1 | `encoding/json` now backed by v2 (1.27): duplicate names rejected, invalid UTF-8 rejected, error texts change | Yes - config decode, every report, every RPC document | **Empirical differential on both toolchains**: duplicate keys still last-wins (`err=nil`), invalid UTF-8 still replaced with U+FFFD on marshal and accepted on unmarshal, unknown fields still ignored. The v1 API keeps v1 semantics; the stricter rules belong to the new `encoding/json/v2` package |
| E2 | `net.UnixConn` reads return `io.EOF` instead of wrapping it in `net.OpError` (1.27) | Almost - the command socket is a UnixConn | No site classifies a **read** EOF through `net.OpError`: the only `net.OpError` handling is dial classification (`common/dialer`) and one construction in `common/ktls`. `errors.Is(err, io.EOF)` is unaffected |
| E3 | `crypto/tls` private methods pulled by `common/badtls` changed signature | Yes - the MITM/HTTP-capture path | **Identical declarations in both toolchains**: `(*Conn).readRecord`, `writeRecordLocked`, `(*halfConn).{decrypt,setErrorLocked,setTrafficSecret,explicitNonceLen}` |
| E4 | `net/http.readRequest`, `net/http.parseBasicAuth`, `net/url.(*URL).setPath` changed signature | Yes - `common/badhttp` | **Identical** in both; 1.27 adds `readRequestLimit` and leaves `readRequest` untouched |
| E5 | `runtime.fwdSig`, `handlingSig`, `_NSIG`, `getsig`, `setsig`, `sigtable` changed | Yes - the Darwin crash handler | **Identical declarations** in `runtime/signal_unix.go` and `signal_darwin.go` in both versions; `_NSIG` is still 32 and matches the handler's `_nsig` |
| E6 | `runtime/metrics` names used by OOM/power telemetry changed or moved | Yes - `service/oomkiller`, `service/powerreport`, `internal/memmetrics` | All nine names compare **byte-identical descriptions** in both toolchains' `runtime/metrics/description.go` (`/memory/classes/total:bytes`, `/gc/heap/live:bytes`, `/memory/classes/heap/{stacks,released,free}:bytes`, `/sched/goroutines:goroutines`, `/gc/gomemlimit:bytes`, `/gc/gogc:percent`, `/cpu/classes/gc/total:cpu-seconds`) |
| E7 | `crypto/{rand,ecdsa,rsa,ecdh}` ignore the caller's `rand` parameter (1.26) | Only if the process overrides `crypto/rand.Reader` | The repository never assigns `rand.Reader`; no code passes a custom reader into key generation |
| E8 | New vet checks fail the build: `printf` `%w`-with-pointer-to-error (1.27) and `go test` now running `stdversion` | Yes - every CI `go test` | `go test -run '^$' ./...` (which runs both checks) and the tagged `go build ./...` pass on the current tree |

## W — behaviour changes to watch (not bugs, no action yet)

| # | Change | Why it matters here |
|---|---|---|
| W1 | **Green Tea GC is the default collector from 1.26** (10-40% less GC overhead, different pacing) | The OOM killer's 50 MiB budget, `GOGC` target and `GOMEMLIMIT` were calibrated under the old collector. `GOEXPERIMENT=nogreenteagc` still exists in 1.27.4's flag list, but 1.26's notes say the opt-out is expected to be removed - assume it is permanent. Re-baseline the memory timeline on device before tuning anything further |
| W2 | `crypto/tls` default key exchanges now include `SecP256r1MLKEM768`/`SecP384r1MLKEM1024` (1.26) | ClientHello grows and carries PQ groups for every outbound that leaves `CurvePreferences` nil. `internal/godebugs` records `tlssecpmlkem Changed: 26, Old: "0"`, so a module declaring `go 1.25.5` keeps the pre-1.26 default - it flips the moment the `go` directive moves. Reasoned from the GODEBUG table, **not yet captured from a ClientHello**; the probe belongs in the next round |
| W3 | HTTP/1 `Response.Body.Close` now drains unread content (1.27): ≤256 KiB, ≤50 ms, in a goroutine | Every early-closed response body on the outbound HTTP paths now reads (and discards) up to 256 KiB after the consumer stops caring - measurable as data use on a metered link and as one extra goroutine per early close |
| W4 | HTTP/2 **server** honours client priority (1.27), disable with `Server.DisableClientPriority` | Inbound HTTP/2 servers (the API surface) change scheduling; irrelevant with one local client, worth knowing when it is exposed |
| W5 | `compress/flate` output bytes changed (1.27) | Gzip artifacts (report archives) differ byte-for-byte while remaining valid; anything comparing archive bytes to a golden value must not |
| W6 | **Darwin binaries require macOS 13+ from 1.27**; the linker defaults `minos` to 13.0 | The macOS client's deployment target must be ≥ 13, and any promise of older macOS has to be dropped with it. `.github/setup_go_for_macos1013.sh` is pinned at Go 1.26.8 and is referenced by **no workflow**: the legacy path is orphaned, and it cannot be rebuilt with the toolchain this repository now pins |
| W7 | `go test` runs the `stdversion` vet check (1.27); `gofmt` alignment changed | Using a 1.27-only stdlib symbol while `go.mod` still says `go 1.25.5` now fails `go test`. The `go` directive is the language/API floor, the `toolchain` line is the compiler - they move independently |

## The tool

```text
scripts/ci/go-toolchain-diff.sh <test-pattern> <package> [toolchain-root]...
```

With no roots given it reads `go.mod` and looks for the language floor and the pinned toolchain under
`/c/Deepseek/.tools/go<VERSION>`; both `bin/go` and `go/bin/go` layouts are accepted. It forces
`GOTOOLCHAIN=local` (otherwise the newer go command silently downloads and uses the pinned toolchain)
and `-count=1` (a cached PASS would compare toolchains it never ran on). Exit 1 means the toolchains
disagree - a behavioural change caused by the toolchain, which is the only direct evidence of one.
