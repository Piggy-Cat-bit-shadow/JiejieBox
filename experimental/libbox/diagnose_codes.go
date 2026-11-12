package libbox

// O02 — the STABLE vocabulary a diagnostic's result is written in.
//
// # Why a code exists at all
//
// The report has always carried a stage id, a status and a human sentence. A sentence is for a
// reader, not for a client: a UI that wants to show a translated hint, colour one row, or offer
// "retry" only for a transient cause cannot branch on prose, and matching substrings of an English
// detail is how a diagnostic quietly becomes untranslatable. Every stage therefore also carries a
// CODE from the fixed set below, and the codes are part of the contract: they may be added, but the
// meaning of an existing one must not change.
//
// # What a code is NOT
//
// It is not a substitute for the observation. A code is only ever set at the point where the fact
// that justifies it was actually observed - the returned error, the walk's result, the refusal that
// happened. Nothing here is derived by parsing a detail string, and nothing is guessed: where the
// pipeline cannot tell, the code says so (`dial-budget-expired`, `dial-unreachable`) instead of
// choosing a friendlier one.
//
// # The three answers a failed dial can give, and why they are separate codes
//
// A dial that does not produce a connection has three genuinely different outcomes, and collapsing
// them into "failed" is the reviewed defect:
//
//   - `dial-no-ack`: something was sent and nothing came back before the time allowed ran out. The
//     path may be blackholed, or merely slow; it is not a refusal.
//   - `dial-refused` / `dial-reset` / `dial-unreachable`: the peer or the kernel answered, and the
//     answer was "no". Retrying is not obviously useful.
//   - `dial-budget-expired`: OUR budget ran out and the dial was abandoned still in flight. The
//     peer's state is unknown, and the report must not pretend the silence was an observation.
//
// The first two are distinguished by the error the real dial returned (`net.Error.Timeout`, the
// syscall errnos), never by wording. The third exists because the runner stops waiting, which is a
// fact about the runner rather than about the path.

// Stage codes. The list is the client's branching surface.
const (
	// connectionTestCodeOK is a stage that ran and succeeded.
	connectionTestCodeOK = "ok"
	// connectionTestCodeSkipped is a stage that was deliberately not run and whose reason is not
	// one of the specific codes below.
	connectionTestCodeSkipped = "skipped"

	// connectionTestCodeBoundReached is the refusal of the process-wide concurrency bound.
	connectionTestCodeBoundReached = "bound-reached"
	// connectionTestCodeUnsupportedNetwork is a request for a network this test cannot exercise.
	connectionTestCodeUnsupportedNetwork = "unsupported-network"
	// connectionTestCodeTargetInvalid is a target that cannot be turned into a request at all.
	connectionTestCodeTargetInvalid = "target-invalid"

	// connectionTestCodeNoExit is a passive path with no exit to dial through.
	connectionTestCodeNoExit = "no-exit"
	// connectionTestCodePathFailure is a passive path that reports a failure.
	connectionTestCodePathFailure = "path-failure"
	// connectionTestCodePathUnknown is a passive path that is missing a fact it needs.
	connectionTestCodePathUnknown = "path-unknown"

	// connectionTestCodeDecisionUnavailable is a build or box that cannot execute the production
	// decision. It is not a failure of the path and must not be read as one.
	connectionTestCodeDecisionUnavailable = "decision-unavailable"
	// connectionTestCodeDecisionFailed is a production decision that returned an error.
	connectionTestCodeDecisionFailed = "decision-failed"
	// connectionTestCodeDecisionTimeout is a production decision that did not settle in its budget.
	connectionTestCodeDecisionTimeout = "decision-timeout"
	// connectionTestCodeRuleRejected is a route decision that refuses the request.
	connectionTestCodeRuleRejected = "rule-rejected"
	// connectionTestCodeRuleUnknown is a decision that needs a fact a socketless request does not
	// carry (a sniffed name, a process path).
	connectionTestCodeRuleUnknown = "rule-unknown"
	// connectionTestCodeNoRule is a walk that matched no rule, so the final outbound decides.
	connectionTestCodeNoRule = "no-rule"

	// connectionTestCodeResolveFailed is a target that could not be turned into an address.
	connectionTestCodeResolveFailed = "resolve-failed"
	// connectionTestCodeNoDNSDecision is a request that never reaches a DNS question (a literal, or
	// a destination the decision rewrote).
	connectionTestCodeNoDNSDecision = "no-dns-decision"
	// connectionTestCodeDNSUnknown is a DNS decision that depends on a fact the preview lacks.
	connectionTestCodeDNSUnknown = "dns-unknown"
	// connectionTestCodeDNSPreview is a passive DNS decision reported as a preview.
	connectionTestCodeDNSPreview = "dns-preview"
	// connectionTestCodeDNSActual is a DNS decision this execution ACTUALLY ran.
	connectionTestCodeDNSActual = "dns-actual"

	// The dial's own outcomes. See the file comment for why no-ack and an explicit "no" differ.
	connectionTestCodeDialRefused      = "dial-refused"
	connectionTestCodeDialNoAck        = "dial-no-ack"
	connectionTestCodeDialUnreachable  = "dial-unreachable"
	connectionTestCodeDialReset        = "dial-reset"
	connectionTestCodeDialFailed       = "dial-failed"
	connectionTestCodeDialBudgetExpire = "dial-budget-expired"
	connectionTestCodeDialCancelled    = "dial-cancelled"
	// connectionTestCodeDialNotAttempted is a dial that was correctly skipped: nothing to dial, a
	// refused decision, or a path with no exit.
	connectionTestCodeDialNotAttempted = "dial-not-attempted"
)

// Finding codes. A finding is a fact that is not a stage: something the run OBSERVED about itself.
const (
	// connectionTestFindingPassiveReused: the path stage answered from the published status model
	// instead of recomputing a path.
	connectionTestFindingPassiveReused = "passive-reused"
	// connectionTestFindingRecovered: a group handed the dial to a second member and that member
	// carried the connection. It is a fact about the hand-off, not a health verdict.
	connectionTestFindingRecovered = "dial-recovered"
	// connectionTestFindingEventsDropped: the subscriber could not keep up and lost events.
	connectionTestFindingEventsDropped = "events-dropped"
)
