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
| `go test ./protocol/mieru/ -count=1` (after the 01B coverage tests) | `ok … 96.286s` |
| `go test -race ./protocol/mieru/ -count=1` (after the 01B coverage tests, CGO on) | `ok … 101.161s` |
| new 01B tests (run individually with `-v`) | both `PASS` (0.00 s / 2.46 s) |
| `python scripts/ci/release-freeze.py verify --lock release/source-lock.json` | `PASS: every pin in the tree is the pin the lock records.` |
| CI `Verify` on `c231dd51` (run 41329115810) | success — gofmt, dependency-pin, nested-module, tagged/untagged build and race jobs |
| CI `Android Core ARM64` on `c231dd51` (run 41329115850) | success |
| CI `Mobile contract verification` on `c231dd51` (run 41329115828) | success on attempt 2; attempt 1 failed the unrelated `transport/http` timing assertion (`2.903s` vs `>3s`) |

Not run, and why: no real WAN/production traffic (out of scope by contract); no Windows/Apple device
run (no build artifacts produced in this phase). The CI rows above are build/gate evidence, not
protocol-behaviour evidence. The `Mobile contract` first-attempt failure is recorded rather than
hidden: it is a scheduler-timing assertion in `transport/http/close_liveness_mechanism_test.go`, a
package this phase did not touch, and the identical commit passes on rerun.

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
5. **Half-close is not representable by this SDK.** The Mieru `Session`/client expose no
   `CloseWrite` (searched the v3.39.0 source), so the adapter cannot propagate a TCP write-half
   close; a caller that half-closes waits for a full close. This predates v3.39.0, is not caused by
   the read-deadline removal, and cannot be fixed in the adapter without an SDK API. It is recorded
   as a residual limitation, not a phase-01 defect.

## 7. 01B hostile review (2026-11-02)

The review was run against the 01A result and the real SDK pin; it added no production code. The
two tests it adds are committed in `bc349d83b2b8fdbd9ebb16b9bc3c5c7437effbdd`.

### 7.1 Pin, lock and consumer consistency

| Check | Result |
|---|---|
| root `go list -m github.com/enfein/mieru/v3` | `v3.39.0`, no `replace` |
| `test/go list -m` | `v3.39.0` (not left behind on v3.38.1) |
| diff of both module files against `0ab03590` | only the two Mieru lines changed; no Cronet replace moved |
| all consumers | `protocol/mieru/*` (client, server, config, tests); no other pin site |
| `release/source-lock.json` | Mieru is not one of the seven locked fork modules; `release-freeze.py verify` = `PASS: every pin in the tree is the pin the lock records.` |
| controlled Cronet native archives | untouched (`cronet_native` still `8c68ce89873c`) |
| `go mod verify` (root and `test/`) | `all modules verified` |

### 7.2 Ledger re-verification (not title matching)

* **AnyTLS**: `SagerNet/sing-anytls` HEAD is `580984e4…`, exactly the JiejieBox pin; the module has
  no tags. No newer stable exists (re-checked 2026-11-02).
* **Snell**: `SagerNet/sing-snell` HEAD is `bc5a12ac…`, exactly the pin; no tags.
* **MASQUE**: `protocol/masque` does not exist at `SagerNet/sing-box` tag `v1.14.4` (contents API
  returns "does not exist"), so the "dev-only, not in stable" statement is source-verified; the
  earlier plan's assumption that upstream has no MASQUE at all was the outdated part.
* **forkop Mieru**: the clone pins `mieru v3.33.0` and its adapter has no fallback/generation
  lifecycle, so it cannot supply a patch for this adapter.
* **X-04**: the `dialStreamUp` upload goroutine at `transport/v2rayxhttp/conn.go:224-231` still
  ignores a non-200 upload response; the row stays a real phase-04 candidate.
