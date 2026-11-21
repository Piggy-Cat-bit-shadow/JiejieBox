package varch_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/adapter/provider/nodestore"
)

func internString(t *testing.T, builder *nodestore.Builder, value string) nodestore.Payload {
	t.Helper()
	payload, err := nodestore.Encode(value)
	if err != nil {
		t.Fatalf("encode %q: %v", value, err)
	}
	if _, err = builder.Intern(payload); err != nil {
		t.Fatalf("intern %q: %v", value, err)
	}
	return payload
}

// TestArchCStoreCrossGeneration covers the generation guard: a Ref (and a Lease) from one store can
// never address a record of another store, even when the index exists there.
func TestArchCStoreCrossGeneration(t *testing.T) {
	builderA := nodestore.NewBuilder(1)
	payloadA := internString(t, builderA, "alpha")
	internString(t, builderA, "beta")
	storeA := builderA.Seal()
	if storeA == nil {
		t.Fatal("store A is nil")
	}
	if storeA.Generation() != 1 {
		t.Fatalf("generation A = %d", storeA.Generation())
	}

	builderB := nodestore.NewBuilder(2)
	payloadB := internString(t, builderB, "gamma")
	storeB := builderB.Seal()

	refA := storeA.Ref(0)
	if _, err := storeB.Payload(refA); !errors.Is(err, nodestore.ErrForeignRef) {
		t.Fatalf("cross-store Payload error = %v, want ErrForeignRef", err)
	}
	if storeB.Matches(refA, payloadB) {
		t.Fatal("cross-store Matches returned true")
	}
	if storeA.Matches(refA, payloadA) != true {
		t.Fatal("same-store Matches returned false")
	}
	// Same numeric index, different store: must still be foreign.
	if refA.Index() != 0 || refA.Generation() != 1 {
		t.Fatalf("ref identity = %d/%d", refA.Generation(), refA.Index())
	}
	if _, ok := storeB.Ref(0), true; !ok {
		t.Fatal("unreachable")
	}
	leaseB := storeB.Acquire()
	if leaseB.Matches(storeB.Ref(0), payloadB) != true {
		t.Fatal("lease of B should match its own record")
	}
	if leaseB.Matches(refA, payloadB) {
		t.Fatal("a lease cannot borrow another store's ref")
	}
	leaseB.Release()
	leaseA := storeA.Acquire()
	if _, err := leaseA.Payload(storeB.Ref(0)); !errors.Is(err, nodestore.ErrForeignRef) {
		t.Fatalf("lease Payload cross-store error = %v", err)
	}
	leaseA.Release()
	storeA.Close()
	storeB.Close()
}

// TestArchCStoreCloseVsLease covers the close/lease ordering: Close retires the generation for new
// readers immediately, but existing leases keep the arena alive and readable until the last release.
func TestArchCStoreCloseVsLease(t *testing.T) {
	builder := nodestore.NewBuilder(7)
	payload := internString(t, builder, "record")
	store := builder.Seal()

	lease := store.Acquire()
	if lease == nil {
		t.Fatal("Acquire on an open store returned nil")
	}
	ref := store.Ref(0)
	store.Close()
	if store.Freed() {
		t.Fatal("arena was freed while a lease is outstanding")
	}
	if store.Acquire() != nil {
		t.Fatal("Acquire after Close must return nil: no new reader may start")
	}
	got, err := lease.Payload(ref)
	if err != nil {
		t.Fatalf("lease read after Close: %v", err)
	}
	if string(got) != string(payload.Bytes) {
		t.Fatal("lease read different bytes after Close")
	}
	if !lease.Matches(ref, payload) {
		t.Fatal("lease Matches after Close = false")
	}
	store.Close() // idempotent, must not free under the lease
	if store.Freed() {
		t.Fatal("second Close freed the arena under a lease")
	}
	lease.Release()
	if !store.Freed() {
		t.Fatal("last Release of a closed store did not free the arena")
	}
	if _, err = lease.Payload(ref); !errors.Is(err, nodestore.ErrClosed) {
		t.Fatalf("read after release error = %v, want ErrClosed", err)
	}
	lease.Release() // idempotent
}

// TestArchCStoreLeaseReleasedBeforeClose is the opposite order: release before close must not free
// (the owner has not retired the generation), and close must then free immediately.
func TestArchCStoreLeaseReleasedBeforeClose(t *testing.T) {
	builder := nodestore.NewBuilder(1)
	internString(t, builder, "x")
	store := builder.Seal()
	lease := store.Acquire()
	lease.Release()
	if store.Freed() {
		t.Fatal("store freed before Close")
	}
	if _, err := store.Payload(store.Ref(0)); err != nil {
		t.Fatalf("readable before Close: %v", err)
	}
	store.Close()
	if !store.Freed() {
		t.Fatal("Close after the last release must free immediately")
	}
}

