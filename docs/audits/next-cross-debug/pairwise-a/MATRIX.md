# Window A — pair matrix (second round)

Routing table for the rows this window attacked. `REPORT.md` has the narrative and the six-stage
evidence; `REPRODUCE.md` has the commands. Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` /
`NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`.

- Repository: `Piggy-Cat-bit-shadow/JiejieBox` `testing`, start `17b6fb2e0`; code commits `f11def4b1`, `c867befd0`, `8426642c9`, `9e00fb390`, `418044508` (the three files in this directory are documentation commits)
- Fork: `Piggy-Cat-bit-shadow/sing-anytls` `main` at `da880d8`
- Platform: Windows amd64, Go 1.27.4; `-race` with mingw64 gcc; tags `with_trusttunnel,with_quic`

## Summary

| Row | Systems (repo) | Shared object | Conclusion | Commit |
| --- | --- | --- | --- | --- |
| A-P03 | `transport/trusttunnel.Client` (JiejieBox) × reference-manager trim / `Guard.runShed` / idle watcher (JiejieBox) | `clientSession.streams`, the only "is this carrier in use" evidence | **FIXED** | `f11def4b1` |
| A-P04 | `protocol/anytls.Outbound` (JiejieBox) × `sagernet/sing-anytls` session pool (fork) × `route/reference.go` boundary walk (JiejieBox) | the pool's idle-session list and live-stream set; the boundary's verdict | **FIXED** | `c867befd0`, `8426642c9` (fork `693a1fe`, `da880d8`) |
| A-P09 | `transport/trusttunnel.Client` offer lifecycle × `Close`/`Reset`/network generation | pending offer, `c.session`, generation stamp | **NO_DEFECT** (regression re-run) | — |
| A-P12 | `common/resourceguard.Guard` × OOM report / metadata diagnostics (JiejieBox) | the level→plan publication and the shed hooks | **NO_DEFECT** (executed) | `9e00fb390` |
| A-P05 | `protocol/naive` H3 probe budget × caller deadline (JiejieBox) | the caller's remaining budget and the H3 fallback window | **POLICY_DECISION** | — |
| A-P11 | H2/H3 response body × pool recycle (JiejieBox) | the stream's lifetime as the pool's Active/Idle evidence | **NO_DEFECT** (3 shapes executed, 2 mutation controls) | `418044508` |
| A-P02 | QUIC PMTU probe × path migration (JiejieBox + `quic-go` fork) | the path's MTU/send-queue state | **NOT_COVERED** | — |

## A-P03 — TrustTunnel: the session a caller is handed is not reserved (FIXED)

| Column | Value |
| --- | --- |
| Systems and repos | ① `transport/trusttunnel` (JiejieBox) ② the reclaim side: `route/reference.go` `retireIdleResources` / `retireSuspectTarget`, `common/resourceguard.Guard.runShed`, and the client's own `idleWatcher` (JiejieBox) |
| Owner (authority) | ① the client owns the session and its stream count; ② the walk owns *when* to trim/retire — it does not own the object |
| Shared boundary | `clientSession.streams`: the counter that means "a stream is using this carrier" |
| Failure timeline | T1 `getSession` returns the session with `streams==0` → T2 `CloseIdleConnections` reads 0, detaches, closes → T3 `roundTrip` increments and sends CONNECT on a closed carrier → T4 fresh: demand fails; reused: a second session is created; probe: `_check` fails and retires a healthy carrier |
| Why exactly 2 systems | the borrower is the protocol client; the closer is a different subsystem's walk (and a second observer, the idle watcher, decides from the same reading). Neither system is wrong alone — the pair is |
| Product path executed | `DialContext` → `openStream` → `getSession` (real entry), with `CloseIdleConnections` called from inside the hand-off window; `Check()` → `getSession` for the probe entry |
| Evidence before | `a trim closed the carrier the demand had just been handed`; `trusttunnel: CONNECT hold.test:1: use of closed network connection`; reused: `expected 1, actual 2` sessions; probe: `a trim closed the carrier under the health probe` |
| Evidence after | three attacks green; full package green (52.976 s), `-race` green (55.689 s); leak control green; mutation control = the parent commit (`17b6fb2e0`) |
| Uncovered boundary | an in-flight CONNECT that is promised but not yet written can still be cut by `Reset`/`Close` (those verbs are documented to cut in-flight work); not attacked with a real H3 carrier beyond the shared fake |
| Commit | `f11def4b1` |

## A-P04 — AnyTLS: the pool cannot express the reuse boundary (FIXED)

| Column | Value |
| --- | --- |
| Systems and repos | ① `protocol/anytls.Outbound` (JiejieBox) ② `sing-anytls.Client` session pool (fork) ③ `route/reference.go`'s boundary walk (JiejieBox) |
| Owner (authority) | ② the pool owns the idle list and the stream set; ③ owns the decision "this resource predates the boundary"; ① is the adapter the walk can see |
| Shared boundary | the pool's idle-session list plus the session's live-stream set, and the capability the walk is allowed to ask for |
| Failure timeline | T1 a burst coalesces N flows onto one session → T2 one stream finishes; `releaseSession` lists the session for reuse although another stream is live → T3 sleep/wake boundary; the walk calls the trim, which skips it (`hasStreams`) → T4 the next flow takes it and writes `cmdSYN` into the dead path → T5 ~3 s later `streamOpenTimeout` closes the session and the flow fails |
| Why exactly 2 systems | the boundary is the router's decision, the state that makes the decision safe is the pool's. Fixing it on either side alone is wrong: the outbound cannot see the stream set, and the pool never learns that a boundary happened |
| Product path executed | real outbound + real pinned `sing-anytls` service on loopback TLS; two coalesced flows on one session; one finishes; `Outbound.RetireSuspect()`; next flow observed at the server by client source port |
| Evidence before | with the outbound delegating to `CloseIdleConnections`: `the flow after the reuse boundary was multiplexed onto the session that predates it` (server sees one source port) |
| Evidence after | the post-boundary flow opens a **second** session (two source ports) and the flow already running still echoes; fork: drain-with-a-live-stream red with the `RetireSuspect` body removed, green with it; full fork suite `-race -count=3` green; fork `test` CI run `44673034831` success |
| Uncovered boundary | a creation already in flight at the boundary is not retracted (documented in `REPORT.md` §6); `hysteria2`/`tuic`/`mieru` need the same capability at their own pools and are not covered here |
| Commits | JiejieBox `c867befd0` (pin) + `8426642c9` (consumer); fork `693a1fe` + `da880d8` |

## A-P09 — late creation publishing into a closed pool (NO_DEFECT)

| Column | Value |
| --- | --- |
| Systems and repos | `transport/trusttunnel.Client` shared offer × `Close`/`Reset`/network generation (JiejieBox) |
| Shared object | the pending offer's `discarded` flag, `c.session`, `session.generation` |
| Failure timeline that was looked for | dial starts → `Close`/`Reset` → dial returns success → a session is installed/published anyway |
| Why 2 systems | the dial is the resource producer; the lifecycle verb is the authority that says the owner is gone |
| Attack executed | re-ran the seven late-publish/generation pins (0.316 s, all pass) and read the lock discipline: `discardPendingLocked` and `publishOffer` both hold `c.access`; a discarded offer closes its late success exactly once |
| Evidence | all seven tests pass at HEAD; no interleaving found that installs into a detached owner |
| Uncovered boundary | a real QUIC session whose `alive()` lies across a sleep (that is `P04`'s shape, not this one) |
| Commit | — |

## A-P12 — `Guard.Stats()` is a reclaimer (NO_DEFECT, executed)

| Column | Value |
| --- | --- |
| Systems and repos | `common/resourceguard.Guard` × diagnostics (OOM report, guard metadata) (JiejieBox) |
| Shared object | the level→plan publication (`appliedVersion`, `gate.setPlan`) and the shed hook list |
| Failure timeline that was looked for | a diagnostic reader inside a shed hook re-enters `Stats` → deadlock on `access`/`shedAccess`, or re-runs the transition's hooks on every read |
| Attack executed | a shed hook calls `Stats()` and `Level()` from inside the reclaim; eight further reads at the same level; a real new transition; recovery driven by `Stats()` alone |
| Evidence | no deadlock (5 s bound), one reclaim per transition, recovery edge completes through report reads only; `-race -count=2` green (3.192 s) |
| Decision | the read-only change is **rejected**: a snapshot must describe one transition, which is why `Stats` observes and applies |
| Commit | `9e00fb390` (test only) |

## A-P05 — Naive H3 probe budget × caller deadline (POLICY_DECISION)

| Column | Value |
| --- | --- |
| Systems and repos | `protocol/naive` H3→H2 fallback (JiejieBox) × caller deadline; native Cronet engine (external) |
| Shared object | the caller's remaining budget, split between the H3 probe and the H2 retry; the 30 s fallback window |
| Failure timeline read | a 200 ms deadline gives H3 ~100 ms; the expiry is *ambiguous*, so the flow retries over H2 and the window is **not** armed — the next CONNECT probes H3 again |
| Why 2 systems | the caller's time budget and the transport's availability verdict are different clocks; the fallback window is a third one |
| Evidence | source + existing matrix: `TestH3FallbackProbeExpiryIsAmbiguous`, `TestH3FallbackProxyAnswerOutranksProbeExpiry`, `TestH3FallbackCallerCancelDuringProbeDoesNotRetry` |
| Decision | arming the window on an ambiguous expiry would be a **policy change** with a cost on H3-only deployments; not changed |
| Not observable | native Cronet (`DialEarly`, `DialEarlyWithQUIC`) on this host |

## A-P11 — a stream's end must reach the pool's books (NO_DEFECT)

| Column | Value |
| --- | --- |
| Systems and repos | `transport/trusttunnel` stream/body lifetime × the session pool and its trim (JiejieBox) |
| Shared object | `session.streams`: the stream is the pool's Active/Idle evidence, and the response body is the stream's lifetime |
| Failure timeline that was looked for | a stream ends in a way the caller does not control (server EOF/half-close, caller closes a stalled tunnel, trim arrives mid-flight) and the slot, the carrier or the sibling flow is lost with it |
| Why exactly 2 systems | the body's end belongs to the transport; the slot belongs to the pool. A body that ends without the pool noticing makes the session unkillable; a pool that closes on a liveness guess cuts the body |
| Product path executed | real service + real client on loopback TLS: `DialContext` to a silent destination (`hold.test`), to a target that answers and closes, and a live flow across `CloseIdleConnections` |
| Evidence | 3 tests green (0.11 s): Close of a stalled stream returns and the server releases it; the body reaches EOF and a write after it resolves; the trim leaves a live session untouched and can close it after the last stream. Mutation controls red: dropping the slot return in `streamConn.Close` → `a closed stream left its slot reserved`; making the trim ignore the stream count → `the trim closed a session that was carrying a stream` |
| Uncovered boundary | a raw `http2.Server` RST_STREAM injection was not built (the product server ends a stream by closing it); the 1000-stream limit case is the pre-existing `testStreamLimit` |
| Commit | `418044508` |

## A-P02 — QUIC PMTU probe × path migration (NOT_COVERED)

| Column | Value |
| --- | --- |
| Systems and repos | `transport/trusttunnel` (H3) / `protocol/hysteria2` / `protocol/tuic` × `quic-go` fork × network path |
| Shared object | the path's MTU, congestion window and send queue, inherited across a migration |
| Why it is not covered | needs a PMTU blackhole **and** a NAT rebinding injected into the same flow; the two exist separately (`q35_udp_blackhole_*`, `congestion_test.go`) but the combined attack was not built |
| What would settle it | blackhole a size band, migrate the path (rebinding), and require: the flow completes, unrelated streams are unaffected, the send queue does not grow unboundedly |
