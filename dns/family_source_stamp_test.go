package dns

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// The published source stamp must be the generation the producing operation captured (§C).
//
// # The defect these pin
//
// LookupFamilies read the DNS generation before it called into the lookup and stamped whatever it
// read. Everything that decides which transport answers - rule matching, transport selection, the
// transport's own Type/Tag hooks - runs between that read and the client's capture, so a generation
// advanced there produced an answer stamped with the environment it was NOT asked on. The dialer
// then demoted a perfectly current answer as if it belonged to the environment the flow had left.
//
// The stamp now travels from the client operation that produced the answer. These tests inject the
// advance at two different boundaries and assert both directions:
//
//	advanced BEFORE the client captured  -> the answer must carry the NEW generation
//	advanced AFTER the question was sent  -> the late answer must keep the OLD generation
//
// Reverting the fix to a caller-side pre-read reddens the first; changing it to read "now" at
// publication reddens the second.

// sourceAdvancingTransport injects a generation advance at a chosen hook, once.
type sourceAdvancingTransport struct {
	adapter.DNSTransport
	router *Router
	// advanceOnType advances during the transport's Type() call, which Router.Lookup makes before
	// the client is reached.
	advanceOnType atomic.Bool
	// advanceOnTag advances during Tag(), which the client calls while building the cache key,
	// still before its operation captures the generation.
	advanceOnTag atomic.Bool
	// entered is closed when the exchange begins, so a test can advance AFTER the question issued.
	entered chan struct{}
	// release holds the exchange open until the test closes it.
	release chan struct{}
	once    sync.Once
}

func (t *sourceAdvancingTransport) advance() {
	t.once.Do(func() {
		if t.router != nil {
			t.router.networkGeneration.Add(1)
		}
	})
}

func (t *sourceAdvancingTransport) Type() string {
	if t.advanceOnType.Load() {
		t.advance()
	}
	return "advancing"
}

func (t *sourceAdvancingTransport) Tag() string {
	if t.advanceOnTag.Load() {
		t.advance()
	}
	return "advancing"
}

func (t *sourceAdvancingTransport) Reset()                                         {}
func (t *sourceAdvancingTransport) Close() error                                   { return nil }
func (t *sourceAdvancingTransport) Dependencies() []string                         { return nil }
func (t *sourceAdvancingTransport) Start(adapter.StartStage, *adapter.Scope) error { return nil }

func (t *sourceAdvancingTransport) Exchange(ctx context.Context, message *mDNS.Msg) (*mDNS.Msg, error) {
	if t.entered != nil {
		select {
		case t.entered <- struct{}{}:
		default:
		}
	}
	if t.release != nil {
		select {
		case <-t.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	response := new(mDNS.Msg)
	response.SetReply(message)
	if message.Question[0].Qtype == mDNS.TypeAAAA {
		response.Answer = append(response.Answer, &mDNS.AAAA{
			Hdr:  mDNS.RR_Header{Name: message.Question[0].Name, Rrtype: mDNS.TypeAAAA, Class: mDNS.ClassINET, Ttl: 60},
			AAAA: make([]byte, 16),
		})
	} else {
		response.Answer = append(response.Answer, &mDNS.A{
			Hdr: mDNS.RR_Header{Name: message.Question[0].Name, Rrtype: mDNS.TypeA, Class: mDNS.ClassINET, Ttl: 60},
			A:   []byte{192, 0, 2, 1},
		})
	}
	return response, nil
}

func (t *sourceAdvancingTransport) ExchangeAsync(ctx context.Context, message *mDNS.Msg, callback func(*mDNS.Msg, error)) {
	go callback(t.Exchange(ctx, message))
}

// sourcedLookup harness: a Router whose client records every generation it captures.
func newSourcedHarness(t *testing.T, transport *sourceAdvancingTransport) (*Router, *atomic.Uint64, func() []uint64) {
	t.Helper()
	router := &Router{ctx: context.Background(), logger: log.NewNOPFactory().Logger()}
	transport.router = router
	var (
		access   sync.Mutex
		captures []uint64
	)
	client := NewClient(ClientOptions{
		Context: context.Background(),
		Logger:  log.NewNOPFactory().NewLogger("sourced"),
		NetworkGeneration: func() uint64 {
			value := router.networkGeneration.Load()
			access.Lock()
			captures = append(captures, value)
			access.Unlock()
			return value
		},
	})
	client.Start()
	router.client = client
	captured := func() []uint64 {
		access.Lock()
		defer access.Unlock()
		return append([]uint64(nil), captures...)
	}
	return router, &router.networkGeneration, captured
}

func publishedFamilyResult(t *testing.T, router *Router, transport adapter.DNSTransport, domain string, strategy C.DomainStrategy) adapter.DNSFamilyResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan adapter.DNSFamilyResult, 2)
	err := router.LookupFamilies(ctx, domain, adapter.DNSQueryOptions{
		Transport:      transport,
		LookupStrategy: strategy,
	}, func(result adapter.DNSFamilyResult) { results <- result })
	require.NoError(t, err)
	close(results)
	var published adapter.DNSFamilyResult
	for result := range results {
		published = result
	}
	return published
}

