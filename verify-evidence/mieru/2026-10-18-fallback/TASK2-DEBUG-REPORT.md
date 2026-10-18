# Task 2 — adversarial debug report

Baseline `f6daabb9d5c`, feature `e1efe4cafdc`, Linux UDP-EOF fix `c603a02`,
final tree `16efd62322a`. Evidence root:
[`2026-10-18-fallback`](README.md). This is the direct audit against the
task-2 document's P0/P1 sections, not a restatement of the feature.

## P0/P1 result matrix

| Section | Result | Evidence / residual |
| --- | --- | --- |
| P0-A false success/failure, auth | PASS with one documented SDK limit | UDP blackhole, TCP accept-silent, refused, target SOCKS code, wrong password, DNS/cancel all tested. Wrong password on real mita: 10s TCP + 10s UDP timeout, error, no success. The SDK has no typed auth error, so one secondary attempt remains; no false success and no auth bypass. |
| P0-B dual Client/PacketConn ownership | PASS | Separate Client/Config/serverDialer/UDP registry; Close/InterfaceUpdated/start rollback/late socket tests; old secondary registry zero after swap. |
| P0-C old generation / Close | PASS | Selector generation + `acceptGeneratedConn`; controlled fake generation-1 completion after generation 2; connection closed and `errFallbackStale`. Close-in-flight <7s. |
| P0-D recovery storm / tail latency | PASS | 30s base cooldown, 2x cap 5min, single-flight probe; 50 concurrent new sessions; one probe per window; no global lock. |
| P0-E protocol semantics | PASS | `both`/`no_wait` rejected with fallback; disabled path unchanged; no Mux/auth/crypto/wire changes; only new sessions. |
| P1-F DNS/IPv4/IPv6/different endpoints | PASS (local scope) | IPv4 and IPv6 `::1` fallback, different ports; existing fork dialer/DNS bridge reused on both sides. Different-host FQDN is config/dialer-owned, not a separate live matrix. |
| P1-G Power/background | PASS | No timer/goroutine/background probe; recovery only on business dial; idle enabled vs disabled +0.25 MiB, no extra thread/socket. No Power gate is needed because there is no background work. Real mobile sleep remains NOT_TESTED. |
| P1-H Close/invalid/SDK | PASS | Closing scope, double Close, Start rollback, stale connection, active-session preservation, socket release, race detector. |

## Actual reproduced bugs

- **FB-001** SDK handshake error returns a live conn; old adapter leaked it.
- **FB-002** two silent handshakes left durable secondary preference.
- **FB-003** silent probe could re-probe every request.
- **FB-008** Linux UDP unreachable peer is a bare EOF, initially classified
  ambiguous; this failed the Linux race CI and was fixed by transport-aware EOF
  classification.
- **FB-004/005/006** lifecycle/rollback/port-inheritance hardening.

Full details, mutation commands and logs: [BUG-LEDGER.md](BUG-LEDGER.md);
error rows: [ERROR-CLASSIFICATION.md](ERROR-CLASSIFICATION.md); ownership:
[LIFECYCLE-OWNERSHIP.md](LIFECYCLE-OWNERSHIP.md).

## Reliability evidence

- Real SDK loopback: TCP refused→UDP, UDP blackhole→TCP, TCP accept-silent→UDP,
  UDP Associate both directions, IPv6 `::1`, target refusal, wrong password,
  active primary/secondary sessions, 50 concurrent, single-flight recovery,
  Close/InterfaceUpdated.
- Real mita 3.38.1: healthy, TCP→UDP, UDP→TCP, restricted target code 2,
  wrong-password negative; logs under `interop/mita/`.
- 3000 fallback short sessions: 3000/3000, 3.29s, P50 1.025ms; idle UDP endpoints
  77→0 after 4min; no TCP established leak.
- Full package 82.6s, race 150.3s locally; final Linux Verify and race job green
  at `16efd62322a`.

## Performance

- Disabled old/new: TCP download 1.00-1.07x, upload 1.00-1.02x, UDP echo
  0.92-0.95x (negative retained).
- Enabled healthy vs disabled: 1.024x TCP download, 1.021x upload, 1.053x UDP
  echo, identical first-byte.
- Idle +0.25 MiB; selector 26.6ns/0 allocs; TCP-refused failover 1.5ms,
  recovery 0.5ms; silent primary 10s is the official SDK wait.

## Environment-only residual

No active-session migration (contract), no SDK typed auth error, no iOS/Android
device, TUN, real Wi-Fi↔cellular or multi-hour soak. Real mita is no longer a
blocker; mbox/extended/Hiddify were not built on this Windows host. Those are
`NOT_TESTED`/`BLOCKED`, not approval items.
