package option

import (
	"context"
	"reflect"
	"slices"

	"github.com/sagernet/sing-box/schema"
	"github.com/sagernet/sing/common/byteformats"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badjson"
	"github.com/sagernet/sing/common/json/badoption"
)

// UpstreamMaxHeaderBytes is the request header limit upstream sing-box uses when
// nothing overrides it. transport/http keeps its own copy for the HTTP/2 server;
// this one exists so option resolution can report the effective value without
// importing the transport layer.
const UpstreamMaxHeaderBytes = 1 << 20

type HTTP2Options struct {
	IdleTimeout             badoption.Duration       `json:"idle_timeout,omitempty"`
	KeepAlivePeriod         badoption.Duration       `json:"keep_alive_period,omitempty"`
	StreamReceiveWindow     *byteformats.MemoryBytes `json:"stream_receive_window,omitempty"`
	ConnectionReceiveWindow *byteformats.MemoryBytes `json:"connection_receive_window,omitempty"`
	MaxConcurrentStreams    int                      `json:"max_concurrent_streams,omitempty"`
}

type QUICOptions struct {
	HTTP2Options
	InitialPacketSize        int  `json:"initial_packet_size,omitempty"`
	InitialPacketSizeCeiling int  `json:"initial_packet_size_ceiling,omitempty"`
	DisablePathMTUDiscovery  bool `json:"disable_path_mtu_discovery,omitempty"`
	// BBRProfile sets the HTTP/3 *server* congestion control profile. It is
	// resolved from the http inbound `bbr_profile` option.
	BBRProfile ServerBBRProfile `json:"-"`
	// DisablePathManager turns off QUIC connection migration on the HTTP/3
	// server.
	//
	// Unset leaves quic-go's default, which has the path manager ENABLED. That is
	// what the references get: quic-go/masque-go and quic-go/connect-ip-go both
	// build their server without setting the field. Disabling migration is a
	// legitimate production choice, but it is a behaviour difference from both
	// the references and the library default, so it is opted into rather than
	// compiled in.
	DisablePathManager bool `json:"-"`
	// CongestionControl selects the QUIC congestion control algorithm.
	//
	// CLIENT HTTP/3: unset, "default" and "cubic" all select CUBIC explicitly
	// (NewClientCongestionControl). Leaving the sender to the library would be
	// Reno: this fork's quic-go builds its default with `use Reno = true`.
	//
	// SERVER HTTP/3 (the http and masque inbounds): the same field is honored
	// when bbr_profile is unset. "cubic" / "bbr" / "reno" select that sender;
	// "" / "default" keep quic-go's own sender (this inbound's documented
	// contract is that unset leaves the library default, which is Reno on the
	// pinned fork); an unknown value is refused at construction.
	//
	// bbr_profile and this field are MUTUALLY EXCLUSIVE: a configuration that
	// names both is refused instead of silently letting one win. That is what
	// keeps the production `"bbr_profile": "standard"` meaning exactly what it
	// meant before this field was honored. See
	// ValidateHTTP3ServerCongestionControl and resolveServerCongestionControl.
	CongestionControl string `json:"quic_congestion_control,omitempty" enum:"default,cubic,bbr,reno"`
}

// ValidateHTTP3ServerCongestionControl validates the two server-side congestion
// knobs of an HTTP/3 inbound (http, masque).
//
// bbr_profile is the pre-existing knob: it selects a BBR profile and must keep
// its exact meaning. quic_congestion_control is the shared algorithm selector;
// on a server it is honored only when bbr_profile is unset. Naming both is a
// configuration error rather than a silent precedence, because "BBR standard"
// and "cubic" are contradictory requests and whichever lost would be invisible.
//
// Accepted quic_congestion_control values on a server: "" / "default" (keep
// quic-go's own sender), "cubic", "bbr", "reno". Note that the CLIENT meaning of
// "default" is CUBIC; on a server it means the inbound's existing default,
// because this inbound's contract is "unset leaves the library default". The
// asymmetry is deliberate: changing the server default would alter every
// existing MASQUE/HTTP deployment that sets neither knob.
func ValidateHTTP3ServerCongestionControl(bbrProfile, congestionControl string) error {
	if bbrProfile != "" && congestionControl != "" {
		return E.New("bbr_profile and quic_congestion_control are mutually exclusive; set exactly one")
	}
	switch congestionControl {
	case "", "default", "cubic", "bbr", "reno":
		return nil
	default:
		return E.New("unknown quic_congestion_control: ", congestionControl,
			" (expected cubic, bbr, reno or empty)")
	}
}

type _HTTPClientOptions struct {
	Tag                     string               `json:"tag,omitempty"`
	Engine                  string               `json:"engine,omitempty" enum:"go,apple"`
	Version                 int                  `json:"version,omitempty" enum:"0,1,2,3"`
	DisableVersionFallback  bool                 `json:"disable_version_fallback,omitempty"`
	Headers                 badoption.HTTPHeader `json:"headers,omitempty"`
	HTTP2Options            HTTP2Options         `json:"-"`
	HTTP3Options            QUICOptions          `json:"-"`
	DefaultOutbound         bool                 `json:"-"`
	DisableEmptyDirectCheck bool                 `json:"-"`
	ResolveOnDetour         bool                 `json:"-"`
	DirectResolver          bool                 `json:"-"`
	OutboundTLSOptionsContainer
	DialerOptions
}

