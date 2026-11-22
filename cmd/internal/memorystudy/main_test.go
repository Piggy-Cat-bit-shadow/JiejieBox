package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	singjson "github.com/sagernet/sing/common/json"
)

// The tests in this file pin the parts of the instrument that a plausible-but-wrong implementation
// would get wrong: a missing metric becoming a zero, an unsigned subtraction wrapping, a fixture
// that silently changes between runs, and a budget that passes because nothing was measured.

// TestSignedDeltaKeepsSign is the underflow guard.
//
// Every runtime increment is a uint64. "The heap went down by 40 KiB" is a real answer, and
// computing it in uint64 turns it into 1.8e19 - a number larger than every budget in the file, so a
// gate comparing it would pass precisely when memory was released. Clamping to zero is the other
// wrong answer: it makes a release indistinguishable from no change.
func TestSignedDeltaKeepsSign(t *testing.T) {
	cases := []struct {
		name   string
		after  uint64
		before uint64
		want   int64
	}{
		{"growth", 2000, 1500, 500},
		{"release", 1500, 2000, -500},
		{"unchanged", 1500, 1500, 0},
		{"large but representable", 1 << 40, 1 << 39, 1 << 39},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			delta, ok := signedDelta(testCase.after, testCase.before)
			if !ok {
				t.Fatalf("signedDelta(%d, %d) refused a representable difference", testCase.after, testCase.before)
			}
			if *delta != testCase.want {
				t.Fatalf("signedDelta(%d, %d) = %d, want %d", testCase.after, testCase.before, *delta, testCase.want)
			}
		})
	}
}

// TestSignedDeltaRefusesUnrepresentableInput: a reading above math.MaxInt64 cannot be a signed
// delta, and the instrument must say so rather than clamp it into something that looks plausible.
func TestSignedDeltaRefusesUnrepresentableInput(t *testing.T) {
	if _, ok := signedDelta(math.MaxUint64, 0); ok {
		t.Fatal("signedDelta accepted a value that does not fit in int64; a wrapped delta would " +
			"compare as a huge positive number and pass every budget")
	}
	if _, ok := signedDelta(0, math.MaxUint64); ok {
		t.Fatal("signedDelta accepted a value that does not fit in int64")
	}
}

// TestDeltaOfMissingSideIsMissing: a delta with one missing side is unknown, not equal to the value
// that happens to be present.
func TestDeltaOfMissingSideIsMissing(t *testing.T) {
	value := uint64(4096)
	if delta := deltaOf(&value, nil); delta != nil {
		t.Fatalf("deltaOf with a missing baseline returned %d; a partial subtraction is not a delta", *delta)
	}
	if delta := deltaOf(nil, &value); delta != nil {
		t.Fatalf("deltaOf with a missing reading returned %d", *delta)
	}
}

