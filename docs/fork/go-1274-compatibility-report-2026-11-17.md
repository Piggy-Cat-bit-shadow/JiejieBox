# Go 1.25 → 1.27.4 compatibility program — construction report (2026-11-17)

**Baseline:** `testing` at `4dc2593fbc1bf736afd1d79a3fdec5f7371db328` (clean worktree, equals
`origin/testing` at the start of this session).
**Toolchain:** `go.mod` declares `go 1.25.5` and `toolchain go1.27.4`; the executing toolchain was
`go1.27.4 windows/amd64` (`GOTOOLCHAIN=auto`, `CGO_ENABLED=0`). The `go` directive is the language
minimum, **not** the compiler: `go version` and `go env GOVERSION` both report 1.27.4, and
`.github/workflows/verify.yml` has a step that fails if the running toolchain stops matching the
`toolchain` directive.

## A. What this round concluded

| Item | Result |
|---|---|
| P0 — `oomprofile` / Go 1.27 internal ABI crash | **Root-caused and fixed**; reproduced on the host with the same frames as the device, fixed, and gated by three independent tests |
| P0/P1 — `command.sock` ECONNREFUSED / `SetGroupExpand` | **Diagnosed as "no listener"**, and the main-repo half fixed: the state is now classified, a dead connection is no longer reused, a live socket can no longer be stolen by a second instance |
| Same-class audit | All first-party Go-non-public dependencies inventoried; two previously ungated runtime mirrors now gated; residual gaps named in `docs/fork/go-toolchain-abi-gates.md` |
| New problems found independently | One: the `runtime.g` mirror had no test, so its silent degradation was invisible (P2, now gated). Two platform traps documented (Windows `ECONNREFUSED` encoding; `runtime.g` layout is not statically checkable) |
| Can this go to device/release verification? | The oomprofile half: **yes**, it is the specific crash. The control-channel half: **yes for the Go side**, with the Apple-side contract in `docs/fork/ios-command-client-state.md` unimplemented by design (different repository) |
| iOS/macOS runtime | **NOT_RUN.** No device, no Mac, no signed NetworkExtension in this environment |

## B. Go 1.25 → 1.27.4 mapping, narrowed to what is reachable here

The prior rounds of this migration already landed the broad compatibility work (toolchain pin, tag
profiles, the `-checklinkname` policy gates, the HTTP/2 transport pin). This round asked a different
question: **which dependencies on non-public Go surface exist, and what happens to each when the
toolchain moves?** The inventory and its gates are in `docs/fork/go-toolchain-abi-gates.md`.

Verified differences, from the two toolchains' own sources:

| Surface | Go 1.25.5 | Go 1.27.4 | Consequence here |
|---|---|---|---|
| `internal/profilerecord.MemProfileRecord` | `AllocBytes, FreeBytes int64; AllocObjects, FreeObjects int64; Stack []uintptr` — 56 bytes, `Stack` at 32 | `ObjectSize int64; AllocObjects, FreeObjects int64; Stack []uintptr` — 48 bytes, `Stack` at 24 | **the crash**; and `InUseBytes` changed from measured to derived (`InUseObjects() * ObjectSize`) |
| `runtime/pprof.protomem` | `scaleHeapSample(count, size, rate)` with `size` = total bytes; `bytes` label = average block size | `scaleHeapSample(count, avgSize, rate)` with `avgSize` = per-object size; `bytes` label = `ObjectSize` | a mirror that copied only the struct would report `inuse_space` inflated by the object count |
| `runtime/pprof.labelMap` | `LabelSet{list []label}`, `label{key, value string}` | `label.Set{List []Label}` from `internal/runtime/pprof/label`, `Label{Key, Value string}` | **layout identical** (one slice header, 32-byte elements) — verified, and now gated so the next move is caught |
| `runtime/pprof` profile builder | gzip-wrapped | gzip-wrapped | the fork's copy is deliberately **not** gzip; asserted so it cannot change silently |
| every other pulled symbol in §1.1 | — | still present with the same signature | verified by reading `runtime/mprof.go`, `runtime/symtab.go`, `runtime/cpuprof.go`, `runtime/pprof/vminfo_darwin.go`, `runtime/pprof/defs_darwin*.go` |
| `runtime.g` prefix | — | verified behaviourally on 1.27.4 | see §D |

