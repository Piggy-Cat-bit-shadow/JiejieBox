package route

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func benchRouter(b *testing.B) (*Router, *plainDirectOutbound) {
	b.Helper()
	outbound := &plainDirectOutbound{}
	outbound.canBypass.Store(1)
	return &Router{
		ctx:      context.Background(),
		logger:   log.NewNOPFactory().Logger(),
		outbound: &stubOutboundManager{def: outbound},
	}, outbound
}

// The two halves of this round's decision, measured against the same router and the same flow.
//
// The structural claim is a count, not a duration: for the stack entry the eligibility chain is
// never entered, so the outbound's CanBypass is called zero times per flow, and for the native entry
// it is called once. These benchmarks put a cost on that difference on this machine; they are not an
// end-to-end TUN measurement and not a throughput claim, and the numbers move with the rule set,
// which is why the rule set here is empty and the flow is the minimal eligible one.
//
// Run with:
//
//	go test -run '^$' -bench 'BenchmarkPreMatch' -benchmem ./route/
func BenchmarkPreMatchWithoutAConsumer(b *testing.B) {
	router, _ := benchRouter(b)
	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)
	metadata.ConsumesBypassVerdict = false
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		router.preMatchFlow(context.Background(), &metadata, destination, nil, "")
	}
}

func BenchmarkPreMatchForAConsumer(b *testing.B) {
	router, _ := benchRouter(b)
	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		router.preMatchFlow(context.Background(), &metadata, destination, nil, "")
	}
}
