package nodestore

import (
	"bytes"
	"errors"
	"sync"
)

// chunkSize is the arena allocation unit. A record that does not fit in a fresh chunk gets a chunk
// of its own, so no record is ever split across chunks and an offset always addresses one slice.
//
// 16 KiB keeps the rounding waste small for the small provider documents that are common in
// practice (a 200-node document with ~220-byte records is ~44 KiB of payload, so a 64 KiB chunk
// would waste a whole chunk), while a 1000-node document still needs only a handful of chunks.
const chunkSize = 16 * 1024

var (
	// ErrClosed reports a read from a store whose last lease has been released.
	ErrClosed = errors.New("nodestore: store is closed")
	// ErrForeignRef reports a reference used against a store it does not belong to. This is the
	// generation guard: an index from another generation can never address a record here.
	ErrForeignRef = errors.New("nodestore: reference belongs to another store")
	// ErrOutOfRange reports an index past the store's record count.
	ErrOutOfRange = errors.New("nodestore: reference index out of range")
	// ErrInvalidPayload reports a payload that was not produced by Encode or Opaque.
	ErrInvalidPayload = errors.New("nodestore: invalid payload")
)

type record struct {
	chunk  uint32
	offset uint32
	length uint32
	hash   uint64
}

// Ref identifies one immutable record in one store generation: a store identity, its generation
// number and an index. It deliberately cannot be reduced to the index alone, because an index is
// only meaningful in the store that produced it - a stale recipe holding index 3 must never read
// whatever node happens to sit at index 3 after a refresh.
type Ref struct {
	store *Store
	index uint32
}

// IsZero reports whether the reference is the zero value (no record).
func (r Ref) IsZero() bool {
	return r.store == nil
}

// Generation is the store generation the reference was created in.
func (r Ref) Generation() uint64 {
	if r.store == nil {
		return 0
	}
	return r.store.generation
}

// Index is the record position inside its store.
func (r Ref) Index() uint32 {
	return r.index
}

// Store is the store the reference addresses.
func (r Ref) Store() *Store {
	return r.store
}

// Store is an immutable, generation-tagged bank of configuration records. It is sealed once and
// never mutated afterwards; a refresh builds the next store and retires this one.
//
// # Lifetime
//
// The provider closes a store when its published snapshot stops referencing it. `Close` does not
// free the arena while a lease is outstanding: a reader that acquired a lease before the close keeps
// reading its old records, and the arena is released when the last lease is released. This is the
// ordering the provider needs when a same-tag replacement publishes a new generation while an old
// recipe still holds the old one.
type Store struct {
	generation uint64
	chunks     [][]byte
	records    []record
	arenaBytes int64

	mutex  sync.Mutex
	refs   int
	closed bool
	freed  bool
}

// Generation is the store generation number, monotonic inside one provider.
func (s *Store) Generation() uint64 {
	return s.generation
}

// Records is the number of records, including records that several tags share.
func (s *Store) Records() int {
	return len(s.records)
}

// Bytes is the arena size in bytes, excluding slice headers and record metadata.
func (s *Store) Bytes() int64 {
	return s.arenaBytes
}

// Ref builds a reference to record index. It panics on an index past the record count because that
// is a programming error inside the builder's own bookkeeping, not an input the caller can supply.
func (s *Store) Ref(index uint32) Ref {
	if int(index) >= len(s.records) {
		panic("nodestore: Ref index out of range")
	}
	return Ref{store: s, index: index}
}

// Acquire takes one lease. It returns nil once the store is closed: no new reader may start after
// the owner has decided the generation is over. Readers that already hold a lease are unaffected.
func (s *Store) Acquire() *Lease {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.closed || s.freed {
		return nil
	}
	s.refs++
	return &Lease{store: s}
}

// Close asks the store to release its arena. The release happens immediately when no lease is
// outstanding, and on the last Release otherwise. Close is idempotent.
func (s *Store) Close() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.freeLocked()
}

// Freed reports whether the arena has been released. It exists for tests and diagnostics.
func (s *Store) Freed() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.freed
}

func (s *Store) freeLocked() {
	if s.freed || !s.closed || s.refs > 0 {
		return
	}
	s.freed = true
	s.chunks = nil
	s.records = nil
}

