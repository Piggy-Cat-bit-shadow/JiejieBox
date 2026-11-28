package dns

import (
	"sync/atomic"

	mDNS "github.com/miekg/dns"
)

// DNS cache byte budget.
//
// # The gap this closes
//
// Both in-memory caches here were bounded by ENTRY COUNT only: the exact cache by
// cache_capacity (1024 by default) and the name-level negative cache by
// nxdomainCacheCapacity (2048, clipped to the configured capacity). That bounds the number
// of retained responses but not their size, and DNS responses are not uniform: a measured
// A answer retains ~370 B while a TXT set or a signed answer retains 0.6-1.7 kB, so the
// same 1024 entries can hold ~0.4 MiB or ~1.7 MiB depending only on what is being resolved.
// The count limit is kept - it is the admission control for the number of DISTINCT names -
// and this budget is added on top as the bound on bytes.
//
// # Why the charge is not a second cache authority
//
// The budget never decides what to evict or when; it only refuses an admission that would
// exceed it. Eviction stays freelru's job, which is why nothing here walks the cache or
// keeps a second key index. The three rules that make the accounting exact are:
//
//   - reserve BEFORE the copy: storeCache charges the estimated retained size of the
//     response it is about to copy, and does not copy at all when the charge is refused.
//     A refused response is simply not cached; the entries already cached are untouched.
//   - one charge, one release: the charge is returned by exactly one path - the freelru
//     onEvict callback, which runs for eviction, capacity replacement, Remove, Purge and
//     PurgeExpired. freelru does NOT report a same-key overwrite to onEvict, so the store
//     path removes the previous entry explicitly before adding (see storeCache).
//   - the charge is a function of the stored value only: estimateDNSResponseBytes is
//     deterministic, the cached *dns.Msg is immutable after the store (every read path
//     copies before rewriting TTLs), so the size released is the size charged.
//
// Shared backing is deliberately counted once: the cached copy shares its name strings
// with the response it was copied from, and that response is transient after the exchange,
// so those bytes are retained BY the entry and belong to its charge. Two entries never
// share a backing array, and the per-hit copies the read path makes are transient and are
// not charged at all.
type cacheByteBudget struct {
	limit int64
	used  atomic.Int64
	peak  atomic.Int64
	// refused counts admissions the budget rejected, and excessReleases counts releases
	// that found less charged than they were asked to return. Both must stay at zero
	// except for `refused`, which is the budget doing its job; a non-zero
	// excessReleases is an accounting defect, not a policy outcome.
	refused        atomic.Uint64
	excessReleases atomic.Uint64
}

func newCacheByteBudget(limit int) *cacheByteBudget {
	if limit < 0 {
		limit = 0
	}
	return &cacheByteBudget{limit: int64(limit)}
}

// Reserve charges size bytes, reporting whether the budget allows them. It never leaves
// the budget over its limit, and it never blocks.
func (b *cacheByteBudget) Reserve(size int) bool {
	if b == nil {
		return true
	}
	if size <= 0 {
		return true
	}
	charge := int64(size)
	for {
		used := b.used.Load()
		if used+charge > b.limit {
			b.refused.Add(1)
			return false
		}
		if b.used.CompareAndSwap(used, used+charge) {
			for {
				peak := b.peak.Load()
				if used+charge <= peak || b.peak.CompareAndSwap(peak, used+charge) {
					break
				}
			}
			return true
		}
	}
}

// Release returns size bytes to the budget. A release beyond what is charged is clamped
// and counted: it can only mean a release path is double-counting, and letting the total
// go negative would hand the cache an unbounded allowance.
func (b *cacheByteBudget) Release(size int) {
	if b == nil || size <= 0 {
		return
	}
	charge := int64(size)
	for {
		used := b.used.Load()
		if used < charge {
			b.excessReleases.Add(1)
			if used <= 0 {
				return
			}
			charge = used
		}
		if b.used.CompareAndSwap(used, used-charge) {
			return
		}
	}
}

// settle forces the ledger to zero after a full purge. A purge returns every charge
// through onEvict, so a non-zero total here is a defect; converging instead of holding
// the drift is what keeps one accounting bug from permanently shrinking the cache.
func (b *cacheByteBudget) settle() {
	if b == nil {
		return
	}
	if used := b.used.Swap(0); used != 0 {
		b.excessReleases.Add(1)
	}
}

func (b *cacheByteBudget) Used() int64 {
	if b == nil {
		return 0
	}
	return b.used.Load()
}

func (b *cacheByteBudget) Peak() int64 {
	if b == nil {
		return 0
	}
	return b.peak.Load()
}

func (b *cacheByteBudget) Limit() int64 {
	if b == nil {
		return 0
	}
	return b.limit
}

