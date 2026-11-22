package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
)

// The budget is this round's own number, derived from this round's own baselines.
//
// # Why there is no constant from another project here
//
// A 768 KiB idle budget is a real, published number for a different kernel with different nodes,
// a different relay and a different baseline point. Importing it would produce a gate that either
// passes without meaning anything or fails for a reason that is not a regression. Every budget below
// is written by deriveBudget from observed runs, and the observed values are kept in the file next
// to the threshold so the derivation can be audited without re-running anything.
const (
	budgetSchemaVersion = "memorystudy-budget/2"
	// legacyBudgetSchemaVersion is the previous contract, still readable so that a hand-written or
	// pre-existing budget is EVALUATED (and can fail) instead of being replaced by a decode error.
	// A legacy budget cannot certify a candidate through the reference path: it carries no
	// provenance, so the gate reports it as unprovenanced and evaluates it under the legacy rules
	// (a rule that names no activity matches any activity). New budgets are always written as /2.
	legacyBudgetSchemaVersion = "memorystudy-budget/1"
	gateSchemaVersion         = "memorystudy-gate/1"
	// referenceSchemaVersion names the archived reference manifest.
	referenceSchemaVersion = "memorystudy-reference/1"
	// budgetFactor is the multiplicative headroom over the worst observed baseline. A regression
	// gate needs room for run-to-run noise; it must not have so much room that the regressions it
	// exists to catch fit inside it.
	budgetFactor = 1.25
)

// Budget derivation parameters. A regression gate needs headroom for run-to-run noise; it must not
// have so much headroom that the regressions it exists to catch fit inside it.
const (
	budgetDefaultFactor = budgetFactor
)

type budgetFile struct {
	SchemaVersion string            `json:"schema_version"`
	Baseline      budgetBaseline    `json:"baseline"`
	Provenance    *budgetProvenance `json:"provenance,omitempty"`
	Rules         []budgetRule      `json:"rules"`
	// legacy is set by the reader for a schema/1 file. It is not part of the JSON: it says "this
	// budget predates the activity selector and the reference provenance", which the gate reports
	// instead of silently treating the file as current.
	legacy bool
}

// budgetProvenance is where the thresholds came from, in a form the gate can check.
//
// # Why the ledger's hash and run ids are here
//
// The failure this closes: run the gate matrix once, feed that ledger to -write-budget, and then
// evaluate the SAME ledger against the budget derived from it. Every rule then passes by
// construction, so the gate reports success without testing anything. A budget therefore records
// (a) whether it came from a reference capture at all, (b) the SHA-256 of the ledger it was derived
// from and (c) that ledger's run ids, and the gate refuses a candidate whose evidence is that very
// ledger unless it was explicitly asked to self-check the reference.
type budgetProvenance struct {
	Kind         string `json:"kind"`
	LedgerPath   string `json:"ledger_path,omitempty"`
	LedgerSHA256 string `json:"ledger_sha256"`
	ManifestPath string `json:"manifest_path,omitempty"`
	ManifestSHA  string `json:"manifest_sha256,omitempty"`
	Repetitions  int    `json:"repetitions"`
	Specs        int    `json:"specs"`
	// FixtureDigests binds each spec to the configuration document it measured. A budget whose
	// document differs from the candidate's is not a threshold for the candidate.
	FixtureDigests map[string]string `json:"fixture_digests,omitempty"`
}

// Provenance kinds. Only a reference budget may certify a candidate.
const (
	provenanceReference = "reference"
	provenanceDerived   = "derived"
)

type budgetBaseline struct {
	CapturedAt  string   `json:"captured_at"`
	SourceSHA   string   `json:"source_sha"`
	SourceDirty bool     `json:"source_dirty"`
	GOOS        string   `json:"goos"`
	GOARCH      string   `json:"goarch"`
	GoVersion   string   `json:"go_version"`
	Tags        []string `json:"tags"`
	// FixtureVersion is the generated-document contract the thresholds were derived against.
	FixtureVersion string   `json:"fixture_version"`
	Runs           []string `json:"runs"`
	Notes          []string `json:"notes,omitempty"`
}

type budgetRule struct {
	ID              string    `json:"id"`
	Description     string    `json:"description"`
	Case            string    `json:"case"`
	Nodes           int       `json:"nodes"`
	Groups          int       `json:"groups"`
	NodeProto       string    `json:"node_proto"`
	Activity        []string  `json:"activity"`
	GCMode          string    `json:"gc_mode"`
	Stage           string    `json:"stage"`
	Metric          string    `json:"metric"`
	Per             string    `json:"per,omitempty"`
	Max             *float64  `json:"max,omitempty"`
	Min             *float64  `json:"min,omitempty"`
	Basis           string    `json:"basis,omitempty"`
	Observed        []float64 `json:"observed,omitempty"`
	ObservedMissing int       `json:"observed_missing,omitempty"`
	// Required separates "this platform cannot report it" from "the run did not produce the
	// evidence". A required rule that no record can answer FAILS on a supported platform instead of
	// being skipped: without it, a run that lost its traffic or half-close observation still passed
	// the gate on the strength of the rules that did have data, which is exactly the silent-green
	// failure the gate exists to prevent. A platform-specific metric that is genuinely unavailable
	// opts out explicitly with `required: false`.
	Required bool `json:"required,omitempty"`

	noiseFloor float64
}

func floatPointer(value float64) *float64 { return &value }

