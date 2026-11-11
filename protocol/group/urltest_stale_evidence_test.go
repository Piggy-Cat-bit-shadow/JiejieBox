package group

// The three states of "a late result meets the evidence layer".
//
// `TestASlowerPreChangeAttemptCannotOverwriteTheNewerRoundsResult` covers state 2: a newer entry
// already exists, so the older attempt loses. The other two states were unverified, and one of them
// was a real gap:
//
//   - state 1, NEW GENERATION HAS NO ENTRY: with nothing newer stored, a late result is the only
//     evidence there is. It is kept (pinned below); the next round's attempt overwrites it, and the
//     attempt-start ordering keeps that overwrite safe.
//   - state 3, EVIDENCE DROPPED (reset): a node retirement or a failed path calls
//     DeleteURLTestHistory, which is a drop, not a failure of one attempt. A probe that started
//     BEFORE the drop and finished after it used to store its result and recreate the entry the
//     drop had just removed - stale evidence back in the layer selection reads. The storage now
//     carries an epoch, the attempt captures it, and the guarded store refuses a stale epoch.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

func slowProbeTarget(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(120 * time.Millisecond)
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return server
}

func fastProbeTarget(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return server
}

// TestALateSuccessAfterAnEvidenceDropDoesNotRecreateTheEntry is state 3.
func TestALateSuccessAfterAnEvidenceDropDoesNotRecreateTheEntry(t *testing.T) {
	link := slowProbeTarget(t).URL + "/generate_204"
	history := urltest.NewHistoryStorage()
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{}}

	stale := newHeldProbeOutbound("node-a", slowProbeTarget(t).Listener.Addr().String())
	round := make(chan map[string]uint16, 1)
	go func() {
		round <- URLTestOutboundsWithTarget(context.Background(), manager, history,
			log.NewNOPFactory().NewLogger("stale-drop"), []adapter.Outbound{stale}, link,
			urltest.ExpectedStatus{}, 0, true, TestHistoryHealth)
	}()
	select {
	case <-stale.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the stale attempt never dialled")
	}

	// The node is retired (or its path failed) while the probe is still dialling. This is a DROP:
	// whatever the in-flight probe eventually finds describes a member that was just forgotten.
	history.DeleteURLTestHistory("node-a")
	require.Zero(t, history.HealthEntryCount())
	require.Zero(t, history.DisplayEntryCount())

	stale.release()
	select {
	case <-round:
	case <-time.After(10 * time.Second):
		t.Fatal("the stale attempt never finished")
	}

	require.Zero(t, history.HealthEntryCount(),
		"a result from before the drop must not recreate the health entry the drop removed")
	require.Zero(t, history.DisplayEntryCount(),
		"and it must not recreate the display entry either")
	require.Nil(t, history.LoadURLTestHistory("node-a"))
}

// TestALateSuccessWithNoDropIsTheOnlyEvidence is state 1: nothing newer exists, no drop happened,
// so the late result is the only evidence there is. The next round's newer attempt overwrites it.
func TestALateSuccessWithNoDropIsTheOnlyEvidence(t *testing.T) {
	link := fastProbeTarget(t).URL + "/generate_204"
	history := urltest.NewHistoryStorage()
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{}}

	stale := newHeldProbeOutbound("node-a", slowProbeTarget(t).Listener.Addr().String())
	round := make(chan map[string]uint16, 1)
	go func() {
		round <- URLTestOutboundsWithTarget(context.Background(), manager, history,
			log.NewNOPFactory().NewLogger("stale-keep"), []adapter.Outbound{stale}, link,
			urltest.ExpectedStatus{}, 0, true, TestHistoryHealth)
	}()
	select {
	case <-stale.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the stale attempt never dialled")
	}
	stale.release()
	select {
	case <-round:
	case <-time.After(10 * time.Second):
		t.Fatal("the stale attempt never finished")
	}
	require.Equal(t, 1, history.HealthEntryCount(),
		"with no newer evidence and no drop, the result is the only evidence there is")
	require.NotNil(t, history.LoadURLTestHistory("node-a"))

	// The next round's newer attempt replaces it; ordering is by attempt start, so the older entry
	// cannot win back.
	fresh := URLTestOutboundsWithTarget(context.Background(), manager, history,
		log.NewNOPFactory().NewLogger("stale-keep"), []adapter.Outbound{
			&directProbeOutbound{tag: "node-a"},
		}, link, urltest.ExpectedStatus{}, 0, true, TestHistoryHealth)
	require.Len(t, fresh, 1)
	stored := history.LoadURLTestHistory("node-a")
	require.NotNil(t, stored)
	require.Less(t, stored.Delay, uint16(100),
		"the newer attempt (the fast target) is the stored evidence")
}

// TestANewAttemptAfterAnEvidenceDropIsStored is the other half of state 3: the guard must not make
// a drop permanent. An attempt that starts AFTER the drop captures the new epoch and stores.
func TestANewAttemptAfterAnEvidenceDropIsStored(t *testing.T) {
	link := fastProbeTarget(t).URL + "/generate_204"
	history := urltest.NewHistoryStorage()
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{}}

	history.DeleteURLTestHistory("node-a") // no entry to remove, but it is still a drop
	result := URLTestOutboundsWithTarget(context.Background(), manager, history,
		log.NewNOPFactory().NewLogger("after-drop"), []adapter.Outbound{
			&directProbeOutbound{tag: "node-a"},
		}, link, urltest.ExpectedStatus{}, 0, true, TestHistoryHealth)
	require.Len(t, result, 1)
	require.Equal(t, 1, history.HealthEntryCount(),
		"an attempt that began after the drop belongs to the new evidence epoch")
}
