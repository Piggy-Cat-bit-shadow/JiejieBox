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
	// RetiredAddresses reports the address RANGES an earlier generation of this installation issued
	// from and that this one has retired.
	//
	// # Why it is on the store rather than only on the issuance ledger
	//
	// The ledger is one per application session and dies with the process, so it can only attribute an
	// address the CURRENT process issued or saw retired. A client holds a FakeIP answer for
	// `constant.DefaultDNSTTL`, which spans a daemon restart, an update or a reboot - and after one of
	// those the ledger is empty while the client is still dialling what it was given. The ranges are
	// therefore persisted, and this is the accessor for them.
	//
	// # Why a RANGE and not the address
	//
	// A retired generation is described by its configured range, and the ledger's job of narrowing that
	// to a cursor is what keeps an address the generation never reached from being refused. This method
	// answers the coarser question ("did we issue from a range that covers this?"), and the caller
	// treats a positive answer as a MISSING RECORD, not as proof that the address was issued.
	//
	// A store that cannot answer returns nil, which refuses nothing: the required degradation for an
	// implementation with no retirement memory.
	RetiredAddresses() []netip.Prefix
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
	// FakeIPRetireRange records a range this installation issued from and is now retiring, so a LATER
	// process can still attribute an address a client is holding.
	//
	// It is called on the Start path and must be durable BEFORE the reset that follows it, because the
	// reset deletes the only other on-disk statement of that range (the cursor metadata).
	FakeIPRetireRange(addressRange netip.Prefix) error
	FakeIPRetired() []netip.Prefix
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

// FakeIPNormalizeRetiredRanges drops entries that cannot describe a range a store could have issued
// from, and masks host bits so a loosely spelled range still matches.
//
// It is shared by every storage implementation because a malformed entry must be unable to widen the
// refusal: a `/0` read back from a hand-edited file would otherwise claim the whole address space,
// which is precisely the blanket range guard this design exists to avoid. A `/32` or `/128` is dropped
// for the opposite reason - it cannot be a FakeIP range, so treating it as one would refuse a single
// real address for no evidence at all.
func FakeIPNormalizeRetiredRanges(ranges []netip.Prefix) []netip.Prefix {
	if len(ranges) == 0 {
		return nil
	}
	normalized := make([]netip.Prefix, 0, len(ranges))
	for _, addressRange := range ranges {
		if !addressRange.IsValid() {
			continue
		}
		bits := addressRange.Bits()
		if bits <= 0 || bits >= addressRange.Addr().BitLen() {
			continue
		}
		normalized = append(normalized, addressRange.Masked())
	}
	if len(normalized) > FakeIPMaxRetiredRanges {
		normalized = normalized[len(normalized)-FakeIPMaxRetiredRanges:]
	}
	return normalized
}