// gateSpecs is the fast matrix. It is small on purpose: the full 0/100/1000/10000 scale and the
// 30s/300s idle matrix are separate entry points, and a gate that takes minutes gets disabled.
func gateSpecs(wide bool) []runSpec {
	base := runSpec{
		Activity:     []string{"none"},
		Idle:         []string{"0s"},
		PostIdle:     "0s",
		HeldFlows:    4,
		TrafficBytes: 64 << 10,
	}
	makeSpec := func(mutate func(*runSpec)) runSpec {
		spec := base
		spec.Activity = append([]string(nil), base.Activity...)
		spec.Idle = append([]string(nil), base.Idle...)
		mutate(&spec)
		normalized, err := normalizeSpec(spec)
		if err != nil {
			// The gate matrix is compiled in; an invalid entry is a programming error, not a user
			// error, and it must not be silently dropped.
			panic("memorystudy: invalid gate spec: " + err.Error())
		}
		return normalized
	}

	specs := []runSpec{
		makeSpec(func(spec *runSpec) {
			spec.Case, spec.Nodes, spec.Groups, spec.NodeProto = "empty", 0, 0, "direct"
			spec.GCMode = "forced"
		}),
		makeSpec(func(spec *runSpec) {
			spec.Case, spec.Nodes, spec.Groups, spec.NodeProto = "empty", 0, 0, "direct"
			spec.GCMode = "natural"
			spec.Idle = []string{"0s", "1s"}
		}),
		makeSpec(func(spec *runSpec) {
			spec.Case, spec.Nodes, spec.Groups, spec.NodeProto = "ordinary", 0, 0, "trojan"
			spec.GCMode = "forced"
		}),
		makeSpec(func(spec *runSpec) {
			spec.Case, spec.Nodes, spec.Groups, spec.NodeProto = "ordinary", 100, 2, "trojan"
			spec.GCMode = "forced"
		}),
		makeSpec(func(spec *runSpec) {
			spec.Case, spec.Nodes, spec.Groups, spec.NodeProto = "empty", 0, 0, "direct"
			spec.GCMode = "forced"
			spec.Activity = []string{"traffic"}
		}),
		makeSpec(func(spec *runSpec) {
			spec.Case, spec.Nodes, spec.Groups, spec.NodeProto = "ordinary", 8, 2, "direct"
			spec.GCMode = "forced"
			spec.Activity = []string{"probe"}
		}),
	}
	if wide {
		for _, nodes := range []int{1000, 10000} {
			count := nodes
			specs = append(specs, makeSpec(func(spec *runSpec) {
				spec.Case, spec.Nodes, spec.Groups, spec.NodeProto = "ordinary", count, 2, "trojan"
				spec.GCMode = "forced"
			}))
		}
	}
	return specs
}

// ruleSelector names the case a rule applies to. It is the same fields the ledger echoes on every
// line, so a rule can be checked against a saved JSONL without re-running anything.
//
// # Why Activity is part of the selector
//
// The gate matrix contains two empty/direct/forced cases that differ only in their activity history:
// the plain one and the traffic one. Without an activity term a base rule selected BOTH, so its
// observed population was six runs instead of three, and deleting the plain case's runs entirely
// still left the rule green on the traffic runs - a case that disappeared was silently covered by a
// different case. The gate's own contract is "a rule names exactly the population it was derived
// from"; activity is part of that population.
type ruleSelector struct {
	caseName  string
	nodes     int
	groups    int
	nodeProto string
	activity  []string
	gcMode    string
}

// noise floors, per rule, in the units of the metric the rule reads.
//
// The number is "the smallest change this rule must be able to see". A megabyte-scale heap reading
// can absorb a few KiB of run-to-run spread without losing the ability to catch a doubled buffer; a
// 2 KiB per-node reading cannot absorb 16 KiB without losing the ability to catch a doubled node.
const (
	noiseHeapFloor    = 16 << 10
	noiseTotalFloor   = 64 << 10
	noiseStageFloor   = 8 << 10
	noisePerNodeFloor = 128
	noisePerFlowFloor = 2 << 10
	noiseCountFloor   = 1
	// noiseNaturalIdleFloor is the absolute growth floor for the natural-idle-growth rule.
	// That rule reads a DELTA over one second with no forced collection, so it has no
	// baseline to scale: 0*1.25 is 0 and the floor is the whole threshold. MEASURED across
	// sessions: the same tree produced 0 (three times in the M1 capture), 8192 (a gate run)
	// and 17152 (an earlier session in logs/M1-before-head.log). A floor of 8 KiB was
	// therefore BELOW an already-observed value, i.e. the gate could fail on noise. 32 KiB
	// is ~1.9x the largest observed single-step growth and still two orders of magnitude
	// below a structural per-second leak (one 8 KiB object per second for four seconds
	// would exceed it). It is a calibration to observed noise, not a relaxation of the
	// rule: the rule still fails on real growth, and the 8192/17152 observations are
	// recorded in analysis/M2-gate-baseline.md.
	noiseNaturalIdleFloor = 32 << 10
)

