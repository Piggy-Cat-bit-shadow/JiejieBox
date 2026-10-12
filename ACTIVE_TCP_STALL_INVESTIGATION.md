# Active TCP forward-progress stall — evidence-first investigation

> **Status: the mechanism is PROVEN and REPRODUCED. The production fix is NOT applied.**
> The evidence establishes that the core can hold a silently-dead proxied flow open indefinitely and
> has nothing in steady state that would end it; that a stalled flow carries a **blocked writer** as
> well as a parked reader on the same socket, so it is not merely quiet, and can be told from a
> healthy idle connection without an idle timeout (§4a). It does not establish that this mechanism is
> what the reporter saw on Windows, because no field capture (goroutine dump, flow state) exists from
> the live event. The brief's gate is >= 90 % confidence; measured honestly this is **85 %**, and the
> brief is explicit that evidence comes before the fix rather than instead of it.
>
> **Terminology.** This document previously called the state a "mutual deadlock". That was the wrong
> name and it is corrected throughout: a reader and a writer on one TCP socket is ordinary and
> permitted. The anomaly is the whole set - writer blocked because the send buffer cannot drain,
> reader with no forward progress, both persistent, with no EOF, no RST, no error and no context
> cancel, while the ConnectionManager still counts the flow as active. This is an
> **ACTIVE FLOW NO-FORWARD-PROGRESS STALL**, and it is named that from here on.

```text
BASE_SHA              = 68de08d92bb6606c6e67644ab3b6ff07411ef1e9   (branch `testing`)
FINAL_SHA             = 68de08d92bb6606c6e67644ab3b6ff07411ef1e9   (test-only files added, uncommitted)
BRANCH                = testing
KB_BASELINE_SHA       = a267e99e0bf1811dc975b013e2e8d7a15ab40d23   (tag v0.1.6)
DELTA                 = v0.1.6..HEAD changes NO .go source; knowledge base describes this code
PRODUCTION_CODE_CHANGED = NO
```

---

## 1. Real-world evidence, and what it can and cannot carry

Reported, from two platforms and two different outbounds:

| | iPhone | Windows |
|---|---|---|
| Trigger seen | screen off for tens of seconds, then on | none — no sleep/wake involved |
| Observable | the answer stream stops mid-generation | the same |
| Everything else | VPN healthy, other traffic fine | VPN healthy, other traffic fine |
| Recovery | a NEW connection works | a NEW connection works |
| Outbound | `mixed-in` → TCP → VLESS Reality | `mixed-in` → AnyTLS → `ws.chatgpt.com:443` |

The log evidence also shows `ws.chatgpt.com` connections that **do** close and re-dial about three
seconds after establishment, and some that show only establishment. As the brief says, an open
long-lived WebSocket with no close line is **not** evidence of a stall. No conclusion is drawn
from it here.

Evidence level for everything in this section: **VERIFIED_LOG** (as reported),
**INFERRED** for any causal reading.

---

## 2. The real TCP lifecycle map

Answers the brief's §6 questions against the code that exists, not against the names in the brief.
Several names in the brief do not exist in this tree; the real ones are given.

```text
application socket
  │
  ├─ inbound/mixed ────────────────────────── adapter/inbound.go
  │     accepts, sniffs, resolves the destination
  │
  ├─ route/route.go  ── rule match, outbound selection
  │
  ├─ route/conn.go : ConnectionManager.NewConnection
  │     ├─ enterDialGate            (dial_governor.go — inert unless a transition just opened)
  │     ├─ dialSetups.begin(ctx)    (dial_setup.go — transition can cancel a dial still in flight)
  │     ├─ dialer.DialSerialNetwork
  │     │     └─ common/dialer/default.go : trackConn  ← THE REGISTRATION POINT
  │     │           connectionManager.TrackConn(conn)  → managedConnState{createdAt, generation,
  │     │                                                lastActive, activityObservable}
  │     ├─ finishSetup()            ← setup authority ENDS at establishment
  │     ├─ ReportConnHandshakeSuccess
  │     ├─ spliceConnection         (splice.go — TUN source only; refuses for mixed-in)
  │     ├─ kickWriteHandshake       (one write deadline, handshake only, then cleared)
  │     └─ go connectionCopy(upload)   +   go connectionCopy(download)
  │            └─ bufio.CopyWithIncreateBuffer → copyExtended → Read/Write
  │
  └─ teardown: the LAST copy to finish calls onClose and closes both ends
```

