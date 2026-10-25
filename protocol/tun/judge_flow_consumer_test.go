package tun

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing-tun/gtcpip/header"
	"go4.org/netipx"

	"github.com/stretchr/testify/require"
)

// The TUN inbound serves two entries with one implementation: the stack calls JudgeFlow, and the
// auto-redirect service calls the handler below for the packets its nfqueue intercepts. Only the
// second one has a transport that applies a Bypass verdict - sing-tun's nfqueue answers NfRepeat
// plus the output mark - so only the second one may ask the router for the implicit Direct Fast
// Path decision. See adapter.InboundContext.ConsumesBypassVerdict.
//
// This is the boundary the platform constant could not express: auto_redirect is a mode, and the
// same binary serves both entries. The test runs on any host, because the two entry points exist in
// every build.

// recordingBypassConsumer is the router the inbound holds. It records what the TUN layer claimed
// about the caller, and always answers "continue" so the verdict itself stays uninteresting.
type recordingBypassConsumer struct {
	adapter.Router
	calls             atomic.Int32
	lastConsumesValue atomic.Bool
	seen              atomic.Bool
}

func (r *recordingBypassConsumer) PreMatch(metadata adapter.InboundContext, firstPacket []byte) adapter.PreMatchResult {
	r.calls.Add(1)
	r.lastConsumesValue.Store(metadata.ConsumesBypassVerdict)
	r.seen.Store(true)
	return adapter.PreMatchResult{Action: adapter.PreMatchContinue}
}

func newBypassConsumerInbound() (*Inbound, *recordingBypassConsumer) {
	router := &recordingBypassConsumer{}
	inbound := &Inbound{
		tag:    "tun-in",
		ctx:    context.Background(),
		router: router,
		logger: log.NewNOPFactory().Logger(),
	}
	return inbound, router
}

func TestTheTUNStackDoesNotAskForABypassVerdict(t *testing.T) {
	inbound, router := newBypassConsumerInbound()

	verdict := inbound.JudgeFlow(
		uint8(header.TCPProtocolNumber),
		netip.MustParseAddrPort("192.168.1.2:40000"),
		netip.MustParseAddrPort("93.184.216.34:443"),
		nil,
	)

	require.Equal(t, tun.ActionAccept, verdict.Action)
	require.EqualValues(t, 1, router.calls.Load(), "the flow must still reach the router")
	require.True(t, router.seen.Load())
	require.False(t, router.lastConsumesValue.Load(),
		"the stack installs a Bypass verdict as an accept entry, so it must not ask for one")
}

func TestTheAutoRedirectEntryAsksForABypassVerdict(t *testing.T) {
	inbound, router := newBypassConsumerInbound()

	verdict := (*autoRedirectHandler)(inbound).JudgeFlow(
		uint8(header.TCPProtocolNumber),
		netip.MustParseAddrPort("192.168.1.2:40000"),
		netip.MustParseAddrPort("93.184.216.34:443"),
		nil,
	)

	require.Equal(t, tun.ActionAccept, verdict.Action)
	require.EqualValues(t, 1, router.calls.Load())
	require.True(t, router.lastConsumesValue.Load(),
		"the nfqueue consumer applies a Bypass verdict, so its entry is the one that asks for it")
}

// The two entries must share everything else. If they ever diverge into two flow policies, this is
// where it shows: the same DNS hijack, FakeIP guard, L0 route set and router decision have to
// produce the same answer for both, with only the capability bit differing.
func TestBothEntriesShareTheSameFlowPolicy(t *testing.T) {
	inbound, router := newBypassConsumerInbound()
	inbound.dnsHijackByPort = true

	source := netip.MustParseAddrPort("192.168.1.2:40000")
	destination := netip.MustParseAddrPort("93.184.216.34:53")

	stackVerdict := inbound.JudgeFlow(uint8(header.UDPProtocolNumber), source, destination, nil)
	redirectVerdict := (*autoRedirectHandler)(inbound).JudgeFlow(uint8(header.UDPProtocolNumber), source, destination, nil)

	require.Equal(t, tun.ActionHijackDNS, stackVerdict.Action)
	require.Equal(t, stackVerdict.Action, redirectVerdict.Action,
		"a DNS hijack must not depend on which entry judged the flow")
	require.EqualValues(t, 0, router.calls.Load(),
		"the DNS hijack is decided before the router by both entries")
}

// L0 is shared for the same reason, and it is the one branch where the verdict is a Bypass for a
// flow the platform routes itself. It is decided before either entry reaches the router, so it must
// be identical on both - and it must not become entry-dependent when the capability is added.
func TestBothEntriesShareTheSameL0Policy(t *testing.T) {
	inbound, router := newBypassConsumerInbound()
	var builder netipx.IPSetBuilder
	builder.AddPrefix(netip.MustParsePrefix("10.0.0.0/8"))
	routeSet, err := builder.IPSet()
	require.NoError(t, err)
	inbound.routeAddressSet = []*netipx.IPSet{routeSet}

	source := netip.MustParseAddrPort("192.168.1.2:40000")
	destination := netip.MustParseAddrPort("93.184.216.34:443") // outside 10/8: an L0 miss

	stackVerdict := inbound.JudgeFlow(uint8(header.TCPProtocolNumber), source, destination, nil)
	redirectVerdict := (*autoRedirectHandler)(inbound).JudgeFlow(uint8(header.TCPProtocolNumber), source, destination, nil)

	require.Equal(t, tun.ActionBypass, stackVerdict.Action,
		"an L0 miss is answered before any entry-specific decision")
	require.Equal(t, stackVerdict.Action, redirectVerdict.Action,
		"L0 must not depend on which entry judged the flow")
	require.EqualValues(t, 0, router.calls.Load(),
		"L0 answers without the router on both entries")
}

