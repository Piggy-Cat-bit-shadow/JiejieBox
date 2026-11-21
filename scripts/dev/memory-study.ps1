# Stage-by-stage memory measurement for JiejieBox (GM-1).
#
# Two entry points, one binary:
#
#   .\scripts\dev\memory-study.ps1 measure  -case ordinary -nodes 1000 -groups 2 -gc forced
#   .\scripts\dev\memory-study.ps1 gate     -runs 1
#
# The measure mode is the exploratory entry point (any case, any idle matrix). The gate mode is the
# fast, fixed matrix a CI job runs, evaluated against the checked-in baseline-derived budget.
#
# Nothing here writes into the repository except the budget file, and that only with -WriteBudget.
# Raw evidence goes to -EvidenceDir (outside the checkout by default).

[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('measure', 'gate', 'derive-budget')]
    [string]$Mode = 'measure',

    [string]$Case = 'empty',
    [int]$Nodes = -1,
    [int]$Groups = -1,
    [string]$NodeProto = '',
    [string]$Activity = 'none',
    [string]$Idle = '0s',
    [string]$PostIdle = '0s',
    [string]$Gc = 'forced',
    [int]$HeldFlows = 4,
    [int]$TrafficBytes = 65536,
    [int]$Runs = 1,
    [switch]$Wide,

    [string]$EvidenceDir = 'C:\Deepseek\.hako-general-memory-execution-2026-11-21\metrics',
    [string]$Name = '',
    [string]$Budget = '',
    [string]$From = '',
    [int]$GoMaxProcs = 0,
    [string]$GoDebug = '',
    [switch]$InitTrace,

    # Use the repository as it is instead of a pristine archive of HEAD. The default is the archive
    # because it is reproducible: a baseline captured on a working tree that other people are
    # editing cannot be reproduced by anyone.
    [switch]$UseWorkingTree,
    [string]$SourceSha = ''
)

$ErrorActionPreference = 'Stop'

$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$goRoot = 'C:\Deepseek\.tools\go1.27.4\go\bin'
if (Test-Path $goRoot) { $env:PATH = "$goRoot;$env:PATH" }
if (-not $env:GOMODCACHE) { $env:GOMODCACHE = 'D:\Go\mod-cache' }

function Get-RepoTags {
    param([string]$Root)
    $others = Join-Path $Root 'release\DEFAULT_BUILD_TAGS_OTHERS'
    if (-not (Test-Path $others)) { throw "missing $others; the Windows tag profile cannot be resolved" }
    $tags = (Get-Content -LiteralPath $others -Raw).Trim()
    # with_purego is required by the Cronet files at this pin for any Windows build.
    if ($tags -notmatch '(^|,)with_purego(,|$)') { $tags = "$tags,with_purego" }
    return $tags
}

function New-BuildTree {
    param([string]$Repo, [string]$Mode)
    $head = (& git -C $Repo rev-parse HEAD).Trim()
    if ($UseWorkingTree) {
        return [pscustomobject]@{ Root = $Repo; Sha = $head; Dirty = '1'; Origin = "working tree of $Repo at $head (shared with other agents)" }
    }
    $stamp = (Get-Date).ToUniversalTime().ToString('yyyyMMddTHHmmssZ')
    $tree = Join-Path $EvidenceDir "build-tree-$stamp"
    New-Item -ItemType Directory -Force -Path $tree | Out-Null
    $archive = "$tree.tar"
    & git -C $Repo archive --format=tar -o $archive HEAD
    if ($LASTEXITCODE -ne 0) { throw "git archive failed" }
    # tar is used through a FILE: piping a binary tar stream through PowerShell corrupts it.
    & tar -xf $archive -C $tree
    if ($LASTEXITCODE -ne 0) { throw "tar extraction failed" }
    Remove-Item -Force $archive
    Copy-Item -Recurse -Force (Join-Path $Repo 'cmd\internal\memorystudy') (Join-Path $tree 'cmd\internal')
    return [pscustomobject]@{
        Root   = $tree
        Sha    = $head
        Dirty  = '1'
        Origin = "git archive HEAD of $Repo at $head; the only difference from the commit is the added cmd/internal/memorystudy/ instrument"
    }
}

