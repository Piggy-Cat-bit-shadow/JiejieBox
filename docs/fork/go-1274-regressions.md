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

## R8 — the one-shot cleanup still closed a connection it did not own

**Status: fixed. Found by the second review of this round (2026-11-18), in the cleanup branch the lease fix
had missed.**

The failure path learned to invalidate only the connection a call ran on (R3). The CLEANUP path did not:
`callWithResult`/`callManagedWithResult` still ran `defer c.closeConnection()`, which closes whatever
connection is current when the call returns. A one-shot client whose older call was slow to unwind could
therefore tear down a NEW connection that a later call had just established.

The same `if c.standalone { c.closeConnection() }` block existed at sixteen more sites - every stream and
session teardown (`RefreshRemoteProviders`, the network-quality / STUN / tailscale-ping / tailscale-ssh
sessions, USB devices, taildrop inbox and download). Fixing only the two wrappers would have left the class
in place, so all of them now call `releaseStandaloneConnection(lease)`, which invalidates only while the
call's epoch is still the client's current one. `closeConnection` itself remains for the two places that
own the client's lifecycle: `connect` (a deliberate replacement) and `Disconnect`.

**The test.** `TestAFinishedStandaloneCallCannotCloseANewerConnection`: one-shot client, call one
establishes and finishes (taking its own connection), call two establishes a new connection and is still
in flight, then call one's teardown is invoked with its lease. The new connection must survive, and the
last one-shot call must still close its own. Negative control: restoring `closeConnection` in the helper
makes it fail with *"a finished call must not close the connection a newer call is using"*.

The test invokes the old teardown directly rather than racing for the interleaving, and says so: the
harmful ordering needs the older call to be slow to return after its transport died (it still has a 150 ms
probe to run before its defer), which is a scheduler property rather than a code path. Invoking the
teardown with the old lease is the same defect, deterministically.

**What is still true, and now written down instead of implied:** a one-shot client's contract is one
operation at a time. Two calls racing on ONE standalone client share a connection by design, and the last
one out closes it - so the wrapper still closes the connection when its epoch IS the current one. What
changed is that a teardown can no longer reach backwards and take a newer connection with it.

## R7 — the Linux race job found a data race in one of this round's own tests

**Status: fixed. Test-only; no product code was racing.**

Adding `./experimental/libbox` to the push gate's race leg (it had none - see the step's comment) made the
first Linux `-race` run fail on `TestACancellationDuringAProviderRefreshKeepsTheBox`:

```text
WARNING: DATA RACE
Read at ... by goroutine 49:
  ...TestACancellationDuringAProviderRefreshKeepsTheBox.func3()   provider_refresh_cancellation_e2e_test.go:66
Previous write at ... by goroutine 44:
  ...TestACancellationDuringAProviderRefreshKeepsTheBox.func2()   provider_refresh_cancellation_e2e_test.go:41
```

`firstServed` was a plain `bool`: written by the httptest handler's goroutine, read by the assertion's
polling closure. Windows happened not to schedule the pair that way; Linux did. It is now an
`atomic.Bool`, and the race leg is in the gate so the next one of these is caught at push time rather than
by review.

Why this is worth a row rather than a silent fix: a test-only race is reported by the same `FAIL` line as a
product race, so a reader who only sees the log cannot tell them apart. The distinction has to be in the
record.

## R6 — a manual provider refresh answered "busy" after 150 ms, no matter what the caller asked

**Status: fixed. Found while chasing the second CI failure of the hidden-provider test.**

```text
the provider must publish the new document: the provider's own refresh held it for the whole bounded wait
```

The message came from `refreshOneProvider` in `daemon/provider_refresh.go`: it waited for a provider whose
own timer held it with a FIXED budget - three attempts 50 ms apart, about 150 ms - and then reported
`busy`. The caller's `timeout` was applied to the provider's own fetch, but not to this wait.