// TestTwoEntriesSharingOneRouterDoNotLeakTheCapability is CPD-12, and it is the test the design
// exists for.
//
// In auto_redirect mode ONE Inbound object serves both entries: the stack is built with
// Handler: t, and the auto-redirect service is built with Handler: (*autoRedirectHandler)(t). So
// the capability cannot be a field of the inbound, and it is not: it travels on the per-flow
// InboundContext. Two inbounds sharing one router - and one inbound serving both entries - must
// answer each flow according to the entry that judged it, with no persistence and no borrowing.
func TestTwoEntriesSharingOneRouterDoNotLeakTheCapability(t *testing.T) {
	// A real router: no rules, one bypassable direct outbound, so a flow is implicit-bypass
	// eligible exactly when the entry asks for the decision.
	fixture := newL0ConflictFixture(t, nil, nil)
	plain := &Inbound{
		tag:    "tun-plain",
		ctx:    context.Background(),
		router: fixture.router,
		logger: log.NewNOPFactory().Logger(),
	}

	source := netip.MustParseAddrPort("192.168.1.2:40000")
	destination := netip.MustParseAddrPort("93.184.216.34:443")

	// 128 interleaved rounds over both networks: enough that a per-inbound or per-router flag would
	// be observed carrying over, and both protocols so the capability cannot accidentally be tied to
	// one of them.
	for round := 0; round < 128; round++ {
		protocol := uint8(header.TCPProtocolNumber)
		if round%2 == 1 {
			protocol = uint8(header.UDPProtocolNumber)
		}
		require.Equal(t, tun.ActionAccept, plain.JudgeFlow(protocol, source, destination, nil).Action,
			"round %d: the stack entry of the second inbound must not borrow the other inbound's capability", round)
		require.Equal(t, tun.ActionBypass, (*autoRedirectHandler)(fixture.inbound).JudgeFlow(protocol, source, destination, nil).Action,
			"round %d: the native entry must still receive the verdict", round)
		require.Equal(t, tun.ActionAccept, fixture.inbound.JudgeFlow(protocol, source, destination, nil).Action,
			"round %d: the same inbound, judged by the stack, must not inherit its own native answer", round)
		require.Equal(t, tun.ActionBypass, (*autoRedirectHandler)(plain).JudgeFlow(protocol, source, destination, nil).Action,
			"round %d: the capability belongs to the call, so either inbound can be the native entry", round)
	}
}

// TestTheCapabilityIsPerCallUnderConcurrency is the same property without the serialisation.
//
// Sixteen goroutines drive both inbounds through both entries at once. A capability stored on the
// Router (a field, a map, a shared variable) would be overwritten between the write and the read;
// one stored on the Inbound would be overwritten by the other entry of the SAME object. The failures
// are counted rather than asserted inside the goroutines, because require in a worker goroutine is
// not allowed to call FailNow.
func TestTheCapabilityIsPerCallUnderConcurrency(t *testing.T) {
	fixture := newL0ConflictFixture(t, nil, nil)
	plain := &Inbound{
		tag:    "tun-plain",
		ctx:    context.Background(),
		router: fixture.router,
		logger: log.NewNOPFactory().Logger(),
	}

	source := netip.MustParseAddrPort("192.168.1.2:40000")
	destination := netip.MustParseAddrPort("93.184.216.34:443")

	var (
		workers  = 16
		rounds   = 8
		failures atomic.Int32
		wg       sync.WaitGroup
	)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for round := 0; round < rounds; round++ {
				protocol := uint8(header.TCPProtocolNumber)
				if (worker+round)%2 == 1 {
					protocol = uint8(header.UDPProtocolNumber)
				}
				if plain.JudgeFlow(protocol, source, destination, nil).Action != tun.ActionAccept {
					failures.Add(1)
				}
				if (*autoRedirectHandler)(fixture.inbound).JudgeFlow(protocol, source, destination, nil).Action != tun.ActionBypass {
					failures.Add(1)
				}
				if fixture.inbound.JudgeFlow(protocol, source, destination, nil).Action != tun.ActionAccept {
					failures.Add(1)
				}
				if (*autoRedirectHandler)(plain).JudgeFlow(protocol, source, destination, nil).Action != tun.ActionBypass {
					failures.Add(1)
				}
			}
		}(worker)
	}
	wg.Wait()
	require.Zero(t, failures.Load(),
		"the capability was observed on a flow that should not have it, or lost on one that should")
}
