//go:build badlinkname && tfogo_checklinkname0

package runtimeinfo

// The goroutine report is the other place this package mirrors a runtime structure: it walks
// runtime.allgs and reads runtime.g fields at hand-computed offsets, then DISCOVERS the two offsets it
// could not pin (gopc, startpc) by probing for function names.
//
// That design already degrades safely - if the probe does not verify, collectGoroutines returns nil
// and the JSON field is omitted instead of carrying garbage. What it cannot do is tell anybody that
// it degraded: a build whose runtime.g moved would ship a runtimeinfo.json with no goroutine section
// and nothing would fail. This test is the missing signal. It is the sibling of
// pool_badlinkname_test.go, and it exists for the same reason.
//
// It is gated on the same two tags as the implementation: badlinkname permits the linkname, and
// tfogo_checklinkname0 records that the build really passes -checklinkname=0 (the linker rejects a
// pull of runtime.allgs under the default policy).

import (
	"runtime"
	"testing"
)

func TestGoroutineReportReachesTheRuntimeLayout(t *testing.T) {
	report := Collect().Goroutines
	if report == nil {
		t.Fatal("the goroutine section is missing: the pinned runtime.g layout no longer verifies, so " +
			"runtimeinfo silently reports nothing about goroutines on every shipped mobile build")
	}
	if report.Total == 0 {
		t.Fatal("the goroutine report is empty while this test is running")
	}
	if len(report.ByFunction) == 0 {
		t.Fatal("the goroutine report has no per-function groups")
	}
	// NumGoroutine is sampled here rather than inside Collect, so it is a lower bound only: goroutines
	// come and go between the two observations.
	atLeast := runtime.NumGoroutine()
	if report.Total < atLeast {
		t.Fatalf("the report saw %d live goroutines, fewer than the %d NumGoroutine reports", report.Total, atLeast)
	}
	stacked := 0
	for _, group := range report.ByFunction {
		if group.Function == "" {
			t.Fatalf("a goroutine group has no function name: the startpc offset no longer points at a PC: %+v", group)
		}
		if group.Count <= 0 {
			t.Fatalf("a goroutine group has %d members: %+v", group.Count, group)
		}
		if group.StackBytes > 0 {
			stacked++
		}
	}
	if stacked == 0 {
		t.Fatal("no goroutine group reports stack bytes: the stack bounds are not where the mirror says")
	}
	if report.StackBytes == 0 {
		t.Fatal("the report totals zero stack bytes")
	}
}
