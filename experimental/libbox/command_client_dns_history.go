package libbox

// The DNS history management surface in DAEMON mode.
//
// The platform calls the same method names it calls on the command server; here they travel to the
// daemon's gRPC service, which forwards to the running Box's route router. The response document is
// the one the dns package builds, so the two transports return the same JSON - including the
// truncated/nextSequence pagination contract.

import (
	"context"

	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing-box/dns"
	E "github.com/sagernet/sing/common/exceptions"

	"google.golang.org/protobuf/types/known/emptypb"
)

// DNSHistoryEnable attaches the bounded history, creating it on first use.
func (c *CommandClient) DNSHistoryEnable(optionsJSON string) (*StringBox, error) {
	options, err := parseDNSHistoryEnableOptions(optionsJSON)
	if err != nil {
		return nil, err
	}
	_, err = callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.DNSHistoryEnable(ctx, &daemon.DNSHistoryEnableRequest{
			MaxEntries: options.MaxEntries,
			MaxBytes:   int64(options.MaxBytes),
			TtlMs:      options.TTLMilliseconds,
		})
	})
	if err != nil {
		return nil, E.Cause(err, "enable dns history")
	}
	return wrapString(marshalBindingJSON(dns.HistoryEnableView{Enabled: true})), nil
}

// DNSHistoryDisable detaches the subscription and drops the ring. Idempotent.
func (c *CommandClient) DNSHistoryDisable() error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.DNSHistoryDisable(ctx, &emptypb.Empty{})
	})
	if err != nil {
		return E.Cause(err, "disable dns history")
	}
	return nil
}

// DNSHistoryQuery reads the live window.
func (c *CommandClient) DNSHistoryQuery(queryJSON string) (*StringBox, error) {
	query, err := parseDNSHistoryQueryOptions(queryJSON)
	if err != nil {
		return nil, err
	}
	response, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*daemon.DNSHistoryQueryResponse, error) {
		return client.DNSHistoryQuery(ctx, &daemon.DNSHistoryQueryRequest{
			Since:     query.Since,
			Limit:     query.Limit,
			Transport: query.Transport,
			Source:    query.Source,
			MaxBytes:  query.MaxBytes,
		})
	})
	if err != nil {
		return nil, E.Cause(err, "query dns history")
	}
	return wrapString(response.RecordsJson), nil
}

// DNSHistoryStats reports the history's accounting plus the bus-side drop count.
func (c *CommandClient) DNSHistoryStats() (*StringBox, error) {
	response, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*daemon.DNSHistoryStatsResponse, error) {
		return client.DNSHistoryStats(ctx, &emptypb.Empty{})
	})
	if err != nil {
		return nil, E.Cause(err, "dns history stats")
	}
	return wrapString(response.StatsJson), nil
}

// DNSHistoryTopK aggregates the retained records by transport or source.
func (c *CommandClient) DNSHistoryTopK(field string, limit int32) (*StringBox, error) {
	response, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*daemon.DNSHistoryTopKResponse, error) {
		return client.DNSHistoryTopK(ctx, &daemon.DNSHistoryTopKRequest{Field: field, Limit: limit})
	})
	if err != nil {
		return nil, E.Cause(err, "dns history top-k")
	}
	return wrapString(response.EntriesJson), nil
}

// DNSHistoryClear empties the window and reports how many records it dropped.
func (c *CommandClient) DNSHistoryClear() (*StringBox, error) {
	response, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*daemon.DNSHistoryClearResponse, error) {
		return client.DNSHistoryClear(ctx, &emptypb.Empty{})
	})
	if err != nil {
		return nil, E.Cause(err, "clear dns history")
	}
	return wrapString(marshalBindingJSON(dns.HistoryClearView{Cleared: int(response.Cleared)})), nil
}
