//go:build with_quic

package httpclient

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The race's lifetime edges beyond "one winner": the winner's response may arrive in segments long
// after RoundTrip returned, the cancelled loser's request body must still be released, and a
// cancellation caused by the race itself must not be remembered as a transport failure.

// TestH3RaceWinnerResponseIsReadSegmented: the 200 arrives first, the body streams in two chunks
// with a pause; the caller reads the whole payload from the winning response.
func TestH3RaceWinnerResponseIsReadSegmented(t *testing.T) {
	h3Server := startRetrySafetyH3Server(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		_, _ = io.WriteString(writer, "first-")
		writer.(http.Flusher).Flush()
		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(writer, "second")
	})
	_, h2Transport := startRetrySafetyH2Server(t, okHandler)
	transport := newRetrySafetyTransport(h2Transport, dialRetrySafetyH3(h3Server.address), time.Hour)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	request, err := http.NewRequest(http.MethodGet, "https://"+h3Server.address+"/segmented", nil)
	require.NoError(t, err)
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, "HTTP/3.0", response.Proto)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "first-second", string(body),
		"a body that continues after the headers must be read to its end")
}

// TestRaceLoserRequestBodiesAreClosed: both legs are dispatched (zero fallback delay), H3 wins,
// and the cancelled H2 leg's request body is released even though its transport never finished.
func TestRaceLoserRequestBodiesAreClosed(t *testing.T) {
	winnerBody := &auditTrackedBody{Reader: strings.NewReader("payload")}
	loserBody := &auditTrackedBody{Reader: strings.NewReader("payload")}
	h3Server := startRetrySafetyH3Server(t, okHandler)
	_, h2Transport := startRetrySafetyH2Server(t, func(writer http.ResponseWriter, request *http.Request) {
		// Slow enough that H3 always wins, but the H2 leg has definitely started.
		time.Sleep(100 * time.Millisecond)
		okHandler(writer, request)
	})
	transport := newRetrySafetyTransport(h2Transport, dialRetrySafetyH3(h3Server.address), 0)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	clones := 0
	request, err := http.NewRequest(http.MethodPut, "https://"+h3Server.address+"/loser-body", strings.NewReader("payload"))
	require.NoError(t, err)
	request.Header.Set("Idempotency-Key", "race-loser-body")
	request.GetBody = func() (io.ReadCloser, error) {
		clones++
		if clones == 1 {
			return winnerBody, nil
		}
		return loserBody, nil
	}
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, "HTTP/3.0", response.Proto)
	require.Equal(t, 2, clones, "both legs must have been dispatched for this case")

	require.Eventually(t, func() bool { return winnerBody.closes.Load() == 1 }, time.Second, time.Millisecond,
		"the winner's request body must be released once its upload ends")
	require.Eventually(t, func() bool { return loserBody.closes.Load() == 1 }, time.Second, time.Millisecond,
		"the cancelled loser's request body must be released")
}

// TestRaceLoserCancellationDoesNotArmTheVerdict: the H3 leg is cancelled BY THE RACE, not by the
// peer. That cancellation must not be remembered as "HTTP/3 is broken here".
func TestRaceLoserCancellationDoesNotArmTheVerdict(t *testing.T) {
	h3Server := startRetrySafetyH3Server(t, func(writer http.ResponseWriter, request *http.Request) {
		// Never answer: the race will cancel this leg when HTTP/2 wins.
		<-request.Context().Done()
	})
	_, h2Transport := startRetrySafetyH2Server(t, okHandler)
	transport := newRetrySafetyTransport(h2Transport, dialRetrySafetyH3(h3Server.address), 0)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	request, err := http.NewRequest(http.MethodGet, "https://"+h3Server.address+"/h2-wins", nil)
	require.NoError(t, err)
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, "HTTP/2.0", response.Proto)

	transport.verdictAccess.Lock()
	broken := len(transport.broken)
	transport.verdictAccess.Unlock()
	require.Zero(t, broken,
		"a leg cancelled by the race must not arm the HTTP/3 verdict")
}