// gateRuleTemplates is the rule set without thresholds. -write-budget fills the thresholds in.
func gateRuleTemplates() []budgetRule {
	rule := func(id string, description string, selector ruleSelector, metric string, stage string, noise float64) budgetRule {
		return budgetRule{
			ID:          id,
			Description: description,
			Case:        selector.caseName,
			Nodes:       selector.nodes,
			Groups:      selector.groups,
			NodeProto:   selector.nodeProto,
			Activity:    append([]string(nil), selector.activity...),
			GCMode:      selector.gcMode,
			Stage:       stage,
			Metric:      metric,
			noiseFloor:  noise,
			// Every template this matrix ships is a rule the fast gate is supposed to answer on
			// the platform it was derived for, so the default is "the evidence is required". An
			// optional platform metric has to say so, not the other way round.
			Required: true,
		}
	}
	none := []string{"none"}
	emptyForced := ruleSelector{"empty", 0, 0, "direct", none, "forced"}
	emptyNatural := ruleSelector{"empty", 0, 0, "direct", none, "natural"}
	ordinaryZero := ruleSelector{"ordinary", 0, 0, "trojan", none, "forced"}
	ordinaryHundred := ruleSelector{"ordinary", 100, 2, "trojan", none, "forced"}
	probeCase := ruleSelector{"ordinary", 8, 2, "direct", []string{"probe"}, "forced"}
	// The traffic case is the same empty/direct/forced configuration as emptyForced; only its
	// activity history differs, which is exactly why the selector has to say so.
	trafficCase := ruleSelector{"empty", 0, 0, "direct", []string{"traffic"}, "forced"}

	return []budgetRule{
		rule("base-entry-live-heap",
			"the process floor: what a Box-capable binary already holds at its earliest observable point",
			emptyForced, "readings.go.live_heap_bytes", stageEarliestObservable, noiseHeapFloor),
		rule("base-idle-live-heap",
			"the absolute ledger for an idle base Box with no activity history",
			emptyForced, "readings.go.live_heap_bytes", idleStage("0s"), noiseHeapFloor),
		rule("base-idle-total-memory",
			"Go's own total for an idle base Box, including memory the heap reading does not classify",
			emptyForced, "readings.go.total_bytes", idleStage("0s"), noiseTotalFloor),
		rule("base-idle-goroutines",
			"the goroutine floor of an idle base Box",
			emptyForced, "readings.goroutines.current", idleStage("0s"), noiseCountFloor),
		rule("base-construct-delta",
			"what box.New adds over the decoded configuration for the base Box",
			emptyForced, "deltas.from_previous.live_heap_bytes", stageConstruct, noiseStageFloor),
		rule("base-teardown-goroutines",
			"goroutines still running after Close returned",
			emptyForced, "readings.goroutines.current", stageRunEnd, noiseCountFloor),
		rule("base-teardown-live-heap-delta",
			"live heap above the earliest observable point after Close: the release has to actually happen",
			emptyForced, "deltas.from_entry.live_heap_bytes", stageRunEnd, noiseHeapFloor),
		rule("ordinary-zero-idle-live-heap",
			"the ordinary code path with no nodes: it must not cost more than the base Box",
			ordinaryZero, "readings.go.live_heap_bytes", idleStage("0s"), noiseHeapFloor),
		rule("ordinary-100-construct-per-node",
			"per-node live-heap cost of 100 configured, never-dialled trojan nodes, measured right after construction",
			ordinaryHundred, "deltas.from_entry.live_heap_per_node_bytes", stageConstruct, noisePerNodeFloor),
		rule("ordinary-100-idle-per-node",
			"per-node live-heap cost of the same nodes once the Box is started and idle",
			ordinaryHundred, "deltas.from_entry.live_heap_per_node_bytes", idleStage("0s"), noisePerNodeFloor),
		rule("ordinary-100-goroutines",
			"never-dialled nodes must not buy a goroutine each",
			ordinaryHundred, "readings.goroutines.current", idleStage("0s"), noiseCountFloor),
		rule("ordinary-100-threads",
			"never-dialled nodes must not buy an operating-system thread each",
			ordinaryHundred, "readings.process.threads", idleStage("0s"), noiseCountFloor),
		rule("probe-members-succeed",
			"a URL test round over eight local members must reach all of them",
			probeCase, "probe.succeeded", stageProbe, noiseCountFloor),
		// The natural-idle rule answers a different question from every other rule: not "how much
		// does it hold" but "does an untouched process grow". It reads heap/objects, not the live
		// heap: the live-heap metric is only republished at the end of a collection, so a natural
		// comparison across a second with no collection would report a structural zero and the rule
		// would pass without measuring anything.
		rule("natural-idle-growth",
			"an untouched Box must not grow across one second of natural idle (no forced collection)",
			emptyNatural, "deltas.from_previous.heap_objects_bytes", idleStage("1s"), noiseNaturalIdleFloor),
		withPer(
			rule("traffic-per-flow-live-heap",
				"live heap per held flow: one buffer plus bookkeeping, not two buffers",
				trafficCase, "deltas.from_previous.live_heap_bytes", stageTrafficHeld, noisePerFlowFloor),
			"spec.held_flows"),
		// The two business checks are not budgets: they are the definition of a working relay, so
		// their threshold is the value itself and there is nothing to derive.
		withMin(
			rule("traffic-verified",
				"the traffic fixture must verify both directions",
				trafficCase, "traffic.verified", stageTrafficVerified, 0),
			1),
		withMin(
			rule("traffic-half-close",
				"the client's half-close must be propagated: the destination only answers after EOF",
				trafficCase, "traffic.half_close_propagated", stageTrafficVerified, 0),
			1),
	}
}

func withPer(rule budgetRule, path string) budgetRule {
	rule.Per = path
	return rule
}

func withMin(rule budgetRule, minimum float64) budgetRule {
	rule.Min = floatPointer(minimum)
	return rule
}

// ruleResult is one evaluated rule.
type ruleResult struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Observed    *float64 `json:"observed,omitempty"`
	Budget      string   `json:"budget"`
	Runs        int      `json:"runs"`
	Reason      string   `json:"reason,omitempty"`
}

