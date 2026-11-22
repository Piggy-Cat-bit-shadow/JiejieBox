package catalog_test

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/adapter/outbound/catalog"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/socks"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

type builderCounter interface {
	Builds() int
}

func socksOptions() *option.SOCKSOutboundOptions {
	return &option.SOCKSOutboundOptions{
		ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: 1080},
		Version:       "5",
		Network:       option.NetworkList(network.NetworkTCP),
	}
}

func TestStoreSharesIdenticalRecordsOnlyWithinAnOwner(t *testing.T) {
	store := catalog.NewStore()
	first, err := store.Put("provider-a", C.TypeSOCKS, []byte("same"))
	require.NoError(t, err)
	second, err := store.Put("provider-a", C.TypeSOCKS, []byte("same"))
	require.NoError(t, err)
	otherOwner, err := store.Put("provider-b", C.TypeSOCKS, []byte("same"))
	require.NoError(t, err)
	otherProtocol, err := store.Put("provider-a", C.TypeTrojan, []byte("same"))
	require.NoError(t, err)

	require.Equal(t, first.Generation(), second.Generation(), "identical bytes in one owner share one record")
	require.NotEqual(t, first.Generation(), otherOwner.Generation(), "another owner never shares")
	require.NotEqual(t, first.Generation(), otherProtocol.Generation(), "another protocol never shares")
	require.Equal(t, 3, store.Stats().Records)
	require.Equal(t, 12, store.Stats().Bytes)
	require.Equal(t, 4, store.Stats().Refs)

	first.Release()
	second.Release()
	otherOwner.Release()
	otherProtocol.Release()
	require.Zero(t, store.Stats().Records)
	require.Zero(t, store.Stats().Refs)
}

func TestStoreCloseKeepsLiveReferencesReadableAndRefusesNewRecords(t *testing.T) {
	store := catalog.NewStore()
	ref, err := store.Put("", C.TypeSOCKS, []byte("payload"))
	require.NoError(t, err)
	store.Close()
	require.True(t, ref.Valid(), "a live reference survives store.Close")
	require.Equal(t, []byte("payload"), ref.Payload())
	_, err = store.Put("", C.TypeSOCKS, []byte("after-close"))
	require.ErrorIs(t, err, catalog.ErrClosed)
	ref.Release()
	require.False(t, ref.Valid())
}

func TestStaleReferenceIsInvalid(t *testing.T) {
	store := catalog.NewStore()
	ref, err := store.Put("", C.TypeSOCKS, []byte("payload"))
	require.NoError(t, err)
	retained := ref.Retain()
	ref.Release()
	require.True(t, retained.Valid(), "the retained reference still owns the record")
	retained.Release()
	require.False(t, retained.Valid())
	require.False(t, retained.Retain().Valid(), "retaining a freed record must not resurrect it")
	require.Nil(t, retained.Payload())
}

func TestDescriptorMetadataNeverBuilds(t *testing.T) {
	store := catalog.NewStore()
	constructor := catalog.NewConstructor(store, socks.CatalogProtocol())
	object, handled, err := constructor.Deferred(context.Background(), nil, log.NewNOPFactory().Logger(), "gate", C.TypeSOCKS, socksOptions(), "")
	require.NoError(t, err)
	require.True(t, handled)
	require.NotNil(t, object)

	require.Equal(t, C.TypeSOCKS, object.Type())
	require.Equal(t, "gate", object.Tag())
	require.Equal(t, []string{network.NetworkTCP}, object.Network())
	require.Empty(t, object.Dependencies())
	multiplex, isMultiplex := object.(adapter.OutboundWithMultiplex)
	require.True(t, isMultiplex)
	require.False(t, multiplex.MultiplexEnabled())
	require.Zero(t, object.(builderCounter).Builds(), "a metadata query must not expand the node")
	require.Equal(t, 1, store.Stats().Records)

	require.NoError(t, object.(interface{ Close() error }).Close())
	require.Zero(t, store.Stats().Records, "the descriptor's Close releases its record reference")
}

func TestDescriptorBuildsOnceAndReleasesOnClose(t *testing.T) {
	store := catalog.NewStore()
	constructor := catalog.NewConstructor(store, socks.CatalogProtocol())
	object, handled, err := constructor.Deferred(context.Background(), nil, log.NewNOPFactory().Logger(), "gate", C.TypeSOCKS, socksOptions(), "")
	require.NoError(t, err)
	require.True(t, handled)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	destination := M.SocksaddrFrom(netip.MustParseAddr("127.0.0.1"), 1)
	_, dialErr := object.DialContext(ctx, network.NetworkTCP, destination)
	require.Error(t, dialErr, "the local port is closed; the build itself must still have run")
	require.Equal(t, 1, object.(builderCounter).Builds())
	require.Equal(t, 1, store.Stats().Refs, "the descriptor holds exactly one record reference")

	require.NoError(t, object.(interface{ Close() error }).Close())
	require.Zero(t, store.Stats().Records)
}