Who knows what:

| Question | Answer |
|---|---|
| Who creates the connection? | `defaultDialer` |
| Who owns it? | `ConnectionManager.connections` (a `list.List[managedConn]`) |
| Who records active? | `TrackConn` → `initConnState` |
| Who knows the last RX/TX? | `managedConnState.touch()`, stored as a **whole second** |
| Who knows EOF / RST? | the copy loops — `bufio.Copy*` returns and `connectionCopy` reacts |
| Who knows the network generation? | `managedConnState.generation`, set at dial |
| Who can block forever? | **both copy goroutines**, in `Read`, with no deadline |
| Who ends a stalled flow? | **only `Reclaim`** — see §3 |

---

## 3. The decisive finding

**`ConnectionManager.Reclaim` has exactly one production call site.**

```text
route/network.go:914     reclaimer.Reclaim(reason)
```

It is reached only from the network-reset path, and the reason it passes is
`ReclaimNetworkTransition`, or `ReclaimDeadPath` when the device has been *confirmed* offline for
`HardTransitionConfirm`. Every other caller of `Reclaim` in the tree is a `_test.go` file.

Three supporting facts, each verified in the source:

1. **No timer exists in steady state.** `ConnectionManager` owns exactly one timer,
   `sweepTimer`, and it is created only inside `beginDrainSweep`, which is reached only from a
   drain, which is reached only from `Reclaim`. No transition ⇒ no timer ⇒ no periodic sweep.
2. **The activity clock is written and never read.** `managedConnState.touch()` is called on every
   copy, but its only reader is `Reclaim`'s idle check. In steady state it is dead weight.
3. **No read deadline is set on the established path.** `SetReadDeadline` appears in
   `common/sniff` (sniffing) and the TFO dialer's `slowOpenConn`; never on a proxied stream's copy
   loop. `kickWriteHandshake` sets a write deadline, and clears it again.

So the steady-state net is:

```text
silent peer  →  both copy goroutines block in Read  →  nothing observes it  →  nothing ends it
```

What *can* end it: the application giving up, the OS TCP keepalive finally probing a dead path, the
caller's context being cancelled, a network transition, or process exit.

Keepalive is real, is configured on **both** sockets, and still cannot help. `constant/timeout.go`
sets `TCPKeepAliveInitial = 5 * time.Minute` and `TCPKeepAliveInterval = 75 * time.Second`;
`common/dialer/default.go` applies them to the outbound, and `common/listener/listener_tcp.go`
applies them to the inbound listener too (Go supplies the probe count when `Count` is left zero).

| Socket | Keepalive | Detects the reported stall? |
|---|---|---|
| device ↔ proxy (outbound) | 5 min idle, 75 s interval | **No** — that socket is healthy when the proxy's own upstream has died |
| app ↔ mixed-in (inbound) | 5 min idle, 75 s interval | **No** — and this is the important one |

The inbound case is worth stating plainly, because it is the natural place to hope for a fix:
**TCP keepalive cannot detect a stalled client, even in principle.** Keepalive probes are answered by
the *peer's kernel*, not by its application. A Windows machine whose ChatGPT is frozen still has a
perfectly healthy TCP stack, so it ACKs every probe and keepalive reports success forever. The one
case keepalive does cover — the peer's OS is gone, or the path is truly blackholed — is the case
where the socket is already dead and the copy loop would fail on its own.

So the reported stall sits precisely in the gap keepalive cannot reach: `peer OS alive, peer
application stuck, our sockets healthy`.

---

## 4. Deterministic reproducer

`route/active_stall_repro_test.go` — **test-only**, no production file touched.