type gateReport struct {
	SchemaVersion  string         `json:"schema_version"`
	GeneratedAt    string         `json:"generated_at"`
	BudgetPath     string         `json:"budget_path"`
	BudgetBaseline budgetBaseline `json:"budget_baseline"`
	Output         string         `json:"output"`
	Source         sourceIdentity `json:"source"`
	GoVersion      string         `json:"go_version"`
	GOOS           string         `json:"goos"`
	GOARCH         string         `json:"goarch"`
	Tags           []string       `json:"tags"`
	Passed         int            `json:"passed"`
	Failed         int            `json:"failed"`
	Skipped        int            `json:"skipped"`
	Results        []ruleResult   `json:"results"`
	Runs           []string       `json:"runs"`
	RecordCount    int            `json:"record_count"`
	// Verification is the identity comparison the verdict stands on: which checkout was measured,
	// which revision the budget describes, and whether the budget is a reference rather than a
	// re-derivation of this very ledger.
	Verification *gateVerification `json:"verification,omitempty"`
	// Error is set when the gate REFUSED to compare. The report is written in that case too, so a
	// refusal is an artefact a reviewer can read rather than an exit code.
	Error string `json:"error,omitempty"`
}

func (r *gateReport) summarise() {
	r.Passed, r.Failed, r.Skipped = 0, 0, 0
	for _, result := range r.Results {
		switch result.Status {
		case statusOK:
			r.Passed++
		case statusFailed:
			r.Failed++
		default:
			r.Skipped++
		}
	}
}

// evaluateBudget compares the ledger against the derived thresholds.
//
// A rule that cannot be evaluated is a SKIP, never a PASS: a metric that is null on this platform,
// a stage a failed child never reached, a baseline captured for a different platform. The gate
// reports those separately and refuses to call an all-skip run a success.
func evaluateBudget(budget budgetFile, records []recordMap) []ruleResult {
	platformMismatch := ""
	if budget.Baseline.GOOS != "" && (budget.Baseline.GOOS != runtime.GOOS || budget.Baseline.GOARCH != runtime.GOARCH) {
		platformMismatch = fmt.Sprintf(
			"the budget was derived on %s/%s and this run is %s/%s; thresholds are platform-specific",
			budget.Baseline.GOOS, budget.Baseline.GOARCH, runtime.GOOS, runtime.GOARCH)
	}

	results := make([]ruleResult, 0, len(budget.Rules))
	for _, rule := range budget.Rules {
		result := ruleResult{ID: rule.ID, Description: rule.Description, Budget: rule.BudgetText()}
		if platformMismatch != "" {
			result.Status = statusUnsupported
			result.Reason = platformMismatch
			results = append(results, result)
			continue
		}
		values, matched, missing := observedValues(rule, records)
		result.Runs = matched
		if matched == 0 {
			result.Status = statusUnsupported
			result.Reason = fmt.Sprintf("no ok-status record matched case=%s nodes=%d groups=%d proto=%s gc=%s stage=%s",
				rule.Case, rule.Nodes, rule.Groups, rule.NodeProto, rule.GCMode, rule.Stage)
			if rule.Required {
				result.Status = statusFailed
				result.Reason = "required evidence missing: " + result.Reason
			}
			results = append(results, result)
			continue
		}
		if len(values) == 0 {
			result.Status = statusUnsupported
			result.Reason = fmt.Sprintf("all %d matching record(s) had a null %s", matched, rule.Metric)
			if rule.Required {
				result.Status = statusFailed
				result.Reason = "required evidence missing: " + result.Reason
			}
			results = append(results, result)
			continue
		}
		if missing > 0 {
			result.Reason = fmt.Sprintf("%d of %d matching record(s) had no value", missing, matched)
			if rule.Required {
				// A rule answered by a subset of the runs is not verified: accepting the runs that
				// happen to carry the observation is how a lost half of the matrix becomes a pass.
				result.Status = statusFailed
				result.Reason = "required evidence incomplete: " + result.Reason
				results = append(results, result)
				continue
			}
		}
		worst := values[0]
		for _, value := range values[1:] {
			if rule.Min != nil {
				worst = math.Min(worst, value)
			} else {
				worst = math.Max(worst, value)
			}
		}
		result.Observed = floatPointer(worst)
		switch {
		case rule.Min != nil && worst < *rule.Min:
			result.Status = statusFailed
			results = append(results, result)
		case rule.Max != nil && worst > *rule.Max:
			result.Status = statusFailed
			results = append(results, result)
		default:
			result.Status = statusOK
			results = append(results, result)
		}
	}
	return results
}

// observedValues resolves one rule against the ledger.
func observedValues(rule budgetRule, records []recordMap) (values []float64, matched int, missing int) {
	// A legacy (schema/1) rule has no activity term and keeps matching any activity. That is the
	// old behaviour preserved for old files only; every rule this tool writes names its activity.
	wantActivity := activityKey(rule.Activity)
	for _, record := range records {
		if record.recordKind() != recordStage {
			continue
		}
		if record.stage() != rule.Stage {
			continue
		}
		if record.status() != statusOK {
			continue
		}
		if record.specField("case") != rule.Case ||
			record.specField("nodes") != fmt.Sprint(rule.Nodes) ||
			record.specField("groups") != fmt.Sprint(rule.Groups) ||
			record.specField("node_proto") != rule.NodeProto ||
			record.specField("gc_mode") != rule.GCMode {
			continue
		}
		if wantActivity != "" && record.specActivity() != wantActivity {
			continue
		}
		matched++
		raw, ok := record.resolve(rule.Metric)
		if !ok {
			missing++
			continue
		}
		value, ok := numeric(raw)
		if !ok {
			missing++
			continue
		}
		if rule.Per != "" {
			perRaw, perOK := record.resolve(rule.Per)
			if !perOK {
				missing++
				continue
			}
			divisor, perOK := numeric(perRaw)
			// A denominator has to be POSITIVE. Zero was always rejected; a negative one was
			// accepted until this check existed, and dividing by it flipped the sign of the
			// observation: a "cost per flow" rule then compared a negative number against a
			// positive ceiling, which can never fail, so the rule was green for any measurement.
			if !perOK || divisor <= 0 {
				missing++
				continue
			}
			value /= divisor
		}
		values = append(values, value)
	}
	return values, matched, missing
}