func TestUnsupportedShapeFallsBack(t *testing.T) {
	store := catalog.NewStore()
	constructor := catalog.NewConstructor(store, socks.CatalogProtocol())
	unsupported := socksOptions()
	unsupported.Version = "3"
	object, handled, err := constructor.Deferred(context.Background(), nil, log.NewNOPFactory().Logger(), "gate", C.TypeSOCKS, unsupported, "")
	require.NoError(t, err)
	require.False(t, handled)
	require.Nil(t, object)
	require.Zero(t, store.Stats().Records, "a refused shape must not allocate a record")
}

// TestOneNodeChangeDoesNotCopyOrPinTheWholeRecordSet is the C04 property at the record layer: a
// provider that changes one node must not copy the other N-1 records, and the unchanged descriptors
// keep their existing records valid. Releasing the changed node's old reference frees only that
// record, so memory follows the truly changed set instead of O(K x N).
func TestOneNodeChangeDoesNotCopyOrPinTheWholeRecordSet(t *testing.T) {
	const nodes = 1000
	store := catalog.NewStore()
	refs := make([]catalog.Ref, nodes)
	for i := 0; i < nodes; i++ {
		ref, err := store.Put("nodes", C.TypeSOCKS, []byte("node-"+strconv.Itoa(i)))
		require.NoError(t, err)
		refs[i] = ref
	}
	require.Equal(t, nodes, store.Stats().Records)

	// One node changes: a new record appears while the old one is still referenced.
	replacement, err := store.Put("nodes", C.TypeSOCKS, []byte("node-0-changed"))
	require.NoError(t, err)
	require.Equal(t, nodes+1, store.Stats().Records)
	require.Equal(t, nodes+1, store.Stats().Refs)

	refs[0].Release()
	require.Equal(t, nodes, store.Stats().Records, "the replaced record is freed; the other N-1 are untouched")
	for i := 1; i < nodes; i++ {
		require.True(t, refs[i].Valid(), "unchanged node %d keeps its record", i)
	}
	require.True(t, replacement.Valid())
	replacement.Release()
	for i := 1; i < nodes; i++ {
		refs[i].Release()
	}
	require.Zero(t, store.Stats().Records)
}

// TestDescriptorCanRebuildAfterEviction is P05 at the descriptor layer: the record is the durable
// form, so an idle expansion can be evicted and the next demand must reconstruct the same node.
func TestDescriptorCanRebuildAfterEviction(t *testing.T) {
	store := catalog.NewStore()
	constructor := catalog.NewConstructor(store, socks.CatalogProtocol())
	object, handled, err := constructor.Deferred(context.Background(), nil, log.NewNOPFactory().Logger(), "gate", C.TypeSOCKS, socksOptions(), "")
	require.NoError(t, err)
	require.True(t, handled)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	destination := M.SocksaddrFrom(netip.MustParseAddr("127.0.0.1"), 1)
	_, _ = object.DialContext(ctx, network.NetworkTCP, destination)
	require.Equal(t, 1, object.(builderCounter).Builds())
	require.True(t, object.(interface{ Evict() bool }).Evict(), "an idle expansion is evictable")
	_, _ = object.DialContext(ctx, network.NetworkTCP, destination)
	require.Equal(t, 2, object.(builderCounter).Builds(), "the record rebuilds the runtime after eviction")
	require.NoError(t, object.(interface{ Close() error }).Close())
	require.Zero(t, store.Stats().Records)
}

// TestPayloadIsCopiedAndImmutable is R06 at the store: a caller that mutates its own slice after Put
// cannot change the published record.
func TestPayloadIsCopiedAndImmutable(t *testing.T) {
	store := catalog.NewStore()
	payload := []byte("original")
	ref, err := store.Put("", C.TypeSOCKS, payload)
	require.NoError(t, err)
	payload[0] = 'X'
	require.Equal(t, []byte("original"), ref.Payload())
	ref.Release()
}

// TestMalformedRecordFailsClosed is R03 at the builder: a record that is not the protocol's payload
// returns an error instead of constructing a node.
func TestMalformedRecordFailsBeyondTheRecord(t *testing.T) {
	protocol := socks.CatalogProtocol()
	_, err := protocol.Build(context.Background(), nil, log.NewNOPFactory().Logger(), "bad", []byte("{not-json"))
	require.Error(t, err)
}

// TestHundredSingleNodeUpdatesDoNotGrow is the long-form C04: one node changes per round for 100
// rounds. Record count and reference count must stay flat; unchanged nodes keep their generation and
// are never copied into a new generation.
func TestHundredSingleNodeUpdatesDoNotGrow(t *testing.T) {
	const nodes = 1000
	store := catalog.NewStore()
	refs := make([]catalog.Ref, nodes)
	for i := range refs {
		ref, err := store.Put("nodes", C.TypeSOCKS, []byte("node-"+strconv.Itoa(i)))
		require.NoError(t, err)
		refs[i] = ref
	}
	generations := make([]uint64, nodes)
	for i := range refs {
		generations[i] = refs[i].Generation()
	}
	for round := 0; round < 100; round++ {
		index := round % nodes
		replacement, err := store.Put("nodes", C.TypeSOCKS, []byte("node-"+strconv.Itoa(index)+"-r"+strconv.Itoa(round)))
		require.NoError(t, err)
		refs[index].Release()
		refs[index] = replacement
		require.Equal(t, nodes, store.Stats().Records, "round %d must not add a record", round)
		require.Equal(t, nodes, store.Stats().Refs, "round %d must not add a reference", round)
	}
	for i := 900; i < nodes; i++ {
		require.Equal(t, generations[i], refs[i].Generation(), "an unchanged node keeps its generation")
	}
	for i := range refs {
		refs[i].Release()
	}
	require.Zero(t, store.Stats().Records)
}

