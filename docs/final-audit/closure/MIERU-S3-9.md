# Mieru S3-9: stale-underlay reuse after idle close

## Reproduction (current pin, Mieru SDK v3.39.0)

```
go test -race -count=20 -run TestMieruClientObservesServerClosingAnIdleUnderlay ./protocol/mieru
# 19 pass / 1 fail in the first sample; 15 pass / 5 fail in the final gate sample
# (failure rate 5-25 % depending on host load; aggregate 34/40 pass = 6 failures)
go test -count=20 -run TestMieruClientObservesServerClosingAnIdleUnderlay ./protocol/mieru
# 20/20 pass
```

The final gate log is `evidence/final/mieru-race.log` (15 PASS, 5 FAIL, all the
same `socks5 connection response ... EOF` at `idle_underlay_test.go:140`). The
wider earlier sample was 2 failures in 48 executions. The rate is load-sensitive,
which is consistent with a timing window rather than a deterministic bug.

Failure is the new dial after the idle close (`idle_underlay_test.go:140`), not
the blocked read the test was written for:

```
failed to read socks5 connection response from the server: EOF
```

## Root cause: SDK TOCTOU, proven in the pinned source

`Mux.DialContext` (`pkg/protocol/mux.go:411`) picks an underlay under the mux
lock (`mux.go:428-432`), then calls `AddSession` (`mux.go:455-462`) with **no
liveness check in between**. `baseUnderlay.AddSession`
(`underlay_base.go:131-164`) validates nil/session ID/state/role/duplicate but
not "is this underlay closing". The pick filter only checks `<-Done()`
(`mux.go:770-780`), and `done` is closed **after** `Close` has closed and awaited
the sessions (`underlay_base.go:99-105`); that await is exactly what unblocks the
application read with `io.EOF`. A dial in that gap attaches a session to a dying
underlay, so the next SOCKS5 response read sees EOF.

Deterministic probe on unmodified v3.39.0: `AddSession(s1)` → `Close()` →
`<-Done()` → `AddSession(s2)` returns **nil** (attached to a closed underlay).

## Upstream status (checked 2026-11-11)

- v3.39.0 (2026-10-23) is the newest release; it introduced server-side idle TCP
  close (issue #317).
- `origin/main` 1367cf2 (2026-11-05) has six commits after the tag, none touching
  `mux.go`, `scheduler.go`, close ordering or reuse; no v3.40 tag.
- No upstream issue reports this client-side symptom; no tests cover
  `maybePickExistingUnderlay`/`cleanUnderlay`.
- A throwaway P1+P2 patch (disable scheduler before teardown; reject `AddSession`
  when done/disabled) gave 42/42 clean `-race` runs; the mechanism argument is
  stronger than the small sample: the app-visible EOF can only be produced inside
  `Close`, after P1 sets the closing state.

## Route decision

| route | semantics | risk | verdict |
|---|---|---|---|
| wait for upstream | unchanged | no ETA; feature is new | insufficient alone |
| upstream issue + P1/P2 patch | unchanged for callers; dead underlay never attached | low; one sentinel error path | recommended |
| adapter-level bounded retry | extra attempt may re-pick the same underlay; request already written before EOF | duplicate target connection, masks real failure, +1 RTT / 10 s tail | rejected without product decision |

The adapter cannot force a fresh underlay (`maybePickExistingUnderlay` may pick
the same one; N/(N+1) reuse), and the fork's existing policy deliberately treats
an ambiguous EOF as "do not switch" (`fallback.go:68-73`, `outbound.go:551-558`).
Interim lever if the flake must be suppressed: `multiplexing: off` per outbound
(one TCP underlay per dial, no correctness risk, product-visible cost).

## Status and next action

`UNRESOLVED` — the test assertion is correct and was **not** weakened. Next:
open the upstream issue with the deterministic probe and the fork test, offer
P1+P2 as a PR, and keep a `-race` quarantine run to catch changes. Acceptance for
the fix: 50× `-race` green here plus a new SDK unit test.

The resource observation (S3-2) is unchanged and remains `DEFERRED`: in-process
FD counts include the test inbound, so they cannot prove a client leak.
