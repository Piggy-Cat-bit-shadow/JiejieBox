# W8 measurement driver.
#
# One measurement point = ONE process = ONE `go test -run <Test>` invocation. This script
# never uses -count to fake independent samples: every sample is a fresh subprocess, with
# its own $W8_ROOT (a fresh scratch directory), appending one JSON row to <Out>/rows.jsonl.
#
# Candidate and reference are alternated per sample, so a machine that drifts over the run
# cannot favour one side. The script records for every sample: point, side, sample index,
# git HEAD, tree state, start/end time, exit code, and the number of other `go`/`go.exe`
# processes alive at the start (the "what else was running" column).
#
# Usage (authoritative, frozen SHA, from a clean worktree):
#   .\run.ps1 -Candidate C:\Deepseek\.w8-candidate -Reference C:\Deepseek\.w8-ref `
#             -Root C:\Deepseek\JiejieBox-autonomous-final-closeout-2026-11-23\04-evidence\samples\w8 `
#             -Out  C:\Deepseek\JiejieBox-autonomous-final-closeout-2026-11-23\04-evidence\logs\w8 `
#             -Samples 5
#
# Usage (one side only):
#   .\run.ps1 -Candidate C:\Deepseek\JiejieBox -Samples 1 -Points w2-quietbulk
#
# -Points takes a comma-separated list of point names (see $Points below) or `all`.
# -ExtraTags appends tags (e.g. ",with_low_memory"); the tag string is recorded in every row.

param(
    [string]$Candidate = '',
    [string]$Reference = '',
    [Parameter(Mandatory = $true)][string]$Root,
    [Parameter(Mandatory = $true)][string]$Out,
    [string]$GoExe = 'C:\Deepseek\.tools\go1.27.4\go\bin\go.exe',
    [int]$Samples = 5,
    [string]$Points = 'all',
    [string]$ExtraTags = '',
    [string]$PointTimeout = '20m'
)

$ErrorActionPreference = 'Stop'
New-Item -ItemType Directory -Force -Path $Root, $Out | Out-Null
$RowsPath = Join-Path $Out 'rows.jsonl'
$RunsCsv = Join-Path $Out 'runs.csv'
$DriverLog = Join-Path $Out ('driver-{0}.log' -f (Get-Date -Format 'yyyyMMdd-HHmmss'))

function Write-Log([string]$Message) {
    $line = '{0} {1}' -f (Get-Date -Format 'HH:mm:ss.fff'), $Message
    Write-Host $line
    Add-Content -Path $DriverLog -Value $line -Encoding utf8
}

function Get-Sha([string]$Repo) {
    $sha = & git -C $Repo rev-parse HEAD 2>$null
    if ($LASTEXITCODE -ne 0) { return 'unknown' }
    return $sha.Trim()
}

function Get-TreeState([string]$Repo) {
    $dirty = @(& git -C $Repo status --porcelain 2>$null)
    if ($LASTEXITCODE -ne 0) { return 'unknown' }
    if ($dirty.Count -eq 0) { return 'clean' }
    return ('dirty:{0}' -f $dirty.Count)
}

function Get-Tags([string]$Repo, [string]$Extra) {
    $base = (Get-Content -Raw (Join-Path $Repo 'release\DEFAULT_BUILD_TAGS_OTHERS')).Trim()
    return ($base + ',with_purego' + $Extra)
}

function Get-GoProcessCount {
    return @(Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.ProcessName -in @('go', 'compile', 'link', 'vet') }).Count
}

