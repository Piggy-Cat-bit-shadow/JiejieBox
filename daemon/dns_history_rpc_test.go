package daemon

// The DNS history RPCs against a REAL running Box.
//
// The point of these tests is the forwarding, not the ring (that is covered in dns and libbox): a
// daemon-mode platform must reach the same document the gomobile binding returns, from the running
// instance, and must be told "not enabled" rather than handed an empty window.

import (
	"context"
	stdjson "encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/route"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type dnsHistoryRPCResult struct {
	Records []struct {
		Sequence uint64 `json:"sequence"`
		Name     string `json:"name"`
		Rule     string `json:"rule"`
	} `json:"records"`
	Stats struct {
		Entries  int    `json:"entries"`
		Bytes    int    `json:"bytes"`
		Rejected uint64 `json:"rejected"`
	} `json:"stats"`
	Truncated    bool   `json:"truncated"`
	NextSequence uint64 `json:"nextSequence"`
}

// dnsHistoryServiceConfig is the smallest profile that produces a blocked DNS event: the reject
// action answers locally, so the configured transport is never dialled.
func dnsHistoryServiceConfig(extraKeyword string) string {
	rules := `{"domain_keyword":["history","` + extraKeyword + `"],"action":"reject"}`
	return `{"log":{"level":"error"},
		"dns":{"servers":[{"type":"local","tag":"local"}],"final":"local","rules":[` + rules + `]}}`
}

func startDNSHistoryService(t *testing.T, config string) *StartedService {
	t.Helper()
	service := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	t.Cleanup(service.Close)
	require.NoError(t, service.StartOrReloadService(context.Background(), config, nil))
	require.NotNil(t, service.Instance())
	return service
}

func lookupBlockedServiceName(t *testing.T, service *StartedService, name string) {
	t.Helper()
	boxInstance := service.Instance().Box()
	require.NotNil(t, boxInstance)
	routeRouter, isRouteRouter := boxInstance.Router().(*route.Router)
	require.True(t, isRouteRouter)
	_, _ = routeRouter.LookupDomain(context.Background(), name, adapter.DNSQueryOptions{})
}

func TestTheDaemonDNSHistoryRPCsReadTheRunningBox(t *testing.T) {
	service := startDNSHistoryService(t, dnsHistoryServiceConfig(strings.Repeat("a", 40_000)))
	ctx := context.Background()

	_, err := service.DNSHistoryEnable(ctx, &DNSHistoryEnableRequest{MaxEntries: 16, MaxBytes: 4096, TtlMs: 60_000})
	require.NoError(t, err)
	lookupBlockedServiceName(t, service, "history.test")

	var result dnsHistoryRPCResult
	require.Eventually(t, func() bool {
		response, queryErr := service.DNSHistoryQuery(ctx, &DNSHistoryQueryRequest{MaxBytes: 4096})
		if queryErr != nil {
			return false
		}
		if unmarshalErr := stdjson.Unmarshal([]byte(response.RecordsJson), &result); unmarshalErr != nil {
			return false
		}
		return result.Stats.Rejected >= 1
	}, 5*time.Second, time.Millisecond, "the rejected event is counted")

	require.Empty(t, result.Records, "a 40,000-character rule cannot be retained in a 4 KiB ring")
	require.GreaterOrEqual(t, result.Stats.Rejected, uint64(1))

	response, err := service.DNSHistoryQuery(ctx, &DNSHistoryQueryRequest{MaxBytes: 4096})
	require.NoError(t, err)
	require.LessOrEqual(t, len(response.RecordsJson), 8192,
		"the RPC carries the same bounded document the gomobile binding returns")

	statsResponse, err := service.DNSHistoryStats(ctx, nil)
	require.NoError(t, err)
	var statsResult struct {
		Stats struct {
			Rejected uint64 `json:"rejected"`
		} `json:"stats"`
	}
	require.NoError(t, stdjson.Unmarshal([]byte(statsResponse.StatsJson), &statsResult))
	require.GreaterOrEqual(t, statsResult.Stats.Rejected, uint64(1),
		"the accounting reaches the platform over the RPC, not only over gomobile")

	clearResponse, err := service.DNSHistoryClear(ctx, nil)
	require.NoError(t, err)
	require.GreaterOrEqual(t, clearResponse.Cleared, int32(0))

	_, err = service.DNSHistoryDisable(ctx, nil)
	require.NoError(t, err)
	_, err = service.DNSHistoryQuery(ctx, &DNSHistoryQueryRequest{})
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err),
		"a disabled history is reported, never presented as an empty window")
}

func TestTheDaemonDNSHistoryRPCBoundsARetainedLongRule(t *testing.T) {
	service := startDNSHistoryService(t, dnsHistoryServiceConfig(strings.Repeat("b", 40_000)))
	ctx := context.Background()

	_, err := service.DNSHistoryEnable(ctx, &DNSHistoryEnableRequest{MaxEntries: 16, MaxBytes: 1 << 20, TtlMs: 60_000})
	require.NoError(t, err)
	lookupBlockedServiceName(t, service, "history.test")

	var result dnsHistoryRPCResult
	require.Eventually(t, func() bool {
		response, queryErr := service.DNSHistoryQuery(ctx, &DNSHistoryQueryRequest{MaxBytes: 4096})
		if queryErr != nil {
			return false
		}
		if unmarshalErr := stdjson.Unmarshal([]byte(response.RecordsJson), &result); unmarshalErr != nil {
			return false
		}
		return len(result.Records) >= 1
	}, 5*time.Second, time.Millisecond, "the long-rule event is retained")

	response, err := service.DNSHistoryQuery(ctx, &DNSHistoryQueryRequest{MaxBytes: 4096})
	require.NoError(t, err)
	require.LessOrEqual(t, len(response.RecordsJson), 8192)
	require.True(t, result.Truncated)
	require.NotZero(t, result.NextSequence)
	require.LessOrEqual(t, len(result.Records[0].Rule), 4104)
	require.True(t, strings.HasSuffix(result.Records[0].Rule, "…"))

	// The RPC carries the same effective-budget contract as the gomobile binding: 1 KiB (the floor),
	// the default and a request above the ceiling all stay within what was applied.
	for _, testCase := range []struct {
		id     string
		budget int32
		limit  int
	}{
		{"floor", 1024, 1024},
		{"default", 0, 65536},
		{"above-ceiling", 100_000_000, 65536},
	} {
		floorResponse, queryErr := service.DNSHistoryQuery(ctx, &DNSHistoryQueryRequest{MaxBytes: testCase.budget})
		require.NoError(t, queryErr)
		require.LessOrEqual(t, len(floorResponse.RecordsJson), testCase.limit,
			"the %s request must fit the applied budget", testCase.id)
		var floorResult dnsHistoryRPCResult
		require.NoError(t, stdjson.Unmarshal([]byte(floorResponse.RecordsJson), &floorResult))
		require.NotEmpty(t, floorResult.Records)
		require.NotZero(t, floorResult.NextSequence)
	}

	topK, err := service.DNSHistoryTopK(ctx, &DNSHistoryTopKRequest{Field: "source", Limit: 4})
	require.NoError(t, err)
	require.Contains(t, topK.EntriesJson, `"field":"source"`)

	_, err = service.DNSHistoryTopK(ctx, &DNSHistoryTopKRequest{Field: "bogus"})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestTheDaemonDNSHistoryRPCsRequireARunningService(t *testing.T) {
	service := &StartedService{}

	_, err := service.DNSHistoryQuery(context.Background(), &DNSHistoryQueryRequest{})
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}