// TestMissingMetricsAreNullNotZero is the schema's central promise.
//
// A consumer that reads a missing device footprint as a zero reports a device that costs nothing.
// The writer therefore emits null for every unsupported reading, and the reader must see null.
func TestMissingMetricsAreNullNotZero(t *testing.T) {
	record := stageRecord{
		SchemaVersion: schemaVersion,
		Record:        recordStage,
		Status:        statusOK,
		Stage:         stageEarliestObservable,
		Identity: identity{
			Device: deviceIdentity{Supported: false, Reason: "no device"},
		},
		Readings: readings{
			Device: deviceReadings{Supported: false, Reason: "no device"},
		},
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, path := range []string{
		`"phys_footprint_bytes":null`,
		`"peak_footprint_bytes":null`,
		`"live_heap_bytes":null`,
		`"threads":null`,
		`"profiling_buckets_bytes":null`,
		`"memprofile_rate_bytes":null`,
	} {
		if !strings.Contains(text, path) {
			t.Fatalf("the encoded record does not carry %s; a missing measurement must be null so "+
				"that no reader can sum it as zero:\n%s", path, text)
		}
	}
	// And the reverse: a zero that IS a real zero (an exact count) is still a number.
	if !strings.Contains(text, `"outbounds":0`) {
		t.Fatalf("an exact count of zero must stay a number, not become null:\n%s", text)
	}
}

// TestSamplerMarksUnknownMetricUnsupported: a metric name the toolchain does not publish becomes an
// explicit null with a reason, never a zero.
func TestSamplerMarksUnknownMetricUnsupported(t *testing.T) {
	instance := newSampler()

	if value := instance.uintValue("/this/metric/does/not/exist:bytes"); value != nil {
		t.Fatalf("an unknown metric produced %d", *value)
	}
	// A metric that exists in Go 1.27 must be readable, and one that does not must be absent from
	// the sample list rather than present with a bogus value.
	if _, exists := instance.index["/gc/heap/live:bytes"]; !exists {
		t.Skip("this toolchain does not publish /gc/heap/live:bytes; the ledger degrades to nulls")
	}
	instance.read()
	if value := instance.uintValue("/gc/heap/live:bytes"); value == nil {
		t.Fatal("live heap is null on a toolchain that publishes it")
	}
	instance.read()
	if _, exists := instance.unsupportedNow()["/gc/heap/live:bytes"]; exists && instance.uintValue("/gc/heap/live:bytes") != nil {
		t.Fatal("a metric with a value must not also carry an unsupported reason")
	}
}

// TestFixtureIsDeterministic: two runs of the same spec must produce byte-identical configuration,
// or a difference between them is a difference in the fixture.
func TestFixtureIsDeterministic(t *testing.T) {
	spec := runSpec{Case: "ordinary", Nodes: 3, Groups: 2, NodeProto: "trojan",
		Activity: []string{"none"}, Idle: []string{"0s"}, PostIdle: "0s", GCMode: "forced",
		HeldFlows: 4, TrafficBytes: 1024}
	normalized, err := normalizeSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	first := generateFixture(normalized, 12345, "http://127.0.0.1:1/generate_204")
	second := generateFixture(normalized, 12345, "http://127.0.0.1:1/generate_204")
	if string(first.Document) != string(second.Document) {
		t.Fatal("the fixture is not deterministic")
	}
	if first.Nodes != 3 || first.Groups != 2 || len(first.NodeTags) != 3 || len(first.GroupTags) != 2 {
		t.Fatalf("the fixture does not describe the spec: %d nodes, %d groups, %d tags",
			first.Nodes, first.Groups, len(first.NodeTags))
	}
	if first.ProbeTag != "" {
		t.Fatalf("a case without probe activity produced a URL-test group %q; a URL-test group "+
			"probes its members at startup, so a never-dialled node census must not contain one",
			first.ProbeTag)
	}
}

// TestFixtureParses is the check that keeps the generator honest: a document this tool writes but
// the product cannot read would measure the error path.
func TestFixtureParses(t *testing.T) {
	for _, spec := range []runSpec{
		{Case: "empty", NodeProto: "direct", Activity: []string{"none"}, GCMode: "forced", TrafficBytes: 1024},
		{Case: "ordinary", Nodes: 3, Groups: 2, NodeProto: "trojan", Activity: []string{"none"}, GCMode: "forced", TrafficBytes: 1024},
		{Case: "ordinary", Nodes: 2, Groups: 2, NodeProto: "direct", Activity: []string{"probe"}, GCMode: "forced", TrafficBytes: 1024},
		{Case: "heavy", Nodes: 1, Groups: 1, NodeProto: "heavy", Activity: []string{"none"}, GCMode: "forced", TrafficBytes: 1024},
	} {
		normalized, err := normalizeSpec(spec)
		if err != nil {
			t.Fatalf("%s: %v", spec.Case, err)
		}
		fixture := generateFixture(normalized, 23456, "http://127.0.0.1:1/generate_204")
		ctx := include.Context(t.Context())
		options, err := singjson.UnmarshalExtendedContext[option.Options](ctx, fixture.Document)
		if err != nil {
			t.Fatalf("%s fixture does not parse: %v\n%s", spec.Case, err, fixture.Document)
		}
		if len(options.Outbounds) != 1+normalized.Nodes+normalized.Groups {
			t.Fatalf("%s fixture parsed %d outbounds, want 1 direct + %d nodes + %d groups",
				spec.Case, len(options.Outbounds), normalized.Nodes, normalized.Groups)
		}
		if spec.Case == "ordinary" && len(spec.Activity) == 1 && spec.Activity[0] == "probe" {
			if fixture.ProbeTag == "" {
				t.Fatal("a probe case produced no URL-test group to drive")
			}
		}
	}
}

// TestNormalizeSpec covers the combinations the fixture cannot express.
func TestNormalizeSpec(t *testing.T) {
	valid := runSpec{Case: "ordinary", Nodes: 10, Groups: 2, NodeProto: "trojan",
		Activity: []string{"none"}, Idle: []string{"0s"}, PostIdle: "0s", GCMode: "forced",
		HeldFlows: 4, TrafficBytes: 1024}

	if _, err := normalizeSpec(func() runSpec {
		spec := valid
		spec.Case, spec.Nodes, spec.Groups = "empty", 5, 0
		return spec
	}()); err == nil {
		t.Fatal("case empty accepted nodes")
	}
	if _, err := normalizeSpec(func() runSpec {
		spec := valid
		spec.GCMode = "sometimes"
		return spec
	}()); err == nil {
		t.Fatal("an unknown gc mode was accepted")
	}
	if _, err := normalizeSpec(func() runSpec {
		spec := valid
		spec.Activity = []string{"dial"}
		return spec
	}()); err == nil {
		t.Fatal("an unknown activity was accepted")
	}

	// A group over zero nodes is not a legal configuration (box.New rejects it), so the zero-node
	// case silently has no groups - and it must, or the origin of the per-node slope is invalid.
	zeroNodes := valid
	zeroNodes.Nodes, zeroNodes.Groups = 0, 3
	normalized, err := normalizeSpec(zeroNodes)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Groups != 0 {
		t.Fatalf("a zero-node case kept %d groups; box.New will reject them", normalized.Groups)
	}

	// A probe needs a URL-test group to drive.
	probe := valid
	probe.Activity = []string{"probe"}
	probe.Groups = 0
	normalized, err = normalizeSpec(probe)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Groups < 2 {
		t.Fatalf("a probe case kept %d groups; it needs a URL-test group", normalized.Groups)
	}
}

// TestBuildReplyIsDerivedFromTheRequest: the traffic fixture's reply has to be a function of what
// arrived, or a corrupted request and a corrupted response could cancel out.
func TestBuildReplyIsDerivedFromTheRequest(t *testing.T) {
	first := buildReply([]byte("hello from the client"))
	second := buildReply([]byte("hello from the clienT"))
	if len(first) != len(second) {
		t.Fatalf("the two replies differ in length (%d vs %d) for the same request length", len(first), len(second))
	}
	if string(first) == string(second) {
		t.Fatal("the reply does not depend on the request content, so a corrupted request would " +
			"produce the expected reply")
	}
	// The same request twice must produce the same reply, or the client cannot predict it.
	if string(buildReply([]byte("hello from the client"))) != string(first) {
		t.Fatal("the reply is not deterministic")
	}
	request := requestPayload(3, 32)
	reply := buildReply(request)
	if len(reply) != len(replyMagic)+8+32+len(request) {
		t.Fatalf("the reply is %d bytes, want a header plus a body as long as the request", len(reply))
	}
}

// TestGODEBUGSettingParsing: the policy block reports the values the process inherited, and a
// parser that misses a setting would report "the default" for an overridden one.
func TestGODEBUGSettingParsing(t *testing.T) {
	if got := godedebugSetting("memprofilerate=0,adaptivestackstart=0", "memprofilerate"); got != "0" {
		t.Fatalf("memprofilerate = %q", got)
	}
	if got := godedebugSetting("x=1,memprofilerate=131072", "memprofilerate"); got != "131072" {
		t.Fatalf("memprofilerate = %q", got)
	}
	if got := godedebugSetting("adaptivestackstart=1", "memprofilerate"); got != "" {
		t.Fatalf("a missing setting returned %q", got)
	}
	if got := linkedGODEBUGDefault(buildIdentity{LDFlags: "-s -w -X runtime.godebugDefault=memprofilerate=0,adaptivestackstart=0"}); got != "memprofilerate=0,adaptivestackstart=0" {
		t.Fatalf("linked runtime.godebugDefault = %q", got)
	}
	if got := linkedGODEBUGDefault(buildIdentity{LDFlags: "-s -w"}); got != "" {
		t.Fatalf("a binary without a linked default reported %q", got)
	}
}

// setPath builds the nested map a JSONL reader would have decoded. Booleans stay booleans: the gate
// reads a business check like traffic.verified through the same numeric resolver, which is what
// makes `"min": 1` on a boolean meaningful, and coercing it to json.Number("true") would make that
// check silently unmeasurable.
func setPath(target map[string]any, path string, value any) {
	segments := strings.Split(path, ".")
	current := target
	for _, segment := range segments[:len(segments)-1] {
		next, exists := current[segment].(map[string]any)
		if !exists {
			next = map[string]any{}
			current[segment] = next
		}
		current = next
	}
	switch typed := value.(type) {
	case bool:
		current[segments[len(segments)-1]] = typed
	case int:
		current[segments[len(segments)-1]] = json.Number(fmt.Sprintf("%d", typed))
	case int64:
		current[segments[len(segments)-1]] = json.Number(fmt.Sprintf("%d", typed))
	case float64:
		current[segments[len(segments)-1]] = json.Number(floatString(typed))
	default:
		current[segments[len(segments)-1]] = fmt.Sprint(typed)
	}
}

// syntheticRecord builds one ledger line with the given metric values.
func syntheticRecord(spec runSpec, stage string, status string, values map[string]any) recordMap {
	activity := make([]any, 0, len(spec.Activity))
	for _, entry := range spec.Activity {
		activity = append(activity, entry)
	}
	record := recordMap{
		"schema_version": schemaVersion,
		"record":         recordStage,
		"status":         status,
		// One run id per SPEC, not per case: the summariser is allowed to refuse a run that carries
		// two different fixture digests, and a synthetic ledger that reused one id for three specs
		// would be exactly that.
		"run_id": "synthetic-" + specTag(spec),
		"stage":  stage,
	}
	record["spec"] = map[string]any{
		"case":          spec.Case,
		"nodes":         json.Number(fmt.Sprintf("%d", spec.Nodes)),
		"groups":        json.Number(fmt.Sprintf("%d", spec.Groups)),
		"node_proto":    spec.NodeProto,
		"config_source": "static",
		"activity":      activity,
		"gc_mode":       spec.GCMode,
		"held_flows":    json.Number(fmt.Sprintf("%d", spec.HeldFlows)),
		"traffic_bytes": json.Number(fmt.Sprintf("%d", spec.TrafficBytes)),
	}
	record["identity"] = map[string]any{
		"source": map[string]any{
			"sha": testLedgerSHA, "dirty": false,
			"detected_by": "git-working-tree", "git_sha": testLedgerSHA, "git_dirty": false,
			"build_sha": testLedgerSHA, "build_dirty": false,
		},
		"build": map[string]any{
			"go_version": "go1.27.4", "goos": "windows", "goarch": "amd64",
			"tags": []any{"with_purego"},
		},
		"fixture": map[string]any{"version": fixtureVersion, "document_sha256": testFixtureDigest(spec)},
	}
	record["traffic"] = map[string]any{
		"verified":              true,
		"half_close_propagated": true,
		"flows":                 json.Number("4"),
	}
	for path, value := range values {
		setPath(record, path, value)
	}
	return record
}

// testLedgerSHA is the revision the synthetic ledgers claim. It is a fixed string because the unit
// tests are about the SELECTOR and the EVALUATOR, not about the checkout; the identity tests below
// pass their own HEAD explicitly.
const testLedgerSHA = "1111111111111111111111111111111111111111"

// testFixtureDigest is a stable per-spec document digest for synthetic ledgers.
func testFixtureDigest(spec runSpec) string {
	sum := sha256.Sum256([]byte(specSignature(spec)))
	return hex.EncodeToString(sum[:])
}

// syntheticGateLedger produces one record for every rule in the gate matrix, with a value slightly
// under a plausible threshold. It exists so the gate logic can be tested without a device, a
// network or a clock.
func syntheticGateLedger() []recordMap {
	specs := gateSpecs(false)
	byStage := func(spec runSpec) []recordMap {
		var records []recordMap
		specRecord := func(stage string, values map[string]any) {
			records = append(records, syntheticRecord(spec, stage, statusOK, values))
		}
		specRecord(stageEarliestObservable, map[string]any{
			"readings.go.live_heap_bytes": 1_400_000,
			"readings.go.total_bytes":     14_000_000,
			"readings.goroutines.current": 24,
			"readings.process.threads":    12,
		})
		specRecord(stageFixtureServers, map[string]any{
			"deltas.from_previous.live_heap_bytes": 12_000,
		})
		specRecord(stageConfigParse, map[string]any{
			"deltas.from_previous.live_heap_bytes": 280_000,
		})
		specRecord(stageConstruct, map[string]any{
			"deltas.from_previous.live_heap_bytes":       17_000,
			"deltas.from_entry.live_heap_bytes":          1_700_000,
			"deltas.from_entry.live_heap_per_node_bytes": 2_100,
		})
		specRecord(stageServiceStart, map[string]any{
			"deltas.from_previous.live_heap_bytes": 80_000,
		})
		for _, raw := range spec.Idle {
			specRecord(idleStage(raw), map[string]any{
				"readings.go.live_heap_bytes":                1_720_000,
				"readings.go.heap_objects_bytes":             1_730_000,
				"readings.go.total_bytes":                    14_100_000,
				"readings.goroutines.current":                27,
				"readings.process.threads":                   18,
				"deltas.from_previous.live_heap_bytes":       4_000,
				"deltas.from_previous.heap_objects_bytes":    4_096,
				"deltas.from_entry.live_heap_bytes":          330_000,
				"deltas.from_entry.live_heap_per_node_bytes": 2_200,
			})
		}
		if hasActivity(spec, "probe") {
			specRecord(stageProbe, map[string]any{"probe.succeeded": 8})
		}
		if hasActivity(spec, "traffic") {
			specRecord(stageTrafficHeld, map[string]any{
				"deltas.from_previous.live_heap_bytes": 47_000,
				"traffic.flows":                        4,
			})
			specRecord(stageTrafficVerified, map[string]any{
				"traffic.verified":              true,
				"traffic.half_close_propagated": true,
			})
		}
		if !allActivityNone(spec.Activity) {
			specRecord(idleAgainStage(spec.PostIdle), map[string]any{
				"deltas.from_previous.live_heap_bytes": -20_000,
			})
		}
		specRecord(stageClose, map[string]any{
			"readings.go.live_heap_bytes": 1_610_000,
		})
		specRecord(stageRunEnd, map[string]any{
			"readings.goroutines.current":       24,
			"deltas.from_entry.live_heap_bytes": 220_000,
		})
		return records
	}
	var ledger []recordMap
	for _, spec := range specs {
		ledger = append(ledger, byStage(spec)...)
	}
	return ledger
}

// TestBudgetDerivationAndEvaluation is the gate's own regression: it must pass a ledger that is at
// the baseline, fail one that regresses, and SKIP - never pass - what it cannot see.
func TestBudgetDerivationAndEvaluation(t *testing.T) {
	ledger := syntheticGateLedger()
	budget, err := deriveBudget(ledger, budgetProvenance{Kind: provenanceDerived})
	if err != nil {
		t.Fatalf("the baseline could not be turned into a budget: %v", err)
	}
	for _, rule := range budget.Rules {
		if rule.Min == nil && rule.Max == nil {
			t.Fatalf("rule %s has no threshold", rule.ID)
		}
		if rule.Min == nil && rule.Basis == "" {
			t.Fatalf("rule %s has no recorded basis", rule.ID)
		}
	}
	results := evaluateBudget(budget, ledger)
	for _, result := range results {
		if result.Status != statusOK {
			t.Errorf("rule %s did not pass on its own baseline: %s (%s)", result.ID, result.Status, result.Reason)
		}
	}

	// A regression must fail. The per-node rule is the one a node-descriptor change would move.
	regressed := syntheticGateLedger()
	for index, record := range regressed {
		if record.stage() != stageConstruct {
			continue
		}
		if record.specField("case") != "ordinary" || record.specField("nodes") != "100" {
			continue
		}
		perNode := record["deltas"].(map[string]any)["from_entry"].(map[string]any)
		perNode["live_heap_per_node_bytes"] = json.Number("9000")
		regressed[index] = record
	}
	foundFailure := false
	for _, result := range evaluateBudget(budget, regressed) {
		if result.ID == "ordinary-100-construct-per-node" && result.Status == statusFailed {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Fatal("doubling the per-node construction cost did not fail the per-node budget")
	}

	// A REQUIRED metric that the run did not produce must FAIL, not skip: a gate that treats missing
	// evidence as "not applicable" still reports success on the strength of the rules that did have
	// data, which is the silent-green failure this gate exists to prevent. The audit reproduced
	// exactly that with the traffic rules removed from the ledger.
	unobservable := syntheticGateLedger()
	for index, record := range unobservable {
		if record.stage() != stageTrafficVerified {
			continue
		}
		record["traffic"] = map[string]any{"flows": json.Number("4")}
		unobservable[index] = record
	}
	sawRequiredFailure := false
	for _, result := range evaluateBudget(budget, unobservable) {
		if result.ID != "traffic-verified" {
			continue
		}
		if result.Status != statusFailed {
			t.Fatalf("a null required traffic.verified was evaluated as %s, want failed", result.Status)
		}
		if result.Reason == "" {
			t.Fatal("a failed rule must say why")
		}
		sawRequiredFailure = true
	}
	if !sawRequiredFailure {
		t.Fatal("the traffic rule was not evaluated")
	}

	// The same null value on a rule that opted OUT of being required is a skip again: that is the
	// one legitimate use of unsupported, a platform metric this budget knows it cannot answer.
	optional := budget
	optional.Rules = append([]budgetRule(nil), budget.Rules...)
	for index := range optional.Rules {
		if optional.Rules[index].ID == "traffic-verified" {
			optional.Rules[index].Required = false
		}
	}
	sawOptionalSkip := false
	for _, result := range evaluateBudget(optional, unobservable) {
		if result.ID != "traffic-verified" {
			continue
		}
		if result.Status != statusUnsupported {
			t.Fatalf("a null non-required traffic.verified was evaluated as %s, want unsupported", result.Status)
		}
		if result.Reason == "" {
			t.Fatal("a skipped rule must say why")
		}
		sawOptionalSkip = true
	}
	if !sawOptionalSkip {
		t.Fatal("the optional traffic rule was not evaluated")
	}

	// A foreign platform's budget is a skip, not a failure and not a pass.
	foreign := budget
	foreign.Baseline.GOOS = "plan9"
	for _, result := range evaluateBudget(foreign, ledger) {
		if result.Status != statusUnsupported {
			t.Fatalf("rule %s was evaluated against a plan9 baseline as %s", result.ID, result.Status)
		}
	}
}

// TestDeriveBudgetRefusesAnUnmeasuredRule: a threshold invented for a metric nothing produced is a
// gate that cannot detect anything, so the derivation must fail instead of inventing one.
func TestDeriveBudgetRefusesAnUnmeasuredRule(t *testing.T) {
	ledger := syntheticGateLedger()
	var truncated []recordMap
	for _, record := range ledger {
		if record.stage() == stageTrafficHeld {
			continue
		}
		truncated = append(truncated, record)
	}
	if _, err := deriveBudget(truncated, budgetProvenance{Kind: provenanceDerived}); err == nil {
		t.Fatal("a budget was derived without any held-flow observation")
	}
}

// TestGCSpecModesPlanSeparateProcesses: natural and forced are separate child processes, and the
// planner must never produce one run that claims both.
func TestGCSpecModesPlanSeparateProcesses(t *testing.T) {
	flags := &runFlags{caseName: "empty", nodes: 0, groups: 0, nodeProto: "direct",
		activity: "none", idle: "0s", postIdle: "0s", gcMode: "both", heldFlows: 4,
		trafficBytes: 1024, runs: 1}
	specs, err := planSpecs(flags)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("-gc both planned %d runs, want 2", len(specs))
	}
	modes := map[string]bool{}
	for _, spec := range specs {
		modes[spec.GCMode] = true
	}
	if !modes["natural"] || !modes["forced"] {
		t.Fatalf("the plan does not contain both disciplines: %v", modes)
	}
}

// TestParseDurationListRejectsDecreasing: idle checkpoints are cumulative, so a decreasing list
// silently measures a shorter interval than the stage name claims.
func TestParseDurationListRejectsDecreasing(t *testing.T) {
	if _, err := parseDurationList("300s,30s"); err == nil {
		t.Fatal("a decreasing idle list was accepted; the stage names would lie")
	}
	list, err := parseDurationList("0s,30s,300s")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("parsed %v", list)
	}
}
