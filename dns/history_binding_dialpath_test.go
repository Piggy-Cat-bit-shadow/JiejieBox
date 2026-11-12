package dns

// The history's management surface is where a DNS answer is actually examined after the fact, so the
// path projection has to reach it, and it has to reach it BOUNDED: this surface has its own output
// budget, and a recorded tag is user configuration that can be long.

import (
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

func TestHistoryRecordViewCarriesTheDialPath(t *testing.T) {
	t.Parallel()

	event := QueryEvent{
		Time: time.Unix(0, 0),
		Name: "example.org.",
		DialPath: &DialPathView{
			Steps:         []string{"proxy", "node-a"},
			Leaf:          "node-a",
			Revision:      3,
			RevisionKnown: true,
		},
	}
	presented, cut := PresentHistoryRecord(HistoryRecord{Sequence: 1, Event: event})
	require.False(t, cut)
	require.NotNil(t, presented.DialPath, "the management surface must present the path, not drop it")
	require.Equal(t, []string{"proxy", "node-a"}, presented.DialPath.Steps)
	require.Equal(t, "node-a", presented.DialPath.Leaf)
	require.Equal(t, uint64(3), presented.DialPath.Revision)
	require.True(t, presented.DialPath.RevisionKnown)

	// The reason vocabulary must survive presentation unchanged: a consumer reads it as a fixed set.
	event.DialPath = &DialPathView{Reason: string(adapter.DialPathReasonNoConnection)}
	presented, _ = PresentHistoryRecord(HistoryRecord{Sequence: 2, Event: event})
	require.Equal(t, string(adapter.DialPathReasonNoConnection), presented.DialPath.Reason)
}

func TestHistoryRecordViewBoundsTheDialPath(t *testing.T) {
	t.Parallel()

	longTag := strings.Repeat("x", MaxHistoryFieldBytes*2)
	steps := make([]string, 0, MaxHistoryDialPathSteps+4)
	for index := 0; index < MaxHistoryDialPathSteps+4; index++ {
		steps = append(steps, longTag)
	}
	event := QueryEvent{
		Time: time.Unix(0, 0),
		Name: "example.org.",
		DialPath: &DialPathView{
			Steps:  steps,
			Leaf:   longTag,
			Reason: string(adapter.DialPathReasonNoConnection),
		},
	}
	presented, cut := PresentHistoryRecord(HistoryRecord{Sequence: 1, Event: event})
	require.True(t, cut, "a record that had to be bounded must say so")
	require.Len(t, presented.DialPath.Steps, MaxHistoryDialPathSteps)
	for _, step := range presented.DialPath.Steps {
		require.LessOrEqual(t, len(step), MaxHistoryFieldBytes)
	}
	require.LessOrEqual(t, len(presented.DialPath.Leaf), MaxHistoryFieldBytes)
}
