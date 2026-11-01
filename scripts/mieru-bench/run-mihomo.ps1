param(
    [string]$RepoRoot = "",
    [string]$WorkDir = "",
    [string]$OutDir = "",
    [string]$MihomoExe = "",
    [int]$Rounds = 4,
    [int]$Connections = 4,
    [long]$Bytes = 67108864,
    [int]$Packets = 5000,
    [int]$Size = 1400,
    [int]$Requests = 1000
)

# REF-E runner: Mihomo v1.19.32 (Mieru SDK v3.38.0), same official-SDK server and
# same bench client as the main matrix. Mihomo is started once per round because
# its Mieru client is lazy-started on the first dial; the warmup forces that start
# outside the measured cell.

$ErrorActionPreference = "Stop"
$Utf8NoBom = New-Object System.Text.UTF8Encoding($false)
if (-not $RepoRoot) { $RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path }
if (-not $WorkDir) { $WorkDir = Join-Path $env:TEMP "mieru-bench-work" }
if (-not $OutDir) { $OutDir = Join-Path $RepoRoot "verify-evidence\mieru\2026-10-18-84a38ca85f7\references\mihomo" }
if (-not $MihomoExe) { $MihomoExe = $env:MIHOMO_EXE }
if (-not $MihomoExe) { throw "MihomoExe is required: pass -MihomoExe or set MIHOMO_EXE" }
$RawDir = Join-Path $OutDir "raw"
New-Item -ItemType Directory -Force -Path $RawDir | Out-Null
$ClientExe = Join-Path $WorkDir "mieru-bench-client.exe"
$ServerExe = Join-Path $WorkDir "mieru-bench-server.exe"
$ServerPort = 18671
$SocksPort = 11093
$ServerLog = Join-Path $RawDir "server.log"
$ServerErr = Join-Path $RawDir "server.err.log"
$records = New-Object System.Collections.Generic.List[object]
$serverProcess = $null
$MihomoData = Join-Path $WorkDir "mihomo-data"
New-Item -ItemType Directory -Force -Path $MihomoData | Out-Null

function Median($values) {
    $sorted = @($values | Sort-Object)
    if ($sorted.Count -eq 0) { return 0 }
    $mid = [int][math]::Floor($sorted.Count / 2)
    if ($sorted.Count % 2 -eq 1) { return [double]$sorted[$mid] }
    return ([double]$sorted[$mid - 1] + [double]$sorted[$mid]) / 2
}

function Test-Port([int]$port) {
    $client = $null
    try {
        $client = New-Object System.Net.Sockets.TcpClient
        if ($client.ConnectAsync("127.0.0.1", $port).Wait(200)) { return $true }
        return $false
    } catch { return $false } finally { if ($client) { $client.Close() } }
}

function Wait-Port([int]$port, $process, [int]$timeoutMs) {
    $deadline = (Get-Date).AddMilliseconds($timeoutMs)
    while ((Get-Date) -lt $deadline) {
        if ($process -and $process.HasExited) { throw "mihomo exited before listening on $port" }
        if (Test-Port $port) { return }
        Start-Sleep -Milliseconds 25
    }
    throw "timed out waiting for port $port"
}

function Get-Metrics($process) {
    if (-not $process -or $process.HasExited) { return [pscustomobject]@{ CpuSeconds = 0.0; WorkingSetMiB = 0.0 } }
    $fresh = Get-Process -Id $process.Id -ErrorAction SilentlyContinue
    if (-not $fresh) { return [pscustomobject]@{ CpuSeconds = 0.0; WorkingSetMiB = 0.0 } }
    return [pscustomobject]@{
        CpuSeconds = [double]$fresh.TotalProcessorTime.TotalSeconds
        WorkingSetMiB = [math]::Round($fresh.WorkingSet64 / 1MB, 2)
    }
}

function Write-Config([string]$path, [string]$transport, [string]$mux, [string]$handshake) {
    $yaml = @"
mixed-port: $SocksPort
allow-lan: false
mode: rule
log-level: warning
proxies:
  - name: mieru
    type: mieru
    server: 127.0.0.1
    port: $ServerPort
    transport: $transport
    udp: true
    username: bench
    password: bench-password
    multiplexing: $mux
    handshake-mode: $handshake
rules:
  - MATCH,mieru
"@
    [IO.File]::WriteAllText($path, $yaml, $Utf8NoBom)
}

