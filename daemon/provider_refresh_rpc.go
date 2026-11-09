package daemon

// PP02 — the manual refresh on the daemon's gRPC surface.
//
// This is a thin mapping, deliberately: the kernel entry (PP01) owns the semantics, the report
// and the box identity, and this file only translates its summary into the protobuf shape the
// Windows daemon client consumes. Keeping it thin is what makes the RPC and the in-process
// callers describe the same behaviour.
//
// Compatibility: the method is additive. An older client never calls it, and the daemon does
// not change any existing message or method, so the old ABI keeps working.

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RefreshRemoteProviders runs a bounded manual refresh and returns the per-provider report.
//
// A request with no tags refreshes every provider the running box serves, in tag order. The
// per-provider timeout is request.TimeoutMs; a non-positive value keeps the kernel default.
func (s *StartedService) RefreshRemoteProviders(ctx context.Context, request *RefreshRemoteProvidersRequest) (*RemoteProviderRefreshResponse, error) {
	var timeout time.Duration
	if request != nil && request.TimeoutMs > 0 {
		timeout = time.Duration(request.TimeoutMs) * time.Millisecond
	}
	var tags []string
	if request != nil {
		tags = request.Tags
	}
	summary, err := s.RefreshProviders(ctx, tags, timeout)
	if err != nil {
		// The service is not running, or the request cannot be served at all: a status the
		// client can distinguish from a provider-level failure.
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	result := &RemoteProviderRefreshResponse{
		Cancelled:  summary.Cancelled,
		BoxChanged: summary.BoxChanged,
	}
	for _, item := range summary.Results {
		result.Results = append(result.Results, &RemoteProviderRefreshResult{
			Tag:               item.Tag,
			Status:            string(item.Status),
			RequestedRevision: item.RequestedRevision,
			AppliedRevision:   item.AppliedRevision,
			PendingTags:       item.PendingTags,
			RestartRequired:   item.RestartRequired,
			Reason:            item.Reason,
		})
	}
	return result, nil
}