// BudgetText renders the threshold for the human-readable report.
func (r budgetRule) BudgetText() string {
	switch {
	case r.Min != nil && r.Max != nil:
		return fmt.Sprintf(">= %s and <= %s", floatString(*r.Min), floatString(*r.Max))
	case r.Min != nil:
		return ">= " + floatString(*r.Min)
	case r.Max != nil:
		return "<= " + floatString(*r.Max)
	default:
		return "(no threshold)"
	}
}

// deriveBudget turns observed baselines into thresholds.
//
// Every rule must have at least one observation. A rule with no observation is not given a generous
// default: it fails the derivation, because a threshold invented for a metric that was never
// measured is a gate that cannot detect anything.
func deriveBudget(records []recordMap, provenance budgetProvenance) (budgetFile, error) {
	templates := gateRuleTemplates()
	summarised, err := summariseLedger(records)
	if err != nil {
		return budgetFile{}, err
	}
	provenance.FixtureDigests = summarised.FixtureDigests
	provenance.Repetitions, provenance.Specs = repetitionsAndSpecs(records)
	budget := budgetFile{
		SchemaVersion: budgetSchemaVersion,
		Baseline:      baselineFrom(records),
		Provenance:    &provenance,
	}
	for _, rule := range templates {
		values, matched, missing := observedValues(rule, records)
		rule.Observed = values
		rule.ObservedMissing = missing
		if rule.Min != nil {
			if len(values) == 0 {
				return budgetFile{}, fmt.Errorf(
					"rule %s: no observation for stage %s, so the minimum cannot be justified; "+
						"re-run the gate matrix and derive again", rule.ID, rule.Stage)
			}
			rule.Basis = fmt.Sprintf(
				"business check, not a budget: %d of %d baseline run(s) satisfied it; the minimum is "+
					"the definition of the fixture, not a measurement threshold", len(values), matched)
			budget.Rules = append(budget.Rules, rule)
			continue
		}
		if len(values) == 0 {
			return budgetFile{}, fmt.Errorf(
				"rule %s: no numeric observation in %d matching record(s) of stage %s (activity %q); a "+
					"threshold cannot be derived from a metric this platform does not produce",
				rule.ID, matched, rule.Stage, activityKey(rule.Activity))
		}
		worst := values[0]
		best := values[0]
		for _, value := range values[1:] {
			worst = math.Max(worst, value)
			best = math.Min(best, value)
		}
		noise := rule.noiseFloor
		if noise == 0 {
			// A rule that names no floor gets the stage-level one; the count and per-node rules set
			// their own, because the same number is not meaningful at every scale.
			noise = noiseStageFloor
		}
		threshold := math.Ceil(math.Max(worst*budgetDefaultFactor, worst+noise))
		rule.Max = &threshold
		rule.Basis = fmt.Sprintf(
			"derived from %d baseline run(s) of THIS tree: observed %s..%s; threshold = "+
				"ceil(max(observed)*%.2f, observed+%s) with %s of noise allowance",
			len(values), floatString(best), floatString(worst), budgetDefaultFactor,
			floatString(noise), floatString(noise))
		budget.Rules = append(budget.Rules, rule)
	}
	budget.Baseline.Notes = append(budget.Baseline.Notes,
		"thresholds are derived from the runs listed in baseline.runs; they are not copied from "+
			"another project and they are not a device footprint",
		"re-derive with: memorystudy -write-budget <path> -from <baseline.jsonl> -runs <n> "+
			"(a reference budget additionally needs -reference-manifest)",
		fmt.Sprintf("provenance: kind=%s, ledger_sha256=%s, %d repetitions x %d specs",
			provenance.Kind, provenance.LedgerSHA256, provenance.Repetitions, provenance.Specs))
	return budget, nil
}

func baselineFrom(records []recordMap) budgetBaseline {
	baseline := budgetBaseline{CapturedAt: time.Now().UTC().Format(time.RFC3339)}
	baseline.Runs = runIDs(records)
	stringAt := func(record recordMap, path string) string {
		value, ok := record.resolve(path)
		if !ok {
			return ""
		}
		text, _ := value.(string)
		return text
	}
	// Every stage line carries the identity, but the FIRST one (earliest_observable) predates the
	// fixture: it can only say what the process was, not which document it went on to measure. The
	// scan therefore takes the first non-empty value of each field across the ledger instead of
	// reading one line and inheriting its blanks.
	for _, record := range records {
		if record.recordKind() != recordStage {
			continue
		}
		if baseline.SourceSHA == "" {
			baseline.SourceSHA = stringAt(record, "identity.source.sha")
		}
		if value, ok := record.resolve("identity.source.dirty"); ok {
			if dirty, isBool := value.(bool); isBool {
				baseline.SourceDirty = dirty
			}
		}
		if baseline.GOOS == "" {
			baseline.GOOS = stringAt(record, "identity.build.goos")
		}
		if baseline.GOARCH == "" {
			baseline.GOARCH = stringAt(record, "identity.build.goarch")
		}
		if baseline.GoVersion == "" {
			baseline.GoVersion = stringAt(record, "identity.build.go_version")
		}
		if baseline.FixtureVersion == "" {
			baseline.FixtureVersion = stringAt(record, "identity.fixture.version")
		}
		if len(baseline.Tags) == 0 {
			if tags, ok := record.resolve("identity.build.tags"); ok {
				if list, isList := tags.([]any); isList {
					for _, entry := range list {
						if text, isText := entry.(string); isText {
							baseline.Tags = append(baseline.Tags, text)
						}
					}
				}
			}
		}
	}
	return baseline
}

