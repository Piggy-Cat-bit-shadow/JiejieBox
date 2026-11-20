# Triple-wise C — autonomous exploration: the fswatch driver family

Third and last window of the three-system round. No numbering was given, so the work started from a
map of owners, clocks and entry points, and followed the one driver family that had already produced a
real defect this session.

- Repository: `Piggy-Cat-bit-shadow/JiejieBox`, branch **`testing`**, no new branch
- Starting HEAD: `6acbd4a7f`. Commits added: `89fef8ca0` plus these reports
- Platform: Windows amd64, Go **1.27.4**, `-race` with the bundled mingw64 gcc
- Labels: `FIXED` / `NO_DEFECT` / `SOURCE_ONLY` / `NOT_COVERED` / `NOT_OBSERVABLE` / `POLICY_DECISION`

## 0. Result in one paragraph

One production defect was found and fixed: **the server TLS config's certificate reload was two data
races** - the two `fswatch` callbacks of a renewal staged the certificate and key in shared fields
without synchronisation, and every accepted connection read the config pointer without the lock the
watcher swaps it under. The new tests reproduce both shapes under `-race`, the fix serialises the
reload and locks the reader side, and the new tests also pin the invariant that a half-renewed pair is
never installed. The find came from following the **driver family** rather than the module list: the
`fswatch` callback model is what made this session's certificate-store defect possible, and the same
model has five users in this tree - two now fixed, two already correct, one recorded as not covered.

## 1. FIXED — the server TLS config's reload raced itself and its readers

**Where.** `common/tls/std_server.go`: `certificateUpdated` (the watcher callback) and the handshake
entry points (`Server`, `Client`, `STDConfig`, `Clone`).

**Trigger.** A normal certificate renewal: the certificate file and the key file are rewritten
(certbot and every other renewal tool does both). `fswatch` debounces per path and fires one callback
per path, each on its own goroutine, so the two callbacks overlap. Each one first stores the file
contents in `c.certificate` / `c.key` and then rebuilds the pair with `tls.X509KeyPair(c.certificate,
c.key)`, i.e. it reads the field the other callback writes. Meanwhile every accepted inbound
connection calls `Server(conn)`, which read `c.config` without the lock that the callback takes to
swap it.

**Impact.** Two data races on the normal renewal path of any TLS inbound server (Trojan, AnyTLS,
Hysteria, TrustTunnel, ShadowTLS, ...). A race is undefined behaviour by the Go memory model even when
the rebuilt pair happens to be valid; the practical failure shapes are a transient
`reload key pair: tls: private key does not match public key` (the reload is skipped and retried by
the next event) and, under `-race`, a reported violation in any instrumented build.

**Why it survived.** There was no `std_server_test.go`: the package had extensive client-side and
REALITY coverage, and the server's file-watching reload had never been executed by a test. With a
single callback the staging has one writer, so even a naive test would have passed.

**Fix.** A `reloadAccess` mutex held across the whole reload, so the pair is always rebuilt from one
callback's view of both files; and `RLock` around the `c.config` pointer read in `STDConfig`,
`Client`, `Server` and `Clone`. The published `*tls.Config` is still never mutated in place, so
returning the pointer keeps the contract that callers rely on.

**Evidence.**

| Stage | Evidence |
| --- | --- |
| red | `go test -race -run 'TestAReplacement…|TestConcurrent…' ./common/tls/`: two `WARNING: DATA RACE` reports on `std_server.go:299/305/307` (the staging fields and the pair rebuild), then `--- FAIL` |
| green | same tests pass; no race report |
| mutation | removing `reloadAccess` reproduces the staging race deterministically |
| behaviour kept | a certificate-first callback still reports the mismatch and the config **still serves the old pair** (asserted before the key callback); after both callbacks it serves the pair on disk |
| package | `go test -race ./common/tls/` `ok 4.98 s`; with `release/DEFAULT_BUILD_TAGS_OTHERS` `ok 3.64 s` |

**Six-stage check.**

1. *Loaded*: `common/tls` is in every build; `NewSTDServer` creates the watcher for
   `certificate_path`/`key_path`, and every TLS inbound server goes through `Server(conn)`.
2. *Reached*: the tests call `certificateUpdated` (the watcher's callback body) and the exact
   handshake entry points, not an internal helper.
3. *Decided*: the decision is "is this pair installable, and which config does a new connection get";
   the reload mutex and the read lock are what make the two answers one.
4. *Acted*: the served `*tls.Config` keeps its old pair until both halves match, then switches.
5. *Observed*: the DER served by `STDConfig()` after each step, and the `-race` report as the
   independent witness.
6. *Settled*: the readers stop, the callbacks return, and the config serves the files' pair; no
   goroutine is left parked (the test does not start the watcher).

## 2. The family map (what else uses the same driver)

`fswatch` has five users. Two are now fixed (`common/certificate` earlier this session, `common/tls`
here), two were already correct (`route/rule/rule_set_local.go` - the comment records that it and its
remote sibling were fixed together after being caught on exactly this shape; and
`route/neighbor_resolver_{linux,darwin}.go`, whose every read takes an RLock). One is not covered:
`provider/local`'s reload delegates into the shared provider adapter's node update path, and auditing
that publish path is a different task from the watcher staging.

That distribution is the useful result: the driver model is the risk, and three of five users had a
version of the problem. The two fixes are the same fix shape - serialise the reload, then publish.

## 3. Not attacked, and why

| Candidate | Verdict | Reason |
| --- | --- | --- |
| `provider/local` reload (adapter publish) | `NOT_COVERED` | needs the provider adapter's update path, which is shared with the remote provider |
| ECH-key reload against handshakes | `NOT_COVERED` | the swap takes the same lock, but no test drives ECH reload concurrently |
| inline (`Certificate` in config) reload | `NOT_APPLICABLE` | no watcher is created |
| client-side `RootCAs` staleness | `SOURCE_ONLY` | window 2x2 C's C-P02, with an executable repro; a design decision |

## 4. Gates

`common/tls` is green with `-race` and under the general tag profile; no dependency pin moved
(`release/source-lock.json`, `release/pin-runs.json` untouched). The push gate result is recorded in
the session's final report.

## 5. New directions this window opens

1. **The driver family, not the module, is the unit of this class of defect.** Any future watcher,
   ticker, or callback fan-out should be checked for the three ingredients at once: more than one
   driver goroutine, a shared mutable staging field, and a reader on another goroutine.
2. **`setECHServerConfig` and the client-certificate branch** take the same lock but have no
   concurrent tests; the fix makes them safe, and a test that drives them beside handshakes would pin
   it the way this window pinned the certificate branch.
3. **`STDConfig()` returns the live pointer**, so a caller that keeps it across a reload sees the old
   config. That is the intended contract (the QUIC listeners capture it once), but it is the same
   shape as window 2x2 C's C-P02 on the client side and deserves one explicit note in the API doc.
