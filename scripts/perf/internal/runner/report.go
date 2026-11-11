package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sagernet/sing-box/scripts/perf/internal/schema"
)

func writeRun(runDir string, run *schema.Run) error {
	raw, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runDir, "run.json"), append(raw, '\n'), 0o644)
}

// writeSummaryMD renders the human view. It deliberately shows the spread next
// to the median: a single number from three repetitions is not a result, it is
// a sample.
func writeSummaryMD(runDir string, run *schema.Run) error {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s - %s\n\n", run.Scenario.ID, run.Scenario.Protocol)
	fmt.Fprintf(&builder, "- run_id: `%s`\n- kind: %s\n- started: %s\n", run.RunID, run.Kind, run.StartedAt.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Fprintf(&builder, "- commit: `%s` (%s)\n- branch: `%s`\n", shortCommit(run.Source.Commit), run.Source.CommitSubject, run.Source.Branch)
	if run.Source.Dirty != nil && *run.Source.Dirty {
		fmt.Fprintf(&builder, "- **dirty worktree**: %d file(s)\n", len(run.Source.DirtyFiles))
	}
	fmt.Fprintf(&builder, "- go: %s\n", run.Source.GoVersion)
	if len(run.Source.BuildTags) > 0 {
		fmt.Fprintf(&builder, "- tags: `%s`\n", strings.Join(run.Source.BuildTags, ","))
	}
	fmt.Fprintf(&builder, "- host: %s/%s %s %s, %d cores, %s\n", run.Host.OS, run.Host.Arch, run.Host.Distro, run.Host.DistroVersion, run.Host.CPUCores, run.Host.CPUModel)
	network := run.Network
	fmt.Fprintf(&builder, "- network: %s", network.Topology)
	if network.Profile != "" {
		fmt.Fprintf(&builder, " profile=%s", network.Profile)
	}
	if network.DelayMS != nil {
		fmt.Fprintf(&builder, " delay=%.1fms", *network.DelayMS)
	}
	if network.LossPercent != nil {
		fmt.Fprintf(&builder, " loss=%.2f%%", *network.LossPercent)
	}
	if network.BandwidthMbps != nil {
		fmt.Fprintf(&builder, " bandwidth=%.0fMbps", *network.BandwidthMbps)
	}
	if network.MTU != nil {
		fmt.Fprintf(&builder, " mtu=%d", *network.MTU)
	}
	builder.WriteString("\n\n")

	builder.WriteString("## Summary (median +/- MAD over repetitions)\n\n")
	builder.WriteString("| metric | streams | pattern | n | median | MAD | min | max | p95 | unit |\n")
	builder.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")
	for _, row := range run.Summary {
		fmt.Fprintf(&builder, "| %s | %d | %s | %d | %s | %s | %s | %s | %s | %s |\n",
			row.Metric, row.Streams, row.Pattern, row.N,
			formatFloat(row.Median, 2), formatFloat(row.MAD, 2), formatFloat(row.Min, 2), formatFloat(row.Max, 2), formatFloat(row.P95, 2), row.Unit)
	}

	builder.WriteString("\n## Repetitions\n\n")
	builder.WriteString("| rep | streams | pattern | payload MiB | seconds | goodput MiB/s | CPU s/GiB client | CPU s/GiB server | wire Gbit/s | overhead | p50 ms | p95 ms | p99 ms |\n")
	builder.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, rep := range run.Reps {
		fmt.Fprintf(&builder, "| %d | %d | %s | %.1f | %.3f | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			rep.Index, rep.Streams, rep.Pattern,
			float64(rep.PayloadBytes)/(1<<20), rep.DurationSeconds,
			formatFloat(rep.GoodputMiBPerS, 1),
			formatFloat(procField(rep.Client, func(p *schema.Proc) *float64 { return p.CPUSecondsPerGiB }), 2),
			formatFloat(procField(rep.Server, func(p *schema.Proc) *float64 { return p.CPUSecondsPerGiB }), 2),
			formatFloat(wireField(rep.Wire, func(w *schema.Wire) *float64 { return w.GbitPerSecond }), 2),
			formatFloat(wireField(rep.Wire, func(w *schema.Wire) *float64 { return w.OverheadRatio }), 2),
			formatFloat(latencyField(rep.Latency, func(l *schema.Latency) *float64 { return l.P50MS }), 2),
			formatFloat(latencyField(rep.Latency, func(l *schema.Latency) *float64 { return l.P95MS }), 2),
			formatFloat(latencyField(rep.Latency, func(l *schema.Latency) *float64 { return l.P99MS }), 2))
	}

	builder.WriteString("\n## Resources\n\n")
	builder.WriteString("| rep | client CPU s | client RSS peak MiB | client threads | client FDs | server CPU s | server RSS peak MiB | server threads | server FDs |\n")
	builder.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for _, rep := range run.Reps {
		fmt.Fprintf(&builder, "| %d | %s | %s | %s | %s | %s | %s | %s | %s |\n", rep.Index,
			formatFloat(procField(rep.Client, func(p *schema.Proc) *float64 { return p.CPUSeconds }), 3),
			formatBytesMiB(procField(rep.Client, func(p *schema.Proc) *int64 { return p.RSSPeakBytes })),
			formatInt(procField(rep.Client, func(p *schema.Proc) *int { return p.ThreadsPeak })),
			formatInt(procField(rep.Client, func(p *schema.Proc) *int { return p.FDsPeak })),
			formatFloat(procField(rep.Server, func(p *schema.Proc) *float64 { return p.CPUSeconds }), 3),
			formatBytesMiB(procField(rep.Server, func(p *schema.Proc) *int64 { return p.RSSPeakBytes })),
			formatInt(procField(rep.Server, func(p *schema.Proc) *int { return p.ThreadsPeak })),
			formatInt(procField(rep.Server, func(p *schema.Proc) *int { return p.FDsPeak })))
	}

	if len(run.Failures) > 0 {
		builder.WriteString("\n## Failures\n\n")
		for _, failure := range run.Failures {
			fmt.Fprintf(&builder, "- **%s** (%s): %s\n", failure.Stage, failure.Class, failure.Message)
		}
	}
	if len(run.Notes) > 0 {
		builder.WriteString("\n## Notes\n\n")
		for _, note := range run.Notes {
			fmt.Fprintf(&builder, "- %s\n", note)
		}
	}
	builder.WriteString("\n")
	text := strings.TrimRight(builder.String(), "\n") + "\n"
	return os.WriteFile(filepath.Join(runDir, "summary.md"), []byte(text), 0o644)
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func formatFloat(value *float64, digits int) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%.*f", digits, *value)
}

func formatInt(value *int) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%d", *value)
}

func formatBytesMiB(value *int64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%.1f", float64(*value)/(1<<20))
}

func procField[T any](proc *schema.Proc, pick func(*schema.Proc) *T) *T {
	if proc == nil {
		return nil
	}
	return pick(proc)
}

func wireField(wire *schema.Wire, pick func(*schema.Wire) *float64) *float64 {
	if wire == nil {
		return nil
	}
	return pick(wire)
}

func latencyField(latency *schema.Latency, pick func(*schema.Latency) *float64) *float64 {
	if latency == nil {
		return nil
	}
	return pick(latency)
}
