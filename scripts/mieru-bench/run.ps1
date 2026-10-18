param(
    [string]$RepoRoot = "",
    [string]$WorkDir = "",
    [string]$OutDir = "",
    [string]$OfficialMieru = "",
    [string]$TargetBinary = "",
    [string]$Tags = "",
    [int]$Rounds = 2,
    [int]$Connections = 4,
    [long]$Bytes = 33554432,
    [int]$Packets = 2000,
    [int]$Size = 1400,
    [int]$Requests = 500,
    [switch]$SkipOfficial,
    [switch]$SkipBuild
)

# Mieru benchmark orchestrator (Windows loopback).
#
# One run produces: raw/<variant>-<scenario>-r<round>-<mode>.json (the bench
# client's output plus process CPU/RSS), client/server logs, manifest.json and
# summary.csv. Every variant uses the same official-SDK server harness and the
# same local echo targets; only the proxy client changes.

$ErrorActionPreference = "Stop"
$Utf8NoBom = New-Object System.Text.UTF8Encoding($false)
if (-not $RepoRoot) {
    $RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
}
if (-not $WorkDir) {
    $WorkDir = Join-Path $env:TEMP "mieru-bench-work"
}
if (-not $Tags) {
    $Tags = (Get-Content (Join-Path $RepoRoot "release\DEFAULT_BUILD_TAGS_OTHERS") -Raw).Trim()
}
$Sha = (git -C $RepoRoot rev-parse --short=12 HEAD).Trim()
if (-not $OutDir) {
    $OutDir = Join-Path $RepoRoot "verify-evidence\mieru\$(Get-Date -Format 'yyyy-MM-dd')-$Sha"
}
$RawDir = Join-Path $OutDir "raw"
New-Item -ItemType Directory -Force -Path $WorkDir, $OutDir, $RawDir | Out-Null

$ServerPort = 18671
$OfficialSocksPort = 11091
$TargetSocksPort = 11092
$ServerLog = Join-Path $RawDir "server.log"
$ServerErr = Join-Path $RawDir "server.err.log"
$ClientExe = Join-Path $WorkDir "mieru-bench-client.exe"
$ServerExe = Join-Path $WorkDir "mieru-bench-server.exe"
if (-not $TargetBinary) {
    $TargetBinary = Join-Path $WorkDir "sing-box-mieru.exe"
}

$records = New-Object System.Collections.Generic.List[object]
$serverProcess = $null

function Write-Step([string]$message) {
    Write-Host "== $message"
}

function Test-Port([int]$port) {
    $client = $null
    try {
        $client = New-Object System.Net.Sockets.TcpClient
        $async = $client.ConnectAsync("127.0.0.1", $port)
        if ($async.Wait(200)) {
            return $true
        }
        return $false
    } catch {
        return $false
    } finally {
        if ($client) { $client.Close() }
    }
}

function Wait-Port([int]$port, [System.Diagnostics.Process]$process, [int]$timeoutMs) {
    $deadline = (Get-Date).AddMilliseconds($timeoutMs)
    while ((Get-Date) -lt $deadline) {
        if ($process -and $process.HasExited) {
            throw "process $($process.Id) exited before listening on port $port"
        }
        if (Test-Port $port) {
            return
        }
        Start-Sleep -Milliseconds 25
    }
    throw "timed out waiting for port $port"
}

function Get-Metrics([System.Diagnostics.Process]$process) {
    if (-not $process -or $process.HasExited) {
        return [pscustomobject]@{ CpuSeconds = 0.0; WorkingSetMiB = 0.0; Handles = 0 }
    }
    $fresh = Get-Process -Id $process.Id -ErrorAction SilentlyContinue
    if (-not $fresh) {
        return [pscustomobject]@{ CpuSeconds = 0.0; WorkingSetMiB = 0.0; Handles = 0 }
    }
    return [pscustomobject]@{
        CpuSeconds   = [double]$fresh.TotalProcessorTime.TotalSeconds
        WorkingSetMiB = [math]::Round($fresh.WorkingSet64 / 1MB, 2)
        Handles      = [int]$fresh.HandleCount
    }
}

