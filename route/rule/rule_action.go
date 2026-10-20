package rule

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/sniff"
	"github.com/sagernet/sing-box/common/tlsspoof"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/miekg/dns"
)

func newRuleActionRouteOptions(options option.RawRouteOptionsActionOptions) (RuleActionRouteOptions, error) {
	spoof, spoofMethod, err := tlsspoof.ParseOptions(options.TLSSpoof, options.TLSSpoofMethod)
	if err != nil {
		return RuleActionRouteOptions{}, err
	}
	return RuleActionRouteOptions{
		OverrideAddress:           M.ParseSocksaddrHostPort(options.OverrideAddress, 0),
		OverridePort:              options.OverridePort,
		NetworkStrategy:           (*C.NetworkStrategy)(options.NetworkStrategy),
		FallbackDelay:             time.Duration(options.FallbackDelay),
		UDPDisableDomainUnmapping: options.UDPDisableDomainUnmapping,
		UDPConnect:                options.UDPConnect,
		UDPTimeout:                time.Duration(options.UDPTimeout),
		TLSFragment:               options.TLSFragment,
		TLSFragmentFallbackDelay:  time.Duration(options.TLSFragmentFallbackDelay),
		TLSRecordFragment:         options.TLSRecordFragment,
		TLSSpoof:                  spoof,
		TLSSpoofMethod:            spoofMethod,
	}, nil
}

func NewRuleAction(logger logger.ContextLogger, action option.RuleAction) (adapter.RuleAction, error) {
	switch action.Action {
	case "":
		return nil, nil
	case C.RuleActionTypeRoute:
		routeOptions, err := newRuleActionRouteOptions(action.RouteOptions.RawRouteOptionsActionOptions)
		if err != nil {
			return nil, err
		}
		return &RuleActionRoute{
			Outbound:               action.RouteOptions.Outbound,
			RuleActionRouteOptions: routeOptions,
		}, nil
	case C.RuleActionTypeRouteOptions:
		routeOptions, err := newRuleActionRouteOptions(option.RawRouteOptionsActionOptions(action.RouteOptionsOptions))
		if err != nil {
			return nil, err
		}
		return &routeOptions, nil
	case C.RuleActionTypeBypass:
		routeOptions, err := newRuleActionRouteOptions(action.BypassOptions.RawRouteOptionsActionOptions)
		if err != nil {
			return nil, err
		}
		return &RuleActionBypass{
			Outbound:               action.BypassOptions.Outbound,
			RuleActionRouteOptions: routeOptions,
		}, nil
	case C.RuleActionTypeReject:
		return &RuleActionReject{
			Method: action.RejectOptions.Method,
			NoDrop: action.RejectOptions.NoDrop,
			logger: logger,
		}, nil
	case C.RuleActionTypeHijackDNS:
		return &RuleActionHijackDNS{}, nil
	case C.RuleActionTypeSniff:
		sniffAction := &RuleActionSniff{
			SnifferNames: action.SniffOptions.Sniffer,
			Timeout:      time.Duration(action.SniffOptions.Timeout),
		}
		return sniffAction, sniffAction.build()
	case C.RuleActionTypeResolve:
		return &RuleActionResolve{
			Server:                 action.ResolveOptions.Server,
			Timeout:                time.Duration(action.ResolveOptions.Timeout),
			Strategy:               C.DomainStrategy(action.ResolveOptions.Strategy),
			DisableCache:           action.ResolveOptions.DisableCache,
			DisableOptimisticCache: action.ResolveOptions.DisableOptimisticCache,
			RewriteTTL:             action.ResolveOptions.RewriteTTL,
			ClientSubnet:           action.ResolveOptions.ClientSubnet.Build(netip.Prefix{}),
		}, nil
	default:
		panic(F.ToString("unknown rule action: ", action.Action))
	}
}

