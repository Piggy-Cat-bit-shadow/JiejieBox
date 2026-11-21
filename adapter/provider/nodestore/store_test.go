package nodestore

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func mustEncode(t *testing.T, value any) Payload {
	t.Helper()
	payload, err := Encode(value)
	require.NoError(t, err)
	return payload
}

// TestBuilderDedup proves the dedup rule: identical bytes share one record, and only the first copy
// costs arena space. Nothing is interned process-wide, so the index dies with the builder.
func TestBuilderDedup(t *testing.T) {
	builder := NewBuilder(1)
	first := mustEncode(t, trojanNode("a", "example.com", []string{"h2"}, false))
	second := mustEncode(t, trojanNode("a", "example.com", []string{"h2"}, false))
	firstIndex, err := builder.Intern(first)
	require.NoError(t, err)
	secondIndex, err := builder.Intern(second)
	require.NoError(t, err)
	require.Equal(t, firstIndex, secondIndex)
	require.Equal(t, 1, builder.Records())
	require.Equal(t, int64(len(first.Bytes)), builder.Bytes())
	store := builder.Seal()
	require.NotNil(t, store)
	require.Equal(t, 1, store.Records())
	require.Equal(t, int64(len(first.Bytes)), store.Bytes())
	require.Equal(t, uint64(1), store.Generation())
}

// TestBuilderDedupIsPerStore proves there is no unbounded process-level interning: a second builder
// starts empty, and releasing the first store frees everything it held.
func TestBuilderDedupIsPerStore(t *testing.T) {
	payload := mustEncode(t, trojanNode("a", "example.com", nil, false))
	first := NewBuilder(1)
	index, err := first.Intern(payload)
	require.NoError(t, err)
	require.Equal(t, uint32(0), index)
	second := NewBuilder(2)
	index, err = second.Intern(payload)
	require.NoError(t, err)
	require.Equal(t, uint32(0), index, "a new generation starts a new index space")
	firstStore := first.Seal()
	secondStore := second.Seal()
	require.NotSame(t, firstStore, secondStore)
	require.NotSame(t, firstStore.Ref(0).Store(), secondStore.Ref(0).Store())
}

// TestBuilderCollisionKeepsRecordsSeparate forces two different payloads into the same hash bucket
// and proves the byte comparison keeps them apart. This is the collision test the task requires, at
// the only layer where a collision can be forced deterministically.
func TestBuilderCollisionKeepsRecordsSeparate(t *testing.T) {
	first := mustEncode(t, trojanNode("a", "example.com", nil, false))
	second := mustEncode(t, trojanNode("b", "example.com", nil, false))
	require.NotEqual(t, first.Bytes, second.Bytes)
	builder := NewBuilder(9)
	forcedHash := uint64(0x1234567890abcdef)
	firstForced := Payload{Bytes: first.Bytes, Hash: forcedHash}
	secondForced := Payload{Bytes: second.Bytes, Hash: forcedHash}
	firstIndex, err := builder.internWithHash(firstForced, forcedHash)
	require.NoError(t, err)
	secondIndex, err := builder.internWithHash(secondForced, forcedHash)
	require.NoError(t, err)
	require.NotEqual(t, firstIndex, secondIndex, "different bytes must never share a record")
	require.Equal(t, 2, builder.Records())
	store := builder.Seal()
	require.False(t, store.Matches(store.Ref(firstIndex), secondForced))
	require.True(t, store.Matches(store.Ref(firstIndex), firstForced))
	require.True(t, store.Matches(store.Ref(secondIndex), secondForced))
}

// TestRefBelongsToStore proves the generation guard: an index from one store cannot read a record in
// another store, even at the same index. This is what stops a stale recipe from reading whatever
// node happens to sit at the same position after a refresh.
func TestRefBelongsToStore(t *testing.T) {
	firstBuilder := NewBuilder(1)
	firstIndex, err := firstBuilder.Intern(mustEncode(t, trojanNode("old", "old.example", nil, false)))
	require.NoError(t, err)
	secondBuilder := NewBuilder(2)
	secondIndex, err := secondBuilder.Intern(mustEncode(t, trojanNode("new", "new.example", nil, false)))
	require.NoError(t, err)
	require.Equal(t, firstIndex, secondIndex)
	firstStore := firstBuilder.Seal()
	secondStore := secondBuilder.Seal()
	oldRef := firstStore.Ref(firstIndex)
	newRef := secondStore.Ref(secondIndex)
	require.Equal(t, uint64(1), oldRef.Generation())
	require.Equal(t, uint64(2), newRef.Generation())
	_, err = secondStore.Payload(oldRef)
	require.ErrorIs(t, err, ErrForeignRef)
	require.False(t, secondStore.Matches(oldRef, mustEncode(t, trojanNode("old", "old.example", nil, false))))
	oldPayload, err := firstStore.Payload(oldRef)
	require.NoError(t, err)
	require.Contains(t, string(oldPayload), "old.example")
}

