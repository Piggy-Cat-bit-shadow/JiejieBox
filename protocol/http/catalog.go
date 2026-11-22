package http

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
	transporthttp "github.com/sagernet/sing-box/transport/http"
	"github.com/sagernet/sing/common/json/badoption"
	N "github.com/sagernet/sing/common/network"
)

// CatalogProtocol admits plain HTTP and the standard-TLS HTTP shape with no construction-time
// resource or error path. HTTP/2 and HTTP/3 have their own transports, so versions 2/3 and their
// option bags stay on the eager path (they also cannot round-trip through the base record).
func CatalogProtocol() catalog.Protocol {
	return catalogHTTP{}
}

type catalogHTTP struct{}

func (catalogHTTP) Type() string { return C.TypeHTTP }

type catalogHTTPWire struct {
	VersionWire            int                 `json:"record_version"`
	Server                 string              `json:"server"`
	ServerPort             uint16              `json:"server_port"`
	Username               string              `json:"username,omitempty"`
	Password               string              `json:"password,omitempty"`
	Path                   string              `json:"path,omitempty"`
	Version                int                 `json:"version,omitempty"`
	DisableVersionFallback bool                `json:"disable_version_fallback,omitempty"`
	TLS                    *catalogHTTPTLS     `json:"tls,omitempty"`
	Headers                map[string][]string `json:"headers,omitempty"`
	Dialer                 catalog.Dialer      `json:"dialer"`
}

type catalogHTTPTLS struct {
	Enabled          bool               `json:"enabled"`
	Engine           string             `json:"engine,omitempty"`
	ServerName       string             `json:"server_name,omitempty"`
	Insecure         bool               `json:"insecure,omitempty"`
	DisableSNI       bool               `json:"disable_sni,omitempty"`
	ALPN             catalog.StringList `json:"alpn,omitempty"`
	HandshakeTimeout badoption.Duration `json:"handshake_timeout,omitempty"`
}

func (catalogHTTP) Encode(ctx context.Context, options any) ([]byte, catalog.Facts, bool) {
	httpOptions, ok := options.(*option.HTTPOutboundOptions)
	if !ok || !catalogEligible(ctx, httpOptions) {
		return nil, catalog.Facts{}, false
	}
	wire := catalogHTTPWire{
		VersionWire:            1,
		Server:                 httpOptions.Server,
		ServerPort:             httpOptions.ServerPort,
		Username:               httpOptions.Username,
		Password:               httpOptions.Password,
		Path:                   httpOptions.Path,
		Version:                httpOptions.Version,
		DisableVersionFallback: httpOptions.DisableVersionFallback,
		Headers:                catalogHeaders(httpOptions.Headers),
		Dialer:                 catalog.WrapDialer(httpOptions.DialerOptions),
	}
	if httpOptions.TLS != nil {
		wire.TLS = &catalogHTTPTLS{
			Enabled:          httpOptions.TLS.Enabled,
			Engine:           httpOptions.TLS.Engine,
			ServerName:       httpOptions.TLS.ServerName,
			Insecure:         httpOptions.TLS.Insecure,
			DisableSNI:       httpOptions.TLS.DisableSNI,
			ALPN:             catalog.WrapStringList(httpOptions.TLS.ALPN),
			HandshakeTimeout: httpOptions.TLS.HandshakeTimeout,
		}
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, catalog.Facts{}, false
	}
	return payload, catalog.Facts{
		Network:        []string{N.NetworkTCP, N.NetworkUDP},
		ServerIsDomain: httpOptions.ServerIsDomain(),
	}, true
}

func (catalogHTTP) Build(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, payload []byte) (adapter.Outbound, error) {
	var wire catalogHTTPWire
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, err
	}
	if wire.VersionWire != 1 {
		return nil, fmt.Errorf("http catalog: unsupported record version %d", wire.VersionWire)
	}
	return NewOutbound(ctx, router, logger, tag, wire.value())
}

