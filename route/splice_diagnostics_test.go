package route

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Tests for the handover diagnostics.
//
// The point of these counters is to let a real-device run answer "how many UDP sessions
// were accepted for handover, how many fell back, and where". Two things therefore have to
// be true and are asserted here: the counters record what actually happened, and adding
// them did not change any handover decision.
//
// The third thing asserted here is the WORDING. Every number in this file describes a
// handover being accepted by sing-tun - the last thing this package can observe - and not a
// kernel splice that moved bytes. An earlier revision called the same counter "successes" and
// printed it under a heading that named the mechanism, which reads as a rate of working
// kernel splices. These tests exist so that wording cannot drift back: see
// TestSpliceSnapshot_SummaryDoesNotClaimKernelSpliceSuccess, which is a source-level guard
// because no runtime assertion on a rendered string can catch a hard-coded literal.

func TestSpliceDiagnostics_EmptySnapshot(t *testing.T) {
	var d spliceDiagnostics
	if got := d.total(); got != 0 {
		t.Fatalf("a fresh diagnostics set must be empty, got %d", got)
	}
	if snap := d.snapshot(); len(snap) != 0 {
		t.Fatalf("a fresh snapshot must be empty, got %v", snap)
	}
}

func TestSpliceDiagnostics_RecordsEachReason(t *testing.T) {
	var d spliceDiagnostics
	d.recordOutcome(spliceReasonHandoverAccepted)
	d.recordOutcome(spliceReasonHandoverAccepted)
	d.recordOutcome(spliceReasonSourceNotReplaceable)

	// Acceptance is reported through accepted(), NOT through the reason table. Listing it in
	// both places would double-count every accepted session and break the
	// Attempts == Accepted + sum(Reasons) invariant the device report relies on.
	if _, present := d.snapshot()["handover_accepted"]; present {
		t.Error("acceptance must not appear in the reason table; it has its own accessor")
	}
	if d.accepted() != 2 {
		t.Errorf("accepted: want 2, got %d", d.accepted())
	}
	if got := d.snapshot()["source_not_replaceable"]; got != 1 {
		t.Errorf("source_not_replaceable: want 1, got %d", got)
	}
	if d.total() != 3 {
		t.Errorf("total: want 3, got %d", d.total())
	}
}

func TestSpliceDiagnostics_ZeroReasonsAreOmitted(t *testing.T) {
	// A snapshot reports only what happened, so an absent key means "never seen"
	// rather than "seen zero times" - which keeps on-device output readable.
	var d spliceDiagnostics
	d.recordOutcome(spliceReasonTargetNoUpstream)
	snap := d.snapshot()
	if _, present := snap["handover_accepted"]; present {
		t.Error("a reason that never occurred must not appear in the snapshot")
	}
	if len(snap) != 1 {
		t.Errorf("want exactly one reported reason, got %d", len(snap))
	}
}

func TestSpliceDiagnostics_OutOfRangeReasonIsIgnored(t *testing.T) {
	// Bounds check: a future reason added without widening spliceReasonCount must
	// not write out of bounds.
	var d spliceDiagnostics
	d.recordOutcome(spliceReason(200))
	if d.total() != 0 {
		t.Fatalf("an out-of-range reason must not be recorded, total=%d", d.total())
	}
}

func TestSpliceReason_StringsAreDistinct(t *testing.T) {
	// Every reason must have its own name; two reasons sharing one string would make
	// an on-device report ambiguous.
	seen := make(map[string]spliceReason, spliceReasonCount)
	for i := 0; i < spliceReasonCount; i++ {
		r := spliceReason(i)
		name := r.String()
		if name == "unknown" {
			t.Errorf("reason %d has no name", i)
		}
		if prev, dup := seen[name]; dup {
			t.Errorf("reasons %d and %d share the name %q", prev, i, name)
		}
		seen[name] = r
	}
	if got := spliceReason(200).String(); got != "unknown" {
		t.Errorf("out-of-range reason: want %q, got %q", "unknown", got)
	}
}

