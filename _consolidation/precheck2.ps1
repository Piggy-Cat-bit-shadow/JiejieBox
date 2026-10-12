$ErrorActionPreference = 'Continue'
$git  = "C:\src\MinGit\cmd\git.exe"
$repo = "C:\Deepseek\singbox-consolidation"
$out  = "$repo\_consolidation"
Set-Location $repo
function G { param([string[]]$a) & $git @a 2>&1 }

$branches = @(
  'fix/0.1.6-apple-pin-gates',
  'jiejiebox/apple-finalize'
)

"=== whose commits are already in the staged integration tree? ==="
foreach ($b in $branches) {
  $ref = "origin/$b"
  & $git merge-base --is-ancestor $ref HEAD 2>&1 | Out-Null
  $isAnc = ($LASTEXITCODE -eq 0)
  $rngAll  = "HEAD..$ref"
  $rngNoM  = "HEAD..$ref"
  $uniqAll = @(G @('rev-list','--count',$rngAll))
  $uniq    = @(G @('rev-list','--no-merges','--count',$rngNoM))
  "{0,-40} ancestor_of_integration={1,-6} commits_ahead={2} (non-merge={3})" -f $b, $isAnc, ($uniqAll -join ''), ($uniq -join '')
}

foreach ($b in $branches) {
  $ref = "origin/$b"
  ""
  "################ $b ################"
  "--- unique commits not yet in integration (HEAD..branch) ---"
  @(G @('log','--oneline','--no-merges',"HEAD..$ref")) | Select-Object -First 40
  ""
  "--- unique commits not in testing ---"
  @(G @('log','--oneline','--no-merges',"origin/testing..$ref")) | Select-Object -First 40
  ""
  $base = (G @('merge-base','HEAD',$ref)) | Select-Object -First 1
  "--- MERGE-BASE(integration, branch) = $base ---"
  "--- diffstat branch vs that base ---"
  @(G @('diff','--stat',"$base..$ref")) | Select-Object -Last 25
  ""
  "--- OVERLAP: files changed on both sides since that base ---"
  $cur = @(G @('diff','--name-only',"$base..HEAD")) | Where-Object { $_ }
  $inc = @(G @('diff','--name-only',"$base..$ref")) | Where-Object { $_ }
  $ov  = @($cur | Where-Object { $inc -contains $_ })
  "ours_changed={0} incoming_changed={1} overlap={2}" -f $cur.Count, $inc.Count, $ov.Count
  $ov | Set-Content -Encoding UTF8 (Join-Path $out ("overlap-" + ($b -replace '/','_') + ".txt"))
  $ov
}
