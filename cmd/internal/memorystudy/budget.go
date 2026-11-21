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
	budgetSchemaVersion = "memorystudy-budget/1"
	gateSchemaVersion   = "memorystudy-gate/1"
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
	SchemaVersion string         `json:"schema_version"`
	Baseline      budgetBaseline `json:"baseline"`
	Rules         []budgetRule   `json:"rules"`
}

type budgetBaseline struct {
	CapturedAt  string   `json:"captured_at"`
	SourceSHA   string   `json:"source_sha"`
	SourceDirty bool     `json:"source_dirty"`
	GOOS        string   `json:"goos"`
	GOARCH      string   `json:"goarch"`
	GoVersion   string   `json:"go_version"`
	Tags        []string `json:"tags"`
	Runs        []string `json:"runs"`
	Notes       []string `json:"notes,omitempty"`
}

type budgetRule struct {
	ID              string    `json:"id"`
	Description     string    `json:"description"`
	Case            string    `json:"case"`
	Nodes           int       `json:"nodes"`
	Groups          int       `json:"groups"`
	NodeProto       string    `json:"node_proto"`
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

// ruleSelector names the case a rule applies to. It is the same five fields the ledger echoes on
// every line, so a rule can be checked against a saved JSONL without re-running anything.
type ruleSelector struct {
	caseName  string
	nodes     int
	groups    int
	nodeProto string
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
	emptyForced := ruleSelector{"empty", 0, 0, "direct", "forced"}
	emptyNatural := ruleSelector{"empty", 0, 0, "direct", "natural"}
	ordinaryZero := ruleSelector{"ordinary", 0, 0, "trojan", "forced"}
	ordinaryHundred := ruleSelector{"ordinary", 100, 2, "trojan", "forced"}
	probeCase := ruleSelector{"ordinary", 8, 2, "direct", "forced"}

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
			emptyNatural, "deltas.from_previous.heap_objects_bytes", idleStage("1s"), noiseStageFloor),
		withPer(
			rule("traffic-per-flow-live-heap",
				"live heap per held flow: one buffer plus bookkeeping, not two buffers",
				emptyForced, "deltas.from_previous.live_heap_bytes", stageTrafficHeld, noisePerFlowFloor),
			"spec.held_flows"),
		// The two business checks are not budgets: they are the definition of a working relay, so
		// their threshold is the value itself and there is nothing to derive.
		withMin(
			rule("traffic-verified",
				"the traffic fixture must verify both directions",
				emptyForced, "traffic.verified", stageTrafficVerified, 0),
			1),
		withMin(
			rule("traffic-half-close",
				"the client's half-close must be propagated: the destination only answers after EOF",
				emptyForced, "traffic.half_close_propagated", stageTrafficVerified, 0),
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
			if !perOK || divisor == 0 {
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
func deriveBudget(records []recordMap, previousPath string) (budgetFile, error) {
	templates := gateRuleTemplates()
	budget := budgetFile{
		SchemaVersion: budgetSchemaVersion,
		Baseline:      baselineFrom(records),
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
				"rule %s: no numeric observation in %d matching record(s) of stage %s; a threshold "+
					"cannot be derived from a metric this platform does not produce",
				rule.ID, matched, rule.Stage)
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
		"re-derive with: memorystudy -write-budget <path> -from <baseline.jsonl>")
	_ = previousPath
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
	for _, record := range records {
		if record.recordKind() != recordStage {
			continue
		}
		baseline.SourceSHA = stringAt(record, "identity.source.sha")
		if value, ok := record.resolve("identity.source.dirty"); ok {
			baseline.SourceDirty, _ = value.(bool)
		}
		baseline.GOOS = stringAt(record, "identity.build.goos")
		baseline.GOARCH = stringAt(record, "identity.build.goarch")
		baseline.GoVersion = stringAt(record, "identity.build.go_version")
		if tags, ok := record.resolve("identity.build.tags"); ok {
			if list, isList := tags.([]any); isList {
				for _, entry := range list {
					if text, isText := entry.(string); isText {
						baseline.Tags = append(baseline.Tags, text)
					}
				}
			}
		}
		break
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
	if budget.SchemaVersion != budgetSchemaVersion {
		return budget, fmt.Errorf("%s is %q, this binary writes %q", path, budget.SchemaVersion, budgetSchemaVersion)
	}
	return budget, nil
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
