package rule

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// writeRuleSetPayload builds a valid binary rule set with one IP-CIDR rule.
func writeRuleSetPayload(t *testing.T, prefix string) []byte {
	t.Helper()
	var payload bytes.Buffer
	err := srs.Write(&payload, option.PlainRuleSet{
		Rules: []option.HeadlessRule{{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultHeadlessRule{
				IPCIDR: badoption.Listable[*badoption.Prefixable]{
					common.Ptr(badoption.Prefixable(netip.MustParsePrefix(prefix))),
				},
			},
		}},
	}, C.RuleSetVersionCurrent)
	require.NoError(t, err)
	return payload.Bytes()
}

func remoteRuleSetMatches(t *testing.T, ruleSet *RemoteRuleSet, address string) bool {
	t.Helper()
	return ruleSet.Match(&adapter.InboundContext{Destination: M.ParseSocksaddr(address + ":443")})
}

// TestRemoteRuleSetStreamsBinaryWithoutCache pins the load's peak, not its live set.
//
// A cached profile must retain the download because `CacheFile.SaveRuleSet` stores the bytes. A
// cache-less profile does not: `srs.Read` is a streaming parser, and the parsed form is compact, so
// reading the body into a []byte first adds one temporary copy of the whole file to the peak.
//
// The tail makes that copy observable. It is not part of the zlib stream, so the parser itself
// never needs it; the post-parse drain copies it through one small buffer, while io.ReadAll had to
// hold the whole body in a slice. The assertion is on the load's own allocation, which is
// deterministic because total allocation is monotonic.
func TestRemoteRuleSetStreamsBinaryWithoutCache(t *testing.T) {
	payload := newRemoteRuleSetPayload(t)
	const tailSize = 4 << 20
	// Allocated before the measurement: the handler is part of the process under test.
	tail := make([]byte, tailSize)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(payload)+len(tail)))
		_, _ = writer.Write(payload)
		_, _ = writer.Write(tail)
	}))
	t.Cleanup(server.Close)

	ruleSet, err := NewRemoteRuleSet(context.Background(), log.NewNOPFactory().Logger(), "stream-test", option.RuleSet{
		Type:   C.RuleSetTypeRemote,
		Format: C.RuleSetFormatBinary,
		RemoteOptions: option.RemoteRuleSet{
			URL: server.URL,
		},
	})
	require.NoError(t, err)
	ruleSet.httpClient = server.Client()

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	require.NoError(t, ruleSet.fetch(context.Background(), true))
	runtime.ReadMemStats(&after)

	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("binary load with a %d-byte tail allocated %d bytes (streamed, not buffered)", tailSize, allocated)
	require.Less(t, allocated, uint64(tailSize/2),
		"the %d-byte tail must not be copied into a full-body slice: the binary load has no cache "+
			"to write, so it must parse the response stream", tailSize)

	metadata := &adapter.InboundContext{Destination: M.ParseSocksaddr("10.1.2.3:443")}
	require.True(t, ruleSet.Match(metadata), "the streamed rules must be live, not just parsed")
}

// TestRemoteRuleSetStreamAbortsWhenTheRequestIsCanceled covers the cancellation edge the read-all
// path had for free: the server sends half a rule set and waits, so srs.Read is blocked when the
// caller cancels. The read must fail, the old generation must stay, and nothing may be published.
func TestRemoteRuleSetStreamAbortsWhenTheRequestIsCanceled(t *testing.T) {
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = writer.Write(payload[:len(payload)/2])
		if flusher, isFlusher := writer.(http.Flusher); isFlusher {
			flusher.Flush()
		}
		close(started)
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	ruleSet, err := NewRemoteRuleSet(context.Background(), log.NewNOPFactory().Logger(), "stream-cancel-test", option.RuleSet{
		Type:   C.RuleSetTypeRemote,
		Format: C.RuleSetFormatBinary,
		RemoteOptions: option.RemoteRuleSet{
			URL: server.URL,
		},
	})
	require.NoError(t, err)
	ruleSet.httpClient = server.Client()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- ruleSet.fetch(ctx, true)
	}()

	<-started
	cancel()
	select {
	case err = <-result:
		require.Error(t, err)
		t.Logf("canceled refresh error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("fetch did not return after the request was canceled")
	}
	require.False(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"),
		"a canceled stream must not publish a partial rule set")
}

