package dns

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// The generation the DIALER reads must be cheap, and the family a strict branch forces must be the
// family it asks for (§C, §E).
//
// DNSGeneration is read by the candidate scheduler on every launch decision of every connection.
// The query-side accessor observes the DNS environment (a router-wide lock and an all-transport scan);
// a dial-facing reporter must not, or the dial hot path inherits a process-wide DNS lock shared with
// every query.

// environmentScanTransport counts how many times the router asks for its environment.
type environmentScanTransport struct {
	adapter.DNSTransport
	scans atomic.Int32
}

func (t *environmentScanTransport) Type() string                                   { return "scanning" }
func (t *environmentScanTransport) Tag() string                                    { return "scanning" }
func (t *environmentScanTransport) Reset()                                         {}
func (t *environmentScanTransport) Close() error                                   { return nil }
func (t *environmentScanTransport) Dependencies() []string                         { return nil }
func (t *environmentScanTransport) Start(adapter.StartStage, *adapter.Scope) error { return nil }
func (t *environmentScanTransport) Exchange(context.Context, *mDNS.Msg) (*mDNS.Msg, error) {
	return nil, nil
}
func (t *environmentScanTransport) ExchangeAsync(context.Context, *mDNS.Msg, func(*mDNS.Msg, error)) {
}

func (t *environmentScanTransport) Environment() []string {
	t.scans.Add(1)
	return []string{"192.0.2.53:53"}
}

type environmentScanManager struct {
	transport adapter.DNSTransport
}

func (m *environmentScanManager) Start(adapter.StartStage, *adapter.Scope) error { return nil }
func (m *environmentScanManager) Transports() []adapter.DNSTransport {
	return []adapter.DNSTransport{m.transport}
}
func (m *environmentScanManager) Transport(string) (adapter.DNSTransport, bool) {
	return m.transport, true
}
func (m *environmentScanManager) Default() adapter.DNSTransport { return m.transport }
func (m *environmentScanManager) FakeIP() adapter.FakeIPTransport {
	return nil
}
func (m *environmentScanManager) Create(context.Context, log.ContextLogger, string, string, any) error {
	return nil
}

// TestDNSGenerationIsACheapRead pins the cost of the dial-facing reporter.
func TestDNSGenerationIsACheapRead(t *testing.T) {
	scanning := &environmentScanTransport{}
	router := &Router{
		ctx:       context.Background(),
		logger:    log.NewNOPFactory().Logger(),
		transport: &environmentScanManager{transport: scanning},
	}
	router.DNSGeneration() // the pinning observation is allowed to scan once
	scanning.scans.Store(0)

	for i := 0; i < 8; i++ {
		router.DNSGeneration()
	}
	require.Zero(t, scanning.scans.Load(),
		"the dialer's reporter must not scan transports: it runs on every launch decision of every "+
			"connection, while the router's DNS-environment lock is shared with every query")

	// The control: the query-side accessor still observes, so a resolver change is seen at issue.
	router.dnsGeneration()
	require.Positive(t, scanning.scans.Load())
}

var _ = netip.Addr{}