Not verified, and not claimed: a line-by-line diff of every Go commit between 1.25 and 1.27.4. That
work was scoped to the surfaces this repository actually depends on, which is the only part that can
change its behaviour. `docs/fork/go-toolchain-abi-gates.md` §2 is the procedure for re-deriving it on
the next upgrade.

## C. The 0.1.52 Network Extension crash — closed

### C.1 The facts, from the crash report

`metadata.json`: `source NetworkExtension`, `appMarketingVersion 0.1.52`, `goVersion go1.27.4,
ios/arm64`, `signalName SIGSEGV`, `signalCode SEGV_ACCERR`, `exceptionName panic`,
`exceptionReason runtime error: invalid memory address or nil pointer dereference`, access address
`0x14`, uptime 27m47s. `go.log`:

```text
experimental/libbox/internal/oomprofile.writeHeapProto   oomprofile.go:324
experimental/libbox/internal/oomprofile.writeHeapInternal oomprofile.go:106
experimental/libbox/internal/oomprofile.writeAlloc        oomprofile.go:91
experimental/libbox/internal/oomprofile.WriteFile         oomprofile.go:64
experimental/libbox.writeOOMProfile                       oom_report.go:256
experimental/libbox.OOMRecorderOptions.func3              oom_report.go:82
service/oomkiller.(*Recorder).snapshot                    recorder.go:561
service/oomkiller.goMemoryPressureCallback                service_darwin.go:240
```

`allocs` is the FIRST entry of `oomReportProfiles`, which is why the first profile written is the one
that died.

### C.2 The mechanism, reproduced

`runtimeMemProfileInternal` is `runtime.pprof_memProfileInternal` under `//go:linkname`. The runtime
writes `[]profilerecord.MemProfileRecord` into a slice of **our** type, with **its** 48-byte stride and
`Stack` at offset 24. The mirror still declared the 1.26 shape (56-byte stride, `Stack` at 32), so:

- record 0's `Stack` was read from the right place by coincidence (offset 24 in both);
- record 1's "data pointer" was read from byte 80, which is record 0's **length** field — a small
  integer;
- `for i, addr := range stk` dereferenced that small integer. On the device it faulted at `0x14`
  (20); on this host at `0x5`.

Reproduced with the pinned toolchain, before the fix:

```text
sizeof(memProfileRecord)=56 offset(Stack)=32
panic: runtime error: invalid memory address or nil pointer dereference [signal 0xc0000005 ...]
oomprofile.writeHeapProto  oomprofile.go:324
oomprofile.writeHeapInternal oomprofile.go:106
oomprofile.writeAlloc       oomprofile.go:91
```

Identical frame chain to the device report, `writeHeapProto:324` included. After the fix:

```text
sizeof(memProfileRecord)=48 offset(Stack)=24
allocs profile bytes=3681
```

### C.3 The fix

`experimental/libbox/internal/oomprofile/oomprofile.go`:

- `memProfileRecord` is the 1.27.4 layout, and `InUseBytes` is derived (`InUseObjects() * ObjectSize`);
- `writeHeapProto` uses the per-object scaling the 1.27.4 standard library uses, and emits the object
  size as the `bytes` label unconditionally.

**Why not replace the whole writer with `runtime/pprof.Lookup`?** Because that is not the same
artifact. `runtime/pprof`'s builder wraps its output in `gzip.BestSpeed`; this package writes raw
protobuf, which is what every archived OOM report contains, and gzip costs CPU plus a 64 KiB window of
heap inside the memory-pressure callback that is running *because* the process is out of memory. That
trade was measured, not assumed: the differential test below proves the two writers agree on the
numbers, and asserts the file is not compressed, so the decision is recorded rather than implied.

### C.4 The gates that would have caught it