func readBudgetFile(path string) (budgetFile, error) {
	var budget budgetFile
	content, err := os.ReadFile(path)
	if err != nil {
		return budget, fmt.Errorf("read the budget: %w", err)
	}
	// A budget edited on Windows arrives with a UTF-8 BOM more often than not, and encoding/json
	// rejects it. Stripping it here means the file can be edited in any editor.
	content = bytes.TrimPrefix(content, []byte{0xEF, 0xBB, 0xBF})
	if err = json.Unmarshal(content, &budget); err != nil {
		return budget, fmt.Errorf("decode %s: %w", path, err)
	}
	switch budget.SchemaVersion {
	case budgetSchemaVersion:
	case legacyBudgetSchemaVersion:
		// Readable on purpose: a hand-written budget (the CI canary is one) must still be
		// EVALUATED and be able to fail, not merely rejected as a version mismatch. The gate
		// reports it as unprovenanced and applies the legacy selector, and it can never pass
		// through the reference path.
		budget.legacy = true
	default:
		return budget, fmt.Errorf("%s is %q, this binary writes %q (and still reads the legacy %q)",
			path, budget.SchemaVersion, budgetSchemaVersion, legacyBudgetSchemaVersion)
	}
	if err = validateBudgetStructure(budget, path); err != nil {
		return budget, err
	}
	return budget, nil
}

// validateBudgetStructure refuses a budget that cannot fail.
//
// A threshold is the whole content of a rule: a rule with neither Min nor Max evaluates to PASS
// unconditionally, and a budget whose rules were trimmed or duplicated still "runs". Both were
// demonstrated: deleting 16 of 17 rules, and nulling one rule's threshold, both left the gate at
// exit 0. The file is a baseline, so an empty or duplicated rule set is not a smaller check - it is
// a different one.
//
// A schema/2 budget additionally has to say WHICH POPULATION each rule measures (case, nodes,
// groups, proto, activity, gc) and where its thresholds came from. A missing activity is refused
// rather than treated as "any activity": that default is exactly the hole the activity term exists
// to close, and a budget that lost the field would otherwise silently go back to matching a
// different case's runs.
func validateBudgetStructure(budget budgetFile, path string) error {
	if len(budget.Rules) == 0 {
		return fmt.Errorf("%s has no rules: a gate with an empty rule set verifies nothing and must not pass", path)
	}
	seen := make(map[string]bool, len(budget.Rules))
	for index, rule := range budget.Rules {
		if rule.ID == "" {
			return fmt.Errorf("%s rule %d has no id", path, index)
		}
		if seen[rule.ID] {
			return fmt.Errorf("%s declares rule id %q twice; a duplicate id makes the report ambiguous", path, rule.ID)
		}
		seen[rule.ID] = true
		if rule.Min == nil && rule.Max == nil {
			return fmt.Errorf("%s rule %q has no threshold: it would pass unconditionally", path, rule.ID)
		}
		if !budget.legacy {
			switch {
			case rule.Case == "":
				return fmt.Errorf("%s rule %q does not name a case; a rule that selects everything measures nothing", path, rule.ID)
			case rule.NodeProto == "":
				return fmt.Errorf("%s rule %q does not name a node protocol", path, rule.ID)
			case rule.GCMode == "":
				return fmt.Errorf("%s rule %q does not name a gc discipline", path, rule.ID)
			case len(rule.Activity) == 0:
				return fmt.Errorf("%s rule %q does not name an activity history; without it the rule "+
					"matches every activity of its case, so one case's disappearance can be covered "+
					"by another's runs", path, rule.ID)
			case rule.Stage == "":
				return fmt.Errorf("%s rule %q does not name a stage", path, rule.ID)
			case rule.Metric == "":
				return fmt.Errorf("%s rule %q does not name a metric", path, rule.ID)
			}
		}
	}
	if !budget.legacy {
		if budget.Provenance == nil {
			return fmt.Errorf("%s carries no provenance: a schema/2 budget must record the ledger "+
				"hash and the reference kind it was derived from, so the gate can refuse a budget "+
				"derived from the very ledger it is asked to certify", path)
		}
		if budget.Provenance.Kind != provenanceReference && budget.Provenance.Kind != provenanceDerived {
			return fmt.Errorf("%s declares provenance kind %q (want %q or %q)",
				path, budget.Provenance.Kind, provenanceReference, provenanceDerived)
		}
		if budget.Provenance.LedgerSHA256 == "" {
			return fmt.Errorf("%s does not record the SHA-256 of the ledger its thresholds came from", path)
		}
	}
	return nil
}