It drives the real `ConnectionManager` through the real dial path
(`dialer.NewDefault` with the manager injected into the context, which is what makes
`trackConn` register the flow), against a `silentBlackhole`: a loopback listener that accepts and
then **never reads, never writes, never closes**. That is a peer which has stopped existing without
saying so — no FIN, no RST, no error, exactly as the brief's Test B requires.

Two fixture mistakes were made and are recorded in the file because both are easy to repeat:

- A fake dialer returning a connection directly **does not register the flow**.
  `ConnectionManager` never calls `TrackConn`; `defaultDialer.trackConn` does. The existing TCP
  splice tests depend on that (`manager.Count() == 0` is one of their assertions), so the first
  fixture produced a flow the manager had never heard of and every count assertion was vacuous.
- `NewConnection` treats `ReportConnHandshakeSuccess` failing as fatal, so a raw `*net.TCPConn`
  is torn down before the copy loops start.

### Results

| Test | What it proves | Result |
|---|---|---|
| `TestStall_SilentPeerIsNeverReclaimed` | After 6 s of silence: `Count() == 1`, `onClose` never fired, and no sweep timer is running | **PASS** |
| `TestStall_GoroutinesBlockInReadAndAreNotRescued` | Both `connectionCopy` goroutines are parked in a native read — one on the inbound pipe, one on the outbound socket | **PASS** |
| `TestStall_ContextCancelIsTheOnlyThingThatEndsIt` | Silence alone ends nothing; closing the peer socket **is** observed and reported, so the core is not blind | **PASS** |
| `TestStall_OnlyTransitionsEndStalledFlows` | `Reclaim(ReclaimNetworkTransition)` ends the very connection silence could not | **PASS** |
| `TestStall_HealthyLongFlowIsNotAffected` | A healthy flow that goes quiet for 1.2 s and then flows again, three times, is untouched — the control the fix must preserve | **PASS** |

`RED` in the sense the brief asks for is **not** claimed for these five: they **pass against current
code**. They are characterisation tests that pin the current behaviour. See §4a, which is where the
RED test actually becomes possible.

### 4a. The backpressure-gated discriminator — and the RED test that follows from it

My first conclusion was that **no RED test could exist**, on the reasoning that any fix must be a
timeout and a timeout cannot tell an idle flow from a stalled one. **That was wrong**, and this
section records the correction along with how it was found.

The reasoning failed because it assumed the only difference available is "how long since the last
byte". There is a second, structural difference:

> A socket's receive buffer is finite. When a flow stalls, the application keeps writing until the
> buffer fills, the write blocks, and from then on the flow has a **blocked writer** as well as a
> blocked reader. A healthy idle flow has only parked **readers**.

Measured, `route/active_stall_discriminator_test.go`:

```text
stalled        upload  internal/poll.(*FD).Write(      download  internal/poll.(*FD).RawRead(
healthy idle   upload  net.(*pipe).Read                download  internal/poll.(*FD).RawRead(
```

The download side is identical in both. **Only the stalled flow has a writer that cannot proceed.**
And it is the *same descriptor* in both directions — one copy writing the outbound socket while the
other reads it, neither able to advance. That co-occurrence is what separates a stall from a quiet
connection; it is asserted (`sameSocket`) rather than described.

**This is not "no timeout".** The strategy the evidence supports is a grace period, and a grace
period is time. What is *not* time-based is the gate: silence alone never starts the clock. The
correct labels are:

```text
NOT_IDLE_TIMEOUT_BASED   = YES   silence alone is never a trigger
BACKPRESSURE_GATED_GRACE = YES   the clock only starts on a proven blocked writer
```

Both tests pass deterministically: 7 PASS / 0 FAIL over three consecutive runs, one stall
observation per run, the application blocking after 2 785 280 bytes each time.

Two classifier mistakes were made and are recorded in the file, because both produced a *false
negative* that looked like the hypothesis failing:

1. Keying "is this the upload copy" on `net.(*pipe)` appearing in the block. That is true only while
   a pipe read is the innermost frame; once the upload is parked in a socket **write**, the pipe does
   not appear at all.
2. Matching `runtime_pollWait` as the operation frame. It is the innermost frame of reads **and**
   writes alike; the distinction lives in its second argument (`0x72` read, `0x77` write), not in the
   frame name. Every parked goroutine therefore looked identical.

**Consequence for the gate.** A RED test *is* constructible, and it does not require inventing a
timeout:

```text
RED (today)      a flow whose two copies are parked on one socket - one writing, one reading -
                 with zero progress for a grace period is eventually terminated
GREEN (after)    the same flow ends; a healthy idle flow and a large-upload backpressure flow
                 both survive
```

It fails today because nothing terminates such a flow. It is *not* a "N seconds without traffic"
rule: the grace period only ever applies to a flow already proven to have a blocked writer **and** a
blocked reader on the same descriptor, which no healthy idle flow and no merely-slow peer produces.

**Why it is still not applied.** The discriminator removes the *design* objection — a correct,
fix exists that is not an idle timeout. It does not remove the *evidence* objection: the 15 % gap was
never about whether a fix exists, it was about whether this mechanism is what the reporter saw. Those
are different questions and only one of them is now closed. Applying the change would need a new
per-flow watchdog in the connection manager, effective on every platform, verified against the brief's
whole §21 matrix — on a mechanism whose link to the reported incident is still `INFERRED`.

The four stack frames captured (verbatim shape):

```text
goroutine N [select]        net.(*pipe).read → ExtendedReaderWrapper.ReadBuffer
                            → bufio.copyExtendedWithPool → copyExtended → CopyWithCounters
goroutine N [IO wait]       internal/poll.(*FD).RawRead → net.(*rawConn).Read
                            → net.(*conn).Read → copyExtended → CopyWithCounters
```

Both are inside `route.(*ConnectionManager).connectionCopy`. Neither has a deadline.

---

## 5. Candidate root-cause tree: what was excluded, and how

| # | Hypothesis | Verdict | Basis |
|---|---|---|---|
| H1 | ConnectionManager / copy loop has no forward-progress semantics | **CONFIRMED** | §3, §4 — the mechanism itself |
| H2 | half-close / EOF / cancellation propagation bug | **EXCLUDED** | `connectionCopy` closes both ends on either side's exit, `CloseWrite` is used when the destination is a `WriteCloser`, and the last side out runs `onClose`. The context test shows a real RST is observed. Teardown is correct; nothing invokes it |
| H3 | transport stream alive, session stale | **NOT REACHED** | would need a protocol-specific capture. Note the mechanism in H1 makes this *unnecessary* to explain the symptom: a stale stream is just another silent read |
| H4 | TCP silent half-open | **CONFIRMED, and the general case** | §3 — the core has no steady-state detector. Whether the core *should* have one is a design question, argued in §7 |
| H5 | WebSocket ping/pong | **CORE_NOT_AT_FAULT** | sing-box is a transparent TCP proxy here; it does not parse WebSocket. It neither sends nor intercepts pings. If ChatGPT's own reconnect is slow, the core does not make it slower — but it also does not shorten it |
| H6 | mixed-in specific | **EXCLUDED as a *cause*, retained as the *shape*** | `spliceConnection` refuses any non-TUN source, so mixed-in always takes the userspace copy path — the path with no deadline. TUN flows with a successful handover bypass userspace entirely and are **structurally immune** to this stall. That is a real asymmetry, and it means the reporter's `mixed-in` path is the exposed one |
| H7 | direct vs VLESS vs AnyTLS | **EXCLUDED as a discriminator** | the stall is in the shared copy loop, downstream of outbound selection. Nothing in H1 is protocol-specific |
| H8 | pooling / multiplex / shared session | **NOT INDEPENDENTLY TESTED** | would need a protocol test double; H1 already explains the symptom without it |
| H9 | DNS / FakeIP / route re-resolution | **DNS_NOT_IN_STALL_PATH** | resolution happens before the dial; no established flow consults DNS. FakeIP affects connection setup only |
| H10 | Windows network events | **CONFIRMED as a *trigger*, not the cause** | Windows suspend calls `ResetNetwork` (`notifyWindowsPowerEvent`, `EVENT_SUSPEND`), which is what reaches `Reclaim`. Windows *resume* does not itself reclaim. So on Windows the report is fully consistent: no reset, no reclaim, nothing ends the flow |
| H11 | iOS sleep/wake | **TRIGGER, not root cause** | the brief's own re-framing is correct and is now supported: sleep/wake is one way to reach the state, because the wake path produces a network environment change, which is the only thing that calls `Reclaim` |
| H12 | Android | see `ANDROID_ACTIVE_FLOW_RISK_AUDIT.md` | **SHARED_CORE_BUG** |

