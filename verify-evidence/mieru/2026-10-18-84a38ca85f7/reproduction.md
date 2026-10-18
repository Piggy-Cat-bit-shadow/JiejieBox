# Reproduction

Host: Windows 10 LTSC 19044, AMD Ryzen 7 4800H, Go 1.26.8 windows/amd64, loopback only.
PowerShell 5.1. No WSL, no Docker.

## Build the target

```powershell
$repo = "C:\Deepseek\integration-release-next"
$tags = (Get-Content "$repo\release\DEFAULT_BUILD_TAGS_OTHERS" -Raw).Trim()
Push-Location $repo
go build -tags $tags -o C:\Deepseek\_mieru_bench_after.exe ./cmd/sing-box
Pop-Location
```

`_mieru_bench_after.exe` SHA-256:
`1150F0CDA39BC3C1EBAB4248D10CBD49998A0B616870B85C81025C4B87834917`.

## Official client

`C:\Deepseek\_mieru_interop\bin\mieru.exe`, version `3.38.1`, SHA-256:
`37ADD82895EB6B5D5289F82173D334A5AA697567E21937CC104E23DB0C380C31`.

## Full matrix

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\mieru-bench\run.ps1 `
  -TargetBinary C:\Deepseek\_mieru_bench_after.exe `
  -OfficialMieru C:\Deepseek\_mieru_interop\bin\mieru.exe `
  -OutDir .\verify-evidence\mieru\2026-10-18-84a38ca85f7 `
  -Rounds 4 -Connections 4 -Bytes 67108864 -Packets 5000 -Requests 1000
```

The script:
1. builds `scripts/mieru-bench/client` and `scripts/mieru-bench/server` into `%TEMP%\mieru-bench-work`;
2. starts the official-SDK server harness on 127.0.0.1:18671;
3. for each scenario and round starts the variant, waits for its SOCKS port, warms it with 1 MiB,
   runs `tcp-download`, `tcp-upload`, `tcp-latency`, `udp-echo`, `first-byte`, then stops it;
4. writes one JSON per cell (bench result + process CPU/RSS + startup), the client/server logs,
   `manifest.json`, and `summary.csv`.

Order per round is `target,official` on odd rounds and `official,target` on even rounds. Both variants
use the same server process for the whole run; scenarios are:

| Scenario | transport | mux | handshake |
| --- | --- | --- | --- |
| tcp-low-default | tcp | low | default |
| udp-low-default | udp | low | default |
| tcp-high-nowait | tcp | high | no_wait |

## InterfaceUpdated A/B

```powershell
# baseline binary: the b2d9ff5 tree, no dialed guard
powershell -ExecutionPolicy Bypass -File .\scripts\mieru-bench\run.ps1 `
  -TargetBinary C:\Deepseek\_mieru_interop\sing-box-mieru.exe `
  -OutDir C:\Deepseek\_mieru_ab_baseline `
  -Rounds 4 -Connections 1 -Bytes 1048576 -Packets 100 -Requests 100 -SkipOfficial

# after binary: the 84a38ca working tree
powershell -ExecutionPolicy Bypass -File .\scripts\mieru-bench\run.ps1 `
  -TargetBinary C:\Deepseek\_mieru_bench_after.exe `
  -OutDir C:\Deepseek\_mieru_ab_after `
  -Rounds 4 -Connections 1 -Bytes 1048576 -Packets 100 -Requests 100 -SkipOfficial
```

The two summaries and raw cells are copied to `ab-interface-update/{baseline,after}`; the medians are
joined in `ab-interface-update/comparison.csv`.

## CPU profile

Add the debug listener to a target config:

```json
"experimental": {"debug": {"listen": "127.0.0.1:18080"}}
```

Then, with the official server harness on 18671 and this target on 11092:

```powershell
# 8 GiB download: 4 connections x 2 GiB
& $client -socks 127.0.0.1:11092 -mode tcp-download -connections 4 -bytes 2147483648
# while it runs:
Invoke-WebRequest 'http://127.0.0.1:18080/debug/pprof/profile?seconds=6' -OutFile cpu.pprof
```

`profiles/cpu-tcp-download.pprof` and the two `go tool pprof -top` projections are committed.
The run itself reported 398.8 MiB/s (`profiles/bench.out`).

## What is deliberately not reproduced here

- A real Linux `mita` server, mbox, extended, Hiddify and Mihomo: see `decisions.md` E6 and
  `variants.json.blocked_references`. This host cannot run them; no number is invented for them.
- Wi-Fi/cellular path changes, device sleep/wake, iOS/Android devices, long soak and loss matrix.
  Those require other hardware; the startup A/B and the profile are the local evidence for this round.