// requireCompleteEvidence refuses a ledger that is not the evidence the gate claims to have read.
//
// The thresholds were derived from a 3-repetition baseline, so a session that silently ran one
// repetition, lost a run_end, mixed two processes under one run id or skipped a stage is not a
// weaker measurement of the same thing - it is a different measurement, and every rule it answers
// is answered against the wrong population. Each of these was a demonstrated false pass before this
// check existed: 5 of 6 run_end records removed, the base case deleted entirely, and 1 of 3
// repetitions kept all reached "17 pass" with exit 0.
//
// It is also the check -write-budget uses, so the artefact the thresholds are derived from cannot be
// weaker than the artefact the gate accepts.
func requireCompleteEvidence(current *session, flags *runFlags) error {
	return requireCompleteLedger(current.records, flags.runs, gateSpecs(flags.wide))
}

// requireCompleteLedger is the completeness check over a ledger, whether it was just measured or
// read back from a file. `planned` is the exact case list the ledger must contain.
func requireCompleteLedger(records []recordMap, repetitions int, planned []runSpec) error {
	if repetitions < 1 {
		return fmt.Errorf("incomplete evidence: %d repetition(s) planned; a session that ran nothing "+
			"cannot be completed", repetitions)
	}
	if len(planned) == 0 {
		return fmt.Errorf("incomplete evidence: no planned case, so there is nothing to compare")
	}
	expectedRuns := repetitions * len(planned)
	seen := runIDs(records)
	if len(seen) != expectedRuns {
		return fmt.Errorf("incomplete evidence: %d repetition(s) x %d spec(s) = %d run(s) planned, "+
			"the ledger names %d; a gate cannot be compared against a 3-run baseline with fewer runs",
			repetitions, len(planned), expectedRuns, len(seen))
	}
	// Which case each run measured is part of the population, not decoration: 18 runs that are
	// each a repetition of one spec satisfy "one run_end per run" and still answer every rule from
	// the wrong case. The multiset comparison is what makes "specs x runs" mean it.
	wanted := make(map[string]int, len(planned))
	for _, spec := range planned {
		wanted[specSignature(spec)]++
	}
	perRun := make(map[string][]string, len(seen))
	for _, record := range records {
		id := record.runID()
		if id == "" {
			continue
		}
		perRun[id] = append(perRun[id], recordSpecSignature(record))
	}
	got := make(map[string]int, expectedRuns)
	for _, id := range seen {
		signatures := perRun[id]
		if len(signatures) == 0 {
			return fmt.Errorf("incomplete evidence: run %s has no records", id)
		}
		signature := signatures[0]
		for _, other := range signatures[1:] {
			if other != signature {
				return fmt.Errorf("incomplete evidence: run %s mixes two specs (%q and %q); one run id "+
					"is one measurement", id, signature, other)
			}
		}
		if wanted[signature] == 0 {
			return fmt.Errorf("incomplete evidence: run %s measured the case %q, which is not in the "+
				"planned matrix", id, signature)
		}
		got[signature]++
	}
	for signature, count := range wanted {
		expected := count * repetitions
		if got[signature] != expected {
			return fmt.Errorf("incomplete evidence: case %q appears in %d run(s), want %d "+
				"(%d repetition(s) x %d spec(s) with that signature)", signature, got[signature], expected,
				repetitions, count)
		}
	}
	core := []string{
		stageSetup, stageConfigParse, stageConfigBuild, stageConstruct, stageFixtureServers,
		stageServiceStart, stageReady, stageClose, stageRunEnd,
	}
	runEnd := make(map[string]int, len(seen))
	stageIndex := make(map[string]map[int]bool, len(seen))
	stagePID := make(map[string]map[int]bool, len(seen))
	stageName := make(map[string]map[string]bool, len(seen))
	childOK := make(map[string]bool, len(seen))
	for _, record := range records {
		id := record.runID()
		if id == "" {
			continue
		}
		switch record.recordKind() {
		case recordChildResult:
			// The parent writes this line for every run, including a child that died. `ok` here is
			// the parent's own verdict on the child.
			if record.status() != statusOK {
				cause := ""
				if value, ok := record["failure_cause"].(string); ok {
					cause = value
				}
				return fmt.Errorf("incomplete evidence: run %s is not ok (%s)", id, cause)
			}
			if runEndSeen, ok := record["run_end_seen"].(bool); ok && !runEndSeen {
				return fmt.Errorf("incomplete evidence: run %s is ok but the parent did not see a run_end", id)
			}
			childOK[id] = true
		case recordStage:
			if record.stage() == stageRunEnd {
				runEnd[id]++
			}
			if value, ok := intFromRecord(record, "stage_index"); ok {
				if stageIndex[id] == nil {
					stageIndex[id] = make(map[int]bool)
				}
				stageIndex[id][value] = true
			}
			if value, ok := intFromRecord(record, "pid"); ok {
				if stagePID[id] == nil {
					stagePID[id] = make(map[int]bool)
				}
				stagePID[id][value] = true
			}
			if stageName[id] == nil {
				stageName[id] = make(map[string]bool)
			}
			stageName[id][record.stage()] = true
		}
	}
	for _, id := range seen {
		if !childOK[id] {
			return fmt.Errorf("incomplete evidence: run %s has no ok child_result record", id)
		}
		if runEnd[id] != 1 {
			return fmt.Errorf("incomplete evidence: run %s has %d run_end record(s), want exactly one", id, runEnd[id])
		}
		if len(stagePID[id]) != 1 {
			return fmt.Errorf("incomplete evidence: run %s has %d process ids among its stage records, want exactly one",
				id, len(stagePID[id]))
		}
		indexes := stageIndex[id]
		for expected := 0; expected < len(indexes); expected++ {
			if !indexes[expected] {
				return fmt.Errorf("incomplete evidence: run %s stage indexes are not contiguous from 0 (missing %d)", id, expected)
			}
		}
		for _, stage := range core {
			if !stageName[id][stage] {
				return fmt.Errorf("incomplete evidence: run %s is missing the %s stage", id, stage)
			}
		}
	}
	return nil
}

