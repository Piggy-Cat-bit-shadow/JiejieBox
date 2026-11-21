package provider

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/adapter/provider/nodestore"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/provider/parser"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

// This file reproduces the production path that panicked during the parallel D/M window:
//
//	ProviderLocal.reloadFile -> parser.ParseSubscription -> UpdateNodes -> nodestore.Encode
//
// The panic was `reflect: Elem of invalid type interface {}` from the interface branch of the
// encoder: it asked reflect.Type for the element type of an interface, which is not defined. The
// tests below feed the same mixed protocol document the provider readers produce, through the real
// parser and the real outbound manager, so the regression cannot come back unnoticed.

// mixedProviderDocument is the shape M used: Trojan, VLESS, AnyTLS, Hysteria2 and TUIC in one
// sing-box provider document, each with its own TLS block. The document reader decodes it into five
// different option types; the encoder must walk all of them without panicking.
const mixedProviderDocument = `{
  "outbounds": [
    {
      "type": "trojan", "tag": "trojan-node",
      "server": "trojan.example.com", "server_port": 443,
      "password": "trojan-secret",
      "tls": {"enabled": true, "server_name": "trojan.example.com", "alpn": ["h2", "http/1.1"]}
    },
    {
      "type": "vless", "tag": "vless-node",
      "server": "vless.example.com", "server_port": 443,
      "uuid": "b831381d-6324-4d53-ad4f-8cda48b30811",
      "tls": {"enabled": true, "server_name": "vless.example.com"}
    },
    {
      "type": "anytls", "tag": "anytls-node",
      "server": "anytls.example.com", "server_port": 443,
      "password": "anytls-secret",
      "tls": {"enabled": true, "server_name": "anytls.example.com"}
    },
    {
      "type": "hysteria2", "tag": "hy2-node",
      "server": "hy2.example.com", "server_port": 443,
      "password": "hy2-secret",
      "tls": {"enabled": true, "server_name": "hy2.example.com"}
    },
    {
      "type": "tuic", "tag": "tuic-node",
      "server": "tuic.example.com", "server_port": 443,
      "uuid": "b831381d-6324-4d53-ad4f-8cda48b30811",
      "password": "tuic-secret",
      "tls": {"enabled": true, "server_name": "tuic.example.com"}
    }
  ]
}`

// documentOptionsRegistry is the minimal option-type registry the document reader needs. The
// production registry is the same interface with every protocol registered; these five are the
// types in the document, and the constructors are never called by a decode.
type documentOptionsRegistry struct {
	options map[string]func() any
}

func newDocumentOptionsRegistry() *documentOptionsRegistry {
	registry := &documentOptionsRegistry{options: make(map[string]func() any)}
	registry.add("trojan", func() any { return new(option.TrojanOutboundOptions) })
	registry.add("vless", func() any { return new(option.VLESSOutboundOptions) })
	registry.add("anytls", func() any { return new(option.AnyTLSOutboundOptions) })
	registry.add("hysteria2", func() any { return new(option.Hysteria2OutboundOptions) })
	registry.add("tuic", func() any { return new(option.TUICOutboundOptions) })
	return registry
}

func (r *documentOptionsRegistry) add(outboundType string, constructor func() any) {
	r.options[outboundType] = constructor
}

func (r *documentOptionsRegistry) OptionTypes() []string { return nil }

func (r *documentOptionsRegistry) CreateOptions(outboundType string) (any, bool) {
	constructor, loaded := r.options[outboundType]
	if !loaded {
		return nil, false
	}
	return constructor(), true
}

func parseMixedDocument(t *testing.T) []option.Outbound {
	t.Helper()
	ctx := service.ContextWith[option.OutboundOptionsRegistry](context.Background(), newDocumentOptionsRegistry())
	nodes, err := parser.ParseSubscription(ctx, mixedProviderDocument, nil, "subscription")
	require.NoError(t, err)
	require.Len(t, nodes, 5, "the mixed document must decode into the five protocols")
	return nodes
}

