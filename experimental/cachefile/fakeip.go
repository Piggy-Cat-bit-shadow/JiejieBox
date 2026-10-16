package cachefile

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"os"

	"github.com/sagernet/bbolt"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
)

const fakeipBucketPrefix = "fakeip_"

var (
	bucketFakeIP        = []byte(fakeipBucketPrefix + "address")
	bucketFakeIPDomain4 = []byte(fakeipBucketPrefix + "domain4")
	bucketFakeIPDomain6 = []byte(fakeipBucketPrefix + "domain6")
	keyMetadata         = []byte(fakeipBucketPrefix + "metadata")

	// bucketFakeIPRetired holds the INTERVALS this installation has issued from and retired. It is
	// deliberately NOT under bucketFakeIP, because FakeIPReset deletes that bucket wholesale - and
	// this record is exactly the statement the reset must NOT destroy.
	bucketFakeIPRetired = []byte(fakeipBucketPrefix + "retired")
	keyRetiredRanges    = []byte("ranges")
)

// maxRetiredRanges bounds how many retired generations are remembered. The bound and its
// justification live on `adapter.FakeIPMaxRetiredRanges`, because the in-memory storage keeps the
// same list and the two must not drift; this is the local alias for readability.
const maxRetiredRanges = adapter.FakeIPMaxRetiredRanges

const (
	retiredFamilyIPv4 byte = 0
	retiredFamilyIPv6 byte = 1
)

// FakeIPRetireRange records a range this installation issued from, so that a LATER process can still
// attribute an address a client is holding.
//
// # What this closes
//
// The only other durable statement of a retired range is the cursor metadata, and the same
// `Store.Start` that reads it destroys it:
//
//	metadata := storage.FakeIPMetadata()
//	... s.recordRetired(metadata) ...      // in-memory ledger only
//	_ = storage.FakeIPReset()              // deletes bucketFakeIP, keyMetadata included
//
// So after a restart the ledger is empty, the disk names only the new range, and an address the
// PREVIOUS process issued is indistinguishable from an ordinary literal: it has no mapping
// (`FakeIPLoad` misses) and no range record, so `route.prepareMatchMetadata` forwards it to the OS.
// MEASURED on the reported machine: four synthetic addresses (198.18.0.6/.8/.9/.20) were dialled for
// real and each burned a five-second connect timeout, 94 log lines in total.
//
// # What it does NOT do
//
// It records an INTERVAL, not a range, and the difference is the whole reason this is safe. A
// generation hands addresses out in order, so `{first issuable, cursor}` is exactly what it really
// handed out; refusing on the RANGE instead would refuse `198.18.4.200` on a range whose cursor stopped
// near `198.18.0.2`, which is the compatibility regression the ledger's own cursor bound exists to
// prevent and which the A7b end-to-end row pins. Entries are validated by
// `adapter.FakeIPNormalizeRetiredIntervals` on the way out.
func (c *CacheFile) FakeIPRetireInterval(interval adapter.FakeIPIssuanceInterval) error {
	if !interval.From.IsValid() || !interval.To.IsValid() || interval.From.Compare(interval.To) > 0 {
		return nil
	}
	return c.batch(func(tx *bbolt.Tx) error {
		// The list is read back from disk inside the write, so two Stores sharing one cache file
		// cannot each write a one-entry list over the other's.
		var stored []adapter.FakeIPIssuanceInterval
		if existing := tx.Bucket(bucketFakeIPRetired); existing != nil {
			stored = unmarshalRetiredIntervals(existing.Get(keyRetiredRanges))
		}
		for _, retired := range stored {
			if retired == interval {
				return nil
			}
		}
		stored = append(stored, interval)
		if len(stored) > maxRetiredRanges {
			stored = stored[len(stored)-maxRetiredRanges:]
		}
		bucket, err := tx.CreateBucketIfNotExists(bucketFakeIPRetired)
		if err != nil {
			return err
		}
		return bucket.Put(keyRetiredRanges, marshalRetiredIntervals(stored))
	})
}

// FakeIPRetired returns the intervals this installation has issued from and retired, oldest first.
//
// A read error is reported as "nothing retired", which refuses nothing - the same degradation as a
// storage implementation with no retirement memory at all.
func (c *CacheFile) FakeIPRetired() []adapter.FakeIPIssuanceInterval {
	var stored []adapter.FakeIPIssuanceInterval
	_ = c.view(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketFakeIPRetired)
		if bucket == nil {
			return nil
		}
		stored = unmarshalRetiredIntervals(bucket.Get(keyRetiredRanges))
		return nil
	})
	return stored
}

