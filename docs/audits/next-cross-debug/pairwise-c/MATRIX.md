# Window C — pair matrix (autonomous exploration)

Routing table for the pairs this window chose, attacked and ruled out. `REPORT.md` has the narrative
and the six-stage evidence; `REPRODUCE.md` has the commands. Labels: `FIXED` / `NO_DEFECT` /
`SOURCE_ONLY` / `NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`.

- Repository: `Piggy-Cat-bit-shadow/JiejieBox`, branch `testing`, start `bebb5a35a` (the state the
  two directed windows left; their reports and fixes are not modified here)
- Platform: Windows amd64, Go 1.27.4; `-race` with mingw64 gcc; no Administrator token (so no
  WinDivert/capture), no Linux netns/nftables, no iOS/macOS device
- Commits added: `6eb1f1a8b` (certificate reload), plus these reports

## Summary

| Row | Systems (repo) | Shared object | Conclusion | Commit |
| --- | --- | --- | --- | --- |
| C-P01 | `common/certificate.Store` reload × the file watcher (`github.com/sagernet/fswatch`) | the published trust pool, read from disk by each reload | **FIXED** | `6eb1f1a8b` |
| C-P02 | `common/certificate.Store` × the Go/utls TLS clients (`common/tls`) | `RootCAs`: the pointer a TLS config captured at construction vs the pool the store owns now | **SOURCE_ONLY** (documented, not changed) | — |
| C-P06 | `scripts/ci/uncovered-packages.txt` × the packages it lists | whether the ratchet's "no runner" list is honest, and whether its runnable half is green | **NO_DEFECT** (5 executed green) / **NOT_OBSERVABLE** (2 need Administrator) | — |
| C-P07 | Go toolchain defaults × the spoofed ClientHello wire | `common/tlsspoof`'s fake ClientHello: record framing, size, ALPN, SNI, no post-quantum key share | **NO_DEFECT** (executed, pure logic) / **NOT_OBSERVABLE** (injection needs WinDivert) | — |
| X-P01 | certificate hot-update × TLS/Reality/Naive handshake | the trust pool and the server credential cache | partially covered by C-P01/C-P02; the server-credential half is **NOT_COVERED** | — |
| X-P02 | SOCKS UDP association × flow idle/close | association map, datagram handler | **NOT_COVERED** here (heavily tested already: `udp_associate_relay_test.go`, `outbound_udp_ownership_test.go`) | — |
| X-P03 | mieru underlay × OOM reclaim | the SDK's mux underlays and the reference-manager walk | **SOURCE_ONLY** (prior round's R-3 still stands: `protocol/mieru` implements no releaser) | — |
| X-P04 | detour chain × cancel/timeout | the outer dial context vs the inner handshake/DNS | **NOT_COVERED** | — |
| X-P05 | netns holder × fd/socket lifetime | namespace references and their fds | **NOT_OBSERVABLE** (no netns on Windows) | — |
| X-P06 | rule-provider download × failed update | the live rule set vs a half-downloaded one | **NOT_COVERED** here (`provider/remote` already has 10 suites: retry, deferral, publish boundary, hardening) | — |
| X-P08 | DNS multiplexing × timeout cancel | per-query stream and the shared DNS transport | **NOT_COVERED** here (`dns` + `dns/transport/group` are dense and green) | — |

## C-P01 — an older reload can publish over a newer one (FIXED)

| Column | Value |
| --- | --- |
| Systems and repos | ① `common/certificate.Store` (JiejieBox) ② `github.com/sagernet/fswatch` v0.1.2 (module, not a fork) |
| Owners | ① owns the trust pool and its publication; ② owns *when* a reload runs, and it runs each debounced callback from its own timer goroutine |
| Shared object | the pool built from the configured files, and the store's `currentPool` slot |
| Failure timeline | T1 write 1 → callback 1 starts `update()` → reads the files (snapshot A) → T2 write 2 → callback 2 starts `update()` → reads the files (snapshot B) → publishes B → T3 callback 1 (still holding snapshot A) publishes A. The store now holds A, older than the file content, and nothing else runs until the next write event |
| Why exactly 2 systems | the store decides *what* the trust set is; the watcher decides *how many reloads run concurrently*. Neither is wrong alone; the pair is |
| Product path executed | `Store.update()` as the watcher's callback calls it, through the real constructor (`NewStore` with `certificate_path`), driven by the same two-CA file rewrite a user makes |
| Evidence before | `the store published the trust set read BEFORE the file changed over the one read after it` (red in 0.08 s with the reload held in its publish window) |
| Evidence after | green, package `ok 2.08 s`, `-race -count=2` `ok 5.24 s`; the same test with the lock removed is red again (mutation control) |
| Uncovered boundary | the *content* of a reload is still a read of the files at one instant, so a write that lands mid-read is picked up by the next callback (unchanged, and correct); the platform-anchor publication on darwin is not exercised here |
| Commit | `6eb1f1a8b` |

