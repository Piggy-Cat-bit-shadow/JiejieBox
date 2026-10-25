package adapter

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/common/tlsspoof"
	"github.com/sagernet/sing-box/common/trafficclass"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/miekg/dns"
)

type Inbound interface {
	Lifecycle
	Type() string
	Tag() string
}

type TCPInjectableInbound interface {
	Inbound
	ConnectionHandler
}

type UDPInjectableInbound interface {
	Inbound
	PacketConnectionHandler
}

type InboundRegistry interface {
	option.InboundOptionsRegistry
	Create(ctx context.Context, router Router, logger log.ContextLogger, tag string, inboundType string, options any) (Inbound, error)
}

type InboundManager interface {
	Lifecycle
	Inbounds() []Inbound
	Get(tag string) (Inbound, bool)
	Create(ctx context.Context, router Router, logger log.ContextLogger, tag string, inboundType string, options any) error
}

// UDPConnectPacketConn marks a packet connection whose destination is FIXED for the
// lifetime of the tunnel.
//
// RFC 9298 CONNECT-UDP binds one tunnel to one target host:port, and RFC 9484
// CONNECT-IP binds one tunnel to one assigned address set. Every datagram on such a
// connection therefore goes to the same peer, which is exactly the precondition the
// connected-UDP fast path in route/conn.go needs.
//
// A caller cannot infer this from the connection's shape: a plain SOCKS/mixed UDP
// association also implements N.PacketConn and carries a destination per packet.
// Guessing from the destination would route an association that legitimately
// retargets into a socket that cannot accept a new destination. So the protocol
// layer that KNOWS the destination is fixed declares it here, and the router opts in
// on that declaration alone.
//
// The router sets InboundContext.UDPConnect when this reports true, which selects:
//
//	DialContext("udp", fixed target) over ListenPacket
//	bufio.NewUnbindPacketConn(connected) over the unconnected form
//
// That removes the per-packet destination lookup and enables the connected-socket
// batch read/write path (recvmmsg / sendmmsg / UDP GSO) downstream.
type UDPConnectPacketConn interface {
	// IsUDPConnect reports that this connection's destination is fixed.
	IsUDPConnect() bool
}

type InboundContext struct {
	Inbound     string
	InboundType string
	IPVersion   uint8
	Network     string
	Source      M.Socksaddr
	Destination M.Socksaddr
	User        string
	Outbound    string

	// ConsumesBypassVerdict reports that the caller of PreMatch is an entry whose transport
	// actually applies a Bypass verdict.
	//
	// # The one consumer
	//
	// sing-tun applies ActionBypass in exactly one place: its nfqueue handler, which answers
	// NfRepeat plus the output mark (nfqueue_linux.go, applyVerdict). That handler is what the
	// auto-redirect entry uses. Every other transport - the go, gVisor and system stacks - turns
	// the verdict into the same accept entry as ActionAccept (flow_dispatch.go install/installAccept,
	// and the forwarders branch only on reject, drop and hijack), so for them the verdict cannot
	// change where the bytes go: the splice decision and the userspace copy run identically.
	//
	// # Why the caller sets it, and a platform constant cannot
	//
	// auto_redirect is a MODE, not a platform. The same Linux binary - and the same privileged
	// Android binary, because android satisfies the linux build tag - also serves a plain
	// VPNService or desktop TUN, and iOS, macOS and Windows can never enter the mode at all. A
	// platform check cannot tell the two entries of one binary apart; the entry that owns the
	// nfqueue can, and it is the only one that knows whether its verdict will be applied.
	//
	// # What reads it
	//
	// route/preMatchFlow: the implicit Direct Fast Path authorisation and eligibility chain runs
	// only for a caller that can act on its answer. Explicit `action: bypass` rules and L0 route
	// sets do not consult it - those are operator and platform decisions, not this optimisation.
	ConsumesBypassVerdict bool

	// power report

	RouteRule     string
	RouteOutbound string
	OutboundChain []Outbound

	// TrafficClass is the resolved per-flow traffic class.
	//
	// # What it is
	//
	// Policy about what the flow is FOR, resolved once at route time from the logical outbound
	// chain. It is deliberately not a protocol property: no protocol reads this field, and the
	// component that acts on it is the generic copy layer.
	//
	// # Why it lives on the flow and not on the outbound
	//
	// The same leaf outbound serves several logical groups - a residential node reached through
	// the AI selector and through a general selector is one object - so storing a class on the
	// outbound would let one group's policy leak into the other. The class belongs to the FLOW,
	// and this struct is the per-flow metadata.
	//
	// # Multiplex
	//
	// This field is deliberately NOT copied by ContextForMultiplexSession. A shared multiplex
	// session carries many logical flows with different classes, so the session-level context
	// stays transport-scoped; inheriting the first flow's class would mark the underlying tunnel
	// permanently. See the test for that boundary.
	TrafficClass trafficclass.Class

	// sniffer

	Protocol     string
	Domain       string
	Client       string
	SniffContext any
	SnifferNames []string
	SniffError   error

	// cache

	// Deprecated: implement in rule action
	InboundDetour             string
	LastInbound               string
	OriginDestination         M.Socksaddr
	RouteOriginalDestination  M.Socksaddr
	UDPDisableDomainUnmapping bool
	UDPConnect                bool
	UDPTimeout                time.Duration
	// UoTDatagramDestinations is set when a UDP-over-TCP session carries a
	// per-datagram destination (the non-connect forms of UoT). The session
	// destination authorises only the session itself in that case, so each
	// datagram's own destination still has to be checked against the rules.
	UoTDatagramDestinations  bool
	TLSFragment              bool
	TLSFragmentFallbackDelay time.Duration
	TLSRecordFragment        bool
	TLSSpoof                 string
	TLSSpoofMethod           tlsspoof.Method

	NetworkStrategy     *C.NetworkStrategy
	NetworkType         []C.InterfaceType
	FallbackNetworkType []C.InterfaceType
	FallbackDelay       time.Duration

	DestinationAddresses                []netip.Addr
	DNSResponse                         *dns.Msg
	NamedDNSResponses                   map[string]*dns.Msg
	DestinationAddressMatchFromResponse bool
	SourceGeoIPCode                     string
	GeoIPCode                           string
	ProcessInfo                         *ConnectionOwner
	SourceMACAddress                    net.HardwareAddr
	SourceHostname                      string
	QueryType                           uint16
	QueryClientSubnet                   netip.Prefix
	QueryDNSSEC                         bool
	FakeIP                              bool
	PreMatch                            bool

	// rule cache

	IPCIDRMatchSource bool
	IPCIDRAcceptEmpty bool

	SourceAddressMatch           bool
	SourcePortMatch              bool
	DestinationAddressMatch      bool
	DestinationPortMatch         bool
	DeferredIPCIDRMatchGroups    uint8
	IgnoreDestinationIPCIDRMatch bool
}