// Payload returns the canonical bytes of ref. The slice is read-only: the store owns it.
func (s *Store) Payload(ref Ref) ([]byte, error) {
	if ref.store != s {
		return nil, ErrForeignRef
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.freed {
		return nil, ErrClosed
	}
	if int(ref.index) >= len(s.records) {
		return nil, ErrOutOfRange
	}
	return s.recordBytesLocked(s.records[ref.index])
}

// Matches reports whether payload is exactly the record ref stores. The hash is only a fast reject;
// the final answer is always a byte comparison, so a hash collision cannot make two configurations
// look equal.
func (s *Store) Matches(ref Ref, payload Payload) bool {
	if ref.store != s {
		return false
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.freed {
		return false
	}
	if int(ref.index) >= len(s.records) {
		return false
	}
	stored := s.records[ref.index]
	if stored.hash != payload.Hash || int(stored.length) != len(payload.Bytes) {
		return false
	}
	existing, err := s.recordBytesLocked(stored)
	if err != nil {
		return false
	}
	return bytes.Equal(existing, payload.Bytes)
}

func (s *Store) recordBytesLocked(entry record) ([]byte, error) {
	if int(entry.chunk) >= len(s.chunks) {
		return nil, ErrOutOfRange
	}
	chunk := s.chunks[entry.chunk]
	end := int(entry.offset) + int(entry.length)
	if end > len(chunk) {
		return nil, ErrOutOfRange
	}
	return chunk[entry.offset:end], nil
}

// Lease is one legitimate reader's hold on a store generation. Every record read that must survive
// its generation being retired goes through a lease, not through a bare Ref.
type Lease struct {
	store    *Store
	released bool
}

// Payload reads a record of the leased store.
func (l *Lease) Payload(ref Ref) ([]byte, error) {
	if l.store == nil || l.released {
		return nil, ErrClosed
	}
	return l.store.Payload(ref)
}

// Matches compares a payload against a record of the leased store.
func (l *Lease) Matches(ref Ref, payload Payload) bool {
	if l.store == nil || l.released {
		return false
	}
	return l.store.Matches(ref, payload)
}

// Store is the leased store.
func (l *Lease) Store() *Store {
	if l == nil {
		return nil
	}
	return l.store
}

// Release gives the lease back. The last release of a closed store frees its arena.
func (l *Lease) Release() {
	if l == nil || l.released {
		return
	}
	l.released = true
	store := l.store
	l.store = nil
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.refs > 0 {
		store.refs--
	}
	store.freeLocked()
}

// Builder assembles the next generation of records. It owns its dedup index only for the duration of
// one apply; there is no process-wide interning, so releasing the builder (and its store) releases
// every string and byte it held.
type Builder struct {
	generation uint64
	chunks     [][]byte
	tail       []byte
	records    []record
	hashIndex  map[uint64][]uint32
	arenaBytes int64
	sealed     bool
}

// NewBuilder starts a store generation.
func NewBuilder(generation uint64) *Builder {
	return &Builder{
		generation: generation,
		hashIndex:  make(map[uint64][]uint32),
	}
}

// Generation is the generation number this builder will seal into its store.
func (b *Builder) Generation() uint64 {
	return b.generation
}

// Intern adds payload, or returns the record index already holding exactly those bytes. Dedup is
// per builder, which means per provider generation: identical nodes in one document share one
// record, and nothing survives the store that held it.
func (b *Builder) Intern(payload Payload) (uint32, error) {
	if b.sealed {
		return 0, errors.New("nodestore: builder is sealed")
	}
	if len(payload.Bytes) == 0 || payload.Bytes[0] != formatVersion {
		return 0, ErrInvalidPayload
	}
	if payload.Hash != Hash(payload.Bytes) {
		return 0, ErrInvalidPayload
	}
	return b.internWithHash(payload, payload.Hash)
}

// internWithHash is the collision test's entry point: it forces the index hash so two different
// payloads land in the same bucket, and the test can observe that the byte comparison still keeps
// them in separate records.
func (b *Builder) internWithHash(payload Payload, forcedHash uint64) (uint32, error) {
	if b.sealed {
		return 0, errors.New("nodestore: builder is sealed")
	}
	if len(payload.Bytes) == 0 || payload.Bytes[0] != formatVersion {
		return 0, ErrInvalidPayload
	}
	for _, index := range b.hashIndex[forcedHash] {
		if bytes.Equal(b.recordBytes(b.records[index]), payload.Bytes) {
			return index, nil
		}
	}
	index, err := b.appendRecord(recordPayload{payload: payload, hash: forcedHash})
	if err != nil {
		return 0, err
	}
	b.hashIndex[forcedHash] = append(b.hashIndex[forcedHash], index)
	return index, nil
}

type recordPayload struct {
	payload Payload
	hash    uint64
}

func (b *Builder) appendRecord(input recordPayload) (uint32, error) {
	payload := input.payload
	size := len(payload.Bytes)
	if size > int(^uint32(0)) {
		return 0, errors.New("nodestore: record too large")
	}
	var chunkIndex uint32
	if size > chunkSize {
		b.chunks = append(b.chunks, append([]byte(nil), payload.Bytes...))
		chunkIndex = uint32(len(b.chunks) - 1)
	} else {
		if len(b.tail) == 0 || len(b.tail)+size > cap(b.tail) {
			b.tail = make([]byte, 0, chunkSize)
			b.chunks = append(b.chunks, b.tail)
			chunkIndex = uint32(len(b.chunks) - 1)
		} else {
			chunkIndex = uint32(len(b.chunks) - 1)
		}
		b.tail = append(b.tail, payload.Bytes...)
		// The chunk slice header must observe the grown tail.
		b.chunks[chunkIndex] = b.tail
	}
	offset := 0
	if size <= chunkSize {
		offset = len(b.tail) - size
	}
	b.records = append(b.records, record{
		chunk:  chunkIndex,
		offset: uint32(offset),
		length: uint32(size),
		hash:   input.hash,
	})
	b.arenaBytes += int64(size)
	return uint32(len(b.records) - 1), nil
}

func (b *Builder) recordBytes(entry record) []byte {
	chunk := b.chunks[entry.chunk]
	return chunk[entry.offset : int(entry.offset)+int(entry.length)]
}

// Bytes is the arena size accumulated so far.
func (b *Builder) Bytes() int64 {
	return b.arenaBytes
}

// Records is the number of records accumulated so far.
func (b *Builder) Records() int {
	return len(b.records)
}

// Seal freezes the builder into a store. A builder with no records seals into nil: there is nothing
// to retain and nothing to close.
func (b *Builder) Seal() *Store {
	if b.sealed {
		panic("nodestore: builder sealed twice")
	}
	b.sealed = true
	if len(b.records) == 0 {
		return nil
	}
	chunks := make([][]byte, len(b.chunks))
	for i, chunk := range b.chunks {
		chunks[i] = chunk[:len(chunk):len(chunk)]
	}
	return &Store{
		generation: b.generation,
		chunks:     chunks,
		records:    b.records,
		arenaBytes: b.arenaBytes,
	}
}
