package route

// The ACTIVE half of the route explanation: run the production decision for one request and dial
// the destination that decision actually selected.
//
// # Why this exists as its own entry
//
// The active diagnostic used to be assembled from three independent pieces: `PreviewRoute` (a
// read-only rule preview), `LookupDomain` (the default DNS path) and `outbound.DialContext` (a dial
// of whatever text the caller typed). Each piece was correct on its own, and together they were not
// the production path:
//
//   - the preview evaluated rules against a FQDN even for a literal target, so an `ip_cidr` rule
//     never matched and a REJECT decision was reported as "no rule matched";
//   - a `resolve` rule action names its own DNS server and strategy, and the preview deliberately
//     does not resolve; the diagnostic then resolved through the DEFAULT resolver and dialled a
//     different address than production would have;
//   - `override_address`/`override_port` change the destination every LATER rule sees, and only a
//     real walk applies them in order.
//
// This entry runs the SAME walk `matchRule` runs (same rule list, same action option application,
// same resolve action, same address candidates), then resolves the chain with the same
// `routeDialDecision`/`routeDialer` pair production uses, pins the leaf the same way, and dials with
// the same `dialer.DialSerialNetwork` call. The report is built from that one execution, so the rule
// it names, the DNS server it names, the address it names and the connection it made cannot
// disagree.
//
// # What it deliberately does NOT do
//
// It is not an inbound flow: there is no client connection, so no sniffing (a sniff rule is
// reported as UNKNOWN rather than guessed), no process lookup (a process rule is UNKNOWN for the
// same reason), and no handover/copy loops. It also does not enter the connection manager's flow
// registry: a diagnostic must not create a managed connection, and it must not delete URLTest
// health evidence the way a real flow's failure does. The dial itself still goes through the
// outbound the decision selected, including a failover-capable group's own dial.

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	R "github.com/sagernet/sing-box/route/rule"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// RouteDiagnoseRequest is one diagnostic request.
type RouteDiagnoseRequest struct {
	// Network is the connection's network. Only TCP is meaningful today; it is passed through so a
	// caller cannot silently be handed a UDP answer for a TCP question.
	Network string
	// Destination is what the caller asked for: an address for a literal target, a name otherwise.
	Destination M.Socksaddr
	// Domain is the name the request carries when Destination is a literal (empty otherwise). It is
	// the fact a sniff rule would have produced, supplied by the caller instead of sniffed.
	Domain string
	// UserId is the socket owner when the caller knows it (-1 means unknown). Only the uid is used:
	// a path or package fact cannot be obtained without a socket, and a rule needing one is reported
	// as unknown.
	UserId int32
}

// RouteDiagnoseResolve is the DNS decision the walk ACTUALLY made, as opposed to a preview of what
// a DNS rule would answer.
type RouteDiagnoseResolve struct {
	// Performed reports that a resolve action ran and queried a server.
	Performed bool
	// Server is the configured server tag the action named (empty means the default resolver).
	Server string
	// ServerType is that server's transport type, when it could be resolved.
	ServerType string
	// Strategy is the strategy the action asked for.
	Strategy C.DomainStrategy
	// MatchOnly reports that the action published this answer for RULE MATCHING ONLY
	// (`match_only: true`): it is in the report because the decision really used it, but it is
	// deliberately absent from DestinationAddresses and is not a dial candidate.
	MatchOnly bool
	// Addresses are the answers the resolver returned, in the order the dial may use them.
	Addresses []netip.Addr
}

