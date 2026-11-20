# Triple-wise C — autonomous exploration: matrix

Window C of the three-system round: no assigned numbering, the combinations were derived from the
owner/clock/entry map. Base: `testing` @ `6acbd4a7f` (the state triple-wise B left). Commits added:
`89fef8ca0` plus these reports.

## 1. How the combination was found

The map first looked for a **shared owner with more than one driver**, because that is the shape the
previous rounds' real defects had (two callbacks, one mutable state, no arbitration). `fswatch` is
exactly such a driver: it fires one callback per changed path, each on its own goroutine, and the
debounce is per path, not per watcher. That property produced this session's window-C defect in the
certificate store (a reload publishing an older snapshot last), so the same **driver family** was
walked to its other users:

| User | What the callback mutates | Verdict |
| --- | --- | --- |
| `common/certificate/store.go` | the published `*x509.CertPool` | **FIXED earlier this session** (`6eb1f1a8b`, serialised reload + regression) |
| `common/tls/std_server.go` | the served `*tls.Config`, staged through `certificate`/`key` | **FIXED in this window** (`89fef8ca0`) |
| `route/rule/rule_set_local.go` | the published rule slice | `NO_DEFECT` — already `access`-guarded, and the comment records that the remote sibling was caught on the same shape and both were fixed together |
| `route/neighbor_resolver_{linux,darwin}.go` | the address maps | `NO_DEFECT` — every read is `RLock`, the reload publishes new maps under `Lock` (source read; the platforms are not observable here) |
| `provider/local/lcoal.go` | the provider's node set, through the provider adapter | `NOT_COVERED` — the reload delegates to the adapter's shared update path, which the provider-identity work covers for identity but not for this staging pattern |

## 2. The finding: the server TLS config's reload was two data races

| Column | Value |
| --- | --- |
| Systems | ① `STDServerConfig` (owner of the served `*tls.Config`) ② the `fswatch` callback goroutines (driver: one per changed path) ③ the handshake entry (`Server(conn)`, `STDConfig()`, `Clone()`) reading the config on every accepted connection |
| Shared object | `c.config`, plus the staging fields `c.certificate` / `c.key` |
| Failure timeline | a renewal writes the certificate file and the key file → fswatch fires callback A (cert) and callback B (key) on separate goroutines → both write `c.certificate`/`c.key` unsynchronised, and each rebuilds `tls.X509KeyPair(c.certificate, c.key)` **reading the other's field** → while, per accepted connection, `Server(conn)` reads `c.config` with no lock against the swap under `c.access` |
| Why the third system is not optional | without the handshake readers the staging race is confined to the two callbacks and would rarely be observed; without the second callback the staging has a single writer. The listener's per-connection read is what turns a two-goroutine window into a race observable in normal operation (every renewal on a live listener) |
| Executed | `go test -race ./common/tls/` with two new tests: **two `WARNING: DATA RACE` reports** on the staging fields, and the readers-versus-swap shape. Mutation (removing the new reload lock) reproduces the staging race deterministically; with the fix, `-race` is clean |
| Fix | a `reloadAccess` mutex across the whole reload (the pair is rebuilt from one callback's view of both files) and `RLock` around every `c.config` read (`STDConfig`, `Client`, `Server`, `Clone`) |
| Behaviour kept | a certificate-first callback, with the old key still in memory, still reports `private key does not match public key` and **installs nothing** — asserted, not assumed; the pair is replaced only when both halves match |
| Commit | `89fef8ca0` |

## 3. Also verified, not re-counted

| Item | Verdict | Note |
| --- | --- | --- |
| the certificate store's reload | `FIXED` (`6eb1f1a8b`, earlier window) | same driver family, already closed; listed for the family map, not as new work |
| local rule-set reload | `NO_DEFECT` | locked, with the prior-fix comment |
| neighbour resolver reload | `NO_DEFECT` | locked; platform not observable here |
| client-side certificate consumers (`std_client`/`utls_client`) | `SOURCE_ONLY` | the store's reload never reaches a config that captured `RootCAs`; documented in window 2x2 C (`C-P02`) with an executable repro, still a design decision |

## 4. Not attacked

| Candidate | Verdict | Why |
| --- | --- | --- |
| `provider/local` reload | `NOT_COVERED` | delegates into the shared provider adapter; a fair audit needs the adapter's publish path, not the watcher |
| concurrent ECH-key reload against handshakes | `NOT_COVERED` | `setECHServerConfig` swaps under the same lock the fix now guards, but no test drives ECH reload concurrently |
| `Certificate` (inline) reload | `NOT_APPLICABLE` | no watcher is created for inline certificates |

## 5. Control matrix (reduced)

The claim is "the third system changes the outcome", so the arms below are the three necessary
ingredients, executed with the same test and the same server config:

| Arm | Read concurrency | Second callback | Reload serialised | Result |
| --- | --- | --- | --- | --- |
| baseline (reload alone, no readers) | no | no | n/a | passes either way — the race needs the other two axes |
| A (readers, one callback) | yes | no | yes | passes; no staging overlap |
| B (two callbacks, no readers) | no | yes | **no** | `WARNING: DATA RACE` (mutation) |
| C (readers + two callbacks) | yes | yes | **no** | `WARNING: DATA RACE` (the reported run) |
| fixed (readers + two callbacks) | yes | yes | yes | clean under `-race`, pair converges on the files |

Row A is why the defect did not appear in any existing suite: with one callback the staging is a
single writer, and no test drove two.
