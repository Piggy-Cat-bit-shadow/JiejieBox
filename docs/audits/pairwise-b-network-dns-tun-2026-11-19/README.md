# Pairwise B — network, DNS, TUN, address families, protocol state (2026-11-19)

Window B of the JiejieBox cross-system pairwise debug, continuing on `testing` from window A's head
(`d3b0e8190`). The subject is the data-plane/control-plane boundary: how DNS affects routing, how a
fake-IP result reaches a TUN flow, which physical connections a network change must invalidate, and
whether a declared fallback really runs.

| File | What it holds |
| --- | --- |
| [`PAIRWISE_B_INTERACTION_MAP.md`](PAIRWISE_B_INTERACTION_MAP.md) | ownership table, the TUN data path and the network-change control path with file:line, the platform/mode/build-tag entry table, and the junction list |
| [`PAIRWISE_B_MATRIX.md`](PAIRWISE_B_MATRIX.md) | the 12 pairs plus the free extension: what was executed, what existing suites cover, conclusion and evidence |
| [`PAIRWISE_B_FINDINGS.md`](PAIRWISE_B_FINDINGS.md) | three fixed defects (B-1…B-3) with trigger, why the previous judgement missed them, fix and regression; then ten recorded findings (R-B1…R-B10) with the exact sequence and the test that would settle each |
| [`PAIRWISE_B_EFFECTIVENESS.md`](PAIRWISE_B_EFFECTIVENESS.md) | the six-layer chain for the fixed mechanisms, the mutation controls, and which recorded findings have a detector at all |
| [`PAIRWISE_B_VERIFICATION.md`](PAIRWISE_B_VERIFICATION.md) | commands, per-package results, mutation controls, the `e2e` census, and the limits that are not claimed |

## Result in one table

| | Pair | Status |
| --- | --- | --- |
| B01 | IPv4/IPv6 racing × cancel | `已排除` — the existing dual-stack suite (45 files) was executed here; no new defect |
| B02 | DNS / Fake-IP × TUN | **3 fixed**: B-3 (a degenerate range issued addresses outside itself), B-4 (a placeholder from a retired generation was bypassed to the platform before the router could refuse it), B-5 (with no fakeip server configured, the durable retirement record had no reader); 1 recorded: a reload without `store_fakeip` can remap a client-held address to another domain |
| B03 | auto_redirect × TUN/nftables | `无法观测` — Linux/nftables, no privilege here; nothing claimed |
| B04 | H3/QUIC pool × generation | **B-2 fixed** (a network change made every later vless/vmess/trojan HTTP stream pay a new handshake); 2 recorded: the generation gate's listener half exists in 1 of 7 owners, and check-then-acknowledge is not atomic |
| B05 | Fallback × error class / ALPN | **B-1 fixed** (the Go 1.27 move silently changed TrustTunnel's HTTP/2 preface); 2 recorded: the H3→H2→HTTP/1.1 demotion is silent, and naive's fallback window never arms when its own probe budget expires first |
| B06 | DNS cache × family racing | `未覆盖` as an attack |
| B07 | Reload × live state | `未覆盖` as an attack; 1 recorded while reading B02 (stop-then-build with no preservation) |
| B08 | Rules × outbound switch | `未覆盖` |
| B09 | H2/H3 body × cancel | partially: two new pins on the TrustTunnel session (no PING while idle, one connection) |
| B10 | MASQUE × NAT64/PREF64 | suites executed; the interleavings not attacked |
| B11 | UDP session × TUN route change | deferred to window A's A05 by design |
| B12 | the toolchain as a wire participant | **new pair found here** — B-1 is its first member |

## Reproduce

```sh
go test ./dns/... ./common/dialer ./transport/... ./protocol/masque ./protocol/vless ./protocol/tun
go test -run 'TestHTTP2Preface|TestAnIdleSessionSendsNoPing|TestTheSessionPrefersOneConnection' ./transport/trusttunnel
go test ./transport/v2rayhttp ./transport/v2raygrpclite ./protocol/vless ./protocol/trojan
go test ./dns/transport/fakeip ./protocol/tun
go test ./route -run 'FakeIP|ARetiredPlaceholder|NoFakeIPHistory'
go test ./e2e -count=1          # 58 tests; executed by no workflow, see the verification page §5
```

CI mount: `verify.yml` gained a push-gate step that runs the three TrustTunnel wire pins beside the
existing "toolchain directive" step (~1.3 s, guarded by `require-executed-coverage.sh`), because the
thing it guards against is a repository-wide toolchain default rather than a path.