function Start-Mihomo([string]$scenario, [string]$transport, [string]$mux, [string]$handshake, [int]$round) {
    $configPath = Join-Path $OutDir "mihomo-$scenario.yaml"
    Write-Config $configPath $transport $mux $handshake
    $log = Join-Path $RawDir "mihomo-$scenario-r$round.log"
    $err = Join-Path $RawDir "mihomo-$scenario-r$round.err.log"
    $started = Get-Date
    $process = Start-Process -FilePath $MihomoExe -ArgumentList @("-f", $configPath, "-d", $MihomoData) `
        -PassThru -NoNewWindow -RedirectStandardOutput $log -RedirectStandardError $err
    Wait-Port $SocksPort $process 20000
    $startupMs = [math]::Round(((Get-Date) - $started).TotalMilliseconds, 1)
    Start-Sleep -Milliseconds 500
    return [pscustomobject]@{ Process = $process; StartupMs = $startupMs; Idle = (Get-Metrics $process); Log = $log }
}

function Stop-Mihomo($process) {
    if (-not $process -or $process.HasExited) { return }
    Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
    $process.WaitForExit(5000) | Out-Null
}

function Invoke-Bench($process, [string]$scenario, [string]$mode, [hashtable]$extra) {
    $arguments = @("-socks", "127.0.0.1:$SocksPort", "-label", "mihomo-$scenario", "-mode", $mode)
    foreach ($key in $extra.Keys) { $arguments += @("-$key", [string]$extra[$key]) }
    $before = Get-Metrics $process
    $output = & $ClientExe @arguments 2>&1 | Out-String
    $exit = $LASTEXITCODE
    $after = Get-Metrics $process
    $lines = @($output -split "`r?`n" | Where-Object { $_.TrimStart().StartsWith("{") })
    $result = $null
    if ($lines.Count -gt 0) { try { $result = $lines[$lines.Count - 1] | ConvertFrom-Json } catch {} }
    return [pscustomobject]@{
        Scenario = $scenario; Mode = $mode; Exit = $exit; Result = $result
        CpuSeconds = [math]::Round([double]$after.CpuSeconds - [double]$before.CpuSeconds, 4)
        RssMiB = [double]$after.WorkingSetMiB; Output = $output.Trim()
    }
}

$scenarios = @(
    @{ Name = "tcp-low-default"; Transport = "TCP"; Mux = "MULTIPLEXING_LOW"; Handshake = "HANDSHAKE_DEFAULT" },
    @{ Name = "udp-low-default"; Transport = "UDP"; Mux = "MULTIPLEXING_LOW"; Handshake = "HANDSHAKE_DEFAULT" },
    @{ Name = "tcp-high-nowait"; Transport = "TCP"; Mux = "MULTIPLEXING_HIGH"; Handshake = "HANDSHAKE_NO_WAIT" }
)

try {
    $serverProcess = Start-Process -FilePath $ServerExe -ArgumentList @("-port", "$ServerPort", "-user", "bench", "-password", "bench-password") `
        -PassThru -NoNewWindow -RedirectStandardOutput $ServerLog -RedirectStandardError $ServerErr
    Wait-Port $ServerPort $serverProcess 10000
    foreach ($scenario in $scenarios) {
        for ($round = 1; $round -le $Rounds; $round++) {
            Write-Host "== mihomo $($scenario.Name) round $round"
            $client = $null
            try {
                $client = Start-Mihomo $scenario.Name $scenario.Transport $scenario.Mux $scenario.Handshake $round
                $warmup = Invoke-Bench $client.Process $scenario.Name "tcp-download" @{ connections = 1; bytes = 1048576; size = 1400 }
                $null = $warmup
                $specs = @(
                    @{ Mode = "tcp-download"; Extra = @{ connections = $Connections; bytes = $Bytes; size = $Size } },
                    @{ Mode = "tcp-upload"; Extra = @{ connections = $Connections; bytes = $Bytes; size = $Size } },
                    @{ Mode = "tcp-latency"; Extra = @{ connections = 1; requests = $Requests; size = $Size; warmup = 10 } },
                    @{ Mode = "udp-echo"; Extra = @{ connections = 1; packets = $Packets; size = $Size; window = 16 } },
                    @{ Mode = "first-byte"; Extra = @{ connections = 1; requests = 50; size = 64; warmup = 5 } }
                )
                foreach ($spec in $specs) {
                    $record = Invoke-Bench $client.Process $scenario.Name $spec.Mode $spec.Extra
                    $tag = "mihomo-$($scenario.Name)-r$round-$($spec.Mode)"
                    [ordered]@{
                        variant = "mihomo"; scenario = $scenario.Name; round = $round; mode = $spec.Mode
                        startup_ms = $client.StartupMs; idle = $client.Idle
                        cpu_seconds = $record.CpuSeconds; rss_mib = $record.RssMiB; exit = $record.Exit
                        result = $record.Result; output = $record.Output
                    } | ConvertTo-Json -Depth 12 | ForEach-Object { [IO.File]::WriteAllText((Join-Path $RawDir "$tag.json"), $_, $Utf8NoBom) }
                    $records.Add([pscustomobject]@{
                        Scenario = $scenario.Name; Mode = $spec.Mode; Round = $round; StartupMs = $client.StartupMs
                        CpuSeconds = $record.CpuSeconds; RssMiB = $record.RssMiB
                        PayloadBytes = if ($record.Result) { $record.Result.payload_bytes } else { 0 }
                        ThroughputMiB = if ($record.Result) { $record.Result.throughput_mib_s } else { 0 }
                        P50Ms = if ($record.Result) { $record.Result.p50_ms } else { 0 }
                        P95Ms = if ($record.Result) { $record.Result.p95_ms } else { 0 }
                        P99Ms = if ($record.Result) { $record.Result.p99_ms } else { 0 }
                        PacketsPerSec = if ($record.Result) { $record.Result.packets_per_sec } else { 0 }
                        Loss = if ($record.Result) { $record.Result.loss } else { 0 }
                        Failures = if ($record.Result) { $record.Result.failures } else { 1 }
                    })
                }
            } finally {
                if ($client) { Stop-Mihomo $client.Process }
            }
        }
    }

    $summary = foreach ($group in ($records | Group-Object Scenario, Mode)) {
        $items = $group.Group
        $throughput = @($items | ForEach-Object { [double]$_.ThroughputMiB } | Where-Object { $_ -gt 0 })
        $cpuPerGiB = @($items | Where-Object { $_.PayloadBytes -gt 0 } | ForEach-Object { [double]$_.CpuSeconds / ([double]$_.PayloadBytes / 1GB) })
        [pscustomobject]@{
            Variant = "mihomo"; Scenario = $items[0].Scenario; Mode = $items[0].Mode; Samples = $items.Count
            ThroughputMiB_median = [math]::Round((Median $throughput), 2)
            ThroughputMiB_min = if ($throughput.Count) { [math]::Round(($throughput | Measure-Object -Minimum).Minimum, 2) } else { 0 }
            ThroughputMiB_max = if ($throughput.Count) { [math]::Round(($throughput | Measure-Object -Maximum).Maximum, 2) } else { 0 }
            CPU_s_per_GiB_median = [math]::Round((Median $cpuPerGiB), 2)
            P50Ms_median = [math]::Round((Median @($items.P50Ms)), 3)
            P95Ms_median = [math]::Round((Median @($items.P95Ms)), 3)
            P99Ms_median = [math]::Round((Median @($items.P99Ms)), 3)
            PacketsPerSec_median = [math]::Round((Median @($items.PacketsPerSec)), 1)
            Loss_total = [math]::Round(($items.Loss | Measure-Object -Sum).Sum, 0)
            RssMiB_median = [math]::Round((Median @($items.RssMiB)), 2)
            StartupMs_median = [math]::Round((Median @($items.StartupMs)), 1)
            Failures = [math]::Round(($items.Failures | Measure-Object -Sum).Sum, 0)
        }
    }
    $csv = $summary | Sort-Object Scenario, Mode | ConvertTo-Csv -NoTypeInformation
    [IO.File]::WriteAllLines((Join-Path $OutDir "summary.csv"), [string[]]$csv, $Utf8NoBom)
    $summary | Sort-Object Scenario, Mode | Format-Table -AutoSize | Out-String | Write-Host
} finally {
    if ($serverProcess -and -not $serverProcess.HasExited) { Stop-Process -Id $serverProcess.Id -Force -ErrorAction SilentlyContinue }
}
