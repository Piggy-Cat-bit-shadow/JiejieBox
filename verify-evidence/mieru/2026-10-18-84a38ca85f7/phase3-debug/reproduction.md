# Phase 3 reproduction

Host: Windows 10 LTSC 19044, Go 1.26.8 windows/amd64, AMD Ryzen 7 4800H.
All paths below are relative to `C:\Deepseek\integration-release-next`.

## Build

```powershell
$env:PATH = 'C:\src\_toolchain\goroot\bin;' + $env:PATH
$tags = (Get-Content release\DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()
go build -tags $tags -o C:\Deepseek\_mieru_phase3.exe ./cmd/sing-box
```

## Tests

```powershell
go test ./protocol/mieru ./option ./include -count=1
$env:PATH = 'C:\src\_toolchain\goroot\bin;C:\msys64\mingw64\bin;' + $env:PATH
$env:CGO_ENABLED = '1'
go test -race ./protocol/mieru -count=1
go vet ./protocol/mieru
go build -tags $tags ./...
go test -tags $tags -timeout 20m . ./common/listener
```

The `-race` suite takes about 90 s because the wrong-password/no-wait test waits for the
SDK's 10 s handshake timeout; this is the SDK's `serverRespTimeout`, not a hang.

## Performance A/B

Old binary: `C:\Deepseek\_mieru_bench_after.exe` (phase 2, before these fixes).
New binary: `C:\Deepseek\_mieru_phase3.exe` (this tree).

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\mieru-bench\run.ps1 `
  -TargetBinary C:\Deepseek\_mieru_bench_after.exe `
  -OutDir C:\Deepseek\_p3_old4 `
  -Rounds 4 -Connections 4 -Bytes 67108864 -Packets 2000 -Requests 500 -SkipOfficial

powershell -ExecutionPolicy Bypass -File .\scripts\mieru-bench\run.ps1 `
  -TargetBinary C:\Deepseek\_mieru_phase3.exe `
  -OutDir C:\Deepseek\_p3_new4 `
  -Rounds 4 -Connections 4 -Bytes 67108864 -Packets 2000 -Requests 500 -SkipOfficial
```

The summaries, manifests and raw cells were copied to `performance/old` and
`performance/new`; `performance/comparison.csv` joins them. Both runs use the same official
SDK server harness and local echo targets.

## Interop rerun

```powershell
# official mieru.exe -> this inbound
C:\Deepseek\_mieru_phase3.exe run -c C:\Deepseek\_mieru_interop\server-our-inbound.json
$env:MIERU_CONFIG_JSON_FILE = C:\Deepseek\_mieru_interop\client-official.json
C:\Deepseek\_mieru_interop\bin\mieru.exe run
C:\Deepseek\_mieru_interop\tester.exe -socks 127.0.0.1:11082

# this outbound -> official apis/server harness
C:\Deepseek\_mieru_interop\official-server.exe -port 18665
C:\Deepseek\_mieru_phase3.exe run -c C:\Deepseek\_mieru_interop\client-our-outbound.json
C:\Deepseek\_mieru_interop\tester.exe -socks 127.0.0.1:11083
```

## Reproducing the closed/leaked port failures

The tests themselves are the smallest reproducer:

```powershell
go test ./protocol/mieru -run 'TestMieruInboundCloseReleasesListener|TestMieruInboundPartialStartClosesTheUDPListener|TestMieruInboundCanRebindAfterClose' -v
go test -race ./protocol/mieru -run TestUOTPacketConnConcurrentReads -v
go test ./protocol/mieru -run 'TestMieruOutboundUDPCloseReleasesUnderlaySockets|TestMieruInterfaceUpdateReopensUDPUnderlay' -v
```

Before `3f9303286e0` the first group failed on the UDP bind, the second group reported the
race/interleaved frames, and the third group could not prove the UDP socket was closed.
