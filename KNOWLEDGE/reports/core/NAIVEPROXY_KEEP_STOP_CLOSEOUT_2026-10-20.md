# NaiveProxy KEEP / STOP — closeout

**Date:** 2026-10-20 (Asia/Shanghai)
**Branch:** `testing` only. No branch was created, nothing was force-pushed, no tag or release was made.
**Construction start:** `4f8bc1eaee79ea1e8f4003ea90c464cf5a8a63ca` (local and remote agreed at the start).
**Delivered tip:** the commit that adds this file; `git log -1 --format=%H` on `testing`.
**Status:** this is a point-in-time closeout of the Naive performance work. Performance itself is
**`KEEP / STOP`**: no protocol data path, engine default, window, congestion control or native
dependency was changed, and no further performance round is proposed.

---

## 1. What this round was for

The read-only performance audit left exactly two actionable items — a broken test and a CI hole —
plus one suspected documentation defect. This round closes them, without reopening performance work.

| item | verdict |
|---|---|
| **A1** pool-isolation self-test counted a stale NetLog event id | **FIXED** and verified against a real Cronet engine |
| **A2** the native self-test ran nowhere in CI | **IMPLEMENTED and EXECUTED, then REVERTED**: the macOS runner cannot carry the native bootstrap → `BLOCKED_BY_ENVIRONMENT`, see §3 |
| **A3** "mojibake" in `docs/naive.md` | **A3_ENCODING_FALSE_POSITIVE** — the blob is clean UTF-8; baseline section added instead |
| **A4** audit archived + closeout | **DONE** — the audit was already archived byte-identically; this report is new |

---

## 2. A1 — the test that could never pass

### Root cause

`TestNaiveSelfInsecureConcurrency` counted `"type":249` in the raw NetLog. Chromium 154 assigns
`HTTP2_SESSION_INITIALIZED` the id **267**, so the count was always zero and the invariant "three
insecure pools open three HTTP/2 sessions" had never actually run. Measured on this host against the
real engine:

```
HTTP2_SESSION_INITIALIZED id=267 sessions=3
LEGACY-RED-CHECK count(type=249)=0 dynamic(id=267)=3
```

The legacy line was a temporary probe used to capture that evidence and was removed before the
commit; the surviving assertion is the dynamic one.

### The fix

- The id is resolved **from the NetLog's own `constants` block at runtime**, from whichever
  constants sub-map names the event, so a renamed section does not break it and the next Chromium
  renumbering cannot silently pass.
- Events are counted with a **streaming `json.Decoder`** over the `events` array (NetLog files reach
  hundreds of megabytes); counts are kept per type id, so the result does not depend on whether
  `constants` appears before or after `events`.
- Failures are loud and diagnosable: no constants, unknown event name (the error lists what the file
  does contain, bounded to 12 names), not an object, truncated JSON.
- NetLog lifecycle: `t.TempDir()` instead of a shared `/tmp` path (which does not exist on Windows),
  **non-verbose** logging (the event is recorded anyway; verbose files were 142–325 MB), and a
  single `StopNetLog` through `sync.OnceFunc` instead of a stop plus a deferred second stop. The same
  lifecycle fix was applied to the adjacent ECH test, which keeps verbose logging because it asserts
  on a param value.
- Parser unit tests cover three different ids (249 / 267 / 512), a decoy id inside another event's
  params, constants-after-events ordering, the four failure shapes, and a valid file with zero
  sessions — so a Chromium renumbering fails readably instead of counting zero.

### Real evidence

Run with the production native library on PATH, `GOOS=windows GOARCH=amd64 CGO_ENABLED=0`, tags
`with_naive_outbound,with_purego` (the Windows profile uses purego to load `libcronet.dll`):

| test | result |
|---|---|
| `TestNaiveSelf` | PASS (0.36 s) — direct neighbour regression |
| `TestNaiveSelfInsecureConcurrency` | PASS (`-count=2`), logs `id=267 sessions=3` both times |
| parser unit tests | PASS (5 tests, 11 cases) |

Native engine: Cronet Chromium **154.0.8037.49**, `libcronet.dll` 9 889 280 bytes. Full logs in the
repository history are not attached: they contain none of the values the instruction forbids, but
they are unnecessary — the assertions above are reproducible with the commands in §7.

---

## 3. A2 — CI

`.github/workflows/client-macos.yml` gained one step, `Naive native self-test (test module)`,
immediately after the existing macOS unit-test step:

```yaml
export GOOS="$GOOS_TARGET" GOARCH="$GOARCH_TARGET" CGO_ENABLED="$CGO_ENABLED_TARGET"
cd test
go test -tags "$(cat "../$TAGS_FILE")" -count=1 -timeout 5m \
  -run '^(TestNaiveSelf|TestNaiveSelfInsecureConcurrency)$' -v .
```

The design was right and is kept here as the record: the `test` directory is a separate Go module, so
the root package list could never reach it; the tags are the artifact's tags
(`release/DEFAULT_BUILD_TAGS` contains `with_naive_outbound`); `CGO_ENABLED=1` links the darwin/arm64
native library; it is a hard gate with no `continue-on-error`; and the manual dispatch policy is
untouched.

**What actually happened when it ran** (run
[39400834308](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39400834308)):

- the step **executed** — the log shows the test binary compiling with those tags, linking the
  native library, and running both test names;
- both tests **failed for an environmental reason**, not a logic one: the native engine tried to
  bootstrap to public endpoints (`[2001:4860:4860::8888]:443` and two Cloudflare addresses on the
  mixed-inbound port), the runner has no IPv6 route, the IPv4 dials timed out, and the CONNECT
  handshake then timed out after 13 s;
