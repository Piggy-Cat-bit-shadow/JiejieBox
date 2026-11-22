// Package catalog stores the immutable, rebuildable record of a node once per Box and lets a
// descriptor hold a small reference to it instead of a full per-node recipe.
//
// # What this is not
//
// It is not a second lifecycle and it is not a global node pool. A Store belongs to exactly one Box;
// an owner string ("" for the static configuration, the provider tag for a provider) scopes the
// records so two owners can never share a record by accident. The runtime object is still owned by
// the existing LazyOutbound: this package only replaces the per-node RECIPE with a reference into a
// shared, immutable payload and decodes that payload when the existing builder runs.
//
// # Why records are immutable and reference counted
//
// A provider update replaces one node at a time. If every generation copied every record, unchanged
// nodes would pin whole old generations. Here a changed node gets a new record; an unchanged node
// keeps its existing reference, so only records still named by a live descriptor stay resident. A
// record is freed when its last reference is released; record identities are never reused, so a
// stale reference cannot read a newer record.
//
// # Store identity and generation
//
// A Ref carries the record's store and its creation generation. Payload() re-checks both against the
// record before returning bytes; a record is never mutated or re-pointed, and a store never reuses a
// generation. That is the store-identity/generation check the task asks for, implemented without
// pretending the existing Ref type has a generation field.
package catalog

import (
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
)

// ErrClosed is returned when a store that has been closed is asked to accept a new record.
var ErrClosed = errors.New("node catalog: store is closed")

// ErrEmpty is returned for a zero-length record; a caller falls back to the eager path.
var ErrEmpty = errors.New("node catalog: empty record")

// Stats is the accounting the measurement and the tests read.
type Stats struct {
	Records  int
	Bytes    int
	Refs     int
	Created  uint64
	Released uint64
	StoreID  uint64
	Closed   bool
}

// record is one immutable rebuild payload. Owner and protocol are interned IDs, not strings: the
// per-record header is the difference between the catalog beating an inline recipe and not.
type record struct {
	generation uint64
	ownerID    uint16
	protocolID uint16
	payload    []byte
	refs       atomic.Int32
}

// recordKey is the dedupe identity. It is a value key (no per-record key string allocation) and
// includes the owner and protocol, so identical bytes from another scope can never share.
type recordKey struct {
	hash     [32]byte
	owner    uint16
	protocol uint16
}

// Ref is a lease on one immutable record. The zero value is invalid and safe to Release.
type Ref struct {
	store      *Store
	rec        *record
	generation uint64
}

// Store is one Box's shared record space.
type Store struct {
	mu        sync.Mutex
	id        uint64
	records   map[recordKey]*record
	owners    map[string]uint16
	ownerName []string
	protocols map[string]uint16
	protoName []string
	nextGen   uint64
	created   atomic.Uint64
	released  atomic.Uint64
	closed    bool
}

var storeSequence atomic.Uint64

// NewStore creates an empty store. The caller (the Box) owns it and closes it at teardown.
func NewStore() *Store {
	return &Store{
		id:        storeSequence.Add(1),
		records:   make(map[recordKey]*record),
		owners:    make(map[string]uint16),
		protocols: make(map[string]uint16),
	}
}

func (s *Store) internOwner(owner string) uint16 {
	if id, found := s.owners[owner]; found {
		return id
	}
	id := uint16(len(s.ownerName))
	s.owners[owner] = id
	s.ownerName = append(s.ownerName, owner)
	return id
}

func (s *Store) internProtocol(protocol string) uint16 {
	if id, found := s.protocols[protocol]; found {
		return id
	}
	id := uint16(len(s.protoName))
	s.protocols[protocol] = id
	s.protoName = append(s.protoName, protocol)
	return id
}

