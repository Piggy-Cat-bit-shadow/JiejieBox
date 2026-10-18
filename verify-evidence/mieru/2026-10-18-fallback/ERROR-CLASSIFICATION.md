# Error classification

The official `apis/client.DialContext` and `internal.PostDialHandshake` expose
plain `fmt` errors. The only stable protocol fact at this boundary is the final
SOCKS5 reply; there is no typed "underlay is dead" versus "credentials are
wrong" error. This table is therefore the complete decision contract, not a
claim that every cause can be identified.

| Observed event | Source / evidence | Class | Try secondary for this dial? | Change primary preference? | Notes |
| --- | --- | --- | --- | --- | --- |
| TCP connect refused / no route / host unreachable | `NewTCPUnderlay() failed: ... DialContext()` | transport | yes | cooldown 30 s | local refusal is fast; test TCP-refused 1.5 ms |
| TCP UDP underlay creation error | `NewUDPUnderlay() failed`, socket/bind error | transport | yes | cooldown | no socket is treated as healthy |
| Add session failed | `AddSession() failed` | transport | yes | cooldown | session not established |
| Request write failure | `failed to write socks5 connection request` | transport | yes | cooldown | no payload accepted by server |
| Handshake read timeout | `failed to read socks5 connection response ... timeout` | transport | yes | cooldown/backoff | includes UDP blackhole and TCP accept-but-silent; 10 s SDK timeout |
| Handshake read reset/aborted/forcibly closed/broken pipe | same read error text | transport | yes | cooldown | not a clean EOF |
| Valid SOCKS error reply (e.g. code 1/2) | `server returned socks5 error code N` | target | **no** | primary healthy; no cooldown | proxy/auth path and server processing worked; real mita restricted user code 2 verified 3 ms no-switch |
| Bare EOF before response | `failed to read ...: EOF` | ambiguous | **no** | no change | can be wrong credentials, server close or underlay FIN; conservative |
| DNS NXDOMAIN / address error | `*net.DNSError`, `*net.AddrError` | caller | no | no change | no duplicate DNS/dual attempt |
| Permission denied / bind policy | `os.ErrPermission` | caller | no | no change | local policy failure |
| Context canceled / deadline exceeded / client stopped | `ctx.Err`, `context.*`, `ErrClientIsNotRunning` | caller | no | no change | caller budget exhausted; no hidden retry |
| `client password is not set`, no endpoint, unsupported network | SDK configuration errors | caller | no | no change | config bug, not a network failure |
| Unknown error | anything else | ambiguous | no | no change | conservative; log the error, do not guess |
| Both sides fail silently | primary transport then secondary transport/ambiguous | both-failed | one secondary attempt already made | reset to primary, no durable secondary | wrong-password case; next business dial tries primary again |

## State rules

- Transport failure on the preferred primary: set `preferred=secondary`,
  `primaryUntil=now+cooldown`, increment primary failure counter.
- Probe failure (transport): double cooldown, cap 5 min.
- Probe failure (ambiguous/bare EOF): double cooldown too, so a silent probe
  cannot turn every subsequent request into another probe.
- Success or target reply on primary: primary preferred, cooldown cleared.
- Secondary success: count it, leave primary cooldown unchanged.
- Both sides silent: primary preferred again, cooldown cleared; this is a
  fail-closed decision for credential/config mistakes, not a success.
- A generation mismatch or closed selector ignores every report.

## Explicit limitation

A wrong password on the primary and a correct account on a *different* backup
server can still produce a successful backup connection. Both servers perform
their own Mieru authentication; no authentication is skipped or weakened. The
client cannot distinguish this from a dead primary because the SDK reports both
as a silent handshake. This is recorded as a limitation, not as "safe automatic
recovery".
