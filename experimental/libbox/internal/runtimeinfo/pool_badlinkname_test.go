//go:build badlinkname

package runtimeinfo

import (
	"testing"

	"github.com/sagernet/sing/common/buf"
)

// The buffer-pool report mirrors sync.Pool's private layout instead of using it. That
// mirror cannot fail loudly on its own: when the layout moves, collectBufferPools returns
// nil, the JSON field is omitted, and the diagnostic quietly reports "no buffer pools"
// exactly as it would for a process that never pooled anything.
//
// This test makes the assumption observable. It is gated on badlinkname because that is
// the tag the mirror itself is built with, and it deliberately asserts no exact cached
// count: the pool may be drained by a GC that runs between the releases and the report.
// What it does assert is that the mirror produced a report at all and that the size class
// it says it reports is the one this test exercised, which is the part that breaks when
// sync.Pool changes shape.
func TestBufferPoolReportMatchesPoolLayout(t *testing.T) {
	buffers := make([]*buf.Buffer, 0, 4)
	for range 4 {
		buffers = append(buffers, buf.NewSize(2048))
	}
	for _, buffer := range buffers {
		buffer.Release()
	}

	report := Collect()
	if len(report.Pools) == 0 {
		t.Fatal("no buffer pools reported: the pinned toolchain's sync.Pool layout no longer matches pool_badlinkname.go, " +
			"so the buffer report silently reports nothing")
	}
	found := false
	cached := 0
	for _, pool := range report.Pools {
		cached += pool.Cached
		if pool.Size == 2048 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no 2048-byte size class in the buffer report: %+v", report.Pools)
	}
	t.Logf("pools=%d cached=%d", len(report.Pools), cached)
}
