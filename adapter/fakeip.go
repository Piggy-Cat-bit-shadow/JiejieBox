package adapter

import (
	"net/netip"

	"github.com/sagernet/sing/common/logger"
)

type FakeIPStore interface {
	SimpleLifecycle
	Contains(address netip.Addr) bool
	Create(domain string, isIPv6 bool) (netip.Addr, error)
	Lookup(address netip.Addr) (string, bool)
	// RetiredAddresses reports the address INTERVALS an earlier generation of this installation issued
	// from and that this one has retired.
	//
	// # Why it is on the store rather than only on the issuance ledger
	//
	// The ledger is one per application session and dies with the process, so it can only attribute an
	// address the CURRENT process issued or saw retired. A client holds a FakeIP answer for
	// `constant.DefaultDNSTTL`, which spans a daemon restart, an update or a reboot - and after one of
	// those the ledger is empty while the client is still dialling what it was given. These intervals
	// are persisted, and this is the accessor for them.
	//
	// # Why an INTERVAL and not a range
	//
	// A range is not proof, and refusing on one is the blanket range guard this design forbids. A
	// generation hands addresses out in order, so everything from the first issuable address up to its
	// cursor was really handed out and nothing beyond the cursor was. That cursor-bounded interval is
	// the same shape the in-process ledger records, and it is what keeps an address past everything a
	// generation reached - `198.18.4.200` on a range whose cursor stopped near `198.18.0.2` - crossing
	// as an ordinary literal.
	//
	// A store that cannot answer returns nil, which refuses nothing: the required degradation for an
	// implementation with no retirement memory.
	RetiredAddresses() []FakeIPIssuanceInterval
	Reset() error
}

type FakeIPStorage interface {
	FakeIPMetadata() *FakeIPMetadata
	// FakeIPSaveMetadata persists the address cursor synchronously. Do not add an
	// async variant back: the store reserves a window of addresses ahead of the
	// persisted cursor and rewrites the metadata once per window, so the cursor on
	// disk already leads every address handed out. Buffering that one write per
	// window only makes the reservation pointless: while the write sits in the
	// batch the cursor on disk is at an address that was already issued, and a
	// crash then makes the next start re-issue addresses that clients may still
	// hold mappings for.
	FakeIPSaveMetadata(metadata *FakeIPMetadata) error
	FakeIPStore(address netip.Addr, domain string) error
	FakeIPStoreAsync(address netip.Addr, domain string, logger logger.Logger)
	FakeIPLoad(address netip.Addr) (string, bool)
	FakeIPLoadDomain(domain string, isIPv6 bool) (netip.Addr, bool)
	// FakeIPRetireInterval records an interval this installation issued from and is now retiring, so a
	// LATER process can still attribute an address a client is holding.
	//
	// It is called on the Start path and must be durable BEFORE the reset that follows it, because the
	// reset deletes the only other on-disk statement of that generation (the cursor metadata).
	FakeIPRetireInterval(interval FakeIPIssuanceInterval) error
	FakeIPRetired() []FakeIPIssuanceInterval
	FakeIPReset() error
}

type FakeIPTransport interface {
	DNSTransport
	Store() FakeIPStore
}

// FakeIPMaxRetiredRanges bounds how many retired FakeIP generations are remembered.
//
// # Why a bound is required rather than a growth path
//
// A retirement record is written once per range change, for the life of an installation. A daemon that
// reloads on a schedule would otherwise accumulate one entry per change forever, and nothing ever
// removes them.
//
// # Why it is not 1
//
// A single entry only covers the most recent move, and the address a client is holding may be from ANY
// generation it resolved under within `constant.DefaultDNSTTL`. Ten minutes is easily several
// configuration changes, and the case this exists for is exactly that: the client held an address two
// generations back while the configuration had already moved on twice.
//
// Eight is chosen against the TTL rather than the address space. A placeholder is only in a client's
// hands for the length of one DNS TTL, so the generations that can be live at once are bounded by how
// many times a range can move in ten minutes - and eight such moves, each needing a human or a
// scheduled reload, is beyond any real reload cadence while staying a few hundred bytes of disk.
const FakeIPMaxRetiredRanges = 8

// FakeIPNormalizeRetiredIntervals drops entries that cannot describe an interval a store could have
// issued from, and applies the bound.
//
// It is shared by every storage implementation because a malformed entry must be unable to widen the
// refusal. Each rule has a reason: an interval whose ends are inverted contains nothing, and a reader
// that trusted it would refuse a range's worth of addresses the generation never reached; and ends of
// different bit lengths cannot be compared at all, because a 4-in-6 spelling is a different value from
// the IPv4 address it denotes and `Compare` across the two silently misses.
func FakeIPNormalizeRetiredIntervals(intervals []FakeIPIssuanceInterval) []FakeIPIssuanceInterval {
	if len(intervals) == 0 {
		return nil
	}
	normalized := make([]FakeIPIssuanceInterval, 0, len(intervals))
	for _, interval := range intervals {
		if !interval.From.IsValid() || !interval.To.IsValid() {
			continue
		}
		if interval.From.BitLen() != interval.To.BitLen() {
			continue
		}
		if interval.From.Compare(interval.To) > 0 {
			continue
		}
		normalized = append(normalized, interval)
	}
	if len(normalized) > FakeIPMaxRetiredRanges {
		normalized = normalized[len(normalized)-FakeIPMaxRetiredRanges:]
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// FakeIPRetiredCovers reports whether any retired interval covers the address, returning the interval
// so a refusal can name it.
//
// It exists so the question "did this installation issue from here" is asked the same way by every
// caller, including the bit-length agreement that `FakeIPIssuanceInterval.Covers` documents.
func FakeIPRetiredCovers(intervals []FakeIPIssuanceInterval, address netip.Addr) (FakeIPIssuanceInterval, bool) {
	if !address.IsValid() {
		return FakeIPIssuanceInterval{}, false
	}
	for _, interval := range intervals {
		if interval.Covers(address) {
			return interval, true
		}
	}
	return FakeIPIssuanceInterval{}, false
}