* **Mieru rows**: every accepted change (`66f54e2`, `18ca10d`, `a2711f6`, `2b7d62b`, `f7c2c81`,
  `773d774`) is reachable from tag `v3.39.0`; none is an orphan or a rewritten duplicate, so no
  patch-id reconstruction was needed for this module.

### 7.3 Cancellation, ownership and the new SDK semantics

* The dial handshake is still bounded by `PostDialHandshake`'s own 10 s read timeout
  (`apis/internal/handshake.go:47-50`); the removed `serverRespTimeout` was a second, steady-state
  deadline. `dialDrainTimeout` (5 s) therefore still has its stated meaning.
* The new `bufpool` releases the encrypted buffer immediately after `recv.Decrypt`. The SDK's
  `aeadBlockCipher.Decrypt` uses `c.aead.Open(nil, …)` (`pkg/cipher/cipher.go:184`), i.e. it
  returns independently owned plaintext, so releasing the input is safe. JiejieBox does not use the
  SDK's `PacketOverStreamTunnel`/`UDPAssociateWrapper` on its UoT path, so there is no duplicate
  codec or double buffering.
* No adapter rule was removed or re-implemented: the 01A diff contains only the module files/sums,
  the regression test and the phase docs. Fallback selection, generation checks, `Close` drain and
  `InterfaceUpdated` swap are unchanged and covered by the existing suite.
* `Close`, double `Close`, close-before-start, close-during-dial and stale-generation delivery are
  covered by existing tests (`outbound_test.go`, `fallback_test.go`, `fallback_e2e_test.go`), which
  the 01B run re-executed.

### 7.4 New attack tests

1. `TestMieruEmptyUDPDatagramRoundTrip` (`udp_edge_test.go`) — sends a zero-length datagram through
   the real client/server loopback and then a normal one. It would fail (EOF/hang) if the codec
   turned an empty datagram into EOF or lost frame alignment. This is the JiejieBox equivalent of
   the post-v3.39.0 `UDPAssociateWrapper` fix, and it closes the "check separately" note in
   `UPSTREAM_LEDGER.md` M-04.
2. `TestMieruClientObservesServerClosingAnIdleUnderlay` (`idle_underlay_test.go`) — a TCP proxy
   closes the underlay after 900 ms of silence (the SDK's server-side timeout has a 1-minute
   minimum, so the proxy reproduces the event). The blocked read must fail before its own 8 s
   deadline, and a new dial must build a new underlay. It would fail if the client lost the closure
   or left the session stranded.

Both pass; neither required a code change. The existing `TestMieruOutboundCloseTerminatesSessions`
already covers an explicit close, and `TestOutboundLifecycle` already covers double `Close`.

### 7.5 Did the tests really run?

`verify.yml`'s "race on the fork's own data paths" step runs
`go test -race -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS)" ./protocol/mieru/...` (line 486);
the 01A regression test ran there on `c231dd51`, so CI has already executed the v3.39.0 contract
test rather than only compiling it. The 01B test files carry no build tag and the step has no
`-short`, so the same gate runs them on the 01B head; this handoff records their local result
(§5) and does not claim a CI result that has not happened at commit time. `SKIP` was never reported
as `PASS`.

### 7.6 Conclusion

No production-code defect was found in the 01A Mieru work. The review produced two coverage pins
and one recorded SDK limitation (half-close); all pin/lock/consumer checks pass, and the ledger's
Mieru claims were re-verified against the tag rather than against commit titles.

## 8. Handoff to phase 02

Phase 02 (Naive client / Cronet) starts from:

* `UPSTREAM_LEDGER.md` §3 rows N-01…N-06;
* `FORK_CANDIDATES.md` §2 (`Piggy-Cat-bit-shadow/cronet-go`, `klzgrad/naiveproxy`);
* the rule that Pin ≠ version number: inspect `go.mod` replaces, the four-module cronet pin, and
  `cronet_native` (`8c68ce89873c`) in `release/source-lock.json` before proposing any move;
* row N-03 is the **server**-side idle semantic and belongs to phase 03, not 02.