type (
	HTTPClient        _HTTPClientOptions
	HTTPClientOptions _HTTPClientOptions
)

func (h HTTPClient) Options() HTTPClientOptions {
	options := HTTPClientOptions(h)
	options.Tag = ""
	return options
}

func (o HTTPClientOptions) IsEmpty() bool {
	if o.Tag != "" {
		return false
	}
	o.DefaultOutbound = false
	o.ResolveOnDetour = false
	o.DirectResolver = false
	return reflect.ValueOf(_HTTPClientOptions(o)).IsZero()
}

func (o HTTPClientOptions) MarshalJSON() ([]byte, error) {
	if o.Tag != "" {
		return json.Marshal(o.Tag)
	}
	return badjson.MarshallObjects(_HTTPClientOptions(o), httpClientVariant(_HTTPClientOptions(o)))
}

func (o *HTTPClientOptions) UnmarshalJSONContext(ctx context.Context, content []byte) error {
	if len(content) > 0 && content[0] == '"' {
		*o = HTTPClientOptions{}
		return json.Unmarshal(content, &o.Tag)
	}
	var options _HTTPClientOptions
	err := json.UnmarshalContext(ctx, content, &options)
	if err != nil {
		return err
	}
	err = unmarshalHTTPVersionOptions(ctx, content, &options, options.Version, &options.HTTP2Options, &options.HTTP3Options)
	if err != nil {
		return err
	}
	options.Tag = ""
	*o = HTTPClientOptions(options)
	return nil
}

func (h HTTPClient) MarshalJSON() ([]byte, error) {
	return badjson.MarshallObjects(_HTTPClientOptions(h), httpClientVariant(_HTTPClientOptions(h)))
}

func (h *HTTPClient) UnmarshalJSONContext(ctx context.Context, content []byte) error {
	err := json.UnmarshalContext(ctx, content, (*_HTTPClientOptions)(h))
	if err != nil {
		return err
	}
	return unmarshalHTTPVersionOptions(ctx, content, (*_HTTPClientOptions)(h), h.Version, &h.HTTP2Options, &h.HTTP3Options)
}

func unmarshalHTTPVersionOptions(ctx context.Context, content []byte, baseStruct any, version int, http2Options *HTTP2Options, http3Options *QUICOptions) error {
	switch version {
	case 1:
		return json.UnmarshalContextDisallowUnknownFields(ctx, content, baseStruct)
	case 0, 2:
		return badjson.UnmarshallExcludedContext(ctx, content, baseStruct, http2Options)
	case 3:
		return badjson.UnmarshallExcludedContext(ctx, content, baseStruct, http3Options)
	default:
		return E.New("unknown HTTP version: ", version)
	}
}

// unmarshalHTTPVersionsOptions decodes an HTTP INBOUND option set.
func unmarshalHTTPVersionsOptions(ctx context.Context, content []byte, baseStruct any, versions []int, http2Options *HTTP2Options, http3Options *QUICOptions) error {
	for _, version := range versions {
		if version < 1 || version > 3 {
			return E.New("unknown HTTP version: ", version)
		}
	}
	switch {
	case slices.Contains(versions, 3):
		err := badjson.UnmarshallExcludedContext(ctx, content, baseStruct, http3Options)
		if err != nil {
			return err
		}
		*http2Options = http3Options.HTTP2Options
		return nil
	case slices.Contains(versions, 2):
		return badjson.UnmarshallExcludedContext(ctx, content, baseStruct, http2Options)
	default:
		return json.UnmarshalContextDisallowUnknownFields(ctx, content, baseStruct)
	}
}

func httpVersionsVariant(versions []int, http2Options HTTP2Options, http3Options QUICOptions) any {
	switch {
	case slices.Contains(versions, 3):
		return http3Options
	case slices.Contains(versions, 2):
		return http2Options
	default:
		return nil
	}
}

func httpVersionVariant(version int, http2Options HTTP2Options, http3Options QUICOptions) any {
	switch version {
	case 1:
		return nil
	case 0, 2:
		return http2Options
	case 3:
		return http3Options
	default:
		return nil
	}
}

func httpClientVariant(options _HTTPClientOptions) any {
	return httpVersionVariant(options.Version, options.HTTP2Options, options.HTTP3Options)
}

func describeHTTPClientObject(builder schema.Builder) (*schema.Node, error) {
	node := schema.StrictObject()
	err := builder.FlattenStruct(node, reflect.TypeFor[HTTPClient]())
	if err != nil {
		return nil, err
	}
	err = builder.FlattenStruct(node, reflect.TypeFor[QUICOptions]())
	if err != nil {
		return nil, err
	}
	return node, nil
}

func (h HTTPClient) DescribeSchema(builder schema.Builder) (*schema.Node, error) {
	return builder.Define("HTTPClient", func() (*schema.Node, error) {
		return describeHTTPClientObject(builder)
	})
}

func (o HTTPClientOptions) DescribeSchema(builder schema.Builder) (*schema.Node, error) {
	return builder.Define("HTTPClientReference", func() (*schema.Node, error) {
		clientObject, err := describeHTTPClientObject(builder)
		if err != nil {
			return nil, err
		}
		clientObject.Properties.Remove("tag")
		return schema.AnyOf(schema.TagReferenceNode("http_client"), clientObject), nil
	})
}