func (b *cacheByteBudget) Refused() uint64 {
	if b == nil {
		return 0
	}
	return b.refused.Load()
}

func (b *cacheByteBudget) ExcessReleases() uint64 {
	if b == nil {
		return 0
	}
	return b.excessReleases.Load()
}

// Cache budget sizing. The per-entry allowance is a POLICY value derived from the measured
// retained sizes of the response shapes this client actually caches:
//
//	A answer from a real query            370 B measured
//	AAAA, two answers                     488 B
//	HTTPS/SVCB with hints                 648 B
//	signed A (A + RRSIG)                  632 B
//	CNAME chain (three records)           594 B
//	TXT set, eight 60-byte strings        1674 B
//
// The allowance is set above the largest of those, so a normal browsing mix never reaches
// the budget: 1024 entries of the measured browsing mix retain 0.56 MiB against a 2 MiB
// budget for the default capacity. It binds on a response population whose average
// retained size exceeds 2 kB - DNSSEC-heavy or TXT-heavy traffic - which is exactly the
// tail the count limit cannot bound.
const (
	cacheBytesPerEntry  = 2048
	minimumCacheBytes   = 1 << 20
	maximumCacheBytes   = 8 << 20
	defaultCacheEntries = 1024
)

// cacheByteLimitForCapacity derives the budget for an entry capacity. The floor keeps a
// deliberately tiny cache_capacity from turning the budget into a single-entry cache; the
// ceiling keeps a deliberately large one from turning it into an unbounded one.
func cacheByteLimitForCapacity(cacheCapacity uint32) int {
	limit := int(cacheCapacity) * cacheBytesPerEntry
	if limit < minimumCacheBytes {
		limit = minimumCacheBytes
	}
	if limit > maximumCacheBytes {
		limit = maximumCacheBytes
	}
	return limit
}

// estimateDNSResponseBytes estimates the heap bytes a cached response retains: the
// message header, the question and record slices, and everything the records own.
//
// The wire length is the base because every name, address and payload byte in the message
// is also present in its wire form, and Msg.Len performs a pure counting walk (no
// allocation, no mutation). What the wire form does not contain is the parsed
// representation's own overhead - one allocation per record plus the slice and string
// headers behind it - so that is added per record, with the types whose payload is carried
// in Go slices or strings named explicitly.
//
// The estimate is a BUDGET, not a measurement: it is deliberately close to the measured
// retained size (0.96-1.10 of it for every shape in the table above except HTTPS/SVCB,
// where the key-value slice is approximated at 0.69) and it never depends on anything but
// the message contents.
func estimateDNSResponseBytes(message *mDNS.Msg) int {
	if message == nil {
		return 0
	}
	records := len(message.Question) + len(message.Answer) + len(message.Ns) + len(message.Extra)
	if records == 0 {
		return 0
	}
	// 128: the Msg value itself (header plus the three record slice headers and the
	// question slice header).
	// 16: the parsed Question value (string header plus type and class).
	// 64: the interface slot, RR header and per-record slice headers of one record.
	estimate := 128 + message.Len() + 16*len(message.Question) + 64*records
	for _, record := range message.Answer {
		estimate += parsedRecordExtra(record)
	}
	for _, record := range message.Ns {
		estimate += parsedRecordExtra(record)
	}
	for _, record := range message.Extra {
		estimate += parsedRecordExtra(record)
	}
	return estimate
}

// parsedRecordExtra is the parsed representation's extra cost for one record, on top of
// the wire payload and the fixed per-record overhead: the Go slice and string headers the
// record's value owns. Payload bytes are NOT added again - Msg.Len already counts them -
// only the header each value needs to own them.
func parsedRecordExtra(record mDNS.RR) int {
	switch value := record.(type) {
	case *mDNS.A:
		return 24
	case *mDNS.AAAA:
		return 24
	case *mDNS.NS:
		return 16
	case *mDNS.CNAME:
		return 16
	case *mDNS.PTR:
		return 16
	case *mDNS.DNAME:
		return 16
	case *mDNS.TXT:
		return 24 + 16*len(value.Txt)
	case *mDNS.SPF:
		return 24 + 16*len(value.Txt)
	case *mDNS.RESINFO:
		return 24 + 16*len(value.Txt)
	case *mDNS.SOA:
		return 32
	case *mDNS.SVCB:
		return 24 + 16 + 48*len(value.Value)
	case *mDNS.HTTPS:
		return 24 + 16 + 48*len(value.Value)
	case *mDNS.RRSIG:
		return 32
	case *mDNS.NSEC:
		return 16 + 24
	case *mDNS.OPT:
		return 24 + 16*len(value.Option)
	default:
		// Unmodelled types still have a header and at least one owned value in
		// practice; charge a conservative constant rather than nothing.
		return 64
	}
}
