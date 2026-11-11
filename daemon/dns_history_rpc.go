package daemon

// The DNS history management surface on the daemon's gRPC service.
//
// This is the same contract the gomobile binding exposes, and deliberately the same DOCUMENT: the
// JSON carried by these responses is built by the dns package (`HistoryQueryView` and friends), so
// the hard cap, the clamping, the rune-safe field cut and the truncated/nextSequence pagination
// fields cannot differ between the two transports.
//
// The forwarding goes to the RUNNING instance's route router, read under serviceAccess like every
// other RPC. A history that is not enabled reports "not enabled" rather than an empty window.

import (
	"context"
	stdjson "encoding/json"
	"time"

	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/route"
	E "github.com/sagernet/sing/common/exceptions"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// dnsHistoryRouter returns the running instance's route router.
func (s *StartedService) dnsHistoryRouter() (*route.Router, error) {
	s.serviceAccess.RLock()
	boxService := s.instance
	s.serviceAccess.RUnlock()
	if boxService == nil {
		return nil, status.Error(codes.FailedPrecondition, "service is not running")
	}
	boxInstance := boxService.instance
	if boxInstance == nil {
		return nil, status.Error(codes.FailedPrecondition, "box is not running")
	}
	routeRouter, isRouteRouter := boxInstance.Router().(*route.Router)
	if !isRouteRouter || routeRouter == nil {
		return nil, status.Error(codes.FailedPrecondition, "the running box has no route router")
	}
	return routeRouter, nil
}

// DNSHistoryEnable attaches the bounded history, creating it on first use.
func (s *StartedService) DNSHistoryEnable(ctx context.Context, request *DNSHistoryEnableRequest) (*emptypb.Empty, error) {
	routeRouter, err := s.dnsHistoryRouter()
	if err != nil {
		return nil, err
	}
	historyOptions := dns.HistoryOptions{
		MaxEntries: int(request.MaxEntries),
		MaxBytes:   int(request.MaxBytes),
	}
	if request.TtlMs > 0 {
		historyOptions.TTL = timeMilliseconds(request.TtlMs)
	}
	if _, err = routeRouter.EnableDNSHistory(historyOptions); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &emptypb.Empty{}, nil
}

// DNSHistoryDisable detaches the subscription and drops the ring. Idempotent.
func (s *StartedService) DNSHistoryDisable(ctx context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	routeRouter, err := s.dnsHistoryRouter()
	if err != nil {
		return nil, err
	}
	if err = routeRouter.DisableDNSHistory(); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &emptypb.Empty{}, nil
}

// DNSHistoryQuery reads the live window within the encoded-response bounds.
func (s *StartedService) DNSHistoryQuery(ctx context.Context, request *DNSHistoryQueryRequest) (*DNSHistoryQueryResponse, error) {
	routeRouter, err := s.dnsHistoryRouter()
	if err != nil {
		return nil, err
	}
	if routeRouter.DNSHistory() == nil {
		return nil, status.Error(codes.FailedPrecondition, dns.ErrHistoryNotEnabled.Error())
	}
	records, err := routeRouter.QueryDNSHistory(dns.HistoryQuery{
		Since:     request.Since,
		Limit:     int(request.Limit),
		Transport: request.Transport,
		Source:    dns.QuerySource(request.Source),
		MaxBytes:  int(request.MaxBytes),
	})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	stats, dropped, err := routeRouter.DNSHistoryStats()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	view := dns.BuildHistoryQueryView(records, stats, dropped, int(request.MaxBytes))
	encoded, encodeErr := stdjson.Marshal(view)
	if encodeErr != nil {
		return nil, status.Error(codes.Internal, encodeErr.Error())
	}
	return &DNSHistoryQueryResponse{RecordsJson: string(encoded)}, nil
}

// DNSHistoryStats reports the accounting plus the bus-side drop count.
func (s *StartedService) DNSHistoryStats(ctx context.Context, _ *emptypb.Empty) (*DNSHistoryStatsResponse, error) {
	routeRouter, err := s.dnsHistoryRouter()
	if err != nil {
		return nil, err
	}
	stats, dropped, err := routeRouter.DNSHistoryStats()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	encoded, encodeErr := stdjson.Marshal(dns.BuildHistoryStatsView(stats, dropped))
	if encodeErr != nil {
		return nil, status.Error(codes.Internal, encodeErr.Error())
	}
	return &DNSHistoryStatsResponse{StatsJson: string(encoded)}, nil
}

// DNSHistoryTopK aggregates the retained records by transport or source.
func (s *StartedService) DNSHistoryTopK(ctx context.Context, request *DNSHistoryTopKRequest) (*DNSHistoryTopKResponse, error) {
	routeRouter, err := s.dnsHistoryRouter()
	if err != nil {
		return nil, err
	}
	topField := dns.TopField(request.Field)
	switch topField {
	case dns.TopFieldTransport, dns.TopFieldSource:
	default:
		return nil, status.Error(codes.InvalidArgument, E.New("dns history: unknown top-k field: ", request.Field).Error())
	}
	entries, err := routeRouter.DNSHistoryTopK(topField, int(request.Limit))
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	encoded, encodeErr := stdjson.Marshal(dns.BuildHistoryTopKView(topField, entries))
	if encodeErr != nil {
		return nil, status.Error(codes.Internal, encodeErr.Error())
	}
	return &DNSHistoryTopKResponse{EntriesJson: string(encoded)}, nil
}

// DNSHistoryClear empties the window and reports how many records it dropped.
func (s *StartedService) DNSHistoryClear(ctx context.Context, _ *emptypb.Empty) (*DNSHistoryClearResponse, error) {
	routeRouter, err := s.dnsHistoryRouter()
	if err != nil {
		return nil, err
	}
	cleared, err := routeRouter.ClearDNSHistory()
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &DNSHistoryClearResponse{Cleared: int32(cleared)}, nil
}

// timeMilliseconds converts a millisecond count to a duration.
func timeMilliseconds(milliseconds int64) time.Duration {
	return time.Duration(milliseconds) * time.Millisecond
}
