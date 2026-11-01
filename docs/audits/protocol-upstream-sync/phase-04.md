# Phase 04A — XHTTP correctness

| | |
|---|---|
| Phase | 04A (construction) |
| Date | 2026-11-02 |
| Branch | `testing` (default; no `main`) |
| Start base | `e684354f` (clean, synced) |
| Code commit | `96bd665d106222d43e4339011286e833bf432060` — stream-up upload refusal; `b5c12db3a8bd48cf6807dc880ab2b2ea6f561a26` — refusal precedence |
| Next | `05` — XHTTP data path / XMUX performance |

## 1. The fix: a refused stream-up upload now fails the stream

`dialStreamUp` starts the upload POST and the download GET together. The upload goroutine
treated only a `RoundTrip` error as fatal; a successful HTTP response with `400/404/413` was
drained and ignored. The red test hit its 3 s budget with "DialContext still parked after the
raise failed", and a later `Write` had no status error to report.

The fix in `transport/v2rayxhttp/conn.go`:

1. checks `upResp.StatusCode`; non-200 closes the body **without draining** (the stream is being
   abandoned, so the keep-alive is not worth the unbounded read);
2. breaks the upload pipe with `v2ray-xhttp: unexpected upload status: <status>` so a `Write` that
   arrives after the conn was raised sees the server's verdict;
3. cancels the conn-scoped context, which ends the pending download `RoundTrip` and stops a reader
   already bound to the download body;
4. sends the status to a **dedicated** refusal channel so a dial that has not returned fails with the
   server's status.

The dedicated channel matters: the first revision queued the refusal on the transport-error channel
and then cancelled the context. Cancelling ends the download, the hung upload `RoundTrip` returns
`context canceled`, and that cancellation could reach the dial first; the existing
`TestStreamUpDialFailsOnDownloadError` also showed the shared channel masking a genuine download
failure. `awaitRaise` now prefers only `uploadRefused` when `created` closes at the same time, so a
refusal wins its own cancellation while an ordinary upload transport error never hides the download
cause.

A `200` body is still drained and closed as before; closing it early can reset the paired download
stream before the first response byte is read.

Tests added in `stream_up_refusal_test.go`:

* `TestStreamUpDialFailsOnUploadBadStatus` — download pending, upload 400: dial must fail with the
  status, the pending download RoundTrip must be aborted, the XMUX usage must return to 0 and the
  refusal body must be closed. It failed before the patch as quoted above.
* `TestStreamUpRaisedConnFailsOnLateUploadRefusal` — download answered 200 and the conn was raised,
  then the upload is refused: `Write` returns the status and `Read` ends with an error instead of
  its deadline.
* `TestSplitConnWriteCarriesTheUploadRefusal` — the pipe half: `uploadFailed` makes the next
  `Write` return that error rather than `ErrClosedPipe`.

## 2. Candidate verdicts (client-side only)

| Candidate | Verdict | Evidence |
|---|---|---|
| Xray `dffc7ada5` `packet-up` `Request.GetBody` for GOAWAY replay | `ALREADY_PRESENT` | `applyUplinkData` sets `GetBody` for body/auto placement with the payload slice it owns (`meta.go:430-453`), comment cites the same GOAWAY failure |
| Xray `eef6e63bc` `WaitReadCloser` data race | `INAPPLICABLE` | JiejieBox has no `WaitReadCloser`; late-bound readers are synchronised on `created`/`readerErr` with deadline and deadlock tests |
| Xray `77f98eba0` client race | `ALREADY_COVERED` | the corresponding state is atomically guarded in `xmuxClient`/`connBreaker`; the package `-race` run passes |
| Xray `1aabe7ea7` no forced trailing slash when session/seq are not in the path | `ALREADY_PRESENT` | `barePathForStreamOne` appends the slash only for path placements (`meta.go:482-499`); `trim_test.go`, `configured_query_test.go` pin it |
| Xray `316e07a53` server `h2Params` | `INAPPLICABLE` | server-only option; JiejieBox ships the client |
| Xray `e7a69fa85` server accepts `OPTIONS` | `ALREADY_PRESENT` (client side) | `uplink_http_method` is upper-cased and only `GET` is special-cased (`meta.go:121-131`); no client-side rejection of `OPTIONS` |
| LX lx.25 "refused/ended stream-up upload fails Write" | `MERGED` | this commit; the rest of lx.25's packet-up pipeline was already present |
| `flyzstu/xhttp-box` independent tree | `DEFERRED` | phase 05 comparison oracle; not diffed here |

