package libbox

// O02 — the bounded ACTIVE connection test.
//
// ExplainPath answers "what does the model say" without side effects. This entry is the
// active half: it dials the path once, inside its own budget, and reports what happened as
// a tree of stages with stable ids, so a diagnostic can name the stage that failed instead
// of returning one opaque error.
//
// # What it is not
//
// It writes no health evidence: the URLTest history is not touched, so a user pressing
// "diagnose" can never move a live selection. It changes nothing (no selection, no
// connection pool state beyond the dial it opened and closed), and it reports the passive
// path it walked as its first stage so the active result is always attached to a model.
//
// # Bounds
//
// The whole test is bounded (connectionTestTotalTimeout) and each stage is bounded
// (connectionTestStageTimeout); the dial is bounded by the remaining total budget. There is
// exactly one dial and no background goroutine, so the report is the complete truth when it
// returns — the streaming/subscription form is the next slice, not this one.

import (
	"context"
	stdjson "encoding/json"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/physicalpath"
	"github.com/sagernet/sing-box/route"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// The two budgets are variables rather than constants so a test can shrink them; production
// uses the values below.
var (
	// connectionTestStageTimeout bounds one stage, so a wedged stage cannot consume the
	// whole report.
	connectionTestStageTimeout = 5 * time.Second
	// connectionTestTotalTimeout bounds the whole test, including every stage.
	connectionTestTotalTimeout = 10 * time.Second
)

// connectionTestStageStatus is the vocabulary a caller can rely on.
const (
	connectionTestStageOK      = "ok"
	connectionTestStageFailed  = "failed"
	connectionTestStageSkipped = "skipped"
)

// connectionTestOutcome is the report-level verdict.
const (
	connectionTestOutcomeOK        = "ok"
	connectionTestOutcomeFailed    = "failed"
	connectionTestOutcomeCancelled = "cancelled"
)

// Stable stage ids. They are part of the diagnostic contract: a client may key UI,
// translations or a support checklist on them, so they must not be renamed.
const (
	connectionTestStagePath    = "path"
	connectionTestStageRules   = "rules"
	connectionTestStageDNS     = "dns"
	connectionTestStageResolve = "resolve"
	connectionTestStageDial    = "dial"
)

type connectionTestStage struct {
	ID         string `json:"id"`
	Parent     string `json:"parent,omitempty"`
	Status     string `json:"status"`
	DurationMs int64  `json:"durationMs"`
	Detail     string `json:"detail,omitempty"`
}

type connectionTestReport struct {
	Root    string `json:"root"`
	Network string `json:"network"`
	Target  string `json:"target"`
	Outcome string `json:"outcome"`
	// Routing states WHICH dial semantics this run used, because the two are not interchangeable
	// and a report that mixed them silently could explain a rule it never dialled through:
	//
	//   - "root": dial the fixed root the caller named (the compatibility contract of this entry);
	//   - "rule": dial the outbound the ROUTE DECISION selects for the target (real routing).
	Routing string                `json:"routing"`
	Stages  []connectionTestStage `json:"stages"`
	// Truncated reports that one or more fields were shortened to keep the encoded report within
	// its ceiling. A reader must treat the affected stage detail as incomplete rather than as the
	// whole fact.
	Truncated bool `json:"truncated,omitempty"`
	// Events is the bounded narrative of the test (begin, per-stage facts, final snapshot,
	// end). It is the same stream a live subscriber receives, retained for a late reader.
	Events  []string `json:"events,omitempty"`
	TotalMs int64    `json:"totalMs"`
}

const (
	// connectionTestRoutingRoot dials the caller's fixed root. This is the original contract of
	// ConnectionTest and is kept for every caller that names one.
	connectionTestRoutingRoot = "root"
	// connectionTestRoutingRule dials the outbound the route decision selects for the target. It is
	// what an empty root asks for: "test the path this request would actually take".
	connectionTestRoutingRule = "rule"
)

// connectionTestPath is the slice of the running Box the test needs: the passive status
// model and the outbound namespace. *box.Box satisfies it; tests supply a fake.
type connectionTestPath interface {
	Status(root string, network string) physicalpath.PathStatus
	Outbound() adapter.OutboundManager
}

// connectionTestRulePreviewer is the OPTIONAL extra a path may offer: which route rule decides a
// request for this host. A path that cannot answer it gets a skipped stage with the reason, never a
// guessed decision - "unknown" is a fact a diagnostic is allowed to report.
type connectionTestRulePreviewer interface {
	PreviewRouteRule(network string, domain string, port uint16) (connectionTestRulePreview, bool)
}

// connectionTestRulePreview is that answer, flattened for the report.
type connectionTestRulePreview struct {
	Matched    bool
	RuleIndex  int
	Rule       string
	ActionType string
	Outbound   string
	Unknown    string
	// FinalDestination is where the route decision would DIAL: the request's destination after the
	// matched action's override_address/override_port were applied. A diagnostic that ignores it
	// connects to the text the user typed instead of the endpoint the route chose.
	FinalDestination M.Socksaddr
}

// connectionTestDomainResolver is the OPTIONAL capability that resolves a name through the box's
// real DNS path (cache, strategy and rules included). The DNS STAGE above is a passive preview; a
// diagnostic that must dial an address needs the active answer, and it says so in the report.
type connectionTestDomainResolver interface {
	ResolveDomain(ctx context.Context, domain string) (netip.Addr, error)
}

// connectionTestDNSPreviewer is the OPTIONAL capability that explains which DNS rule and server
// would answer the target's address query. A path that cannot answer gets a skipped stage with the
// reason, never a guessed server.
type connectionTestDNSPreviewer interface {
	PreviewDNSRule(network string, domain string, port uint16) (connectionTestDNSPreview, bool)
}

// connectionTestRouteDialer is the OPTIONAL capability that runs the PRODUCTION route decision for
// one socketless request and dials its result.
//
// It replaces the previous composition for empty-root requests (a read-only rule preview, a default
// DNS lookup and a direct dial of the caller's text). The third result reports whether the build can
// execute the decision at all: a path without it gets skipped stages and no dial, never a guess.
//
// One call performs the whole decision and the dial, so the rule, the DNS action, the address
// candidates and the connection all belong to the same execution.
type connectionTestRouteDialer interface {
	DialRoute(ctx context.Context, request route.RouteDiagnoseRequest) (route.RouteDiagnoseResult, error, bool)
}

// connectionTestDNSPreview is that answer, flattened for the report.
type connectionTestDNSPreview struct {
	Matched    bool
	RuleIndex  int
	Rule       string
	ActionType string
	Server     string
	ServerType string
	Unknown    string
}

// ConnectionTest runs one bounded active test from root over network to target and returns
// the report as JSON. A nil server, a stopped service or a stopped box is an error before
// any dial starts.
func (s *CommandServer) ConnectionTest(root string, network string, target string) (*StringBox, error) {
	if s == nil || s.StartedService == nil {
		return nil, E.New("command server is not started")
	}
	instance := s.Instance()
	if instance == nil {
		return nil, E.New("service is not running")
	}
	boxInstance := instance.Box()
	if boxInstance == nil {
		return nil, E.New("box is not running")
	}
	report := runConnectionTest(context.Background(), newBoxConnectionTestPath(boxInstance), root, network, target)
	return wrapString(marshalConnectionTestReport(report)), nil
}

// dialOutcome is one settled dial.
type dialOutcome struct {
	conn net.Conn
	err  error
}

// runConnectionTest is the bounded runner, separated from the gomobile entry so it can be
// driven with a fake path and a caller context.
func runConnectionTest(ctx context.Context, path connectionTestPath, root string, network string, target string) connectionTestReport {
	return runConnectionTestWithEmitter(ctx, path, root, network, target, nil)
}

// runConnectionTestWithEmitter runs the test against a caller-supplied emitter, so a live
// subscriber and the final report describe the same run.
func runConnectionTestWithEmitter(ctx context.Context, path connectionTestPath, root string, network string, target string, emitter *connectionTestEmitter) (report connectionTestReport) {
	started := time.Now()
	report = connectionTestReport{Root: root, Network: network, Target: target, Routing: connectionTestRoutingRoot}
	if root == "" {
		// "No root" is not "the default root": it asks the route decision where this request would
		// go. The report says which semantics ran so a reader cannot confuse the two.
		report.Routing = connectionTestRoutingRule
	}
	// This diagnostic is a TCP connect through an outbound. A UDP/ICMP request cannot be expressed
	// by it, and pretending otherwise would produce a green report for something it never did.
	if network != "" && network != N.NetworkTCP {
		report.Outcome = connectionTestOutcomeFailed
		report.Stages = append(report.Stages, connectionTestStage{
			ID:     connectionTestStagePath,
			Status: connectionTestStageFailed,
			Detail: "the connection test can only test TCP; requested " + network,
		})
		report.TotalMs = time.Since(started).Milliseconds()
		return report
	}
	if emitter == nil {
		emitter = newConnectionTestEmitter(defaultEmitterBounds(), started)
	}
	// Close finalises the stream on EVERY return path, and the snapshot is attached to the
	// report: a caller that never subscribed still gets the narrative, and a subscriber that
	// did is guaranteed an end.
	defer func() {
		// The stage facts are emitted from the settled report: every return path has already
		// recorded what ran, what was skipped and why, so the narrative cannot disagree with the
		// report the caller receives.
		for _, stage := range report.Stages {
			emitter.Publish(connectionTestEvent{
				Kind:    eventStage,
				Stage:   stage.ID,
				Parent:  stage.Parent,
				Outcome: outcomeForStage(stage.Status),
				Detail:  stage.Detail,
				Elapsed: stage.DurationMs,
			})
		}
		emitter.Publish(connectionTestEvent{Kind: eventFinding, Outcome: outcomeReused, Detail: "passive status reused for the path stage", Elapsed: time.Since(started).Milliseconds()})
		emitter.Close(time.Since(started))
		for _, event := range emitter.Snapshot() {
			report.Events = append(report.Events, renderEvent(event))
		}
	}()
	emitter.Publish(connectionTestEvent{Kind: eventBegin, Name: "connection test", Detail: root + " " + network + " " + target, Outcome: outcomeRunning})

	budgetCtx, cancelBudget := context.WithTimeout(ctx, connectionTestTotalTimeout)
	defer cancelBudget()

	// Stage 1: the passive path. This is a value read; it cannot fail the test by itself,
	// but a path with no exit means there is nothing to dial, and that is a finding rather
	// than an error string.
	stageStarted := time.Now()
	status := path.Status(root, network)
	pathStage := connectionTestStage{
		ID:         connectionTestStagePath,
		Status:     connectionTestStageOK,
		DurationMs: time.Since(stageStarted).Milliseconds(),
	}
	switch {
	case report.Routing == connectionTestRoutingRule:
		// Real-routing mode has no fixed root to validate. Skipping (not failing) the stage is what
		// keeps the test describing the request's own path; the rule stage below supplies the exit.
		pathStage.Status = connectionTestStageSkipped
		pathStage.Detail = "real-routing mode: the dial uses the route decision for the target, not a fixed root"
	case status.Exit != "":
		pathStage.Detail = "exit " + status.Exit
	case status.Failure != nil:
		pathStage.Status = connectionTestStageFailed
		pathStage.Detail = "path failure: " + status.Failure.Detail
	case len(status.Unknowns) > 0:
		pathStage.Status = connectionTestStageFailed
		pathStage.Detail = status.Unknowns[0].Reason
	default:
		pathStage.Status = connectionTestStageFailed
		pathStage.Detail = "the path has no exit"
	}
	report.Stages = append(report.Stages, pathStage)

	host, port := connectionTestTargetHost(target)

	// Real-routing mode: ONE execution decides and dials.
	//
	// The decision, the DNS action and the dial used to be three independent calls, so the report
	// could name a rule, a resolver and an address that production would never have used together.
	// The capability below runs the production walk and dials ITS result, and every stage is then a
	// fact read from that execution instead of a re-derivation.
	if report.Routing == connectionTestRoutingRule {
		runRuleDecisionStages(ctx, budgetCtx, &report, path, host, port, target)
		report.TotalMs = time.Since(started).Milliseconds()
		return report
	}

	// Root mode keeps its compatibility contract: it explains the request, dials the FIXED root the
	// caller named, and says so when the route decision would go elsewhere.
	stageStarted = time.Now()
	rulesStage := connectionTestStage{ID: connectionTestStageRules, Parent: connectionTestStagePath}
	previewer, canPreview := path.(connectionTestRulePreviewer)
	var (
		rulePreview connectionTestRulePreview
		previewed   bool
	)
	switch {
	case !canPreview:
		rulesStage.Status = connectionTestStageSkipped
		rulesStage.Detail = "this build cannot explain the route rule for a running box"
	case host == "":
		rulesStage.Status = connectionTestStageSkipped
		rulesStage.Detail = "the target does not name a host"
	default:
		rulePreview, previewed = previewer.PreviewRouteRule(network, host, port)
		switch {
		case !previewed:
			rulesStage.Status = connectionTestStageSkipped
			rulesStage.Detail = "the running box has no route rule preview"
		case rulePreview.Unknown != "":
			// The preview is honest about what it could not decide; the stage reports that instead
			// of inventing a rule.
			rulesStage.Status = connectionTestStageSkipped
			rulesStage.Detail = rulePreview.Unknown
		case rulePreview.Matched:
			rulesStage.Status = connectionTestStageOK
			rulesStage.Detail = rulePreviewDetail(rulePreview)
			if rulePreview.Outbound != "" && rulePreview.Outbound != status.Exit {
				// The two semantics disagree, and saying so is the point of reporting Routing: this
				// run dialled the caller's root, while the request would be routed elsewhere.
				rulesStage.Detail += "; this test dials root " + status.Exit + " instead"
			}
		default:
			rulesStage.Status = connectionTestStageOK
			rulesStage.Detail = "no rule matched: the final outbound decides"
		}
	}
	rulesStage.DurationMs = time.Since(stageStarted).Milliseconds()
	report.Stages = append(report.Stages, rulesStage)

	// Stage 2b: which DNS rule and server would answer the target's address query.
	//
	// This is a separate stage rather than part of "rules": the route rule decides whether the
	// connection is dialled at all, while the DNS rule decides how the name is resolved before
	// any dial. Reporting them together would have made one stage answer two questions, and the
	// explanation of a failing resolution would have looked like a routing decision.
	stageStarted = time.Now()
	dnsStage := connectionTestStage{ID: connectionTestStageDNS, Parent: connectionTestStageRules}
	dnsPreviewer, canPreviewDNS := path.(connectionTestDNSPreviewer)
	switch {
	case connectionTestLiteralAddress(host):
		// A literal address never reaches a DNS rule. Reporting a default DNS server for it was
		// one of the reviewed inaccuracies.
		dnsStage.Status = connectionTestStageSkipped
		dnsStage.Detail = "the target is a literal address: no DNS decision applies"
	case !canPreviewDNS:
		dnsStage.Status = connectionTestStageSkipped
		dnsStage.Detail = "this build cannot explain the DNS rule for a running box"
	case host == "":
		dnsStage.Status = connectionTestStageSkipped
		dnsStage.Detail = "the target does not name a host"
	default:
		dnsPreview, dnsPreviewed := dnsPreviewer.PreviewDNSRule(network, host, port)
		switch {
		case !dnsPreviewed:
			dnsStage.Status = connectionTestStageSkipped
			dnsStage.Detail = "the running box has no DNS rule preview"
		case dnsPreview.Unknown != "":
			dnsStage.Status = connectionTestStageSkipped
			dnsStage.Detail = dnsPreview.Unknown
		case dnsPreview.Matched:
			dnsStage.Status = connectionTestStageOK
			// "preview" is part of the fact: root mode did not resolve anything, so this is what a
			// DNS query would hit, not what this run observed.
			dnsStage.Detail = "preview: " + dnsPreviewDetail(dnsPreview)
		default:
			dnsStage.Status = connectionTestStageOK
			dnsStage.Detail = "preview: no rule matched; the default DNS server would answer a local lookup"
		}
	}
	dnsStage.DurationMs = time.Since(stageStarted).Milliseconds()
	report.Stages = append(report.Stages, dnsStage)

	// Stage 3: resolve the target into the address the dial will use.
	stageStarted = time.Now()
	address, resolveErr := connectionTestAddress(target)
	resolveStage := connectionTestStage{
		ID:         connectionTestStageResolve,
		Parent:     connectionTestStagePath,
		Status:     connectionTestStageOK,
		DurationMs: time.Since(stageStarted).Milliseconds(),
	}
	if resolveErr != nil {
		resolveStage.Status = connectionTestStageFailed
		resolveStage.Detail = resolveErr.Error()
		report.Stages = append(report.Stages, resolveStage)
		report.Outcome = connectionTestOutcomeFailed
		report.TotalMs = time.Since(started).Milliseconds()
		return report
	}
	resolveStage.Detail = address.String()
	report.Stages = append(report.Stages, resolveStage)

	// Stage 4: the dial, through the root the caller named.
	dialStage := connectionTestStage{ID: connectionTestStageDial, Parent: connectionTestStagePath}
	if pathStage.Status != connectionTestStageOK {
		dialStage.Status = connectionTestStageSkipped
		dialStage.Detail = "no exit to dial through"
		report.Stages = append(report.Stages, dialStage)
		report.Outcome = connectionTestOutcomeFailed
		report.TotalMs = time.Since(started).Milliseconds()
		return report
	}
	outbound, loaded := path.Outbound().Outbound(status.Exit)
	if !loaded || outbound == nil {
		dialStage.Status = connectionTestStageSkipped
		dialStage.Detail = "the exit is not an outbound: " + status.Exit
		report.Stages = append(report.Stages, dialStage)
		report.Outcome = connectionTestOutcomeFailed
		report.TotalMs = time.Since(started).Milliseconds()
		return report
	}

	stageStarted = time.Now()
	outcome, answered := runBoundedOutboundDial(ctx, budgetCtx, outbound, network, address)
	dialStage.DurationMs = time.Since(stageStarted).Milliseconds()
	switch {
	case !answered && ctx.Err() != nil:
		dialStage.Status = connectionTestStageFailed
		dialStage.Detail = "cancelled: " + ctx.Err().Error()
		report.Outcome = connectionTestOutcomeCancelled
	case !answered:
		dialStage.Status = connectionTestStageFailed
		dialStage.Detail = "the stage budget expired before the dial answered"
		report.Outcome = connectionTestOutcomeFailed
	case outcome.err == nil:
		dialStage.Status = connectionTestStageOK
		dialStage.Detail = "connected via " + outbound.Tag() + " to " + address.String()
		if outcome.conn != nil {
			_ = outcome.conn.Close()
		}
		report.Outcome = connectionTestOutcomeOK
	default:
		dialStage.Status = connectionTestStageFailed
		dialStage.Detail = outcome.err.Error()
		report.Outcome = connectionTestOutcomeFailed
	}
	report.Stages = append(report.Stages, dialStage)
	report.TotalMs = time.Since(started).Milliseconds()
	return report
}

// runRuleDecisionStages runs the production decision for an empty-root request and reports every
// stage from that ONE execution.
func runRuleDecisionStages(ctx context.Context, budgetCtx context.Context, report *connectionTestReport, path connectionTestPath, host string, port uint16, target string) {
	rulesStage := connectionTestStage{ID: connectionTestStageRules, Parent: connectionTestStagePath}
	dnsStage := connectionTestStage{ID: connectionTestStageDNS, Parent: connectionTestStageRules}
	resolveStage := connectionTestStage{ID: connectionTestStageResolve, Parent: connectionTestStagePath}
	dialStage := connectionTestStage{ID: connectionTestStageDial, Parent: connectionTestStagePath}

	appendAll := func() {
		report.Stages = append(report.Stages, rulesStage, dnsStage, resolveStage, dialStage)
	}
	stageStarted := time.Now()
	destination, parseErr := connectionTestAddress(target)
	if parseErr != nil {
		rulesStage.Status = connectionTestStageSkipped
		rulesStage.Detail = "the target could not be parsed, so there is no request to decide"
		dnsStage.Status = connectionTestStageSkipped
		dnsStage.Detail = "the target could not be parsed"
		resolveStage.Status = connectionTestStageFailed
		resolveStage.Detail = parseErr.Error()
		dialStage.Status = connectionTestStageSkipped
		dialStage.Detail = "nothing to dial"
		appendAll()
		report.Outcome = connectionTestOutcomeFailed
		return
	}
	resolveStage.DurationMs = time.Since(stageStarted).Milliseconds()

	dialRoute, canDialRoute := path.(connectionTestRouteDialer)
	if !canDialRoute {
		rulesStage.Status = connectionTestStageSkipped
		rulesStage.Detail = "this build cannot execute the production route decision for a running box"
		dnsStage.Status = connectionTestStageSkipped
		dnsStage.Detail = "the route decision could not be executed, so no DNS decision was observed"
		resolveStage.Status = connectionTestStageOK
		resolveStage.Detail = destination.String()
		dialStage.Status = connectionTestStageSkipped
		dialStage.Detail = "real-routing mode needs the production decision, which this build cannot execute"
		appendAll()
		report.Outcome = connectionTestOutcomeFailed
		return
	}

	request := route.RouteDiagnoseRequest{
		Network:     report.Network,
		Destination: destination,
		UserId:      -1,
	}
	if destination.IsDomain() {
		request.Domain = destination.Fqdn
	}

	// One bounded execution: the walk, its DNS action and the dial all run inside this call, so the
	// report cannot mix facts from different decisions.
	decisionCtx, cancelDecision := context.WithTimeout(budgetCtx, connectionTestStageTimeout)
	defer cancelDecision()
	type decisionOutcome struct {
		result route.RouteDiagnoseResult
		err    error
	}
	settled := make(chan decisionOutcome, 1)
	executionStarted := time.Now()
	go func() {
		result, err, _ := dialRoute.DialRoute(decisionCtx, request)
		settled <- decisionOutcome{result: result, err: err}
	}()
	var outcome decisionOutcome
	answered := true
	select {
	case outcome = <-settled:
	case <-decisionCtx.Done():
		answered = false
		// A decision that ignores its context must not hold the report; whatever it eventually
		// returns is closed by the waiter rather than leaked.
		go func() {
			late := <-settled
			if late.result.Conn != nil {
				_ = late.result.Conn.Close()
			}
		}()
	}
	executionMs := time.Since(executionStarted).Milliseconds()

	if !answered {
		rulesStage.Status = connectionTestStageSkipped
		rulesStage.Detail = "the production decision did not complete within the stage budget"
		dnsStage.Status = connectionTestStageSkipped
		dnsStage.Detail = "the production decision did not complete"
		resolveStage.Status = connectionTestStageSkipped
		resolveStage.Detail = "the production decision did not complete"
		dialStage.Status = connectionTestStageFailed
		dialStage.DurationMs = executionMs
		if ctx.Err() != nil {
			dialStage.Detail = "cancelled: " + ctx.Err().Error()
			report.Outcome = connectionTestOutcomeCancelled
		} else {
			dialStage.Detail = "the stage budget expired before the decision answered"
			report.Outcome = connectionTestOutcomeFailed
		}
		appendAll()
		return
	}
	if outcome.err != nil {
		rulesStage.Status = connectionTestStageFailed
		rulesStage.Detail = outcome.err.Error()
		dnsStage.Status = connectionTestStageSkipped
		dnsStage.Detail = "the production decision failed"
		resolveStage.Status = connectionTestStageSkipped
		resolveStage.Detail = "the production decision failed"
		dialStage.Status = connectionTestStageSkipped
		dialStage.Detail = "nothing to dial through"
		appendAll()
		report.Outcome = connectionTestOutcomeFailed
		return
	}

	result := outcome.result
	// Stage facts from the execution. The durations are the execution's own phase timings, so the
	// report can still show where the time went.
	rulesStage.DurationMs = result.WalkMs
	if rulesStage.DurationMs == 0 {
		rulesStage.DurationMs = executionMs
	}
	dnsStage.DurationMs = result.ResolveMs
	resolveStage.DurationMs = result.ResolveMs
	dialStage.DurationMs = result.DialMs

	literalTarget := connectionTestLiteralAddress(host)
	switch {
	case result.Unknown != "":
		rulesStage.Status = connectionTestStageSkipped
		rulesStage.Detail = result.Unknown
	case result.Rejected:
		rulesStage.Status = connectionTestStageOK
		rulesStage.Detail = decisionDetail(result)
	case result.Matched:
		rulesStage.Status = connectionTestStageOK
		rulesStage.Detail = decisionDetail(result)
	default:
		rulesStage.Status = connectionTestStageOK
		rulesStage.Detail = "no rule matched: the final outbound " + result.Outbound + " decides"
	}

	switch {
	case result.Resolve.Performed:
		// ACTUAL DNS: the walk ran the resolve action and this is what it answered. Not a preview.
		dnsStage.Status = connectionTestStageOK
		dnsStage.Detail = "actual: resolved via " + resolveLabel(result.Resolve) + " -> " + addressList(result.Resolve.Addresses)
	case literalTarget && !result.FinalDestination.IsDomain():
		dnsStage.Status = connectionTestStageSkipped
		dnsStage.Detail = "the target is a literal address: no DNS decision applies"
	case result.FinalDestination.IsIP() && result.OriginalDestination.IsDomain():
		// The decision rewrote a name into a literal: nothing is resolved, and saying "the default
		// server would answer" would name a server that is never asked.
		dnsStage.Status = connectionTestStageSkipped
		dnsStage.Detail = "the route decision rewrote the destination to " + result.FinalDestination.String() + ": no DNS decision applies"
	case result.FinalDestination.IsDomain() && !result.FinalDestination.Addr.IsValid() && result.OriginalDestination.IsIP():
		// The decision rewrote a literal into a name; the dial resolves it, but not here.
		dnsStage.Status = connectionTestStageSkipped
		dnsStage.Detail = "the route decision rewrote the destination to " + result.FinalDestination.String() + ": no DNS decision applies to the original target"
	case result.Unknown != "" || result.Rejected:
		dnsStage.Status = connectionTestStageSkipped
		dnsStage.Detail = "the route decision did not reach a DNS question"
	default:
		dnsPreviewer, canPreviewDNS := path.(connectionTestDNSPreviewer)
		switch {
		case !canPreviewDNS || host == "":
			dnsStage.Status = connectionTestStageSkipped
			dnsStage.Detail = "this build cannot explain the DNS rule for a running box"
		default:
			dnsPreview, dnsPreviewed := dnsPreviewer.PreviewDNSRule(report.Network, host, port)
			switch {
			case !dnsPreviewed:
				dnsStage.Status = connectionTestStageSkipped
				dnsStage.Detail = "the running box has no DNS rule preview"
			case dnsPreview.Unknown != "":
				dnsStage.Status = connectionTestStageSkipped
				dnsStage.Detail = dnsPreview.Unknown
			case dnsPreview.Matched:
				dnsStage.Status = connectionTestStageOK
				dnsStage.Detail = "preview: " + dnsPreviewDetail(dnsPreview)
			default:
				dnsStage.Status = connectionTestStageOK
				dnsStage.Detail = "preview: no rule matched; the default DNS server would answer a local lookup"
			}
		}
	}

	switch {
	case result.Resolve.Performed:
		resolveStage.Status = connectionTestStageOK
		resolveStage.Detail = "'" + host + "' -> " + addressList(result.DestinationAddresses) + " (route decision)"
	case result.Rejected || result.Unknown != "":
		resolveStage.Status = connectionTestStageSkipped
		resolveStage.Detail = "the route decision does not dial this request"
	case result.FinalDestination.IsDomain():
		resolveStage.Status = connectionTestStageOK
		resolveStage.Detail = "the outbound keeps the name '" + result.FinalDestination.Fqdn + "' and resolves it at dial time"
	default:
		resolveStage.Status = connectionTestStageOK
		resolveStage.Detail = "route destination " + result.FinalDestination.String()
	}

	switch {
	case result.Rejected:
		dialStage.Status = connectionTestStageSkipped
		dialStage.Detail = "the route decision rejects this request: " + result.RejectDetail
		report.Outcome = connectionTestOutcomeFailed
	case result.Unknown != "":
		dialStage.Status = connectionTestStageSkipped
		dialStage.Detail = "the route decision is unknown: " + result.Unknown
		report.Outcome = connectionTestOutcomeFailed
	case !result.Dialed:
		dialStage.Status = connectionTestStageSkipped
		dialStage.Detail = "the decision selected no dialable destination"
		report.Outcome = connectionTestOutcomeFailed
	case result.DialError == nil:
		dialStage.Status = connectionTestStageOK
		dialStage.Detail = diagnoseSuccessDetail(result)
		if result.Conn != nil {
			_ = result.Conn.Close()
		}
		report.Outcome = connectionTestOutcomeOK
	default:
		dialStage.Status = connectionTestStageFailed
		dialStage.Detail = result.DialError.Error()
		report.Outcome = connectionTestOutcomeFailed
	}
	appendAll()
}

// diagnoseSuccessDetail renders the dial stage from what the connection actually did.
//
// The endpoint named is the one that answered, not the candidate list the decision started with: a
// diagnostic that reports a dead candidate as "connected" is the reviewed inaccuracy. When the
// endpoint cannot be observed - a proxy's target, or a member a failover group chose - the detail
// says so and reports the peer separately; the pre-selected leaf is only named as such.
func diagnoseSuccessDetail(result route.RouteDiagnoseResult) string {
	var detail string
	switch {
	case result.DialedOutboundKnown && result.DialedOutbound != "":
		detail = "connected via " + result.DialedOutbound
	case result.DialedOutbound != "":
		detail = "connected via the group that owns the dial (pre-selected " + result.DialedOutbound + ")"
	default:
		detail = "connected"
	}
	if result.DialedEndpoint != "" {
		detail += " to " + result.DialedEndpoint
	} else if result.DialedDestination != "" {
		detail += " toward " + result.DialedDestination
	}
	detail += " (route decision)"
	if result.DialedEndpoint == "" && result.DialedEndpointUnknown != "" {
		detail += "; " + result.DialedEndpointUnknown
	}
	if result.DialedPeer != "" && result.DialedPeer != result.DialedEndpoint {
		detail += "; peer " + result.DialedPeer
	}
	return detail
}

// decisionDetail renders one decided request.
func decisionDetail(result route.RouteDiagnoseResult) string {
	detail := "rule[" + strconv.Itoa(result.RuleIndex) + "]"
	if result.Rule != "" {
		detail += " " + result.Rule
	}
	if result.ActionType != "" {
		detail += " -> " + result.ActionType
	}
	if result.Outbound != "" {
		detail += " " + result.Outbound
	}
	return detail
}

// addressList renders resolved addresses as a candidate list.
func addressList(addresses []netip.Addr) string {
	if len(addresses) == 0 {
		return "no address"
	}
	rendered := make([]string, 0, len(addresses))
	for _, address := range addresses {
		rendered = append(rendered, address.String())
	}
	return "[" + strings.Join(rendered, ",") + "]"
}

// resolveLabel renders the resolver the walk actually named, without inventing one: an action with
// no configured server is reported as the default resolver.
func resolveLabel(resolve route.RouteDiagnoseResolve) string {
	if resolve.Server == "" {
		return "the default resolver"
	}
	label := "server " + resolve.Server
	if resolve.ServerType != "" {
		label += " (" + resolve.ServerType + ")"
	}
	return label
}

// runBoundedOutboundDial performs one dial inside the stage budget for the root-mode contract.
//
// A dial that ignores its context is abandoned by the report, and whatever it eventually returns is
// closed by the waiter instead of leaking.
func runBoundedOutboundDial(ctx context.Context, budgetCtx context.Context, outbound adapter.Outbound, network string, destination M.Socksaddr) (dialOutcome, bool) {
	dialCtx, cancelDial := context.WithTimeout(budgetCtx, connectionTestStageTimeout)
	defer cancelDial()
	settled := make(chan dialOutcome, 1)
	go func() {
		conn, dialErr := outbound.DialContext(dialCtx, network, destination)
		settled <- dialOutcome{conn: conn, err: dialErr}
	}()
	select {
	case outcome := <-settled:
		return outcome, true
	case <-dialCtx.Done():
		go func() {
			late := <-settled
			if late.conn != nil {
				_ = late.conn.Close()
			}
		}()
		return dialOutcome{}, false
	}
}

// connectionTestLiteralAddress reports whether the target host is a literal IP. A literal never
// reaches a DNS rule, so the DNS stage must say "no decision applies" rather than name a default
// server.
func connectionTestLiteralAddress(host string) bool {
	if host == "" {
		return false
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsValid()
}

// connectionTestAddress turns the configured target into the address a TCP dial uses. Only
// http/https are accepted: this test measures the PROXY PATH, so it needs a plain
// stream target, and accepting any scheme would silently measure something else.
// connectionTestTargetHost extracts the host and port the rule preview needs. It is deliberately
// separate from connectionTestAddress: the preview runs BEFORE resolution, so it must not depend on
// a lookup succeeding, and a target without a port still names a host a rule can match.
func connectionTestTargetHost(target string) (string, uint16) {
	parsed, err := url.Parse(target)
	if err != nil {
		return "", 0
	}
	host := parsed.Hostname()
	if host == "" {
		return "", 0
	}
	if parsed.Port() != "" {
		port, portErr := strconv.ParseUint(parsed.Port(), 10, 16)
		if portErr != nil {
			return host, 0
		}
		return host, uint16(port)
	}
	if parsed.Scheme == "https" {
		return host, 443
	}
	return host, 80
}

// rulePreviewDetail renders the matched rule for the report without leaking anything the preview
// did not decide.
func rulePreviewDetail(preview connectionTestRulePreview) string {
	detail := "rule[" + strconv.Itoa(preview.RuleIndex) + "]"
	if preview.Rule != "" {
		detail += " " + preview.Rule
	}
	if preview.ActionType != "" {
		detail += " -> " + preview.ActionType
	}
	if preview.Outbound != "" {
		detail += " " + preview.Outbound
	}
	return detail
}

// dnsPreviewDetail renders the DNS decision for the report without inventing a server.
func dnsPreviewDetail(preview connectionTestDNSPreview) string {
	detail := "rule[" + strconv.Itoa(preview.RuleIndex) + "]"
	if preview.Rule != "" {
		detail += " " + preview.Rule
	}
	if preview.ActionType != "" {
		detail += " -> " + preview.ActionType
	}
	if preview.Server != "" {
		detail += " " + preview.Server
		if preview.ServerType != "" {
			detail += " (" + preview.ServerType + ")"
		}
	}
	return detail + " [A]"
}

func connectionTestAddress(target string) (M.Socksaddr, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return M.Socksaddr{}, E.Cause(err, "parse target")
	}
	switch parsed.Scheme {
	case "http", "https":
	case "":
		return M.Socksaddr{}, E.New("target has no scheme")
	default:
		return M.Socksaddr{}, E.New("unsupported target scheme: ", parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return M.Socksaddr{}, E.New("target has no host")
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	portValue, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return M.Socksaddr{}, E.Cause(err, "parse target port")
	}
	return M.ParseSocksaddrHostPort(host, uint16(portValue)), nil
}

// marshalConnectionTestReport encodes the report. An encoding failure cannot happen for
// this shape (no maps, channels or functions), and a nil result would make the gomobile
// caller dereference it, so an empty document is the safe fallback.
// Connection-test report bounds.
//
// The report crosses the gRPC/FFI bridge as one string. Its user-controlled parts are the target
// (a URL), the root tag and every stage detail/event line - and a stage detail contains the RULE
// DESCRIPTION, which for a rule-set or a large domain list is unbounded. One pathological
// configuration could therefore produce a multi-megabyte string at the binding surface. The helpers
// below cap each field and, if the encoded form is still over the hard ceiling, drop the narrative
// and then the stages rather than fail the call: a diagnostic that cannot be delivered is worse than
// a diagnostic that says it was shortened.
const (
	connectionTestMaxFieldBytes  = 4096
	connectionTestMaxReportBytes = 256 << 10
)

func boundedConnectionTestReport(report connectionTestReport, fieldBytes int, keepEvents bool) (connectionTestReport, bool) {
	truncated := false
	if bounded, cut := truncateConnectionTestStringTo(report.Root, fieldBytes); cut {
		report.Root, truncated = bounded, true
	} else {
		report.Root = bounded
	}
	if bounded, cut := truncateConnectionTestStringTo(report.Target, fieldBytes); cut {
		report.Target, truncated = bounded, true
	} else {
		report.Target = bounded
	}
	for index := range report.Stages {
		if bounded, cut := truncateConnectionTestStringTo(report.Stages[index].Detail, fieldBytes); cut {
			report.Stages[index].Detail, truncated = bounded, true
		} else {
			report.Stages[index].Detail = bounded
		}
	}
	if !keepEvents {
		if len(report.Events) > 0 {
			report.Events = nil
			truncated = true
		}
	} else {
		for index := range report.Events {
			if bounded, cut := truncateConnectionTestStringTo(report.Events[index], fieldBytes); cut {
				report.Events[index], truncated = bounded, true
			} else {
				report.Events[index] = bounded
			}
		}
	}
	report.Truncated = report.Truncated || truncated
	return report, truncated
}

func truncateConnectionTestStringTo(value string, limit int) (string, bool) {
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "…(truncated)", true
}

func marshalConnectionTestReport(report connectionTestReport) string {
	encode := func(candidate connectionTestReport) (string, int) {
		encoded, err := stdjson.Marshal(candidate)
		if err != nil {
			return "{}", 0
		}
		return string(encoded), len(encoded)
	}
	candidate, _ := boundedConnectionTestReport(report, connectionTestMaxFieldBytes, true)
	encoded, size := encode(candidate)
	if size <= connectionTestMaxReportBytes {
		return encoded
	}
	// Drop the narrative first, then shorten every remaining field, then keep only the verdict.
	candidate, _ = boundedConnectionTestReport(report, connectionTestMaxFieldBytes, false)
	encoded, size = encode(candidate)
	if size <= connectionTestMaxReportBytes {
		return encoded
	}
	candidate, _ = boundedConnectionTestReport(report, 512, false)
	encoded, size = encode(candidate)
	if size <= connectionTestMaxReportBytes {
		return encoded
	}
	candidate.Stages = nil
	candidate.Truncated = true
	encoded, _ = encode(candidate)
	return encoded
}

// outcomeForStage maps a settled stage status onto the emitter's vocabulary.
func outcomeForStage(status string) connectionTestOutcome {
	switch status {
	case connectionTestStageOK:
		return outcomeOK
	case connectionTestStageSkipped:
		return outcomeSkipped
	case connectionTestStageFailed:
		return outcomeFailed
	default:
		return outcomeRunning
	}
}

// ConnectionTestSink receives the narrative of a running connection test.
//
// A sink that blocks only delays ITS OWN delivery: the test publishes without waiting and the
// queue drops what it cannot hold, so a slow platform cannot stall or extend the diagnostic.
type ConnectionTestSink interface {
	Emit(event string)
}

// ConnectionTestSubscribe runs one connection test and delivers its events to sink as they are
// produced, returning the final report. The sink sees the begin first and the end last; a sink
// that cannot keep up loses events, which the report records as a dropped finding.
func (s *CommandServer) ConnectionTestSubscribe(root string, network string, target string, sink ConnectionTestSink) (*StringBox, error) {
	if s == nil || s.StartedService == nil {
		return nil, E.New("command server is not started")
	}
	instance := s.Instance()
	if instance == nil {
		return nil, E.New("service is not running")
	}
	boxInstance := instance.Box()
	if boxInstance == nil {
		return nil, E.New("box is not running")
	}
	started := time.Now()
	emitter := newConnectionTestEmitter(defaultEmitterBounds(), started)
	drained := make(chan struct{})
	if sink != nil {
		go func() {
			defer close(drained)
			for {
				select {
				case event := <-emitter.Events():
					sink.Emit(renderEvent(event))
				case <-emitter.Finished():
					// Drain what is still queued, then stop: the stream is finished.
					for {
						select {
						case event := <-emitter.Events():
							sink.Emit(renderEvent(event))
						default:
							return
						}
					}
				}
			}
		}()
	} else {
		close(drained)
	}
	report := runConnectionTestWithEmitter(context.Background(), newBoxConnectionTestPath(boxInstance), root, network, target, emitter)
	select {
	case <-drained:
	case <-time.After(connectionTestStageTimeout):
		// A sink that never returns must not hold the result: the report is complete and the
		// delivery is the platform's problem.
	}
	return wrapString(marshalConnectionTestReport(report)), nil
}