## C-P02 — the store's reload does not reach the TLS clients that captured its pool (SOURCE_ONLY)

| Column | Value |
| --- | --- |
| Systems and repos | ① `common/certificate.Store` (JiejieBox) ② `common/tls` clients: `std_client.go`, `utls_client.go`, and the `STDConfig()` consumers (QUIC, HTTP transports) |
| Owners | ① owns the live pool; ② owns the `tls.Config` each outbound keeps for its lifetime |
| Shared object | the `*x509.CertPool` pointer. `Store.Pool()` returns the current one; a TLS config copies the pointer once |
| Failure timeline | T1 outbound constructed → `RootCAs = store.Pool()` (pool A) → T2 CA file changes → the store publishes pool B → T3 every connection that outbound makes still verifies against pool A. Adding a CA has no effect; removing one does not revoke it |
| Why exactly 2 systems | the reload is the store's event; the config is the TLS layer's copy. The store cannot rewrite a `tls.Config` it does not own |
| What the code shows | `std_client.go:132` and `utls_client.go:222` assign `RootCAs = adapter.RootPoolFromContext(ctx)` at construction and `Clone()` copies it. The **Windows** TLS engine (`windows_client.go:296-302`) reads `c.store.Pool()` **at verification time**, so the two engines behave differently for the same store |
| Why it is not fixed here | a complete fix touches four consumer families (`STDConfig()` is captured by the QUIC and HTTP transports at construction too) and either clones the config per handshake — the hot path for every TLS connection — or replaces the default verification with a manual one that must reproduce session-resumption semantics. That is an architecture change, not a defect-local patch, and it needs its own design + CI cycle |
| Next step (executable) | build a `Store` from a temp CA file, construct a `tls.NewClient` config, handshake against a local server whose leaf is signed by a second CA, rewrite the CA file and wait for `store.Pool()` to change, then handshake again through the SAME config: today it fails with `unknown authority`, and a freshly built config succeeds. The Windows engine on the same store succeeds, which is the intended semantics |
| Commit | — |

## C-P06 — the "no workflow runs these" ratchet, executed (NO_DEFECT / NOT_OBSERVABLE)

`scripts/ci/uncovered-packages.txt` lists seven packages with tests and no runner. All seven were run
on this host:

| Package | Result |
| --- | --- |
| `common/netcap` | `ok` 0.069 s |
| `common/physicalpath` | `ok` 0.090 s |
| `common/power` | `ok` 4.772 s |
| `common/schannel` | `ok` 0.076 s |
| `common/suspendwatch` | `ok` 0.131 s |
| `common/tlsspoof` | 6 pure-logic tests `ok`; 3 WinDivert integration tests fail with `Access is denied` |
| `common/windivert` | 5 integration tests fail with `open SCM: Access is denied` |

The list's reason ("needs a device or privilege") is accurate: the only failures are the two that
open WinDivert/SCM, which requires an Administrator token this session does not have. The five
runnable ones are green, which is evidence the ratchet is not hiding a known-red suite.

## C-P07 — the spoofed ClientHello contract, executed (NO_DEFECT for the pure half)

`common/tlsspoof`'s fake ClientHello tests run here and pass: one well-formed handshake record, one
segment, `ALPN` set, the requested `SNI`, and **no post-quantum key share** (the Go 1.27 default that
the release profile pins back is not reintroduced by the spoofed hello). Measured size 303 bytes.
The injection half needs WinDivert and is `NOT_OBSERVABLE` without Administrator.

## Candidates not attacked here (and why)

`X-P02` (SOCKS UDP association) already has `udp_associate_relay_test.go` and
`outbound_udp_ownership_test.go` (19 KB) and passed in the directed windows' suite runs; `X-P03`
(mieru underlay) is the prior round's recorded R-3, still true; `X-P04` (detour chain × cancel) and
`X-P06` (rule-provider failure) were not built as new attacks — their packages are dense
(`provider/remote` alone has 10 suites) and this window spent its budget on the certificate pair;
`X-P05` needs netns. `X-P08` (DNS multiplexing) remains covered by the `dns` suites the directed
windows ran green.
