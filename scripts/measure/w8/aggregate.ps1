# W8 aggregation: rows.jsonl + runs.csv -> per-point/per-side medians, extremes and the
# per-sample exit codes. It does not interpret: it emits numbers, and the report makes the
# claims.
#
#   .\aggregate.ps1 -Out C:\...\04-evidence\logs\w8
#
# Outputs (in -Out):
#   aggregate.csv     one row per point x side x field: samples, median, min, max
#   aggregate.json    the same as JSON, for the summary writer
#   sample-count.csv  point x side -> number of rows, number of exits, distinct SHAs

param(
    [Parameter(Mandatory = $true)][string]$Out
)

$ErrorActionPreference = 'Stop'
$rowsPath = Join-Path $Out 'rows.jsonl'
$runsPath = Join-Path $Out 'runs.csv'
if (-not (Test-Path $rowsPath)) { throw "no rows at $rowsPath" }

$rows = Get-Content $rowsPath | ForEach-Object { $_ | ConvertFrom-Json }
$runs = @()
if (Test-Path $runsPath) { $runs = Import-Csv $runsPath }

# Fields that are measured quantities rather than identity. Only these are aggregated.
$numericFields = @(
    'payload_bytes', 'cold_ms', 'cold_heap_peak_alloc', 'cold_heap_peak_objects', 'cold_heap_after_alloc',
    'cold_heap_after_objects', 'cold_total_alloc', 'cold_rss_delta', 'cold_peak_rss', 'cold_read_ops', 'cold_write_ops',
    'update_ms', 'update_apply_ms', 'update_server_hits', 'update_heap_peak_alloc', 'update_heap_peak_objects',
    'update_heap_after_alloc', 'update_heap_after_objects', 'update_total_alloc', 'update_rss_delta', 'update_peak_rss',
    'update_read_ops', 'update_write_ops', 'observer_polls', 'observer_temp_dirs_seen', 'observer_partial_artifacts',
    'recovery_hits', 'recovered_heap_alloc', 'recovered_heap_objects', 'recovered_peak_rss',
    'publish_ms', 'publish_heap_alloc', 'publish_heap_objects', 'publish_peak_rss', 'publish_read_ops', 'publish_write_ops',
    'repair_ms', 'repair_heap_alloc', 'repair_heap_objects', 'repair_peak_rss',
    'quiet_in_use', 'quiet_granted', 'quiet_peak', 'quiet_refused', 'quiet_reserved_in_use', 'quiet_geometry_needed',
    'bulk_after_in_use', 'bulk_after_granted', 'bulk_after_released', 'bulk_after_refused', 'bulk_after_peak',
    'bulk_sampled_peak_in_use', 'bulk_sampled_peak_reserved', 'bulk_sampled_peak_refused',
    'bulk_upload_reads', 'bulk_download_reads', 'bulk_expanded_directions',
    'bulk_throughput_mib_s', 'bulk_throughput_low', 'bulk_throughput_high', 'bulk_elapsed_ms_median',
    'bulk_heap_alloc', 'bulk_heap_objects', 'bulk_total_alloc', 'bulk_peak_rss', 'bulk_proc_read_ops', 'bulk_proc_write_ops',
    'latency_p95_us', 'latency_p99_us', 'latency_max_us', 'latency_samples', 'quiet_expanded_directions',
    'bulk_to_quiet_drain_ms', 'final_in_use', 'final_granted', 'final_released', 'final_refused', 'final_peak',
    'final_double_releases', 'final_heap_alloc', 'final_heap_objects', 'final_rss', 'final_peak_rss', 'final_goroutines',
    'after_close_in_use', 'after_close_granted', 'after_close_released', 'after_close_heap_alloc', 'after_close_heap_objects',
    'after_close_rss', 'after_close_peak_rss', 'after_close_goroutines',
    'armed_in_use', 'armed_filled_in_use', 'armed_filled_granted', 'armed_filled_peak', 'armed_filled_refused',
    'interactive_in_use', 'interactive_reserved_in_use', 'interactive_granted', 'interactive_expanded',
    'critical_in_use', 'critical_granted', 'critical_refused', 'critical_expanded', 'critical_in_use_unchanged',
    'recovered_in_use', 'recovered_granted', 'recovered_expanded', 'recovered_reserve',
    'geometry_in_use', 'geometry_geometry_needed', 'geometry_expanded', 'ordinary_expanded',
    'cycles', 'nodes', 'elapsed_ms', 'build_failures', 'update_failures', 'cycles_per_second', 'updates_per_second',
    'heap_objects_first', 'heap_objects_last', 'heap_objects_growth', 'heap_alloc_growth', 'heap_objects_per_cycle',
    'rss_first', 'rss_last', 'rss_growth', 'handles_first', 'handles_last', 'handles_growth',
    'goroutines_first', 'goroutines_last', 'goroutines_growth', 'outbounds_last', 'document_bytes'
)

