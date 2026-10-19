package rule

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// TestRemoteRuleSetStreamsBinaryWithoutCache pins the load's peak, not its live set.
//
// A cached profile must retain the download because `CacheFile.SaveRuleSet` stores the bytes. A
// cache-less profile does not: `srs.Read` is a streaming parser, and the parsed form is compact, so
// reading the body into a []byte first adds one temporary copy of the whole file to the peak.
//
// The tail makes that copy observable. It is not part of the zlib stream, so a streaming parse
// never reads it, while io.ReadAll must. The assertion is on the load's own allocation, which is
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
	t.Logf("binary load with a %d-byte tail allocated %d bytes (no cached copy)", tailSize, allocated)
	require.Less(t, allocated, uint64(tailSize/2),
		"the %d-byte tail must not be read into the heap: the binary load has no cache to write, "+
			"so it must parse the response stream", tailSize)

	metadata := &adapter.InboundContext{Destination: M.ParseSocksaddr("10.1.2.3:443")}
	require.True(t, ruleSet.Match(metadata), "the streamed rules must be live, not just parsed")
}
