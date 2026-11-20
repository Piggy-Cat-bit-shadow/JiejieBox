# Two-by-two cross debug — window C — autonomous exploration

Third and last window of the pairwise round: no fixed pair was assigned, so the work started from a
structural census and then narrowed to the pairs below.

- Repository: `Piggy-Cat-bit-shadow/JiejieBox`, branch **`testing`**, no new branch
- Starting HEAD: `bebb5a35a` (the state the two directed windows left). Commits added:
  `6eb1f1a8b` (certificate reload) plus these reports
- Platform: Windows amd64, Go **1.27.4**; `-race` with the bundled mingw64 gcc; no Administrator
  token, no Linux netns, no device
- Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` / `NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`

## 0. Result in one paragraph

One production defect was reproduced and fixed: **the certificate store's file-watcher reload was not
serialised**, so two overlapping reloads could publish in the opposite order to their reads and leave
the process trusting an OLDER snapshot than the files on disk - "the CA I just added is not trusted",
and in the revocation direction "the CA I just removed is still trusted". The store had no tests of
its own before this window, which is why the race survived. A second, **documented but not changed**
finding came out of the same pair: the store's reload never reaches the TLS clients that captured its
pool at construction, while the Windows TLS engine reads the pool live - two engines, two behaviours,
one store. Everything else this window touched is a census result rather than a defect: all seven
packages the CI ratchet lists as "no runner" were executed here, the five that can run are green, and
the two that need Administrator are `NOT_OBSERVABLE`. The remaining candidate pairs are recorded as
`NOT_COVERED` with what already exists, rather than claimed.

## 1. What was actually executed

| Command | Result |
| --- | --- |
| `go test -count=1 ./common/certificate/` | `ok` 2.08 s (new regression included) |
| `go test -race -count=2 ./common/certificate/` | `ok` 5.24 s |
| `go test -count=1 ./common/netcap/ ./common/physicalpath/ ./common/power/ ./common/schannel/ ./common/suspendwatch/` | all `ok` (0.069 s / 0.090 s / 4.772 s / 0.076 s / 0.131 s) |
| `go test -count=1 -v ./common/tlsspoof/` | 6 pure-logic tests PASS; 3 WinDivert integration tests FAIL: `Access is denied` |
| `go test -count=1 ./common/windivert/` | 5 integration tests FAIL: `open SCM: Access is denied` |

Platform note: the two failing packages need an Administrator token (WinDivert/SCM) and are listed as
`NOT_OBSERVABLE`; nothing about the wire or the capture path is claimed from them.

## 2. FIXED — `C-P01`: overlapping reloads could publish a stale trust set

**Where.** `common/certificate/store.go`. `update()` read every configured file into a new pool and
then published it under `s.access`, with nothing ordering two concurrent calls. `fswatch`
(`github.com/sagernet/fswatch` v0.1.2) fires each debounced callback from its own `time.AfterFunc`
goroutine, so two watched paths - or two writes to one path close enough together - run two updates at
once.

**Trigger and impact.** Callback 1 reads the files (snapshot A) and is descheduled; the file changes;
callback 2 reads the new content (snapshot B), publishes B; callback 1 resumes and publishes A. The
store is then holding a trust set **older than the files**. Consequences: a newly added CA is not
trusted by any TLS handshake until some later write happens to trigger another reload; a revoked CA
stays trusted for the same reason. The reload "ran", which is why a log-only observer sees nothing
wrong.

**Fix.** One mutex held across both the read and the publish (`updateAccess`), so "the last reload to
run" and "the reload that read the files last" are the same event. Reloads are file-change driven and
rare; `Pool()` keeps its reader lock and the only lock order is `update -> access`, so there is no
inversion and no hot-path cost.

**Red → green → mutation.**

| Stage | Evidence |
| --- | --- |
| red (mutation) | with the lock removed: `the store published the trust set read BEFORE the file changed over the one read after it`, in 0.08 s |
| green | the same test passes; package `ok 2.08 s`, `-race -count=2` `ok 5.24 s`; the two CAs are compared by real `x509` verification of a leaf, not by pointer identity |
| mutation control | the red above is the mutation: it is the committed test run against `update()` without the reload lock |

**Six-stage check.**

1. *Loaded*: `common/certificate` is built into every process (`box` builds the store from the
   `certificate` option); the reload path is the `Start(StartStateStart)` watcher.
2. *Reached*: `NewStore` builds the watcher when `certificate_path`/`certificate_directory_path` is
   set; the watcher's callback is `store.update`, which is exactly what the test drives.
3. *Decided*: the decision is "which snapshot is current"; before the fix it was the last publisher,
   now it is the last reader.
4. *Acted*: the published object is the `*x509.CertPool` that `Pool()` hands to TLS verification.
5. *Observed*: a leaf signed by the newer CA verifies (`x509.Verify` against `store.Pool()`), a leaf
   from the older one does not - an external witness, not the store's own flag.
6. *Settled*: both reloads return, the final pool matches the final file content, and the test leaves
   no watcher running (the store is constructed without `Start`).

**Collateral.** A reload still reads the files at one instant; a write that lands mid-read is picked
up by the next callback, unchanged. The test hook is nil in production and costs one branch.

## 3. Documented, not changed — `C-P02`: the reload does not reach the TLS clients

The same store owns the pool, but a TLS config *copies the pointer* when it is built
(`std_client.go:132`, `utls_client.go:222`; `Clone()` copies it again), and outbounds are constructed
once per box start. The Windows TLS engine, by contrast, reads `c.store.Pool()` **at verification
time** (`windows_client.go:296-302`). So the same store hot-reloads for one engine and not the other,
and on the Go/utls path (the default) a runtime CA change never reaches an existing outbound.

This is a real coordination gap, but the complete fix is an architecture change, not a defect patch:
`STDConfig()` is also captured at construction by the QUIC and HTTP transports, so the choices are
either cloning the TLS config per handshake (the hot path for every TLS connection) or replacing the
default verification with a manual one that has to reproduce session-resumption semantics. It is
recorded as `SOURCE_ONLY` with an executable reproduction in `REPRODUCE.md` rather than half-fixed at
the end of the round.

## 4. Census results

### 4.1 The "no workflow runs this" ratchet, executed

`scripts/ci/uncovered-packages.txt` lists seven packages with tests and no runner, all with the stated
reason "needs a device or privilege". All seven were run here: `common/netcap`,
`common/physicalpath`, `common/power`, `common/schannel`, `common/suspendwatch` are green; the three
`common/tlsspoof` integration tests and all five `common/windivert` integration tests fail only when
they open WinDivert/SCM (`Access is denied`), which is the documented reason. The ratchet's list is
therefore honest, and its runnable half is not hiding a red suite. `NO_DEFECT` for the five,
`NOT_OBSERVABLE` for the two.

### 4.2 The spoofed ClientHello contract, executed

`common/tlsspoof`'s pure-logic tests pass here: one well-formed handshake record, one segment, the
configured ALPN, the requested SNI, and no post-quantum key share (303 bytes). That is the wire-level
half of `X-P07`: a Go toolchain default cannot silently leak into the spoofed hello through this code.

## 5. Not attacked, and why

| Candidate | Label | What exists / why not |
| --- | --- | --- |
| `X-P02` SOCKS UDP association × idle/close | `NOT_COVERED` | the package already carries `udp_associate_relay_test.go` and `outbound_udp_ownership_test.go` (19 KB) and was green in the directed windows' runs; no new attack was designed |
| `X-P03` mieru underlay × reclaim | `SOURCE_ONLY` | the prior round's R-3 stands: `protocol/mieru` implements no `IdleConnectionReleaser`, so the trim walk cannot reach the SDK's underlays; a fix is fork-level and needs a pin |
| `X-P04` detour chain × cancel | `NOT_COVERED` | needs a two-layer outbound harness; not built |
| `X-P05` netns × fd owner | `NOT_OBSERVABLE` | no netns on this host |
| `X-P06` rule-provider download × failure | `NOT_COVERED` | `provider/remote` already has ten suites (retry, build deferral, cache, publish boundary, hardening, update-context bounds); no new failure mode was constructed |
| `X-P08` DNS multiplexing × cancel | `NOT_COVERED` | `dns` and `dns/transport/group` are dense and were green in the directed windows' runs |
| `X-P01` server-credential hot update | `NOT_COVERED` | the trust-store half is C-P01/C-P02; the *server* certificate (inbound TLS) has no file watcher, so the question is a feature request, not a bug |

## 6. Gates

`common/certificate` (with `-race`), the five runnable ratchet packages and the build are green; the
two Administrator-only packages are `NOT_OBSERVABLE`. No dependency pin moved, so
`release/source-lock.json` and `release/pin-runs.json` are untouched. The branch state, the CI
evidence for the preceding windows and the pin-run records are in
`docs/audits/next-cross-debug/pairwise-a/REPORT.md` §5.