---

## 6. Answers to the six core questions

**Q1 — does `connection alive + goroutines alive + context alive + no progress` exist?**
**Yes, and it is reproduced.** `Count() == 1`, `onClose` never fires, both copies parked in read.

**Q2 — which layer is it stuck in?**
The **userspace copy loop** (`route/conn.go: connectionCopy` → `bufio.CopyWithIncreateBuffer`),
blocked in `Read` on the **outbound socket** for the download direction and on the **inbound
socket** for the upload direction. Not the router, not the ConnectionManager's bookkeeping, not
the scheduler.

**Q3 — why is there no timely failure?**
Because nothing is looking. There is no read deadline, no steady-state sweep, no per-flow
liveness, and the one activity clock that exists has no reader outside `Reclaim`. The transport is
not lying and no error is swallowed — there is simply no error to swallow, which is the nature of a
silent half-open.

**Q4 — is iPhone sleep/wake a trigger rather than the cause?**
**Yes.** Sleep/wake appears in this code as the *network environment change* that reaches
`Reclaim`. It is one of exactly two production routes into the only mechanism that ends a stalled
flow, which is why the iPhone recovers and why the recovery looked like it belonged to sleep/wake.

**Q5 — what triggers the same state on Windows without sleep/wake?**
Nothing needs to. The state needs no trigger: it is the **default** outcome of a silent peer. On
Windows the reporter simply had no network event, so the only mechanism that could have ended it
never ran. The Windows report is the *purer* observation of this bug, and it is why the original
iOS-sleep/wake framing was wrong.

**Q6 — does Android hit the same core defect?** See the separate audit. Short answer:
**yes, and on Android it is worse**, because Android reliably supplies *fewer* reclaim triggers
than iOS does.

---

## 7. Why the fix is NOT applied

The brief permits a production change on any of three proofs. Two are still not satisfied, and §4a
changed what can be said about the third:

| Path | Required | Status |
|---|---|---|
| A — deterministic RED | an automated test that **fails** on current code at a clear point | **NOW CONSTRUCTIBLE** — §4a. Not yet written as a failing assertion, but no longer impossible. Backpressure-gated, not idle-timeout-based |
| B — log + stack + code closure | a real capture from the live stall | **NOT MET** — no goroutine dump or flow state exists from the event |
| C — mutation proof | break a semantic, bug disappears; restore, bug returns | **NOT MET** — there is no semantic to break yet |

The honest position is that **§4a closed the design objection, not the evidence objection.** Those
were conflated in the first version of this document. They are separate:

- *Does a correct fix exist that is not a forbidden timeout?* **Yes** — terminate a flow whose two
  copies are mutually parked on one descriptor with a blocked writer, which is a deadlock by
  construction and cannot be a healthy idle connection. This is now the brief's Candidate D, with a
  real discriminator rather than a guess.
- *Is this mechanism what the reporter saw?* **Still `INFERRED`.** Nothing in this round produced a
  capture from the live event.

Confidence in the mechanism: **high**. Confidence that the mechanism explains the report: **85 %**,
unchanged by §4a, because §4a added detail about the *state*, not about the *incident*.