// FakeIPMetadata reads the persisted address cursor without consuming it. This runs
// on the FakeIP store's Start path, so it must stay a read-only view: the previous
// implementation used a write batch that deleted the key, which took the single
// write lock during startup and destroyed the only persisted copy of the cursor.
// The delete is committed before the call returns, so afterwards the cursor exists
// only in the caller's memory until the store saves it again (the next reserved
// window, or Close). A process that died in between left no metadata at all, and
// the next Start then takes the metadata == nil branch, resets the whole FakeIP
// bucket, and starts re-issuing from the beginning of the range — wiping every
// cached domain mapping and handing addresses to different domains while clients
// may still resolve the old ones.
func (c *CacheFile) FakeIPMetadata() *adapter.FakeIPMetadata {
	var metadata adapter.FakeIPMetadata
	err := c.view(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketFakeIP)
		if bucket == nil {
			return os.ErrNotExist
		}
		metadataBinary := bucket.Get(keyMetadata)
		if len(metadataBinary) == 0 {
			return os.ErrInvalid
		}
		return metadata.UnmarshalBinary(metadataBinary)
	})
	if err != nil {
		return nil
	}
	return &metadata
}

// FakeIPSaveMetadata stages the cursor and flushes immediately, REPORTING whether it landed.
//
// # Why the return value is load-bearing, and was not
//
// `adapter.FakeIPStorage` states the contract this implements:
//
//	FakeIPSaveMetadata persists the address cursor synchronously. ... the store reserves a
//	window of addresses ahead of the persisted cursor and rewrites the metadata once per
//	window, so the cursor on disk already leads every address handed out.
//
// and the store acts on it - `dns/transport/fakeip/store.go` refuses to hand out any address of
// the window when this returns an error:
//
//	err := s.storage.FakeIPSaveMetadata(metadata)
//	if err != nil {
//	    return netip.Addr{}, E.Cause(err, "save fakeip metadata")
//	}
//	s.reservedCount = reservedAddressCount
//
// The implementation returned `nil` unconditionally while `Flush` swallowed the write error and
// DROPPED the batch, so that branch was unreachable and the reservation the comment describes was
// not enforced: on a failed write the store still advanced `reservedCount` and handed out
// addresses whose cursor had never reached the disk. A crash then re-issued addresses that clients
// may still hold mappings for - the exact hazard the comment says the synchronous write prevents.
//
// MEASURED before the change: with the database closed underneath it, this function returned nil
// and the staged cursor was lost with only a warning in the log.
//
// Flush takes pendingAccess itself, so the lock is released explicitly before calling it (a defer
// would deadlock). The caller - the FakeIP store - reserves once per window plus once on Close, so
// blocking here is cheap.
func (c *CacheFile) FakeIPSaveMetadata(metadata *adapter.FakeIPMetadata) error {
	c.pendingAccess.Lock()
	added := c.pending.fakeIPMetadata == nil
	c.pending.fakeIPMetadata = metadata
	c.enqueueLocked(added, 0)
	c.pendingAccess.Unlock()
	return c.flush()
}

func putFakeIPMetadata(tx *bbolt.Tx, metadata *adapter.FakeIPMetadata) error {
	bucket, err := tx.CreateBucketIfNotExists(bucketFakeIP)
	if err != nil {
		return err
	}
	metadataBinary, err := metadata.MarshalBinary()
	if err != nil {
		return err
	}
	return bucket.Put(keyMetadata, metadataBinary)
}

func (c *CacheFile) FakeIPStore(address netip.Addr, domain string) error {
	c.queueFakeIP(address, domain)
	c.Flush()
	return nil
}

func (c *CacheFile) FakeIPStoreAsync(address netip.Addr, domain string, logger logger.Logger) {
	c.queueFakeIP(address, domain)
}