A manual refresh is the caller saying "do it now". If the provider is already downloading, the right answer
is to wait for that download (it publishes the same document), not to answer "busy" after 150 ms - and a UI
that offers "refresh now" gets an error for pressing it at the wrong moment. On a 12-core workstation the
automatic update finishes inside the window; on a 2-CPU runner it does not, which is why the daemon test
failed on CI and passed locally. The W8 retry made the automatic download slightly longer and therefore
easier to hit.

**The fix.** The wait is a time budget derived from the caller's timeout, clamped to `[200 ms, 2 s]`
(`busyWaitBudget`): a caller does not sit through its whole deadline on a "busy" answer, and a near-zero
timeout does not disable the wait. `TestRefreshWaitsOutAProviderItsOwnTimerHolds` now asserts the budget
(`elapsed >= minBusyWait`, `elapsed < timeout`, more than one attempt) instead of a fixed call count.

**The second half was the test's own bug.** The RPC maps the kernel's typed status to a string
(`provider_refresh_rpc.go`), and the hidden-provider test's setup assertion compared that string against
typed constants, so it could never match. The setup now accepts "the document is in effect"
(`published` or `unchanged`) rather than "this call is the one that installed it", which is the honest
requirement: the provider downloads on its own schedule and either order is correct.

**Verification.** On the shape that failed: Linux, two CPUs, the test alone ×6 and the whole package
(9.9 s) green; Windows whole package green (41.5 s); CI green with the test in the gate.

## R3–R5 — the independent review's findings (2026-11-18), all fixed

An external review of this round found three defects in the control-channel work. They are recorded as
the reviewer stated them, with the fix, the test that now holds the line, and the negative control that
proves the test would have caught the original defect.

### R3 [P1] — a stale failure closed the NEW connection

**The defect.** `classifyCommandFailure` closed "the client's connection" whenever the probe said
nothing was listening. By the time a failure is classified, that connection may no longer be the
client's current one: if another goroutine re-established in the meantime, the late failure cleared the
**new** connection - healthy, newer than the failure, and carrying the live subscriptions. The locking was
correct, the decision was about the wrong object, which is why the existing race tests could not exclude
it.

**The fix.** Every call now carries a `commandCallLease`: the epoch of the connection it ran on and the
endpoint that connection points at. `invalidateConnection(epoch)` compares the epoch and closes **under
one lock acquisition**, so a concurrent install cannot slip between check and close. A stale lease
neither closes anything nor is reported as `ErrCommandServerNotRunning`, because the client is in fact
connected; the original error is forwarded unchanged. File: `experimental/libbox/command_endpoint.go`,
`command_client.go`.