# Each point is: name, test regex, extra environment (hashtable), and whether it is one of
# the "quiet window" points that should not run while another measurement point is alive.
$PointTable = @(
    @{ Name = 'w1-publish';          Test = '^TestW8W1PublishRestore$';               Env = @{} }
    @{ Name = 'w1-damaged-bytes';    Test = '^TestW8W1DamagedRepair$';                Env = @{ W8_DAMAGE = 'bytes' } }
    @{ Name = 'w1-damaged-manifest'; Test = '^TestW8W1DamagedRepair$';                Env = @{ W8_DAMAGE = 'manifest' } }
    @{ Name = 'w1-damaged-missing';  Test = '^TestW8W1DamagedRepair$';                Env = @{ W8_DAMAGE = 'missing' } }
    @{ Name = 'w2-quietbulk';        Test = '^TestW8W2QuietBulk$';                    Env = @{} }
    @{ Name = 'w2-pressure';         Test = '^TestW8W2Pressure$';                     Env = @{} }
    @{ Name = 'w2-geometry';         Test = '^TestW8W2Geometry$';                     Env = @{} }
    @{ Name = 'g01-store-1000-cold'; Test = '^TestW8G01Fetch$'; Env = @{ W8_MODE = 'store'; W8_ENTRIES = '1000'; W8_ARM = 'cold' } }
    @{ Name = 'g01-store-1000-200';  Test = '^TestW8G01Fetch$'; Env = @{ W8_MODE = 'store'; W8_ENTRIES = '1000'; W8_ARM = 'update200' } }
    @{ Name = 'g01-store-1000-304';  Test = '^TestW8G01Fetch$'; Env = @{ W8_MODE = 'store'; W8_ENTRIES = '1000'; W8_ARM = 'update304' } }
    @{ Name = 'g01-store-10000-cold'; Test = '^TestW8G01Fetch$'; Env = @{ W8_MODE = 'store'; W8_ENTRIES = '10000'; W8_ARM = 'cold' } }
    @{ Name = 'g01-store-10000-200'; Test = '^TestW8G01Fetch$'; Env = @{ W8_MODE = 'store'; W8_ENTRIES = '10000'; W8_ARM = 'update200' } }
    @{ Name = 'g01-store-10000-304'; Test = '^TestW8G01Fetch$'; Env = @{ W8_MODE = 'store'; W8_ENTRIES = '10000'; W8_ARM = 'update304' } }
    @{ Name = 'g01-cachefile-10000-304'; Test = '^TestW8G01Fetch$'; Env = @{ W8_MODE = 'cachefile'; W8_ENTRIES = '10000'; W8_ARM = 'update304' } }
    @{ Name = 'g01-both-10000-200';  Test = '^TestW8G01Fetch$'; Env = @{ W8_MODE = 'both'; W8_ENTRIES = '10000'; W8_ARM = 'update200' } }
    @{ Name = 'g01-both-10000-304';  Test = '^TestW8G01Fetch$'; Env = @{ W8_MODE = 'both'; W8_ENTRIES = '10000'; W8_ARM = 'update304' } }
    @{ Name = 'g01-source-10000-cold'; Test = '^TestW8G01Fetch$'; Env = @{ W8_MODE = 'store'; W8_ENTRIES = '10000'; W8_ARM = 'cold'; W8_RULESET_FORMAT = 'source' } }
    @{ Name = 'g01-shortbody';       Test = '^TestW8G01ShortBody$';                   Env = @{} }
    @{ Name = 'g01-fault-save_short'; Test = '^TestW8G01CacheFaults$'; Env = @{ W8_FAULT = 'save_short' } }
    @{ Name = 'g01-fault-save_error'; Test = '^TestW8G01CacheFaults$'; Env = @{ W8_FAULT = 'save_error' } }
    @{ Name = 'g01-fault-load_garbage'; Test = '^TestW8G01CacheFaults$'; Env = @{ W8_FAULT = 'load_garbage' } }
    @{ Name = 'cycles-buildclose';   Test = '^TestW8LongBuildClose$';                 Env = @{ W8_CYCLES = '100'; W8_SAMPLE_EVERY = '10' } }
    @{ Name = 'cycles-provider';     Test = '^TestW8LongProviderUpdate$';             Env = @{ W8_CYCLES = '100'; W8_NODES = '1000'; W8_SAMPLE_EVERY = '10' } }
)

$selected = $PointTable
if ($Points -ne 'all') {
    $wanted = $Points -split ',' | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne '' }
    $selected = $PointTable | Where-Object { $wanted -contains $_.Name }
    if ($selected.Count -eq 0) { throw "no point matched -Points '$Points'" }
}

$sides = @()
if ($Candidate -ne '') { $sides += @{ Name = 'candidate'; Repo = $Candidate } }
if ($Reference -ne '') { $sides += @{ Name = 'reference'; Repo = $Reference } }
if ($sides.Count -eq 0) { throw 'pass -Candidate and/or -Reference' }