// RouteDiagnoseResult is one diagnostic execution.
type RouteDiagnoseResult struct {
	// Matched reports that a rule decided the request. False with an empty Unknown means the final
	// outbound decides.
	Matched    bool
	RuleIndex  int
	Rule       string
	ActionType string
	// Outbound is the selected route target (the rule's outbound, or the default outbound).
	Outbound string
	// Unknown states, when non-empty, that the decision needs a fact a socketless diagnostic does
	// not have. Nothing is dialled in that case.
	Unknown          string
	UnknownRuleIndex int

	OriginalDestination M.Socksaddr
	// FinalDestination is where production would DIAL: the request's destination after every
	// matched rule's override_address/override_port was applied.
	FinalDestination M.Socksaddr
	// DestinationAddresses are the address candidates the decision produced (a resolve action, or
	// the literal destination itself). Production dials these instead of re-resolving a name.
	DestinationAddresses []netip.Addr
	// Resolve is the actual DNS decision, when one was made.
	Resolve RouteDiagnoseResolve
	// Rejected reports that the decision refuses the request (reject or a DNS hijack action).
	Rejected bool
	// RejectDetail is the decision's own refusal.
	RejectDetail string

	// DialedDestination is the destination handed to the dial, rendered for a report. It is the
	// CANDIDATE form when the decision produced several addresses - the actual endpoint is
	// DialedEndpoint.
	DialedDestination string
	// DialedPeer is the transport peer of the established connection, when it could be read. For a
	// proxy this is the proxy, not the target.
	DialedPeer string
	// DialedEndpoint is the address the connection actually reached, when that is observable: the
	// peer of a direct dial. A proxy outbound reaches the target through a transport hop whose
	// destination cannot be read from this connection, and a group that owns the dial may have
	// chosen another member - both leave this empty and set DialedEndpointUnknown.
	DialedEndpoint string
	// DialedEndpointUnknown explains why DialedEndpoint is empty.
	DialedEndpointUnknown string
	// DialedOutbound is the LEAF that was selected for the dial.
	DialedOutbound string
	// DialedOutboundKnown reports whether DialedOutbound names the object that actually dialled.
	// A failover-capable group owns the dial and re-resolves a member per attempt, so the
	// pre-selected leaf is not necessarily the member that answered.
	DialedOutboundKnown bool
	// Dialed reports that a dial was attempted.
	Dialed bool
	// DialError is the dial's outcome; Conn is the established connection on success and belongs to
	// the caller, who must close it.
	DialError error
	Conn      net.Conn

	// WalkMs, ResolveMs and DialMs are the phase durations of this ONE execution, so the report can
	// still show where the time went without re-running anything.
	WalkMs    int64
	ResolveMs int64
	DialMs    int64
}