// specSignature is the identity of one planned case, in the fields a ledger echoes. Two runs with
// different signatures are different measurements and cannot stand in for each other.
func specSignature(spec runSpec) string {
	return strings.Join([]string{
		spec.Case,
		fmt.Sprint(spec.Nodes),
		fmt.Sprint(spec.Groups),
		spec.NodeProto,
		spec.ConfigSource,
		activityKey(spec.Activity),
		spec.GCMode,
		fmt.Sprint(spec.HeldFlows),
		fmt.Sprint(spec.TrafficBytes),
	}, "|")
}

// recordSpecSignature is specSignature read back from a ledger line.
func recordSpecSignature(record recordMap) string {
	return strings.Join([]string{
		record.specField("case"),
		record.specField("nodes"),
		record.specField("groups"),
		record.specField("node_proto"),
		record.specField("config_source"),
		record.specActivity(),
		record.specField("gc_mode"),
		record.specField("held_flows"),
		record.specField("traffic_bytes"),
	}, "|")
}

// repetitionsAndSpecs reports how many repetitions of how many distinct cases a ledger contains.
func repetitionsAndSpecs(records []recordMap) (repetitions int, specs int) {
	perRun := make(map[string]string)
	for _, record := range records {
		id := record.runID()
		if id == "" || record.recordKind() != recordStage {
			continue
		}
		if _, exists := perRun[id]; !exists {
			perRun[id] = recordSpecSignature(record)
		}
	}
	if len(perRun) == 0 {
		return 0, 0
	}
	counts := make(map[string]int)
	for _, signature := range perRun {
		counts[signature]++
	}
	specs = len(counts)
	repetitions = -1
	for _, count := range counts {
		if repetitions < 0 || count < repetitions {
			repetitions = count
		}
	}
	if specs == 0 {
		return 0, 0
	}
	return repetitions, specs
}

func writeBudgetFile(path string, budget budgetFile) error {
	content, err := json.MarshalIndent(budget, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	return os.WriteFile(path, content, 0o644)
}

func writeGateReport(path string, report gateReport) error {
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), 0o644)
}

func printGateReport(writer io.Writer, report gateReport, budget budgetFile) {
	fmt.Fprintf(writer, "memorystudy gate: %d pass, %d fail, %d skip (%d records, budget %s, baseline %s)\n",
		report.Passed, report.Failed, report.Skipped, report.RecordCount, report.BudgetPath,
		budget.Baseline.SourceSHA)
	if verification := report.Verification; verification != nil {
		fmt.Fprintf(writer, "identity: candidate %s, checkout %s, baseline %s, reference %s%s\n",
			shortOrNone(verification.CandidateSHA), shortOrNone(verification.CheckoutSHA),
			shortOrNone(verification.BaselineSHA), referenceDescription(verification),
			describeSelfCheck(verification.SelfCheck))
		if verification.LegacyBudget {
			fmt.Fprintf(writer, "note: this budget is the legacy schema/1 contract: its rules name no "+
				"activity history and it carries no provenance, so it cannot certify a candidate\n")
		}
		for _, warning := range verification.Warnings {
			fmt.Fprintf(writer, "warning: %s\n", warning)
		}
	}
	if report.Error != "" {
		fmt.Fprintf(writer, "refused: %s\n", report.Error)
	}
	if budget.Baseline.SourceSHA != "" && report.Source.SHA != "" && budget.Baseline.SourceSHA != report.Source.SHA {
		fmt.Fprintf(writer, "note: the budget was derived at %s and this run is %s; the thresholds still apply, the comparison of the two is not a regression\n",
			budget.Baseline.SourceSHA, report.Source.SHA)
	}
	ordered := append([]ruleResult(nil), report.Results...)
	sort.SliceStable(ordered, func(left, right int) bool {
		rank := func(status string) int {
			switch status {
			case statusFailed:
				return 0
			case statusUnsupported:
				return 2
			default:
				return 1
			}
		}
		return rank(ordered[left].Status) < rank(ordered[right].Status)
	})
	for _, result := range ordered {
		label := strings.ToUpper(result.Status)
		observed := "-"
		if result.Observed != nil {
			observed = floatString(*result.Observed)
		}
		line := fmt.Sprintf("  %-11s %-32s observed %-12s budget %-20s runs %d",
			label, result.ID, observed, result.Budget, result.Runs)
		if result.Reason != "" {
			line += "  (" + result.Reason + ")"
		}
		fmt.Fprintln(writer, line)
	}
}

func shortOrNone(sha string) string {
	if sha == "" {
		return "(none)"
	}
	return shortSHA(sha)
}

func referenceDescription(verification *gateVerification) string {
	if verification.ReferenceKind == "" {
		return "(none)"
	}
	if verification.ReferenceLedger == "" {
		return verification.ReferenceKind
	}
	return verification.ReferenceKind + " ledger " + shortSHA(verification.ReferenceLedger)
}

func describeSelfCheck(selfCheck bool) string {
	if !selfCheck {
		return ""
	}
	return ", self-check (the reference ledger is being verified against its own budget)"
}