func NewDNSRuleAction(logger logger.ContextLogger, action option.DNSRuleAction) adapter.RuleAction {
	switch action.Action {
	case "":
		return nil
	case C.RuleActionTypeRoute:
		return &RuleActionDNSRoute{
			Server:      action.RouteOptions.Server,
			Speculative: action.RouteOptions.Speculative,
			RuleActionDNSRouteOptions: RuleActionDNSRouteOptions{
				Strategy:               C.DomainStrategy(action.RouteOptions.Strategy),
				Timeout:                time.Duration(action.RouteOptions.Timeout),
				DisableCache:           action.RouteOptions.DisableCache,
				DisableOptimisticCache: action.RouteOptions.DisableOptimisticCache,
				RewriteTTL:             action.RouteOptions.RewriteTTL,
				ClientSubnet:           netip.Prefix(common.PtrValueOrDefault(action.RouteOptions.ClientSubnet)),
				RemoveClientSubnet:     action.RouteOptions.RemoveClientSubnet,
			},
		}
	case C.RuleActionTypeEvaluate:
		return &RuleActionEvaluate{
			Server:      action.EvaluateOptions.Server,
			Tag:         action.EvaluateOptions.Tag,
			Speculative: action.EvaluateOptions.Speculative,
			RuleActionDNSRouteOptions: RuleActionDNSRouteOptions{
				Strategy:               C.DomainStrategy(action.EvaluateOptions.Strategy),
				Timeout:                time.Duration(action.EvaluateOptions.Timeout),
				DisableCache:           action.EvaluateOptions.DisableCache,
				DisableOptimisticCache: action.EvaluateOptions.DisableOptimisticCache,
				RewriteTTL:             action.EvaluateOptions.RewriteTTL,
				ClientSubnet:           netip.Prefix(common.PtrValueOrDefault(action.EvaluateOptions.ClientSubnet)),
				RemoveClientSubnet:     action.EvaluateOptions.RemoveClientSubnet,
			},
		}
	case C.RuleActionTypeRespond:
		return &RuleActionRespond{}
	case C.RuleActionTypeRouteOptions:
		return &RuleActionDNSRouteOptions{
			Strategy:               C.DomainStrategy(action.RouteOptionsOptions.Strategy),
			Timeout:                time.Duration(action.RouteOptionsOptions.Timeout),
			DisableCache:           action.RouteOptionsOptions.DisableCache,
			DisableOptimisticCache: action.RouteOptionsOptions.DisableOptimisticCache,
			RewriteTTL:             action.RouteOptionsOptions.RewriteTTL,
			ClientSubnet:           netip.Prefix(common.PtrValueOrDefault(action.RouteOptionsOptions.ClientSubnet)),
			RemoveClientSubnet:     action.RouteOptionsOptions.RemoveClientSubnet,
		}
	case C.RuleActionTypeReject:
		return &RuleActionReject{
			Method: action.RejectOptions.Method,
			NoDrop: action.RejectOptions.NoDrop,
			logger: logger,
		}
	case C.RuleActionTypePredefined:
		return &RuleActionPredefined{
			Rcode:  action.PredefinedOptions.Rcode.Build(),
			Answer: common.Map(action.PredefinedOptions.Answer, option.DNSRecordOptions.Build),
			Ns:     common.Map(action.PredefinedOptions.Ns, option.DNSRecordOptions.Build),
			Extra:  common.Map(action.PredefinedOptions.Extra, option.DNSRecordOptions.Build),
		}
	case C.RuleActionTypeAdblock:
		return &RuleActionAdblock{}
	default:
		panic(F.ToString("unknown rule action: ", action.Action))
	}
}

type RuleActionRoute struct {
	Outbound string
	RuleActionRouteOptions
}

func (r *RuleActionRoute) Type() string {
	return C.RuleActionTypeRoute
}

func (r *RuleActionRoute) String() string {
	var descriptions []string
	descriptions = append(descriptions, r.Outbound)
	descriptions = append(descriptions, r.Descriptions()...)
	return F.ToString("route(", strings.Join(descriptions, ","), ")")
}

type RuleActionBypass struct {
	Outbound string
	RuleActionRouteOptions
}

func (r *RuleActionBypass) Type() string {
	return C.RuleActionTypeBypass
}