// DiagnoseRoute runs the production route decision for one request and dials its destination.
//
// A nil error means the walk itself completed: a rejection, an unknown decision and a failed dial
// are RESULTS, not errors, because a diagnostic exists to report them. An error is returned only
// when the router cannot answer at all (no outbound manager, a chain that cannot be resolved).
//
// The return values are NAMED because the destination and the phase timings are facts about the
// walk as a whole: the deferred assignment below must reach the caller, and a bare `return result,
// nil` with an unnamed result would hand back the pre-walk copy instead.
func (r *Router) DiagnoseRoute(ctx context.Context, request RouteDiagnoseRequest) (result RouteDiagnoseResult, err error) {
	started := time.Now()
	result = RouteDiagnoseResult{
		RuleIndex:           -1,
		UnknownRuleIndex:    -1,
		OriginalDestination: request.Destination,
		FinalDestination:    request.Destination,
	}
	if r == nil {
		return result, E.New("no router")
	}
	network := request.Network
	if network == "" {
		network = N.NetworkTCP
	}
	metadata := adapter.InboundContext{
		Inbound:     "connection-test",
		Network:     network,
		Destination: request.Destination,
		Domain:      request.Domain,
	}
	if request.Destination.IsIP() {
		address := request.Destination.Addr
		if address.Is4() || address.Is4In6() {
			metadata.IPVersion = 4
		} else {
			metadata.IPVersion = 6
		}
	}
	if request.UserId >= 0 {
		// The uid is the only process fact a socketless request can state. A path or package rule
		// needs more, and is reported as unknown below rather than evaluated against this record.
		metadata.ProcessInfo = &adapter.ConnectionOwner{UserId: request.UserId}
	}
	defer func() {
		result.FinalDestination = metadata.Destination
		result.DestinationAddresses = metadata.DestinationAddresses
		result.WalkMs = time.Since(started).Milliseconds() - result.ResolveMs
	}()

	selectedRule, walkErr := r.diagnoseWalk(ctx, &metadata, &result)
	if walkErr != nil {
		return result, walkErr
	}
	if result.Unknown != "" {
		return result, nil
	}

	var selectedOutbound adapter.Outbound
	if selectedRule != nil {
		switch action := selectedRule.Action().(type) {
		case *R.RuleActionRoute:
			result.Outbound = action.Outbound
			loaded := false
			selectedOutbound, loaded = r.outbound.Outbound(action.Outbound)
			if !loaded {
				return result, E.New("outbound not found: ", action.Outbound)
			}
		case *R.RuleActionBypass:
			if action.Outbound != "" {
				result.Outbound = action.Outbound
				loaded := false
				selectedOutbound, loaded = r.outbound.Outbound(action.Outbound)
				if !loaded {
					return result, E.New("outbound not found: ", action.Outbound)
				}
			}
		case *R.RuleActionReject:
			// The decision refuses the request. This is a RESULT, not a failed diagnostic: the
			// listener must see no connection, and the report must say which rule refused it.
			//
			// The detail comes from the side-effect-free form: `Error` feeds the data plane's drop
			// counter and flood limiter, and for `method: reply` it returns a NIL error (the inbound
			// layer writes the reply), which the diagnostic used to dereference - a panic on its own
			// goroutine that took the whole process down.
			result.Rejected = true
			result.RejectDetail = action.RejectionDetail(network)
			result.ActionType = selectedRule.Action().Type()
			return result, nil
		case *R.RuleActionHijackDNS:
			result.Rejected = true
			result.RejectDetail = "the route decision hands this connection to the DNS hijack path"
			result.ActionType = selectedRule.Action().Type()
			return result, nil
		}
	}
	if selectedRule == nil || (selectedOutbound == nil && result.Outbound == "") {
		selectedOutbound = r.outbound.Default()
		if selectedOutbound == nil {
			return result, E.New("no rule matched and the box has no default outbound")
		}
		if result.Outbound == "" {
			result.Outbound = selectedOutbound.Tag()
		}
	}

	// The chain resolution and dial ownership are production's own, including a failover-capable
	// group that owns this flow's dial.
	chain, dialOwner, chainErr := routeDialDecision(selectedOutbound, &metadata, network)
	if chainErr != nil {
		return result, chainErr
	}
	leaf := chain[len(chain)-1]
	// Pin the leaf for the hand-off, exactly as production does: a provider refresh replaces the
	// object without changing its tag, and dialling the object the chain named would use a closed
	// one.
	releaseOutbound, leased := r.acquireOutbound(leaf)
	if !leased {
		replacement, found := r.outbound.Outbound(leaf.Tag())
		if !found {
			return result, E.New("outbound ", leaf.Tag(), " was replaced while the diagnostic was being set up")
		}
		leaf = replacement
		releaseOutbound, leased = r.acquireOutbound(leaf)
		if !leased {
			return result, E.New("outbound ", leaf.Tag(), " was replaced while the diagnostic was being set up")
		}
	}
	defer releaseOutbound()
	result.DialedOutbound = leaf.Tag()
	result.DialedOutboundKnown = dialOwner == nil

	dialStart := time.Now()
	result.Dialed = true
	dialDestination := metadata.Destination
	result.DialedDestination = destinationForError(metadata)
	dialler := routeDialer(leaf, dialOwner, &metadata)
	if len(metadata.DestinationAddresses) > 0 || dialDestination.IsIP() {
		result.Conn, result.DialError = dialer.DialSerialNetwork(ctx, dialler, network, dialDestination,
			metadata.DestinationAddresses, metadata.NetworkStrategy, metadata.NetworkType,
			metadata.FallbackNetworkType, metadata.FallbackDelay)
	} else {
		// A name with no address candidates: the outbound resolves it with its own DNS path, which
		// is what production does. Resolving it here would take the decision away from the outbound
		// (a remote-resolving proxy must receive the NAME, not an address chosen for it).
		result.Conn, result.DialError = dialler.DialContext(ctx, network, dialDestination)
	}
	result.DialMs = time.Since(dialStart).Milliseconds()
	diagnoseDialAttribution(&result, leaf)
	return result, nil
}

// diagnoseDialAttribution records the endpoint the connection actually reached, and says so
// explicitly when that is not observable.
//
// # Why the peer is not the target
//
// A direct outbound dials the target itself, so the established connection's peer IS the endpoint
// the diagnostic reached - the reviewed failure reported the CANDIDATE LIST instead, which named an
// address that never answered. A proxy outbound reaches the target THROUGH a transport hop: the
// peer is the proxy, and which server the proxy then contacted is not on this connection. A
// failover-capable group owns the dial and re-resolves a member per attempt, so even the leaf the
// chain named is not necessarily the member that answered.
//
// The report therefore states what it knows and names what it does not: peer, endpoint, and the
// reason an endpoint is unknown. It never presents a candidate as the answer.
func diagnoseDialAttribution(result *RouteDiagnoseResult, leaf adapter.Outbound) {
	if result.DialError != nil || result.Conn == nil {
		result.DialedEndpointUnknown = "no connection was established"
		return
	}
	if peer := result.Conn.RemoteAddr(); peer != nil {
		result.DialedPeer = peer.String()
	}
	if !result.DialedOutboundKnown {
		result.DialedEndpointUnknown = "a failover-capable group owns this dial and re-resolves the member, so the endpoint attribution is not observable here"
		return
	}
	if result.DialedPeer == "" {
		result.DialedEndpointUnknown = "the established connection has no observable remote address"
		return
	}
	if leaf != nil && leaf.Type() == C.TypeDirect {
		// A direct outbound dials the target itself: the peer is the endpoint.
		result.DialedEndpoint = result.DialedPeer
		return
	}
	result.DialedEndpointUnknown = "the outbound reaches the target through a transport hop; the target endpoint is not observable here"
}