// TestEncodeParsedMixedProviderDocument is the direct reproduction of the production panic: encode
// every node the real document reader produced. Before the fix this panicked on the first node,
// because every node's `Options` field is an interface.
func TestEncodeParsedMixedProviderDocument(t *testing.T) {
	nodes := parseMixedDocument(t)
	for _, node := range nodes {
		node := node
		t.Run(node.Type+"+"+node.Tag, func(t *testing.T) {
			payload, err := nodestore.Encode(node)
			require.NoError(t, err)
			require.NotEmpty(t, payload.Bytes)
			// A node record is compact, not a re-serialisation of the document.
			require.Less(t, len(payload.Bytes), 4096, "record %d bytes", len(payload.Bytes))
			require.True(t, nodestore.Equivalent(node, node))

			// The same document decoded twice must produce byte-identical records, which is what
			// lets an unchanged refresh keep its live objects.
			reparsed := parseMixedDocument(t)
			for _, other := range reparsed {
				if other.Type == node.Type && other.Tag == node.Tag {
					otherPayload, err := nodestore.Encode(other)
					require.NoError(t, err)
					require.Equal(t, payload.Bytes, otherPayload.Bytes)
					require.True(t, nodestore.Equivalent(node, other))
				}
			}
		})
	}
}

// documentNode is a protocol-agnostic runtime object: these tests exercise the document -> option
// tree -> record path, not a protocol's wire behaviour. Only construction is observed.
type documentNode struct {
	outbound.Adapter
}

func (n *documentNode) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage == adapter.StartStateInitialize {
		scope.Add(func() error { return nil })
	}
	return nil
}

func (n *documentNode) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("not used by the document tests")
}

func (*documentNode) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not used by the document tests")
}

func registerDocumentNode[T any](registry *outbound.Registry, outboundType string, builds *atomic.Int32) {
	outbound.Register[T](registry, outboundType, func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ T) (adapter.Outbound, error) {
		builds.Add(1)
		return &documentNode{Adapter: outbound.NewAdapter(outboundType, tag, []string{"tcp"}, nil)}, nil
	})
}

type documentFixture struct {
	provider *Adapter
	builds   *atomic.Int32
}

func newDocumentFixture(t *testing.T) *documentFixture {
	t.Helper()
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	factory := log.NewNOPFactory()
	logger := factory.Logger()
	registry := outbound.NewRegistry()
	var builds atomic.Int32
	registerDocumentNode[option.TrojanOutboundOptions](registry, "trojan", &builds)
	registerDocumentNode[option.VLESSOutboundOptions](registry, "vless", &builds)
	registerDocumentNode[option.AnyTLSOutboundOptions](registry, "anytls", &builds)
	registerDocumentNode[option.Hysteria2OutboundOptions](registry, "hysteria2", &builds)
	registerDocumentNode[option.TUICOutboundOptions](registry, "tuic", &builds)
	endpointManager := endpoint.NewManager(endpoint.NewRegistry())
	manager := outbound.NewManager(registry, endpointManager, "")
	// A static node is required before the start stages: the manager's default outbound is what
	// Start uses instead of calling the (unset) fallback. It is not part of the document under test.
	require.NoError(t, manager.Create(ctx, nil, logger, "static", "trojan", &option.TrojanOutboundOptions{}))
	scope := adapter.NewScope(ctx, logger)
	t.Cleanup(func() { require.NoError(t, scope.Close()) })
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, scope.Start("endpoint", endpointManager, stage))
		require.NoError(t, scope.Start("outbound", manager, stage))
	}
	adapterInstance := NewAdapter(ctx, nil, manager, factory, logger, "subscription", "inline", option.ProviderHealthCheckOptions{})
	require.NoError(t, adapterInstance.Start())
	t.Cleanup(func() { require.NoError(t, adapterInstance.Close()) })
	return &documentFixture{provider: &adapterInstance, builds: &builds}
}