func (r *RuleActionBypass) String() string {
	if r.Outbound == "" {
		return "bypass()"
	}
	var descriptions []string
	descriptions = append(descriptions, r.Outbound)
	descriptions = append(descriptions, r.Descriptions()...)
	return F.ToString("bypass(", strings.Join(descriptions, ","), ")")
}

type RuleActionRouteOptions struct {
	OverrideAddress           M.Socksaddr
	OverridePort              uint16
	NetworkStrategy           *C.NetworkStrategy
	NetworkType               []C.InterfaceType
	FallbackNetworkType       []C.InterfaceType
	FallbackDelay             time.Duration
	UDPDisableDomainUnmapping bool
	UDPConnect                bool
	UDPTimeout                time.Duration
	TLSFragment               bool
	TLSFragmentFallbackDelay  time.Duration
	TLSRecordFragment         bool
	TLSSpoof                  string
	TLSSpoofMethod            tlsspoof.Method
}

func (r *RuleActionRouteOptions) Type() string {
	return C.RuleActionTypeRouteOptions
}

func (r *RuleActionRouteOptions) String() string {
	return F.ToString("route-options(", strings.Join(r.Descriptions(), ","), ")")
}

func (r *RuleActionRouteOptions) Descriptions() []string {
	var descriptions []string
	if r.OverrideAddress.IsValid() {
		descriptions = append(descriptions, F.ToString("override-address=", r.OverrideAddress.AddrString()))
	}
	if r.OverridePort > 0 {
		descriptions = append(descriptions, F.ToString("override-port=", r.OverridePort))
	}
	if r.NetworkStrategy != nil {
		descriptions = append(descriptions, F.ToString("network-strategy=", r.NetworkStrategy))
	}
	if r.NetworkType != nil {
		descriptions = append(descriptions, F.ToString("network-type=", strings.Join(common.Map(r.NetworkType, C.InterfaceType.String), ",")))
	}
	if r.FallbackNetworkType != nil {
		descriptions = append(descriptions, F.ToString("fallback-network-type=", strings.Join(common.Map(r.FallbackNetworkType, C.InterfaceType.String), ",")))
	}
	if r.FallbackDelay > 0 {
		descriptions = append(descriptions, F.ToString("fallback-delay=", r.FallbackDelay.String()))
	}
	if r.UDPDisableDomainUnmapping {
		descriptions = append(descriptions, "udp-disable-domain-unmapping")
	}
	if r.UDPConnect {
		descriptions = append(descriptions, "udp-connect")
	}
	if r.UDPTimeout > 0 {
		descriptions = append(descriptions, "udp-timeout")
	}
	if r.TLSFragment {
		descriptions = append(descriptions, "tls-fragment")
	}
	if r.TLSFragmentFallbackDelay > 0 {
		descriptions = append(descriptions, F.ToString("tls-fragment-fallback-delay=", r.TLSFragmentFallbackDelay.String()))
	}
	if r.TLSRecordFragment {
		descriptions = append(descriptions, "tls-record-fragment")
	}
	if r.TLSSpoof != "" {
		descriptions = append(descriptions, F.ToString("tls-spoof=", r.TLSSpoof))
		descriptions = append(descriptions, F.ToString("tls-spoof-method=", r.TLSSpoofMethod.String()))
	}
	return descriptions
}

type RuleActionDNSRoute struct {
	Server      string
	Speculative bool
	RuleActionDNSRouteOptions
}

func (r *RuleActionDNSRoute) Type() string {
	return C.RuleActionTypeRoute
}

func (r *RuleActionDNSRoute) String() string {
	return formatDNSRouteAction("route", r.Server, r.Speculative, r.RuleActionDNSRouteOptions)
}

type RuleActionEvaluate struct {
	Server      string
	Tag         string
	Speculative bool
	RuleActionDNSRouteOptions
}

func (r *RuleActionEvaluate) Type() string {
	return C.RuleActionTypeEvaluate
}

func (r *RuleActionEvaluate) String() string {
	return formatDNSRouteAction("evaluate", r.Server, r.Speculative, r.RuleActionDNSRouteOptions)
}