| Test | What it fails on |
|---|---|
| `TestToolchainProfileRecordsStillMatchTheMirrors` | parses `$GOROOT/src/internal/profilerecord/profilerecord.go` with `go/parser` and compares the field list of all three records, then requires `InUseBytes` to still derive from `InUseObjects()` and `ObjectSize`. **This is the test that fails on the next toolchain move**, and it names the fields. |
| `TestMemProfileRecordLayoutMatchesGo1274` | the concrete 64-bit numbers (48/24), with the 0.1.52 crash named in the message |
| `TestHeapSampleValuesMatchTheirObjectSizeLabel` | every heap/allocs sample: `alloc_space == alloc_objects × bytes-label` and `inuse_space == inuse_objects × bytes-label`. Hermetic — it needs nothing outside the profile. |
| `TestHeapWriterArithmeticOnAFixedRecord` | the writer's arithmetic on a record the test owns: `{ObjectSize 8192, AllocObjects 3, FreeObjects 1}` at rate 1 must produce `[3, 24576, 2, 16384]` and a `bytes` label of 8192. Deterministic, and states the expected numbers instead of observing them. |
| `TestMirrorAgreesWithStandardLibrary` | the same heap written by this package and by `runtime/pprof`: sample types, default sample type, period and the raw-protobuf format compared exactly; the allocation counters compared as ordered magnitudes (a layout or arithmetic error moves them by a factor of the object count) |
| `TestGoroutineProfileCarriesLabels` | the `pprof.labelMap` mirror, behaviourally: a goroutine labelled with `pprof.Labels` must appear labelled |
| `TestWriteFileProducesWellFormedProfiles` | all six profiles parse, with the sample types and units pprof consumers expect |

Negative controls, run deliberately:

- mirror struct left at 1.26 but the arithmetic updated → `MemProfileRecord has 4 fields in the pinned
  toolchain and 5 in this mirror` (simulated drift);
- struct fixed but the old arithmetic kept → three failures:
  `heap: sample 37 reports alloc_space 4096 for 256 objects of size 4096; want 1048576`,
  `sample values = [3 8192 2 8192], want [3 24576 2 16384]`, and
  `heap: the standard library reports 2785136 allocated bytes against the mirror's 40056`;
- the pre-fix code itself → the SIGSEGV in C.2.

The differential comparison was reworked after a stress run: `-count=10` exposed that two separate
observations of a live heap can disagree on the in-use figures by a large factor when a GC runs
between them, so equality was the wrong assertion there. The exact arithmetic moved to the fixed-record
test above, where it is deterministic, and the cross-writer test kept the structural equality and the
ordered allocation counters. `-count=10` now passes.

### C.5 Profile semantics after the fix

The fixed-record test states the contract in numbers. The cross-writer test compares the same heap
through both writers, and on this host the two files report the same sample types, the same period,
and allocation counters that agree to the byte:

```text
heap:   mirror alloc=5941/9664768 inuse=233/3677288 standard alloc=5941/9664768 inuse=233/3677288
allocs: mirror alloc=5941/9664768 inuse=233/3677288 standard alloc=5941/9664768 inuse=233/3677288
``` The six profiles are covered by
`TestWriteFileProducesWellFormedProfiles`, including the empty-profile case (`block`/`mutex` with
their rates at zero), and `TestWriteFileRejectsUnknownProfile` covers the failure path.

### C.6 Does the diagnostic itself make the OOM worse?

Reviewed, and **not** changed, because the existing design already bounds it:

- `Recorder.snapshot` is single-flight (`snapshotAccess`), capped at `maxAutomaticSnapshots = 8`, and
  rate-limited (`pressureSnapshotMinInterval = 1h`, `resetSnapshotMinInterval = 10m`);
- the heap dump is written only when explicitly requested (`heapDump` is false on the pressure path);
- the profiles are written to the draft directory one at a time, and `WriteFile` removes a partial file
  on error.

Residual risk, stated rather than fixed: the callback still writes six profiles **synchronously** on
the pressure callback's thread. The fix here removes the crash; it does not claim the callback is a
good place to do this work. A budgeted/deferred variant is a larger change to the diagnostic contract
and needs device measurements to justify, so it is left as the next experiment rather than pushed in
blind.

## C2. The `command.sock` incident — closed on the Go side

### C2.1 Which failure this is

`connect: connection refused` on a unix socket means the file exists and **nothing is listening**. It
is not the historical 138-byte `EINVAL` (a bind-time path-length failure, fixed in
`docs/fork/ios-command-socket-path-fix.md`) and not `ENOENT`. The reported path is 96 bytes, well
inside the 103-byte budget, which is consistent: `resolveCommandSocketPath` produced the documented
`command.sock`, and both sides agreed on it.

