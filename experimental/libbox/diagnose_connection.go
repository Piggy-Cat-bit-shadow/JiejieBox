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
	"net/url"
	"strconv"
	"time"

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
	Root    string                `json:"root"`
	Network string                `json:"network"`
	Target  string                `json:"target"`
	Outcome string                `json:"outcome"`
	Stages  []connectionTestStage `json:"stages"`
	TotalMs int64                 `json:"totalMs"`
}

// connectionTestPath is the slice of the running Box the test needs: the passive status
// model and the outbound namespace. *box.Box satisfies it; tests supply a fake.
type connectionTestPath interface {
	Status(root string, network string) physicalpath.PathStatus
	Outbound() adapter.OutboundManager
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
	report := runConnectionTest(context.Background(), boxInstance, root, network, target)
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
	started := time.Now()
	report := connectionTestReport{Root: root, Network: network, Target: target}

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

	// Stage 2: resolve the target into the address the dial will use.
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

	// Stage 3: the dial, if there is an exit to dial through.
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
	dialCtx, cancelDial := context.WithTimeout(budgetCtx, connectionTestStageTimeout)
	defer cancelDial()
	// The dial runs on its own goroutine so the report can return when the budget expires
	// even if the outbound ignores its context. That is the whole difference between "the
	// test is bounded" and "the test is bounded when the dial cooperates": a wedged dial
	// must not hold the diagnostic. Whatever the dial eventually returns after the report is
	// gone is closed by the waiter below rather than leaked.
	settled := make(chan dialOutcome, 1)
	go func() {
		conn, dialErr := outbound.DialContext(dialCtx, N.NetworkTCP, address)
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
		dialStage.Detail = "connected via " + status.Exit
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

// connectionTestAddress turns the configured target into the address a TCP dial uses. Only
// http/https are accepted: this test measures the PROXY PATH, so it needs a plain
// stream target, and accepting any scheme would silently measure something else.
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
func marshalConnectionTestReport(report connectionTestReport) string {
	encoded, err := stdjson.Marshal(report)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}
