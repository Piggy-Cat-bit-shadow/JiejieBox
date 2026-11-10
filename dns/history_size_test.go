package dns

// The ring's byte accounting must charge EVERY retained field.
//
// The reviewed failure: a DNS rule description (`QueryEvent.Rule`) of 40,000 characters was not
// charged at all, so two records were accounted 154 B, a `maxBytes=4096` ring retained tens of
// kilobytes, and the read side sized its JSON output from that same wrong number (80,485 bytes for
// one query). These tests pin the fields and the bound, so a future field that is forgotten fails
// here instead of becoming a memory and output leak.

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func eventWithRule(name string, rule string, at time.Time) QueryEvent {
	event := historyEvent(name, "remote", QuerySourceBlocked, at)
	event.Rule = rule
	return event
}

func TestALongRuleIsChargedAndRejectedOverBudget(t *testing.T) {
	now := time.Now()
	longRule := "domain_keyword: [" + strings.Repeat("a", 40_000) + "]"

	bounded := NewHistory(HistoryOptions{MaxBytes: 4096})
	bounded.Record(eventWithRule("history.test.", longRule, now))
	stats := bounded.Stats()
	require.Zero(t, stats.Entries, "a record that cannot fit must not be retained")
	require.EqualValues(t, 1, stats.Rejected)
	require.Zero(t, stats.Bytes)

	// With room, the SAME record is charged its real size - which is what makes the budget above
	// meaningful.
	roomy := NewHistory(HistoryOptions{MaxBytes: 1 << 20})
	roomy.Record(eventWithRule("history.test.", longRule, now))
	stats = roomy.Stats()
	require.Equal(t, 1, stats.Entries)
	require.GreaterOrEqual(t, stats.Bytes, len(longRule),
		"the rule text is retained, so it must be charged")
}

// TestEveryRetainedVariableFieldIsCharged compares two otherwise identical records: the difference
// in the charged size must cover the field that changed. A field the accounting forgets shows up as
// a missing delta.
func TestEveryRetainedVariableFieldIsCharged(t *testing.T) {
	now := time.Now()
	base := historyEvent("example.com.", "remote", QuerySourceUpstream, now)

	charge := func(event QueryEvent) int {
		history := NewHistory(HistoryOptions{MaxBytes: 1 << 20})
		history.Record(event)
		return history.Stats().Bytes
	}

	baseCharge := charge(base)
	require.Positive(t, baseCharge)

	// Each case charges at least the number of bytes the field grew by.
	withRule := base
	withRule.Rule = strings.Repeat("r", 1000)
	require.GreaterOrEqual(t, charge(withRule)-baseCharge, 1000, "Rule must be charged")

	withName := base
	withName.Name = strings.Repeat("n", 1000) + "."
	require.GreaterOrEqual(t, charge(withName)-baseCharge, len(withName.Name)-len(base.Name), "Name must be charged")

	withErr := base
	withErr.Err = strings.Repeat("e", 1000)
	require.GreaterOrEqual(t, charge(withErr)-baseCharge, 1000, "Err must be charged")

	withTransport := base
	withTransport.TransportTag = strings.Repeat("t", 1000)
	require.GreaterOrEqual(t, charge(withTransport)-baseCharge, 1000-len(base.TransportTag), "TransportTag must be charged")

	withAnswers := base
	for index := 0; index < 8; index++ {
		withAnswers.Answers = append(withAnswers.Answers, strings.Repeat("s", 125))
	}
	require.GreaterOrEqual(t, charge(withAnswers)-baseCharge, 8*125, "Answers must be charged")
}

// TestASingleRecordCannotPushTheRingOverItsByteLimit fills the ring and checks that eviction keeps
// the accounting at or below the budget: the reviewed bug let the RETAINED bytes exceed it because
// the rule was invisible to the charge.
func TestASingleRecordCannotPushTheRingOverItsByteLimit(t *testing.T) {
	const budget = 4096
	history := NewHistory(HistoryOptions{MaxBytes: budget, MaxEntries: 256})
	now := time.Now()
	for index := 0; index < 16; index++ {
		history.Record(eventWithRule("q.example.", strings.Repeat("r", 900), now))
		stats := history.Stats()
		require.LessOrEqual(t, stats.Bytes, budget,
			"the retained accounting must never exceed the configured byte budget")
	}
	// The window is shorter than the input: the ring evicted what did not fit.
	require.Less(t, history.Stats().Entries, 16)
}