**The test.** `TestAStaleFailureCannotInvalidateANewerConnection` (two real gRPC servers: establish on A,
re-establish on B, stop A, then classify a failure carrying A's lease) plus
`TestAConnectionReplacedBetweenCallsIsNotClosedByAnOlderFailure` for the primitive, and
`TestTheLeaseEndpointIsTheOneTheCallUsed` for the endpoint half.

**Negative control.** Restoring the old body (`_ = c.invalidateConnection(lease.epoch); c.closeConnection()`)
makes the test fail with `a failure of an older connection must not be reported as 'the core is not
running' while a newer one is live: command server is not running: ...`.

### R4 [P1] — the socket claim could still delete a live instance's socket, and could not tell "unknown"

**The defect.** Two halves. (a) `claimCommandSocket` was check-then-act: between "nothing answers" and
`os.Remove`, another instance could bind the path, and the removal then deleted a **live** socket. (b)
The liveness probe returned a bool, so a timeout, a permission error or an unsupported transport was
indistinguishable from "nothing is listening" - and the path was deleted on that guess.

**The fix.** (a) An exclusive, non-blocking ownership lock on a sibling file (`<socket>.lock`, `flock`
on unix, `LockFileEx(LOCKFILE_FAIL_IMMEDIATELY)` on Windows) is taken **before** the probe and held for
the life of the listener. A second instance fails immediately with "another instance owns ..." and never
reaches the probe. (b) The probe is now three-valued (`socketStale` / `socketLive` / `socketUnknown`);
only `socketStale` may be removed, and `socketUnknown` refuses. A mode check additionally refuses
anything that is neither a socket nor a regular file, so an empty directory at the socket name can no
longer be `os.Remove`d on a stale probe. Files: `command_socket.go`,
`command_socket_lock_unix.go`, `command_socket_lock_windows.go`, `command_server.go` (lock lifecycle).

The lock orders instances **of this package**. A foreign process that binds the path without taking the
lock can still race us, which is why the probe is kept as a second check rather than removed.

**The tests.** `TestTheOwnershipLockSerialisesInstances`, `TestASecondInstanceCannotDeleteALiveSocket`
(real unix listener; the second acquire fails and the first stays reachable),
`TestAcquireCommandSocketReleasesTheLockOnClaimFailure`, `TestClaimCommandSocketRefusesWhenItCannotTell`,
`TestClaimCommandSocketRefusesADirectoryWithOurName`.

**Negative control.** Restoring the boolean semantics (any non-live answer clears the path) makes
`TestClaimCommandSocketRefusesWhenItCannotTell` fail with the file already deleted.

### R5 [P2] — the error contract promised more than a probe can know

**The defect.** `ErrCommandServerNotRunning` was documented as "the request was never delivered and
nothing was applied". A post-hoc probe cannot establish that: the request may have been delivered,
executed, and the reply lost with the transport. The Apple-side contract inherited the overpromise.

**The fix.** The contract now says what it proves - "the endpoint was unreachable at classification
time" - and explicitly not what it cannot: delivery and application are unknown. The UI guidance changed
with it: re-issue means **re-assert the desired state** (read the group's expansion, set it only if it
differs), never "repeat the command". Files: `command_endpoint.go` (error and probe docs),
`docs/fork/ios-command-client-state.md` §2.1, §2.2, §3. `TestProbeCommandServerDoesNotClaimHistory`
pins that the probe document carries no claim about earlier requests.

## R2 — a hidden-provider test raced the provider's first download

**Status: fixed as a test dependency; the underlying product behaviour is R2b/W8.**

Found by the daemon gate on its first CI run, and only because that step was changed in the same
commit to name failing tests in a public annotation: the check run said
`daemon gate failing tests: TestHiddenURLTestSurvivesAProviderRepublication`, which is the whole
diagnosis for a reader without admin access to the job logs.

The test asserted the provider's node was already a member immediately after `StartOrReloadService`:

```go
started := newHiddenGroupService(t, profile)
require.Equal(t, []string{"pool/node-1"}, liveGroupMembers(t, started, "REGION"))
```

A **remote** provider downloads on its own schedule, so the group is legitimately empty until the first
publication lands. On a warm 12-core host the download wins the race; on a cold CI runner it does not.
Every later step in the same test already waits (`waitForMembers`), so the first assertion was the only
one depending on machine speed; it now uses the same bounded wait (15 s) and asserts the same
expectation. Its siblings that assert members immediately use `inline` providers or static outbounds,
which are built synchronously - checked, not assumed.

Why this is worth recording separately from R1: it is the same area (provider republication reaching a
hidden group under go1.27.4) and it would have looked like a product regression to whoever found it
next. The differential was the instrument that separated them -
`go-toolchain-diff.sh TestHiddenURLTestSurvivesAProviderRepublication ./daemon` reports agreement on
both toolchains, so the failure was environmental ordering, not a behaviour change.

### R2b — the real cause: the provider's first download is aborted on Linux (resolved)

Running that test alone, repeatedly, gave the answer — and it is not about the dialect at all. The
provider logs it:

```text
ERROR provider/remote[pool]: update outbound provider: download provider document:
      Get "http://127.0.0.1:43307/sub.json": network changed while dialling
```

`network changed while dialling` is **this fork's own error**, raised when the interface monitor
reports a change during a dial. On Linux that event lands inside the provider's very FIRST download, so
the download fails, the provider stays empty, and the test's 15-second wait expires. On Windows the
event ordering differs and the first download survives - which is why the failure looked
platform-dependent, and why the package run passed while the same test alone failed: earlier tests had
already absorbed the interface events.

**Two things followed.**

- The test no longer depends on the start-time download: it publishes through the manual refresh
  (`RefreshRemoteProviders`, the production path and what its sibling tests use) before asserting
  membership. Verified on Linux: 6/6 separate-process runs, the whole package green; on Windows:
  repeated focused runs and the whole package green. It is back in the push gate, with no `-skip`.
- The product observation is recorded as **W8** below, not fixed here: the first provider download can
  fail on an interface event and the subscription stays empty until the next `update_interval` (an hour
  in this profile) or a manual refresh.

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
| W2 | `crypto/tls` default key exchanges now include `SecP256r1MLKEM768`/`SecP384r1MLKEM1024` (1.26) | **MEASURED 2026-11-18** with `scripts/ci/clienthello-probe` (three runs, same host): `go 1.25.5` directive + 1.27.4 toolchain → `[4588 29 23 24 25]`; `go 1.27.4` directive + 1.27.4 → `[4588 4587 4589 29 23 24 25]`; `go 1.25.5` + 1.25.5 → `[4588 29 23 24 25]`. So the `tlssecpmlkem` gate follows the **`go` line, not the toolchain**, and this repository (go 1.25.5) keeps the pre-1.26 set even when built by 1.27.4. Two consequences: today's binaries are unchanged, and the ClientHello grows the day the directive moves - which is a deliberate decision to take then, not a silent upgrade. (X25519MLKEM768, group 4588, was already offered under 1.25.5: PQ was not introduced by this migration) |
| W3 | HTTP/1 `Response.Body.Close` now drains unread content (1.27): ≤256 KiB, ≤50 ms, in a goroutine | Every early-closed response body on the outbound HTTP paths now reads (and discards) up to 256 KiB after the consumer stops caring - measurable as data use on a metered link and as one extra goroutine per early close |
| W4 | HTTP/2 **server** honours client priority (1.27), disable with `Server.DisableClientPriority` | Inbound HTTP/2 servers (the API surface) change scheduling; irrelevant with one local client, worth knowing when it is exposed |
| W5 | `compress/flate` output bytes changed (1.27) | Gzip artifacts (report archives) differ byte-for-byte while remaining valid; anything comparing archive bytes to a golden value must not |
| W6 | **Darwin binaries require macOS 13+ from 1.27**; the linker defaults `minos` to 13.0 | The macOS client's deployment target must be ≥ 13, and any promise of older macOS has to be dropped with it. `.github/setup_go_for_macos1013.sh` is pinned at Go 1.26.8 and is referenced by **no workflow**: the legacy path is orphaned, and it cannot be rebuilt with the toolchain this repository now pins |
| W7 | `go test` runs the `stdversion` vet check (1.27); `gofmt` alignment changed | Using a 1.27-only stdlib symbol while `go.mod` still says `go 1.25.5` now fails `go test`. The `go` directive is the language/API floor, the `toolchain` line is the compiler - they move independently |
| W8 | A provider's **first** download can be aborted by this fork's own `network changed while dialling`, and the subscription then stays empty until `update_interval` (an hour in the hidden-provider profile) or a manual refresh | **FIXED** on 2026-11-18: `provider/remote` retries the download up to `providerDownloadRetries` times with a short backoff, only for `dialer.IsNetworkChanged` - the one failure that provably never reached the server, so a retry cannot double-apply. `dialer.ErrNetworkChanged` and `dialer.IsNetworkChanged` are exported for it. Tests: `provider/remote/download_retry_test.go` (retried once, not retried for an ordinary transport error, bounded, cancelled context). Negative control: disabling the retry makes the first test fail |

## The tool

```text
scripts/ci/go-toolchain-diff.sh <test-pattern> <package> [toolchain-root]...
```

With no roots given it reads `go.mod` and looks for the language floor and the pinned toolchain under
`/c/Deepseek/.tools/go<VERSION>`; both `bin/go` and `go/bin/go` layouts are accepted. It forces
`GOTOOLCHAIN=local` (otherwise the newer go command silently downloads and uses the pinned toolchain)
and `-count=1` (a cached PASS would compare toolchains it never ran on). Exit 1 means the toolchains
disagree - a behavioural change caused by the toolchain, which is the only direct evidence of one.
