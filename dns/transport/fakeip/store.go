package fakeip

import (
	"context"
	"net/netip"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/service"
)

const reservedAddressCount = 1024

// The two generation indices a store uses. They are constants rather than counters because a store
// serves exactly one live generation at a time and retires at most one, and the LEDGER tick - not the
// index - is what separates one store's live generation from another's. `FakeIPMetadata` carries no
// generation index at all, so a restart finds the same index again; if the index were a counter it
// would have to be persisted, which is exactly the new disk format this change must not introduce.
const (
	generationRetired = 0
	generationLive    = 1
)

var _ adapter.FakeIPStore = (*Store)(nil)

type Store struct {
	ctx        context.Context
	logger     logger.Logger
	inet4Range netip.Prefix
	inet6Range netip.Prefix
	inet4Last  netip.Addr
	inet6Last  netip.Addr
	storage    adapter.FakeIPStorage

	// ledger remembers what this store has handed out, for longer than this store lives. It is looked
	// up from the context, so it belongs to whoever owns the sequence of Boxes; when there is none,
	// nothing is recorded and the Box has no cross-Box issuance memory. See adapter.FakeIPIssuanceLedger.
	//
	// It is held as the CONCRETE adapter type, not as an interface, because that is what the registry
	// hands back: `service.MustRegisterPtr[adapter.FakeIPIssuanceLedger]` keys on
	// `common.DefaultValue[*adapter.FakeIPIssuanceLedger]()` and `service.PtrFromContext` returns
	// exactly that pointer. A pointer to an interface would be a different key, and a mismatched pair
	// returns nil rather than failing to compile - which is why the pair is pinned by a test.
	ledger *adapter.FakeIPIssuanceLedger
	// generationSequence is the ledger tick this store's ranges belong to, taken once at Start.
	// `FakeIPMetadata` carries no generation index, so a restart finds the same index again; the tick
	// is what separates the generation this store serves from the one it retired.
	generationSequence uint64

	addressAccess sync.Mutex
	inet4Current  netip.Addr
	inet6Current  netip.Addr
	reservedCount int
}

func NewStore(ctx context.Context, logger logger.Logger, inet4Range netip.Prefix, inet6Range netip.Prefix) *Store {
	store := &Store{
		ctx:        ctx,
		logger:     logger,
		inet4Range: inet4Range,
		inet6Range: inet6Range,
	}
	if inet4Range.IsValid() {
		store.inet4Last = broadcastAddress(inet4Range)
	}
	if inet6Range.IsValid() {
		store.inet6Last = broadcastAddress(inet6Range)
	}
	return store
}

func broadcastAddress(prefix netip.Prefix) netip.Addr {
	addr := prefix.Addr()
	raw := addr.As16()
	bits := prefix.Bits()
	if addr.Is4() {
		bits += 96
	}
	for i := bits; i < 128; i++ {
		raw[i/8] |= 1 << (7 - i%8)
	}
	if addr.Is4() {
		return netip.AddrFrom4([4]byte(raw[12:]))
	}
	return netip.AddrFrom16(raw)
}

func nextAddress(addressRange netip.Prefix, last netip.Addr, current netip.Addr) netip.Addr {
	address := current.Next()
	if address == last || !addressRange.Contains(address) {
		address = addressRange.Addr().Next().Next()
	}
	return address
}