// is not a binary rule set must fail in srs.Read and publish nothing. The empty body is the
// truncation-at-byte-zero case; the garbage body is a wrong-format download.
// TestRemoteRuleSetStreamRejectsUnparseableBodies covers the branch before the drain: a body that
// is not a binary rule set must fail in srs.Read and publish nothing. The empty body is the
// truncation-at-byte-zero case; the garbage body is a wrong-format download.
func TestRemoteRuleSetStreamRejectsUnparseableBodies(t *testing.T) {
	for name, body := range map[string][]byte{
		"empty":   nil,
		"garbage": []byte("this is not an SRS file"),
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
				_, _ = writer.Write(body)
			}))
			t.Cleanup(server.Close)

			ruleSet, err := NewRemoteRuleSet(context.Background(), log.NewNOPFactory().Logger(), "stream-"+name, option.RuleSet{
				Type:   C.RuleSetTypeRemote,
				Format: C.RuleSetFormatBinary,
				RemoteOptions: option.RemoteRuleSet{
					URL: server.URL,
				},
			})
			require.NoError(t, err)
			ruleSet.httpClient = server.Client()

			err = ruleSet.fetch(context.Background(), true)
			require.Error(t, err)
			require.False(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
		})
	}
}

// TestRemoteRuleSetStreamDoesNotPublishATruncatedBody pins the failure semantics the streaming
// path could have silently changed.
//
// `srs.Read` returns as soon as the declared rules are parsed; the zlib footer and whatever follows
// are still unread. The old read-all path consumed the body to EOF, so a download truncated after
// the last rule failed the update. A streaming path that publishes immediately would accept it.
//
// The second generation matches a different prefix, so "the old generation stayed live" is
// observable rather than inferred from an error string alone.
func TestRemoteRuleSetStreamDoesNotPublishATruncatedBody(t *testing.T) {
	firstPayload := writeRuleSetPayload(t, "10.0.0.0/8")
	secondPayload := writeRuleSetPayload(t, "192.168.0.0/16")
	var truncated atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if truncated.Load() {
			// Claim more than is sent: the rules parse, but the stream is short.
			writer.Header().Set("Content-Length", strconv.Itoa(len(secondPayload)+1024))
			_, _ = writer.Write(secondPayload)
			return
		}
		writer.Header().Set("Content-Length", strconv.Itoa(len(firstPayload)))
		_, _ = writer.Write(firstPayload)
	}))
	t.Cleanup(server.Close)

	ruleSet, err := NewRemoteRuleSet(context.Background(), log.NewNOPFactory().Logger(), "stream-truncated-test", option.RuleSet{
		Type:   C.RuleSetTypeRemote,
		Format: C.RuleSetFormatBinary,
		RemoteOptions: option.RemoteRuleSet{
			URL: server.URL,
		},
	})
	require.NoError(t, err)
	ruleSet.httpClient = server.Client()

	require.NoError(t, ruleSet.fetch(context.Background(), true))
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))

	truncated.Store(true)
	err = ruleSet.fetch(context.Background(), true)
	require.Error(t, err, "a body truncated after the rules must fail the update, as it did when read to EOF")
	t.Logf("truncated refresh error: %v", err)

	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"),
		"the previous generation must stay live after a failed refresh")
	require.False(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"),
		"the truncated generation must not be published")
}

// TestRemoteRuleSetStreamsOverHTTP2 runs the streaming load over a real TLS/HTTP2 server.
//
// The response body contract is shared with HTTP/1.1, but the transport is not: an H2 body is a
// stream that can be reset independently of the connection. The parse and the post-parse drain must
// complete over that body, and the handler asserts the request was actually HTTP/2 so a silent
// fallback to HTTP/1.1 cannot make this test pass without exercising the intended path.
func TestRemoteRuleSetStreamsOverHTTP2(t *testing.T) {
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	var servedHTTP2 atomic.Bool
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		servedHTTP2.Store(request.ProtoMajor == 2)
		writer.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = writer.Write(payload)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	ruleSet, err := NewRemoteRuleSet(context.Background(), log.NewNOPFactory().Logger(), "stream-h2-test", option.RuleSet{
		Type:   C.RuleSetTypeRemote,
		Format: C.RuleSetFormatBinary,
		RemoteOptions: option.RemoteRuleSet{
			URL: server.URL,
		},
	})
	require.NoError(t, err)
	client := server.Client()
	transport, isTransport := client.Transport.(*http.Transport)
	require.True(t, isTransport)
	transport.ForceAttemptHTTP2 = true
	ruleSet.httpClient = client

	require.NoError(t, ruleSet.fetch(context.Background(), true))
	require.True(t, servedHTTP2.Load(), "the fixture must exercise HTTP/2, not fall back to HTTP/1.1")
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
}
