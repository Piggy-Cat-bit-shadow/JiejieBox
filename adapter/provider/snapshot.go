package provider

import (
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/provider/nodestore"
)

// appliedSnapshot is the provider's published configuration snapshot: one immutable store
// generation plus the tag -> record index map.
//
// # Why the record map is not a map of options
//
// An `option.Outbound` copy is a shallow header: it keeps the decoded options tree behind the
// interface alive. For a provider document that tree has no other owner after the apply returns -
// the outbound runtime copies the fields it needs - so the provider's change-detection copy was the
// last reference to the whole decoded configuration of every published node. The records below
// replace that reference with canonical bytes that are a few hundred bytes rather than a decoded
// struct graph.
//
// The snapshot is immutable once published. A refresh builds the next one and retires the previous
// store; a reader that must read records after the refresh holds a lease.
type appliedSnapshot struct {
	store   *nodestore.Store
	entries map[string]nodestore.Ref
}

// snapshotGeneration is a monotonically increasing number used to name a store generation. It is
// guarded by updateAccess, which serialises applies.
func (a *Adapter) nextSnapshotGeneration() uint64 {
	a.snapshotGeneration++
	return a.snapshotGeneration
}

// currentSnapshot returns the published snapshot. The caller must not retain the returned value
// across an apply that could replace it unless it also holds a lease; the apply path holds
// updateAccess and is therefore safe.
func (a *Adapter) currentSnapshot() *appliedSnapshot {
	a.snapshotAccess.RLock()
	defer a.snapshotAccess.RUnlock()
	return a.snapshot
}

// hasAppliedRecord reports whether this provider owns tag's configuration record. It replaces the
// `_, owned := a.appliedOutbounds[tag]` ownership test: a tag without a record was published by
// someone else and must not be replaced.
func (a *Adapter) hasAppliedRecord(tag string) bool {
	a.snapshotAccess.RLock()
	defer a.snapshotAccess.RUnlock()
	if a.snapshot == nil {
		return false
	}
	_, owned := a.snapshot.entries[tag]
	return owned
}

// appliedRecord returns the record reference the current snapshot holds for tag.
func (a *Adapter) appliedRecord(tag string) (nodestore.Ref, bool) {
	a.snapshotAccess.RLock()
	defer a.snapshotAccess.RUnlock()
	if a.snapshot == nil {
		return nodestore.Ref{}, false
	}
	ref, owned := a.snapshot.entries[tag]
	return ref, owned
}

// appliedMatches reports whether payload is exactly the record currently applied for tag. It is the
// replacement for `reflect.DeepEqual(*node.outbound, a.appliedOutbounds[tag])`: equal bytes mean the
// old comparison would have been true.
func (a *Adapter) appliedMatches(tag string, payload nodestore.Payload) bool {
	ref, owned := a.appliedRecord(tag)
	if !owned {
		return false
	}
	return ref.Store().Matches(ref, payload)
}

// appliedPayload reads the bytes of a record that must survive the next publication (a failed
// replacement keeps the previous object, so the next snapshot has to carry its record forward).
func (a *Adapter) appliedPayload(ref nodestore.Ref) ([]byte, bool) {
	if ref.IsZero() {
		return nil, false
	}
	payload, err := ref.Store().Payload(ref)
	if err != nil {
		return nil, false
	}
	return payload, true
}

// publishSnapshot installs next and retires the store it replaced. The previous store is closed
// only after the swap, and a close with outstanding leases defers the arena release to the last
// lease - that is the ordering that lets an old recipe read its old record after a same-tag
// replacement published a new one.
func (a *Adapter) publishSnapshot(next *appliedSnapshot) {
	a.snapshotAccess.Lock()
	previous := a.snapshot
	a.snapshot = next
	a.snapshotAccess.Unlock()
	if previous != nil && previous.store != nil && (next == nil || previous.store != next.store) {
		previous.store.Close()
	}
}