// queueFakeIP records a new address allocation in the pending batch. The replaced
// mapping is looked up through FakeIPLoad, not just through the pending map: the
// address may already be persisted by an earlier batch, in which case the old
// domain still exists in the on-disk reverse bucket and would otherwise be handed
// out (and size-accounted) as if it were still live. The old domain's reverse
// entry is left as an explicit tombstone rather than deleted, because putFakeIP
// may legitimately keep the on-disk entry (the domain may have been re-mapped to
// another address since), and a plain delete would then let the stale on-disk
// address leak back out through FakeIPLoadDomain.
func (c *CacheFile) queueFakeIP(address netip.Addr, domain string) {
	oldDomain, loaded := c.FakeIPLoad(address)
	c.pendingAccess.Lock()
	defer c.pendingAccess.Unlock()
	if loaded {
		if address.Is4() {
			c.pending.fakeIPAddress4[oldDomain] = netip.Addr{}
		} else {
			c.pending.fakeIPAddress6[oldDomain] = netip.Addr{}
		}
	}
	pendingDomain, pendingLoaded := c.pending.fakeIPDomain[address]
	c.pending.fakeIPDomain[address] = domain
	if address.Is4() {
		c.pending.fakeIPAddress4[domain] = address
	} else {
		c.pending.fakeIPAddress6[domain] = address
	}
	c.enqueueLocked(!pendingLoaded, len(domain)-len(pendingDomain))
}

func putFakeIP(tx *bbolt.Tx, address netip.Addr, domain string) error {
	bucket, err := tx.CreateBucketIfNotExists(bucketFakeIP)
	if err != nil {
		return err
	}
	addressBytes := address.AsSlice()
	oldDomain := bucket.Get(addressBytes)
	err = bucket.Put(addressBytes, []byte(domain))
	if err != nil {
		return err
	}
	if address.Is4() {
		bucket, err = tx.CreateBucketIfNotExists(bucketFakeIPDomain4)
	} else {
		bucket, err = tx.CreateBucketIfNotExists(bucketFakeIPDomain6)
	}
	if err != nil {
		return err
	}
	// A forward entry can outlive its reverse entry: the same domain may have been
	// re-mapped to another address (which overwrote the reverse bucket) while this
	// address still points at it in the forward bucket. Deleting the reverse entry
	// here unconditionally would tear down a mapping that is still live for the
	// other address, so only delete it when it still points back at this address.
	if oldDomain != nil && bytes.Equal(bucket.Get(oldDomain), addressBytes) {
		err = bucket.Delete(oldDomain)
		if err != nil {
			return err
		}
	}
	return bucket.Put([]byte(domain), addressBytes)
}

func (c *CacheFile) FakeIPLoad(address netip.Addr) (string, bool) {
	c.pendingAccess.RLock()
	domain, cached := c.pending.fakeIPDomain[address]
	if !cached && c.writing != nil {
		domain, cached = c.writing.fakeIPDomain[address]
	}
	c.pendingAccess.RUnlock()
	if cached {
		return domain, true
	}
	_ = c.view(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(bucketFakeIP)
		if bucket == nil {
			return nil
		}
		domain = string(bucket.Get(address.AsSlice()))
		return nil
	})
	return domain, domain != ""
}

func (p *pendingWrites) fakeIPAddress(domain string, isIPv6 bool) (netip.Addr, bool) {
	if isIPv6 {
		address, loaded := p.fakeIPAddress6[domain]
		return address, loaded
	}
	address, loaded := p.fakeIPAddress4[domain]
	return address, loaded
}

func (c *CacheFile) FakeIPLoadDomain(domain string, isIPv6 bool) (netip.Addr, bool) {
	c.pendingAccess.RLock()
	address, cached := c.pending.fakeIPAddress(domain, isIPv6)
	if !cached && c.writing != nil {
		address, cached = c.writing.fakeIPAddress(domain, isIPv6)
	}
	c.pendingAccess.RUnlock()
	if cached {
		// A pending entry with an invalid address is a tombstone written by
		// queueFakeIP for a domain whose mapping was replaced; report it as not
		// loaded so the caller falls through to a fresh policy evaluation instead
		// of resolving the domain to a stale on-disk address.
		return address, address.IsValid()
	}
	_ = c.view(func(tx *bbolt.Tx) error {
		var bucket *bbolt.Bucket
		if isIPv6 {
			bucket = tx.Bucket(bucketFakeIPDomain6)
		} else {
			bucket = tx.Bucket(bucketFakeIPDomain4)
		}
		if bucket == nil {
			return nil
		}
		address = M.AddrFromIP(bucket.Get([]byte(domain)))
		return nil
	})
	return address, address.IsValid()
}