# Flatten the nested per-phase stat maps (quiet_stats/armed_filled/... ) into prefixed
# fields so the aggregation is uniform.
function Flatten-Row($row) {
    $flat = @{}
    foreach ($property in $row.PSObject.Properties) {
        $name = $property.Name
        $value = $property.Value
        if ($null -eq $value) { continue }
        if ($value -is [System.Management.Automation.PSCustomObject]) {
            foreach ($inner in $value.PSObject.Properties) {
                $flat[('{0}_{1}' -f $name, $inner.Name)] = $inner.Value
            }
        }
        else {
            $flat[$name] = $value
        }
    }
    return $flat
}

$flatRows = $rows | ForEach-Object { [pscustomobject](Flatten-Row $_) }
$groups = $flatRows | Group-Object -Property point, w8_side
$aggregate = @()
foreach ($group in $groups) {
    $parts = $group.Name -split ', '
    $point = $parts[0]
    $side = $parts[1]
    $first = $group.Group[0]
    foreach ($field in $numericFields) {
        $values = @()
        foreach ($row in $group.Group) {
            $property = $row.PSObject.Properties[$field]
            if ($null -eq $property -or $null -eq $property.Value) { continue }
            $number = 0.0
            if ([double]::TryParse([string]$property.Value, [ref]$number)) { $values += $number }
        }
        if ($values.Count -eq 0) { continue }
        $sorted = $values | Sort-Object
        $aggregate += [pscustomobject]@{
            point       = $point
            side        = $side
            field       = $field
            samples     = $values.Count
            median      = $sorted[[int][math]::Floor(($sorted.Count - 1) / 2)]
            min         = $sorted[0]
            max         = $sorted[$sorted.Count - 1]
            sha         = $first.w8_sha
            tree        = $first.w8_tree
            tags        = $first.w8_tags
            low_memory  = $first.low_memory
        }
    }
}
$aggregate | Sort-Object point, field, side | Export-Csv -Path (Join-Path $Out 'aggregate.csv') -NoTypeInformation -Encoding utf8
$aggregate | Sort-Object point, field, side | ConvertTo-Json -Depth 4 | Set-Content -Path (Join-Path $Out 'aggregate.json') -Encoding utf8

$counts = $groups | ForEach-Object {
    $parts = $_.Name -split ', '
    [pscustomobject]@{
        point       = $parts[0]
        side        = $parts[1]
        rows        = $_.Count
        failed_rows = @($_.Group | Where-Object { $_.failed -eq $true }).Count
        shas        = (@($_.Group | ForEach-Object { $_.w8_sha } | Sort-Object -Unique) -join ';')
        low_memory  = (@($_.Group | ForEach-Object { $_.low_memory } | Sort-Object -Unique) -join ';')
    }
}
$counts | Sort-Object point, side | Export-Csv -Path (Join-Path $Out 'sample-count.csv') -NoTypeInformation -Encoding utf8

$exitSummary = $runs | Group-Object -Property point, side | ForEach-Object {
    $parts = $_.Name -split ', '
    [pscustomobject]@{
        point      = $parts[0]
        side       = $parts[1]
        processes  = $_.Count
        non_zero   = @($_.Group | Where-Object { [int]$_.exit_code -ne 0 }).Count
        shas       = (@($_.Group | ForEach-Object { $_.sha } | Sort-Object -Unique) -join ';')
        trees      = (@($_.Group | ForEach-Object { $_.tree } | Sort-Object -Unique) -join ';')
    }
}
$exitSummary | Sort-Object point, side | Export-Csv -Path (Join-Path $Out 'exit-codes.csv') -NoTypeInformation -Encoding utf8

Write-Host ('aggregate: {0} rows, {1} point/side groups -> {2}' -f $rows.Count, $groups.Count, $Out)