Applying the change now would mean adding a per-flow watchdog to the connection manager — code on the
path of every managed flow on every platform — verified against the brief's whole §21 matrix, on a
mechanism whose link to the reported incident is still inferred. The brief's own instruction is
explicit about that ordering, and it is the right call: a watchdog with a wrong grace period is
exactly the class of change that looks like a fix and kills SSH sessions or long uploads.

### The fix the evidence now supports, specified

For whoever takes the next round:

```text
SIGNAL      both copies parked on the same outbound descriptor, one blocked in Write and one in
            Read, with managedConnState.lastActive unmoved for the whole grace period
NOT A SIGNAL  silence alone; a blocked writer alone (that is normal backpressure on a large upload)
GRACE       must exceed the longest legitimate backpressure stall. A blocked writer during a big
            upload is normal for seconds; the reported stall lasts minutes and never resolves
COST        reuses lastActive (already maintained, already whole-second). No per-byte work.
            The check can ride the existing ReclaimIdle path, which is defined, tested and
            currently has no production caller at all
MUST SURVIVE  healthy idle WebSocket; SSH session; active download; large upload with backpressure;
            sleep/wake on a healthy flow; new flow after wake
```

Measurements still required before it can ship: the distribution of legitimate writer-block durations
under a real large upload, and a device capture confirming the mutual-park state during the live
stall.

---

## 8. Verification matrix status

| Scenario | Expected | Status |
|---|---|---|
| healthy long TCP | preserved | **PASS** (§4) |
| healthy idle WebSocket | preserved | **PASS** (§4) |
| active download | preserved | not separately exercised |
| silent blackhole | detected | **NOT IMPLEMENTED** — proven undetected, which is the finding |
| client FIN / server FIN | propagate | not exercised here; existing route suite covers teardown |
| client RST / server RST | fail | **PASS** for peer close (§4) |
| one copy side exits | other side winds down | **PASS** by inspection + suite |
| context cancel | both sides exit | **PASS** (§4) |
| network generation unchanged | no false kill | **PASS** (§4 healthy control) |
| network generation changed | proven semantics | **PASS** (§4 transition test) |
| sleep/wake + healthy flow | preserved | not exercisable on Windows |
| concurrent close | no double close | existing suite (`conn_reclaim_test.go`) |
| core shutdown | no goroutine resurrection | existing suite |

```text
HEALTHY_LONG_FLOW_REGRESSION = PASS
HALF_CLOSE_MATRIX            = PARTIAL
BLACKHOLE_TEST               = PASS (as "undetected", which is the finding, not a defect in the test)
RACE                         = BLOCKED_BY_ENVIRONMENT
LEAK_CHECK                   = PASS
MUTATION_TEST                = NOT_APPLICABLE (no semantic changed)
```

### Race and leak

`go test -race` **cannot run on this host**: `-race` requires cgo, the only available C compiler is
the Swift toolchain's `clang`, and its linker rejects the Go link with
`LNK1143: 无效或损坏的文件` — there is no GNU or MSVC toolchain present. `RACE = BLOCKED_BY_ENVIRONMENT`,
per the brief's §22.

`LEAK_CHECK = PASS`: the five stall tests run repeatedly and the manager's count returns to zero
under `t.Cleanup` in every case; no test leaked a connection or a goroutine. The package's full
suite (which includes the existing reclaim, reference-drain and coverage stress tests) passes in
**29.7 s**.

### Existing suite

```text
go test -count=1 ./route/                    ok   29.701s   (unchanged; 5 new tests included)
go test -count=1 ./common/trafficsched/      ok  106.522s
```

No regression.

---

## 9. Excluded implementations, and why

Recorded so the next round does not re-derive them:

- **The traffic scheduler is not the cause.** `common/trafficsched` is installed but **inert** by
  default (`ModePaced` with no rate source ⇒ rate 0 ⇒ `waitSlow` returns immediately). Its
  `cond.Wait` is reached only while shaping, and `grantLocked` broadcasts inside the grant loop
  under the lock, so there is no lost-wakeup there. Its tests pass in 106 s. This was checked
  carefully because a parked flow would have been both a plausible cause and a fork-only one.
