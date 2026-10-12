$ErrorActionPreference = 'Continue'
$git  = "C:\src\MinGit\cmd\git.exe"
$repo = "C:\Deepseek\singbox-consolidation"
$out  = "$repo\_consolidation"

$branches = @(
  'audit/upstream-absorb-0.1.6-91ca1652d',
  'closure/0.1.6-consumer-dns-parity',
  'closure/0.1.6-group-crosskind',
  'closure/0.1.6-static-validation-final',
  'closure/0.1.6-static-validation-final-wire',
  'closure/0.1.6-static-validation-true-final',
  'debug/static-validation-false-deny-20261012',
  'fix/route-aware-outbound-capability-validation',
  'fix/0.1.6-apple-pin-gates',
  'integration/0.1.6-final-freeze',
  'jiejiebox/apple-finalize',
  'jiejiebox/p1c-iphone-device-closure',
  'jiejiebox/v0.1.6-hardening'
)

function G { param([string[]]$a) & $git -C $repo @a 2>&1 }

$rows = @()
foreach ($b in $branches) {
  $ref = "origin/$b"
  $tip = (G @('rev-parse', $ref)) | Select-Object -First 1
  $subject = (G @('log','-1','--format=%s', $ref)) | Select-Object -First 1
  $mb = (G @('merge-base','origin/testing', $ref)) | Select-Object -First 1
  $lr = (G @('rev-list','--left-right','--count', "origin/testing...$ref")) | Select-Object -First 1

  $ahead = 0; $behind = 0
  if ($lr -match '^(\d+)\s+(\d+)$') { $behind = [int]$Matches[1]; $ahead = [int]$Matches[2] }

  # unique non-merge commits on the branch not in testing
  $uniq = @(G @('rev-list','--no-merges','--count', "origin/testing..$ref"))
  $uniqCount = 0
  if ($uniq.Count -gt 0 -and "$($uniq[0])" -match '^\d+$') { $uniqCount = [int]$uniq[0] }

  # patch-equivalence vs testing
  $cherry = @(G @('cherry','origin/testing', $ref))
  $plus  = @($cherry | Where-Object { $_ -match '^\+' }).Count
  $minus = @($cherry | Where-Object { $_ -match '^-' }).Count

  # diffstat vs merge-base
  $stat = @(G @('diff','--shortstat', "$mb..$ref"))
  $statLine = if ($stat.Count -gt 0) { ("$($stat[0])".Trim()) } else { '' }

  # changed file count
  $files = @(G @('diff','--name-only', "$mb..$ref"))
  $fileCount = @($files | Where-Object { $_ -and $_ -notmatch '^warning:' }).Count

  $rows += [pscustomobject]@{
    branch     = $b
    tip        = $tip
    subject    = $subject
    merge_base = $mb
    ahead      = $ahead
    behind     = $behind
    uniq_nomerge = $uniqCount
    cherry_plus  = $plus
    cherry_minus = $minus
    files        = $fileCount
    shortstat    = $statLine
  }
}

$rows | ConvertTo-Json -Depth 4 | Set-Content -Encoding UTF8 "$out\branch-metrics.json"

# ancestry matrix between candidate branches
$anc = @()
foreach ($a in $branches) {
  foreach ($b in $branches) {
    if ($a -eq $b) { continue }
    & $git -C $repo merge-base --is-ancestor "origin/$a" "origin/$b" 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { $anc += "$a  IS_ANCESTOR_OF  $b" }
  }
}
$anc | Set-Content -Encoding UTF8 "$out\ancestry-matrix.txt"

# ancestry of each branch vs testing
$vsTesting = @()
foreach ($b in $branches) {
  & $git -C $repo merge-base --is-ancestor "origin/$b" "origin/testing" 2>&1 | Out-Null
  $isAnc = ($LASTEXITCODE -eq 0)
  $vsTesting += "{0,-52} ancestor_of_testing={1}" -f $b, $isAnc
}
$vsTesting | Set-Content -Encoding UTF8 "$out\vs-testing.txt"

# branch tip short log for the report
$logLines = @()
foreach ($b in $branches) {
  $logLines += "### $b"
  $logLines += @(G @('log','--oneline','--no-merges', "origin/testing..origin/$b"))
  $logLines += ""
}
$logLines | Set-Content -Encoding UTF8 "$out\unique-commits.txt"

"=== METRICS ==="
$rows | ForEach-Object {
  "{0,-52} ahead={1,-4} behind={2,-4} uniq={3,-4} cherry(+/-)={4}/{5} files={6}" -f `
    $_.branch, $_.ahead, $_.behind, $_.uniq_nomerge, $_.cherry_plus, $_.cherry_minus, $_.files
}
""
"=== ANCESTRY PAIRS ==="
if ($anc.Count -eq 0) { "(none)" } else { $anc }
""
"=== VS TESTING ==="
$vsTesting
""
"=== WRITTEN ==="
Get-ChildItem $out | Select-Object Name, Length | Format-Table -AutoSize
