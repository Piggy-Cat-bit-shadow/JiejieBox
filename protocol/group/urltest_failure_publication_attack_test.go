package group

// A probe that CONNECTS but gets the wrong status is a failure the client must show as a timeout.
// The measurement engine returns an EMPTY Measurement for that error path (it never produced a
// result), which is exactly the shape that can silently lose the evidence stamps a display write is
// authorized with. This test drives the real round against a real local server that answers 500 where
// 204 was required, in BOTH modes, and requires the display terminal state to appear.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// wrongAnswerServer answers every probe with a status the configuration does not accept.
func wrongAnswerServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(delay)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestAStatusMismatchIsATimeoutInBothModes(t *testing.T) {
	for _, testCase := range []struct {
		name string
		mode TestHistoryMode
		link func(server *httptest.Server) string
		// expected is what the round must accept; the server answers 500 so the probe fails.
		expected urltest.ExpectedStatus
	}{
		{
			name:     "health mode, explicit expected status",
			mode:     TestHistoryHealth,
			link:     func(server *httptest.Server) string { return server.URL + "/generate_204" },
			expected: urltest.ExpectedStatus{{Start: http.StatusNoContent, End: http.StatusNoContent}},
		},
		{
			name:     "display mode, explicit expected status",
			mode:     TestHistoryDisplayOnly,
			link:     func(server *httptest.Server) string { return server.URL + "/generate_204" },
			expected: urltest.ExpectedStatus{{Start: http.StatusNoContent, End: http.StatusNoContent}},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for _, delay := range []time.Duration{0, 700 * time.Millisecond} {
				server := wrongAnswerServer(t, delay)
				link := testCase.link(server)
				history := urltest.NewHistoryStorage()
				t.Cleanup(func() { _ = history.Close() })

				healthy := newSharingProbeOutbound("member")
				close(healthy.gate)
				manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{"member": healthy}}

				URLTestOutboundsWithTarget(context.Background(), manager, history,
					log.NewNOPFactory().NewLogger("status-mismatch"),
					[]adapter.Outbound{healthy}, link, testCase.expected, 0, true, testCase.mode)

				result, loaded := history.LoadDisplayResult("member")
				require.True(t, loaded,
					"delay %v: a probe that answered 500 where 204 was required must produce a display terminal state", delay)
				require.True(t, result.Failed())
				require.False(t, result.CompletedAt.IsZero())
			}
		})
	}
}

// TestADialFailureIsATimeoutInBothModes is the same question for the path where the node's own
// connection fails: the error arrives before any HTTP exchange exists.
func TestADialFailureIsATimeoutInBothModes(t *testing.T) {
	for _, mode := range []TestHistoryMode{TestHistoryHealth, TestHistoryDisplayOnly} {
		server := newProbeTarget(t)
		link := server.URL + "/generate_204"
		history := urltest.NewHistoryStorage()
		t.Cleanup(func() { _ = history.Close() })

		refusing := &refusingProbeOutbound{tag: "member"}
		manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{"member": refusing}}

		URLTestOutboundsWithTarget(context.Background(), manager, history,
			log.NewNOPFactory().NewLogger("dial-failure"),
			[]adapter.Outbound{refusing}, link, urltest.ExpectedStatus{}, 0, true, mode)

		result, loaded := history.LoadDisplayResult("member")
		require.True(t, loaded, "mode %v: a refused connection must produce a display terminal state", mode)
		require.True(t, result.Failed())
	}
}

var _ = option.URLTestOutboundOptions{}
