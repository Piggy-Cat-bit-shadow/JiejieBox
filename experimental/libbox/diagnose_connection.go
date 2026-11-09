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
	"time"
	"unicode/utf8"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/physicalpath"
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

	// Stage 2: which rule decides this request, from the same rule list production walks.
	//
	// The order matters: this is the step that explains WHY the exit above was chosen, and it has
	// to run before the dial so the report answers "would this go there" even when the dial then
	// fails.
	stageStarted = time.Now()
	rulesStage := connectionTestStage{ID: connectionTestStageRules, Parent: connectionTestStagePath}
	previewer, canPreview := path.(connectionTestRulePreviewer)
	host, port := connectionTestTargetHost(target)
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
			if report.Routing == connectionTestRoutingRoot && rulePreview.Outbound != "" && rulePreview.Outbound != status.Exit {
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
	literalTarget := connectionTestLiteralAddress(host)
	rewrittenToLiteral := report.Routing == connectionTestRoutingRule && rulePreview.Matched &&
		rulePreview.FinalDestination.IsValid() && rulePreview.FinalDestination.IsIP()
	switch {
	case literalTarget:
		// A literal address never reaches a DNS rule. Reporting a default DNS server for it was
		// one of the reviewed inaccuracies.
		dnsStage.Status = connectionTestStageSkipped
		dnsStage.Detail = "the target is a literal address: no DNS decision applies"
	case rewrittenToLiteral:
		dnsStage.Status = connectionTestStageSkipped
		dnsStage.Detail = "the route decision rewrote the destination to " + rulePreview.FinalDestination.String() + ": no DNS decision applies"
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
			dnsStage.Detail = dnsPreviewDetail(dnsPreview)
		default:
			dnsStage.Status = connectionTestStageOK
			dnsStage.Detail = "no rule matched: the default DNS server answers"
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
	// The concrete destination: in rule mode it is what the route decision would dial (override_port
	// and override_address included), not the text the caller typed. A domain the decision produced
	// is resolved through the box's real DNS path, bounded by the stage budget.
	dialDestination := address
	switch {
	case report.Routing == connectionTestRoutingRule && rulePreview.FinalDestination.IsValid():
		dialDestination = rulePreview.FinalDestination
		if dialDestination.IsDomain() {
			resolver, canResolve := path.(connectionTestDomainResolver)
			if !canResolve {
				resolveStage.Status = connectionTestStageFailed
				resolveStage.Detail = "the route decision names " + dialDestination.String() + ", and this build cannot resolve it through the box"
				report.Stages = append(report.Stages, resolveStage)
				report.Outcome = connectionTestOutcomeFailed
				report.TotalMs = time.Since(started).Milliseconds()
				return report
			}
			resolveCtx, cancelResolve := context.WithTimeout(budgetCtx, connectionTestStageTimeout)
			address, resolveAddressErr := resolver.ResolveDomain(resolveCtx, dialDestination.Fqdn)
			cancelResolve()
			if resolveAddressErr != nil || !address.IsValid() {
				reason := "no address"
				if resolveAddressErr != nil {
					reason = resolveAddressErr.Error()
				}
				resolveStage.Status = connectionTestStageFailed
				resolveStage.Detail = "resolve " + dialDestination.Fqdn + " through the box: " + reason
				report.Stages = append(report.Stages, resolveStage)
				report.Outcome = connectionTestOutcomeFailed
				report.TotalMs = time.Since(started).Milliseconds()
				return report
			}
			dialDestination = M.SocksaddrFrom(address, dialDestination.Port)
			resolveStage.Detail = "'" + rulePreview.FinalDestination.Fqdn + "' -> " + dialDestination.String() + " (box DNS)"
		} else {
			resolveStage.Detail = "route destination " + dialDestination.String()
		}
	default:
		resolveStage.Detail = address.String()
	}
	report.Stages = append(report.Stages, resolveStage)

	// Stage 4: the dial, if there is an exit to dial through.
	dialStage := connectionTestStage{ID: connectionTestStageDial, Parent: connectionTestStagePath}
	var outbound adapter.Outbound
	switch report.Routing {
	case connectionTestRoutingRoot:
		if pathStage.Status != connectionTestStageOK {
			dialStage.Status = connectionTestStageSkipped
			dialStage.Detail = "no exit to dial through"
			report.Stages = append(report.Stages, dialStage)
			report.Outcome = connectionTestOutcomeFailed
			report.TotalMs = time.Since(started).Milliseconds()
			return report
		}
		var loaded bool
		outbound, loaded = path.Outbound().Outbound(status.Exit)
		if !loaded || outbound == nil {
			dialStage.Status = connectionTestStageSkipped
			dialStage.Detail = "the exit is not an outbound: " + status.Exit
			report.Stages = append(report.Stages, dialStage)
			report.Outcome = connectionTestOutcomeFailed
			report.TotalMs = time.Since(started).Milliseconds()
			return report
		}
	default:
		// Real routing: the preview is the only thing that can name a dialable outbound. A preview
		// that could not decide, or a rule that does not route (reject/hijack), leaves nothing to
		// dial - reporting that is more useful than dialling something else and calling it a test.
		switch {
		case !previewed:
			dialStage.Status = connectionTestStageSkipped
			dialStage.Detail = "real-routing mode needs the route decision, which this build cannot produce"
			report.Stages = append(report.Stages, dialStage)
			report.Outcome = connectionTestOutcomeFailed
			report.TotalMs = time.Since(started).Milliseconds()
			return report
		case rulePreview.Unknown != "":
			dialStage.Status = connectionTestStageSkipped
			dialStage.Detail = "the route decision is unknown: " + rulePreview.Unknown
			report.Stages = append(report.Stages, dialStage)
			report.Outcome = connectionTestOutcomeFailed
			report.TotalMs = time.Since(started).Milliseconds()
			return report
		case rulePreview.Matched && rulePreview.Outbound == "":
			dialStage.Status = connectionTestStageSkipped
			dialStage.Detail = "the route decision does not select a dialable outbound (" + rulePreview.ActionType + ")"
			report.Stages = append(report.Stages, dialStage)
			report.Outcome = connectionTestOutcomeFailed
			report.TotalMs = time.Since(started).Milliseconds()
			return report
		case rulePreview.Matched:
			var loaded bool
			outbound, loaded = path.Outbound().Outbound(rulePreview.Outbound)
			if !loaded || outbound == nil {
				dialStage.Status = connectionTestStageSkipped
				dialStage.Detail = "the rule's outbound is not loaded: " + rulePreview.Outbound
				report.Stages = append(report.Stages, dialStage)
				report.Outcome = connectionTestOutcomeFailed
				report.TotalMs = time.Since(started).Milliseconds()
				return report
			}
		default:
			outbound = path.Outbound().Default()
			if outbound == nil {
				dialStage.Status = connectionTestStageSkipped
				dialStage.Detail = "no rule matched and the box has no default outbound"
				report.Stages = append(report.Stages, dialStage)
				report.Outcome = connectionTestOutcomeFailed
				report.TotalMs = time.Since(started).Milliseconds()
				return report
			}
		}
	}

	stageStarted = time.Now()
	dialCtx, cancelDial := context.WithTimeout(budgetCtx, connectionTestStageTimeout)
	defer cancelDial()
	// The dial runs on its own goroutine so the report can return when the budget expires
	// even if the outbound ignores its context. That is the whole difference between "the
	// test is bounded" and "the test is bounded when the dial cooperates": a wedged dial
	// must not hold the diagnostic. Whatever the dial eventually returns after the report is
	// gone is closed by the waiter below rather than leaked.
	settled := make(chan dialOutcome, 1)
	go func() {
		conn, dialErr := outbound.DialContext(dialCtx, N.NetworkTCP, dialDestination)
		settled <- dialOutcome{conn: conn, err: dialErr}
	}()
	var outcome dialOutcome
	answered := true
	select {
	case outcome = <-settled:
	case <-dialCtx.Done():
		answered = false
		go func() {
			late := <-settled
			if late.conn != nil {
				_ = late.conn.Close()
			}
		}()
	}
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
		dialStage.Detail = "connected via " + outbound.Tag() + " to " + dialDestination.String()
		if report.Routing == connectionTestRoutingRule {
			dialStage.Detail += " (route decision)"
		}
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