func (s *Store) Start() error {
	// The ledger, when one exists, is owned by whoever owns the sequence of Boxes rather than by this
	// Box, so it is looked up from the context exactly as the cache file is. Nothing is recorded at
	// construction: a configuration check builds a Box and closes it without ever starting it, and a
	// write from there would land in a shared ledger while a real Box is running.
	s.ledger = service.PtrFromContext[adapter.FakeIPIssuanceLedger](s.ctx)
	if s.ledger != nil {
		// The tick is taken BEFORE the retired generation is recorded, so the two do not collide.
		// `FakeIPMetadata` carries no generation index, so a restart finds the same index again and the
		// tick is the only thing separating "the generation that just handed this address out" from
		// "the generation that is replacing it".
		s.generationSequence = s.ledger.Advance()
	}
	var storage adapter.FakeIPStorage
	cacheFile := service.FromContext[adapter.CacheFile](s.ctx)
	if cacheFile != nil && cacheFile.StoreFakeIP() {
		storage = cacheFile
	}
	if storage == nil {
		storage = NewMemoryStorage()
	}
	metadata := storage.FakeIPMetadata()
	if metadata != nil && metadata.Inet4Range == s.inet4Range && metadata.Inet6Range == s.inet6Range {
		s.inet4Current = metadata.Inet4Current
		s.inet6Current = metadata.Inet6Current
	} else {
		// The persisted metadata describes a generation that is about to be DESTROYED by the reset
		// below, and it is the only surviving statement of what that generation handed out. Its ranges
		// and cursors are read here, BEFORE the reset, so the addresses a client may still be holding
		// stay attributable after the configuration that issued them is gone.
		//
		// This is the durable proof source, and it exists only when `experimental.cache_file` has
		// `store_fakeip` on: `MemoryStorage.FakeIPMetadata` returns nil, so on the default in-memory
		// path there is nothing here to read and the ledger records only what this process issues.
		if metadata != nil {
			s.recordRetired(metadata)
		}
		// The RETIREMENT RECORD is written here too, and it is deliberately separate from the ledger
		// above.
		//
		// The ledger is one per application session and dies with the process, so it can only attribute
		// an address THIS process issued or saw retired. The reset below then deletes the cursor
		// metadata - the only other on-disk statement of the retired range - so after a restart nothing
		// at all remembers it, and an address the previous process issued is dialled as an ordinary
		// literal. MEASURED on the reported machine: four such addresses (198.18.0.6/.8/.9/.20), each
		// burning a five-second connect timeout, 94 log lines in total.
		s.recordRetiredRanges(storage, metadata)
		if s.inet4Range.IsValid() {
			s.inet4Current = s.inet4Range.Addr().Next()
			// Resume past what THIS PROCESS already handed out, when there is no durable cursor to
			// restore (no `store_fakeip`, i.e. MemoryStorage). See resumeAfterLedger.
			if resumed, ok := s.resumeAfterLedger(s.inet4Range); ok {
				s.inet4Current = resumed
			}
		}
		if s.inet6Range.IsValid() {
			s.inet6Current = s.inet6Range.Addr().Next()
			if resumed, ok := s.resumeAfterLedger(s.inet6Range); ok {
				s.inet6Current = resumed
			}
		}
		_ = storage.FakeIPReset()
	}
	s.storage = storage
	// Seed this generation at the point it starts from, which is either what the metadata restored or
	// the beginning of the configured range. The cursor can lead the true high-water mark by the
	// reservation window `Create` persists; that over-approximation is bounded and documented on
	// IssuanceLedger.RecordSeed.
	if s.ledger != nil {
		s.ledger.RecordSeed(s.generationSequence, generationLive, s.inet4Range, s.inet4Current, s.inet6Range, s.inet6Current)
	}
	return nil
}

// recordRetired records the generation whose metadata is about to be reset.
//
// The retired generation is recorded at the tick BEFORE the one this store advanced to, and under the
// retired generation index, so it cannot be overwritten by the generation replacing it: both
// identifiers take part in the record's identity, and `Store.Start` advances the tick exactly once per
// generation.
func (s *Store) recordRetired(metadata *adapter.FakeIPMetadata) {
	if s.ledger == nil {
		return
	}
	retiredSequence := s.generationSequence
	if retiredSequence > 0 {
		retiredSequence--
	}
	s.ledger.RecordSeed(retiredSequence, generationRetired, metadata.Inet4Range, metadata.Inet4Current,
		metadata.Inet6Range, metadata.Inet6Current)
}

func (s *Store) Contains(address netip.Addr) bool {
	return s.inet4Range.Contains(address) || s.inet6Range.Contains(address)
}