func (c *InboundContext) ResetRuleCache() {
	c.IPCIDRMatchSource = false
	c.IPCIDRAcceptEmpty = false
	c.ResetRuleMatchCache()
}

func (c *InboundContext) ResetRuleMatchCache() {
	c.SourceAddressMatch = false
	c.SourcePortMatch = false
	c.DestinationAddressMatch = false
	c.DestinationPortMatch = false
	c.DeferredIPCIDRMatchGroups = 0
}

func (c *InboundContext) DNSResponseAddressesForMatch() []netip.Addr {
	return DNSResponseAddresses(c.DNSResponse)
}

func DNSResponseAddresses(response *dns.Msg) []netip.Addr {
	if response == nil || response.Rcode != dns.RcodeSuccess {
		return nil
	}
	addresses := make([]netip.Addr, 0, len(response.Answer))
	for _, rawRecord := range response.Answer {
		switch record := rawRecord.(type) {
		case *dns.A:
			addr := M.AddrFromIP(record.A)
			if addr.IsValid() {
				addresses = append(addresses, addr)
			}
		case *dns.AAAA:
			addr := M.AddrFromIP(record.AAAA)
			if addr.IsValid() {
				addresses = append(addresses, addr)
			}
		case *dns.HTTPS:
			for _, value := range record.SVCB.Value {
				switch hint := value.(type) {
				case *dns.SVCBIPv4Hint:
					for _, ip := range hint.Hint {
						addr := M.AddrFromIP(ip).Unmap()
						if addr.IsValid() {
							addresses = append(addresses, addr)
						}
					}
				case *dns.SVCBIPv6Hint:
					for _, ip := range hint.Hint {
						addr := M.AddrFromIP(ip)
						if addr.IsValid() {
							addresses = append(addresses, addr)
						}
					}
				}
			}
		}
	}
	return addresses
}

type inboundContextKey struct{}

type dnsTransportTagKey struct{}

func ContextWithDNSTransportTag(ctx context.Context, transportTag string) context.Context {
	return context.WithValue(ctx, (*dnsTransportTagKey)(nil), transportTag)
}

func DNSTransportTagFromContext(ctx context.Context) (string, bool) {
	transportTag, loaded := ctx.Value((*dnsTransportTagKey)(nil)).(string)
	return transportTag, loaded
}

func ContextForMultiplexSession(ctx context.Context) context.Context {
	var sessionContext InboundContext
	metadata := ContextFrom(ctx)
	if metadata != nil {
		sessionContext.Outbound = metadata.Outbound
	}
	ctx = ContextWithDNSTransportTag(ctx, "")
	ctx = interrupt.ContextWithIsExternalConnection(ctx, false)
	return WithContext(ctx, &sessionContext)
}

func WithContext(ctx context.Context, inboundContext *InboundContext) context.Context {
	return context.WithValue(ctx, (*inboundContextKey)(nil), inboundContext)
}

func ContextFrom(ctx context.Context) *InboundContext {
	metadata := ctx.Value((*inboundContextKey)(nil))
	if metadata == nil {
		return nil
	}
	return metadata.(*InboundContext)
}

func ExtendContext(ctx context.Context) (context.Context, *InboundContext) {
	var newMetadata InboundContext
	if metadata := ContextFrom(ctx); metadata != nil {
		newMetadata = *metadata
	}
	return WithContext(ctx, &newMetadata), &newMetadata
}

func OverrideContext(ctx context.Context) context.Context {
	if metadata := ContextFrom(ctx); metadata != nil {
		newMetadata := *metadata
		return WithContext(ctx, &newMetadata)
	}
	return ctx
}
