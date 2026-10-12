$ErrorActionPreference = 'Continue'
$git  = "C:\src\MinGit\cmd\git.exe"
$repo = "C:\Deepseek\singbox-consolidation"
$out  = "$repo\_consolidation"
Set-Location $repo

function G { param([string[]]$a) & $git @a 2>&1 }

"=== 1. take HEAD (testing) side: comment-only conflicts ==="
$ours = @(
  'common/runtimecoord/platform_events.go',
  'common/runtimecoord/platform_events_test.go',
  'common/sniff/quic_boundary_test.go',
  'route/splice_diagnostics.go',
  'route/splice_diagnostics_test.go',
  'route/tcp_splice_diagnostics_test.go'
)
foreach ($f in $ours) {
  & $git checkout --ours -- $f 2>&1 | Out-Null
  & $git add -- $f 2>&1 | Out-Null
  "  ours  <- $f"
}

"=== 2. take INCOMING side for the apple build script (coherent $framework design) ==="
& $git checkout --theirs -- scripts/ci/build-apple-libbox.sh 2>&1 | Out-Null
"  theirs<- scripts/ci/build-apple-libbox.sh"

"=== 3. union the checksum files (regex-merge both sides) ==="
foreach ($f in @('go.sum','test/go.sum')) {
  $st = @(G @('status','--short','--',$f))
  $ls = @(G @('ls-files','-u','--',$f))
  $stage = ''
  $lines = @()
  foreach ($entry in $ls) {
    # format: <mode> <sha> <stage>\t<path>
    if ($entry -match '^(\d+)\s+([0-9a-f]+)\s+(\d)\s+(.+)$') {
      $s = $Matches[3]
      $blob = @(G @('cat-file','blob',$Matches[2]))
      if ($s -eq '2') { $stage = 'ours'; $oursLines = $blob }
      if ($s -eq '3') { $incLines   = $blob }
    }
  }
  $merged = @($oursLines + $incLines | Where-Object { $_ -match '^[^ ]+ v[^ ]+ (h1:|/go.mod h1:)' } | Sort-Object -Unique)
  $merged | Set-Content -Encoding ASCII (Join-Path $repo $f)
  & $git add -- $f 2>&1 | Out-Null
  "  union <- $f  (ours=$($oursLines.Count) incoming=$($incLines.Count) merged=$($merged.Count))"
}

"=== 4. append HEAD's unique root-artifact cleanup into the apple script ==="
$ap = Join-Path $repo 'scripts/ci/build-apple-libbox.sh'
$content = Get-Content -LiteralPath $ap
$idx = ($content | Select-String -Pattern '^rm -rf "\$framework"$' | Select-Object -First 1).LineNumber
if ($idx) {
  $before = $content[0..($idx-1)]
  $after  = if ($idx -lt $content.Count) { $content[$idx..($content.Count-1)] } else { @() }
  $insert = @(
    '',
    '# Also drop any framework left at the repository root. Nothing writes there any more, so a',
    '# stale directory would only ever be one an earlier revision of this script produced - and',
    '# leaving it behind is exactly how a build failure gets mistaken for a successful build.'
  )
  $new = @($before + $insert + $after)
  $new | Set-Content -Encoding UTF8 $ap
  "  inserted root-cleanup comment after line $idx"
} else {
  "  WARNING: marker 'rm -rf `"`$framework`"' not found"
}

"=== 5. force the authoritative Apple gitlink (90a0e9ae) ==="
& $git update-index --cacheinfo 160000,90a0e9aef4c03df6cbc4cf170fe4bdef50bf1e07,clients/apple 2>&1
"  gitlink set"

"=== 6. remaining unmerged paths (must be empty) ==="
$unmerged = @(G @('diff','--name-only','--diff-filter=U'))
if ($unmerged.Count -eq 0) { "  (none)" } else { $unmerged }

"=== 7. leftover conflict markers in the tree ==="
$markers = @(& $git grep -n -E '^(<<<<<<<|>>>>>>>) ' -- . 2>&1)
if ($markers.Count -eq 0) { "  (none)" } else { $markers | Select-Object -First 20 }

"=== 8. git diff --cached --check ==="
& $git diff --cached --check 2>&1 | Select-Object -First 30
"  EXIT=$LASTEXITCODE"

"=== 9. status summary ==="
$st = @(G @('status','--short'))
"M  entries = " + @($st | Where-Object { $_ -match '^M' }).Count
"??  entries = " + @($st | Where-Object { $_ -match '^\?\?' }).Count
@($st | Where-Object { $_ -match '^\?\?' })
"=== clients/apple in index ==="
@(& $git ls-files -s clients/apple)