// TestSourceStampFollowsTheProducingOperation covers the boundaries that run AFTER a caller-side
// pre-read and BEFORE the client captures: transport Type selection and cache-key Tag.
func TestSourceStampFollowsTheProducingOperation(t *testing.T) {
	for _, testCase := range []struct {
		name string
		set  func(*sourceAdvancingTransport)
	}{
		{"advance during transport Type selection", func(transport *sourceAdvancingTransport) {
			transport.advanceOnType.Store(true)
		}},
		{"advance during cache-key Tag", func(transport *sourceAdvancingTransport) {
			transport.advanceOnTag.Store(true)
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			transport := &sourceAdvancingTransport{}
			router, generation, captured := newSourcedHarness(t, transport)
			generation.Store(1)
			testCase.set(transport)

			published := publishedFamilyResult(t, router, transport, "stamp.test.", C.DomainStrategyIPv4Only)
			t.Logf("captures=%v published=%d known=%v", captured(), published.SourceGeneration, published.HasSourceGeneration)
			require.True(t, published.HasSourceGeneration)
			require.EqualValues(t, 2, published.SourceGeneration,
				"the answer was produced after the advance, so its source is the new generation")
			require.Equal(t, captured()[0], published.SourceGeneration,
				"the published stamp must equal the generation the operation captured")
		})
	}
}

// TestLateAnswerKeepsTheGenerationItWasAskedOn is the opposite direction: the generation advances
// while the question is already on the wire, and the answer must keep the old stamp.
func TestLateAnswerKeepsTheGenerationItWasAskedOn(t *testing.T) {
	transport := &sourceAdvancingTransport{entered: make(chan struct{}, 1), release: make(chan struct{})}
	router, generation, captured := newSourcedHarness(t, transport)
	generation.Store(1)
	// The advance must NOT be triggered by Type/Tag here: it happens after the exchange begins.
	transport.advanceOnType.Store(false)
	transport.advanceOnTag.Store(false)

	done := make(chan adapter.DNSFamilyResult, 1)
	go func() {
		done <- publishedFamilyResult(t, router, transport, "late.test.", C.DomainStrategyIPv4Only)
	}()

	select {
	case <-transport.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the exchange never began")
	}
	// The question is on the wire; the environment moves before the answer arrives.
	router.networkGeneration.Add(1)
	close(transport.release)

	published := <-done
	t.Logf("captures=%v published=%d known=%v", captured(), published.SourceGeneration, published.HasSourceGeneration)
	require.True(t, published.HasSourceGeneration)
	require.EqualValues(t, 1, published.SourceGeneration,
		"a late answer must keep the generation it was asked on, not the one that arrived meanwhile")
}

// TestWarmCacheReportsAKnownCurrentSource keeps the cache-hit path from degrading to unknown.
func TestWarmCacheReportsAKnownCurrentSource(t *testing.T) {
	transport := &sourceAdvancingTransport{}
	router, generation, _ := newSourcedHarness(t, transport)
	generation.Store(1)

	first := publishedFamilyResult(t, router, transport, "warm.test.", C.DomainStrategyIPv4Only)
	require.True(t, first.HasSourceGeneration, "a cold query must report its source")

	// The environment moves; the cached entry still matches the environment key, so the hit is
	// served under the current one.
	generation.Store(2)
	second := publishedFamilyResult(t, router, transport, "warm.test.", C.DomainStrategyIPv4Only)
	require.True(t, second.HasSourceGeneration, "a cache hit must not report unknown")
	require.EqualValues(t, 2, second.SourceGeneration,
		"the hit is served under the current environment, which is the key it matched")
	require.Equal(t, []string{"192.0.2.1"}, addressTexts(second.Addresses))
}

// TestClientWithoutAGenerationCallbackReportsUnknown keeps third-party clients compatible: a client
// that cannot report a generation publishes unknown, which the consumer treats as current.
func TestClientWithoutAGenerationCallbackReportsUnknown(t *testing.T) {
	transport := &sourceAdvancingTransport{}
	router := &Router{ctx: context.Background(), logger: log.NewNOPFactory().Logger()}
	transport.router = router
	client := NewClient(ClientOptions{Context: context.Background(), Logger: log.NewNOPFactory().NewLogger("plain")})
	client.Start()
	router.client = client

	published := publishedFamilyResult(t, router, transport, "unknown.test.", C.DomainStrategyIPv4Only)
	require.False(t, published.HasSourceGeneration)
	require.Equal(t, []string{"192.0.2.1"}, addressTexts(published.Addresses))
}

func addressTexts(addresses []netip.Addr) []string {
	texts := make([]string, len(addresses))
	for index, address := range addresses {
		texts[index] = address.String()
	}
	return texts
}