type blockingProtocol struct {
	started chan struct{}
	release chan struct{}
}

func (p *blockingProtocol) Type() string { return "blocking" }

func (p *blockingProtocol) Encode(context.Context, any) ([]byte, catalog.Facts, bool) {
	return []byte("blocking"), catalog.Facts{Network: []string{network.NetworkTCP}}, true
}

func (p *blockingProtocol) Build(ctx context.Context, _ adapter.Router, _ log.ContextLogger, _ string, _ []byte) (adapter.Outbound, error) {
	close(p.started)
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &catalogBlockingOutbound{Adapter: outbound.NewAdapter("blocking", "blocking", []string{network.NetworkTCP}, nil)}, nil
}

type catalogBlockingOutbound struct {
	outbound.Adapter
}

func (o *catalogBlockingOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, net.ErrClosed
}

func (o *catalogBlockingOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// TestCloseDuringBuildDoesNotPublishAndFreesTheRecord is C02 at the catalog layer: Close while the
// builder is parked must not publish the runtime, and the record must stay alive only until the
// builder's own retained reference returns.
func TestCloseDuringBuildDoesNotPublishAndFreesTheRecord(t *testing.T) {
	protocol := &blockingProtocol{started: make(chan struct{}), release: make(chan struct{})}
	store := catalog.NewStore()
	constructor := catalog.NewConstructor(store, protocol)
	object, handled, err := constructor.Deferred(context.Background(), nil, log.NewNOPFactory().Logger(), "blocking", "blocking", nil, "")
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, 1, store.Stats().Records)

	dialed := make(chan error, 1)
	go func() {
		_, dialErr := object.DialContext(context.Background(), network.NetworkTCP, M.SocksaddrFrom(netip.MustParseAddr("127.0.0.1"), 1))
		dialed <- dialErr
	}()
	<-protocol.started
	require.NoError(t, object.(interface{ Close() error }).Close())
	close(protocol.release)
	require.Error(t, <-dialed, "a build that lost the Close race must not be published")
	_, built := object.(interface {
		Built() (adapter.Outbound, bool)
	}).Built()
	require.False(t, built)
	require.Zero(t, store.Stats().Records, "the builder's retained reference is the only owner and it released")
}

// TestLifecycleScopeCloseReleasesTheRecord is the production release path: the manager closes a
// component by closing its scope, not by calling Close itself. A descriptor whose Start did not
// register its own Close would leak the record here (the unit tests that call Close directly would
// still pass), so this test drives the scope.
func TestLifecycleScopeCloseReleasesTheRecord(t *testing.T) {
	store := catalog.NewStore()
	constructor := catalog.NewConstructor(store, socks.CatalogProtocol())
	object, handled, err := constructor.Deferred(context.Background(), nil, log.NewNOPFactory().Logger(), "scope", C.TypeSOCKS, socksOptions(), "")
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, 1, store.Stats().Records)

	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	require.NoError(t, object.(adapter.Lifecycle).Start(adapter.StartStateInitialize, scope))
	require.NoError(t, scope.Close())
	require.Zero(t, store.Stats().Records, "the scope drain must release the descriptor's record")
	require.Zero(t, store.Stats().Refs)
}

type domainFactProtocol struct{}

func (domainFactProtocol) Type() string { return "domain-fact" }

func (domainFactProtocol) Encode(context.Context, any) ([]byte, catalog.Facts, bool) {
	return []byte("domain"), catalog.Facts{Network: []string{network.NetworkTCP}, ServerIsDomain: true}, true
}

func (domainFactProtocol) Build(ctx context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ []byte) (adapter.Outbound, error) {
	return &catalogBlockingOutbound{Adapter: outbound.NewAdapter("domain-fact", tag, []string{network.NetworkTCP}, nil)}, nil
}

// TestDescriptorPublishesServerIsDomainFact: the fact is not only computed for the record, it must
// be visible on the published Adapter. A false answer hides a resolver edge from static validation
// and the cross-kind cycle check.
func TestDescriptorPublishesServerIsDomainFact(t *testing.T) {
	store := catalog.NewStore()
	constructor := catalog.NewConstructor(store, domainFactProtocol{})
	object, handled, err := constructor.Deferred(context.Background(), nil, log.NewNOPFactory().Logger(), "domain", "domain-fact", nil, "")
	require.NoError(t, err)
	require.True(t, handled)
	require.True(t, object.(interface{ ResolvesDomainServer() bool }).ResolvesDomainServer())
	require.NoError(t, object.(interface{ Close() error }).Close())
}