func (s *Store) Close() error {
	if s.storage == nil {
		return nil
	}
	s.addressAccess.Lock()
	metadata := &adapter.FakeIPMetadata{
		Inet4Range:   s.inet4Range,
		Inet6Range:   s.inet6Range,
		Inet4Current: s.inet4Current,
		Inet6Current: s.inet6Current,
	}
	s.addressAccess.Unlock()
	return s.storage.FakeIPSaveMetadata(metadata)
}

// recordRetiredRanges persists the ranges whose generation is ending, so a LATER process can still
// attribute an address a client is holding.
//
// # What it records, and why it is an INTERVAL built from the metadata's own cursor
//
// `adapter.FakeIPIssuanceInterval{range.Addr().Next(), cursor}` is exactly what the retiring generation
// really handed out: addresses are allocated by walking the range in order, so everything from the first
// issuable address up to the persisted cursor was issued and nothing beyond it was. That bound is the
// same one `seedFamily` applies to the ledger, and it is what keeps `198.18.4.200` - on a range whose
// cursor stopped near `198.18.0.2` - crossing as an ordinary literal, which the A7b end-to-end row pins.
//
// Recording the RANGE instead would refuse those addresses, and the cross-process end-to-end row caught
// exactly that on its first run.
//
// # What it deliberately does not record
//
// `s.inet4Range` - the range this Store is ABOUT to be configured with - is not recorded: on a first run
// it has issued nothing, and on a move it is recorded by the NEXT move, from its own metadata. Recording
// it here would be a union for its own sake, and it would make a freshly configured range refusable on
// the strength of an issuance that never happened.
//
// # The case this cannot reach, and why that is the right boundary
//
// If the metadata is nil - no cache file, `store_fakeip` off, or a failed read - nothing on disk names
// the range or the cursor being retired, so the store genuinely cannot know them, and guessing from the
// range it is about to use is the blanket refusal this design exists to avoid. That case is covered
// where it can be: the in-memory ledger records what THIS process issued, and an in-process range move
// is exactly the ledger's case (`TestFIPAddressFromThePreviousGenerationDoesNotEscape`). Across a
// process boundary with no cache file there is no evidence at all.
//
// # Why it must run BEFORE FakeIPReset
//
// The reset deletes `bucketFakeIP`, and `keyMetadata` lives in it. The record lives in its own bucket
// precisely because it is the statement the reset must not destroy.
//
// # Why it takes the storage as an argument
//
// `s.storage` is assigned AFTER this call, because that assignment is the last statement of the branch
// this runs in. Reading the field here returned nil and recorded nothing - which the cross-process
// end-to-end row caught as a plain "the address still crossed", with no hint as to why.
func (s *Store) recordRetiredRanges(storage adapter.FakeIPStorage, metadata *adapter.FakeIPMetadata) {
	if storage == nil || metadata == nil {
		return
	}
	_ = storage.FakeIPRetireInterval(issuedInterval(metadata.Inet4Range, metadata.Inet4Current))
	_ = storage.FakeIPRetireInterval(issuedInterval(metadata.Inet6Range, metadata.Inet6Current))
}