- the identical tests pass on Windows against the production `libcronet.dll`, so this is the runner's
  network path to the native bootstrap, not the test.

Repairing that would mean changing Cronet's native DNS bootstrap, which this round explicitly
excludes. Shipping the step anyway would make the manual macOS job permanently red, so it was
**reverted** (`1c64f1a41`). Status: **`BLOCKED_BY_ENVIRONMENT`** for GitHub's macOS runner. The step
is recorded above so it can be re-enabled on a runner where the native bootstrap works, or after the
native limitation is addressed separately.

---

## 4. CI execution record

| workflow | run | result |
|---|---|---|
| macOS arm64 (manual dispatch, `deep_checks=false`) | [39400834308](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39400834308) | **failure** in the new step; root cause above; step reverted afterwards |
| Verify (push) | [39400821022](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39400821022) | **success** |

The failure is the expected evidence that the coverage gap is real and that the runner cannot carry
it yet; it is not reported as a pass, and it is not hidden by a skip.

---

## 5. A3 — the documentation was never broken

The audit reported that `docs/naive.md` showed `鈫?` where an arrow belongs. Checked against the Git
blob bytes at `HEAD`, with strict UTF-8 decoding:

```
bytes: 19757
utf-8 strict: OK
replacement chars: 0
mojibake arrow (U+9236): 0
real arrow (U+2192): 4
```

**`A3_ENCODING_FALSE_POSITIVE / NO_CHANGE`.** The original file was not edited; the historical
benchmark line (`470 → 214 ns`, `482 → 216 ns`, `796 → 215 ns`, `1 → 0 alloc`) is preserved word for
word, and it stays attributed as a historical local benchmark.

What was added instead is a new `## Performance baseline (measured)` section in `docs/naive.md`:

- the snapshot (2026-10-20, `5b46c36d3`, Windows 11 amd64, 16 logical cores, Chromium 154.0.8037.49,
  loopback, one process), and a link to the audit;
- H2 457.7 MiB/s / 5.5 CPU s/GiB and H3 158.2 MiB/s / 17.0 CPU s/GiB, labelled as **combined client
  and server cost on loopback**;
- the 32 MiB bulk shape (523 writes / 523 flushes, `writeChunked=0`, ~64 KiB per write);
- the engine matrix with the explicit statement that engine count and stream count moved together,
  so no causal or "2 engines is optimal" claim is made;
- the H3→H2 fallback statement (no regression while H3 works; ~4 s first attempt when UDP is gone,
  which is recovery, not throughput);
- **`NOT_COMPARABLE`** against the official NaiveProxy client or Caddy, and `NOT_RUN` for WAN,
  mobile and native-internal profiling;
- the `runtime.cgocall` 72.6% clarification: a Go-profiler visibility limit, not a Chromium CPU
  figure.

---

## 6. Performance verdict: `KEEP / STOP`

Nothing in this round touched `protocol/naive/**`, `cronet-go`, engine/window/QUIC defaults, the
H3→H2 budget logic, or any release/signing path. The measured baseline is recorded so a future
change has something honest to compare against; it is not a mandate to optimise further.

---

## 7. Verification

| command | result |
|---|---|
| `cd test && go test -run 'TestNetLogEventCount' -count=1 .` | PASS (5 tests) |
| `cd test && PATH=…/libcronet.dll-dir:$PATH go test -tags 'with_naive_outbound,with_purego' -run '^TestNaiveSelf$' -count=1 .` | PASS |
| same, `-run '^TestNaiveSelfInsecureConcurrency$' -count=2` | PASS, `id=267 sessions=3` |
| `cd test && go vet -tags 'with_naive_outbound,with_purego' .` | clean |
| `gofmt -l` on the two changed Go files | clean |
| YAML parse of the changed workflow (PyYAML) | valid; 18 steps, new step at index 4 |
| `git diff --check` | clean |

Known limits: no WAN, no macOS runner execution from this host, no Caddy/official-client comparison.
Those stay `NOT_RUN` / `NOT_COMPARABLE`.

---

## 8. Gate matrix

| gate | result |
|---|---|
| G0 repository state | `testing`, started at `4f8bc1eae` = `origin/testing`; no branch, no force push; only the pre-existing `clients/apple` + `clients/desktop` pointers dirty |
| G1 no fixed event id | DONE — runtime constants; 11 parser cases including three ids and four failure shapes |
| G2 native E2E | DONE on this host for `TestNaiveSelf` and `TestNaiveSelfInsecureConcurrency` (Windows amd64, purego, real `libcronet.dll`) |
| G3 CI contains the tests | **NOT SHIPPED**: the step was implemented and executed on the macOS runner ([39400834308](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39400834308)) and failed on the native bootstrap, so it was reverted rather than shipped red; `BLOCKED_BY_ENVIRONMENT`, reproduction in §3 |
| G4 NetLog safety | DONE — temp dir, non-verbose, single stop, streaming parser, bounded error output |
| G5 A3 by blob bytes | DONE — false positive; history preserved; new baseline section |
| G6 KEEP/STOP with limits | DONE — `NOT_COMPARABLE` and `NOT_RUN` stated explicitly |
| G7 modules and regressions | DONE — parser tests, native tests, `go vet`, `gofmt`, YAML; no root-module change |
| G8 delivery | DONE — commits pushed to `testing`; audit already archived byte-identically at `a9d835bd9` |
