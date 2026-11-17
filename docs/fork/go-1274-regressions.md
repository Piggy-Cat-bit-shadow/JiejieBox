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

## R1 — a connection opened before a subscription refresh keeps using the replaced node

**Status: resolved. The behaviour difference is real and is now documented; the test that exposed it was
asserting more than the contract, and has been corrected. No product change.**

```text
$ scripts/ci/go-toolchain-diff.sh 'TestARecoveryPinsARemoteSubscriptionSnapshot' ./daemon

== comparison ==
go1.25.5:PASS
go1.27.4:FAIL              (before the test correction)

RESULT: the toolchains DISAGREE.
```

### The first reading, and why it was wrong

The test asserted that immediately after `StartedService.RefreshProviders` returned, traffic stopped
flowing through the node the subscription had just replaced. Under 1.27.4 it kept flowing, and the
provider reported `status=published`, so the first reading was "the refresh does not reach the data
plane". That reading is **wrong**, and the probe that disproved it was one line: ask the same question
over a **new** connection.

```text
reused connection : 200 under 1.27.4, 500/absent under 1.25.5   <- the difference
new connection    : 502 under BOTH toolchains                    <- the data plane is correct
```

A 502 is the HTTP inbound failing to dial the dead node: the running Box **had** adopted the new
document on both toolchains. What differs is only what happens to a connection that was established
before the refresh.

### What the contract actually says

`Selector.onProviderUpdated` documents it:

> Replacing the OBJECT behind the same tag is a subscription refresh, not a switch: the in-flight
> dial's lease keeps the old object open and the dial must be allowed to finish.

So a pooled connection continuing through the replaced node is the *designed* behaviour. Whether it
does comes down to **when the retired object's connections are closed**, which is a lifetime property
rather than a routing one - and that lifetime moved between go1.25.5 and go1.27.4. The mechanism is
still unidentified (see the remaining question below); the user-visible consequence is bounded to
connections that already existed when the refresh landed, and new connections are routed correctly on
both toolchains.

### What was fixed

Nothing in the product. `TestARecoveryPinsARemoteSubscriptionSnapshot` now asks its post-refresh
questions over a **fresh connection** (`servedOnANewConnection`), with a comment recording why: a reused
connection answers a different question, and asserting on it made the test depend on the retirement
timing of a node object. The recovery assertion (line 271) uses the same instrument, so it can no
longer pass on a stale pooled connection either.

The differential command above now reports `go1.25.5:PASS / go1.27.4:PASS`.

### Why CI did not see it, and what changed

**`./daemon` was compiled by every gate and executed by none.** `verify.yml` runs
`go test -run '^$' ./...` (type-check and link) plus a curated list of packages that did not include
`daemon`, so the package could be broken for months on a green branch. `verify.yml` now runs the
provider and recovery suites by name, behind the same anti-vacuity guard as the other gates - which is
also how this test's contract will stay honest.

### Remaining question, for the next round

Which Go change moved the retirement lifetime? Candidates worth a bisect (add a 1.26 toolchain to
`go-toolchain-diff.sh` to split the range): the 1.27 HTTP/1 `Response.Body.Close` drain (it changes
when a connection is considered reusable and closed), and the 1.26 Green Tea GC's effect on finalizer
and cleanup timing if retirement is driven by `runtime.AddCleanup`. Neither is demonstrated; both are
one experiment away.

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
