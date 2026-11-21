# Fast memory budget gate for CI.
#
# This is the script a workflow step runs. It is deliberately thin: it builds the canonical Windows
# tag profile, runs the FIXED gate matrix (six child processes), evaluates it against
# cmd/internal/memorystudy/testdata/budget.json, and exits non-zero on a budget failure.
#
#   .\scripts\dev\memory-study-gate.ps1
#   .\scripts\dev\memory-study-gate.ps1 -Runs 3 -EvidenceDir D:\artifacts\memory
#
# What it does NOT do, and must not pretend to do:
#   - it is not a device measurement. phys_footprint needs an iOS/tvOS Network Extension; the ledger
#     records it as null and this gate skips no rule on that basis because the rules do not read it;
#   - it does not run the 10000-node scale case or the 300-second idle matrix. -Wide adds 1000 and
#     10000 nodes if a slower job wants them; the 30s/300s matrix is a separate, explicit entry point;
#   - a skipped rule is not a pass. The gate exits non-zero if EVERY rule skips, so a platform
#     mismatch cannot look green.
#
# Baseline: the budget is derived from three independent runs of this same matrix on windows/amd64
# at 00aac2129d889ca1461d44e44ccdb542976b8126 (see testdata/budget.json for the observed values and
# the derivation of each threshold). Re-derive on a new platform rather than editing the numbers:

[CmdletBinding()]
param(
    [int]$Runs = 1,
    [string]$EvidenceDir = 'C:\Deepseek\.hako-general-memory-execution-2026-11-21\metrics\gate',
    [string]$Budget = '',
    [switch]$Wide,
    [string]$GoDebug = '',
    [int]$GoMaxProcs = 0,
    # Use the working tree instead of a pristine archive of HEAD. CI has a clean checkout, so it
    # passes this; a local run against a dirty tree gets the archive by default and is reproducible.
    [switch]$UseWorkingTree
)

$ErrorActionPreference = 'Stop'

$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$goRoot = 'C:\Deepseek\.tools\go1.27.4\go\bin'
if (Test-Path $goRoot) { $env:PATH = "$goRoot;$env:PATH" }
if (-not $env:GOMODCACHE) { $env:GOMODCACHE = 'D:\Go\mod-cache' }

$study = Join-Path $PSScriptRoot 'memory-study.ps1'
$arguments = @{
    Mode          = 'gate'
    Runs          = $Runs
    EvidenceDir   = $EvidenceDir
    Name          = 'gate-' + (Get-Date).ToUniversalTime().ToString('yyyyMMddTHHmmssZ')
    GoMaxProcs    = $GoMaxProcs
    GoDebug       = $GoDebug
}
if ($Budget) { $arguments.Budget = $Budget }
if ($Wide) { $arguments.Wide = $true }
if ($UseWorkingTree) { $arguments.UseWorkingTree = $true }

& $study @arguments
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host 'memory budget gate: PASS'
exit 0
