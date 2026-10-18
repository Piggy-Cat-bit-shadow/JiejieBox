# Real mita 3.38.1 fallback interop

Unmodified `git@v3.38.1` `cmd/mita`, built for Windows:

```powershell
cd C:\Users\Jie\go\pkg\mod\github.com\enfein\mieru\v3@v3.38.1
go build -o C:\Deepseek\_mieru_refs\mita.exe ./cmd/mita
New-Item -ItemType Directory -Force -Path C:\var\run\mita
$env:MITA_CONFIG_JSON_FILE = '...\server.json'
$env:MITA_INSECURE_UDS = '1'
mita.exe run
```

`MITA_INSECURE_UDS=1` skips only the Linux chown of its Unix RPC socket; no
source/protocol changes. Server configs `server.json` (user `baozi`, loopback
allowed) and `server-restricted.json` (user `restricted`, default loopback
denied). Target configs use the same Mieru credentials and `fallback`.

| Scenario | Result | Evidence |
| --- | --- | --- |
| Primary TCP healthy, fallback UDP configured | TCP + UDP associate PASS; no fallback log | `healthy.log` |
| Primary TCP port has no listener, fallback UDP reaches mita | PASS; log `primary tcp transport failed, trying fallback udp` | `tcp-fail.log` |
| Primary UDP blackhole, fallback TCP reaches mita | PASS after 10.06 s SDK handshake timeout; log `primary udp ... fallback tcp` | `udp-fail.log` |
| Restricted user sends a loopback target, mita replies SOCKS code 2 | TCP fails in 3 ms; **no** `transport failed/trying fallback`; UDP target also errors | `target-restricted.log` |

The restricted case is the real target-refusal check: a valid SOCKS reply is
target-class and must not trip the primary transport health. It did not.
The 10 s case is the official SDK's standard handshake timeout, not an extra
selector timer.