func (c *CacheFile) FakeIPReset() error {
	c.flushAccess.Lock()
	defer c.flushAccess.Unlock()
	c.pendingAccess.Lock()
	for _, domain := range c.pending.fakeIPDomain {
		c.pending.count--
		c.pending.size -= len(domain)
	}
	clear(c.pending.fakeIPDomain)
	clear(c.pending.fakeIPAddress4)
	clear(c.pending.fakeIPAddress6)
	if c.pending.fakeIPMetadata != nil {
		c.pending.fakeIPMetadata = nil
		c.pending.count--
	}
	c.pendingAccess.Unlock()
	return c.batch(func(tx *bbolt.Tx) error {
		// `bucketFakeIPRetired` is deliberately NOT in this list. A reset destroys the MAPPINGS and the
		// cursor, but the statement that this installation once issued from a range is exactly what has
		// to survive it - otherwise a later process cannot attribute the addresses a client is still
		// holding, which is the defect `FakeIPRetireRange` exists to close.
		for _, bucketName := range [][]byte{bucketFakeIP, bucketFakeIPDomain4, bucketFakeIPDomain6} {
			if tx.Bucket(bucketName) == nil {
				continue
			}
			err := tx.DeleteBucket(bucketName)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// The retirement record's encoding is a count, then one entry per interval, each written as
//
//	family | from | to
//
// with a 2-byte count prefix and fixed-size bodies per family.
//
// # Why the family byte is explicit and comes first
//
// The obvious encoding - addresses alone, family implied by their length - carries an ambiguity that is
// not worth having: a 4-byte body would have to be told apart from a truncated 16-byte one by reading
// the byte AFTER it, which is only valid when that value happens to be in range. An explicit tag removes
// the inference. The family is chosen from `Addr().BitLen()` rather than `Is4()`, because an address
// built from sixteen bytes reports `Is4() == false` even when its bits are 0..32, and the two ends of one
// interval must not disagree about their own length.
//
// It is deliberately FORWARD compatible rather than versioned: a reader stops at the count it was given
// and ignores any trailing bytes, so a file written by a build with a larger bound is read correctly by
// a smaller one, and a truncated value yields the entries it does hold rather than an error. That is the
// opposite of the metadata decoder above, which allocates on a length prefix read from disk and reports a
// truncation - correctly, because a partial CURSOR would re-issue addresses that are still live, while a
// partial RETIREMENT LIST is still a serviceable set of intervals.
func marshalRetiredIntervals(intervals []adapter.FakeIPIssuanceInterval) []byte {
	data := make([]byte, 0, 2+len(intervals)*34)
	data = binary.BigEndian.AppendUint16(data, uint16(len(intervals)))
	for _, interval := range intervals {
		from, to := interval.From, interval.To
		if from.BitLen() == 32 {
			rawFrom := from.As4()
			rawTo := to.As4()
			data = append(data, retiredFamilyIPv4)
			data = append(data, rawFrom[:]...)
			data = append(data, rawTo[:]...)
			continue
		}
		rawFrom := from.As16()
		rawTo := to.As16()
		data = append(data, retiredFamilyIPv6)
		data = append(data, rawFrom[:]...)
		data = append(data, rawTo[:]...)
	}
	return data
}

func unmarshalRetiredIntervals(data []byte) []adapter.FakeIPIssuanceInterval {
	if len(data) < 2 {
		return nil
	}
	count := int(binary.BigEndian.Uint16(data[:2]))
	if count > maxRetiredRanges {
		count = maxRetiredRanges
	}
	offset := 2
	intervals := make([]adapter.FakeIPIssuanceInterval, 0, count)
	for range count {
		if offset >= len(data) {
			break
		}
		family := data[offset]
		offset++
		var length int
		switch family {
		case retiredFamilyIPv4:
			length = 4
		case retiredFamilyIPv6:
			length = 16
		default:
			// An unknown family means this value was not written by this encoder. Stop rather than
			// guess a length and read whatever follows as addresses.
			return adapter.FakeIPNormalizeRetiredIntervals(intervals)
		}
		if offset+length*2 > len(data) {
			break
		}
		var from, to netip.Addr
		if length == 4 {
			var rawFrom, rawTo [4]byte
			copy(rawFrom[:], data[offset:offset+4])
			copy(rawTo[:], data[offset+4:offset+8])
			from = netip.AddrFrom4(rawFrom)
			to = netip.AddrFrom4(rawTo)
		} else {
			var rawFrom, rawTo [16]byte
			copy(rawFrom[:], data[offset:offset+16])
			copy(rawTo[:], data[offset+16:offset+32])
			from = netip.AddrFrom16(rawFrom)
			to = netip.AddrFrom16(rawTo)
		}
		offset += length * 2
		intervals = append(intervals, adapter.FakeIPIssuanceInterval{From: from, To: to})
	}
	return adapter.FakeIPNormalizeRetiredIntervals(intervals)
}
