package trojan

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound/catalog"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

// CatalogProtocol is the catalog adapter for the SAME eligible Trojan shape the existing Lazy path
// already covers. It reuses newLazyRecipe for eligibility, so the two paths cannot drift on which
// options are admitted; the record is the recipe itself, encoded explicitly, not the options tree.
func CatalogProtocol() catalog.Protocol {
	return catalogTrojan{}
}

type catalogTrojan struct{}

func (catalogTrojan) Type() string { return C.TypeTrojan }

func (catalogTrojan) Encode(ctx context.Context, options any) ([]byte, catalog.Facts, bool) {
	trojanOptions, ok := options.(*option.TrojanOutboundOptions)
	if !ok || trojanOptions == nil {
		return nil, catalog.Facts{}, false
	}
	recipe, _ := newLazyRecipe(ctx, trojanOptions)
	if recipe == nil {
		return nil, catalog.Facts{}, false
	}
	payload, err := json.Marshal(catalogRecipeFrom(*recipe))
	if err != nil {
		return nil, catalog.Facts{}, false
	}
	return payload, catalog.Facts{
		Network:        recipe.network.Build(),
		ServerIsDomain: trojanOptions.ServerIsDomain(),
	}, true
}

func (catalogTrojan) Build(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, payload []byte) (adapter.Outbound, error) {
	var wire catalogRecipe
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, err
	}
	if wire.VersionWire != 1 {
		return nil, fmt.Errorf("trojan catalog: unsupported record version %d", wire.VersionWire)
	}
	return NewOutbound(ctx, router, logger, tag, wire.value().outboundOptions())
}

// catalogRecipe is the explicit, versioned rebuild payload. It mirrors lazyRecipe field for field and
// is checked by a corpus test that decodes it and compares the reconstructed options with the
// original; a field added to lazyRecipe without being added here fails that test.
type catalogRecipe struct {
	VersionWire int                 `json:"record_version"`
	Server      string              `json:"server"`
	ServerPort  uint16              `json:"server_port"`
	Password    string              `json:"password"`
	Network     catalog.NetworkList `json:"network"`

	TLSEngine           string             `json:"tls_engine,omitempty"`
	TLSDisableSNI       bool               `json:"tls_disable_sni,omitempty"`
	TLSServerName       string             `json:"tls_server_name,omitempty"`
	TLSInsecure         bool               `json:"tls_insecure,omitempty"`
	TLSALPN             catalog.StringList `json:"tls_alpn,omitempty"`
	TLSHandshakeTimeout badoption.Duration `json:"tls_handshake_timeout,omitempty"`

	Multiplex *option.OutboundMultiplexOptions `json:"multiplex,omitempty"`

	Dialer catalog.Dialer `json:"dialer"`
}

func catalogRecipeFrom(recipe lazyRecipe) catalogRecipe {
	return catalogRecipe{
		VersionWire:         1,
		Server:              recipe.server,
		ServerPort:          recipe.serverPort,
		Password:            recipe.password,
		Network:             catalog.WrapNetworkList(recipe.network),
		TLSEngine:           recipe.tlsEngine,
		TLSDisableSNI:       recipe.tlsDisableSNI,
		TLSServerName:       recipe.tlsServerName,
		TLSInsecure:         recipe.tlsInsecure,
		TLSALPN:             catalog.WrapStringList(recipe.tlsALPN),
		TLSHandshakeTimeout: recipe.tlsHandshakeTimeout,
		Multiplex:           copyMultiplex(recipe.multiplex),
		Dialer:              catalogDialerFrom(recipe.dialer),
	}
}

func (wire catalogRecipe) value() lazyRecipe {
	recipe := lazyRecipe{
		server:              wire.Server,
		serverPort:          wire.ServerPort,
		password:            wire.Password,
		network:             wire.Network.Value(),
		tlsEngine:           wire.TLSEngine,
		tlsDisableSNI:       wire.TLSDisableSNI,
		tlsServerName:       wire.TLSServerName,
		tlsInsecure:         wire.TLSInsecure,
		tlsALPN:             wire.TLSALPN.Value(),
		tlsHandshakeTimeout: wire.TLSHandshakeTimeout,
		multiplex:           copyMultiplex(wire.Multiplex),
		dialer:              lazyDialerFrom(wire.Dialer),
	}
	return recipe
}

func catalogDialerFrom(dialer lazyDialerRecipe) catalog.Dialer {
	return catalog.Dialer{
		ConnectTimeout:             dialer.connectTimeout,
		TCPFastOpen:                dialer.tcpFastOpen,
		TCPMultiPath:               dialer.tcpMultiPath,
		DisableTCPKeepAlive:        dialer.disableTCPKeepAlive,
		TCPKeepAlive:               dialer.tcpKeepAlive,
		TCPKeepAliveInterval:       dialer.tcpKeepAliveInterval,
		TCPKeepAliveSystemDefaults: dialer.tcpKeepAliveSystemDefaults,
		ReuseAddr:                  dialer.reuseAddr,
		ProtectPath:                dialer.protectPath,
		UDPBindPort:                dialer.udpBindPort,
		UDPFragment:                copyBoolPtr(dialer.udpFragment),
		UDPFragmentDefault:         dialer.udpFragmentDefault,
		DomainStrategy:             dialer.domainStrategy,
	}
}

func lazyDialerFrom(dialer catalog.Dialer) lazyDialerRecipe {
	return lazyDialerRecipe{
		connectTimeout:             dialer.ConnectTimeout,
		tcpFastOpen:                dialer.TCPFastOpen,
		tcpMultiPath:               dialer.TCPMultiPath,
		disableTCPKeepAlive:        dialer.DisableTCPKeepAlive,
		tcpKeepAlive:               dialer.TCPKeepAlive,
		tcpKeepAliveInterval:       dialer.TCPKeepAliveInterval,
		tcpKeepAliveSystemDefaults: dialer.TCPKeepAliveSystemDefaults,
		reuseAddr:                  dialer.ReuseAddr,
		protectPath:                dialer.ProtectPath,
		udpBindPort:                dialer.UDPBindPort,
		udpFragment:                copyBoolPtr(dialer.UDPFragment),
		udpFragmentDefault:         dialer.UDPFragmentDefault,
		domainStrategy:             dialer.DomainStrategy,
	}
}
