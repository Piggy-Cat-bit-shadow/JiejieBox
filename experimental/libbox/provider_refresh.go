package libbox

// PP03 — the manual refresh as the Apple/Android binding sees it.
//
// CommandServer embeds the daemon's StartedService, so the live box's refresh entry (PP01) is
// already reachable from here; what the platforms need is a gomobile-shaped method: a string
// argument, a millisecond budget, and a JSON result. The JSON is returned as *StringBox
// because the binding surface forbids a bare string result (TestGomobileMethodResultSurface).
//
// What this wrapper does NOT do is invent state: the report is the daemon's own summary, with
// per-provider statuses, revisions and pending tags, and an error when the box is not running.
// The socket/CLI command path (command_client) is generated from started_service.proto and is
// PP02's binding, not a second implementation here.

import (
	"context"
	stdjson "encoding/json"
	"strings"
	"time"

	"github.com/sagernet/sing-box/daemon"
	E "github.com/sagernet/sing/common/exceptions"
)

// RefreshProviders refreshes the running box's remote providers and returns the per-provider
// report as JSON.
//
// tags is a comma-separated selection; an empty string means every provider the box serves.
// timeoutMilliseconds bounds EACH provider; a non-positive value uses 30 seconds.
func (s *CommandServer) RefreshProviders(tags string, timeoutMilliseconds int64) (*StringBox, error) {
	if s.StartedService == nil {
		return nil, E.New("refresh providers: the service is not started")
	}
	summary, err := s.StartedService.RefreshProviders(context.Background(), splitRefreshSelection(tags), refreshTimeout(timeoutMilliseconds))
	if err != nil {
		return nil, E.Cause(err, "refresh providers")
	}
	encoded, err := formatRefreshSummary(summary)
	if err != nil {
		return nil, err
	}
	return wrapString(encoded), nil
}

// splitRefreshSelection turns the binding's comma-separated argument into tags.
func splitRefreshSelection(tags string) []string {
	selection := make([]string, 0)
	for _, tag := range strings.Split(tags, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		selection = append(selection, tag)
	}
	return selection
}

// refreshTimeout bounds each provider; a non-positive value uses the default budget.
func refreshTimeout(timeoutMilliseconds int64) time.Duration {
	if timeoutMilliseconds <= 0 {
		return 30 * time.Second
	}
	return time.Duration(timeoutMilliseconds) * time.Millisecond
}

// formatRefreshSummary serialises the report for the platform.
func formatRefreshSummary(summary *daemon.ProviderRefreshSummary) (string, error) {
	encoded, err := stdjson.Marshal(summary)
	if err != nil {
		return "", E.Cause(err, "encode provider refresh summary")
	}
	return string(encoded), nil
}
