package dns

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// "来源竞争：共享等待" from the 02 matrix.
//
// Two concurrent lookups for the same name share ONE exchange through the client's singleflight, and
// the shared answer was asked on the operation's generation. The interesting half is what happens
// when the environment moves while the waiter is parked: the client re-keys the exchange by
// environment, so the waiter abandons the stale shared wait and issues its own query under the new
// environment. Both behaviours are asserted here because they point in opposite directions: sharing
// must not invent a new source, and a changed environment must not hand the waiter the old one.
type sharedWaitTransport struct {
	adapter.DNSTransport
	access    sync.Mutex
	exchanges int
	entered   chan struct{}
	release   chan struct{}
	once      sync.Once
}

func newSharedWaitTransport() *sharedWaitTransport {
	return &sharedWaitTransport{entered: make(chan struct{}), release: make(chan struct{})}
}

func (t *sharedWaitTransport) Type() string                                   { return "shared-wait" }
func (t *sharedWaitTransport) Tag() string                                    { return "shared-wait" }
func (t *sharedWaitTransport) Reset()                                         {}
func (t *sharedWaitTransport) Close() error                                   { return nil }
func (t *sharedWaitTransport) Dependencies() []string                         { return nil }
func (t *sharedWaitTransport) Start(adapter.StartStage, *adapter.Scope) error { return nil }

func (t *sharedWaitTransport) Exchange(ctx context.Context, message *mDNS.Msg) (*mDNS.Msg, error) {
	t.access.Lock()
	t.exchanges++
	t.access.Unlock()
	t.once.Do(func() { close(t.entered) })
	select {
	case <-t.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	response := new(mDNS.Msg)
	response.SetReply(message)
	response.Answer = append(response.Answer, &mDNS.A{
		Hdr: mDNS.RR_Header{Name: message.Question[0].Name, Rrtype: mDNS.TypeA, Class: mDNS.ClassINET, Ttl: 60},
		A:   []byte{192, 0, 2, 1},
	})
	return response, nil
}

func (t *sharedWaitTransport) ExchangeAsync(ctx context.Context, message *mDNS.Msg, callback func(*mDNS.Msg, error)) {
	go callback(t.Exchange(ctx, message))
}

func (t *sharedWaitTransport) exchangeCount() int {
	t.access.Lock()
	defer t.access.Unlock()
	return t.exchanges
}

type sharedWaitHarness struct {
	router    *Router
	transport *sharedWaitTransport
	captures  func() []uint64
}

func newSharedWaitHarness(t *testing.T) *sharedWaitHarness {
	t.Helper()
	transport := newSharedWaitTransport()
	router := &Router{ctx: context.Background(), logger: log.NewNOPFactory().Logger()}
	var (
		captureAccess sync.Mutex
		captures      []uint64
	)
	client := NewClient(ClientOptions{
		Context: context.Background(),
		Logger:  log.NewNOPFactory().NewLogger("shared-wait"),
		NetworkGeneration: func() uint64 {
			value := router.networkGeneration.Load()
			captureAccess.Lock()
			captures = append(captures, value)
			captureAccess.Unlock()
			return value
		},
	})
	client.Start()
	router.client = client
	router.networkGeneration.Store(1)
	return &sharedWaitHarness{
		router:    router,
		transport: transport,
		captures: func() []uint64 {
			captureAccess.Lock()
			defer captureAccess.Unlock()
			return append([]uint64(nil), captures...)
		},
	}
}

func (h *sharedWaitHarness) lookup(t *testing.T, channel chan<- adapter.DNSFamilyResult, domain string) {
	t.Helper()
	var published adapter.DNSFamilyResult
	err := h.router.LookupFamilies(context.Background(), domain, adapter.DNSQueryOptions{
		Transport:      h.transport,
		LookupStrategy: C.DomainStrategyIPv4Only,
	}, func(result adapter.DNSFamilyResult) { published = result })
	require.NoError(t, err)
	channel <- published
}

func TestSharedWaitKeepsTheProducingOperationsSource(t *testing.T) {
	t.Run("no environment change: one exchange, one source", func(t *testing.T) {
		harness := newSharedWaitHarness(t)

		first := make(chan adapter.DNSFamilyResult, 1)
		second := make(chan adapter.DNSFamilyResult, 1)
		go harness.lookup(t, first, "shared.test.")
		select {
		case <-harness.transport.entered:
		case <-time.After(2 * time.Second):
			t.Fatal("the first exchange never began")
		}
		go harness.lookup(t, second, "shared.test.")
		time.Sleep(50 * time.Millisecond)
		close(harness.transport.release)

		firstResult := <-first
		secondResult := <-second
		t.Logf("exchanges=%d captures=%v first=%d shared=%v", harness.transport.exchangeCount(),
			harness.captures(), firstResult.SourceGeneration, firstResult.HasSourceGeneration)
		require.Equal(t, 1, harness.transport.exchangeCount(), "the two lookups must share one exchange")
		require.True(t, firstResult.HasSourceGeneration)
		require.True(t, secondResult.HasSourceGeneration)
		require.EqualValues(t, 1, firstResult.SourceGeneration)
		require.Equal(t, firstResult.SourceGeneration, secondResult.SourceGeneration,
			"a waiter must report the producing operation's source, not its own reading of 'now'")
	})

	t.Run("environment moves while waiting: the waiter re-queries under it", func(t *testing.T) {
		harness := newSharedWaitHarness(t)
		defer func() {
			select {
			case <-harness.transport.release:
			default:
				close(harness.transport.release)
			}
		}()

		first := make(chan adapter.DNSFamilyResult, 1)
		second := make(chan adapter.DNSFamilyResult, 1)
		go harness.lookup(t, first, "shared-change.test.")
		select {
		case <-harness.transport.entered:
		case <-time.After(2 * time.Second):
			t.Fatal("the first exchange never began")
		}
		go harness.lookup(t, second, "shared-change.test.")
		time.Sleep(50 * time.Millisecond)
		// The environment moves while the waiter is parked. The exchange key includes it, so the
		// waiter must not consume the answer asked on the old environment.
		harness.router.networkGeneration.Add(1)
		close(harness.transport.release)

		firstResult := <-first
		secondResult := <-second
		t.Logf("exchanges=%d captures=%v first=%d second=%d", harness.transport.exchangeCount(),
			harness.captures(), firstResult.SourceGeneration, secondResult.SourceGeneration)
		require.Equal(t, 2, harness.transport.exchangeCount(),
			"the waiter must re-query under the environment it woke up in")
		require.EqualValues(t, 1, firstResult.SourceGeneration)
		require.EqualValues(t, 2, secondResult.SourceGeneration,
			"the re-query was asked on generation 2 and must be stamped with it")
	})
}
