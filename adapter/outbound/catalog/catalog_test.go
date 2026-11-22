package catalog_test

import (
	"context"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
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
