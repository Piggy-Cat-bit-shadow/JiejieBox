# Pairwise A — resource, memory, connection and lifecycle (2026-11-19)

Window A of the JiejieBox cross-system pairwise debug. The subject is not "does each module work" but
"when two correct modules run together, who owns the object".

| File | What it holds |
| --- | --- |
| [`PAIRWISE_A_MATRIX.md`](PAIRWISE_A_MATRIX.md) | owner map, the A01–A11 pair table (shared boundary, attacks executed, conclusion, evidence), and the capability table the conclusions rest on |
| [`PAIRWISE_A_FINDINGS.md`](PAIRWISE_A_FINDINGS.md) | the four fixed defects (F-1…F-4) with trigger, why the previous judgement missed them, fix and regression; then the seven confirmed-but-unfixed findings (R-1…R-7) with the test that would settle each |
| [`PAIRWISE_A_EFFECTIVENESS.md`](PAIRWISE_A_EFFECTIVENESS.md) | the mechanism chain per claim — loaded → real entry → decision → action → effect → reclaim — and the four mutation controls |
| [`PAIRWISE_A_VERIFICATION.md`](PAIRWISE_A_VERIFICATION.md) | environment, commands, per-package results, mutation controls, the pre-existing red tests, and the coverage limits that are *not* claimed |
| `raw/` | raw command output cited by the verification page |

## Result in one table

| | Pair | Status |
| --- | --- | --- |
| A01 | ResourceGuard × OOMKiller | 1 fixed (recovery edge never woke the queue); authority and stale-apply ruled out; a real `Critical` now provably stops a real TLS creation before the dial, with a mutation control |
| A02 | Connection pool × lifecycle | 2 fixed (naive's shed hook terminated live tunnels; hy2/tuic installed a client after teardown); trusttunnel reservation, anytls reuse boundary and the mieru releaser gap recorded |
| A03 | Admission × protocol reuse | no defect: AnyTLS charges only creation, TLS seam covers the TLS outbounds, naive charges the native socket, MASQUE is one endpoint tunnel |
| A04 | iOS NE × H3 speed test × TrustTunnel | `未覆盖` — no device; no number claimed |
| A05 | TUN UDP NAT × guard | 4 confirmed by reading (no admission on the NAT path; eviction does not cancel the dial it launched; download-only flows expire; the 8 MiB NAT budget × instances) — needs the `sing-tun` fork, so not fixed here |
| A06 | Queue × priority × cancel | 1 fixed (same change as A01), shrink direction pinned |
| A07 | OOM reclaim × trim × generation | no harmful duplicate: the OOMKiller serialises its passes, and the guard's shed hooks are idle-only for the two owners that keep one |
| A08/A09/A10 | diagnostics / body lifetime / runtime-GC | A08 and A10 `未覆盖`; A09 partially: the two pool-lifecycle tests that were red at HEAD are fixed, and the package is now executed by a push job (F-4) |
| A11 | free extensions | the naive contract violation is exactly an A11 finding; the third (build) guard is recorded so a later round does not "unify" it |

## Reproduce

```sh
go test ./common/resourceguard ./protocol/hysteria2 ./protocol/tuic ./service/oomkiller
go test ./transport/v2rayxhttp
go test -tags "with_naive_outbound,with_purego" ./protocol/naive
go test -race ./common/resourceguard ./protocol/hysteria2 ./protocol/tuic ./service/oomkiller
go test -race -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS)" ./transport/v2rayxhttp
```

CI mounts: `.github/workflows/resource-guard.yml` (race leg + path filters extended to
`protocol/hysteria2`, `protocol/tuic` and `service/oomkiller`, ~2.5 s per QUIC suite) and
`.github/workflows/verify.yml` (push-gate race step extended to `transport/v2rayxhttp`, measured
~23 s under `-race`).