function Build-Artifacts {
    if ($SkipBuild) { return }
    Write-Step "build bench client"
    & go build -o $ClientExe (Join-Path $RepoRoot "scripts\mieru-bench\client")
    if ($LASTEXITCODE -ne 0) { throw "bench client build failed" }
    Write-Step "build official-SDK server harness"
    & go build -o $ServerExe (Join-Path $RepoRoot "scripts\mieru-bench\server")
    if ($LASTEXITCODE -ne 0) { throw "server harness build failed" }
    if (-not (Test-Path $TargetBinary)) {
        Write-Step "build target $TargetBinary"
        Push-Location $RepoRoot
        try {
            & go build -tags $Tags -o $TargetBinary ./cmd/sing-box
            if ($LASTEXITCODE -ne 0) { throw "target build failed" }
        } finally {
            Pop-Location
        }
    }
}

function Start-Server {
    Write-Step "start official-SDK server on $ServerPort"
    $script:serverProcess = Start-Process -FilePath $ServerExe `
        -ArgumentList @("-port", "$ServerPort", "-user", "bench", "-password", "bench-password") `
        -PassThru -NoNewWindow -RedirectStandardOutput $ServerLog -RedirectStandardError $ServerErr
    Wait-Port $ServerPort $script:serverProcess 10000
    Write-Host "server pid=$($script:serverProcess.Id)"
}

function MuxValue([string]$mux) {
    switch ($mux) {
        "off" { return "MULTIPLEXING_OFF" }
        "low" { return "MULTIPLEXING_LOW" }
        "middle" { return "MULTIPLEXING_MIDDLE" }
        "high" { return "MULTIPLEXING_HIGH" }
        default { throw "unknown mux $mux" }
    }
}

function HandshakeValue([string]$handshake) {
    switch ($handshake) {
        "default" { return "HANDSHAKE_DEFAULT" }
        "no_wait" { return "HANDSHAKE_NO_WAIT" }
        default { throw "unknown handshake $handshake" }
    }
}

function PortBindingsFor([string]$transport) {
    switch ($transport) {
        "tcp" { return @(@{ port = $ServerPort; protocol = "TCP" }) }
        "udp" { return @(@{ port = $ServerPort; protocol = "UDP" }) }
        "both" { return @(@{ port = $ServerPort; protocol = "TCP" }, @{ port = $ServerPort; protocol = "UDP" }) }
        default { throw "unknown transport $transport" }
    }
}

function Write-OfficialConfig([string]$path, [string]$transport, [string]$mux, [string]$handshake) {
    $config = [ordered]@{
        profiles = @(
            [ordered]@{
                profileName = "default"
                user = @{ name = "bench"; password = "bench-password" }
                servers = @(
                    [ordered]@{
                        ipAddress = "127.0.0.1"
                        portBindings = @(PortBindingsFor $transport)
                    }
                )
                multiplexing = @{ level = (MuxValue $mux) }
                handshakeMode = (HandshakeValue $handshake)
            }
        )
        activeProfile = "default"
        rpcPort = 18993
        socks5Port = $OfficialSocksPort
        advancedSettings = @{ noCheckUpdate = $true }
        loggingLevel = "WARN"
    }
    $config | ConvertTo-Json -Depth 10 | ForEach-Object { [IO.File]::WriteAllText($path, $_, $Utf8NoBom) }
}

function Write-TargetConfig([string]$path, [string]$transport, [string]$mux, [string]$handshake) {
    $config = [ordered]@{
        inbounds = @(
            [ordered]@{ type = "mixed"; tag = "mixed-in"; listen = "127.0.0.1"; listen_port = $TargetSocksPort }
        )
        outbounds = @(
            [ordered]@{
                type = "mieru"; tag = "mieru-out"; server = "127.0.0.1"; server_port = $ServerPort
                username = "bench"; password = "bench-password"
                transport = $transport; multiplexing = $mux; handshake_mode = $handshake
            }
        )
        route = [ordered]@{ rules = @([ordered]@{ inbound = @("mixed-in"); action = "route"; outbound = "mieru-out" }) }
        log = @{ level = "warn" }
    }
    $config | ConvertTo-Json -Depth 10 | ForEach-Object { [IO.File]::WriteAllText($path, $_, $Utf8NoBom) }
}

