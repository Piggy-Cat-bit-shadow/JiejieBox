package fakeip

import (
	"net/netip"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/logger"
)

var _ adapter.FakeIPStorage = (*MemoryStorage)(nil)

type MemoryStorage struct {
	addressAccess sync.RWMutex
	domainAccess  sync.RWMutex
	addressCache  map[netip.Addr]string
	domainCache4  map[string]netip.Addr
	domainCache6  map[string]netip.Addr

	// retiredAccess guards retired. It is kept even though this storage is discarded with the process,
	// because a Store built over it is not: an in-process reload moves the range and builds a new Store
	// over the SAME storage object, and that is the move whose evidence has to survive.
	retiredAccess sync.RWMutex
	retired       []netip.Prefix
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		addressCache: make(map[netip.Addr]string),
		domainCache4: make(map[string]netip.Addr),
		domainCache6: make(map[string]netip.Addr),
	}
}

func (s *MemoryStorage) FakeIPMetadata() *adapter.FakeIPMetadata {
	return nil
}

func (s *MemoryStorage) FakeIPSaveMetadata(metadata *adapter.FakeIPMetadata) error {
	return nil
}

func (s *MemoryStorage) FakeIPStore(address netip.Addr, domain string) error {
	s.addressAccess.Lock()
	s.domainAccess.Lock()
	if oldDomain, loaded := s.addressCache[address]; loaded {
		if address.Is4() {
			delete(s.domainCache4, oldDomain)
		} else {
			delete(s.domainCache6, oldDomain)
		}
	}
	s.addressCache[address] = domain
	if address.Is4() {
		s.domainCache4[domain] = address
	} else {
		s.domainCache6[domain] = address
	}
	s.domainAccess.Unlock()
	s.addressAccess.Unlock()
	return nil
}

func (s *MemoryStorage) FakeIPStoreAsync(address netip.Addr, domain string, logger logger.Logger) {
	_ = s.FakeIPStore(address, domain)
}

func (s *MemoryStorage) FakeIPLoad(address netip.Addr) (string, bool) {
	s.addressAccess.RLock()
	defer s.addressAccess.RUnlock()
	domain, loaded := s.addressCache[address]
	return domain, loaded
}

func (s *MemoryStorage) FakeIPLoadDomain(domain string, isIPv6 bool) (netip.Addr, bool) {
	s.domainAccess.RLock()
	defer s.domainAccess.RUnlock()
	if !isIPv6 {
		address, loaded := s.domainCache4[domain]
		return address, loaded
	} else {
		address, loaded := s.domainCache6[domain]
		return address, loaded
	}
}

func (s *MemoryStorage) FakeIPReset() error {
	s.addressAccess.Lock()
	s.domainAccess.Lock()
	s.addressCache = make(map[netip.Addr]string)
	s.domainCache4 = make(map[string]netip.Addr)
	s.domainCache6 = make(map[string]netip.Addr)
	s.domainAccess.Unlock()
	s.addressAccess.Unlock()
	return nil
}

// FakeIPRetireRange records a range for as long as this storage lives.
//
// On this path there is no disk, so the record cannot outlive the process - which is the documented
// boundary of the in-memory configuration, not a gap. It is still recorded, because an in-process
// range move builds a NEW Store over the SAME storage object, and without this the ledger's
// `recordRetired` would be the only holder of evidence that a Store could already be asked for.
func (s *MemoryStorage) FakeIPRetireRange(addressRange netip.Prefix) error {
	if !addressRange.IsValid() {
		return nil
	}
	s.retiredAccess.Lock()
	defer s.retiredAccess.Unlock()
	for _, retired := range s.retired {
		if retired == addressRange {
			return nil
		}
	}
	s.retired = append(s.retired, addressRange)
	if len(s.retired) > adapter.FakeIPMaxRetiredRanges {
		s.retired = s.retired[len(s.retired)-adapter.FakeIPMaxRetiredRanges:]
	}
	return nil
}

func (s *MemoryStorage) FakeIPRetired() []netip.Prefix {
	s.retiredAccess.RLock()
	defer s.retiredAccess.RUnlock()
	return adapter.FakeIPNormalizeRetiredRanges(append([]netip.Prefix(nil), s.retired...))
}
