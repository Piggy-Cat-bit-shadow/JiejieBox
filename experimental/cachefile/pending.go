package cachefile

import (
	"context"
	"encoding/binary"
	"net/netip"
	"time"

	"github.com/sagernet/bbolt"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/buf"
)

const defaultBufferSize = 1024 * 1024

type pendingWrites struct {
	dnsCache       map[saveCacheKey]saveDNSCacheEntry
	rdrc           map[saveCacheKey]struct{}
	fakeIPDomain   map[netip.Addr]string
	fakeIPAddress4 map[string]netip.Addr
	fakeIPAddress6 map[string]netip.Addr
	fakeIPMetadata *adapter.FakeIPMetadata
	count          int
	size           int
}

func newPendingWrites() *pendingWrites {
	return &pendingWrites{
		dnsCache:       make(map[saveCacheKey]saveDNSCacheEntry),
		rdrc:           make(map[saveCacheKey]struct{}),
		fakeIPDomain:   make(map[netip.Addr]string),
		fakeIPAddress4: make(map[string]netip.Addr),
		fakeIPAddress6: make(map[string]netip.Addr),
	}
}

func (c *CacheFile) enqueueLocked(added bool, sizeDelta int) {
	if added {
		c.pending.count++
		if c.pending.count == 1 && c.flushInterval > 0 {
			c.flushTimer.Reset(c.flushInterval)
		}
	}
	c.pending.size += sizeDelta
	if c.pending.size >= c.bufferSize {
		select {
		case c.flushSignal <- struct{}{}:
		default:
		}
	}
}

func (c *CacheFile) loopFlush(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.flushTimer.C:
		case <-c.flushSignal:
		}
		c.Flush()
	}
}

// Flush writes the pending batch and reports whether it reached the disk.
//
// The error is the whole point of the split below. `Flush` is part of `adapter.CacheFile`, so its
// signature cannot grow a return value without a cross-package change; but a caller that has
// PROMISED durability needs to know whether the write happened, and silently dropping the batch is
// not the same as writing it.
func (c *CacheFile) flush() error {
	c.flushAccess.Lock()
	defer c.flushAccess.Unlock()
	c.pendingAccess.Lock()
	if c.pending.count == 0 {
		c.pendingAccess.Unlock()
		return nil
	}
	writing := c.pending
	c.writing = writing
	c.pending = newPendingWrites()
	c.pendingAccess.Unlock()
	err := c.update(func(tx *bbolt.Tx) error {
		return c.writePending(tx, writing)
	})
	if err != nil {
		// The batch is gone: `pending` was replaced above and `writing` is dropped here, so a
		// failed flush LOSES the staged writes rather than retrying them. That is why the error
		// has to reach the caller instead of only the log - see
		// `CacheFile.FakeIPSaveMetadata`, whose caller re-derives the batch it needs.
		c.logger.Warn("save cache: ", err)
	}
	c.pendingAccess.Lock()
	c.writing = nil
	c.pendingAccess.Unlock()
	return err
}

// Flush writes the pending batch, reporting nothing. It is the best-effort entry point for the
// background flusher and for teardown, where there is no caller left to fail.
func (c *CacheFile) Flush() {
	_ = c.flush()
}

func (c *CacheFile) writePending(tx *bbolt.Tx, pending *pendingWrites) error {
	if len(pending.dnsCache) > 0 {
		bucket, err := c.createBucket(tx, bucketDNSCache)
		if err != nil {
			return err
		}
		for key, entry := range pending.dnsCache {
			err = putCacheEntry(bucket, key, entry.value)
			if err != nil {
				return err
			}
		}
	}
	if len(pending.rdrc) > 0 {
		bucket, err := c.createBucket(tx, bucketRDRC)
		if err != nil {
			return err
		}
		expiresAt := make([]byte, 8)
		binary.BigEndian.PutUint64(expiresAt, uint64(time.Now().Add(c.rdrcTimeout).Unix()))
		for key := range pending.rdrc {
			err = putCacheEntry(bucket, key, expiresAt)
			if err != nil {
				return err
			}
		}
	}
	for address, domain := range pending.fakeIPDomain {
		err := putFakeIP(tx, address, domain)
		if err != nil {
			return err
		}
	}
	if pending.fakeIPMetadata != nil {
		err := putFakeIPMetadata(tx, pending.fakeIPMetadata)
		if err != nil {
			return err
		}
	}
	return nil
}

func putCacheEntry(bucket *bbolt.Bucket, key saveCacheKey, value []byte) error {
	transportBucket, err := bucket.CreateBucketIfNotExists([]byte(key.TransportName))
	if err != nil {
		return err
	}
	keyBytes := buf.Get(2 + len(key.QuestionName))
	defer buf.Put(keyBytes)
	binary.BigEndian.PutUint16(keyBytes, key.QType)
	copy(keyBytes[2:], key.QuestionName)
	return transportBucket.Put(keyBytes, value)
}

func getCacheEntry(bucket *bbolt.Bucket, key saveCacheKey) []byte {
	transportBucket := bucket.Bucket([]byte(key.TransportName))
	if transportBucket == nil {
		return nil
	}
	keyBytes := buf.Get(2 + len(key.QuestionName))
	defer buf.Put(keyBytes)
	binary.BigEndian.PutUint16(keyBytes, key.QType)
	copy(keyBytes[2:], key.QuestionName)
	return transportBucket.Get(keyBytes)
}
