# Mieru upstream issue draft + patch outline (ready to file)

Status: **draft only** — not filed, because filing acts under the maintainer's
account and needs the owner's decision. The reproduction and analysis are complete
(`MIERU-S3-9.md`); this file is the text and the patch shape the next step needs.

## Issue body (suggested)

Title: `Client can attach a new session to an underlay already being closed (EOF on the following SOCKS5 handshake)`

Environment: mieru v3.39.0 (latest as of 2026-11-11), Go 1.25.5, Linux.
Trigger: a server with `tcp_idle_timeout` set (the feature from #317) closes an
idle TCP underlay while the client keeps it in the pool.

Symptom: after an idle underlay is closed, a new `DialContext` occasionally
returns a connection whose SOCKS5 response read fails with `EOF`. The server
never sees a request from that dial; the failing connection is not a fresh
underlay.

Root cause (pinned source):
1. `Mux.DialContext` (`pkg/protocol/mux.go:428-432`) picks an underlay under
   `m.mu`, releases the lock, then calls `underlay.AddSession` at
   `mux.go:455-462`. There is no liveness check between pick and attach, and the
   attach error is flattened with `%v` (`mux.go:461`).
2. `baseUnderlay.AddSession` (`underlay_base.go:131-164`) validates nil, session
   ID, state, role and duplicate — but not whether the underlay is closing.
3. `StreamUnderlay.Close` sets a deadline, calls `baseUnderlay.Close`
   (`underlay_base.go:99-104`) which closes and **waits for** its sessions (that
   wait is what unblocks the app's `Read` with `io.EOF`), and only afterwards
   executes `close(done)` (`underlay_base.go:105`). The pick filter only skips
   `<-Done()` (`mux.go:770-780`), so a dial in this gap attaches to a dying
   underlay.
4. Removal (`cleanUnderlay`, `mux.go:799-801`) runs on a 5 s ticker and at dial
   entry, always after the gap. No retry exists on the handshake path
   (`apis/internal/handshake.go:30-53`).

Deterministic probe (unmodified v3.39.0, in-package copy): `AddSession(s1)` OK →
`Close()` → `<-Done()` → `AddSession(s2)` returns **nil** (a session attached to a
closed underlay). This alone shows the missing guard.

Integration repro: a test proxy accepts a TCP connection, relays it, then closes
it after an idle interval; the client's next dial through the SOCKS5 inbound
fails with `failed to read socks5 connection response from the server: EOF`.
Observed 1/20 and 5/20 under `-race` (load-sensitive), 20/20 without race.

Patch outline (validated in a throwaway copy: 42/42 `-race` runs clean):
- **P1** at the top of `StreamUnderlay.Close` (and `closeIfIdle`), mark the
  underlay unusable before tearing down sessions:
  `Scheduler().SetRemainingTime(0)` (or an explicit `closing` flag) so
  `maybePickExistingUnderlay` skips it and `IncPending` returning false routes
  `DialContext` to `newUnderlay`.
- **P2** make `baseUnderlay.AddSession` reject when `done` is closed or the
  scheduler is disabled, returning a typed sentinel (do not string-match).
- **P3** optional: in `DialContext`, on that sentinel, retry attach once on a
  fresh underlay. P1 alone covers the observed failure by construction.
- Regression test: `AddSession` on a closing underlay must fail; `DialContext`
  must still succeed by creating a new one; run the idle-close integration test
  50× under `-race`.

Impact/notes: the adapter cannot work around this safely. `maybePickExistingUnderlay`
may re-pick the same underlay (reuse probability N/(N+1)), the request is already
written before EOF, and the fork's existing policy deliberately treats an
ambiguous EOF as "do not switch" (`fallback.go:68-73`, `outbound.go:551-558`).
An adapter retry risks a duplicate target connection and hides the real failure.
Interim lever: `multiplexing: off` per outbound (one TCP underlay per dial, no
correctness risk, higher connection count).

## What the next stage must do

1. File this issue (or a PR with P1+P2) on `enfein/mieru` with the probe and the
   fork test.
2. If the flake must be suppressed before upstream ships, make the
   `multiplexing: off` trade-off a product decision; do not merge an adapter
   retry silently.
3. Keep `TestMieruClientObservesServerClosingAnIdleUnderlay` running in a `-race`
   quarantine job; acceptance for the fix is 50× `-race` green plus the SDK unit
   test.
