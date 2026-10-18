# Real mita 3.38.1 on Windows

This supersedes the phase-2 statement "mita has no Windows build". The official source
does build for Windows; the earlier assumption was wrong. The binary is unmodified:

```powershell
cd C:\Users\Jie\go\pkg\mod\github.com\enfein\mieru\v3@v3.38.1
go build -o C:\Deepseek\_mieru_refs\mita.exe ./cmd/mita
```

| Fact | Value |
| --- | --- |
| `mita version` | `3.38.1` |
| source | `github.com/enfein/mieru/v3 v3.38.1` (unmodified) |
| SHA-256 | `7A878036476D1F4D0723FA7544F6EFEA9797C905E358768E721B37687D9964B2` |
| server config | `server.json` in this directory, SHA-256 `10901B8373238E2DB61B159AF24E66B1E53CAAC10172BF431D13B087E8553757` |
| user | `baozi` / `manlianpenfen`, `allowPrivateIP`, `allowLoopbackIP` |
| listener | `127.0.0.1:18666`, TCP + UDP, MTU default 1400 |

`mita run` is the foreground server command. On Windows it needs two environment details:

1. The RPC socket is hard-coded to `/var/run/mita/mita.sock`; create `C:\var\run\mita` so the
   AF_UNIX path resolves on the current drive.
2. `updateServerUDSPermission` tries to `chown` the socket to the Linux user `mita`.
   `MITA_INSECURE_UDS=1` skips only that permission step (the official CLI explicitly
   supports the variable). No source is patched.

```powershell
New-Item -ItemType Directory -Force -Path C:\var\run\mita
$env:MITA_CONFIG_JSON_FILE = '...\server.json'
$env:MITA_INSECURE_UDS = '1'
mita.exe run
```

Server log: `mita-run3.log`; both `tcp 127.0.0.1:18666` and `udp 127.0.0.1:18666` are bound.

## Interop

| Direction | Result | Log |
| --- | --- | --- |
| official `mieru.exe` 3.38.1 → real mita | TCP PASS, UDP PASS | `mita-client.log` |
| this outbound (`_mieru_phase3.exe`) → real mita | TCP PASS, UDP PASS | `mita-our-client.log` |

## Four-round client matrix against real mita

Both clients used the same real mita server, same local SOCKS5 entry shape, 4 TCP
connections × 64 MiB, 2000 UDP datagrams, 1000 latency samples, 4 rounds. Raw cells are in
`raw/`; `summary.csv` and `comparison.csv` are the generated projections.

| Scenario | Target / official throughput | note |
| --- | ---: | --- |
| tcp-low-default TCP download | **1.150** | +15.0% |
| tcp-low-default TCP upload | 1.020 | +2.0% |
| tcp-high-nowait TCP download | **1.144** | +14.4% |
| tcp-high-nowait TCP upload | 0.963 | -3.7% |
| udp-low-default TCP download | 1.032 | +3.2% |
| udp-low-default TCP upload | 0.739 | **-26.1%** |
| UDP echo (tcp-low / tcp-high / udp-low) | 1.050 / 1.091 / **1.455** | P50 1.024 / 1.026 / 0.611 |
| first-byte P50 (tcp-low / tcp-high / udp-low) | 1.969 / 1.965 / 1.329 | **target slower**; P95 is 1.05-1.09 |
| TCP latency P95 | 0.99-1.01 | unchanged |

The two negative results are real and are not hidden:

- **First-byte P50 against mita is ~1.0 ms for the target vs ~0.52 ms for the official
  client**, while P95 is essentially equal. The target spends about half a millisecond more
  on the first request through mita, probably in the local sing-box/mixed path or the first
  Mieru session; it is not visible against the SDK harness. This is a new, concrete item for
  the next performance round, not a claim of universal speedup.
- **UDP-transport TCP upload is 0.739x** in this matrix (official median 118.0 MiB/s, target
  87.2). The 4-round official range is 81-141, so the median is noisy, but the direction is
  the same as the phase-2 upload gap. It stays visible.

This matrix is a product comparison against the real server. It does not replace the
phase-2 SDK-harness matrix; it corrects the "mita unavailable" limitation.