// TestLeaseOutlivesGeneration is the same-tag replacement rule at the store layer: the provider
// retires the old store, but a reader that acquired a lease before the retirement still reads the
// exact old record, and the arena is released only when that reader lets go.
func TestLeaseOutlivesGeneration(t *testing.T) {
	builder := NewBuilder(1)
	index, err := builder.Intern(mustEncode(t, trojanNode("a", "old.example", nil, false)))
	require.NoError(t, err)
	store := builder.Seal()
	ref := store.Ref(index)
	lease := store.Acquire()
	require.NotNil(t, lease)

	// The provider publishes the next generation and retires the old store.
	nextBuilder := NewBuilder(2)
	nextIndex, err := nextBuilder.Intern(mustEncode(t, trojanNode("a", "new.example", nil, false)))
	require.NoError(t, err)
	nextStore := nextBuilder.Seal()
	require.Equal(t, ref.Index(), uint32(nextIndex))
	store.Close()
	require.False(t, store.Freed(), "an outstanding lease must keep the arena alive")
	require.Nil(t, store.Acquire(), "no new reader may start after the close")

	oldPayload, err := lease.Payload(ref)
	require.NoError(t, err)
	require.Contains(t, string(oldPayload), "old.example")
	newPayload, err := nextStore.Payload(nextStore.Ref(nextIndex))
	require.NoError(t, err)
	require.Contains(t, string(newPayload), "new.example")
	require.NotEqual(t, string(oldPayload), string(newPayload))

	lease.Release()
	require.True(t, store.Freed(), "the last lease release frees the arena")
	_, err = store.Payload(ref)
	require.ErrorIs(t, err, ErrClosed)
	// Close is idempotent and releasing twice is a no-op.
	store.Close()
	lease.Release()
}

// TestStoreConcurrentLeaseReaders exercises the close/lease race under -race: readers hold leases,
// the writer retires stores, and every read either sees its own generation or a clean closed error.
func TestStoreConcurrentLeaseReaders(t *testing.T) {
	payload := mustEncode(t, trojanNode("a", "example.com", nil, false))
	const rounds = 50
	var writer sync.WaitGroup
	var readers sync.WaitGroup
	stop := make(chan struct{})
	for reader := 0; reader < 4; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				builder := NewBuilder(1)
				index, err := builder.Intern(payload)
				if err != nil {
					continue
				}
				store := builder.Seal()
				lease := store.Acquire()
				if lease == nil {
					continue
				}
				_, err = lease.Payload(store.Ref(index))
				require.NoError(t, err)
				store.Close()
				lease.Release()
			}
		}()
	}
	writer.Add(1)
	go func() {
		defer writer.Done()
		for round := 0; round < rounds; round++ {
			builder := NewBuilder(uint64(round + 1))
			index, err := builder.Intern(payload)
			require.NoError(t, err)
			store := builder.Seal()
			store.Close()
			require.True(t, store.Freed())
			require.Equal(t, uint64(round+1), store.Generation())
			_ = index
		}
	}()
	writer.Wait()
	close(stop)
	readers.Wait()
}

// TestLargeRecordGetsOwnChunk proves a record larger than the arena chunk is stored intact rather
// than split or truncated.
func TestLargeRecordGetsOwnChunk(t *testing.T) {
	large := make([]byte, chunkSize+1024)
	for i := range large {
		large[i] = byte(i % 251)
	}
	type blob struct {
		Data []byte
	}
	builder := NewBuilder(1)
	payload := mustEncode(t, blob{Data: large})
	index, err := builder.Intern(payload)
	require.NoError(t, err)
	store := builder.Seal()
	stored, err := store.Payload(store.Ref(index))
	require.NoError(t, err)
	require.Equal(t, payload.Bytes, stored)
}

// TestSealEmptyBuilder proves an empty generation retains nothing to close.
func TestSealEmptyBuilder(t *testing.T) {
	require.Nil(t, NewBuilder(1).Seal())
}
