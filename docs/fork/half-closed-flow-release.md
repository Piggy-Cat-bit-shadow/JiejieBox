# Releasing a half-closed flow whose peer is gone

The report: on the phone's Activity page several connections to Bilibili hosts stayed **Active** for
about two hours after the application was closed, all with a `createdAt` of `05:02:18` and `0 kB/s`,
and only "Close All Connections" removed them.

This is the record of what each layer actually said, the one state that had no owner, and the
mechanism that now releases it.

## 1. What each layer said, and why the core already held the entries

| Layer | What it means for "Active" | Where |
| --- | --- | --- |
| Hako row | `closedAt == nil` renders the Active badge; the last line is `format(createdAt)`; an active row shows the *rate* (`0 kB/s`), a closed one shows the duration | Apple client `HakoConnectionView.swift:143`, `:271`, `:293` |
| Activity filter | the default filter is `.active`, and it is applied by `closedAt == 0` in libbox's store | `ConnectionListViewModel.swift:233`, `command_types.go:242-261` |
| Client store | fed only by the event stream: `apply(events)` on a `LibboxConnections` | `CommandClient.swift:618-620` |
| Close-all | calls the API and does **not** clear the store locally, so rows can only disappear through a closed event | `ConnectionListViewModel.swift:288-290` |
| Daemon | seeds the stream from the traffic manager and reconciles **once a second**: any ID in its snapshot that is no longer in the active table gets a closed event | `daemon/started_service.go:961`, `:985`, `:993`, `:1031`, `:1117-1210` |
| traffic manager | an entry leaves only in `leave()`, which is reached only by a tracker's `Close`/`CloseFlow` | `common/trafficcontrol/tracker.go:96-144`, `manager.go:89-120` |

The chain is therefore closed: rows disappeared **after** close-all, and close-all does not touch the
client's copy, so the closed events arrived through a live subscription, so the daemon's reconcile was
running, so the entries were still in the core's active table when the user looked. The UI was
faithful; it was reporting a real held flow.

## 2. The state with no owner

A stream has two copy directions, and each direction is also the only observer of its own peer. When
the upload direction ends **cleanly** (the client sent FIN, so the read returned EOF), that direction
is finished and its goroutine exits:

- its read side is at EOF forever, so it can never block on the peer again;
- its write side is only used when the surviving direction has something to send, which for a silent
  server is never.

If the peer's socket then disappears, no error is raised anywhere. Neither manager has a
steady-state clock that could notice: `route/conn_reclaim.go`'s idle record is read only during a
network transition, and `common/trafficcontrol` only reacts to a close. So the flow stays in both
layers' tables, and only close-all can release it.

Two measurements decide what a fix may rely on:

| Measured | Result |
| --- | --- |
| Kernel socket, peer FIN then full close, keepalive armed, polls the read side for 12s | reads keep returning **EOF**; no terminal error is ever reported |
| Same socket, polled with **writes** | terminal error within 500ms (`WSAECONNABORTED`) |
| Userspace stack (`*tun.GoConn`) after the device's FIN, keepalive armed 1s/1s/3, nothing answered | a later `Read` reports `go: keepalive timeout: connection timed out` in 3.8s |

