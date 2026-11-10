package dns

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// Concurrent A/AAAA lookups must not share mutable query metadata (§E3).
//
// LookupFamilies runs the two families concurrently, and each family's query metadata (QTYPE,
// IP version, client subnet) is written into a context. The risk this test rules out is that the
// two families share one metadata object, or that one family's write lands in the other's exchange:
// an A query carrying IPVersion 6 would match IPv6-focused rules, and an ECS prefix from the wrong
// family would be sent upstream and poison the cache key.
//
// The transport blocks until BOTH families have entered, so the two exchanges are provably
// overlapping when their metadata is recorded.

type metadataRecord struct {
	qType     uint16
	queryType uint16
	ipVersion int
	domain    string
	subnet    string
}

type metadataBarrierTransport struct {
	adapter.DNSTransport
	access  sync.Mutex
	records []metadataRecord
	entered chan struct{}
	gate    chan struct{}
	count   int
}

func newMetadataBarrierTransport() *metadataBarrierTransport {
	return &metadataBarrierTransport{
		entered: make(chan struct{}, 2),
		gate:    make(chan struct{}),
	}
}

func (t *metadataBarrierTransport) Type() string                                   { return "metadata-barrier" }
func (t *metadataBarrierTransport) Tag() string                                    { return "metadata-barrier" }
func (t *metadataBarrierTransport) Reset()                                         {}
func (t *metadataBarrierTransport) Close() error                                   { return nil }
func (t *metadataBarrierTransport) Dependencies() []string                         { return nil }
func (t *metadataBarrierTransport) Start(adapter.StartStage, *adapter.Scope) error { return nil }

func (t *metadataBarrierTransport) Exchange(ctx context.Context, message *mDNS.Msg) (*mDNS.Msg, error) {
	record := metadataRecord{qType: message.Question[0].Qtype}
	if metadata := adapter.ContextFrom(ctx); metadata != nil {
		record.queryType = metadata.QueryType
		record.ipVersion = int(metadata.IPVersion)
		record.domain = metadata.Domain
	}
	if opt := message.IsEdns0(); opt != nil {
		for _, item := range opt.Option {
			if subnet, isSubnet := item.(*mDNS.EDNS0_SUBNET); isSubnet {
				record.subnet = subnet.Address.String() + "/" + itoaSubnet(int(subnet.SourceNetmask))
			}
		}
	}
	t.access.Lock()
	t.records = append(t.records, record)
	t.count++
	complete := t.count >= 2
	t.access.Unlock()
	if complete {
		select {
		case <-t.gate:
		default:
			close(t.gate)
		}
	} else {
		select {
		case <-t.entered:
		default:
		}
	}
	select {
	case <-t.gate:
	case <-time.After(5 * time.Second):
	}
	response := new(mDNS.Msg)
	response.SetReply(message)
	if message.Question[0].Qtype == mDNS.TypeAAAA {
		response.Answer = append(response.Answer, &mDNS.AAAA{
			Hdr:  mDNS.RR_Header{Name: message.Question[0].Name, Rrtype: mDNS.TypeAAAA, Class: mDNS.ClassINET, Ttl: 60},
			AAAA: netip.MustParseAddr("2001:db8::1").AsSlice(),
		})
	} else {
		response.Answer = append(response.Answer, &mDNS.A{
			Hdr: mDNS.RR_Header{Name: message.Question[0].Name, Rrtype: mDNS.TypeA, Class: mDNS.ClassINET, Ttl: 60},
			A:   netip.MustParseAddr("192.0.2.1").AsSlice(),
		})
	}
	return response, nil
}

func (t *metadataBarrierTransport) ExchangeAsync(ctx context.Context, message *mDNS.Msg, callback func(*mDNS.Msg, error)) {
	go callback(t.Exchange(ctx, message))
}

func itoaSubnet(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [3]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}

// TestConcurrentFamiliesDoNotShareQueryMetadata is the isolation evidence.
func TestConcurrentFamiliesDoNotShareQueryMetadata(t *testing.T) {
	transport := newMetadataBarrierTransport()
	router := legacyRuleRouter(t, transport, nil)
	router.defaultDomainStrategy = 0 // AsIS: one complete lookup, both families
	// The modern rule walk is the path that annotates the per-family query metadata.
	router.legacyDNSMode = false

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	addresses, err := router.Lookup(ctx, "metadata.test.", adapter.DNSQueryOptions{
		ClientSubnet: netip.MustParsePrefix("192.0.2.0/24"),
	})
	require.NoError(t, err)
	require.Len(t, addresses, 2, "both families must answer: %v", addresses)

	transport.access.Lock()
	records := append([]metadataRecord(nil), transport.records...)
	transport.access.Unlock()
	t.Logf("records=%+v", records)
	require.Len(t, records, 2, "both exchanges must have run concurrently")

	for _, record := range records {
		switch record.qType {
		case mDNS.TypeA:
			require.EqualValues(t, mDNS.TypeA, record.queryType,
				"the A exchange must carry its own QTYPE in the metadata")
			require.Equal(t, 4, record.ipVersion,
				"the A exchange must not be seen as an IPv6 query")
		case mDNS.TypeAAAA:
			require.EqualValues(t, mDNS.TypeAAAA, record.queryType)
			require.Equal(t, 6, record.ipVersion,
				"the AAAA exchange must not be seen as an IPv4 query")
		default:
			t.Fatalf("unexpected QTYPE %d", record.qType)
		}
		require.Equal(t, "metadata.test", record.domain)
		require.Equal(t, "192.0.2.0/24", record.subnet,
			"the configured client subnet must reach the exchange of each family independently")
	}
}