type RuleActionRespond struct{}

func (r *RuleActionRespond) Type() string {
	return C.RuleActionTypeRespond
}

func (r *RuleActionRespond) String() string {
	return "respond"
}

func formatDNSRouteAction(action string, server string, speculative bool, options RuleActionDNSRouteOptions) string {
	var descriptions []string
	descriptions = append(descriptions, server)
	if speculative {
		descriptions = append(descriptions, "speculative")
	}
	if options.DisableCache {
		descriptions = append(descriptions, "disable-cache")
	}
	if options.DisableOptimisticCache {
		descriptions = append(descriptions, "disable-optimistic-cache")
	}
	if options.RewriteTTL != nil {
		descriptions = append(descriptions, F.ToString("rewrite-ttl=", *options.RewriteTTL))
	}
	if options.Timeout > 0 {
		descriptions = append(descriptions, F.ToString("timeout=", options.Timeout.String()))
	}
	if options.ClientSubnet.IsValid() {
		descriptions = append(descriptions, F.ToString("client-subnet=", options.ClientSubnet))
	}
	if options.RemoveClientSubnet {
		descriptions = append(descriptions, "remove-client-subnet")
	}
	return F.ToString(action, "(", strings.Join(descriptions, ","), ")")
}

type RuleActionDNSRouteOptions struct {
	Strategy               C.DomainStrategy
	Timeout                time.Duration
	DisableCache           bool
	DisableOptimisticCache bool
	RewriteTTL             *uint32
	ClientSubnet           netip.Prefix
	RemoveClientSubnet     bool
}

func (r *RuleActionDNSRouteOptions) Type() string {
	return C.RuleActionTypeRouteOptions
}

func (r *RuleActionDNSRouteOptions) String() string {
	var descriptions []string
	if r.DisableCache {
		descriptions = append(descriptions, "disable-cache")
	}
	if r.DisableOptimisticCache {
		descriptions = append(descriptions, "disable-optimistic-cache")
	}
	if r.RewriteTTL != nil {
		descriptions = append(descriptions, F.ToString("rewrite-ttl=", *r.RewriteTTL))
	}
	if r.Timeout > 0 {
		descriptions = append(descriptions, F.ToString("timeout=", r.Timeout.String()))
	}
	if r.ClientSubnet.IsValid() {
		descriptions = append(descriptions, F.ToString("client-subnet=", r.ClientSubnet))
	}
	if r.RemoveClientSubnet {
		descriptions = append(descriptions, "remove-client-subnet")
	}
	return F.ToString("route-options(", strings.Join(descriptions, ","), ")")
}

var (
	ErrReset = E.New("connection reset")
	ErrDrop  = E.New("packet dropped")
)

type RejectedError struct {
	Cause error
}

func (r *RejectedError) Error() string {
	return "rejected"
}

func (r *RejectedError) Unwrap() error {
	return r.Cause
}

func IsRejected(err error) bool {
	var rejected *RejectedError
	return errors.As(err, &rejected)
}

type BypassedError struct {
	Cause error
}

func (b *BypassedError) Error() string {
	return "bypassed"
}

func (b *BypassedError) Unwrap() error {
	return b.Cause
}

func IsBypassed(err error) bool {
	var bypassed *BypassedError
	return errors.As(err, &bypassed)
}

type RuleActionReject struct {
	Method      string
	NoDrop      bool
	logger      logger.ContextLogger
	dropAccess  sync.Mutex
	dropCounter []time.Time
}

func (r *RuleActionReject) Type() string {
	return C.RuleActionTypeReject
}

func (r *RuleActionReject) String() string {
	if r.Method == C.RuleActionRejectMethodDefault {
		return "reject"
	}
	return F.ToString("reject(", r.Method, ")")
}

