//go:build with_naive_outbound

package naive

import (
	"context"
	"net"
	"net/netip"
	"testing"

	mDNS "github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

// The Chromium DNS bridge (audit Q34: the slow/failing DNS callback is a native boundary).
//
// These tests drive the exact function the outbound installs in cronet.NaiveClientOptions, so a
// change to the bridge's contract - "always answer with a DNS message", "never drop the policy
// options", "no second timer" - fails here instead of appearing as a Chromium name-resolution
// failure in production.

type bridgeTestDialer struct {
	optionsCalls int
	options      adapter.DNSQueryOptions
}

func (d *bridgeTestDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, E.New("not used")
}

func (d *bridgeTestDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, E.New("not used")
}

func (d *bridgeTestDialer) QueryOptions() adapter.DNSQueryOptions {
	d.optionsCalls++
	return d.options
}

func (d *bridgeTestDialer) EffectiveFamilyStrategy() C.DomainStrategy {
	return C.DomainStrategyAsIS
}

type bridgeTestDNSRouter struct {
	response *mDNS.Msg
	err      error

	exchanges int
	lastQuery *mDNS.Msg
	lastCtx   context.Context
}

func (r *bridgeTestDNSRouter) Start(adapter.StartStage, *adapter.Scope) error { return nil }
func (r *bridgeTestDNSRouter) Close() error                                   { return nil }
func (r *bridgeTestDNSRouter) Exchange(ctx context.Context, message *mDNS.Msg, _ adapter.DNSQueryOptions) (*mDNS.Msg, error) {
	r.exchanges++
	r.lastQuery = message
	r.lastCtx = ctx
	return r.response, r.err
}
func (r *bridgeTestDNSRouter) ExchangeAsync(ctx context.Context, message *mDNS.Msg, options adapter.DNSQueryOptions, callback func(*mDNS.Msg, error)) {
	response, err := r.Exchange(ctx, message, options)
	callback(response, err)
}
func (r *bridgeTestDNSRouter) Lookup(context.Context, string, adapter.DNSQueryOptions) ([]netip.Addr, error) {
	return nil, E.New("not used")
}
func (r *bridgeTestDNSRouter) ClearCache()                                    {}
func (r *bridgeTestDNSRouter) LookupReverseMapping(netip.Addr) (string, bool) { return "", false }
func (r *bridgeTestDNSRouter) ResetNetwork()                                  {}

var _ dialer.ResolveDialer = (*bridgeTestDialer)(nil)
var _ adapter.DNSRouter = (*bridgeTestDNSRouter)(nil)

func bridgeTestQuery(t *testing.T) *mDNS.Msg {
	t.Helper()
	message := new(mDNS.Msg)
	message.SetQuestion("proxy.example.", mDNS.TypeA)
	message.Id = 0x2345
	return message
}

// TestDNSBridgeWithoutARouterIsAbsent pins the bare-build behaviour: no policy, no bridge.
func TestDNSBridgeWithoutARouterIsAbsent(t *testing.T) {
	resolver := newCronetDNSResolver(nil, &bridgeTestDialer{}, logger.NOP())
	require.Nil(t, resolver, "a build without a DNS router must not install a bridge")
}

// TestDNSBridgeAnswersFailuresWithSERVFAIL is the failing/slow exchange contract: the native
// callback cannot accept an error, so a failure becomes a DNS answer carrying the same question.
func TestDNSBridgeAnswersFailuresWithSERVFAIL(t *testing.T) {
	router := &bridgeTestDNSRouter{err: E.New("resolver exploded")}
	resolver := newCronetDNSResolver(router, &bridgeTestDialer{}, logger.NOP())
	require.NotNil(t, resolver)

	query := bridgeTestQuery(t)
	response := resolver(context.Background(), query)
	require.NotNil(t, response, "a failing exchange must still produce a DNS message")
	require.Equal(t, mDNS.RcodeServerFailure, response.Rcode)
	require.Equal(t, query.Id, response.Id, "the answer must belong to the question")
	require.Len(t, router.lastQuery.Question, 1)
	require.Equal(t, query.Question[0].Name, router.lastQuery.Question[0].Name)
	require.Equal(t, 1, router.exchanges)
}

// TestDNSBridgeReturnsTheSuccessUnchanged pins that the policy's answer is not rewritten.
func TestDNSBridgeReturnsTheSuccessUnchanged(t *testing.T) {
	answer := new(mDNS.Msg)
	answer.SetReply(bridgeTestQuery(t))
	answer.Answer = append(answer.Answer, &mDNS.A{
		Hdr: mDNS.RR_Header{Name: "proxy.example.", Rrtype: mDNS.TypeA, Class: mDNS.ClassINET, Ttl: 60},
		A:   netip.MustParseAddr("192.0.2.7").AsSlice(),
	})
	router := &bridgeTestDNSRouter{response: answer}
	resolver := newCronetDNSResolver(router, &bridgeTestDialer{}, logger.NOP())

	response := resolver(context.Background(), bridgeTestQuery(t))
	require.Same(t, answer, response, "the bridge must not copy or rewrite the policy's answer")
	require.Len(t, response.Answer, 1)
}

// TestDNSBridgePassesTheDialersQueryOptions pins the policy plumbing: the exchange must be made
// with THIS outbound's resolver options, not with defaults.
func TestDNSBridgePassesTheDialersQueryOptions(t *testing.T) {
	expected := adapter.DNSQueryOptions{Strategy: C.DomainStrategyIPv4Only}
	testDialer := &bridgeTestDialer{options: expected}
	router := &bridgeTestDNSRouter{response: new(mDNS.Msg)}
	resolver := newCronetDNSResolver(router, testDialer, logger.NOP())

	resolver(context.Background(), bridgeTestQuery(t))
	require.Equal(t, 1, testDialer.optionsCalls, "one query must read the options once")
}

// TestDNSBridgeKeepsTheQueryContext pins that the bridge adds no timer of its own: the context the
// router sees is the one Chromium supplied, and a cancelled one is answered rather than dropped.
func TestDNSBridgeKeepsTheQueryContext(t *testing.T) {
	router := &bridgeTestDNSRouter{err: context.Canceled}
	resolver := newCronetDNSResolver(router, &bridgeTestDialer{}, logger.NOP())

	ctx, cancel := context.WithCancel(context.Background())
	query := bridgeTestQuery(t)
	responseCh := make(chan *mDNS.Msg, 1)
	go func() {
		responseCh <- resolver(ctx, query)
	}()
	cancel()
	response := <-responseCh
	require.NotNil(t, response)
	require.Equal(t, mDNS.RcodeServerFailure, response.Rcode)
	require.ErrorIs(t, router.lastCtx.Err(), context.Canceled,
		"the router must see the caller's cancellation, not a private timer")
}

// TestDNSBridgeIgnoresANilQuery pins the native-boundary guard.
func TestDNSBridgeIgnoresANilQuery(t *testing.T) {
	router := &bridgeTestDNSRouter{}
	resolver := newCronetDNSResolver(router, &bridgeTestDialer{}, logger.NOP())
	require.Nil(t, resolver(context.Background(), nil))
	require.Zero(t, router.exchanges)
}
