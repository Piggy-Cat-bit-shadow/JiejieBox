# Reproduction

## Build and tests

```powershell
$env:PATH = 'C:\src\_toolchain\goroot\bin;' + $env:PATH
Set-Location C:\Deepseek\integration-release-next
$tags = (Get-Content release\DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()
go build -tags $tags -o C:\Deepseek\_mieru_fallback.exe ./cmd/sing-box
go test ./protocol/mieru ./option ./include -count=1
$env:PATH = 'C:\src\_toolchain\goroot\bin;C:\msys64\mingw64\bin;' + $env:PATH
$env:CGO_ENABLED = '1'
go test -race ./protocol/mieru -count=1
go vet ./protocol/mieru ./option
go build -tags $tags ./...
```

The fallback suite alone:

```powershell
go test ./protocol/mieru -run 'TestMieruFallback|TestFallback|TestResolveFallback|TestClassifyDialError|TestTransportRuntimeCloses|TestOutboundStartRollsBack|TestAcceptGenerated|TestMieruFallbackJSON' -count=1 -v
```

Saved verbose output is `tests/fallback-tests.txt` (59.7 s); the full package was
147 s under host load and race 85.1 s (`tests/race.txt`). The 10/20 s cases are
the official SDK's fixed handshake timeout on silent TCP/UDP paths, not test
sleeps; the `fallbackSelector` clock is fake in the state tests.

## Feature smoke

Real mita (unmodified v3.38.1, foreground, `MITA_INSECURE_UDS=1` for its Unix
socket chown):

```powershell
New-Item -ItemType Directory -Force -Path C:\var\run\mita
$env:MITA_CONFIG_JSON_FILE = '...\server.json'
$env:MITA_INSECURE_UDS = '1'
mita.exe run
```

Then a target config with `fallback.enabled` and `tester.exe` through the mixed
inbound. Configs/logs for healthy, primary TCP refused, primary UDP blackhole,
and real mita restricted-user target refusal are in `interop/mita/`.

## Mutation reproductions

`repro/bug1.txt`, `bug2.txt`, `bug3.txt` were produced by temporarily removing
the corresponding fix, running the named test, and restoring the file:

- bug1: remove `conn.Close()` in `transportRuntime.dial`; `closed` stays 0.
- bug2: remove `selector.reportBothFailed`; wrong password leaves
  `preferred=secondary`.
- bug3: remove ambiguous-probe backoff; `PrimaryUntil` remains in the past and
  the next `choose` becomes a probe again.

Production code was restored after each run; the final suite passes.

## Performance

- `performance/disabled-old|new` and `-2` are two 4-round pairs; combined in
  `disabled-combined.csv`.
- `performance/enabled-vs-disabled` is 4 rounds against real mita, same target
  binary with `fallback` on/off.
- `performance/idle` is the no-dial RSS/handle/thread snapshot.
- `performance/selector-benchmark.txt`: `go test -run '^$' -bench BenchmarkFallbackSelectorChooseReport -benchmem -count=3`.