So on a **kernel socket** the finished side cannot be interrogated at all without writing data, while
on the **userspace stack** (which is what the phone's TUN inbound uses) the stack's own keepalive
turns a vanished peer into a real read error. That difference is the whole design space, and it is
why "just add a timeout" is not the answer: the connection in this state is indistinguishable, by
construction, from `curl -X POST` waiting on a slow backend.

## 3. The mechanism

`route/conn_halfclose.go`, started from the copy direction that just returned EOF, for the **upload**
arm only (the download arm's finished side is the outbound socket, whose liveness the surviving
direction already observes by writing):

1. arm a keepalive on the finished side — idle 20s, interval 10s, count 3, **only for flows that have
   half-closed**, so a live peer's kernel answers without waking its application and a peer whose
   socket is gone answers with RST;
2. poll that side, 15s backing off to 2min: a read that has already returned EOF never blocks again,
   so a reset can only be seen by a read that happens later;
3. release the flow **only** on a terminal error. `io.EOF` never releases, a local read deadline never
   releases, `net.ErrClosed` (this side was closed by the surviving direction) stops the observer
   without a verdict, and an unexpected byte stops it rather than eating stream data.

The teardown is now one `flowTeardown` with a `sync.Once`, shared by both copy directions and the
observer. `done.Swap(true)` was correct for exactly two participants; the observer is a third, and
`onClose` still runs exactly once (proven under a forced race).

Nothing is killed on elapsed time, zero bytes, a one-sided EOF alone, or "the app is not in the
background". A silent peer that still owns its socket answers the probes and keeps its connection for
as long as it likes.

## 4. The line a real-device run reports

One log line at tunnel shutdown, in the same shape as the splice and transition summaries:

```
half-closed flows: 7 observed, released 7 (peer reset 6, keepalive timeout 1), stopped without a verdict 0
```

Acceptance on the phone needs no instrumentation:

1. start the tunnel, use the applications that produce the reported rows (Bilibili is the original);
2. close them, so their sockets send FIN;
3. leave the phone idle for a minute or two;
4. stop the tunnel and read the line above.

`observed` counts half-closed flows taken under observation, `released` counts those whose finished
side reported a terminal error, and the split says whether the peer reset us or nothing answered at
all. A flow whose peer is still alive is observed and never released, which is the intended result for
a suspended application.

## 5. What this does not cover

- **A kernel-socket inbound** (mixed, redirect) whose peer vanishes after its FIN. Measured above:
  the read side reports EOF forever and only a write surfaces the reset. This is recorded as a
  characterisation test (`e2e/connection_lifecycle_test.go`,
  `TestTrafficTrackerHalfClosedKernelPeerIsNotObservable`) rather than papered over with a timeout.
  The phone's own path is the TUN inbound, which is the one this mechanism does cover.
- **An application the system suspended** (no FIN at all). The OS still owns the socket, the
  connection is genuinely open, and no probe can distinguish it from one the user is about to resume.
- It is not a fix for the platform's own keepalive idle: sing-tun's userspace stack initialises it from
  `tcpEstablishedTimeout` (2h04m, upstream behaviour in `stack_go_conn.go:319`), so before this change
  nothing was going to act on an idle flow for about two hours, and even then there was no reader left
  to see it.

## 6. Gates

```
go build -tags <DEFAULT_BUILD_TAGS_OTHERS> ./...                              ok
go vet  -tags <...> ./route ./protocol/tun ./e2e ./common/trafficcontrol       ok
go test -tags <...> ./route -count=1                                          ok
go test -tags <...> ./protocol/tun ./e2e ./common/trafficcontrol ./daemon      ok
go test -tags <...> ./experimental/libbox ./experimental/clashapi .            ok
go test -tags <...> -race ./route ./protocol/tun ./e2e ./common/trafficcontrol ./daemon ./experimental/libbox   ok
go test -tags <...> -run TestTrafficTracker ./e2e -count=5                    ok
go test -tags <...> -run TestTUNDevice ./protocol/tun -count=10               ok
negative control: observer disabled                                           the TUN test fails with
                                                                              "the traffic table holds 1
                                                                              connections, want 0"
```

The race runs need cgo; on this Windows host they were run with `CC=C:\msys64\mingw64\bin\gcc.exe`
and `CGO_ENABLED=1`. The full `./route` package under `-race` found one real defect in the first
version of the observer - the timer handle was stored from two goroutines, because the callback can
start before the assignment completes - and that handle is no longer stored at all; there is nothing
to stop, since a late probe checks the teardown and returns.

`Verify` ran on `38dad31db` and passed, including the "race on the fork's own data paths" step that
had failed on `5034c5ee0` for exactly that race:
<https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39699826372>.

**One gate that is NOT green on this host, and must not be claimed as such.** `go test ./...`
additionally fails three `common/tls` tests - `TestWindowsClientHandshakeTLS13`,
`TestWindowsClientRoundtripTLS13`, `TestWindowsClientTLS13PostHandshakeConcurrentWrite` - which
`KNOWLEDGE/ACCEPTANCE.md:219` records as `NETWORK_DEPENDENT` ("this host's Schannel has no
TLS-1.3-over-TCP"). They cannot depend on this work: `go list -deps ./common/tls` does not include
`route`. That is why the list above names packages rather than `./...`, and why the commit message of
`38dad31db` claiming `go test ./... ok` was wrong on this host and is corrected here.


Tests, by what they pin:

| File | Pins |
| --- | --- |
| `route/conn_halfclose_test.go` | the decision (release on a peer verdict, never on EOF / a local deadline / an unexpected byte), the capability walk, the diagnostics counters, once-only teardown over 20 rounds |
| `route/tun_halfclose_test.go` | the real userspace stack: an unanswered probe becomes a terminal read error the observer acts on; a device that answers keeps the flow; the shutdown line |
| `protocol/tun/flow_release_test.go` | a device FIN and a device RST each release the tracked flow, in the userspace-copy and the splice-eligible shape |
| `e2e/connection_lifecycle_test.go` | a real box: clean FIN, RST, close-all and shutdown release the traffic entry; the kernel-socket limit is a characterisation |

## 7. Provenance

`5034c5ee0466bc3027eda6ff98d9e8162ef364c6` on `testing` is the mechanism and its tests.
`38dad31dbe38194121de963c3cc6009f9287760c` is the diagnostics line, the data race the race detector
found in the observer's own scheduler, and this record; `Verify` passed on it. The Apple client was
read at its pinned submodule commit `cdaaf2226ea659bd3a5d5dec056d3c6e769dc3ec`; nothing in the client
was changed, because the client was reporting the truth.