func (r *RuleActionReject) Error(ctx context.Context) error {
	var returnErr error
	switch r.Method {
	case C.RuleActionRejectMethodDefault:
		returnErr = &RejectedError{ErrReset}
	case C.RuleActionRejectMethodDrop:
		return &RejectedError{ErrDrop}
	case C.RuleActionRejectMethodReply:
		return nil
	default:
		panic(F.ToString("unknown reject method: ", r.Method))
	}
	if r.NoDrop {
		return returnErr
	}
	r.dropAccess.Lock()
	defer r.dropAccess.Unlock()
	timeNow := time.Now()
	r.dropCounter = common.Filter(r.dropCounter, func(t time.Time) bool {
		return timeNow.Sub(t) <= 30*time.Second
	})
	r.dropCounter = append(r.dropCounter, timeNow)
	if len(r.dropCounter) > 50 {
		if ctx != nil {
			r.logger.DebugContext(ctx, "dropped due to flooding")
		}
		return &RejectedError{ErrDrop}
	}
	return returnErr
}

type RuleActionHijackDNS struct{}

func (r *RuleActionHijackDNS) Type() string {
	return C.RuleActionTypeHijackDNS
}

func (r *RuleActionHijackDNS) String() string {
	return "hijack-dns"
}

type RuleActionSniff struct {
	SnifferNames   []string
	StreamSniffers []sniff.StreamSniffer
	PacketSniffers []sniff.PacketSniffer
	Timeout        time.Duration
	// Deprecated
	OverrideDestination bool
}

func (r *RuleActionSniff) Type() string {
	return C.RuleActionTypeSniff
}

func (r *RuleActionSniff) build() error {
	for _, name := range r.SnifferNames {
		switch name {
		case C.ProtocolTLS:
			r.StreamSniffers = append(r.StreamSniffers, sniff.TLSClientHello)
		case C.ProtocolHTTP:
			r.StreamSniffers = append(r.StreamSniffers, sniff.HTTPHost)
		case C.ProtocolQUIC:
			r.PacketSniffers = append(r.PacketSniffers, sniff.QUICClientHello)
		case C.ProtocolDNS:
			r.StreamSniffers = append(r.StreamSniffers, sniff.StreamDomainNameQuery)
			r.PacketSniffers = append(r.PacketSniffers, sniff.DomainNameQuery)
		case C.ProtocolSTUN:
			r.PacketSniffers = append(r.PacketSniffers, sniff.STUNMessage)
		case C.ProtocolWireGuard:
			// WireGuard is selectable on its own so a config can ask for the classification
			// without also enabling the BitTorrent sniffers it used to be confused with.
			r.PacketSniffers = append(r.PacketSniffers, sniff.WireGuard)
		case C.ProtocolBitTorrent:
			// WireGuard is registered unconditionally ahead of UTP here, not only when the config
			// also names "wireguard". The loop walks SnifferNames in the order the user wrote, so
			// a config naming only "bittorrent" would otherwise reproduce LX 078: a 148-byte
			// handshake initiation starts 01 00 00 00 and satisfies UTP's two-nibble heuristic.
			// This mirrors defaultPacketSniffers, where the same order is load-bearing.
			r.PacketSniffers = append(r.PacketSniffers, sniff.WireGuard)
			r.StreamSniffers = append(r.StreamSniffers, sniff.BitTorrent)
			r.PacketSniffers = append(r.PacketSniffers, sniff.UTP)
			r.PacketSniffers = append(r.PacketSniffers, sniff.UDPTracker)
		case C.ProtocolDTLS:
			r.PacketSniffers = append(r.PacketSniffers, sniff.DTLSRecord)
		case C.ProtocolSSH:
			r.StreamSniffers = append(r.StreamSniffers, sniff.SSH)
		case C.ProtocolRDP:
			r.StreamSniffers = append(r.StreamSniffers, sniff.RDP)
		case C.ProtocolNTP:
			r.PacketSniffers = append(r.PacketSniffers, sniff.NTP)
		default:
			return E.New("unknown sniffer: ", name)
		}
	}
	return nil
}