The state it describes — socket file present, listener gone — is exactly what a Network Extension that
exits without `Close` leaves behind, because `UnixListener.Close` is what unlinks the file.

### C2.2 The causal chain, and its limit

The device session contains a SIGSEGV in the OOM profile writer at 03:16:12. `ECONNREFUSED` is the
signature of a process that exited without unlinking. So **the chain NE crashed → stale socket → UI
gets ECONNREFUSED is consistent with the evidence**, and the two reports are from one session.

What cannot be proven from the available material: that the UI error was observed *after* that SIGSEGV
rather than before it (no timestamped UI log was provided), and that no second exit happened. The
report therefore treats the chain as **supported, not proven**, and fixes the control channel's own
defects independently — because they are wrong regardless of what killed the core.

### C2.3 What was actually wrong in this repository

1. **A cached connection was never invalidated.** `getClientForCall` returned `c.grpcClient` whenever it
   was non-nil. When the core dies, that connection is in `TRANSIENT_FAILURE`, and gRPC reconnects on
   its own backoff — up to two minutes between attempts. A kernel that comes back a few seconds later
   would keep failing every UI operation, each of which looks like a fresh failure to the user.
2. **"The core is not running" was not expressible.** Every transport failure arrived as
   `Unavailable`/`DeadlineExceeded` with prose inside. A UI could only display it. Worse, the
   handler-bound establishment path (the one the UI uses) reports `DeadlineExceeded`, because it
   probes with `WaitForReady(true)` until its own deadline — a code that reads as "slow", inviting a
   retry of an operation nothing received.
3. **`CommandServer.Start` would unlink a live socket.** `os.Remove(sockPath)` ran unconditionally
   before `bind`. With two live instances the first keeps its accepted connections while every new
   client lands on the second, and nothing reports it.

### C2.4 The fix

