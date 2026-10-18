# Environment

| Fact | Value |
| --- | --- |
| Date | 2026-10-18 |
| Tree | `C:\Deepseek\integration-release-next`, branch `testing` |
| HEAD | `84a38ca85f7a46b3910147148dd349da20bb5c17` |
| SDK | `github.com/enfein/mieru/v3 v3.38.1` |
| Go | `go1.26.8 windows/amd64` |
| Build tags | `with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_usbip,with_openvpn,with_openconnect,with_xhttp,badlinkname` (release/DEFAULT_BUILD_TAGS_OTHERS) |
| CGO | disabled for the target binary |
| OS | Windows 10 Enterprise LTSC, 10.0.19044 |
| CPU | AMD Ryzen 7 4800H with Radeon Graphics |
| Network | loopback only; every peer is a local process |
| Shell | Windows PowerShell 5.1 |
| WSL | not installed (`wsl -l -v` prints the install help) |
| Docker | not installed (`docker` is not a recognized command) |
| Official client | `mieru.exe` 3.38.1, SHA-256 `37ADD82895EB6B5D5289F82173D334A5AA697567E21937CC104E23DB0C380C31` |
| Official server | `scripts/mieru-bench/server` built against the same v3.38.1; mita has no Windows build |
| Target binary | SHA-256 `1150F0CDA39BC3C1EBAB4248D10CBD49998A0B616870B85C81025C4B87834917` |
| REF-E | Mihomo v1.19.32 Windows amd64, SHA-256 `04F8D7FC2B314771E1ECBF15951D59F0E1CB7914503C88CFB6972FF6A824E314`, Mieru SDK v3.38.0 |

## Limits that affect the numbers

- **No CPU pinning.** The two client processes and the server share the same 8-core/16-thread host.
  Throughput cells are medians of four ABBA rounds, not tail-clean single runs.
- **Process CPU resolution** is the Windows scheduler tick (~15.6 ms). CPU/GiB is only reported for
  runs that move at least 256 MiB; latency/first-byte CPU columns are meaningless and are ignored.
- **RSS is sampled** at idle and after each workload; it is not a peak-RSS counter
  and there is no after-close sample.
- **Product boundaries differ**: the target process contains the sing-box core, a mixed inbound and
  the router; the official process is a dedicated client daemon. RSS and startup include those
  boundaries. Only the UoT framing microbench and the shared-server comparison isolate the adapter.
- **Server is the SDK entry point**, not the mita binary. It is the same process for both client
  variants, so it cancels in the client comparison, but it is not a mita performance measurement.
- No Wi-Fi/cellular hand-off, sleep/wake, iOS/Android or long-soak run was possible here.

## Provenance

`git -C C:\Deepseek\integration-release-next rev-parse HEAD` is recorded in `manifest.json` and
`variants.json`; binary hashes are in both files. The A/B baseline binary was built from the
`b2d9ff5` tree before the `dialed` guard; the after binary is the `84a38ca` working tree. Both hashes
are recorded in the respective `ab-interface-update/*/manifest.json`.
