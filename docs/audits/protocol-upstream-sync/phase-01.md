# Phase 01A — upstream reuse map and Mieru stable update

| | |
|---|---|
| Phase | 01A (construction) |
| Date | 2026-11-02 |
| Branch | `testing` (the repository's real default branch; there is no `main`) |
| Base at start | `7e6cc9cc` local, fast-forwarded to `0ab035904de5536273f6070a766b4fc657d7d77c` after `git fetch`; the work tree was clean, so the fast-forward was safe |
| Code commit | `fac646ca20eca25a0129c5580df3d0497b44963c` |
| Handoff docs commit | this file + `UPSTREAM_LEDGER.md` + `FORK_CANDIDATES.md` + `STATE.md` |
| Next phase | `02` — Naive client / Cronet |

## 1. What this phase actually delivered

1. **Synced before working.** The checkout was 14 commits behind `origin/testing`; `git fetch` +
   `git merge --ff-only origin/testing` moved it to `0ab03590` with no local changes lost.
2. **Built the reusable map**, not a report: `UPSTREAM_LEDGER.md` carries per-protocol rows with a
   named upstream revision, a fork candidate, the local file/symbol, a coverage label and a
   decision; `FORK_CANDIDATES.md` records fork heads and what each is good for. Every later phase
   can start from a row instead of re-searching.
3. **Shipped the Mieru update**: SDK `v3.38.1 → v3.39.0` in the root and `test/` modules, no
   adapter rewrite, plus a regression test that fails on the old pin and passes on the new one.

The phase deliberately did **not** pre-empt phases 02–06: no Naive, XHTTP, MASQUE or other
protocol code was touched. Their rows are candidates with follow-ups.

## 2. Reading v3.39.0 at the source level (not from release bullets)

The release note has two bullets; the diff has four relevant changes and one non-change.

| Change | Where | What it means for this adapter |
|---|---|---|
| Client steady-state read deadline removed (`66f54e2`, issue #321) | `pkg/protocol/session.go` | Before: every client `Session.Write` stored `readDeadline = now+10s`; `Session.Read` captured it once and returned `ErrTimeout` when it fired. A read that started after a write died 10 s later **even though the upload was healthy** — the reported iperf3 one-way upload stall. After: no such arming. |
| `bufpool` (`18ca10d`, `a2711f6`, read/write paths) | `internal/bufpool`, `pkg/protocol/underlay_{stream,packet}.go`, `apis/common/packet_over_stream.go` | Size-classed pooling replaces per-segment `make`. Ownership is explicit: `Acquire → use → Release`; `Decrypt` returns independently owned plaintext, so the encrypted buffer is released right after. |
| Server `tcp_idle_timeout` (`2b7d62b`, `f7c2c81`, `773d774`) | `ServerAdvancedSettings`, `Mux.SetServerTCPIdleTimeout`, `IdleTrackingConn`, `cleanUnderlay` | New opt-in server feature, minimum 1m, empty = off. `IdleTrackingConn` is installed unconditionally on server TCP underlays, but `closeIfIdle` only runs when the timeout is `>0`, so default behaviour is unchanged. |
| Post-release `main` commits (`eaad5d5`, `0fe6d64`, `addba18`) | `apis/common/udp_associate_wrapper.go`, `pkg/socks5/udp.go` | **Not in v3.39.0.** One is a real zero-length-UDP fix; JiejieBox's UoT path uses its own codec, so it is deferred to the next stable. |
| **Non-change**: handshake bound | `apis/internal/handshake.go:47-50` | `PostDialHandshake` still sets a 10 s read timeout around the SOCKS5 reply and clears it afterwards. The removed deadline was a second, steady-state one. This is why `fallback.go`'s comment and `outbound.go`'s `dialDrainTimeout` rationale remain true. |

## 3. Why no adapter code changed

* **Dial/handshake contracts**: a dial is still bounded by the SDK's 10 s handshake read timeout;
  `dialDrainTimeout` (5 s) still prevents teardown from inheriting it; `Close`/`InterfaceUpdated`
  still drain in-flight dials and stop the generation.
* **UoT ownership**: `protocol/mieru/uot.go` already replaced the SDK's
  `PacketOverStreamTunnel` + `UDPAssociateWrapper` (which allocated three times per datagram) with
  a direct pooled codec. The new `bufpool` therefore helps the underlay write path, and there is no
  double-buffering layer to unwind.
* **Failure classification**: `classifyDialError` and the fallback selector key off dial errors,
  not on steady-state read timeouts; the handshake timeout that produces the "transport" class is
  unchanged.
* **Server**: no JiejieBox config sets `tcp_idle_timeout`, so the inbound keeps its previous
  behaviour exactly.

A code change here would have been a rewrite of something the SDK already owns correctly. The
ledger records the server idle-timeout feature as **deferred**, not silently adopted: it needs an
option/schema/docs surface and a ≥1m policy decision.

## 4. Regression test

`protocol/mieru/upload_idle_test.go` — `TestMieruClientReadSurvivesLongUnidirectionalUpload`.

It reproduces the defect through the real adapter and SDK, over loopback:

1. a discard router accepts the inbound session and stays silent;
2. the client writes `arm` (this is the write that armed the old deadline) and the server reads it;
3. the client starts a read that waits for application data;
4. the client keeps uploading for 11 s (past the former 10 s deadline);
5. only then does the server write a reply.

On `v3.38.1` the read returns `TIMEOUT` at 11.05 s; on `v3.39.0` it returns the reply. The test is
skipped under `-short` because the regression window is the old 10 s deadline.

## 5. Verification actually run

Environment: `go1.25.5 windows/amd64`; `-race` required `CGO_ENABLED=1` with
`C:\msys64\mingw64\bin` on `PATH` (the plain `go test -race` fails with "-race requires cgo").

| Command | Result |
|---|---|
| `go test ./protocol/mieru/ -count=1` (baseline v3.38.1, before the bump) | `ok … 82.098s` |
| new test on `v3.38.1` (control) | `FAIL … TIMEOUT` at `upload_idle_test.go:116` |
| new test on `v3.39.0` | `PASS … 11.05s` |
| `go test ./protocol/mieru/ -count=1` (full suite, v3.39.0) | `ok … 91.161s` |
| `go test -race ./protocol/mieru/ -count=1` (CGO on) | `ok … 97.309s` |
| `go mod tidy -diff` (root and `test/`) | exit 0 |
| `go list -mod=readonly -m all` (root and `test/`) | exit 0 |
| `go mod verify` (root and `test/`) | `all modules verified` |
| `go build ./protocol/mieru/ ./include/` | exit 0 |

Not run, and why: no real WAN/production traffic (out of scope by contract); no Windows/Apple
device run (no build artifacts produced in this phase); no CI run is claimed as evidence — the
local suite above is the evidence. The push to `testing` triggers `verify.yml` (no path filter) and
`android-core-arm64.yml` (`go.mod` / `protocol/**`) by their declared triggers; this handoff does
not claim their result.

## 6. Risks and open decisions

1. **The removed read deadline was also an accidental dead-peer detector.** A peer that disappears
   without closing will now be detected by Mieru's session heartbeat / TCP, not by a 10 s read
   timer. That is upstream's design decision in a stable release; do not add an adapter timeout
   without a failing test.
2. **Server idle timeout stays opt-in and unimplemented here.** Existing Mieru inbounds behave
   exactly as before; the feature is queued in `UPSTREAM_LEDGER.md` (M-03).
3. **The other protocols were only mapped.** No claim is made that any fork is "behind"; the rows
   name a hypothesis and the file to check.
4. **Controlled Cronet archives were not touched.** Any Naive upgrade must move source pins and
   native archives together, per `release/dependencies.json` and `release/source-lock.json`.

## 7. Handoff to phase 02

Phase 02 (Naive client / Cronet) starts from:

* `UPSTREAM_LEDGER.md` §3 rows N-01…N-06;
* `FORK_CANDIDATES.md` §2 (`Piggy-Cat-bit-shadow/cronet-go`, `klzgrad/naiveproxy`);
* the rule that Pin ≠ version number: inspect `go.mod` replaces, the four-module cronet pin, and
  `cronet_native` (`8c68ce89873c`) in `release/source-lock.json` before proposing any move;
* row N-03 is the **server**-side idle semantic and belongs to phase 03, not 02.