func (r *RuleActionSniff) String() string {
	if len(r.SnifferNames) == 0 && r.Timeout == 0 {
		return "sniff"
	} else if len(r.SnifferNames) > 0 && r.Timeout == 0 {
		return F.ToString("sniff(", strings.Join(r.SnifferNames, ","), ")")
	} else if len(r.SnifferNames) == 0 && r.Timeout > 0 {
		return F.ToString("sniff(", r.Timeout.String(), ")")
	} else {
		return F.ToString("sniff(", strings.Join(r.SnifferNames, ","), ",", r.Timeout.String(), ")")
	}
}

type RuleActionResolve struct {
	Server                 string
	Timeout                time.Duration
	Strategy               C.DomainStrategy
	DisableCache           bool
	DisableOptimisticCache bool
	RewriteTTL             *uint32
	ClientSubnet           netip.Prefix
}

func (r *RuleActionResolve) Type() string {
	return C.RuleActionTypeResolve
}

func (r *RuleActionResolve) String() string {
	var options []string
	if r.Server != "" {
		options = append(options, r.Server)
	}
	if r.Strategy != C.DomainStrategyAsIS {
		options = append(options, F.ToString(option.DomainStrategy(r.Strategy)))
	}
	if r.DisableCache {
		options = append(options, "disable_cache")
	}
	if r.DisableOptimisticCache {
		options = append(options, "disable_optimistic_cache")
	}
	if r.RewriteTTL != nil {
		options = append(options, F.ToString("rewrite_ttl=", *r.RewriteTTL))
	}
	if r.Timeout > 0 {
		options = append(options, F.ToString("timeout=", r.Timeout.String()))
	}
	if r.ClientSubnet.IsValid() {
		options = append(options, F.ToString("client_subnet=", r.ClientSubnet))
	}
	if len(options) == 0 {
		return "resolve"
	} else {
		return F.ToString("resolve(", strings.Join(options, ","), ")")
	}
}

type RuleActionPredefined struct {
	Rcode  int
	Answer []dns.RR
	Ns     []dns.RR
	Extra  []dns.RR
}

func (r *RuleActionPredefined) Type() string {
	return C.RuleActionTypePredefined
}

func (r *RuleActionPredefined) String() string {
	var options []string
	options = append(options, dns.RcodeToString[r.Rcode])
	options = append(options, common.Map(r.Answer, dns.RR.String)...)
	options = append(options, common.Map(r.Ns, dns.RR.String)...)
	options = append(options, common.Map(r.Extra, dns.RR.String)...)
	return F.ToString("predefined(", strings.Join(options, ","), ")")
}

func (r *RuleActionPredefined) Response(request *dns.Msg) *dns.Msg {
	return &dns.Msg{
		MsgHdr: dns.MsgHdr{
			Id:                 request.Id,
			Response:           true,
			Authoritative:      true,
			RecursionDesired:   true,
			RecursionAvailable: true,
			Rcode:              r.Rcode,
		},
		Question: request.Question,
		Answer:   rewriteRecords(r.Answer, request.Question[0]),
		Ns:       rewriteRecords(r.Ns, request.Question[0]),
		Extra:    rewriteRecords(r.Extra, request.Question[0]),
	}
}

func rewriteRecords(records []dns.RR, question dns.Question) []dns.RR {
	return common.Map(records, func(it dns.RR) dns.RR {
		if strings.HasPrefix(it.Header().Name, "*") {
			if strings.HasSuffix(question.Name, it.Header().Name[1:]) {
				it = dns.Copy(it)
				it.Header().Name = question.Name
			}
		}
		return it
	})
}