func TestSpliceReason_AcceptedBucketIsNamedForTheHandover(t *testing.T) {
	// The bucket's NAME is the claim. A name that asserts the kernel moved the stream would
	// be wrong: the counter is incremented when sing-tun's Splice() returns true, which is
	// before anything can be said about what the kernel does next. The old name is asserted
	// absent rather than merely replaced, because a rename that leaves the old string
	// reachable (in a report, in a log line) is not a rename.
	require.Equal(t, "handover_accepted", spliceReasonHandoverAccepted.String())
	require.Equal(t, spliceReason(0), spliceReasonHandoverAccepted,
		"the accepted bucket must stay the first bucket: the numeric value is what a released build's report is indexed by")
}

func TestSpliceSnapshot_AcceptanceRatio(t *testing.T) {
	cases := []struct {
		name     string
		attempts uint64
		accepted uint64
		want     float64
	}{
		{"no attempts is zero rather than NaN", 0, 0, 0},
		{"all accepted", 10, 10, 1},
		{"none accepted", 10, 0, 0},
		{"half accepted", 10, 5, 0.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := SpliceSnapshot{Attempts: tc.attempts, Accepted: tc.accepted}
			if got := snap.AcceptanceRatio(); got != tc.want {
				t.Errorf("want %v, got %v", tc.want, got)
			}
		})
	}
}

func TestSameConn(t *testing.T) {
	// sameConn replaced a direct `any(a) != any(b)` comparison, which panics when a
	// dynamic type is not comparable. The panic would happen inside the splice
	// decision, so this guard matters.
	a := &socketConn{}
	b := &socketConn{}

	if !sameConn(a, a) {
		t.Error("an identical value must compare equal")
	}
	if sameConn(a, b) {
		t.Error("different pointers must not compare equal")
	}
	if !sameConn(nil, nil) {
		t.Error("two nils must compare equal")
	}
	if sameConn(a, nil) {
		t.Error("a value and nil must not compare equal")
	}
	// A slice is not comparable; the direct form would panic here.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("sameConn must not panic on an uncomparable type: %v", r)
			}
		}()
		if sameConn([]int{1}, []int{1}) {
			t.Error("distinct uncomparable values must not compare equal")
		}
	}()
}

// --- one outcome per session (§19) ----------------------------------------------

func TestSpliceDiagnostics_AttemptsInvariantHolds(t *testing.T) {
	// Attempts == Accepted + sum(Reasons), by construction. This is the property that
	// makes a device report self-consistent: if a session could write two counters, or
	// none, the totals would drift and a ratio computed from them would be meaningless.
	var d spliceDiagnostics
	d.recordOutcome(spliceReasonHandoverAccepted)
	d.recordOutcome(spliceReasonHandoverAccepted)
	d.recordOutcome(spliceReasonHandoverAccepted)
	d.recordOutcome(spliceReasonSourceNotNAT)
	d.recordOutcome(spliceReasonSpliceRejected)
	d.recordOutcome(spliceReasonTargetNoUpstream)

	snapshot := SpliceSnapshot{
		Attempts: d.total(),
		Accepted: d.accepted(),
		Reasons:  d.snapshot(),
	}

	require.EqualValues(t, 6, snapshot.Attempts)
	require.EqualValues(t, 3, snapshot.Accepted)

	var reasonSum uint64
	for _, count := range snapshot.Reasons {
		reasonSum += count
	}
	// Reasons excludes acceptance, so acceptance plus reasons is the whole population.
	require.EqualValues(t, snapshot.Attempts, snapshot.Accepted+reasonSum,
		"every session must contribute to exactly one bucket")

	// And acceptance must not appear in the reason table, or it would be counted twice.
	_, acceptedInReasons := snapshot.Reasons["handover_accepted"]
	require.False(t, acceptedInReasons, "acceptance must not also be listed as a reason")
}

func TestSpliceDiagnostics_OutcomeCountsOneBucketOnly(t *testing.T) {
	// A single session must move exactly one counter.
	var d spliceDiagnostics
	d.recordOutcome(spliceReasonTargetNotReplaceable)

	require.EqualValues(t, 1, d.total())
	require.EqualValues(t, 0, d.accepted())
	require.Len(t, d.snapshot(), 1)
}