// resumeAfterLedger answers where a NEW generation may start issuing without handing out an address
// this process already used for something else.
//
// # The failure it prevents
//
// With no `experimental.cache_file` (the default), the storage is MemoryStorage, whose metadata is
// nil, so Start has no cursor to restore and begins at the first address of the range. A reload then
// re-issues 198.18.0.2 to whichever domain asks first, while a client may still be holding that
// address for the domain the PREVIOUS generation resolved. `route/route.go` looks the address up and
// finds the new domain, so the client's connection is delivered to the wrong destination - not
// refused, silently wrong, which is worse than the placeholder leak the refusal exists for.
//
// # Why the ledger is the right evidence, and what it cannot see
//
// The ledger is owned by whoever owns the sequence of Boxes, so it survives a reload: it is the only
// in-process statement of what the retiring generation issued. After a process RESTART without
// `store_fakeip` it is empty - there is genuinely no evidence then, and the durable-record path
// (`recordRetiredRanges`) covers the restarts where the cache file exists.
//
// The answer is conservative: the cursor is placed at the highest address any generation recorded in
// THIS range, so the next allocation starts past it. Addresses stay inside the range, and a range that
// is exhausted wraps exactly as it did before.
func (s *Store) resumeAfterLedger(addressRange netip.Prefix) (netip.Addr, bool) {
	if s.ledger == nil || !addressRange.IsValid() {
		return netip.Addr{}, false
	}
	var highest netip.Addr
	for _, interval := range s.ledger.IssuedIntervals() {
		if !interval.To.IsValid() || interval.To.BitLen() != addressRange.Addr().BitLen() {
			continue
		}
		if !addressRange.Contains(interval.To) {
			continue
		}
		if !highest.IsValid() || interval.To.Compare(highest) > 0 {
			highest = interval.To
		}
	}
	return highest, highest.IsValid()
}

// issuedInterval is the range of addresses a generation really handed out, or an empty interval when
// the range or the cursor cannot describe one.
//
// It mirrors `seedFamily` in adapter/fakeip_issuance.go, deliberately: the ledger and the persisted
// record answer the same question about the same generation, and a second derivation of "what was
// issued" is how the two would come to disagree.
func issuedInterval(addressRange netip.Prefix, cursor netip.Addr) adapter.FakeIPIssuanceInterval {
	if !addressRange.IsValid() || !cursor.IsValid() {
		return adapter.FakeIPIssuanceInterval{}
	}
	if cursor.BitLen() != addressRange.Addr().BitLen() || !addressRange.Contains(cursor) {
		return adapter.FakeIPIssuanceInterval{}
	}
	firstIssuable := addressRange.Addr().Next()
	if !firstIssuable.IsValid() || cursor.Compare(firstIssuable) < 0 {
		return adapter.FakeIPIssuanceInterval{}
	}
	return adapter.FakeIPIssuanceInterval{From: firstIssuable, To: cursor}
}

// RetiredAddresses reports the intervals an earlier generation of this installation issued from.
//
// Two sources with different lifetimes, and both are needed:
//
//	the storage's record   survives a restart, an in-process reload and - with `store_fakeip` - a
//	                       reboot; it answers "this INSTALLATION once issued from here".
//	the in-memory ledger   survives only the process, but it knows intervals the storage never sees
//	                       when there is no cache file at all.
//
// Both are already cursor-bounded, so they are reported as they are: this method does not widen
// anything, and the caller uses them as the same kind of evidence.
func (s *Store) RetiredAddresses() []adapter.FakeIPIssuanceInterval {
	s.addressAccess.Lock()
	defer s.addressAccess.Unlock()
	var retired []adapter.FakeIPIssuanceInterval
	if s.storage != nil {
		retired = append(retired, s.storage.FakeIPRetired()...)
	}
	if s.ledger != nil {
		retired = append(retired, s.ledger.IssuedIntervals()...)
	}
	return adapter.FakeIPNormalizeRetiredIntervals(retired)
}