function Start-Client([string]$variant, [string]$scenario, [string]$transport, [string]$mux, [string]$handshake, [int]$round) {
    $tag = "$variant-$scenario-r$round"
    $stdout = Join-Path $RawDir "$tag.log"
    $stderr = Join-Path $RawDir "$tag.err.log"
    $started = Get-Date
    if ($variant -eq "official") {
        if (-not $OfficialMieru -or -not (Test-Path $OfficialMieru)) {
            throw "official mieru.exe not found; pass -OfficialMieru"
        }
        $configPath = Join-Path $OutDir "official-$scenario.json"
        Write-OfficialConfig $configPath $transport $mux $handshake
        $env:MIERU_CONFIG_JSON_FILE = $configPath
        try {
            $process = Start-Process -FilePath $OfficialMieru -ArgumentList @("run") -PassThru -NoNewWindow `
                -RedirectStandardOutput $stdout -RedirectStandardError $stderr
        } finally {
            Remove-Item Env:MIERU_CONFIG_JSON_FILE -ErrorAction SilentlyContinue
        }
        $socksPort = $OfficialSocksPort
    } else {
        $configPath = Join-Path $OutDir "target-$scenario.json"
        Write-TargetConfig $configPath $transport $mux $handshake
        $process = Start-Process -FilePath $TargetBinary -ArgumentList @("run", "-c", $configPath) -PassThru -NoNewWindow `
            -RedirectStandardOutput $stdout -RedirectStandardError $stderr
        $socksPort = $TargetSocksPort
    }
    Wait-Port $socksPort $process 20000
    $startupMs = [math]::Round(((Get-Date) - $started).TotalMilliseconds, 1)
    Start-Sleep -Milliseconds 500
    $idle = Get-Metrics $process
    return [pscustomobject]@{
        Variant = $variant
        Scenario = $scenario
        Process = $process
        SocksPort = $socksPort
        StartupMs = $startupMs
        Idle = $idle
        Log = $stdout
        ErrLog = $stderr
    }
}

function Stop-Client($client) {
    if (-not $client -or -not $client.Process) { return }
    $process = $client.Process
    if (-not $process.HasExited) {
        try { Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue } catch {}
        try { $process.WaitForExit(5000) | Out-Null } catch {}
    }
}

function Invoke-Bench($client, [string]$mode, [hashtable]$extra) {
    $arguments = @("-socks", "127.0.0.1:$($client.SocksPort)", "-label", "$($client.Variant)-$($client.Scenario)", "-mode", $mode)
    foreach ($key in $extra.Keys) {
        $arguments += @("-$key", [string]$extra[$key])
    }
    $before = Get-Metrics $client.Process
    $jsonText = & $ClientExe @arguments 2>&1 | Out-String
    $exit = $LASTEXITCODE
    $after = Get-Metrics $client.Process
    $result = $null
    $lines = @($jsonText -split "`r?`n" | Where-Object { $_.TrimStart().StartsWith("{") })
    if ($lines.Count -gt 0) {
        try { $result = $lines[$lines.Count - 1] | ConvertFrom-Json } catch {}
    }
    return [pscustomobject]@{
        Client = $client
        Mode = $mode
        Exit = $exit
        Output = $jsonText.Trim()
        Result = $result
        CpuBefore = $before
        CpuAfter = $after
        CpuSeconds = [math]::Round([double]$after.CpuSeconds - [double]$before.CpuSeconds, 4)
        RssAfter = [double]$after.WorkingSetMiB
    }
}

function Save-Record($record, [int]$round, [int]$order) {
    $client = $record.Client
    $tag = "$($client.Variant)-$($client.Scenario)-r$round-$order-$($record.Mode)"
    $payload = [ordered]@{
        variant = $client.Variant
        scenario = $client.Scenario
        round = $round
        order = $order
        mode = $record.Mode
        startup_ms = $client.StartupMs
        idle = $client.Idle
        cpu_seconds = $record.CpuSeconds
        rss_mib = $record.RssAfter
        exit = $record.Exit
        result = $record.Result
        output = $record.Output
    }
    $payload | ConvertTo-Json -Depth 12 | ForEach-Object { [IO.File]::WriteAllText((Join-Path $RawDir "$tag.json"), $_, $Utf8NoBom) }
    $records.Add([pscustomobject]@{
        Variant = $client.Variant
        Scenario = $client.Scenario
        Round = $round
        Order = $order
        Mode = $record.Mode
        StartupMs = $client.StartupMs
        IdleRssMiB = $client.Idle.WorkingSetMiB
        CpuSeconds = $record.CpuSeconds
        RssMiB = $record.RssAfter
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

function Get-Median([double[]]$values) {
    $sorted = @($values | Sort-Object)
    if ($sorted.Count -eq 0) { return 0 }
    $middle = [int][math]::Floor($sorted.Count / 2)
    if ($sorted.Count % 2 -eq 1) {
        return [double]$sorted[$middle]
    }
    return ([double]$sorted[$middle - 1] + [double]$sorted[$middle]) / 2
}

function Write-Summary {
    $groups = $records | Group-Object Variant, Scenario, Mode
    $summary = foreach ($group in $groups) {
        $items = $group.Group
        $throughput = @($items | Where-Object { $_.ThroughputMiB -gt 0 } | ForEach-Object { $_.ThroughputMiB })
        $cpuPerGiB = @($items | Where-Object { $_.PayloadBytes -gt 0 } | ForEach-Object { $_.CpuSeconds / ($_.PayloadBytes / 1GB) })
        [pscustomobject]@{
            Variant = $items[0].Variant
            Scenario = $items[0].Scenario
            Mode = $items[0].Mode
            Samples = $items.Count
            ThroughputMiB_median = [math]::Round((Get-Median $throughput), 2)
            ThroughputMiB_min = if ($throughput.Count) { [math]::Round(($throughput | Measure-Object -Minimum).Minimum, 2) } else { 0 }
            ThroughputMiB_max = if ($throughput.Count) { [math]::Round(($throughput | Measure-Object -Maximum).Maximum, 2) } else { 0 }
            CPU_s_per_GiB_median = [math]::Round((Get-Median $cpuPerGiB), 2)
            P50Ms_median = [math]::Round((Get-Median @($items.P50Ms)), 3)
            P95Ms_median = [math]::Round((Get-Median @($items.P95Ms)), 3)
            P99Ms_median = [math]::Round((Get-Median @($items.P99Ms)), 3)
            PacketsPerSec_median = [math]::Round((Get-Median @($items.PacketsPerSec)), 1)
            Loss_total = [math]::Round(($items.Loss | Measure-Object -Sum).Sum, 0)
            RssMiB_median = [math]::Round((Get-Median @($items.RssMiB)), 2)
            StartupMs_median = [math]::Round((Get-Median @($items.StartupMs)), 1)
            Failures = [math]::Round(($items.Failures | Measure-Object -Sum).Sum, 0)
        }
    }
    $csv = $summary | Sort-Object Variant, Scenario, Mode | ConvertTo-Csv -NoTypeInformation
    [IO.File]::WriteAllLines((Join-Path $OutDir "summary.csv"), [string[]]$csv, $Utf8NoBom)
    $summary | Sort-Object Variant, Scenario, Mode | Format-Table -AutoSize | Out-String | Write-Host
}

function Write-Manifest {
    $os = Get-CimInstance Win32_OperatingSystem
    $cpu = Get-CimInstance Win32_Processor | Select-Object -First 1
    $hash = if (Test-Path $TargetBinary) { (Get-FileHash -Algorithm SHA256 $TargetBinary).Hash } else { "" }
    $officialHash = if ($OfficialMieru -and (Test-Path $OfficialMieru)) { (Get-FileHash -Algorithm SHA256 $OfficialMieru).Hash } else { "" }
    [ordered]@{
        date = (Get-Date).ToString("o")
        head = $Sha
        repo = $RepoRoot
        os = "$($os.Caption) $($os.Version)"
        cpu = $cpu.Name
        go = (& go version)
        tags = $Tags
        target_binary = $TargetBinary
        target_sha256 = $hash
        official_client = $OfficialMieru
        official_client_sha256 = $officialHash
        server_harness_sha256 = (Get-FileHash -Algorithm SHA256 $ServerExe).Hash
        bench_client_sha256 = (Get-FileHash -Algorithm SHA256 $ClientExe).Hash
        rounds = $Rounds
        connections = $Connections
        bytes = $Bytes
        packets = $Packets
        packet_size = $Size
        requests = $Requests
        scenarios = $script:scenarioList
        variants = $script:variantList
    } | ConvertTo-Json -Depth 6 | ForEach-Object { [IO.File]::WriteAllText((Join-Path $OutDir "manifest.json"), $_, $Utf8NoBom) }
}

$scenarioList = @(
    [ordered]@{ Name = "tcp-low-default"; Transport = "tcp"; Mux = "low"; Handshake = "default" },
    [ordered]@{ Name = "udp-low-default"; Transport = "udp"; Mux = "low"; Handshake = "default" },
    [ordered]@{ Name = "tcp-high-nowait"; Transport = "tcp"; Mux = "high"; Handshake = "no_wait" }
)
$variantList = if ($SkipOfficial) { @("target") } else { @("official", "target") }

try {
    Build-Artifacts
    Start-Server
    foreach ($scenario in $scenarioList) {
        for ($round = 1; $round -le $Rounds; $round++) {
            $order = if ($round % 2 -eq 0) { @("official", "target") } else { @("target", "official") }
            $order = @($order | Where-Object { $variantList -contains $_ })
            $position = 0
            foreach ($variant in $order) {
                $position++
                Write-Step "$($scenario.Name) round $round variant $variant"
                $client = $null
                try {
                    $client = Start-Client $variant $scenario.Name $scenario.Transport $scenario.Mux $scenario.Handshake $round
                    Write-Host "startup=$($client.StartupMs)ms idleRSS=$($client.Idle.WorkingSetMiB)MiB"
                    # Warm up the SDK, DNS and the local listener before measuring.
                    $null = Invoke-Bench $client "tcp-download" @{ connections = 1; bytes = 1048576; size = 1400 }
                    $specifications = @(
                        @{ Mode = "tcp-download"; Extra = @{ connections = $Connections; bytes = $Bytes; size = $Size } },
                        @{ Mode = "tcp-upload"; Extra = @{ connections = $Connections; bytes = $Bytes; size = $Size } },
                        @{ Mode = "tcp-latency"; Extra = @{ connections = 1; requests = $Requests; size = $Size; warmup = 10 } },
                        @{ Mode = "udp-echo"; Extra = @{ connections = 1; packets = $Packets; size = $Size; window = 16 } },
                        @{ Mode = "first-byte"; Extra = @{ connections = 1; requests = 50; size = 64; warmup = 5 } }
                    )
                    foreach ($specification in $specifications) {
                        $record = Invoke-Bench $client $specification.Mode $specification.Extra
                        Save-Record $record $round $position
                    }
                } finally {
                    Stop-Client $client
                }
            }
        }
    }
    Write-Summary
    Write-Manifest
    Write-Host "evidence: $OutDir"
} finally {
    if ($serverProcess -and -not $serverProcess.HasExited) {
        Stop-Process -Id $serverProcess.Id -Force -ErrorAction SilentlyContinue
    }
}
