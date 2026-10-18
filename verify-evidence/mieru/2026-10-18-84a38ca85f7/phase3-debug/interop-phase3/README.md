# Phase 3 interop rerun (after the fixes)

Same host, same official SDK 3.38.1. Both directions returned `PASS tcp` / `PASS udp`.

| Direction | Files | Result |
| --- | --- | --- |
| official `mieru.exe` 3.38.1 → this inbound (`_mieru_phase3.exe`) | `client-official.log`, `server-phase3.log` | TCP PASS, UDP ASSOCIATE PASS |
| this outbound (`_mieru_phase3.exe`) → official `apis/server` harness | `client-phase3.log`, `server-official.log` | TCP PASS, UDP ASSOCIATE PASS |

Commands are in `../reproduction.md`. The official server is the SDK entry point, not the
`mita` binary (no Windows build); this remains a documented limitation, not a PASS for
`mita`. The logs are the filtered structured lines; PowerShell wrappers and ANSI escapes
were removed, no content was edited.