The `packet-up` POST path was checked separately: it already rejects non-200 and treats a stalled
200 body as a failure (`conn.go:1247-1266`), so the defect was specific to `stream-up`.

## 3. Verification

| Command | Result |
|---|---|
| new tests before the patch | `FAIL … DialContext still parked after the raise failed` (3.00 s) |
| new tests after the patch | `PASS` (0.00–0.01 s) |
| `go test ./transport/v2rayxhttp/ -run TestStreamUp -count=10` | `ok` (repeated for the tie-break race) |
| `go test ./transport/v2rayxhttp/ -run 'TestStreamUp\|TestSplitConnWrite' -count=1` (04B matrix) | all 400/404/413/500 subtests `PASS` |
| `go test ./transport/v2rayxhttp/ -count=1` | `ok … 17.791s` (04B run) |
| `go test -race ./transport/v2rayxhttp/ -count=1` | `ok … 18.948s` (04B run) |
| `go vet ./transport/v2rayxhttp/` | exit 0 |

No loopback Xray server binary was used; the fake `RoundTripper` exercises the same HTTP contract
without a network. Real-interop remains `UNVERIFIED` (no local Xray server in this environment).

## 4. 04B hostile review (2026-11-02)

Scope: only the 04 diff (`dialStreamUp`, `awaitRaise`, `splitConn.uploadFailed`) and its direct
tests. No 05 data-path work was touched.

### 4.1 Attacks run

* **Refusal status matrix** — upload POST answers 400, 404, 413 and 500 while the download GET is
  pending. Each response body's `Read` blocks forever, so a drain would park the dial instead of
  passing on an EOF body. All four subtests get the real status, close the body, abort the download
  and return `openUsage` to 0.
* **Post-raise refusal** — the download already answered 200 and a reader is parked on it when the
  upload is refused; `Write` returns the status and `Read` ends with an error rather than its
  deadline.
* **The cancellation tie** — the first revision queued the refusal on the transport-error channel
  and then cancelled the context. The existing `TestStreamUpDialFailsOnDownloadError` caught the
  consequence: the cancellation could win and replace the real status, and it could mask a genuine
  download failure. `b5c12db3` separates `uploadRefused`, queues it before the cancel, and lets only
  that channel win when `created` closes simultaneously.
* **Race/close** — the package `-race` suite (including the pre-existing close-aborts-pending-
  download and per-dial tests) passes; the new tests add no goroutine of their own.

### 4.2 Result

One defect was found by the debug pass and fixed in the 04 scope (the refusal/cancellation
precedence, `b5c12db3`). The status-matrix hardening is tests-only (`669fa275`). No other defect was
found; no 05 throughput work was pulled in.

Remaining limitation: these are fake-`RoundTripper` contract tests, not real Xray interop over
H1/H2/H3. A real handler matrix stays `BLOCKED_BY_ENVIRONMENT` (no local Xray server).

## 5. Handoff to phase 05

* The correctness gap that phase 04 was created for (X-04) is closed.
* Phase 05 owns the data path: packet-up batching, XMUX flow control, and the `xhttp-box` tree diff
  (X-06). Do not re-open the upload-status path without a new upstream revision.
* The `transport/v2rayxhttp` unit suite and `-race` are the regression gate for those changes.
