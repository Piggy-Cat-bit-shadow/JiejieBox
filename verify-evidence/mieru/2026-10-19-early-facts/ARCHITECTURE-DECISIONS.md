# Architecture decisions

## The problem statement, reduced to two sentences

Mieru already knew how to fall back and MASQUE already knew how to fall back a version; what both
lacked was **the earliest fact they could have acted on**. Mieru let the official SDK flatten a typed
socket error into text and then guessed from the text; MASQUE's CONNECT-IP branch had no bound of its
own on the HTTP/3 attempt, so the fallback it already implements was unreachable in the one failure it
exists for.

## Options considered

| Option | Shape | What it costs | Verdict |
|---|---|---|---|
| A. Pure wiring of existing interfaces | Only make the existing facts reach the existing consumers | Cheapest, but the typed socket error is destroyed inside the SDK before any existing interface can see it | Insufficient alone |
| B. Minimal producer-side patch | Smallest new code that keeps the proof the SDK discards; no new public interface | One context-scoped ledger in `protocol/mieru`, one window in `transport/http` | **Chosen** |
| C. New narrow capability/event interface | A public "underlay failure event" or `UDPHealth` capability other protocols could consume | New public surface, zero-value semantics, capability penetration tests, and a second place that can disagree with `FailureFacts` | Rejected: no consumer needs a *state*, only a per-attempt fact |

The decision follows the directive's rule: the common entry point is **already there**
(`adapter.FailureFacts`, the shared dialer, the epoch snapshots, `runtimecoord`); the missing piece was
that one producer could not see its own evidence. That is a producer bug, not an architecture gap.

## Why no new public interface

| Existing capability | Why it cannot carry this fact |
|---|---|
| `adapter.FailureFacts` | Can carry it — and does. The problem was upstream of it: no error chain survived to attach the fact to. |
| `NetworkFamilyCapabilitySnapshotter` | Produces a snapshot of the LOCAL network. The failure in question is a refusal/unreachable from a specific socket attempt; a snapshot cannot express it, and reading it again would already be stale. |
| Admission | Runs *before* the attempt and already refuses what it can prove. It cannot report an outcome. |
| `InterfaceUpdateListener` | A generation event, not a per-attempt result. |
| `runtimecoord` | Register/generation/cancel/rebind ownership; it does not choose transports and must not learn protocol errors. |
| A new "UDP health" capability | Would duplicate protocol policy (Mieru cooldown vs MASQUE backoff vs group penalty), which the directive explicitly forbids. |

Zero-value/degradation behaviour therefore stays trivial: no interface was added, so no existing
implementation can lose a capability, and `FailureFactsOf` still reports "no fact" for every producer
that does not attach one.

## Mieru: the per-dial observation ledger

**Old entry point was late/wrong:** `classifyDialError` received a string. The signature was
`classifyDialError(ctx, err, transport)` and the only typed evidence it could read was
`stderror.TypedError` or errors that the SDK happened to wrap with `%w`. Every underlay error is
wrapped with `%v` (`pkg/protocol/mux.go`), so a refusal, an unreachable network and a local interface
failure all arrived as prose. The SDK's own English prefix ("NewTCPUnderlay() failed") still identified
a transport failure, but the OS message inside it is formatted in the system language, so a refusal
could not be marked definite and its stage was lost. A local `EADDRNOTAVAIL` was indistinguishable from
a real path failure.

**New evidence:** `serverDialer.DialContext` / `ListenPacket` record the error (or the success) in a
`dialObservation` carried by the context of exactly one SDK dial. `transportRuntime.dial` reads it once
the SDK returned and passes it to the classifier. **Consumer:** the same classifier, so the selector
and the fact wrapper see a real value instead of a guess.

**Why other protocols are unaffected:** the ledger lives in `protocol/mieru`, is installed only around
`t.client.DialContext`, and is never read outside that call. It is not a health state: a later socket
success clears the recorded failure, and the object is unreachable after the dial returns.

### Classification table after this round

| Evidence (in order) | Class | Tries secondary? | Preferred result | `FailureFacts` |
|---|---|---|---|---|
| caller ctx done / SDK stopped / DNS / address / permission | caller | no | unchanged | Dial / Caller |
| typed refused, unreachable, or path-failure errno | **socket** | yes | secondary, cooldown | Dial / Path |
| typed `EADDRNOTAVAIL`/`ENETDOWN` (local interface) | caller | no | unchanged | Dial / Caller |
| typed kernel connect timeout / reset, or a media timeout at connect | **socket-timeout** | yes | secondary, cooldown | none (a stall is not proof) |
| SOCKS error reply | target | no | primary, cooldown cleared | Target / Target |
| handshake read timeout / reset (underlay was up) | transport | yes | secondary, cooldown | Handshake / Path |
| bare EOF, TCP primary | ambiguous | no | unchanged | none |
| bare EOF, UDP primary (Linux unreachable peer) | transport | yes | secondary, cooldown | Handshake / Path |

Two deliberate omissions:

- `socket-timeout` carries no fact. The SDK flattened the error, so the group's `isTimeoutFailure`
  can no longer see a `net.Error`; a Path fact at the dial stage would demote a member for a mere
  stall, which is the demotion the group's timeout rule exists to prevent.
- `ambiguous` carries no fact, unchanged: wrong credentials, a server close and an underlay FIN are
  indistinguishable at this API boundary.

### The both-failed rule

`reportBothFailed` previously reset the preference for every pair of transport-class failures. That
kept a wrong password from pinning the secondary — correct — but it also made a **proven** double
outage re-dial both endpoints on every request. Measured with two refused endpoints: one primary dial
and one secondary dial per request, forever, with no new information.

The rule now reads the *certainty*, not just the class:

| primary | secondary | result | why |
|---|---|---|---|
| definite socket | definite socket | keep secondary through its cooldown | both endpoints proved unusable; the cooldown already schedules exactly one probe |
| definite socket | anything else | reset to primary | the primary is cheap to re-test (a refusal is ~1 ms), which is how its recovery is noticed |
| transport/ambiguous | anything | reset to primary | the wrong-credential shape; do not strand an account on the backup |

This changes only one decision and keeps the fail-closed property the previous round established. Tests:
`TestDefiniteSocketFailureOnBothTransportsDoesNotDoubleDial`, `TestDefinitePrimaryAndSilentSecondaryResetsToPrimary`,
`TestTwoSilentHandshakesStillResetToPrimary`; the mutation that disables the first row makes the first
test fail with 5 primary dials instead of 1 (see the ledger).

## MASQUE: the tunnel window

**Old entry point was late/wrong:** `openTunnel` called `c.http3.OpenTunnel(ctx, ...)` with the
caller's context. The stream-dial path had `http3EstablishTimeout` since an earlier round, precisely
because an unbounded H3 attempt hands the caller a timeout and never reaches H2. The tunnel path never
got the same treatment, so a blackholed CONNECT-IP setup waited for `C.TCPTimeout` (15 s), returned the
caller's deadline (not `ErrHTTP3Unavailable`), and skipped the H2 branch. Worse, nothing was armed, so
the next reconnect paid it again.

**New evidence:** the attempt gets `http3TunnelEstablishTimeout` (3 s) of its own. On expiry, the
attempt is treated as "H3 did not come up": the H3-broken memory is armed and the H2/H1 branch runs
inside the caller's remaining budget. **Consumer:** the same `openTunnel` branch, then `TunnelTransport`
reporting and MASQUE's session, which continue unchanged.

Rules preserved exactly (same shape as `DialContext`):

- caller context done → return the caller's error, arm nothing, do not start an H2 tunnel;
- `disable_version_fallback` → no H2 attempt and no armed memory, for **every** H3 failure including
  the window expiry, and the error names the window;
- an error from a working H3 connection (`!errors.Is(err, ErrHTTP3Unavailable)`, e.g. a 4xx/403 status)
  → returned as-is, no version switch, nothing armed.

Why a separate tunable: the two lifecycles differ (a stream dial ends at the connection, a tunnel setup
ends at the 200 that begins a long-lived session). Reusing the constant would make a future change to
one silently move the other. The value is 3 s because the measurement supports it (four round trips at
~750 ms, and 12 s left of the 15 s budget), not because it was copied — the measured loopback setup is
3.4 ms.

## Same-generation ordering: investigated, deliberately unchanged

The directive asks whether several in-generation dial results arriving out of order can publish a
wrong preference, and to test it rather than assume. `TestSameGenerationOrderOfDialOutcomes` pins the
answer:

- Two attempts choose the primary. The faster one succeeds; the slower one then fails. The failure
  **does** flip the preference to the secondary for one cooldown.
- The next successful probe clears it, so the flip is a hint, not a verdict.

Why this is accepted rather than fixed with an attempt-order guard:

1. **"Newer" is not well defined for a long attempt.** A handshake that started first can observe a
   *later* world: the endpoint may have died while it waited. Ignoring its failure would keep new
   sessions on a primary that is dead now, which is worse than a bounded preference.
2. **The cost is bounded and self-healing.** One cooldown (30 s, at most 5 min), cleared by the next
   success, target reply or probe. MASQUE's `claimHTTP3Outcome` guard exists because a stale failure
   there *arms a broken memory that pins the version for the whole backoff and escalates it* — a much
   costlier error, and one where the successful attempt really does describe a newer moment.
3. Changing it would require threading an attempt token through `choose`/`report` (a public-in-package
   API used by every selector test) for no measured user-facing failure.

Revisit only if a real log shows a preference flip after a success on the same generation, or if the
Mieru cooldown ever grows to the point where a wrong hint costs minutes.

## Rejected alternatives (and why)

| Alternative | Why rejected |
|---|---|
| Vendor or fork the Mieru SDK to make the 10 s response read configurable/context-aware | Solves the floor, but costs a GPLv3 fork, an upgrade path and a divergence from upstream for a constant. The directive asks for the price to be stated, not paid by default. Recorded as the only real way to go below 10 s. |
| Race TCP and UDP in parallel on every dial | Doubles connection budget on the healthy path — exactly what the user said not to do. |
| A background health ticker / periodic probe | The SDK already starts a 5 s session-clean ticker per underlay; adding a probe would add wakeups, battery cost, and a "health verdict" the architecture forbids. |
| Lazy SDK client creation to save ~0.25 MiB | No measured user-facing problem, and the previous round already recorded the idle cost; lazy start would put a 500 ms-scale startup inside the first request. |
| Copy the 3 s window blindly onto CONNECT-IP and declare it correct | The window is justified by the quic-go handshake bound (10 s) and the caller budget (15 s), not by analogy; a separate tunable keeps them independent. |
| A per-dial goroutine so a cancelled caller returns immediately | Would return before connection ownership is settled: the SDK still creates a session afterwards, `Close` may stop the client under it, and in-flight accounting would have to move into the goroutine. The wait is bounded (10 s) and the classification is already correct; recorded as a residual instead of a risky refactor. |
| Add `FailureFacts` to TLS/QUIC/HTTP errors generally | Not proven at those boundaries; the directive forbids guessed facts. Only the Mieru socket stage has proof today. |
| Let the group treat a socket connect timeout as Path/Dial | It would demote a member for a stall (the flattened error is no longer a `net.Error`). Fact-less is the conservative, correct answer. |
