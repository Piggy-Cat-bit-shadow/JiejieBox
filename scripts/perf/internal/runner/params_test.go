package runner

import (
	"testing"
	"time"
)

// TestLoadParametersUseResolvedValues pins the bug the review found: with all
// CLI parameters omitted the record must show the scenario-resolved duration,
// streams, reps and warmup, not the zero fields of Options.
func TestLoadParametersUseResolvedValues(t *testing.T) {
	scenario := &Scenario{ID: "unit"}
	opts := Options{} // everything omitted
	got := loadParameters(opts, scenario, []string{"udp"}, []int{4}, 7*time.Second, 2*time.Second, 5)
	if got["duration_s"] != 7.0 || got["warmup_s"] != 2.0 || got["reps"] != 5 {
		t.Fatalf("resolved values missing: %#v", got)
	}
	if streams, ok := got["streams"].([]int); !ok || len(streams) != 1 || streams[0] != 4 {
		t.Fatalf("streams = %#v", got["streams"])
	}
	if got["udp_drain_ms"] != 0.0 || got["udp_packet_size"] != 1200 {
		t.Fatalf("udp defaults = %#v", got)
	}
	// A paced run resolves an unspecified drain to the loader default.
	opts.UDPRate = 500
	got = loadParameters(opts, scenario, []string{"udp"}, []int{1}, time.Second, time.Second, 1)
	if got["udp_drain_ms"] != 500.0 {
		t.Fatalf("paced drain default = %v, want 500", got["udp_drain_ms"])
	}
}
