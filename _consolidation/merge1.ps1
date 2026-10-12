$ErrorActionPreference = 'Continue'
$git  = "C:\src\MinGit\cmd\git.exe"
$repo = "C:\Deepseek\singbox-consolidation"
$out  = "$repo\_consolidation"

function G { param([string[]]$a) & $git -C $repo @a 2>&1 }

# ---- create staging integration branch from latest origin/testing ----
$testingBefore = (G @('rev-parse','origin/testing')) | Select-Object -First 1
& $git -C $repo switch --detach origin/testing 2>&1 | Out-Null
& $git -C $repo switch -c integration/0.1.6-testing-consolidation 2>&1 | Out-Null
$head = (G @('rev-parse','HEAD')) | Select-Object -First 1

"TESTING_BEFORE = $testingBefore"
"INTEGRATION_START = $head"
"MATCH = " + ($testingBefore -eq $head)
"BRANCH = " + ((G @('symbolic-ref','--short','HEAD')) | Select-Object -First 1)
""
"=== TRIAL MERGE ORIGIN/closure/0.1.6-static-validation-final-wire ==="
& $git -C $repo merge --no-commit --no-ff origin/closure/0.1.6-static-validation-final-wire 2>&1 | Out-String
$mergeExit = $LASTEXITCODE
"MERGE_EXIT = $mergeExit"
""
"=== CONFLICT STATUS ==="
$st = @(G @('status','--short'))
$conflicts = @($st | Where-Object { $_ -match '^(UU|AA|DU|UD|AU|UA|DD)\s' })
"CONFLICT_COUNT = " + $conflicts.Count
$conflicts | ForEach-Object { $_ }
""
"=== ALL CHANGED (first 40) ==="
$st | Select-Object -First 40
""
"=== DIFFSTAT (staged merge result) ==="
@(G @('diff','--cached','--shortstat')) | ForEach-Object { $_ }

$conflicts | Set-Content -Encoding UTF8 "$out\final-wire-conflicts.txt"
$st | Set-Content -Encoding UTF8 "$out\final-wire-status.txt"

""
"=== OVERLAP PRE-CHECK: files changed on both sides since merge-base ==="
$base = (G @('merge-base','HEAD','origin/closure/0.1.6-static-validation-final-wire')) | Select-Object -First 1
"BASE = $base"
& $git -C $repo diff --name-only "$base..HEAD" 2>&1 | Sort-Object | Set-Content -Encoding UTF8 "$out\cur-files.txt"
& $git -C $repo diff --name-only "$base..origin/closure/0.1.6-static-validation-final-wire" 2>&1 | Sort-Object | Set-Content -Encoding UTF8 "$out\inc-files.txt"
# HEAD is now MERGE_HEAD-containing state; use the integration start sha instead
& $git -C $repo diff --name-only "$base..$head" 2>&1 | Sort-Object | Set-Content -Encoding UTF8 "$out\cur-files.txt"
$cur = @(Get-Content "$out\cur-files.txt" | Where-Object { $_ })
$inc = @(Get-Content "$out\inc-files.txt" | Where-Object { $_ })
$overlap = @($cur | Where-Object { $inc -contains $_ })
"CUR_FILES(touched on our side since base) = " + $cur.Count
"INC_FILES(incoming side) = " + $inc.Count
"OVERLAP_FILES = " + $overlap.Count
$overlap | Set-Content -Encoding UTF8 "$out\final-wire-overlap.txt"
$overlap