// RuleActionAdblock is a DNS-only terminal action: the rule that carries it has already
// established that the query name is advertising, so the answer is manufactured here rather
// than looked up.
//
// # Why it is not reject
//
// `reject` says "refuse this request". A refused DNS query is a failure the caller is entitled
// to retry or to re-ask elsewhere, and on this fork's targets it is: a refused or dropped query
// becomes another query. adblock says the opposite thing - the name is known not to resolve, and
// the cheapest correct answer is the one the client will stop asking about. That is a different
// verdict with a different required shape, so it is a different action. `reject/drop` also
// produces no negative-caching information at all, which is the half of the job this action
// exists to do.
//
// # Why it is not predefined
//
// A `predefined` rule can already emit NXDOMAIN, and an operator can already hand-write the SOA
// for it. What they cannot express with it is intent: `predefined` is a general response builder
// whose owner decides everything, including whether the result is negative-cacheable. adblock
// fixes the response - NXDOMAIN, empty answer, one SOA carrying constant.DefaultDNSNegativeTTL -
// so that a huge third-party rule-set can be attached to it without every user having to
// re-derive the negative-caching shape. The two share RuleActionPredefined.Response's flag
// convention, so a local response looks the same whichever action produced it.
//
// # Where it takes effect
//
// It is handled in the DNS rule walk, which runs before the DNS client, its exact cache, its
// singleflight and any transport. Nothing about this type reaches a transport: there is no
// field, no option and no state here that an upstream exchange could be started from. The action
// holds no data at all, so the same value is safe to share across every rule and goroutine, and
// answering is allocation-only.
type RuleActionAdblock struct{}

var _ adapter.RuleAction = (*RuleActionAdblock)(nil)

func (r *RuleActionAdblock) Type() string {
	return C.RuleActionTypeAdblock
}

func (r *RuleActionAdblock) String() string {
	return C.RuleActionTypeAdblock
}

// Response builds the local NXDOMAIN for the current request.
//
// The response is RFC 2308 shape, which is what makes the answer worth more than a refusal:
// NXDOMAIN states that the NAME does not exist, so a client that caches it stops asking for the
// A, AAAA, HTTPS and SVCB records separately. The SOA in the authority section is what a client
// needs to cache it at all - an NXDOMAIN with no SOA carries no negative TTL, and a resolver that
// follows RFC 2308 has nothing to record. Both the SOA's own TTL and its MINIMUM are set to the
// same value, because clients disagree about which of the two is authoritative
// (RFC 2308 section 3 says the minimum of the two).
//
// The name, type, class and ID all come from the request being answered. Only the verdict is
// fixed; a response that described a different question than the one asked would be discarded by
// the client, and the retry would put the query back on the wire that this action removed it
// from. The record is newly constructed per call rather than shared, so concurrent queries cannot
// race on one dns.Msg.
func (r *RuleActionAdblock) Response(request *dns.Msg) *dns.Msg {
	response := &dns.Msg{
		MsgHdr: dns.MsgHdr{
			// The same flag convention as RuleActionPredefined.Response and the rest of the
			// locally answered responses in this tree: a DNS server that answered from its own
			// knowledge marks the answer authoritative, and it does resolve on the client's
			// behalf. TC/CD/AD are all zero - nothing is truncated, and nothing here is
			// cryptographically validated.
			Id:                 request.Id,
			Response:           true,
			Authoritative:      true,
			RecursionDesired:   true,
			RecursionAvailable: true,
			Rcode:              dns.RcodeNameError,
		},
	}
	// A question is what makes the answer intelligible, and the router refuses a request without
	// one long before a rule runs. The guard is here because this action is reachable from Go, and
	// the alternative to the guard is an index panic in the query path.
	if len(request.Question) == 0 {
		return response
	}
	question := request.Question[0]
	response.Question = []dns.Question{question}
	response.Ns = []dns.RR{&dns.SOA{
		Hdr: dns.RR_Header{
			// The zone that makes the statement is the name itself. A synthetic parent zone
			// would have to invent a name, and every invented name is one more thing a client
			// or an operator has to interpret.
			Name:   question.Name,
			Rrtype: dns.TypeSOA,
			Class:  question.Qclass,
			Ttl:    C.DefaultDNSNegativeTTL,
		},
		Ns:      "localhost.",
		Mbox:    "hostmaster.localhost.",
		Serial:  1,
		Refresh: 1800,
		Retry:   900,
		Expire:  604800,
		// RFC 2308: the negative TTL is min(SOA TTL, SOA MINIMUM). Both carry the same
		// constant so no client can read a longer lifetime than the other field promises.
		Minttl: C.DefaultDNSNegativeTTL,
	}}
	return response
}