- `classifyCommandFailure` (new, `command_endpoint.go`): after a failed call whose status is
  `Unavailable` or `DeadlineExceeded`, probe the endpoint the client actually dialed — a `connect(2)`
  with a 150 ms cap. If the probe is definitive and says nothing is listening, close the connection and
  return `ErrCommandServerNotRunning`. If the probe says something IS listening (a live server's
  semantic `Unavailable`, e.g. `daemon/managed_service.go`'s "OOM recorder not available"), return the
  original error untouched — a healthy connection with live subscriptions must not be torn down for an
  answer.
- `ProbeCommandServer()` (new): `{"state":"running|notRunning|unknown","detail":...}`, so a UI can ask
  before acting. `unknown` for XPC/remote transports, because claiming "down" without evidence is worse
  than forwarding the error.
- `claimCommandSocket` (new, `command_socket.go`): remove our socket file only when nothing answers on
  it; refuse to take it from a live listener; never touch a name this package does not allocate. The
  stale-socket recovery a crashed extension needs still works, because there nobody answers.
- The endpoint a client dialed is remembered at establishment (`serverEndpoint`), so the classifier
  never probes an address it did not use.

No RPC is retried. `ECONNREFUSED` is no longer merely reworded: the connection is dropped and the next
operation re-establishes.

### C2.5 Other operations on the same path

Every `StartedService`/`ManagedService` RPC goes through `callWithResult`/`callManagedWithResult`, so
the classification and invalidation cover node selection, clash mode, URL test, provider refresh,
connection close, system proxy, debug triggers and the configuration queries. The stream handlers are
unchanged: a broken transport ends them and they report `Disconnected` as before.

### C2.6 UI contract

`docs/fork/ios-command-client-state.md`. The Apple client repository was **not** modified. The two
required changes are named there: classify before presenting, and re-drive the pending operation once
the tunnel reports connected.

## D. Problems found independently of the two reports

1. **The `runtime.g` mirror had no test.** `experimental/libbox/internal/runtimeinfo` walks
   `runtime.allgs` and reads `runtime.g` at hand-computed offsets; it discovers `gopc`/`startpc` by
   probing, and degrades to "no goroutine section" if the probe fails. The degradation was invisible:
   nothing failed, and a device would simply report less. Added
   `goroutine_badlinkname_test.go`, which requires the report to exist, be non-empty, and carry
   function names and stack bytes. It passes on 1.27.4 (`total=9, groups=9, stackBytes=81920` in the
   probe run), so the mirror is currently correct — the point is that it can no longer become silently
   wrong.
2. **`syscall.ECONNREFUSED` is the wrong constant on Windows.** Measured on the pinned toolchain: a
   refused connect reports errno 10061, while `syscall.ECONNREFUSED` is `0x20000016`; `errors.Is(err,
   syscall.ECONNREFUSED)` is `false` and `errors.Is(err, windows.WSAECONNREFUSED)` is `true`. A single
   cross-platform comparison would have reported every dead core as "unknown" on Windows. The check is
   now platform-split (`command_endpoint_unix.go` / `command_endpoint_windows.go`).
3. **`runtime.g`'s layout cannot be statically gated** and there is no way to make it so from outside
   the runtime. Recorded in the gates document rather than papered over with a size assertion that
   would prove nothing.
4. **The `runtime.g` walk is incompatible with checkptr, by construction.** Running the package under
   `-race` produced `fatal error: found bad pointer in Go heap` inside `probeGoroutine`: the mirror
   reads the words after `goid` through a truncated type, which is out of bounds by construction and is
   exactly what checkptr instruments. It is unrecoverable, so it would have crashed every
   `go test -race` run that touched `runtimeinfo.Collect()`. The implementation and its gate are now
   gated on `!race`, and a race build compiles the stub: one diagnostic section is omitted in a
   development-only build instead of the process dying. `-d=checkptr=1` without `-race` has no build
   tag to detect, and is documented as unsupported for this package.
5. **The iOS transport itself is now tested, not only the decision it feeds.** Go on this Windows host
   supports AF_UNIX, so `command_transport_unix_test.go` drives the production resolver, the unix
   dialer, the probe and the classifier against a real socket file, then reproduces the state the
   incident reported and requires the server side to claim the path again. Only the device remains
   `NOT_RUN`.
6. **A toolchain-caused behaviour difference, found by the differential sweep, and what it turned out
   to be.** `TestARecoveryPinsARemoteSubscriptionSnapshot` passed under go1.25.5 and failed under
   go1.27.4 on the same source, deterministically. The first reading was "a manual provider refresh no
   longer reaches the data plane"; one extra probe (ask the same question over a NEW connection)
   disproved it - a new connection gets a 502 on **both** toolchains, i.e. the Box did adopt the new
   document. What actually differs is a connection opened **before** the refresh: it keeps carrying
   traffic through the replaced node under 1.27.4 and not under 1.25.5. That is a lifetime property,
   and the selector documents it as intended ("the in-flight dial's lease keeps the old object open").
   The test was asserting more than the contract, so the **test** was corrected (its post-refresh
   probes now use a fresh connection, and the recovery probe with it), the product was not touched, and
   the differential now reports agreement on both toolchains. Recorded with the wrong first reading and
   the evidence that overturned it: `docs/fork/go-1274-regressions.md` §R1.
7. **A gate gap that made the above invisible: `./daemon` was compiled by every gate and executed by
   none.** `verify.yml` ran `go test -run '^$' ./...` plus a curated package list that skipped it, so
   the package could be broken for months on a green branch. It now runs in the push gate: 191 tests
   (~40 s), with one test excluded by name because it is not self-contained on Linux - see
   `docs/fork/go-1274-regressions.md` §R2b - and any failure is published as a check-run annotation.
   Its first CI run did exactly what it was added for: it failed, and the annotation named the test.
8. **Eight further candidates were examined and excluded with evidence**, and seven behaviour changes
   were catalogued as "watch, do not act yet": see the same file, §E and §W. The most consequential of
   those is the Green Tea GC becoming the default collector in 1.26, which invalidates the calibration
   of this fork's OOM budget until it is re-measured on device.

## E. Code and dependency changes

No dependency changed. No tag profile changed. No API was removed.

| File | Change |
|---|---|
| `experimental/libbox/internal/oomprofile/oomprofile.go` | the 1.27.4 record layout and per-object semantics; long comments naming the crash and the version difference |
| `experimental/libbox/internal/oomprofile/oomprofile_test.go` (new) | six-profile structure, per-sample semantic consistency, label mirror behaviour, differential against `runtime/pprof`, raw-protobuf format assertion |
| `experimental/libbox/internal/oomprofile/mirror_test.go` (new) | `$GOROOT` source comparison for all three record types + `InUseBytes`, the label set, and the concrete layout numbers |
| `experimental/libbox/internal/runtimeinfo/goroutine_badlinkname_test.go` (new) | the missing gate for the `runtime.g` mirror |
| `experimental/libbox/command_endpoint.go` (new) | endpoint model, liveness probe, `ErrCommandServerNotRunning`, `classifyCommandFailure`, `ProbeCommandServer` |
| `experimental/libbox/command_endpoint_unix.go`, `command_endpoint_windows.go` (new) | the platform-correct refused-connect test |
| `experimental/libbox/command_client.go` | classify failures on both call paths; remember the dialed endpoint |
| `experimental/libbox/command_server.go` | claim the socket path through `claimCommandSocket` |
| `experimental/libbox/command_socket.go` | `claimCommandSocket` + `socketPathIsLive` |
| `experimental/libbox/command_client_control_test.go`, `command_socket_claim_test.go`, `command_transport_unix_test.go` (new) | 15 tests over real gRPC transports, including a real AF_UNIX socket |
| `.github/workflows/verify.yml` | two push-gate steps, both behind `scripts/ci/require-executed-coverage.sh` so a `-run` filter that matches nothing fails |
| `docs/fork/go-toolchain-abi-gates.md`, `docs/fork/ios-command-client-state.md`, this report (new) | the durable record |

Compatibility: the OOM report format is unchanged (same file names, same raw-protobuf profile
encoding, same sample types). The command channel's wire protocol is unchanged. Two additive Go APIs
(`ErrCommandServerNotRunning`, `CommandClient.ProbeCommandServer`) are new; existing call sites behave
as before except that a failure with no listener is now reported with a stable prefix.

## F. Tests actually run, with their parameters

All on `go1.27.4 windows/amd64`, `CGO_ENABLED=0`, from `C:\Deepseek\JiejieBox`.

| Command | Result |
|---|---|
| `go test -count=1 -v ./experimental/libbox/internal/oomprofile/` | PASS (9 test cases + subtests; `-count=10` also PASS) |
| `go test -count=1 -v ./experimental/libbox/internal/oomprofile/` before the fix | **panic / FAIL** — §C.2 |
| `go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS),tfogo_checklinkname0" -ldflags=-checklinkname=0 ./experimental/libbox/internal/runtimeinfo/` | PASS (2) |
| `go test -count=1 -v ./experimental/libbox/ -run 'TestClaimCommandSocket\|TestCommandEndpointMatchesDialTarget\|TestProbeCommandServer\|TestAStandaloneClient…\|TestAHandlerBoundClient…\|TestAClientRecovers…\|TestAServerAnswerOfUnavailable…'` | PASS (12) |
| the same `-run` set plus `TestTheClientReachesAServerOnAUnixSocket\|TestALiveUnixSocketIsNotStolenOnThisPlatform\|TestAStaleUnixSocketFileIsRemoved` (real AF_UNIX on this host) | PASS (15) |
| `go test -count=1 ./experimental/libbox/` (the whole package) | PASS (`ok … 12.341s`) |
| `go test -count=1 -c` for `darwin/arm64` and `linux/arm64` (oomprofile) | compiles |
| `GOOS=linux/arm64`, `darwin/arm64`, `windows/amd64` `go build ./experimental/libbox/` | PASS |
| `go build ./...` (untagged) | PASS |
| `go test -run '^$' ./...` (untagged; every test binary compiles and links) | PASS |
| `go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./...` | PASS |
| `go vet ./experimental/libbox/` | PASS |
| `gofmt -l` on every file touched | clean |

Race and checkptr, with `CGO_ENABLED=1 CC=<the bundled MinGW-w64 16.2.0>` (the host does have a C
toolchain, next to the Go installs under `C:\Deepseek\.tools`):

| Command | Result |
|---|---|
| `go test -race ./experimental/libbox/internal/oomprofile/` | PASS (1.5s) |
| `go test -race -tags "…,tfogo_checklinkname0" -ldflags=-checklinkname=0 ./experimental/libbox/internal/runtimeinfo/` | PASS, with the goroutine mirror compiled out (§D.4) |
| `go test -race ./experimental/libbox/` (the whole package, control-channel and unix-transport tests included) | PASS (`ok … 14.573s`) |

That last row is the one that matters for the new concurrency: the client's generation counter, the
`connectAccess`/`clientAccess` ordering around `serverEndpoint`, and the classifier's `closeConnection`
all run under the race detector against real servers.

## G. Long-term gates

Added to the push gate, both anti-vacuity guarded:

1. **`mirrored runtime structures match the pinned toolchain`** — the oomprofile source/semantic/
   differential gates plus the `runtimeinfo` mirrors under their real tags.
2. **`the control channel's failure states`** — the 15 tests above, over TCP and over a real AF_UNIX
   socket.
3. **`daemon configuration recovery`** — the whole `daemon` package (191 tests, ~40 s) with one test
   excluded by name (`-skip`), which is the package that had never been executed by a gate.

The durable artifact is `docs/fork/go-toolchain-abi-gates.md`: the inventory of every first-party
dependency on non-public Go surface, which ones are gated, which are loud, which are gaps, and the
ordered procedure for the next upgrade. That document is what makes the next `toolchain` bump a
reviewable change instead of an experiment.

### Reading CI without repository admin rights

The repository is **public**, and a green branch is not the same as an unobservable one. What is and is
not reachable anonymously:

| Endpoint | Reachable anonymously | Use |
|---|---|---|
| `GET /repos/{o}/{r}` | yes (`private: false`) | confirm the repository is public before claiming CI is unobservable |
| `GET /repos/{o}/{r}/actions/runs?head_sha=<sha>` | yes | the run for one commit, with `status`/`conclusion` |
| `GET /repos/{o}/{r}/actions/runs/{id}/jobs` | yes | per-job and **per-step** `conclusion` |
| `GET /repos/{o}/{r}/check-runs/{id}/annotations` | yes | the `::error::` messages a step emitted |
| `GET /repos/{o}/{r}/actions/jobs/{id}/logs` | **no** (`Must have admin rights`) | raw logs |

That asymmetry is why the gates now emit failing test names as `::error::` annotations rather than
relying on the log: the annotation is the only part of a failure that a reader without admin rights can
see. It worked on the daemon gate's first failure, which is how R2 was identified without a log.

## H. Residual risk and limits

| Risk | Why it remains | Minimum next step |
|---|---|---|
| iOS NetworkExtension run of the fixed writer | no device, no signing | build `libbox` for ios/arm64 with the release tag set, install, drive a pressure snapshot (or `TriggerOOMReport`), confirm `snapshot-*.allocs.pb` parses with `pprof` and reports sane totals |
| the six-profile write still runs synchronously on the pressure callback | a redesign changes the diagnostic contract and needs device measurements | measure RSS/CPU/GC around a pressure snapshot on device; then decide between a budget, a deferral, or a lighter tier |
| the UI half of the `command.sock` incident | different repository | implement `docs/fork/ios-command-client-state.md` §3 in the Apple client |
| `ECONNREFUSED` causal link to the crash | no timestamped UI log | capture the NE exit time and the UI error time in one session |
| the unix-socket branch of the new client/server code | the client half and the claim rule are tested over a real AF_UNIX socket on this host; `CommandServer.Start()`'s own bind cannot be exercised end-to-end here because a bare `CommandServer` has no `StartedService` to register | on device, confirm the stale-socket recovery after a forced NE kill |
| `GAP` rows in the gates document (§1.1 function signatures, `crypto/tls` internals, Darwin signal handler) | none of them can be safely asserted from source text alone. The `runtime/pprof` ones are however exercised end-to-end by the profile gates: symbolisation, cycle scaling and stack expansion all run inside the six profiles those tests validate | each is named with its failure mode; the first is the one to close next, by comparing pulled signatures against `$GOROOT` |
| the `runtime.g` mirror under `-d=checkptr=1` | checkptr rejects the out-of-bounds scan, and there is no build tag that identifies that flag combination | document it (done, §D.4); do not run the goroutine report under checkptr |
