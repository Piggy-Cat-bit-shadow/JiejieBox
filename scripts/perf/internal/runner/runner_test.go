package runner

import (
	"testing"
	"time"
)

func TestRenderFileRejectsUnresolvedPlaceholders(t *testing.T) {
	vars := templateVars{"PORT": "8080"}
	rendered := vars.render(`{"port": __PORT__, "other": __MISSING__}`)
	missing := findPlaceholders(rendered)
	if len(missing) != 1 || missing[0] != "MISSING" {
		t.Fatalf("findPlaceholders = %v, want [MISSING]", missing)
	}
}

func TestRenderFileSubstitutesEveryOccurrence(t *testing.T) {
	vars := templateVars{"NAME": "bench"}
	got := vars.render("__NAME__ and __NAME__")
	if got != "bench and bench" {
		t.Fatalf("render = %q", got)
	}
}

func TestFindPlaceholdersIgnoresProse(t *testing.T) {
	// A double underscore inside a sentence is not a placeholder; only the
	// __NAME__ form with no whitespace or JSON punctuation counts.
	if missing := findPlaceholders(`{"note": "a __ b __ c"}`); len(missing) != 0 {
		t.Fatalf("findPlaceholders = %v, want none", missing)
	}
	if missing := findPlaceholders("__OK__"); len(missing) != 1 || missing[0] != "OK" {
		t.Fatalf("findPlaceholders = %v, want [OK]", missing)
	}
}

func TestParseDurationFallsBackOnGarbage(t *testing.T) {
	if got := parseDuration("", 5*time.Second); got != 5*time.Second {
		t.Fatalf("empty duration = %v, want fallback", got)
	}
	if got := parseDuration("not-a-duration", 3*time.Second); got != 3*time.Second {
		t.Fatalf("garbage duration = %v, want fallback", got)
	}
	if got := parseDuration("2s", 9*time.Second); got != 2*time.Second {
		t.Fatalf("valid duration = %v", got)
	}
}

func TestSummaryStatistics(t *testing.T) {
	values := []float64{10, 12, 11, 9, 10, 10, 30}
	row := statRow("goodput", "MiB/s", 4, "upload", values)
	if row.N != len(values) {
		t.Fatalf("N = %d", row.N)
	}
	if row.Median == nil || *row.Median != 10 {
		t.Fatalf("median = %v, want 10", row.Median)
	}
	// Deviations from 10 are 0,2,1,1,0,0,20; their median is 1.
	if row.MAD == nil || *row.MAD != 1 {
		t.Fatalf("MAD = %v, want 1", row.MAD)
	}
	if row.Min == nil || *row.Min != 9 || row.Max == nil || *row.Max != 30 {
		t.Fatalf("min/max = %v/%v", row.Min, row.Max)
	}
	// Seven samples cannot justify a P95; a percentile there would just be the
	// maximum wearing a statistical label.
	if row.P95 != nil {
		t.Fatalf("P95 must be absent for n=7")
	}
}

func TestPercentileNeedsEnoughSamples(t *testing.T) {
	many := make([]float64, 100)
	for i := range many {
		many[i] = float64(i)
	}
	row := statRow("latency", "ms", 1, "connect", many)
	// Nearest-rank: the 95th of 0..99 is 94, the 99th is 98.
	if row.P95 == nil || *row.P95 != 94 {
		t.Fatalf("P95 = %v, want 94", row.P95)
	}
	if row.P99 == nil || *row.P99 != 98 {
		t.Fatalf("P99 = %v, want 98", row.P99)
	}
}
