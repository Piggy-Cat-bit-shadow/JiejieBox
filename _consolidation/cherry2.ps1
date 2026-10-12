$ErrorActionPreference = 'Continue'
$git  = "C:\src\MinGit\cmd\git.exe"
$repo = "C:\Deepseek\singbox-consolidation"
Set-Location $repo
function G { param([string[]]$a) & $git @a 2>&1 }

$branches = @('fix/0.1.6-apple-pin-gates','jiejiebox/apple-finalize')

foreach ($b in $branches) {
  $ref = "origin/$b"
  ""
  "################ $b ################"

  "### git cherry -v (integration -> branch):  '+' = unique patch, '-' = patch-equivalent already present"
  $ch = @(G @('cherry','-v','HEAD',$ref))
  $plus  = @($ch | Where-Object { $_ -match '^\+' })
  $minus = @($ch | Where-Object { $_ -match '^-' })
  "TOTAL={0}  PLUS(unique)={1}  MINUS(equiv)={2}" -f $ch.Count, $plus.Count, $minus.Count
  "--- MINUS (already patch-equivalent in the integration tree) ---"
  $minus | Select-Object -First 60

  "### commits unique to the branch, in DAG order (HEAD..branch, merges included)"
  @(G @('log','--oneline','--left-right','--graph','--boundary',"HEAD...$ref")) | Select-Object -First 5
}