// publishAppliedSnapshot builds the next generation of records from the previous ownership map,
// the payloads this apply decided, and the set of objects the apply published, then swaps it in.
//
// It mirrors the deletion rule the option map had: an entry is dropped only when the tag is neither
// published by this apply nor still present in the outbound manager. The latter case is a node the
// manager refused to remove because another LIVE node names it as a dependency; keeping its record
// keeps ownership, so a later document that names the tag again is not mistaken for a foreign
// outbound and refused.
func (a *Adapter) publishAppliedSnapshot(nextRefs map[string]nodestore.Ref, records map[string]nodestore.Payload, published map[string]adapter.Outbound) {
	entries := nextRefs
	for tag := range records {
		if _, exists := entries[tag]; !exists {
			// Placeholder: the tag is being published for the first time and its record is built
			// below from records[tag].
			entries[tag] = nodestore.Ref{}
		}
	}
	for tag := range entries {
		if _, stillPublished := published[tag]; stillPublished {
			continue
		}
		if a.outbound != nil {
			if _, stillExists := a.outbound.Outbound(tag); stillExists {
				continue
			}
		}
		delete(entries, tag)
	}
	builder := nodestore.NewBuilder(a.nextSnapshotGeneration())
	indices := make(map[string]uint32, len(entries))
	for tag, oldRef := range entries {
		payload, havePayload := records[tag]
		if !havePayload {
			// The object was not rebuilt (unchanged, or its replacement failed), so the record must
			// be carried forward byte for byte. Reading it from the previous generation is exact by
			// construction; re-encoding the current document would instead record a configuration
			// that never produced the live object.
			previousBytes, loaded := a.appliedPayload(oldRef)
			if !loaded {
				continue
			}
			payload = nodestore.Payload{Bytes: previousBytes, Hash: nodestore.Hash(previousBytes)}
		}
		index, err := builder.Intern(payload)
		if err != nil {
			continue
		}
		indices[tag] = index
	}
	store := builder.Seal()
	next := &appliedSnapshot{entries: make(map[string]nodestore.Ref, len(indices))}
	if store != nil {
		next.store = store
		for tag, index := range indices {
			next.entries[tag] = store.Ref(index)
		}
	}
	a.publishSnapshot(next)
}

// closeSnapshot retires the published snapshot. It is called after the outbounds have been released
// during Close: at that point nothing in the provider can legitimately read a record any more, and a
// lease that is still held keeps its own generation alive.
func (a *Adapter) closeSnapshot() {
	a.publishSnapshot(nil)
}

// snapshotLease is the test/diagnostic reader of the published snapshot. It holds a real store lease,
// so it keeps reading the generation it observed even after the provider published the next one.
type snapshotLease struct {
	snapshot *appliedSnapshot
	lease    *nodestore.Lease
}

func (a *Adapter) acquireSnapshotLease() *snapshotLease {
	a.snapshotAccess.RLock()
	snapshot := a.snapshot
	var lease *nodestore.Lease
	if snapshot != nil && snapshot.store != nil {
		lease = snapshot.store.Acquire()
	}
	var entries map[string]nodestore.Ref
	if snapshot != nil {
		entries = make(map[string]nodestore.Ref, len(snapshot.entries))
		for tag, ref := range snapshot.entries {
			entries[tag] = ref
		}
	}
	a.snapshotAccess.RUnlock()
	if lease == nil {
		return nil
	}
	return &snapshotLease{
		snapshot: &appliedSnapshot{store: snapshot.store, entries: entries},
		lease:    lease,
	}
}

// Ref returns the record reference this lease observed for tag.
func (s *snapshotLease) Ref(tag string) (nodestore.Ref, bool) {
	if s == nil || s.snapshot == nil {
		return nodestore.Ref{}, false
	}
	ref, owned := s.snapshot.entries[tag]
	return ref, owned
}

// Payload reads tag's record through the lease.
func (s *snapshotLease) Payload(tag string) ([]byte, bool) {
	ref, owned := s.Ref(tag)
	if !owned {
		return nil, false
	}
	payload, err := s.lease.Payload(ref)
	if err != nil {
		return nil, false
	}
	return payload, true
}

// Generation is the store generation this lease observed.
func (s *snapshotLease) Generation() uint64 {
	if s == nil || s.snapshot == nil || s.snapshot.store == nil {
		return 0
	}
	return s.snapshot.store.Generation()
}

// storeGeneration reports the generation of the store the provider publishes right now.
func (a *Adapter) storeGeneration() uint64 {
	a.snapshotAccess.RLock()
	defer a.snapshotAccess.RUnlock()
	if a.snapshot == nil || a.snapshot.store == nil {
		return 0
	}
	return a.snapshot.store.Generation()
}

// storeBytes reports the arena bytes of the currently published store. Diagnostics only.
func (a *Adapter) storeBytes() int64 {
	a.snapshotAccess.RLock()
	defer a.snapshotAccess.RUnlock()
	if a.snapshot == nil || a.snapshot.store == nil {
		return 0
	}
	return a.snapshot.store.Bytes()
}

// storeRecords reports the number of records in the currently published store. Diagnostics only.
func (a *Adapter) storeRecords() int {
	a.snapshotAccess.RLock()
	defer a.snapshotAccess.RUnlock()
	if a.snapshot == nil || a.snapshot.store == nil {
		return 0
	}
	return a.snapshot.store.Records()
}

// Close releases the lease. The last release of a closed store frees its arena.
func (s *snapshotLease) Close() {
	if s == nil || s.lease == nil {
		return
	}
	s.lease.Release()
	s.lease = nil
}