foreach ($side in $sides) {
    $side.Sha = Get-Sha $side.Repo
    $side.Tree = Get-TreeState $side.Repo
    $side.Tags = Get-Tags $side.Repo $ExtraTags
    Write-Log ('side={0} repo={1} sha={2} tree={3}' -f $side.Name, $side.Repo, $side.Sha, $side.Tree)
}

if (-not (Test-Path $RunsCsv)) {
    Set-Content -Path $RunsCsv -Value 'point,side,sample,sha,tree,tags,start_utc,end_utc,exit_code,other_go_processes,rows' -Encoding utf8
}

$overallStart = Get-Date
Write-Log ('driver start; points={0}; samples={1}; extra_tags={2}' -f $selected.Count, $Samples, $ExtraTags)

foreach ($point in $selected) {
    for ($sample = 1; $sample -le $Samples; $sample++) {
        # Alternate which side runs first so a warming cache or a drifting machine cannot
        # systematically favour one side.
        $ordered = @($sides)
        if (($sample % 2) -eq 0) { [array]::Reverse($ordered) }
        foreach ($side in $ordered) {
            $sampleRoot = Join-Path (Join-Path $Root $point.Name) ('{0}-{1}' -f $side.Name, $sample)
            if (Test-Path $sampleRoot) { Remove-Item -Recurse -Force $sampleRoot }
            New-Item -ItemType Directory -Force -Path $sampleRoot | Out-Null

            $start = (Get-Date).ToUniversalTime()
            $otherGo = Get-GoProcessCount
            Write-Log ('sample point={0} side={1} n={2} other_go={3}' -f $point.Name, $side.Name, $sample, $otherGo)

            $saved = @{}
            foreach ($key in $point.Env.Keys) {
                $saved[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
                [Environment]::SetEnvironmentVariable($key, [string]$point.Env[$key], 'Process')
            }
            $savedRows = if (Test-Path $RowsPath) { (Get-Content $RowsPath | Measure-Object -Line).Lines } else { 0 }
            $env:W8_SIDE = $side.Name
            $env:W8_SHA = $side.Sha
            $env:W8_TREE = $side.Tree
            $env:W8_TAGS = $side.Tags
            $env:W8_POINT = $point.Name
            $env:W8_OUT = $RowsPath
            $env:W8_ROOT = $sampleRoot

            Push-Location $side.Repo
            try {
                & $GoExe test -tags $side.Tags -run $point.Test -count=1 -v -timeout $PointTimeout ./internal/w8measure 2>&1 |
                    Tee-Object -FilePath (Join-Path $Out ('{0}.{1}.{2}.log' -f $point.Name, $side.Name, $sample)) |
                    Out-Null
                $code = $LASTEXITCODE
            }
            finally {
                Pop-Location
            }
            $end = (Get-Date).ToUniversalTime()
            $rowsNow = if (Test-Path $RowsPath) { (Get-Content $RowsPath | Measure-Object -Line).Lines } else { 0 }
            $newRows = $rowsNow - $savedRows
            Add-Content -Path $RunsCsv -Encoding utf8 -Value ('{0},{1},{2},{3},{4},"{5}",{6},{7},{8},{9},{10}' -f `
                    $point.Name, $side.Name, $sample, $side.Sha, $side.Tree, $side.Tags,
                $start.ToString('o'), $end.ToString('o'), $code, $otherGo, $newRows)
            Write-Log ('done point={0} side={1} n={2} exit={3} rows={4}' -f $point.Name, $side.Name, $sample, $code, $newRows)
            foreach ($key in $saved.Keys) {
                [Environment]::SetEnvironmentVariable($key, $saved[$key], 'Process')
            }
        }
    }
}

$overallEnd = Get-Date
$finalRows = if (Test-Path $RowsPath) { (Get-Content $RowsPath | Measure-Object -Line).Lines } else { 0 }
Write-Log ('driver end; elapsed_s={0:N1}; rows={1}' -f ($overallEnd - $overallStart).TotalSeconds, $finalRows)

