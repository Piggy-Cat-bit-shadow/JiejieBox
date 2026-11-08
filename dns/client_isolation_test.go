package dns

// D02 — what a shared lookup is allowed to share.
//
// The shared query key is (question, transport tag, client subnet, environment,
// timeout). Anything outside the key must produce its OWN upstream query: a
// caller asking a different question, or asking through a different member, has
// not asked the same thing, and handing it the leader's answer would be a wrong
// answer rather than a saved query.
//
// These tests pin that from the outside: one held leader is enough to make the
// difference observable, because a caller that (wrongly) joined the leader's
// flight would block until the leader is released, while an isolated caller runs
// its own exchange and returns.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mDNS "github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
)

// taggedHerdTransport reports a different Tag while sharing the same fake
// upstream, so the query counter still shows how many upstream exchanges happened.
type taggedHerdTransport struct {
	*herdTransport
	tag string
}

func (t *taggedHerdTransport) Tag() string { return t.tag }

// questionWith retargets the shared herd message at another name.
func questionWith(name string) *mDNS.Msg {
	message := herdMessage()
	message.Question[0].Name = name
	return message
}

// startHeldLeader starts the first exchange and returns once it is provably
// inside the fake upstream (holding the leader).
func startHeldLeader(t *testing.T, client *Client, transport adapter.DNSTransport, message *mDNS.Msg, upstream *herdTransport) <-chan error {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.Exchange(ctx, transport, message, adapter.DNSQueryOptions{}, nil)
		result <- err
	}()
	require.Eventually(t, func() bool { return upstream.queryCount.Load() == 1 }, 2*time.Second, time.Millisecond,
		"the leader never reached the upstream")
	return result
}

// exchangeNow runs one exchange and requires it to return without waiting for the
// held leader: a shared flight would block until the leader is released.
func exchangeNow(t *testing.T, client *Client, transport adapter.DNSTransport, message *mDNS.Msg) {
	t.Helper()
	type outcome struct {
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := client.Exchange(ctx, transport, message, adapter.DNSQueryOptions{}, nil)
		done <- outcome{err: err}
	}()
	select {
	case got := <-done:
		require.NoError(t, got.err, "the isolated exchange must succeed on its own")
	case <-time.After(2 * time.Second):
		t.Fatal("the exchange waited for the held leader: the two lookups were merged although they are not the same query")
	}
}

// TestASingleQuestionIsShared is the control: the same question through the same
// member does merge, so the isolation cases below are not proving that nothing
// ever merges.
func TestASingleQuestionIsShared(t *testing.T) {
	upstream := &herdTransport{holdFirst: make(chan struct{})}
	client := newHerdClient(t, upstream)

	leader := startHeldLeader(t, client, upstream, herdMessage(), upstream)

	waiterDone := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = client.Exchange(ctx, upstream, herdMessage(), adapter.DNSQueryOptions{}, nil)
		close(waiterDone)
	}()
	// Give the waiter time to join the leader's flight; it must not query on its own.
	time.Sleep(100 * time.Millisecond)
	require.EqualValues(t, 1, upstream.queryCount.Load(),
		"a second caller for the same question must join the leader's flight instead of querying")

	close(upstream.holdFirst)
	require.NoError(t, <-leader)
	select {
	case <-waiterDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the joined waiter never finished")
	}
	require.EqualValues(t, 1, upstream.queryCount.Load())
}

// TestADifferentQuestionIsItsOwnQuery: a different name is a different question.
func TestADifferentQuestionIsItsOwnQuery(t *testing.T) {
	upstream := &herdTransport{holdFirst: make(chan struct{})}
	client := newHerdClient(t, upstream)

	leader := startHeldLeader(t, client, upstream, herdMessage(), upstream)
	exchangeNow(t, client, upstream, questionWith("other.example.org."))
	require.EqualValues(t, 2, upstream.queryCount.Load(),
		"a different name must be its own upstream query")

	close(upstream.holdFirst)
	require.NoError(t, <-leader)
}

// TestADifferentMemberIsItsOwnQuery: the same question through two different
// resolvers is not one question. The transport tag is part of the shared key
// because two members are two upstreams and their answers are not interchangeable.
func TestADifferentMemberIsItsOwnQuery(t *testing.T) {
	upstream := &herdTransport{holdFirst: make(chan struct{})}
	client := newHerdClient(t, upstream)
	memberOne := &taggedHerdTransport{herdTransport: upstream, tag: "one"}
	memberTwo := &taggedHerdTransport{herdTransport: upstream, tag: "two"}

	leader := startHeldLeader(t, client, memberOne, herdMessage(), upstream)
	exchangeNow(t, client, memberTwo, herdMessage())
	require.EqualValues(t, 2, upstream.queryCount.Load(),
		"the same question through a different member must be its own upstream query")

	close(upstream.holdFirst)
	require.NoError(t, <-leader)
}