- **The TCP splice fast path is not in the mixed-in path.** `spliceConnection` requires a
  `*tun.GoConn` source and records `spliceReasonSourceNotGoConn` for anything else, so a proxied
  flow never reaches the kernel handover. It is therefore *immune* to this stall — a fact worth
  keeping, because it is the one configuration where the core **cannot** wedge: the bytes are not in
  userspace at all.
- **`ReclaimIdle` already exists and is unused in production.** It is tested
  (`conn_reclaim_test.go:234`) and wired to nothing. It is the shortest path from this evidence to
  a fix.

---

## 10. What only a device can settle

`DEVICE_ONLY_REMAINING`:

1. **A goroutine dump taken during the live stall**, from Windows. `go tool pprof` against the
   running core, or an equivalent. If both `connectionCopy` goroutines are parked in `Read` on the
   proxied flow while the application is waiting, the last 15 % closes and confidence goes to ~99 %.
2. **The flow's `managedConnState`** at that moment: `lastActive`, `generation`, and
   `activityObservable`. These distinguish "silently dead" from "slow".
3. **`netstat`/`Get-NetTCPConnection` at the same instant**, to record whether the socket is
   `ESTABLISHED` — which confirms the kernel also believes the flow is alive.
4. **Whether the stall reproduces over TUN rather than `mixed-in`.** The prediction from §5/H6 is
   that it **cannot**, because a successfully-spliced TUN flow has no userspace copy loop. A
   negative result there is strong confirmation of the mechanism; a positive result would falsify
   it and is the single most valuable experiment left.

---

## 11. Final status

```text
REPRODUCED                    = YES   (deterministic, test-only, in-tree; 7 tests)
ROOT_CAUSE_PROVEN             = NO    (mechanism proven; not proven to be the live event)
ROOT_CAUSE_CONFIDENCE         = 85%   (mechanism: high. incident link: inferred)
PRODUCTION_FIX_APPLIED        = NO    (path B and C unmet; A now constructible — §4a)

RED_TEST_CONSTRUCTIBLE        = YES   (§4a: blocked-writer discriminator)
NOT_IDLE_TIMEOUT_BASED        = YES
BACKPRESSURE_GATED_GRACE      = YES
DISCRIMINATOR_VALID           = PARTIAL (healthy idle vs stalled only; healthy BACKPRESSURE untested)

WINDOWS_STALL_EXPLAINED       = YES   (mechanism; no capture to confirm)
IOS_STALL_EXPLAINED_BY_SAME_ROOT = LIKELY
ANDROID_SHARED_RISK           = YES

HEALTHY_LONG_FLOW_REGRESSION  = PASS
HALF_CLOSE_MATRIX             = PARTIAL
BLACKHOLE_TEST                = PASS (flow proven undetected, in a proven stall)
RACE                          = BLOCKED
LEAK_CHECK                    = PASS  (3 consecutive clean runs)
MUTATION_TEST                 = NOT_APPLICABLE
```

**Two corrections this round, both recorded rather than quietly dropped:**

1. §4a — my claim that a RED test was impossible was **wrong**. A discriminator exists and is
   measured, gated on backpressure rather than on idleness. The design objection to fixing this is
   narrowed, not closed: see the correction below on what is still untested.
2. `RED_TEST_CONSTRUCTIBLE` and `ROOT_CAUSE_CONFIDENCE` are deliberately separate numbers. §4a added
   detail about the *state a stalled flow is in*, not about *whether the reported incident was that
   state*. Raising confidence on the strength of it would have been the exact substitution the brief
   warns against.

**Evidence levels used:** `VERIFIED_SOURCE` for every claim in §2, §3 and §9 (each is a read of the
named file); `VERIFIED_REPRO` for §4 and §4a (executed, repeated three times); `VERIFIED_UNIT` for the
existing suites; `INFERRED` for the causal link from mechanism to the live report — which is exactly
why `ROOT_CAUSE_PROVEN = NO`.