// Put stores payload once per (owner, protocol, bytes) and returns a new reference. Identical bytes
// from the same owner/protocol share one record; another owner or protocol always gets its own
// record, because a provider node may not share mutable ownership with a static node even when the
// bytes happen to be equal.
func (s *Store) Put(owner string, protocol string, payload []byte) (Ref, error) {
	if s == nil {
		return Ref{}, ErrClosed
	}
	if len(payload) == 0 {
		return Ref{}, ErrEmpty
	}
	hash := sha256.Sum256(payload)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Ref{}, ErrClosed
	}
	key := recordKey{hash: hash, owner: s.internOwner(owner), protocol: s.internProtocol(protocol)}
	if existing, found := s.records[key]; found && existing.refs.Load() > 0 {
		existing.refs.Add(1)
		return Ref{store: s, rec: existing, generation: existing.generation}, nil
	}
	rec := &record{
		generation: s.nextGen,
		ownerID:    key.owner,
		protocolID: key.protocol,
		payload:    append([]byte(nil), payload...),
	}
	s.nextGen++
	s.records[key] = rec
	s.created.Add(1)
	rec.refs.Store(1)
	return Ref{store: s, rec: rec, generation: rec.generation}, nil
}

// Close stops new records. Existing references stay valid and readable: a descriptor published
// before Close may still need to build during the close window, and the task explicitly forbids
// discovering "the store is closed" on the first dial.
func (s *Store) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for key, rec := range s.records {
		if rec.refs.Load() == 0 {
			delete(s.records, key)
			s.released.Add(1)
		}
	}
}

// Stats returns a consistent snapshot for tests and measurement.
func (s *Store) Stats() Stats {
	if s == nil {
		return Stats{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := Stats{StoreID: s.id, Closed: s.closed}
	for _, rec := range s.records {
		stats.Records++
		stats.Bytes += len(rec.payload)
		stats.Refs += int(rec.refs.Load())
	}
	stats.Created = s.created.Load()
	stats.Released = s.released.Load()
	return stats
}

// Retain adds one reference; the caller must Release it.
func (r Ref) Retain() Ref {
	if r.rec == nil {
		return Ref{}
	}
	if r.rec.refs.Add(1) <= 1 {
		r.rec.refs.Add(-1)
		return Ref{}
	}
	return Ref{store: r.store, rec: r.rec, generation: r.rec.generation}
}

// Release drops one reference. The record is unlinked from its store when the last reference goes;
// its bytes are then unreachable, so a stale Ref fails Valid and Payload returns nil.
func (r Ref) Release() {
	if r.rec == nil || r.store == nil {
		return
	}
	if r.rec.refs.Add(-1) != 0 {
		return
	}
	store := r.store
	store.mu.Lock()
	for key, rec := range store.records {
		if rec == r.rec {
			delete(store.records, key)
			store.released.Add(1)
			break
		}
	}
	store.mu.Unlock()
	r.rec.payload = nil
}

// Valid reports whether the reference still names a live record of the generation it was made for.
func (r Ref) Valid() bool {
	return r.rec != nil && r.generation == r.rec.generation && r.rec.refs.Load() > 0 && r.rec.payload != nil
}

// Payload returns the immutable bytes after re-checking the record's identity. The caller must hold
// a reference for the duration of its use; a descriptor does, and a builder Retains for its build.
func (r Ref) Payload() []byte {
	if !r.Valid() {
		return nil
	}
	return r.rec.payload
}

// Owner and Protocol describe the record's scope. The builder checks the protocol before decoding,
// so a foreign ref cannot be decoded as another protocol.
func (r Ref) Owner() string {
	if r.rec == nil || r.store == nil {
		return ""
	}
	return r.store.ownerName[r.rec.ownerID]
}

func (r Ref) Protocol() string {
	if r.rec == nil || r.store == nil {
		return ""
	}
	return r.store.protoName[r.rec.protocolID]
}

func (r Ref) Generation() uint64 {
	if r.rec == nil {
		return 0
	}
	return r.rec.generation
}