// TestProviderUpdateParsedMixedDocument is the full path that panicked: the mixed document is
// decoded by the real parser and applied through the real UpdateNodes, so the encoder runs on the
// first apply (after each object is constructed) and again on an unchanged refresh.
func TestProviderUpdateParsedMixedDocument(t *testing.T) {
	fixture := newDocumentFixture(t)
	initialBuilds := fixture.builds.Load() // the static node
	nodes := parseMixedDocument(t)
	// A panic here fails the test; before the fix this line panicked with
	// "reflect: Elem of invalid type interface {}" inside Encode.
	require.NoError(t, fixture.provider.UpdateNodes(nodes))
	require.EqualValues(t, initialBuilds+5, fixture.builds.Load())
	require.Len(t, fixture.provider.Outbounds(), 5)
	require.Equal(t, 5, fixture.provider.storeRecords())
	require.Greater(t, fixture.provider.storeBytes(), int64(0))
	// Every supported protocol shape stays inside the canonical domain: no node may fall back to an
	// Opaque record, because that would silently rebuild it on every refresh. This is the regression
	// for "the safe fallback must not become the normal path".
	require.Zero(t, fixture.provider.opaqueRecords.Load(), "a supported document must not produce Opaque records")

	// An unchanged refresh of the same document must reuse every object and keep the records.
	require.NoError(t, fixture.provider.UpdateNodes(parseMixedDocument(t)))
	require.EqualValues(t, initialBuilds+5, fixture.builds.Load(), "an unchanged mixed document must not rebuild nodes")

	// A changed node must be rebuilt, and the store must follow it without touching the others.
	changed := parseMixedDocument(t)
	changed[0] = option.Outbound{
		Type: "trojan",
		Tag:  changed[0].Tag,
		Options: func() *option.TrojanOutboundOptions {
			options := *changed[0].Options.(*option.TrojanOutboundOptions)
			options.Password = "rotated-secret"
			return &options
		}(),
	}
	require.NoError(t, fixture.provider.UpdateNodes(changed))
	require.EqualValues(t, initialBuilds+6, fixture.builds.Load(), "the changed node must be rebuilt")
}

// TestProviderUpdateNilOptionsShapes covers the shapes a document cannot express but an embedder
// can: nil Options and a typed-nil pointer in the Options interface. Both must publish and encode
// without panicking; the encoder has to distinguish nil from a typed nil, and the manager accepts
// both as the zero options value.
func TestProviderUpdateNilOptionsShapes(t *testing.T) {
	fixture := newDocumentFixture(t)
	nodes := []option.Outbound{
		{Type: "trojan", Tag: "nil-options"},
		{Type: "vless", Tag: "typed-nil-options", Options: (*option.VLESSOutboundOptions)(nil)},
		{Type: "anytls", Tag: "empty-options", Options: &option.AnyTLSOutboundOptions{}},
		{Type: "hysteria2", Tag: "typed-nil-hy2", Options: (*option.Hysteria2OutboundOptions)(nil)},
		{Type: "tuic", Tag: "typed-nil-tuic", Options: (*option.TUICOutboundOptions)(nil)},
	}
	require.NoError(t, fixture.provider.UpdateNodes(nodes))
	require.Len(t, fixture.provider.Outbounds(), 5)
	require.Equal(t, 5, fixture.provider.storeRecords())
	// Nil Options and a non-nil interface holding a nil pointer are different values to DeepEqual,
	// and the record store must preserve that difference.
	initialBuilds := fixture.builds.Load()
	oldGeneration := fixture.provider.storeGeneration()
	require.NoError(t, fixture.provider.UpdateNodes(nodes))
	require.GreaterOrEqual(t, fixture.provider.storeGeneration(), oldGeneration)
	require.Equal(t, initialBuilds, fixture.builds.Load(), "the nil shapes are unchanged on a second apply")
}