// --- the device-readable summary (§20) ------------------------------------------

func TestSpliceSnapshot_SummaryFormatIsStable(t *testing.T) {
	snapshot := SpliceSnapshot{
		Attempts: 123,
		Accepted: 100,
		Reasons: map[string]uint64{
			"source_not_replaceable": 5,
			"splice_rejected":        18,
		},
	}

	got := snapshot.SpliceSummary()
	require.Equal(t,
		"UDP handover diagnostics: attempts=123 accepted=100 acceptance_ratio=0.813 source_not_replaceable=5 splice_rejected=18",
		got)
}

func TestSpliceSnapshot_SummaryReasonOrderIsDeterministic(t *testing.T) {
	// The reason order must come from the enum, not from map iteration. A rotating order
	// would make two device logs hard to compare by eye or by diff, which is the only
	// thing this line is for.
	snapshot := SpliceSnapshot{
		Attempts: 3,
		Accepted: 0,
		Reasons: map[string]uint64{
			"source_not_nat":     1,
			"target_no_upstream": 1,
			"splice_rejected":    1,
		},
	}

	first := snapshot.SpliceSummary()
	for i := 0; i < 50; i++ {
		require.Equal(t, first, snapshot.SpliceSummary(),
			"the summary must render identically every time")
	}
}

func TestSpliceSnapshot_SummaryOmitsZeroReasons(t *testing.T) {
	snapshot := SpliceSnapshot{Attempts: 2, Accepted: 2, Reasons: map[string]uint64{}}
	require.Equal(t,
		"UDP handover diagnostics: attempts=2 accepted=2 acceptance_ratio=1.000",
		snapshot.SpliceSummary())
}

// --- the summary must not claim a kernel splice (§5) ----------------------------

// TestSpliceSnapshot_SummaryDoesNotClaimKernelSpliceSuccess is the control for the wording
// itself, and it has to read the SOURCE rather than a rendered string.
//
// A runtime assertion cannot catch this class of regression: if someone rewrites the format
// and the expected literal changes with it, both the code and the expectation move together
// and the test stays green while the report starts lying again. What cannot move silently is
// the set of words in the producer.
//
// The mutation this is designed to catch, and which was run by hand: restore the old heading
// (the one that named the mechanism), count "successes" instead of "accepted", and print the
// ratio without naming it acceptance. With that in place this test fails on four separate
// assertions and the format test above fails on its literal. Either one alone would be a
// decoration; together they make the wording load-bearing.
func TestSpliceSnapshot_SummaryDoesNotClaimKernelSpliceSuccess(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("splice_diagnostics.go"))
	require.NoError(t, err, "the summary producer must be readable: this guard reads it, it does not guess")

	body := string(source)

	// The words that make the claim honest.
	require.Contains(t, body, "handover diagnostics",
		"the summary must name what it measures: a handover, not a splice")
	require.Contains(t, body, "accepted=%d",
		"the count must be reported as accepted, not as successes")
	require.Contains(t, body, "acceptance_ratio=%.3f",
		"the ratio must be named for what it is: acceptance")

	// The words that would make it dishonest. The list is written as fragments so that this
	// guard's own text is not a match for it - the file it reads is the producer, and a
	// comment here must never be able to fail the producer's check.
	for _, forbidden := range []string{
		"successes=",
		"splice diag" + "nostics",
		"successful spli" + "ce",
		"splice suc" + "cess",
		"splice ra" + "tio",
		"spliced=%d",
	} {
		require.NotContains(t, body, forbidden,
			"the diagnostics must not claim a kernel splice succeeded; %q reintroduces that claim", forbidden)
	}

	// And the accessor names must not claim it either.
	require.NotContains(t, body, "Successes",
		"the exported field is read by device reports; it must say accepted")
	require.NotContains(t, body, "func (s SpliceSnapshot) Ratio()",
		"an unqualified Ratio() on this snapshot reads as a splice rate")
}
