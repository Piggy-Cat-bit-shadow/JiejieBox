package socks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound/catalog"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
)

// CatalogProtocol admits the SOCKS shapes whose eager constructor cannot fail after this check and
// whose normalized options round-trip through the explicit record. The conservative exclusions are
// the construction-time resource/error paths: detour/bind/resolver options, the authenticated idle
// pool, UoT/tuning objects and an explicit destination-DNS ownership.
func CatalogProtocol() catalog.Protocol {
	return catalogSocks{}
}

type catalogSocks struct{}

func (catalogSocks) Type() string { return C.TypeSOCKS }

type catalogSocksWire struct {
	VersionWire int                 `json:"record_version"`
	Server      string              `json:"server"`
	ServerPort  uint16              `json:"server_port"`
	Version     string              `json:"version,omitempty"`
	Username    string              `json:"username,omitempty"`
	Password    string              `json:"password,omitempty"`
	Network     catalog.NetworkList `json:"network"`
	Dialer      catalog.Dialer      `json:"dialer"`
}

func (catalogSocks) Encode(ctx context.Context, options any) ([]byte, catalog.Facts, bool) {
	socksOptions, ok := options.(*option.SOCKSOutboundOptions)
	if !ok || !catalogEligible(ctx, socksOptions) {
		return nil, catalog.Facts{}, false
	}
	wire := catalogSocksWire{
		VersionWire: 1,
		Server:      socksOptions.Server,
		ServerPort:  socksOptions.ServerPort,
		Version:     socksOptions.Version,
		Username:    socksOptions.Username,
		Password:    socksOptions.Password,
		Network:     catalog.WrapNetworkList(socksOptions.Network),
		Dialer:      catalog.WrapDialer(socksOptions.DialerOptions),
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, catalog.Facts{}, false
	}
	return payload, catalog.Facts{
		Network:        socksOptions.Network.Build(),
		ServerIsDomain: socksOptions.ServerIsDomain(),
	}, true
}

func (catalogSocks) Build(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, payload []byte) (adapter.Outbound, error) {
	var wire catalogSocksWire
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, err
	}
	if wire.VersionWire != 1 {
		return nil, fmt.Errorf("socks catalog: unsupported record version %d", wire.VersionWire)
	}
	return NewOutbound(ctx, router, logger, tag, wire.value())
}

func (wire catalogSocksWire) value() option.SOCKSOutboundOptions {
	socksOptions := option.SOCKSOutboundOptions{
		ServerOptions: option.ServerOptions{Server: wire.Server, ServerPort: wire.ServerPort},
		Version:       wire.Version,
		Username:      wire.Username,
		Password:      wire.Password,
		Network:       wire.Network.Value(),
	}
	wire.Dialer.Apply(&socksOptions.DialerOptions)
	return socksOptions
}

func catalogEligible(ctx context.Context, options *option.SOCKSOutboundOptions) bool {
	if options == nil || options.Server == "" {
		return false
	}
	switch options.Version {
	case "", "4", "4a", "5":
	default:
		return false
	}
	if options.DialerOptions.Detour != "" ||
		options.DialerOptions.BindInterface != "" ||
		options.DialerOptions.Inet4BindAddress != nil ||
		options.DialerOptions.Inet6BindAddress != nil ||
		options.DialerOptions.BindAddressNoPort ||
		options.DialerOptions.RoutingMark != 0 ||
		options.DialerOptions.NetNs != "" ||
		options.DialerOptions.DomainResolver != nil ||
		options.DialerOptions.NetworkStrategy != nil ||
		len(options.DialerOptions.NetworkType) > 0 ||
		len(options.DialerOptions.FallbackNetworkType) > 0 ||
		options.DialerOptions.FallbackDelay != 0 ||
		options.DialerOptions.DestinationDNSOwnership {
		return false
	}
	if options.TCPPreconnect != nil || options.UDPOverTCP != nil || options.TCPTuning != nil {
		return false
	}
	if options.ServerIsDomain() {
		// The eager constructor resolves the authority here and may return a configuration error.
		// Deferring that error to the first dial would move the error stage, so this node falls back.
		if _, err := dialer.ResolveAuthority(ctx, nil, true); err != nil {
			return false
		}
	}
	return true
}