// diagnoseWalk mirrors matchRule's decision points on a private metadata copy.
//
// It returns the rule that DECIDES the request (or nil when the final outbound decides). A fact the
// walk cannot obtain without a socket ends it as Unknown, which is a result rather than a guess.
func (r *Router) diagnoseWalk(ctx context.Context, metadata *adapter.InboundContext, result *RouteDiagnoseResult) (adapter.Rule, error) {
	for index, rule := range r.rules {
		metadata.ResetRuleCache()
		if need := r.processNeedAt(index); need != processNeedNone && !diagnoseProcessFactsSatisfy(metadata, need) {
			result.Rule = rule.String()
			result.UnknownRuleIndex = index
			result.Unknown = "the decision depends on process facts a diagnostic request does not have"
			return nil, nil
		}
		if !rule.Match(metadata) {
			continue
		}
		applyActionRouteOptions(metadata, rule.Action())
		switch action := rule.Action().(type) {
		case *R.RuleActionResolve:
			resolveStarted := time.Now()
			addresses, strategy, serverType, err := r.diagnoseResolve(ctx, metadata, action)
			result.ResolveMs += time.Since(resolveStarted).Milliseconds()
			if err != nil {
				return nil, err
			}
			if addresses != nil {
				result.Resolve = RouteDiagnoseResolve{
					Performed:  true,
					Server:     action.Server,
					ServerType: serverType,
					Strategy:   strategy,
					MatchOnly:  action.MatchOnly,
					Addresses:  addresses,
				}
				publishResolvedAddresses(metadata, action, addresses, strategy)
			}
		case *R.RuleActionSniff:
			if metadata.Domain == "" {
				result.Rule = rule.String()
				result.UnknownRuleIndex = index
				result.Unknown = "the decision depends on a sniffed domain and a diagnostic request has no stream to sniff"
				return nil, nil
			}
		}
		actionType := rule.Action().Type()
		decided := actionType == C.RuleActionTypeRoute ||
			actionType == C.RuleActionTypeReject ||
			actionType == C.RuleActionTypeHijackDNS
		if bypassAction, isBypass := rule.Action().(*R.RuleActionBypass); isBypass {
			decided = bypassAction.Outbound != ""
		}
		if decided {
			result.Matched = true
			result.RuleIndex = index
			result.Rule = rule.String()
			result.ActionType = actionType
			return rule, nil
		}
	}
	return nil, nil
}

// diagnoseProcessFactsSatisfy reports whether the process record holds the fields a rule may read.
func diagnoseProcessFactsSatisfy(metadata *adapter.InboundContext, need processNeed) bool {
	if metadata.ProcessInfo == nil {
		return false
	}
	if need&processNeedPath != 0 && len(metadata.ProcessInfo.ProcessPaths) == 0 {
		return false
	}
	if need&processNeedPackage != 0 && len(metadata.ProcessInfo.PackageNames) == 0 {
		return false
	}
	return true
}

// diagnoseResolve performs a resolve action through the SAME DNS call production makes.
//
// It returns nil addresses when the action's lookup name is empty, which is what actionResolve does:
// there is nothing to resolve, so nothing happened.
func (r *Router) diagnoseResolve(ctx context.Context, metadata *adapter.InboundContext, action *R.RuleActionResolve) ([]netip.Addr, C.DomainStrategy, string, error) {
	name := resolveLookupName(metadata)
	if name == "" {
		return nil, action.Strategy, "", nil
	}
	addresses, strategy, serverType, err := r.lookupResolveAction(ctx, metadata, action)
	if err != nil {
		return nil, strategy, serverType, err
	}
	return addresses, strategy, serverType, nil
}