func (s *Store) Create(domain string, isIPv6 bool) (netip.Addr, error) {
	if address, loaded := s.storage.FakeIPLoadDomain(domain, isIPv6); loaded {
		return address, nil
	}

	s.addressAccess.Lock()
	defer s.addressAccess.Unlock()

	// Double-check after acquiring lock
	if address, loaded := s.storage.FakeIPLoadDomain(domain, isIPv6); loaded {
		return address, nil
	}

	if !isIPv6 && !s.inet4Current.IsValid() {
		return netip.Addr{}, E.New("missing IPv4 fakeip address range")
	} else if isIPv6 && !s.inet6Current.IsValid() {
		return netip.Addr{}, E.New("missing IPv6 fakeip address range")
	}
	// Advance and persist the cursor a whole window ahead of the addresses this
	// store is about to hand out, and then leave the metadata alone until the
	// window is exhausted. The allocation path therefore never depends on a
	// metadata write that could still be buffered when the process dies: whatever
	// point it dies at, the cursor on disk is already past every address issued in
	// this window, so a restart resumes past them instead of re-issuing addresses
	// that are still live.
	if s.reservedCount == 0 {
		metadata := &adapter.FakeIPMetadata{
			Inet4Range:   s.inet4Range,
			Inet6Range:   s.inet6Range,
			Inet4Current: s.inet4Current,
			Inet6Current: s.inet6Current,
		}
		for range reservedAddressCount {
			if metadata.Inet4Current.IsValid() {
				metadata.Inet4Current = nextAddress(s.inet4Range, s.inet4Last, metadata.Inet4Current)
			}
			if metadata.Inet6Current.IsValid() {
				metadata.Inet6Current = nextAddress(s.inet6Range, s.inet6Last, metadata.Inet6Current)
			}
		}
		err := s.storage.FakeIPSaveMetadata(metadata)
		if err != nil {
			return netip.Addr{}, E.Cause(err, "save fakeip metadata")
		}
		s.reservedCount = reservedAddressCount
	}
	s.reservedCount--
	var address netip.Addr
	if !isIPv6 {
		s.inet4Current = nextAddress(s.inet4Range, s.inet4Last, s.inet4Current)
		address = s.inet4Current
	} else {
		s.inet6Current = nextAddress(s.inet6Range, s.inet6Last, s.inet6Current)
		address = s.inet6Current
	}
	// An issued address MUST be inside the configured range, and must not be the range's network or
	// broadcast address: `Contains` is what route/route.go uses to decide "this is a placeholder" and
	// to restore its domain, so an address outside it is handed on as a LITERAL destination and
	// dialled for real.
	//
	// It is reachable with a degenerate prefix. `nextAddress` wraps to `range.Addr().Next().Next()`,
	// which assumes at least two addresses above the network address: for a /31 or /32 (or a /127 or
	// /128) the wrap target itself is outside the range, so every allocation would return it. A
	// caller that configures such a range gets an error on the first query instead of a stream of
	// synthetic addresses that the router will treat as real ones.
	if !s.issuableAddress(address, isIPv6) {
		if isIPv6 {
			return netip.Addr{}, E.New("the IPv6 fakeip range ", s.inet6Range,
				" has no issuable address; it must contain more than one address")
		}
		return netip.Addr{}, E.New("the IPv4 fakeip range ", s.inet4Range,
			" has no issuable address; it must contain more than one address")
	}
	s.storage.FakeIPStoreAsync(address, domain, s.logger)
	if s.ledger != nil {
		// The exact record: this is one address the store really handed out, so the ledger narrows
		// whatever the persisted cursor over-approximated at Start. The address IS the advanced cursor -
		// `nextAddress` returns the address it moved TO, not the one it moved from.
		s.ledger.RecordIssued(s.generationSequence, generationLive, address)
	}
	return address, nil
}

func (s *Store) Lookup(address netip.Addr) (string, bool) {
	return s.storage.FakeIPLoad(address)
}

// issuableAddress reports whether an allocated address may be handed to a client at all.
//
// Two things are required, and both are about the address being USABLE as a placeholder rather than
// merely present in the arithmetic: it must be inside its configured range (route/route.go's
// `Contains` is the only thing that makes the address recognizable as one of ours), and it must be a
// host address rather than the range's network or broadcast address.
func (s *Store) issuableAddress(address netip.Addr, isIPv6 bool) bool {
	addressRange := s.inet4Range
	last := s.inet4Last
	if isIPv6 {
		addressRange = s.inet6Range
		last = s.inet6Last
	}
	if !addressRange.IsValid() || !address.IsValid() {
		return false
	}
	if !addressRange.Contains(address) {
		return false
	}
	if address == addressRange.Addr() || address == last {
		return false
	}
	return true
}

func (s *Store) Reset() error {
	return s.storage.FakeIPReset()
}