function Build-StudyBinary {
    param([pscustomobject]$Tree)
    $out = Join-Path $EvidenceDir 'bin'
    New-Item -ItemType Directory -Force -Path $out | Out-Null
    $exe = Join-Path $out 'memorystudy.exe'
    $tags = Get-RepoTags -Root $Tree.Root
    Push-Location $Tree.Root
    try {
        & go build -tags $tags -o $exe '.\cmd\internal\memorystudy'
        if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    }
    finally { Pop-Location }
    Write-Host "memorystudy: built $exe with tags $tags"
    return $exe
}

$tree = New-BuildTree -Repo $repo -Mode $Mode
$exe = Build-StudyBinary -Tree $tree
if (-not $SourceSha) { $SourceSha = $tree.Sha }
# The declared identity is what makes an archive build auditable: without a checkout there is no VCS
# stamp, and a ledger with an empty revision is not evidence.
$env:MEMORYSTUDY_SOURCE_SHA = $SourceSha
$env:MEMORYSTUDY_SOURCE_DIRTY = $tree.Dirty
$env:MEMORYSTUDY_SOURCE_ORIGIN = $tree.Origin
if ($GoMaxProcs -gt 0) { $env:GOMAXPROCS = "$GoMaxProcs" }
if ($GoDebug) { $env:GODEBUG = $GoDebug }

if (-not $Name) { $Name = (Get-Date).ToUniversalTime().ToString('yyyyMMddTHHmmssZ') + "-$Mode" }
$jsonl = Join-Path $EvidenceDir "$Name.jsonl"
$csv = Join-Path $EvidenceDir "$Name.csv"

$common = @()
if ($GoMaxProcs -gt 0) { $common += @('-gomaxprocs', "$GoMaxProcs") }
if ($GoDebug) { $common += @('-godebug', $GoDebug) }
if ($InitTrace) { $common += @('-inittrace') }

Push-Location $tree.Root
try {
    switch ($Mode) {
        'measure' {
            $arguments = @('-case', $Case, '-activity', $Activity, '-idle', $Idle, '-post-idle', $PostIdle,
                '-gc', $Gc, '-held-flows', "$HeldFlows", '-traffic-bytes', "$TrafficBytes",
                '-runs', "$Runs", '-output', $jsonl, '-csv', $csv, '-verbose') + $common
            if ($Nodes -ge 0) { $arguments += @('-nodes', "$Nodes") }
            if ($Groups -ge 0) { $arguments += @('-groups', "$Groups") }
            if ($NodeProto) { $arguments += @('-node-proto', $NodeProto) }
            & $exe @arguments
            if ($LASTEXITCODE -ne 0) { throw "memorystudy measure failed with exit $LASTEXITCODE" }
        }
        'gate' {
            $budgetPath = if ($Budget) { $Budget } else { Join-Path $tree.Root 'cmd\internal\memorystudy\testdata\budget.json' }
            if (-not (Test-Path $budgetPath)) {
                throw "no budget at $budgetPath; derive one first: memorystudy.ps1 derive-budget"
            }
            $arguments = @('-gate', '-runs', "$Runs", '-output', $jsonl, '-budget', $budgetPath) + $common
            if ($Wide) { $arguments += @('-wide') }
            & $exe @arguments
            if ($LASTEXITCODE -ne 0) { throw "the memory budget gate failed with exit $LASTEXITCODE" }
        }
        'derive-budget' {
            $budgetPath = if ($Budget) { $Budget } else { Join-Path $repo 'cmd\internal\memorystudy\testdata\budget.json' }
            $arguments = @('-write-budget', $budgetPath) + $common
            if ($From) {
                $arguments += @('-from', $From)
            }
            else {
                $arguments += @('-runs', "$Runs", '-output', $jsonl, '-csv', $csv)
                if ($Wide) { $arguments += @('-wide') }
            }
            & $exe @arguments
            if ($LASTEXITCODE -ne 0) { throw "budget derivation failed with exit $LASTEXITCODE" }
            Write-Host "memorystudy: wrote $budgetPath"
        }
    }
}
finally { Pop-Location }

Write-Host "memorystudy: raw ledger $jsonl"
if (Test-Path $csv) { Write-Host "memorystudy: csv view   $csv" }
