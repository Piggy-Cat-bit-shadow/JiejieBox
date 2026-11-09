//go:build !race

package group

// raceEnabled reports whether this test binary was built with the race detector. The 10,000-node
// scaled case is skipped under -race: instrumenting it measures the race runtime, not the budget.
const raceEnabled = false
