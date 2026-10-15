package constant

const (
	DefaultDNSTTL = 600

	// DefaultDNSNegativeTTL is the negative-caching lifetime the `adblock` DNS rule action
	// advertises in the SOA of the NXDOMAIN it generates locally.
	//
	// It follows the repository's DNS default rather than inventing a second convention: 600
	// seconds is what every locally answered record here already reports, and those answers are
	// the same class of locally manufactured fact. The value is deliberately not configurable -
	// the point of adblock is to be a zero-configuration terminal action.
	//
	// Unlike them it is a NEGATIVE TTL, so the tradeoff is not symmetric and both sides were
	// weighed: a shorter one has the resolver, not this process, spend the queries adblock exists
	// to remove, while a longer one can outlive a rule-set update in a downstream resolver's cache.
	// 600 seconds is the middle of that range and the shorter end of what blocking resolvers
	// advertise (AdGuard Home: 3600, Blocky: 3600).
	DefaultDNSNegativeTTL uint32 = DefaultDNSTTL
)

type DomainStrategy = uint8

const (
	DomainStrategyAsIS DomainStrategy = iota
	DomainStrategyPreferIPv4
	DomainStrategyPreferIPv6
	DomainStrategyIPv4Only
	DomainStrategyIPv6Only
)

const (
	DNSTypeLegacy      = "legacy"
	DNSTypeUDP         = "udp"
	DNSTypeTCP         = "tcp"
	DNSTypeTLS         = "tls"
	DNSTypeHTTPS       = "https"
	DNSTypeQUIC        = "quic"
	DNSTypeHTTP3       = "h3"
	DNSTypeLocal       = "local"
	DNSTypeHosts       = "hosts"
	DNSTypeFakeIP      = "fakeip"
	DNSTypeDHCP        = "dhcp"
	DNSTypeMDNS        = "mdns"
	DNSTypeTailscale   = "tailscale"
	DNSTypeOpenConnect = "openconnect"
	DNSTypeOpenVPN     = "openvpn"
	DNSTypeGroup       = "group"
)

const (
	DNSProviderAliDNS     = "alidns"
	DNSProviderCloudflare = "cloudflare"
	DNSProviderACMEDNS    = "acmedns"
)