// TestArchCStoreDedupAndPayloads covers record identity: identical payloads share one record, a
// one-byte difference does not, and payload bytes are independent of the builder.
func TestArchCStoreDedupAndPayloads(t *testing.T) {
	builder := nodestore.NewBuilder(1)
	first, err := nodestore.Encode(map[string]int{"a": 1, "b": 2})
	if err != nil {
		t.Fatal(err)
	}
	second, err := nodestore.Encode(map[string]int{"b": 2, "a": 1})
	if err != nil {
		t.Fatal(err)
	}
	indexFirst, err := builder.Intern(first)
	if err != nil {
		t.Fatal(err)
	}
	indexSecond, err := builder.Intern(second)
	if err != nil {
		t.Fatal(err)
	}
	if indexFirst != indexSecond {
		t.Fatal("the same configuration with a different map order became two records")
	}
	different, err := nodestore.Encode(map[string]int{"a": 1, "b": 3})
	if err != nil {
		t.Fatal(err)
	}
	indexDifferent, err := builder.Intern(different)
	if err != nil {
		t.Fatal(err)
	}
	if indexDifferent == indexFirst {
		t.Fatal("two different configurations share a record")
	}
	store := builder.Seal()
	if store.Records() != 2 {
		t.Fatalf("records = %d, want 2", store.Records())
	}
	payload, err := store.Payload(store.Ref(indexFirst))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != string(first.Bytes) {
		t.Fatal("stored payload differs from the interned payload")
	}
	// Mutating the caller's payload slice must not mutate the store (the builder copies bytes).
	first.Bytes[0] = 0xff
	stored, err := store.Payload(store.Ref(indexFirst))
	if err != nil {
		t.Fatal(err)
	}
	if stored[0] == 0xff {
		t.Fatal("store aliases the caller's payload bytes")
	}
	if payloadCopy := append([]byte(nil), stored...); string(payloadCopy) != string(payload) {
		t.Fatal("unreachable")
	}
}

// TestArchCStoreOpaqueNeverMatches proves the Opaque record is stable-unique: it can never equal
// another opaque record or a canonical one, which is what keeps ownership without a false match.
func TestArchCStoreOpaqueNeverMatches(t *testing.T) {
	builder := nodestore.NewBuilder(1)
	opaqueA := nodestore.Opaque()
	opaqueB := nodestore.Opaque()
	canonical, err := nodestore.Encode("value")
	if err != nil {
		t.Fatal(err)
	}
	indexOpaque, err := builder.Intern(opaqueA)
	if err != nil {
		t.Fatalf("opaque is not a valid payload: %v", err)
	}
	indexCanonical, err := builder.Intern(canonical)
	if err != nil {
		t.Fatal(err)
	}
	store := builder.Seal()
	refOpaque := store.Ref(indexOpaque)
	refCanonical := store.Ref(indexCanonical)
	if store.Matches(refOpaque, opaqueB) {
		t.Fatal("two opaque payloads matched")
	}
	if store.Matches(refOpaque, canonical) {
		t.Fatal("opaque matched a canonical payload")
	}
	if store.Matches(refCanonical, opaqueA) {
		t.Fatal("canonical matched an opaque payload")
	}
	// The payload itself is a valid Encode-shaped payload (version + marker) so Intern accepted it.
	if len(opaqueA.Bytes) < 2 || opaqueA.Bytes[1] != 0xff {
		t.Fatal("opaque payload layout changed")
	}
}

// TestArchCStoreHashIsFastRejectNotDecision: a payload with the right bytes but a wrong hash is
// rejected by Matches, and more importantly Matches always answers from a byte comparison. This
// pins the caller-visible contract; the collision path itself is covered in-package.
func TestArchCStoreHashIsFastRejectNotDecision(t *testing.T) {
	builder := nodestore.NewBuilder(1)
	payload := internString(t, builder, "hash")
	store := builder.Seal()
	ref := store.Ref(0)
	wrongHash := payload
	wrongHash.Hash ^= 0xffffffffffffffff
	if store.Matches(ref, wrongHash) {
		t.Fatal("a wrong hash matched")
	}
	sameLengthWrongBytes := payload
	sameLengthWrongBytes.Bytes = append([]byte(nil), payload.Bytes...)
	sameLengthWrongBytes.Bytes[len(sameLengthWrongBytes.Bytes)-1] ^= 0xff
	sameLengthWrongBytes.Hash = nodestore.Hash(sameLengthWrongBytes.Bytes)
	if store.Matches(ref, sameLengthWrongBytes) {
		t.Fatal("same-length different bytes matched")
	}
	if !store.Matches(ref, payload) {
		t.Fatal("the exact payload did not match")
	}
}

// TestArchCStoreConcurrentLeases is a plain (non-race) concurrency probe of the refcount: many
// concurrent leases on a retiring generation must all read and the arena must free exactly once.
// Without a race detector this is a smoke test, not a race proof.
func TestArchCStoreConcurrentLeases(t *testing.T) {
	builder := nodestore.NewBuilder(1)
	payload := internString(t, builder, "concurrent")
	store := builder.Seal()
	ref := store.Ref(0)
	const readers = 8
	// Take every lease before Close: a reader that starts after Close is *supposed* to get nil.
	leases := make([]*nodestore.Lease, readers)
	for i := range leases {
		leases[i] = store.Acquire()
		if leases[i] == nil {
			t.Fatalf("Acquire %d on an open store returned nil", i)
		}
	}
	store.Close()
	var wait sync.WaitGroup
	results := make(chan error, readers)
	for i := 0; i < readers; i++ {
		wait.Add(1)
		go func(lease *nodestore.Lease) {
			defer wait.Done()
			got, err := lease.Payload(ref)
			if err != nil {
				results <- err
				return
			}
			if !reflect.DeepEqual(got, payload.Bytes) {
				results <- errors.New("different bytes")
			}
			lease.Release()
		}(leases[i])
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !store.Freed() {
		t.Fatal("store not freed after all leases released")
	}
}