func (wire catalogHTTPWire) value() option.HTTPOutboundOptions {
	httpOptions := option.HTTPOutboundOptions{
		ServerOptions:          option.ServerOptions{Server: wire.Server, ServerPort: wire.ServerPort},
		Username:               wire.Username,
		Password:               wire.Password,
		Path:                   wire.Path,
		Version:                wire.Version,
		DisableVersionFallback: wire.DisableVersionFallback,
		Headers:                restoreHeaders(wire.Headers),
	}
	wire.Dialer.Apply(&httpOptions.DialerOptions)
	if wire.TLS != nil {
		httpOptions.TLS = &option.OutboundTLSOptions{
			Enabled:          wire.TLS.Enabled,
			Engine:           wire.TLS.Engine,
			ServerName:       wire.TLS.ServerName,
			Insecure:         wire.TLS.Insecure,
			DisableSNI:       wire.TLS.DisableSNI,
			ALPN:             wire.TLS.ALPN.Value(),
			HandshakeTimeout: wire.TLS.HandshakeTimeout,
		}
	}
	return httpOptions
}

func catalogHeaders(headers badoption.HTTPHeader) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	wire := make(map[string][]string, len(headers))
	for name, values := range headers {
		wire[name] = append([]string(nil), values...)
	}
	return wire
}

func restoreHeaders(headers map[string][]string) badoption.HTTPHeader {
	if len(headers) == 0 {
		return nil
	}
	restored := make(badoption.HTTPHeader, len(headers))
	for name, values := range headers {
		restored[name] = badoption.Listable[string](append([]string(nil), values...))
	}
	return restored
}

func catalogEligible(ctx context.Context, options *option.HTTPOutboundOptions) bool {
	if options == nil || options.Server == "" {
		return false
	}
	// Admit HTTP/1.x only. Version 0 is NOT HTTP/1: with no path and no Host header the
	// transport resolves it to HTTP/2, whose options are not in the record. Rejecting by the
	// EFFECTIVE version (and any non-zero HTTP/2 option bag) is what prevents a silent option drop.
	host := options.Headers.Build().Get("Host")
	if transporthttp.ResolveVersion(options.Version, options.Path, host) != 1 {
		return false
	}
	if options.HTTP2Options != (option.HTTP2Options{}) {
		return false
	}
	if options.Path != "" && host != "" {
		// The eager constructor refuses this combination; admitting it would move the error to dial.
		return false
	}
	if options.DisableVersionFallback && (options.TLS == nil || !options.TLS.Enabled) {
		// The eager client reports "HTTP/2 requires TLS" for this shape.
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
	if options.TLS != nil && options.TLS.Enabled {
		tlsOptions := options.TLS
		if tlsOptions.Engine != "" && tlsOptions.Engine != C.TLSEngineGo {
			return false
		}
		if tlsOptions.Reality != nil || tlsOptions.UTLS != nil || tlsOptions.ECH != nil {
			return false
		}
		if len(tlsOptions.Certificate) > 0 || tlsOptions.CertificatePath != "" ||
			len(tlsOptions.CertificateSHA256) > 0 || len(tlsOptions.CertificatePublicKeySHA256) > 0 {
			return false
		}
		if len(tlsOptions.ClientCertificate) > 0 || tlsOptions.ClientCertificatePath != "" ||
			len(tlsOptions.ClientKey) > 0 || len(tlsOptions.ClientKeyPath) > 0 {
			return false
		}
		if tlsOptions.MinVersion != "" || tlsOptions.MaxVersion != "" ||
			tlsOptions.CipherSuites != nil || tlsOptions.CurvePreferences != nil {
			return false
		}
		if tlsOptions.Fragment || tlsOptions.FragmentFallbackDelay != 0 || tlsOptions.RecordFragment ||
			tlsOptions.Spoof != "" || tlsOptions.SpoofMethod != "" {
			return false
		}
		if tlsOptions.KernelTx || tlsOptions.KernelRx {
			return false
		}
	}
	if options.ServerIsDomain() {
		if _, err := dialer.ResolveAuthority(ctx, nil, true); err != nil {
			return false
		}
	}
	return true
}
