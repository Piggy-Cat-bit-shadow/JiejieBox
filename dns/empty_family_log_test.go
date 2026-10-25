package dns

import (
	"context"
	"net/netip"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

// The dual-stack probe asks A and AAAA as separate lookups and combines them. A name that is
// published only as A - which is every outbound `server` resolved through a hosts entry with one
// IPv4 address - therefore leaves the AAAA lookup with an empty answer on EVERY connection.
//
// That empty half-answer used to be logged as `lookup failed for <domain>` at ERROR, once per
// connection, for traffic that is working exactly as configured. It is the same defect (and the
// same cost: log I/O on the dial path plus a line that trains an operator to ignore ERROR) that
// ErrLocalVerdict removed for blocked names, so it is fixed the same way: the per-family error is
// still returned and still published, only its log level changes.
//
// The two tests are a pair and neither is sufficient alone:
//   - the family probe must not log at Error,
//   - and a lookup that is NOT a family probe must still log at Error, so the change cannot be
//     "silence everything that comes back empty".

// capturingLogger records the level and text of every line the router emits.
type capturingLogger struct {
	access sync.Mutex
	errors []string
	debugs []string
}

var _ logger.ContextLogger = (*capturingLogger)(nil)

func (l *capturingLogger) record(dst *[]string, args ...any) {
	l.access.Lock()
	defer l.access.Unlock()
	*dst = append(*dst, joinArgs(args))
}

func (l *capturingLogger) snapshot() (errors []string, debugs []string) {
	l.access.Lock()
	defer l.access.Unlock()
	return append([]string(nil), l.errors...), append([]string(nil), l.debugs...)
}

func (l *capturingLogger) Trace(args ...any) {}
func (l *capturingLogger) Debug(args ...any) { l.record(&l.debugs, args...) }
func (l *capturingLogger) Info(args ...any)  {}
func (l *capturingLogger) Warn(args ...any)  {}
func (l *capturingLogger) Error(args ...any) { l.record(&l.errors, args...) }
func (l *capturingLogger) Fatal(args ...any) { l.record(&l.errors, args...) }
func (l *capturingLogger) Panic(args ...any) { l.record(&l.errors, args...) }

func (l *capturingLogger) TraceContext(ctx context.Context, args ...any) {}
func (l *capturingLogger) DebugContext(ctx context.Context, args ...any) {
	l.record(&l.debugs, args...)
}
func (l *capturingLogger) InfoContext(ctx context.Context, args ...any) {}
func (l *capturingLogger) WarnContext(ctx context.Context, args ...any) {}
func (l *capturingLogger) ErrorContext(ctx context.Context, args ...any) {
	l.record(&l.errors, args...)
}
func (l *capturingLogger) FatalContext(ctx context.Context, args ...any) {
	l.record(&l.errors, args...)
}
func (l *capturingLogger) PanicContext(ctx context.Context, args ...any) {
	l.record(&l.errors, args...)
}

func joinArgs(args []any) string {
	// The router's own calls already carry their spacing ("lookup failed for ", domain), so the
	// recorder concatenates exactly as the real formatter would rather than inserting its own.
	var b []byte
	for _, arg := range args {
		switch value := arg.(type) {
		case string:
			b = append(b, value...)
		case error:
			b = append(b, value.Error()...)
		default:
			b = append(b, '?')
		}
	}
	return string(b)
}

func quietTestRouter(t *testing.T, transport *fakeDNSTransport, capture logger.ContextLogger) *Router {
	t.Helper()
	return &Router{
		ctx:    context.Background(),
		logger: capture,
		transport: &fakeDNSTransportManager{
			transports:       map[string]adapter.DNSTransport{transport.tag: transport},
			defaultTransport: transport,
		},
		client: NewClient(ClientOptions{
			Context:      context.Background(),
			DisableCache: true,
			Logger:       capture,
		}),
	}
}

// TestFamilyProbeEmptyAnswerIsNotAnError drives the real production path: LookupFamilies over a
// transport that only knows an IPv4 address.
func TestFamilyProbeEmptyAnswerIsNotAnError(t *testing.T) {
	t.Parallel()

	transport := &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.1")}
	capture := &capturingLogger{}
	router := quietTestRouter(t, transport, capture)

	results := make(map[bool]adapter.DNSFamilyResult)
	err := router.LookupFamilies(context.Background(), "only-a.example", adapter.DNSQueryOptions{}, func(result adapter.DNSFamilyResult) {
		results[result.IPv6] = result
	})
	require.NoError(t, err, "one family answering is a successful dual-stack lookup")

	ipv4, loaded := results[false]
	require.True(t, loaded, "the IPv4 family must be published")
	require.NoError(t, ipv4.Err)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.1")}, ipv4.Addresses)

	ipv6, loaded := results[true]
	require.True(t, loaded, "the IPv6 family must be published even when empty")
	require.Error(t, ipv6.Err, "the per-family error is preserved for the caller")
	require.Empty(t, ipv6.Addresses)

	errors, debugs := capture.snapshot()
	require.Empty(t, errors, "an empty address family is not a lookup failure; got %v", errors)
	require.Contains(t, joinAll(debugs), "no addresses for only-a.example",
		"the empty family must still be visible at debug level; got %v", debugs)
}

// TestUnquietEmptyAnswerStillLogsAnError is the negative control: the same empty answer on a
// lookup that is NOT part of a family probe keeps its ERROR line, so the fix cannot hide a real
// resolution failure.
func TestUnquietEmptyAnswerStillLogsAnError(t *testing.T) {
	t.Parallel()

	transport := &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.1")}
	capture := &capturingLogger{}
	router := quietTestRouter(t, transport, capture)

	addresses, err := router.Lookup(context.Background(), "only-a.example", adapter.DNSQueryOptions{
		LookupStrategy: C.DomainStrategyIPv6Only,
		Strategy:       C.DomainStrategyIPv6Only,
	})
	require.Error(t, err)
	require.Empty(t, addresses)

	errors, _ := capture.snapshot()
	require.Len(t, errors, 1, "a standalone empty lookup must keep reporting at ERROR; got %v", errors)
	require.Contains(t, joinAll(errors), "lookup failed for only-a.example")
}

func joinAll(lines []string) string {
	var b []byte
	for _, line := range lines {
		b = append(b, line...)
		b = append(b, '\n')
	}
	return string(b)
}
